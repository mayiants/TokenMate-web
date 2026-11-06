# Provider Health Monitoring (6.6) — Implementation Report

## Overview
自动基于真实上游流量监控 Provider 健康状态，5 分钟窗口聚合，实时计算 healthy/degraded/unhealthy 状态。

## Architecture
```
Proxy handler (chat/embeddings/image/audio)
  ↓ recordProviderHealthAsync()
ProviderHealthBuffer (30s flush, 500 max)
  ↓ POST /api/webhook/provider-health-batch
Portal webhook → UPSERT provider_health_stats (5-min bucket)
  ↓
Admin API + Admin UI (/admin/provider-health)
```

## Database

### New Table: `provider_health_stats`
```sql
CREATE TABLE provider_health_stats (
    provider         VARCHAR(50)  NOT NULL,
    bucket           TIMESTAMP    NOT NULL,   -- 5-minute boundary
    request_count    INT DEFAULT 0,
    success_count    INT DEFAULT 0,
    failure_count    INT DEFAULT 0,
    timeout_count    INT DEFAULT 0,
    rate_limit_count INT DEFAULT 0,
    error_5xx_count  INT DEFAULT 0,
    error_4xx_count  INT DEFAULT 0,
    total_latency_ms BIGINT DEFAULT 0,
    avg_latency_ms   INT DEFAULT 0,
    error_rate       DECIMAL(5,4) DEFAULT 0,
    status           VARCHAR(20) DEFAULT 'unknown',
    PRIMARY KEY (provider, bucket)
);
```

### Status Thresholds (computed per bucket)
| Status | Condition |
|--------|-----------|
| healthy | error_rate < 5% AND avg_latency < 3000ms |
| degraded | error_rate < 20% AND avg_latency < 10000ms |
| unhealthy | error_rate >= 20% OR avg_latency >= 10000ms |
| unknown | request_count = 0 |

## Proxy Side (ai-proxy-go)

### Files Added/Modified
| File | Change |
|------|--------|
| `internal/server/provider_health_buffer.go` | **NEW** — 30s flush buffer, goroutine-based |
| `pkg/billing/billing.go` | Add `ProviderHealthRecord` struct + `SendProviderHealthBatchToPortal()` |
| `internal/server/handlers.go` | Add `recordProviderHealthAsync()` calls in all 4 request paths (chat stream/non-stream, embeddings, image, audio) |
| `cmd/server/main.go` | Wire `ProviderHealthBuffer` into deps |

### Recording Points (per handler)
- **Transport error** (timeout/connection refused) → `errorType: "timeout"`
- **Upstream 429** → `errorType: "rate_limit"`
- **Upstream 5xx** → `errorType: "error_5xx"`
- **Upstream 4xx** → `errorType: "error_4xx"`
- **Success (200)** → `errorType: ""`

## Portal Side (ai-portal-go)

### Files Added/Modified
| File | Change |
|------|--------|
| `migrations/20260516_provider_health_stats.sql` | **NEW** — create table + indexes |
| `internal/routes/webhook.go` | Add `handleProviderHealthBatch()` + `upsertProviderHealth()` with 5-min bucket UPSERT |
| `internal/routes/admin.go` | Add `listProviderHealth`, `getProviderHealthSummary`, `getProviderHealthDetail` |
| `internal/routes/models.go` | Fix `channel_health` JOIN bug (`channel_id` → `provider`) |
| `internal/web/data.go` | Add `GetAdminProviderHealthData()` |
| `internal/web/pages.go` | Add `handleAdminProviderHealth()` route |
| `internal/web/handler.go` | Register `admin-provider-health` template |
| `web/templates/admin-provider-health.html` | **NEW** — Summary cards + filterable table |
| `web/templates/layout.html` | Add sidebar nav "Provider 健康监控" |

### Admin APIs
| Endpoint | Description |
|----------|-------------|
| `GET /api/admin/provider-health` | List all providers with 15-min aggregated health |
| `GET /api/admin/provider-health/summary` | healthy/degraded/unhealthy/unknown counts |
| `GET /api/admin/provider-health/:provider` | Detail + 24h hourly history |

## Test Results

### Webhook Batch Test
```bash
curl -X POST /api/webhook/provider-health-batch \
  -d '{"records":[
    {"provider":"openai","statusCode":200,"latencyMs":450,"errorType":""},
    {"provider":"openai","statusCode":200,"latencyMs":520,"errorType":""},
    {"provider":"anthropic","statusCode":500,"latencyMs":3200,"errorType":"error_5xx"},
    {"provider":"anthropic","statusCode":429,"latencyMs":100,"errorType":"rate_limit"}
  ]}'
```
Result: `{"ok":true,"updated":4,"count":4}`

### DB Verification
```
 provider  | bucket              | request_count | success | failure | rate_limit | error_5xx | avg_latency | error_rate | status
-----------+---------------------+---------------+---------+---------+------------+-----------+-------------+------------+-----------
 anthropic | 2026-05-16 15:55:00 |             2 |       0 |       2 |          1 |         1 |        1650 |     1.0000 | unhealthy
 openai    | 2026-05-16 15:55:00 |             2 |       2 |       0 |          0 |         0 |         485 |     0.0000 | healthy
```

### Admin API Verification
- `GET /api/admin/provider-health` → OpenAI: healthy (100% success, 485ms), Anthropic: unhealthy (0% success, 1650ms)
- `GET /api/admin/provider-health/summary` → `{"healthy":1,"unhealthy":1,"total":2}`

### Admin UI Verification
- Page `/admin/provider-health` renders correctly with dark mode support
- Summary cards show healthy/unhealthy/degraded/unknown counts
- Table shows provider status badges, request counts, success rates, latencies
- Filter dropdown works by status

## Deployment Notes
- Migration `20260516_provider_health_stats.sql` must be run manually (Portal does not auto-run migrations on startup)
- Proxy `ProviderHealthBuffer` flush loop is non-blocking (goroutine-based)
- Existing `channel_health` table is preserved for manual probe APIs
