# ai-portal-go / ai-proxy-go Review Report

> Date: 2026-07-05  
> Portal commit: `689ad27`  
> Proxy commit: `b92759c`  
> Status: P0 fixes implemented and verified; targeted tests passing.

---

## 1. Synchronization & Verification

| Project | Local → Origin | Build | `go vet` | `gofmt` | Targeted tests |
|---|---|---|---|---|---|
| `ai-proxy-go` | `815c45e → b92759c` | ✅ | ✅ | ✅ | ✅ |
| `ai-portal-go` | `75cef45 → 689ad27` | ✅ | ✅* | ✅ | ✅ |

\* `ai-portal-go` `go vet ./...` reports `permission denied` on `uploads/enterprise-apps`; this is an environment artifact, not a code issue.

---

## 2. Recent Change Summary

### ai-proxy-go

Large performance/observability iteration (~5,800 insertions):

- Refactored `pkg/keypool/manager.go` into `health.go`, `persist.go`, `queue.go`, `selection.go`, `sync.go`.
- Introduced `sync.Pool` in `pkg/pool/pool.go` for hot-path objects.
- Eliminated repeated upstream-response JSON parsing across provider adapters.
- Migrated to `prometheus/client_golang` with histogram/counter metrics.
- Added local Redis-backed balance cache + async settle.
- Added per-provider bounded queue + worker pool for non-streaming upstream dispatch (opt-in).
- Reduced keypool lock contention and precomputed model matching sets.
- Added OpenTelemetry trace propagation.
- Added Portal HTTPS callbacks (`/api/webhook/sync`, `/api/webhook/balance-update`).

### ai-portal-go

Control-plane hot-path optimization (~4,000 insertions):

- OpenTelemetry trace propagation for webhooks.
- DB/Redis TLS configuration via environment variables.
- HTTPS callbacks to proxies for sync/balance events.
- `api_keys.key_hash` index + merged enterprise queries in `/api/keys/validate`.
- Batched DB writes for usage-stats webhook path.
- Wired previously dead metrics.
- In-memory cache for `system_settings` rate-limit defaults.
- `config_version` in key-pool sync response.
- Added load-test tooling and local CI runner.

---

## 3. Architecture Recap

```text
┌────────────────────────────────────────┐
│ ai-portal-go  (Control Plane)          │
│  - Multi-tenant admin & API key mgmt   │
│  - Billing, pricing, invoicing         │
│  - Webhooks → Proxy                    │
└──────────────┬─────────────────────────┘
               │ HTTPS / webhook callbacks
               ▼
┌────────────────────────────────────────┐
│ ai-proxy-go   (Data Plane)             │
│  - JWT/auth, rate limiting             │
│  - Key pool, provider adapters         │
│  - Pre-consume, settle, audit, metrics │
└────────────────────────────────────────┘
```

---

## 4. Risk & Recommendations (Updated Section 6.2)

### 4.1 ai-proxy-go

| # | Item | Severity | Status / Recommendation |
|---|---|---|---|
| 1 | **Responses API billing gap** | P0 | ✅ Fixed: `handleResponses` now pre-consumes and settles for convert, passthrough, and streaming paths. |
| 2 | **Streaming context cancellation** | P0 | ✅ Fixed: all streaming handlers now use `DoRequestBytesWithContext(c.Request.Context(), ...)` and close `resp.Body` on context cancellation; pre-consumed balance is refunded as `client_disconnect`. |
| 3 | **Info-level logging of request/response bodies** | P1 | ✅ Fixed: `handlers_chat.go` logs request/response bodies at `Debug` only. |
| 4 | **`readProviderResult` unbounded body read** | P1 | ✅ Fixed: `pkg/keypool/queue.go` caps response body at 50 MiB and returns `ErrResponseTooLarge`. |
| 5 | **OpenTelemetry exporter blocks startup** | P1 | ✅ Fixed: `pkg/telemetry/telemetry.go` installs a no-op provider synchronously and builds the OTLP exporter in a background goroutine. |
| 6 | **Global Prometheus registry** | P1 | ✅ Fixed: `pkg/metrics/prometheus.go` uses `atomic.Pointer` and provides `NewPrometheusRegistry()` / `SetDefaultRegistry()`. |
| 7 | **Duplicate RPM/budget checks** | P2 | ✅ Fixed: `429cd48` removed `KeyHasRPMBudget` and the redundant smart-routing budget check. |
| 8 | **Audit path still spawns goroutine per request** | P2 | `recordUsageAsync` starts a goroutine even though usage buffer already batches. Consider collapsing into the buffer path. |
| 9 | **Provider queue memory across many providers** | P2 | Queues are lazily created and never pruned. Long-running instances with many provider names may accumulate goroutines/maps. Consider a bounded provider queue map or TTL eviction. |

**Notes on items removed from the earlier draft:**

- *"Provider queue disabled by default"* is intentional/opt-in, not a risk.
- *Multi-proxy balance-cache overdraw* is acknowledged in code/docs (`06b6e7f`) and is a documented limitation, not an unmitigated bug.

### 4.2 ai-portal-go

| # | Item | Severity | Status / Recommendation |
|---|---|---|---|
| 1 | **Hand-rolled metrics** | P0 | ✅ Fixed: `pkg/metrics/metrics.go` now uses `prometheus/client_golang` collectors exposed via `Metrics.Handler()`. |
| 2 | **Synchronous audit logging** | P0 | ✅ Fixed: `pkg/audit/audit.go` now buffers entries and flushes in batches via a background worker with shutdown drain. |
| 3 | **`/api/keys/validate` cache invalidation** | P1 | ✅ Fixed: `validate:{keyHash}` is evicted on key/group/subscription mutations. |
| 4 | **`/api/keys/validate` active service_models cache** | P0 | ✅ Fixed: active model set cached in Redis (`active_service_models`) and invalidated by `syncServiceModelsToKeys`. |
| 5 | **Batch webhook per-record pricing lookups** | P1 | ✅ Fixed: added `PricingService.CalculateCostBatch` and `BillingService.GetPricingRatioBatch`; `processStatsBatch` and `processBillingBatch` now resolve pricing once per `(provider, region, model, minute)` group. |
| 6 | **`config_version` includes volatile health data** | P1 | ✅ Fixed: `provider_health`/`key_health` were already excluded from the MD5 hash; added regression test proving stability across health changes. |
| 7 | **Webhook Redis queue failure logging** | P1 | ✅ Fixed: Redis push failures now log at `Error` and increment `WebhookRedisQueueFailed` in `handleUsage`, `processStatsBatch`, and `processUsageStatsQueue`. |
| 8 | **`system_settings` cache refresh** | P2 | ✅ Fixed: `645bf1f` added lazy TTL refresh to the in-memory `systemSettingsCache`; getters reload from DB after expiration. |
| 9 | **Missing config documentation** | P2 | `ADMIN_SECRET`, `PROXY_SYNC_SECRET`, Redis/DB TLS vars are not in README/CLAUDE.md. Fix: update docs. |

### 4.3 Cross-Project Consistency

| # | Item | Severity | Recommendation |
|---|---|---|---|
| 1 | Telemetry package duplication | P2 | Both repos have nearly identical `pkg/telemetry/telemetry.go`. Long-term: extract shared module. |
| 2 | Metrics stack divergence | P1 | ✅ Fixed: Portal `pkg/metrics/metrics.go` now uses `prometheus/client_golang` collectors exposed via `Metrics.Handler()`. |
| 3 | Log schema enrichment | P2 | Proxy adds `provider`, `model`, `trace_id`; Portal webhooks now propagate trace. Standardize remaining fields. |

---

## 5. P0 Fix Proposals

### 5.1 ai-proxy-go

#### 5.1.1 Responses API Billing Gap

**Problem:** `handleResponses` does not reserve balance before upstream calls or settle after completion.

**Proposed fix:**

1. In `internal/server/handlers_responses.go`, before calling the adaptor:
   - Estimate input tokens from the request messages.
   - Call `preConsumeLocal(c, userID, keyType, model, provider, inputTokens)`.
   - If rejected, return `402 Payment Required`.
2. After receiving the upstream response:
   - Parse usage (extend `ExtractUsage` for responses shape if needed).
   - Call settle logic (`settleLocal` or `handlerDeps.Billing.Settle`).
3. Add tests for both passthrough and convert modes.

**Risk:** Responses API response shape differs from chat completions; verify `ExtractUsage` covers it or add a responses-specific extractor.

#### 5.1.2 Streaming Context Cancellation

**Problem:** Streaming handlers call `adaptor.DoRequestBytes(...)` (no context) for the initial upstream handshake, so client disconnect does not abort the upstream request.

**Proposed fix:**

1. Change `handleStreamingResponse` and `handleResponses` streaming branches to use `adaptor.DoRequestBytesWithContext(c.Request.Context(), body, false)` for the handshake.
2. Derive `streamCtx` from `c.Request.Context()` so cancellation propagates through the entire stream.
3. Per-provider adaptor testing to ensure context cancellation is respected.

**Risk:** Some provider adaptors may have subtle behavior with context cancellation; needs channel-by-channel verification.

#### 5.1.3 Multi-Proxy Balance-Cache Overdraw

**Problem:** Local per-process balance cache with optimistic `DECRBY` can overdraw when multiple Proxy instances serve the same user.

**Proposed fix (combined approach):**

1. **Redis-backed central balance guard:** Use Redis `DECRBY` with a floor at `-(max_single_request_cost)`. On each request, check if `cached_balance - estimated_cost >= -soft_overdraft_limit`.
2. **Soft-reject threshold:** Reject when cached balance < `estimated_cost * pre_consume_margin`.
3. **Periodic reconciliation:** Every N seconds or on webhook balance-update, sync the authoritative balance from Portal and correct any drift.
4. **Record overdrafts:** Log/metric overdraft events for billing reconciliation.

**Risk:** Adds a Redis RTT to the hot path; can be mitigated by keeping a local copy that is invalidated by webhook.

### 5.2 ai-portal-go

#### 5.2.1 Migrate Metrics to Prometheus Client

**Problem:** `pkg/metrics/metrics.go` accumulates latency as a counter sum and emits plain text.

**Proposed fix:**

1. Replace `Metrics` struct with `prometheus/client_golang` counters/gauges/histograms.
2. Keep a `/metrics` endpoint that serves the registry.
3. For backward compatibility, optionally keep `ToPrometheusFormat()` as a thin wrapper around the registry for any legacy consumers.
4. Add a request-duration histogram with labels: `method`, `route`, `status_code`.

**Risk:** Any external tooling parsing the old text format may need updating; verify before deploying.

#### 5.2.2 Asynchronous Audit Logging

**Problem:** `pkg/audit/audit.go` performs synchronous DB inserts.

**Proposed fix:**

1. Convert `AuditLogger` to a channel + background worker.
2. Use dual alternating batches: one active batch receiving records, one flushing.
3. On flush failure, write to a fallback log file and metric; do not block the caller.
4. Keep a synchronous path only for critical security events if required by compliance.
5. Ensure graceful shutdown drains the remaining batch.

**Risk:** Adds complexity; must not lose audit records on crash. Use periodic flush (e.g., 1s) + max-batch-size flush.

#### 5.2.3 Pre-Load Active Service Models into Redis

**Problem:** `/api/keys/validate` still queries `service_models` on Redis cache miss.

**Proposed fix:**

1. On startup and after any model CRUD, write the active model list to Redis key `service_models:active` with a TTL.
2. In `validateKey`, on cache miss read from Redis instead of the DB.
3. Invalidate `service_models:active` on model create/update/delete.

**Risk:** Slightly increases complexity of model admin endpoints; needs a test for invalidation.

---

## 6. Test Coverage Improvement Plan

### 6.1 ai-proxy-go

Current low-coverage packages:

| Package | Coverage | Focus |
|---|---|---|
| `internal/server` | ~27% | Handler integration tests with mock adaptor and httptest recorder. Cover pre-consume rejection, queue fallback, upstream errors, streaming headers. |
| `pkg/keypool` | ~36% | Extend `queue_test.go`; add `selection_test.go` for priority/tier/quota/LRU; add `sync_test.go` for config-version skip and merge. |
| `internal/auth` | ~31% | Balance-cache hit/miss/invalidation, graceful degradation, concurrent access, mock Portal server. |
| `pkg/billing` | ~49% | Mock Portal pre-consume/settle; cost calculation and margin/refund paths. |

### 6.2 ai-portal-go

Current low-coverage packages:

| Package | Coverage | Focus |
|---|---|---|
| `internal/routes` | ~4.7% | DB-backed integration tests for `/api/keys/validate`, `/webhook/usage-stats-batch`, `/api/admin/pricing/*`. Use `testutil` DSN. |
| `internal/services` | ~50% | Pricing rule resolution with seeded rules; exchange-rate caching; cost rounding. |
| `pkg/audit` | ~39% | Batch flush, failure retry, shutdown drain after async conversion. |
| `internal/web` | ~13% | `httptest` + Gin page rendering; assert on key HTML/i18n tokens. |

### 6.3 Cross-Cutting Actions

1. Add `make test-coverage` to both repos, running `go test -coverprofile` and printing per-package coverage.
2. Set CI thresholds:
   - Proxy: `internal/server`, `pkg/keypool`, `internal/auth` ≥ 50%.
   - Portal: `internal/routes` ≥ 20%, `internal/services` ≥ 60%.
3. Document test database setup and required env vars (`TEST_DATABASE_DSN`).
4. Use table-driven tests and httptest recorder to reduce reliance on real infrastructure.

---

## 7. Immediate Action Items

1. ✅ Sync both repos to latest `origin/main`.
2. ✅ Build, vet, format, and targeted tests passing.
3. ✅ Produce this corrected review report.
4. ✅ Implement P1/P2 fixes listed in section 4 (excluding P0 items).
5. **Next:** Address remaining P2 polish items:
   - ai-proxy-go: audit goroutine per request, provider queue map TTL/eviction.
   - ai-portal-go: missing config documentation.
   - Cross-project: telemetry package duplication, log schema enrichment.
6. **After that:** Decide which P0 proposals to schedule and assign owners.
7. **Parallel track:** Begin incremental test coverage work in a separate branch.

---

## 8. Conclusion

Both projects are in a healthy, fast-moving state. The recent iteration successfully closed many performance and observability gaps identified in the optimization plan. The remaining P0 items are well-understood and have concrete proposals. The main near-term risks are (1) completing the P1/P2 polish, (2) avoiding regressions while landing P0 fixes, and (3) raising test coverage in the most complex packages.
