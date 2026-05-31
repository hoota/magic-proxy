# SOCKS5-WS-Proxy

SOCKS5-прокси поверх WebSocket через HTTPS. Маскирует TCP-трафик под обычный HTTPS WebSocket-трафик.

```
[Браузер] → SOCKS5 → [socks5-client :9999] → WSS → [Nginx :443] → [ws-proxy-server :8080] → Интернет
```

## Архитектура

- **socks5-client** — слушает SOCKS5 на `127.0.0.1:9999`, мультиплексирует все соединения в **одно** WebSocket-соединение и отправляет на удалённый сервер через HTTPS.
- **ws-proxy-server** — принимает WebSocket-соединения, извлекает целевые адреса, устанавливает TCP-соединения и пересылает данные.
- **Nginx** — терминирует TLS, проксирует WebSocket на бэкенд.

### Мультиплексирование

Все SOCKS5-соединения мультиплексируются через одно WebSocket-соединение. Каждый WebSocket binary frame содержит:

```
[4 байта: session_id, big-endian]
[1 байт:  тип сообщения]
  0x01 = OPEN   (client→server: payload = целевой адрес + порт)
  0x02 = STATUS (server→client: payload = 1 байт статус)
  0x03 = DATA   (bidirectional: payload = raw данные)
  0x04 = CLOSE  (bidirectional: нет payload)
```

## Сборка

Требуется Go 1.21+.

```bash
make build-all
```

Бинарники:
- `bin/socks5-client` — локальный клиент (macOS / Linux)
- `bin/ws-proxy-server` — удалённый сервер (Linux)
- `bin/loadtest` — нагрузочный тест

Отдельные цели:

```bash
make build-client    # только клиент
make build-server    # только сервер
make clean           # удалить bin/
```

## Запуск

### Удалённый сервер (на Linux-сервере)

```bash
./ws-proxy-server --port 8080 --path /ws-proxy
```

| Параметр | Default | Описание |
|---|---|---|
| `--port` | 8080 | Порт для WebSocket (bind 127.0.0.1) |
| `--path` | /ws-proxy | URI endpoint |
| `--max-connections` | 100 | Максимум одновременных сессий |
| `--allowed-ports` | (все) | Разрешённые порты, через запятую: `80,443` |

Env-переменные: `WS_LISTEN_PORT`, `WS_PATH`, `MAX_CONNECTIONS`, `ALLOWED_PORTS`

### Локальный клиент (на вашей машине)

```bash
./socks5-client --port 9999 --server wss://example.com/ws-proxy
```

| Параметр | Default | Описание |
|---|---|---|
| `--port` | 9999 | Порт SOCKS5 (bind 127.0.0.1) |
| `--server` | (обязательный) | Полный URL WebSocket-сервера |
| `--insecure` | false | Пропустить проверку TLS-сертификата |

Env-переменные: `SOCKS5_PORT`, `WS_SERVER_URL`, `WS_INSECURE`

### Проверка

```bash
curl --socks5-hostname 127.0.0.1:9999 https://ifconfig.me
```

Должен вернуться IP удалённого сервера.

## Nginx

Конфигурация для production (Let's Encrypt):

```nginx
server {
    listen 443 ssl;
    server_name example.com;

    ssl_certificate     /etc/letsencrypt/live/example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;

    location /ws-proxy {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

## Нагрузочный тест

```bash
make build-loadtest
./bin/loadtest
```

Параметры через env-переменные:

| Env | Default | Описание |
|---|---|---|
| `N` | 100 | Количество запросов |
| `C` | 20 | Конкурентность |
| `PROXY` | 127.0.0.1:9999 | Адрес SOCKS5 прокси |
| `URL` | postman-echo.com/get | Target URL |

```bash
N=500 C=50 ./bin/loadtest
```

## Локальное тестирование

```bash
make certs           # сгенерировать self-signed сертификаты
make test-nginx      # запустить nginx на порту 40443
make stop-nginx      # остановить

# Запустить компоненты
./bin/ws-proxy-server --port 8080 --path /ws-proxy &
./bin/socks5-client --port 9999 --server wss://localhost:40443/ws-proxy --insecure &

# Тест
curl --socks5-hostname 127.0.0.1:9999 https://httpbin.org/ip
```

## Зависимости

- Go 1.21+
- [nhooyr.io/websocket](https://pkg.go.dev/nhooyr.io/websocket) — WebSocket
- [golang.org/x/net](https://pkg.go.dev/golang.org/x/net) — SOCKS5 dialer (только для loadtest)
- nginx (для TLS termination)

## Лицензия

MIT
