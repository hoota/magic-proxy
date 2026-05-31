package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"socks5-ws-proxy/internal/socks5"
	"socks5-ws-proxy/internal/wsclient"
)

func main() {
	listenPort := flag.Int("port", 9999, "port to listen for SOCKS5 connections (binds 127.0.0.1)")
	serverURL := flag.String("server", "", "full WebSocket URL of the remote server (e.g. wss://example.com/ws-proxy)")
	insecure := flag.Bool("insecure", false, "skip TLS certificate verification")

	flag.Parse()

	if *serverURL == "" {
		if v := os.Getenv("WS_SERVER_URL"); v != "" {
			*serverURL = v
		} else {
			fmt.Fprintln(os.Stderr, "error: --server flag or WS_SERVER_URL env is required")
			os.Exit(1)
		}
	}

	if v := os.Getenv("SOCKS5_PORT"); v != "" && !flag.Parsed() {
		if p, err := strconv.Atoi(v); err == nil {
			*listenPort = p
		}
	}

	if v := os.Getenv("WS_INSECURE"); v == "1" || v == "true" {
		*insecure = true
	}

	listenAddr := fmt.Sprintf("127.0.0.1:%d", *listenPort)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := wsclient.New(*serverURL, *insecure)
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("failed to connect to %s: %v", *serverURL, err)
	}
	defer client.Close()

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", listenAddr, err)
	}
	defer ln.Close()

	log.Printf("socks5-client listening on %s, proxying to %s", listenAddr, *serverURL)

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		cancel()
		ln.Close()
		client.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}

		go handleConn(conn, client)
	}
}

func handleConn(browserConn net.Conn, client *wsclient.Client) {
	addrType, addr, port, err := socks5.Handshake(browserConn)
	if err != nil {
		log.Printf("socks5 handshake failed: %v", err)
		socks5.SendReply(browserConn, socks5.RepGeneralFailure)
		return
	}

	log.Printf("new session: %s:%d (type=0x%02x)", addr, port, addrType)

	sid, err := client.OpenSession(browserConn, addrType, addr, port)
	if err != nil {
		log.Printf("open session failed for %s:%d: %v", addr, port, err)
		socks5.SendReply(browserConn, socks5.RepGeneralFailure)
		return
	}

	socks5.SendReply(browserConn, socks5.RepSuccess)

	client.StartRelay(sid, browserConn)
}
