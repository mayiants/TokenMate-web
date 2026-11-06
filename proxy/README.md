# AI Proxy Go

High-performance AI API gateway written in Go, designed to replace ai-proxy-singapore with enhanced features from new-api.

## Features

- **Token Estimation**: Local token counting before API calls (from new-api)
- **Channel Affinity**: Route similar requests to same channel for better cache hits
- **Format Conversion**: OpenAI ↔ Claude ↔ Gemini format conversion
- **Cache Token Billing**: Proper billing for cached tokens with discounts
- **7-Year Audit Retention**: Partitioned storage with hot/warm/cold tiers
- **40+ Provider Support**: Reuse adapters from new-api

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    ai-portal-go (Control Plane)             │
│                    Admin, Billing, Invoice, API Key Mgmt    │
└─────────────────────────────────────────────────────────────┘
                              │
                              │ HTTPS API / Webhooks
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    ai-proxy-go (This Project)               │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ API Gateway Layer                                    │   │
│  │ - JWT Authentication                                 │   │
│  │ - Rate Limiting                                      │   │
│  │ - Request Routing                                    │   │
│  └─────────────────────────────────────────────────────┘   │
│                              │                              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Smart Proxy Layer (from new-api)                     │   │
│  │ - Token Estimation                                   │   │
│  │ - Channel Affinity Cache                             │   │
│  │ - Format Conversion                                  │   │
│  │ - Pre-consume Billing                                │   │
│  └─────────────────────────────────────────────────────┘   │
│                              │                              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Provider Adapters (from new-api)                     │   │
│  │ - OpenAI, Claude, Gemini, DeepSeek, Qwen, etc.      │   │
│  └─────────────────────────────────────────────────────┘   │
│                              │                              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Audit Layer (7-year retention)                       │   │
│  │ - Hot: PostgreSQL (0-3 months)                      │   │
│  │ - Warm: PostgreSQL (3-12 months)                    │   │
│  │ - Cold: S3/Glacier (12-84 months)                   │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

## Quick Start

### Prerequisites

- Go 1.22+
- Redis 7+

> Current `main`: the Proxy runtime depends on Redis and Portal HTTP. It does **not** open a PostgreSQL connection; `AI_PROXY_DATABASE_*` / `DB_*` keys are legacy config fields and are not required to run the Proxy.

### Build

```bash
# Install dependencies
make deps

# Build
make build

# Run
./ai-proxy
```

### Docker

```bash
# Build image
docker build -t ai-proxy-go:latest .

# Run
docker run -p 8080:8080 \
  -e AI_PROXY_REDIS_HOST=redis \
  -e AI_PROXY_PORTAL_URL=http://portal:3001 \
  -e AI_PROXY_AUTH_JWT_SECRET=your-32-char-jwt-secret \
  -e AI_PROXY_ADMIN_SECRET=your-32-char-admin-secret \
  -e AI_PROXY_PORTAL_SYNC_SECRET=your-32-char-sync-secret \
  ai-proxy-go:latest
```

## Configuration

Configuration is loaded from `config.yaml` and can be overridden by environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `AI_PROXY_SERVER_PORT` | Server port | 8080 |
| `AI_PROXY_DATABASE_*` | Legacy/unused: current Proxy runtime does not connect to PostgreSQL | — |
| `AI_PROXY_REDIS_HOST` | Redis host | localhost |
| `AI_PROXY_REDIS_PORT` | Redis port | 6379 |
| `AI_PROXY_REDIS_TLS` | Enable Redis TLS | false |
| `AI_PROXY_REDIS_TLS_SKIP_VERIFY` | Skip Redis TLS certificate verification | false |
| `AI_PROXY_PORTAL_URL` | Portal API URL | http://localhost:3001 |
| `AI_PROXY_AUTH_JWT_SECRET` | JWT signing secret | (required) |
| `AI_PROXY_ADMIN_SECRET` | Admin/sync secret for `/admin/*` and Portal callbacks | (required) |
| `AI_PROXY_PORTAL_SYNC_SECRET` | Shared secret for Portal webhook callbacks | (required) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OpenTelemetry OTLP collector endpoint | (optional) |
| `OTEL_TRACES_EXPORTER` | OpenTelemetry exporter type (`otlp` or `none`) | none |

## API Endpoints

### Proxy Endpoints (Authentication Required)

```
POST /v1/chat/completions      - OpenAI-compatible chat
POST /v1/completions           - Legacy completions
POST /v1/embeddings            - Text embeddings
POST /v1/images/generations    - Image generation
POST /v1/audio/transcriptions  - Audio transcription
POST /v1/audio/translations    - Audio translation
POST /v1/messages              - Claude native endpoint
POST /v1/responses             - OpenAI Responses API
```

### Admin Endpoints (Admin Secret Required)

```
GET  /admin/stats              - System statistics
GET  /admin/cache/affinity     - Channel affinity stats
DELETE /admin/cache/affinity   - Clear affinity cache
GET  /admin/audit/search       - Search audit logs
```

### Health Check

```
GET /health                    - Health check endpoint
GET /metrics                   - Prometheus metrics
```

## Code Reuse from new-api

| Component | Source | Location |
|-----------|--------|----------|
| Token Counter | `new-api/service/token_counter.go` | `pkg/token/counter.go` |
| Channel Affinity | `new-api/service/channel_affinity.go` | `pkg/cache/affinity.go` |
| Format Conversion | `new-api/service/convert.go` | `pkg/convert/` |
| Billing | `new-api/service/quota.go` | `pkg/billing/billing.go` |
| Provider Adapters | `new-api/relay/channel/*` | `pkg/relay/channel/` |
| DTO Types | `new-api/dto/*` | `dto/` |

## Development

```bash
# Run in development mode
make dev

# Run tests
make test

# Lint
make lint

# Format code
make fmt

# Full check
make check
```

## Migration from Node.js

See [ai-proxy-go-design.md](../ai-proxy-go-design.md) for detailed migration plan.

## License

MIT
