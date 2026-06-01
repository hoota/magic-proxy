package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

var proxyAddrs = []string{
	"127.0.0.1:9999",
	"127.0.0.1:9998",
	"127.0.0.1:9997",
}

func main() {
	total := 100
	concurrency := 20
	targetURL := "https://postman-echo.com/get"

	if v := os.Getenv("N"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			total = n
		}
	}
	if v := os.Getenv("C"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			concurrency = n
		}
	}
	if v := os.Getenv("URL"); v != "" {
		targetURL = v
	}

	clients := make([]*http.Client, len(proxyAddrs))
	for i, addr := range proxyAddrs {
		dialer, err := proxy.SOCKS5("tcp", addr, nil, &net.Dialer{
			Timeout: 10 * time.Second,
		})
		if err != nil {
			log.Fatalf("socks5 dialer %s: %v", addr, err)
		}
		clients[i] = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				Dial: func(network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				},
				TLSClientConfig:     &tls.Config{},
				TLSHandshakeTimeout: 10 * time.Second,
			},
		}
	}

	log.Printf("load test: %d requests, %d concurrent, %d proxies", total, concurrency, len(clients))

	var ok, fail atomic.Int64
	var perProxy []atomic.Int64
	for range proxyAddrs {
		perProxy = append(perProxy, atomic.Int64{})
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	start := time.Now()

	for i := 0; i < total; i++ {
		wg.Add(1)
		sem <- struct{}{}

		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()

			ci := rand.Intn(len(clients))
			uuid := randomUUID()
			url := fmt.Sprintf("%s?x=%s", targetURL, uuid)

			resp, err := clients[ci].Get(url)
			if err != nil {
				log.Printf("[%d] ERROR (proxy %s): %v", idx, proxyAddrs[ci], err)
				fail.Add(1)
				return
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("[%d] read body: %v", idx, err)
				fail.Add(1)
				return
			}

			if resp.StatusCode != 200 {
				log.Printf("[%d] status %d: %s", idx, resp.StatusCode, string(body[:min(len(body), 200)]))
				fail.Add(1)
				return
			}

			if !strings.Contains(string(body), uuid) {
				log.Printf("[%d] uuid not found in response: %s", idx, string(body[:min(len(body), 300)]))
				fail.Add(1)
				return
			}

			ok.Add(1)
			perProxy[ci].Add(1)
			if (idx+1)%10 == 0 {
				elapsed := time.Since(start)
				log.Printf("[%d/%d] ok=%d fail=%d (%.1f req/s)", idx+1, total, ok.Load(), fail.Load(), float64(ok.Load()+fail.Load())/elapsed.Seconds())
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	log.Printf("result: %d ok, %d fail, %v elapsed (%.1f req/s)",
		ok.Load(), fail.Load(), elapsed.Round(time.Millisecond), float64(total)/elapsed.Seconds())

	for i, addr := range proxyAddrs {
		log.Printf("  %s: %d requests", addr, perProxy[i].Load())
	}

	if fail.Load() > 0 {
		os.Exit(1)
	}
}

func randomUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%08x%04x%04x%04x%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
