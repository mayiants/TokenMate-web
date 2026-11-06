# AI Portal Go

Admin portal and API key management system for the AI Proxy platform, written in Go. Replaces the Node.js `ai-proxy-portal` with enhanced enterprise features, i18n, and billing.

## Features

- **Multi-tenant Dashboard** — User, Enterprise, Super Admin, and System Admin role-based views
- **API Key Management** — Create, manage, and route API keys with group-based access control
- **Rate Limiting** — TPW/RPW/RP5H enforcement with per-key, per-group, and subscription-tier policies
- **Billing & Invoices** — Usage-based billing, subscription plans, recharge records, invoice generation
- **Enterprise Management** — Enterprise creation, member invitations, quota enforcement, usage stats
- **Provider & Model Management** — CRUD for AI providers and models with field-alignment validation
- **i18n (English-default)** — Bilingual EN/ZH with runtime proper-noun glossary protection
- **Audit Logging** — GDPR-compliant audit trails with retention policies
- **Admin Reconciliation** — Invoice review, balance audit, and transaction reconciliation

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    ai-portal-go (This Project)               │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Web Frontend (HTMX + Alpine.js + Tailwind CSS)       │   │
│  │ - Admin Dashboard, Billing, API Keys, Enterprise      │   │
│  └─────────────────────────────────────────────────────┘   │
│                              │                              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Go Backend (Gin + GORM)                              │   │
│  │ - JWT Authentication & RBAC                          │   │
│  │ - CRUD APIs for users, keys, groups, providers       │   │
│  │ - Billing engine, rate limit enforcement             │   │
│  │ - Webhook notifications to Proxy                     │   │
│  └─────────────────────────────────────────────────────┘   │
│                              │                              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Data Layer                                           │   │
│  │ - PostgreSQL (primary data store)                    │   │
│  │ - Redis (caching, rate limits, session store)        │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                               │
                               │ Webhook / API Sync
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                    ai-proxy-go (Proxy)                       │
│                    Request routing, format conversion        │
└─────────────────────────────────────────────────────────────┘
```

## Quick Start

### Prerequisites

- Go 1.24+
- PostgreSQL 15+
- Redis 7+
- Node.js 20+ (for Tailwind CSS rebuild)

### Local Development

```bash
# Clone and enter directory
cd ai-portal-go

# Copy environment template and configure
cp .env.template .env
# Edit .env with your database credentials and secrets

# Build
go build -o portal ./cmd/server

# Rebuild Tailwind CSS (required if modifying templates)
bash scripts/rebuild-tailwind.sh

# Run
./portal
```

### Docker

```bash
# Build and start full stack (Portal + Postgres + Redis)
docker compose -f docker-compose.portal.yml up -d --build

# Or build image only
docker build -t ai-portal-go:latest .

# Run standalone
docker run -p 3001:3001 \
  -e DB_HOST=postgres \
  -e DB_PORT=5432 \
  -e DB_NAME=ai_proxy_portal \
  -e DB_USER=postgres \
  -e DB_PASSWORD=password \
  -e REDIS_HOST=redis \
  -e JWT_SECRET=your-32-char-secret \
  ai-portal-go:latest
```

### One-click Start Script

```bash
# Interactive mode
./start.sh

# Start specific components
./start.sh portal       # Portal only
./start.sh mvp          # Portal + Proxy (full stack)
./start.sh simulate     # Simulation mode
./start.sh stop         # Stop all services
./start.sh status       # Check service health
```

## Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | 3001 |
| `DB_HOST` | PostgreSQL host | localhost |
| `DB_PORT` | PostgreSQL port | 5432 |
| `DB_NAME` | Database name | ai_proxy_portal |
| `DB_USER` | Database user | postgres |
| `DB_PASSWORD` | Database password | (required) |
| `DB_SSLMODE` | PostgreSQL SSL mode (`disable`/`require`/`verify-ca`/`verify-full`) | disable |
| `DB_SSL_ROOT_CERT` | Path to PostgreSQL CA certificate | (optional) |
| `REDIS_HOST` | Redis host | localhost |
| `REDIS_PORT` | Redis port | 6379 |
| `REDIS_PASSWORD` | Redis password | (optional) |
| `REDIS_DB` | Redis database index | 0 |
| `REDIS_TLS` | Enable Redis TLS | false |
| `REDIS_TLS_SKIP_VERIFY` | Skip Redis TLS certificate verification | false |
| `JWT_SECRET` | JWT signing secret | (required, 32+ chars) |
| `ADMIN_MODE` | Admin mode (`production` / `development`) | production |
| `ADMIN_EMAIL` | Default admin email | admin@example.com |
| `ADMIN_PASSWORD` | Default admin password (production requires ≥12 chars) | — |
| `ADMIN_SECRET` | Secret for `/metrics` and admin sync endpoints | (required) |
| `PROXY_SYNC_SECRET` | Shared secret for Proxy webhook callbacks | (required) |
| `ENCRYPTION_KEY` | AES encryption key for secrets | (required) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OpenTelemetry OTLP collector endpoint | (optional) |
| `OTEL_TRACES_EXPORTER` | OpenTelemetry exporter type (`otlp` or `none`) | none |

## Development

```bash
# Run all checks
go vet ./...
gofmt -l .
python3 scripts/i18n_full_audit.py --strict
bash scripts/rebuild-tailwind.sh

# Run tests (no database required)
go test ./internal/middleware/... ./internal/web/... ./pkg/... ./tests/performance/... -v

# Run tests (database required — needs PostgreSQL)
go test ./internal/db/... ./internal/routes/... -v

# Run Playwright E2E tests
npx playwright test e2e/ui-smoke.spec.js --workers=1
```

## CI/CD

CI runs on every push/PR via GitHub Actions (`.github/workflows/ci-portal.yml`):

- Go build + `go vet` + `gofmt` check
- Unit tests (no database required)
- i18n full audit (`--strict` mode)
- Tailwind CSS rebuild verification

See `docs/cicd-plan-202606180841.md` for the full CI/CD roadmap.

## Documentation

See `docs/` for design documents, implementation plans, audit reports, and test results. Key docs:

| Document | Description |
|----------|-------------|
| `docs/cicd-plan-202606180841.md` | CI/CD implementation plan |
| `docs/project-comprehensive-review-202606132130.md` | Comprehensive project review |
| `docs/web-frontend-architecture-design-v3-202605040636.md` | Frontend architecture |
| `CLAUDE.md` | Naming conventions and code style |

## Related Projects

| Project | Description |
|---------|-------------|
| `ai-proxy-go` | Go API proxy gateway (request routing, format conversion) |
| `tokenrouter` | Token-based routing research |

## License

MIT
