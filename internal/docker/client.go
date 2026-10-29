// Package docker читает контейнеры через Docker API (обычно через socket-proxy).
// Только GET: запись в Docker здесь не предусмотрена.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// Стандартные labels Compose — по ним группируем стеки.
	labelComposeProject = "com.docker.compose.project"
	labelComposeService = "com.docker.compose.service"
	// Свои сервисы DocPort помечаем этим label и не показываем на дашборде.
	labelSystem = "com.docport.system"
)

// Client — тонкая обёртка над HTTP к Docker Engine API.
type Client struct {
	http *http.Client
	base string // например http://proxy:2375
}

// NewClient берёт DOCKER_HOST:
//   - tcp://proxy:2375  — через socket-proxy (боевой вариант в compose)
//   - unix:///var/run/docker.sock — прямой сокет (локально / отладка)
func NewClient() *Client {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}

	transport := &http.Transport{}
	base := host

	switch {
	case strings.HasPrefix(host, "unix://"):
		sock := strings.TrimPrefix(host, "unix://")
		// Для unix-сокета http.Client всё равно хочет URL со схемой http.
		base = "http://docker"
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}
	case strings.HasPrefix(host, "tcp://"):
		base = "http://" + strings.TrimPrefix(host, "tcp://")
	default:
		base = host
	}

	return &Client{
		http: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
		base: strings.TrimRight(base, "/"),
	}
}

// Сырые структуры ответа Docker API (поля как в /containers/json).
type apiContainer struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Created int64             `json:"Created"`
	Labels  map[string]string `json:"Labels"`
	Ports   []apiPort         `json:"Ports"`
}

type apiPort struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

// Port — только проброшенный порт (PublicPort != 0).
type Port struct {
	HostIP        string
	HostPort      int
	ContainerPort int
	Protocol      string
}

// Container — нормализованные данные для UI.
type Container struct {
	ID string
	Name string
	// Image — короткое имя для карточки; ImageFull — как отдал Docker (для title).
	Image     string
	ImageFull string
	State     string
	Status    string
	Health    string // healthy | unhealthy | starting | ""
	// Uptime — короткий аптайм/давность из Status Docker ("Up 2 hours" → "2h"),
	// не дата создания образа и не Created контейнера.
	Uptime  string
	Created time.Time
	Service string // имя сервиса Compose, если есть
	Ports   []Port
	Running bool
	System  bool // true → скрыть с дашборда
}

// Stack — карточка: либо compose-проект, либо один standalone-контейнер.
type Stack struct {
	Name       string
	Kind       string // compose | standalone
	Containers []Container
	Running    int
	Total      int
	Active     bool // есть хотя бы один running
}

// Overview — снимок для страницы: секции раздельно, чтобы не смешивались.
type Overview struct {
	Compose    []Stack
	Standalone []Stack
	FetchedAt  time.Time
	Error      string
}

// Overview собирает карточки из all=true (включая stopped/exited).
func (c *Client) Overview(ctx context.Context) Overview {
	out := Overview{FetchedAt: time.Now().UTC()}
	list, err := c.listContainers(ctx)
	if err != nil {
		out.Error = err.Error()
		return out
	}

	composeMap := map[string]*Stack{}
	var standalone []Container

	for _, raw := range list {
		ct := toContainer(raw)
		if ct.System {
			continue
		}
		if project := raw.Labels[labelComposeProject]; project != "" {
			st, ok := composeMap[project]
			if !ok {
				st = &Stack{Name: project, Kind: "compose"}
				composeMap[project] = st
			}
			st.Containers = append(st.Containers, ct)
			st.Total++
			if ct.Running {
				st.Running++
			}
			continue
		}
		// docker run и прочее без compose-label → отдельная плитка.
		standalone = append(standalone, ct)
	}

	for _, st := range composeMap {
		st.Active = st.Running > 0
		sortContainers(st.Containers)
		out.Compose = append(out.Compose, *st)
	}
	sortStacks(out.Compose)

	for _, ct := range standalone {
		st := Stack{
			Name:       ct.Name,
			Kind:       "standalone",
			Containers: []Container{ct},
			Total:      1,
			Running:    0,
			Active:     ct.Running,
		}
		if ct.Running {
			st.Running = 1
		}
		out.Standalone = append(out.Standalone, st)
	}
	sortStacks(out.Standalone)

	return out
}

// listContainers: all=true — нужны и неактивные стеки.
func (c *Client) listContainers(ctx context.Context) ([]apiContainer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/containers/json?all=true", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker request: %w", err)
	}
	defer res.Body.Close()
	// Лимит на ответ, чтобы случайно большой JSON не съел память.
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker api %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var list []apiContainer
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func toContainer(raw apiContainer) Container {
	name := ""
	if len(raw.Names) > 0 {
		// Docker отдаёт Names с ведущим "/".
		name = strings.TrimPrefix(raw.Names[0], "/")
	}
	ports := make([]Port, 0, len(raw.Ports))
	seen := map[string]struct{}{}
	for _, p := range raw.Ports {
		// PublicPort == 0 → порт только внутри сети контейнера, не проброшен на хост.
		if p.PublicPort == 0 {
			continue
		}
		// IPv4 и IPv6 биндинги одного порта схлопываем в одну строку UI.
		key := fmt.Sprintf("%d:%d/%s", p.PublicPort, p.PrivatePort, p.Type)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		hostIP := p.IP
		if hostIP == "" || hostIP == "0.0.0.0" || hostIP == "::" || hostIP == ":::" {
			hostIP = "0.0.0.0"
		}
		ports = append(ports, Port{
			HostIP:        hostIP,
			HostPort:      p.PublicPort,
			ContainerPort: p.PrivatePort,
			Protocol:      p.Type,
		})
	}
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].HostPort != ports[j].HostPort {
			return ports[i].HostPort < ports[j].HostPort
		}
		return ports[i].ContainerPort < ports[j].ContainerPort
	})

	state := strings.ToLower(raw.State)
	return Container{
		ID:        raw.ID,
		Name:      name,
		Image:     shortImage(raw.Image),
		ImageFull: raw.Image,
		State:     state,
		Status:    raw.Status,
		Health:    parseHealth(raw.Status),
		Uptime:    parseUptime(raw.Status),
		Created:   time.Unix(raw.Created, 0).UTC(),
		Service:   raw.Labels[labelComposeService],
		Ports:     ports,
		Running:   state == "running",
		System:    raw.Labels[labelSystem] == "true",
	}
}

// Health в list API часто только в Status ("Up 2 hours (healthy)") — отдельный inspect не делаем.
func parseHealth(status string) string {
	s := strings.ToLower(status)
	switch {
	case strings.Contains(s, "(healthy)"):
		return "healthy"
	case strings.Contains(s, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(s, "(health: starting)"):
		return "starting"
	default:
		return ""
	}
}

// Сначала running, потом по имени сервиса/контейнера.
// Сначала контейнеры с проброшенными на хост портами, потом running, потом по имени.
func sortContainers(list []Container) {
	sort.Slice(list, func(i, j int) bool {
		pi, pj := len(list[i].Ports) > 0, len(list[j].Ports) > 0
		if pi != pj {
			return pi
		}
		if list[i].Running != list[j].Running {
			return list[i].Running
		}
		a := list[i].Service
		if a == "" {
			a = list[i].Name
		}
		b := list[j].Service
		if b == "" {
			b = list[j].Name
		}
		return strings.ToLower(a) < strings.ToLower(b)
	})
}

// Активные стеки выше неактивных.
func sortStacks(list []Stack) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Active != list[j].Active {
			return list[i].Active
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
}

// shortImage укорачивает голые sha256 ID без тега (ломают вёрстку карточек).
// "sha256:9b6a7a155aff77..." → "9b6a7a155aff", обычные repo:tag не трогаем.
func shortImage(img string) string {
	img = strings.TrimSpace(img)
	if strings.HasPrefix(img, "sha256:") {
		id := strings.TrimPrefix(img, "sha256:")
		if len(id) > 12 {
			return id[:12]
		}
		return id
	}
	// Иногда Docker отдаёт 64-символьный hex без префикса.
	if len(img) >= 64 && isHexID(img[:64]) {
		return img[:12]
	}
	return img
}

func isHexID(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// parseUptime вытаскивает длительность из Status Docker:
// "Up 2 hours (healthy)" → "2h", "Exited (0) 3 days ago" → "3d".
func parseUptime(status string) string {
	s := strings.TrimSpace(status)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)

	if strings.HasPrefix(lower, "up ") {
		rest := strings.TrimSpace(s[3:])
		if i := strings.Index(rest, " ("); i >= 0 {
			rest = strings.TrimSpace(rest[:i])
		}
		return shortenDockerDuration(rest)
	}

	// "Exited (0) 3 days ago" / "Restarting (1) 10 seconds ago"
	if strings.HasSuffix(lower, " ago") {
		body := strings.TrimSpace(s[:len(s)-4])
		parts := strings.Fields(body)
		if len(parts) >= 2 {
			return shortenDockerDuration(parts[len(parts)-2] + " " + parts[len(parts)-1])
		}
	}
	return ""
}

func shortenDockerDuration(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(lower, "less than a second"):
		return "<1s"
	case strings.Contains(lower, "about a minute"):
		return "~1m"
	case strings.Contains(lower, "about an hour"):
		return "~1h"
	}

	fields := strings.Fields(lower)
	if len(fields) < 2 {
		return s
	}
	n, unit := fields[0], fields[1]
	if _, err := strconv.Atoi(n); err != nil {
		return s
	}
	switch {
	case strings.HasPrefix(unit, "second"):
		return n + "s"
	case strings.HasPrefix(unit, "minute"):
		return n + "m"
	case strings.HasPrefix(unit, "hour"):
		return n + "h"
	case strings.HasPrefix(unit, "day"):
		return n + "d"
	case strings.HasPrefix(unit, "week"):
		return n + "w"
	case strings.HasPrefix(unit, "month"):
		return n + "mo"
	case strings.HasPrefix(unit, "year"):
		return n + "y"
	default:
		return s
	}
}

// PortLinkScheme — одна ссылка для UI по эвристике порта (без TLS-пробы).
// "" = ссылку не показывать (udp / явно не HTTP).
func PortLinkScheme(hostPort int, protocol string) string {
	if strings.EqualFold(protocol, "udp") {
		return ""
	}
	switch hostPort {
	case 443, 8443, 9443, 4443, 10443:
		return "https"
	case 22, 23, 25, 53, 110, 143, 445, 465, 587, 993, 995,
		1433, 1521, 2049, 3306, 5432, 5672, 6379, 11211, 27017:
		return ""
	default:
		// Остальное чаще веб/метрики по HTTP; TLS на кастомном порту без пробы не угадать.
		return "http"
	}
}

// FormatAge — возраст Created для UI: 12s / 5m / 3h / 2d / 3w / 6mo / 1y.
func FormatAge(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	case d < 14*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	case d < 60*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/(24*7))) + "w"
	case d < 365*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/(24*30))) + "mo"
	default:
		y := int(d.Hours() / (24 * 365))
		if y < 1 {
			y = 1
		}
		return strconv.Itoa(y) + "y"
	}
}
