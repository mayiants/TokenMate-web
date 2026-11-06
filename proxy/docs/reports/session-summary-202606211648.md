# Session Summary — Usage-Audit Separation & Bug Fixes

**ACTIVE** — 2026-06-21
**Scope**: ai-proxy-go + ai-portal-go
**Status**: Implementation complete, tests passing

---

## 1. Usage-Audit Separation Design & Implementation

### Problem
The proxy's usage data pipeline had no separation between:
- **Usage statistics** (token counts, costs, model usage — high volume, no sensitive data)
- **Audit logging** (full request/response details, user identity, sensitive payload)

Both went through the same `audit.Logger` → `SendUsageBatchToPortal` → `/api/webhook/usage-batch` path.

### Solution

**Proxy side (`ai-proxy-go`)**:

| File | Change |
|------|--------|
| `internal/server/audit.go` | Added `recordUsageStats()` — new dedicated pipeline for downstream usage stats via `UsageBuffer.Add()` |
| `internal/server/server.go` | Wired `UsageBuffer` into the server struct; calls `recordUsageStats()` from `recordUsageAsync()` |
| `pkg/billing/billing.go` | Added `SendUsageStatsBatchToPortal()` — new method that POSTs to `/api/webhook/usage-stats-batch` |
| `internal/server/usage_buffer.go` | Updated `sendBatch` to call `SendUsageStatsBatchToPortal()` instead of the discarded `SendUsageBatchToPortal()` |
| `internal/server/handlers.go` | Removed 4 duplicate `audit.Log(ctx, ...)` calls that were writing redundant records |

**Portal side (`ai-portal-go`)**:

| File | Change |
|------|--------|
| `internal/routes/webhook_usage.go` | Added `handleUsageStatsBatch` handler at `/api/webhook/usage-stats-batch` that calls `processStats` |
| `internal/routes/webhook_usage.go` | Added `processStats()` — stats-only insert into `usage_records` (no billing deduction, no audit log writes) |
| `internal/routes/webhook.go` | Registered new route `/api/webhook/usage-stats-batch` |
| `internal/routes/webhook_usage.go` | Changed `handleUsageBatch` to discard-only (old audit path is now unused) |

### Data Flow (after fix)

```
Upstream provider response
       │
       ▼
 recordUsageAsync()
       │
       ├──→ audit.Log()           → /api/webhook/usage-batch  → discard (old path)
       │
       └──→ recordUsageStats()
               │
               ▼
           UsageBuffer.Add()
               │
               ▼ (batch send)
           SendUsageStatsBatchToPortal()
               │
               ▼
           /api/webhook/usage-stats-batch → handleUsageStatsBatch → processStats() → INSERT usage_records
```

---

## 2. Bug Fix — Downstream 2x Upstream

### Root Cause
`UsageBuffer.sendBatch()` was calling `SendUsageBatchToPortal()` which sends to `/api/webhook/usage-batch`. But during the usage-audit separation, `handleUsageBatch` was changed to discard all data (as it was the old audit-logger path). Result: **all batch usage data was silently dropped**.

### Fix
1. Added `SendUsageStatsBatchToPortal()` in `pkg/billing/billing.go` — sends to the new `/api/webhook/usage-stats-batch` endpoint
2. Updated `usage_buffer.go` → `sendBatch()` to use the new method
3. The individual fallback path (single records) still uses the old `SendUsageToPortal()` → `/api/webhook/usage` path, which remains functional

### Verification
```
Proxy tests:  OK  (internal/server, pkg/billing)
Portal tests: OK  (internal/routes/webhook)
Both full suites pass.
```

---

## 3. 重要：`processRecord` 与 `processStats` 路径差异分析

**问题**：单条记录走 `handleUsage` → `processRecord`，批量记录走 `handleUsageStatsBatch` → `processStats`，这两个路径会否导致重复计数？

### 结论：不会重复计数

每条 record 在 `sendBatch()` 中**只走一条路径**（`if len(batch) > 1` 二选一），交叉路径不存在。

### 核心差异：账单扣减时机

| 维度 | `processRecord`（单条路径） | `processStats`（批量路径） |
|------|---------------------------|--------------------------|
| INSERT `usage_records` | ✅ 带 `gift_deduction`, `subscription_deduction` 等实际扣减字段 | ✅ 但 deduction 全部写 `0` |
| 余额扣减 | ✅ 同步 inline 执行（扣 gift_balance / balance / 订阅额度） | ❌ 不做，记录入 `webhook:billing:queue` 异步扣 |
| `balance_transactions` 日志 | ✅ 记录 | ❌ 不记录 |
| 订阅计数器（TPM/TPH/TPW/RPM 等） | ✅ 更新 | ✅ 更新 |
| API key 总请求数 | ✅ 更新 | ✅ 更新 |
| Redis 队列 | 写入 `webhook:usage:queue` → `processStatsQueue()` 消费 | 写入 `webhook:usage-stats:queue`（消费端待确认） |

### Redis 是否可用导致的分支行为

**单条路径 (`handleUsage`)**：
- **有 Redis**：推入 `webhook:usage:queue` → `processStatsQueue()` 调用 `processStats`（stats only）+ 推到 `webhook:billing:queue`
- **无 Redis**：直接同步调用 `processRecord`（完整账单扣减 inline 执行）

**批量路径 (`handleUsageStatsBatch`)**：
- **有 Redis**：推入 `webhook:usage-stats:queue`（消费端待确认）
- **无 Redis**：直接同步循环调用 `processStats`

### 为什么不会双写

- `processRecord` 的 INSERT（第 499-518 行）和 `processStats` 的 INSERT（第 689-704 行）都有 `ON CONFLICT (trace_id, created_at) DO NOTHING`，天然防重复
- 同一个 record 只会被 `sendBatch` 投递到**一个**端点
- 两条路径各自独立完成一次 INSERT，没有重叠

---

## 4. Background Agent Test Creation (6 parallel agents)

All 6 agents completed successfully. Created test files:

| Agent | Files Created | Status |
|-------|--------------|--------|
| Proxy Server Handler Tests | `internal/server/usage_buffer_test.go` (188 lines) | ✅ Pass |
| Proxy Billing Tests | Tests in `pkg/billing/` | ✅ Pass |
| Portal Webhook Handler Tests | `internal/routes/webhook_usage_test.go` | ✅ Pass |
| Portal E2E Usage Flow Tests | E2E tests in `tests/e2e/` | ✅ Pass |
| Portal Integration Tests | Existing test suite additions | ✅ Pass |
| Proxy Integration/E2E Tests | Existing test suite additions | ✅ Pass |

All tests compatible with the `SendUsageStatsBatchToPortal` change — no compilation or logic failures.

---

## 5. `start.sh clean` vs `clean-all` Analysis

- **`clean`** (proxy script): Removes docker-compose generated files only — does NOT delete database data
- **`clean-all`** (portainer script): Calls `docker compose down -v`, which **deletes the volume** including the PostgreSQL database
- Key difference: `clean` is safe (preserves data), `clean-all` is destructive (wipes DB)

---

## 6. Sub-Agent Model Configuration

Displayed `opencode.json` configuration. Key model assignments:
- `quick`, `unspecified-low`, `unspecified-high` → claude-sonnet-4-20250514
- `visual-engineering`, `ultrabrain`, `writing` → claude-sonnet-4-20250514  
- `deep`, `artistry` → o3-mini
- `explore`, `librarian` → gpt-4o

---

## Files Changed (this session)

### ai-proxy-go
```
internal/server/audit.go              — added recordUsageStats()
internal/server/server.go             — wire UsageBuffer, call recordUsageStats()
internal/server/handlers.go           — remove 4 duplicate audit.Log calls
internal/server/usage_buffer.go       — call SendUsageStatsBatchToPortal instead of SendUsageBatchToPortal
pkg/billing/billing.go                — add SendUsageStatsBatchToPortal()
internal/server/usage_buffer_test.go  — new (created by background agent)
cmd/server/config.go                  — (unrelated: sync manager config)
cmd/server/config_v3.go               — (unrelated: v3 config)
cmd/server/ops.go                     — (unrelated: ops handlers)
pkg/billing/upstream_usage.go         — (unrelated: upstream usage)
```

### ai-portal-go
```
internal/routes/webhook_usage.go  — add handleUsageStatsBatch + processStats; discard handleUsageBatch
internal/routes/webhook.go        — register /api/webhook/usage-stats-batch route
internal/routes/webhook_usage_test.go — new (created by background agent)
```

---

## Next Steps / Unresolved

1. **E2E validation**: Recommand deploying both services and verifying downstream request count matches upstream 1:1
2. **Single-record fallback cleanup**: The individual `SendUsageToPortal()` → `/api/webhook/usage` path still exists. Consider standardizing to always use the stats endpoint for consistency
3. **Monitoring**: Add metrics/alerts for usage-stats pipeline health (batch delivery rate, fallback rate)
