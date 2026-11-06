# AI Proxy Go Project Documentation

## Naming Conventions

### Document Files

**Format:** `name-YYYYMMDDHHMM.md`

**Examples:**
- `proxy-design-202605011850.md`
- `provider-adapter-202605021030.md`
- `performance-test-202605031415.md`

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

### Directory Structure

```
docs/
├── *-design-YYYYMMDDHHMM.md    # Design documents
├── *-plan-YYYYMMDDHHMM.md      # Implementation plans
├── *-analysis-YYYYMMDDHHMM.md  # Analysis documents
└── reports/                    # Test results, benchmarks, completion reports
    ├── *-report-YYYYMMDDHHMM.md
    ├── *-test-YYYYMMDDHHMM.md
    └── *-summary-YYYYMMDDHHMM.md
```

## Code Style

- Go: Standard formatting with `gofmt`, `go vet`
- Use `internal/` for private packages
- Use `pkg/` for public packages
- Comments: Explain WHY, not WHAT

## Build & Test

```bash
# Build
go build -o ai-proxy-go ./cmd/server

# Test
go test ./... -v

# Run
./ai-proxy-go --config config.yaml
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `AI_PROXY_SERVER_PORT` | Server port | 8080 |
| `AI_PROXY_DATABASE_*` | Legacy/unused: current Proxy runtime does not connect to PostgreSQL | — |
| `AI_PROXY_REDIS_HOST` | Redis host | localhost |
| `AI_PROXY_REDIS_PORT` | Redis port | 6379 |
| `AI_PROXY_REDIS_TLS` | Enable Redis TLS | false |
| `AI_PROXY_REDIS_TLS_SKIP_VERIFY` | Skip Redis TLS verification | false |
| `AI_PROXY_PORTAL_URL` | Portal API URL | http://localhost:3001 |
| `AI_PROXY_AUTH_JWT_SECRET` | JWT signing secret | (required) |
| `AI_PROXY_ADMIN_SECRET` | Admin/sync secret | (required) |
| `AI_PROXY_PORTAL_SYNC_SECRET` | Portal webhook callback secret | (required) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OpenTelemetry OTLP endpoint | (optional) |
| `OTEL_TRACES_EXPORTER` | OpenTelemetry exporter type | none |

Build artifacts are ignored:
- `ai-proxy` (binary)
- `ai-proxy-go` (binary)
- `*.log`
- `config.yaml` (contains secrets)