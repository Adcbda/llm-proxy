.PHONY: build test test-go test-ui test-e2e dev-backend dev-ui clean

build:
	cd ui && npm ci && npm run build
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/llm-proxy ./cmd/llm-proxy

test: test-go test-ui

test-go:
	go test ./...

test-ui:
	cd ui && npm test && npm run build

test-e2e:
	cd ui && npm run test:e2e

dev-backend:
	go run ./cmd/llm-proxy

dev-ui:
	cd ui && npm run dev

clean:
	go clean
