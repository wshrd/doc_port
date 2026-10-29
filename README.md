# DocPort

Лёгкий read-only дашборд Docker-стеков: карточки Compose и standalone-контейнеров, проброшенные порты, статусы, темы light/dark, EN/RU.

## Запуск

```bash
docker compose up -d --build
```

Открыть: `http://HOST:6969`  
Порт снаружи задаётся в `docker-compose.yml` (`6969:80`).

## Что внутри

| Сервис | Роль |
|--------|------|
| `web` | Go-приложение (SSR), слушает `:80` в контейнере |
| `proxy` | `tecnativa/docker-socket-proxy` — только чтение Docker API |

Веб **не** монтирует Docker socket напрямую. Контейнеры DocPort скрыты с дашборда (`com.docport.system=true`).

## Обновление

```bash
docker compose up -d --build
```
