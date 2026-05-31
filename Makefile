.PHONY: build-client build-server build-all clean certs test-nginx stop-nginx

build-client:
	CGO_ENABLED=0 go build -o bin/socks5-client ./cmd/client

build-server:
	CGO_ENABLED=0 go build -o bin/ws-proxy-server ./cmd/server

build-all: build-client build-server

clean:
	rm -rf bin/

certs:
	@mkdir -p certs
	openssl req -x509 -newkey rsa:2048 -keyout certs/key.pem -out certs/cert.pem \
		-days 365 -nodes -subj "/CN=localhost" 2>/dev/null
	@echo "certs generated in certs/"

test-nginx: certs
	nginx -c $(CURDIR)/nginx/test.conf -p $(CURDIR)/
	@echo "test nginx started on port 40443"

stop-nginx:
	nginx -c $(CURDIR)/nginx/test.conf -s stop 2>/dev/null || true
	@echo "test nginx stopped"
