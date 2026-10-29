// DocPort — лёгкий read-only дашборд Docker-стеков.
// Отдаёт HTML (SSR), без JSON API: браузер не ходит в Docker напрямую.
package main

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"docport/internal/docker"
)

// Шаблоны и статика вшиты в бинарник — образ на scratch без файловой системы.
//
//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// pageData — данные для полной страницы и HTML-фрагмента карточек.
type pageData struct {
	Overview docker.Overview
	Now      time.Time
	Partial  bool
	// HostHint — hostname из запроса (без порта), для ссылок http(s)://host:publishedPort.
	HostHint string
}

// stackData нужен, потому что {{template "x" .}} в Go подменяет $ на переданный аргумент.
// Без обёртки внутри карточки нельзя надёжно взять Now/HostHint с корневой страницы.
type stackData struct {
	Stack    docker.Stack
	Now      time.Time
	HostHint string
}

func main() {
	// В контейнере слушаем :80; наружный порт задаётся в compose.
	addr := env("LISTEN_ADDR", ":80")
	client := docker.NewClient()

	funcs := template.FuncMap{
		"age":        docker.FormatAge,
		"portScheme": docker.PortLinkScheme,
		"fmtTime": func(t time.Time) string {
			return t.Format("2006-01-02 15:04:05 UTC")
		},
		"sub": func(a, b int) int { return a - b },
		"withStack": func(s docker.Stack, now time.Time, host string) stackData {
			return stackData{Stack: s, Now: now, HostHint: host}
		},
	}
	tmpl, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Полная страница: каркас + первая отрисовка карточек.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		render(w, tmpl, "index.html", pageData{
			Overview: client.Overview(r.Context()),
			Now:      time.Now().UTC(),
			Partial:  false,
			HostHint: hostHint(r),
		})
	})

	// HTML-фрагмент карточек для автообновления без JSON и без перезагрузки всей страницы.
	mux.HandleFunc("/cards", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		render(w, tmpl, "cards.html", pageData{
			Overview: client.Overview(r.Context()),
			Now:      time.Now().UTC(),
			Partial:  true,
			HostHint: hostHint(r),
		})
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("docport listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// Корректное завершение по SIGTERM (docker stop) / Ctrl+C.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func render(w http.ResponseWriter, tmpl *template.Template, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Не вызываем http.Error после начала записи: шаблон мог уже частично уйти в ответ.
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

// Базовые заголовки: только свои ресурсы, без iframe, без кэша чувствительных данных.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Host приходит как "host:uiPort" — для ссылок на сервисы нужен только host,
// иначе получится http://localhost:9090:18080.
func hostHint(r *http.Request) string {
	h := r.Host
	if h == "" {
		return "localhost"
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	// Host без порта (или IPv6 без скобок)
	return strings.Trim(h, "[]")
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
