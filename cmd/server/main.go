package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"socks5-ws-proxy/internal/wsserver"
)

func main() {
	listenPort := flag.Int("port", 8080, "port to listen for WebSocket connections from nginx (binds 127.0.0.1)")
	wsPath := flag.String("path", "/ws-proxy", "WebSocket endpoint URI")
	allowedPorts := flag.String("allowed-ports", "", "comma-separated list of allowed ports (empty = all)")
	maxConns := flag.Int("max-connections", 100, "max concurrent sessions")

	flag.Parse()

	if v := os.Getenv("WS_LISTEN_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			*listenPort = p
		}
	}
	if v := os.Getenv("WS_PATH"); v != "" {
		*wsPath = v
	}
	if v := os.Getenv("ALLOWED_PORTS"); v != "" && *allowedPorts == "" {
		*allowedPorts = v
	}
	if v := os.Getenv("MAX_CONNECTIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*maxConns = n
		}
	}

	var ports []int
	if *allowedPorts != "" {
		for _, s := range strings.Split(*allowedPorts, ",") {
			s = strings.TrimSpace(s)
			if p, err := strconv.Atoi(s); err == nil {
				ports = append(ports, p)
			}
		}
	}

	listenAddr := fmt.Sprintf("127.0.0.1:%d", *listenPort)

	srv := wsserver.New(wsserver.Config{
		ListenAddr:   listenAddr,
		WSPath:       *wsPath,
		AllowedPorts: ports,
		MaxConns:     *maxConns,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}()

	if err := srv.Start(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
