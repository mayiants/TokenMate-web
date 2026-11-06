# Build stage
FROM golang:1.26-alpine AS builder

# Build arguments for network environment customization.
# Defaults use official mirrors; override via --build-arg for local/CI mirrors.
ARG APK_MIRROR=dl-cdn.alpinelinux.org
ARG GOPROXY=https://proxy.golang.org,direct

RUN sed -i "s|dl-cdn.alpinelinux.org|${APK_MIRROR}|g" /etc/apk/repositories

WORKDIR /app

ENV GOPROXY=${GOPROXY}

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev') \
              -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
              -X main.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')" \
    -o ai-proxy ./cmd/server

# Final stage
FROM alpine:3.19

# Build argument must be redeclared in this stage.
ARG APK_MIRROR=dl-cdn.alpinelinux.org

RUN sed -i "s|dl-cdn.alpinelinux.org|${APK_MIRROR}|g" /etc/apk/repositories

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata su-exec

# Create non-root user
RUN adduser -D -g '' appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/ai-proxy .
# Do NOT copy config.yaml - use environment variables instead
# This allows configuration via docker-compose/k8s env vars

# Entrypoint fixes volume permissions (named volumes are root-owned) then
# drops to the non-root user via su-exec.
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:8080/health || exit 1

# Run
ENTRYPOINT ["/docker-entrypoint.sh"]
