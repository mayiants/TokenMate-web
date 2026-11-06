# AI Portal Go Project Documentation

## Naming Conventions

### Document Files

**Format:** `name-YYYYMMDDHHMM.md`

**Examples:**
- `portal-rewrite-plan-202605021900.md`
- `api-compatibility-202605031030.md`
- `deployment-guide-202605041415.md`

**Rules:**
1. Use lowercase with hyphens for name
2. Include timestamp (year-month-day-hour-minute)
3. Timestamp reflects document creation time
4. Update documents by creating new versions (don't modify old dated docs)

**Document Types (must use timestamp suffix):**
- `*-design-YYYYMMDDHHMM.md` - Architecture and design documents
- `*-test-YYYYMMDDHHMM.md` - Test plans and test results
- `*-analysis-YYYYMMDDHHMM.md` - Code analysis and investigation reports
- `*-report-YYYYMMDDHHMM.md` - Completion reports, reviews, audits
- `*-summary-YYYYMMDDHHMM.md` - Summaries and status updates
- `*-plan-YYYYMMDDHHMM.md` - Implementation plans
- `*-proposal-YYYYMMDDHHMM.md` - Feature proposals

### Document Status Markers

Add these headers to documents:

- **OUTDATED**: Document superseded by newer version
- **HISTORICAL**: Kept for reference only (reports, reviews, audits)
- **DRAFT**: Work in progress
- **ACTIVE**: Current and maintained

Example:
```markdown
> **⚠️ OUTDATED** - This document has been superseded by xxx-v2.md
```

### Directory Structure

```
docs/
├── *-design-YYYYMMDDHHMM.md    # Design documents
├── *-plan-YYYYMMDDHHMM.md      # Implementation plans
├── *-analysis-YYYYMMDDHHMM.md  # Analysis documents
├── *-proposal-YYYYMMDDHHMM.md  # Feature proposals
└── reports/                    # Test results, benchmarks, completion reports
    ├── *-report-YYYYMMDDHHMM.md
    ├── *-test-YYYYMMDDHHMM.md
    └── *-summary-YYYYMMDDHHMM.md
```

## Code Style

- Go: Standard Go formatting, `gofmt`, `go vet`
- Use `internal/` for private packages
- Use `pkg/` for public packages
- Comments: English, explain WHY not WHAT
- Commit messages: Conventional commits format

## Single-Track Rule (mutations live in the API layer only)

All mutating business operations MUST have a single implementation under
`internal/routes/` (JSON API). `internal/web/` may only render pages and set
cookies/session; any SQL in `internal/web/` handlers should be rejected in
review. Legacy form-POST endpoints were removed in favor of fetch()/API calls.

## Build & Test

```bash
# Build
go build -o portal ./cmd/server

# Test
go test ./... -v

# Run
./portal

# Docker
docker build -t ai-portal-go .
docker run -p 3001:3001 ai-portal-go
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | 3001 |
| `DB_HOST` | PostgreSQL host | localhost |
| `DB_PORT` | PostgreSQL port | 5432 |
| `DB_NAME` | Database name | ai_proxy_portal |
| `DB_USER` | Database user | postgres |
| `DB_PASSWORD` | Database password | (required) |
| `REDIS_HOST` | Redis host | localhost |
| `REDIS_PORT` | Redis port | 6379 |
| `REDIS_PASSWORD` | Redis password | (optional) |
| `JWT_SECRET` | JWT signing secret | (required, 32+ chars) |
| `ADMIN_MODE` | Admin mode | production |
| `ADMIN_EMAIL` | Default admin email | admin@example.com |
| `ADMIN_PASSWORD` | Default admin password (production requires ≥12 chars) | — |
| `ADMIN_SECRET` | Secret for `/metrics` and admin sync endpoints | (required) |
| `PROXY_SYNC_SECRET` | Shared secret for Proxy webhook callbacks | (required) |
| `ENCRYPTION_KEY` | AES encryption key for secrets | (required) |

## Rebuild Requirements

**重要：修改代码后必须重建才能生效！**

### Go 后端修改
```bash
# 本地构建
go build -o portal ./cmd/server

# Docker 环境
docker compose down portal && docker compose up -d portal --build
```

### 前端修改 (dist/)
```bash
# 前端由 ai-portal-go/web 目录下的模板和静态资源组成，修改后重启服务即可
# Tailwind CSS 重新构建（如需修改样式）
bash scripts/rebuild-tailwind.sh
```

### 数据库迁移
```bash
# 自动建表：启动时自动创建缺失的表和索引
# 手动迁移：连接数据库执行 SQL
docker exec -it ai-proxy-postgres-dev psql -U postgres -d ai_proxy_portal
```

## Architecture

```
ai-portal-go/
├── cmd/server/             # Entry point, DB init, route registration
├── internal/
│   ├── config/             # Config loading from env
│   ├── db/                 # PostgreSQL + Redis connections, callbacks
│   ├── middleware/         # JWT auth, role-based access, request logging
│   ├── query/              # Dashboard/admin query helpers
│   ├── routes/             # HTTP handlers (auth, apikeys, webhook, admin, billing)
│   ├── services/           # Billing, pricing, exchange rates
│   └── web/                # Server-rendered pages, i18n, static assets
├── pkg/
│   ├── audit/              # Audit logging
│   ├── crypto/             # Key hashing / encryption
│   ├── i18n/               # Internationalization
│   ├── jwt/                # JWT generation/verification
│   ├── mail/               # Email sending
│   ├── metrics/            # Prometheus metrics (github.com/prometheus/client_golang)
│   ├── payment/            # Payment gateway abstractions
│   ├── queue/              # Redis-backed job queue
│   ├── telemetry/          # OpenTelemetry tracing
│   └── validation/         # Input validation helpers
└── migrations/             # SQL migration files
```

## API Compatibility

Go Portal 必须保持与 Node.js Portal 完全相同的 API 接口：

- Proxy 无需改动
- 前端无需改动
- 数据库 schema 兼容

## Related Projects

| Project | Path | Description |
|---------|------|-------------|
| ai-proxy-main | ../ai-proxy-main/ | Node.js Portal + Proxy (reference) |
| ai-proxy-go | ../ai-proxy-go/ | Go Proxy |
| new-api | ../new-api/ | Open source reference |

## Git Ignore

Build artifacts are ignored:
- `portal` (binary)
- `server` (binary)
- `*.log`
- `.env`
