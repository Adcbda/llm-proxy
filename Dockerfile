FROM node:24-alpine AS ui-builder
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:1.22-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY webui/embed.go ./webui/embed.go
COPY --from=ui-builder /src/webui/dist/ ./webui/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/llm-proxy ./cmd/llm-proxy

FROM alpine:3.22
RUN addgroup -S llmproxy && adduser -S -G llmproxy llmproxy && mkdir -p /data && chown llmproxy:llmproxy /data
COPY --from=go-builder /out/llm-proxy /usr/local/bin/llm-proxy
USER llmproxy
ENV LLMPROXY_LISTEN=0.0.0.0:8080 \
    LLMPROXY_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/llm-proxy"]

