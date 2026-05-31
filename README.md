# SOCKS5-WS-Proxy

A SOCKS5 proxy tunneled over WebSocket through HTTPS. Masks TCP traffic as regular HTTPS WebSocket traffic.

```
[Browser] → SOCKS5 → [socks5-client :9999] → WSS → [Nginx :443] → [ws-proxy-server :8080] → Internet
```

## Architecture

- **socks5-client** — listens for SOCKS5 on `127.0.0.1:9999`, multiplexes all connections into a **single** WebSocket connection, and forwards them to the remote server via HTTPS.
- **ws-proxy-server** — accepts WebSocket connections, extracts target addresses, establishes TCP connections, and relays data.
- **Nginx** — terminates TLS, proxies WebSocket to the backend.

### Multiplexing

All SOCKS5 connections are multiplexed over a single WebSocket connection. Each WebSocket binary frame contains:

```
[4 bytes: session_id, big-endian]
[1 byte:  message type]
  0x01 = OPEN   (client→server: payload = target address + port)
  0x02 = STATUS (server→client: payload = 1 byte status)
  0x03 = DATA   (bidirectional: payload = raw data)
  0x04 = CLOSE  (bidirectional: no payload)
```

## Build

Requires Go 1.21+.

```bash
make build-all
```

Binaries:
- `bin/socks5-client` — local client (macOS / Linux)
- `bin/ws-proxy-server` — remote server (Linux)
- `bin/loadtest` — load testing tool

Individual targets:

```bash
make build-client    # client only
make build-server    # server only
make clean           # remove bin/
```

## Usage

### Remote server (on your Linux server)

```bash
./ws-proxy-server --port 8080 --path /ws-proxy
```

| Flag | Default | Description |
|---|---|---|
| `--port` | 8080 | WebSocket listen port (binds 127.0.0.1) |
| `--path` | /ws-proxy | WebSocket endpoint URI |
| `--max-connections` | 100 | Max concurrent sessions |
| `--allowed-ports` | (all) | Allowed ports, comma-separated: `80,443` |

Environment variables: `WS_LISTEN_PORT`, `WS_PATH`, `MAX_CONNECTIONS`, `ALLOWED_PORTS`

### Local client (on your machine)

```bash
./socks5-client --port 9999 --server wss://example.com/ws-proxy
```

| Flag | Default | Description |
|---|---|---|
| `--port` | 9999 | SOCKS5 listen port (binds 127.0.0.1) |
| `--server` | (required) | Full WebSocket server URL |
| `--insecure` | false | Skip TLS certificate verification |

Environment variables: `SOCKS5_PORT`, `WS_SERVER_URL`, `WS_INSECURE`

### Verify

```bash
curl --socks5-hostname 127.0.0.1:9999 https://ifconfig.me
```

Should return the remote server's IP address.

## Nginx

Production configuration (Let's Encrypt):

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

## Load Testing

```bash
make build-loadtest
./bin/loadtest
```

Configuration via environment variables:

| Env | Default | Description |
|---|---|---|
| `N` | 100 | Number of requests |
| `C` | 20 | Concurrency |
| `PROXY` | 127.0.0.1:9999 | SOCKS5 proxy address |
| `URL` | postman-echo.com/get | Target URL |

```bash
N=500 C=50 ./bin/loadtest
```

## Local Testing

```bash
make certs           # generate self-signed certificates
make test-nginx      # start nginx on port 40443
make stop-nginx      # stop nginx

# Start components
./bin/ws-proxy-server --port 8080 --path /ws-proxy &
./bin/socks5-client --port 9999 --server wss://localhost:40443/ws-proxy --insecure &

# Test
curl --socks5-hostname 127.0.0.1:9999 https://httpbin.org/ip
```

## Dependencies

- Go 1.21+
- [nhooyr.io/websocket](https://pkg.go.dev/nhooyr.io/websocket) — WebSocket
- [golang.org/x/net](https://pkg.go.dev/golang.org/x/net) — SOCKS5 dialer (loadtest only)
- nginx (for TLS termination)

## License

MIT
