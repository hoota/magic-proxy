#!/bin/bash
set -eo pipefail

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT_DIR"

LOG_DIR="$ROOT_DIR/logs"
mkdir -p "$LOG_DIR"

SERVER_PID=""
CLIENT_PID1=""
CLIENT_PID2=""
CLIENT_PID3=""

cleanup() {
    echo ""
    echo "--- cleanup ---"
    [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
    [ -n "$CLIENT_PID1" ] && kill "$CLIENT_PID1" 2>/dev/null || true
    [ -n "$CLIENT_PID2" ] && kill "$CLIENT_PID2" 2>/dev/null || true
    [ -n "$CLIENT_PID3" ] && kill "$CLIENT_PID3" 2>/dev/null || true
    make stop-nginx 2>/dev/null || true
    echo "done"
}
trap cleanup EXIT

echo "--- pre-cleanup ---"
pkill -f "ws-proxy-server --port 44088" 2>/dev/null || true
pkill -f "socks5-client --port 999" 2>/dev/null || true
make stop-nginx 2>/dev/null || true
sleep 0.5

echo "--- building ---"
make build-all
go build -o bin/loadtest ./cmd/loadtest

echo "--- generating certs ---"
if [ ! -f certs/cert.pem ] || [ ! -f certs/key.pem ]; then
    make certs
else
    echo "certs already exist, skipping"
fi

echo "--- starting ws-proxy-server on :44088 ---"
./bin/ws-proxy-server --port 44088 --path /ws-proxy --log-level info > "$LOG_DIR/ws-proxy-server.log" 2>&1 &
SERVER_PID=$!
sleep 0.5

echo "--- starting nginx on :40443 ---"
make test-nginx > /dev/null 2>&1
sleep 0.5

echo "--- starting socks5-client on :9999, :9998, :9997 ---"
./bin/socks5-client --port 9999 --server wss://localhost:40443/ws-proxy --insecure --log-level info > "$LOG_DIR/socks5-client-9999.log" 2>&1 &
CLIENT_PID1=$!

./bin/socks5-client --port 9998 --server wss://localhost:40443/ws-proxy --insecure --log-level info > "$LOG_DIR/socks5-client-9998.log" 2>&1 &
CLIENT_PID2=$!

./bin/socks5-client --port 9997 --server wss://localhost:40443/ws-proxy --insecure --log-level info > "$LOG_DIR/socks5-client-9997.log" 2>&1 &
CLIENT_PID3=$!

sleep 1

echo "--- running loadtest ---"
echo ""
./bin/loadtest
EXIT_CODE=$?
echo ""

echo "--- logs ---"
echo "== ws-proxy-server =="
cat "$LOG_DIR/ws-proxy-server.log"
echo ""
for PORT in 9999 9998 9997; do
    echo "== socks5-client :$PORT =="
    cat "$LOG_DIR/socks5-client-$PORT.log"
    echo ""
done

exit $EXIT_CODE
