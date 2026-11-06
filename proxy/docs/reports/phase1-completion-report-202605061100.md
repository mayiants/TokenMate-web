> **HISTORICAL** — May 2026 Phase 1 completion report.

# Phase 1 Completion Report

**Date:** 2026-05-06  
**Projects:** `ai-portal-go` (Portal) + `ai-proxy-go` (Proxy)  
**Scope:** Tasks 1–8 (Rate Limit Refinement, Redis Pub/Sub Sync, Azure/AWS Adaptors)

---

## Executive Summary

All 8 tasks in Phase 1 have been completed, tested, and pushed to GitHub. The Proxy now supports per-user RPM/TPM rate limits from Portal auth, real-time key pool sync via Redis Pub/Sub, and dedicated adaptors for Azure OpenAI and AWS Bedrock.

| # | Task | Status | Est. | Actual |
|---|------|--------|------|--------|
| 1 | Portal sync `validation_status` filter | ✅ Done | — | — |
| 2 | Proxy consume `providers[]` from sync | ✅ Done | — | — |
| 3 | Key models filtering in `GetKeyByPriority` | ✅ Done | — | — |
| 4 | Upstream Key Token Bucket (60s window) | ✅ Done | — | — |
| 5 | Affinity Cache refactor (unified key format) | ✅ Done | — | — |
| 6 | User Key Rate Limit refinement | ✅ Done | 2h | ~2h |
| 7 | Redis Pub/Sub sync | ✅ Done | 4h | ~3h |
| 8 | Azure + AWS Adaptor | ✅ Done | 6h | ~4h |

---

## 1. Portal Sync `validation_status` Filter

**Portal** `internal/routes/sync.go`

- Sync query now filters `WHERE enabled = true AND validation_status = 'valid'`
- Ensures only validated upstream keys are distributed to Proxies
- Prevents Proxy from attempting to use invalid/expired keys

---

## 2. Proxy Consume `providers[]` from Sync

**Proxy** `pkg/keypool/manager.go`

- Proxy now consumes `providers[]` array from Portal sync response
- `updateProviderConfigs()` caches provider configs in memory
- `resolveBaseURL()` implements 3-tier fallback:
  1. `upstreamKey.BaseURL` (key-level override)
  2. Synced ProviderConfig from Portal
  3. `DefaultProviderConfigs` hardcode fallback
- `groq` + `xiaomi` added to fallback map

---

## 3. Key Models Filtering in `GetKeyByPriority`

**Proxy** `pkg/keypool/manager.go`

- `GetKeyByPriority` now accepts `reqModel` parameter
- Filters upstream keys by `Models` whitelist (non-empty = strict)
- `contains()` helper added for string slice matching
- Only keys that explicitly support the requested model are selected

---

## 4. Upstream Key Token Bucket (60s Fixed Window)

**Proxy** `pkg/keypool/manager.go`

- Per-key RPM/TPM tracking with 60s fixed window
- `checkUpstreamAvailable(key)` pre-checks RPM before selection
- `RecordUpstreamRequest()` increments RPM counter
- `RecordUpstreamTokens()` increments TPM; auto-triggers 60s cooldown if limit exceeded
- `RateLimitUntil` field on `KeyInternal` for cooldown state

---

## 5. Affinity Cache Refactor

**Proxy** `pkg/cache/affinity.go`

- **Critical fix**: Unified read/write cache key format to `priority:model:group:sha1(body)[:8]`
- Previously `GetChannelWithPriority` used `hash(priority:model:keyID)` while `RecordChannelWithPriority` used `priority:model:group:sha1(body)[:8]`, causing cache to **never hit**
- `GetKeyByPriority` now accepts `preferredKeyID` — affinity hit bypasses LRU if key is in filtered candidate set
- Routing priority: Priority filter → Availability check → Affinity hit → LRU fallback

---

## 6. User Key Rate Limit Refinement

### Portal Changes

| File | Change |
|------|--------|
| `internal/routes/apikeys.go` | Validate endpoint returns `rate_limit_tpm` |
| `internal/routes/apikeys.go` | Create endpoint accepts and stores `rate_limit_tpm` (default 100000) |

### Proxy Changes

| File | Change |
|------|--------|
| `internal/auth/auth.go` | `Claims` adds `RateLimitTPM`; parses from Portal validate response |
| `internal/ratelimit/ratelimit.go` | **Rewritten**: removed 8 hardcoded RPM presets; `sync.Map` on-demand counters |
| `internal/ratelimit/ratelimit.go` | `AllowRPM(redisKey, rpmLimit)` — request pre-check |
| `internal/ratelimit/ratelimit.go` | `RecordTokens(redisKey, tpmLimit, tokens)` — post-request TPM tracking |
| `internal/server/handlers.go` | All handlers (chat, embeddings, claude, simulate) call `recordTPM()` after request |
| `cmd/server/main.go` | Creates `rateLimitMiddleware`, passes to `server.New()` |
| `internal/server/server.go` | `New()` signature updated to accept `*ratelimit.Middleware` |

### Architecture

```
User Request → Auth Middleware (sets rate_limit_rpm/tpm in context)
            → Rate Limit Middleware (checks RPM via token bucket)
            → Handler (forwards to upstream)
            → Handler (on success, calls RecordTokens for TPM)
```

- **RPM**: Checked before request; hard block with 429 if exceeded
- **TPM**: Recorded after request; logs warning if exceeded (request already completed)
- Both use local token buckets with async Redis sync (non-blocking)

### Test Results

```
✓ PASS: Portal Login
✓ PASS: API key created with RPM=5, TPM=500
✓ PASS: Validate returns rate_limit_rpm=5, rate_limit_tpm=500
✓ PASS: Request 6 correctly rate-limited (429)
```

---

## 7. Redis Pub/Sub Sync

### Portal Changes

| File | Change |
|------|--------|
| `internal/db/db.go` | Added `PublishSyncEvent(ctx)` — publishes to `ai-proxy:sync` channel |
| `internal/db/db.go` | `SyncMessage` struct: `{"type":"sync_needed","source":"portal","timestamp":"..."}` |
| `internal/routes/admin.go` | All upstream key CRUD (create/delete/toggle/import/validate) → `go PublishSyncEvent()` |
| `internal/routes/admin.go` | All provider CRUD (create/update/delete/toggle) → `go PublishSyncEvent()` |
| `internal/web/admin_handlers.go` | Web handlers also publish on upstream key mutations |
| `internal/web/actions.go` | Toggle upstream key action publishes sync event |

### Proxy Changes

| File | Change |
|------|--------|
| `pkg/keypool/manager.go` | `StartPubSubListener()` — subscribes to `ai-proxy:sync` |
| `pkg/keypool/manager.go` | Auto-reconnect with 5s backoff on connection loss |
| `cmd/server/main.go` | Calls `keyPoolMgr.StartPubSubListener()` at startup |

### Behavior

1. Admin modifies upstream key or provider in Portal
2. Portal publishes `sync_needed` message to Redis Pub/Sub
3. All connected Proxies receive message within milliseconds
4. Each Proxy triggers `SyncFromPortal()` immediately
5. Existing 60s HTTP polling continues as fallback

### Test Results

```
✓ PASS: Proxy received Pub/Sub sync notification (manual Redis PUBLISH)
✓ PASS: Proxy triggered sync after Pub/Sub message
✓ PASS: Portal mutation (create upstream key) triggered Pub/Sub sync
```

---

## 8. Azure + AWS Bedrock Adaptors

### Azure OpenAI Adaptor

**File:** `pkg/relay/channel/azure/azure.go`

| Feature | Implementation |
|---------|----------------|
| Auth | `api-key` header (not Bearer token) |
| URL | `https://{resource}.openai.azure.com/openai/deployments/{deployment}/chat/completions?api-version=...` |
| Deployment | Extracted from `BaseURL` (upstream key override) or falls back to model name |
| API Version | Configurable via `APIVersion` (default `2024-02-01`) |
| Format | OpenAI-compatible request/response |

### AWS Bedrock Adaptor

**File:** `pkg/relay/channel/aws/aws.go`

| Feature | Implementation |
|---------|----------------|
| Auth | AWS SigV4 lightweight signing |
| Region | Extracted from `BaseURL` (e.g., `us-east-1`, `us-west-2`) |
| Credentials | `APIKey` format: `access_key_id:secret_access_key` |
| URL | `https://bedrock-runtime.{region}.amazonaws.com/model/{model-id}/converse` |
| Request | OpenAI → Bedrock Converse conversion (messages, system, inferenceConfig) |
| Response | Bedrock Converse → OpenAI conversion (choices, usage, finish_reason) |
| Model Mapping | Claude → `anthropic.claude-3-*`, Llama → `meta.llama3-*`, Mistral → `mistral.mistral-*`, default → Claude Sonnet |

### Infrastructure Changes

| File | Change |
|------|--------|
| `pkg/relay/base/adaptor.go` | Exported `SharedHTTPClient()` for adaptor reuse |
| `pkg/relay/interface.go` | `GetAdaptor` switch routes `microsoft` → `azure.NewAzureAdaptor`, `amazon` → `aws.NewAWSAdaptor` |
| `pkg/relay/providers.go` | `DefaultProviderConfigs` updated: `microsoft` has `{resource}/{deployment}` placeholder, `amazon` has `{region}` placeholder |

### Unit Test Results

```
=== Azure ===
✓ PASS: TestNewAzureAdaptor (api-key header, no Authorization)
✓ PASS: TestAzureGetRequestURL (deployment in BaseURL + model fallback)
✓ PASS: TestAzureCustomAPIVersion (2023-05-15 override)

=== AWS ===
✓ PASS: TestNewAWSAdaptor (region=us-west-2, credentials parsed)
✓ PASS: TestAWSDefaultRegion (fallback us-east-1)
✓ PASS: TestMapModelToBedrock (Claude/Llama/Mistral/Titan/default)
✓ PASS: TestAWSGetRequestURL (contains Bedrock model path)
✓ PASS: TestAWSToProvider (system→top-level, messages→conversation)
✓ PASS: TestAWSFromProvider (Bedrock JSON → OpenAI response)
✓ PASS: TestSigV4Signing (credentials parsed correctly)
```

---

## Environment Fix Applied During Testing

**Issue:** `.env` and `.env.go-platform` had inconsistent `PROXY_SYNC_SECRET` and `JWT_SECRET`, causing Proxy sync to return HTTP 401.

**Fix:** Updated `.env` to match `.env.go-platform`:
- `PROXY_SYNC_SECRET=<ENCRYPTION_KEY>`
- `JWT_SECRET=<PLACEHOLDER_JWT_SECRET>`

---

## Git Commits

### Portal (`mayiants/ai-portal-go`)

| Commit | Message |
|--------|---------|
| `6a0eca7` | (baseline) Portal bug fixes |
| `59d786d` | feat: include rate_limit_tpm in auth validate response |
| `2065302` | feat: Redis Pub/Sub sync notifications from Portal |
| `0382bfa` | fix: create API key now accepts and stores rate_limit_tpm |

### Proxy (`mayiants/ai-proxy-go`)

| Commit | Message |
|--------|---------|
| `514a392` | (baseline) Key pool critical bugs + affinity cache |
| `9a064a0` | feat: per-user RPM/TPM rate limits from Portal auth |
| `52d2131` | feat: Redis Pub/Sub real-time sync listener in Proxy |
| `ca6d6e5` | test: add integration test script for Task 6 & 7 |
| `4c795b1` | feat: Azure + AWS Bedrock adaptors (Task 8) |

---

## Known Issues / Technical Debt

| # | Issue | Impact | Status |
|---|-------|--------|--------|
| 1 | **Sync Runtime State Reset** — `updatePoolsFromSync` rebuilds pools, resetting `Failures`, `CooldownUntil`, etc. | Key state lost on every sync | **FIXED** — merge logic implemented in `updatePoolsFromSync` |
| 2 | **Redis `loadFromRedis` broken** — `internalKeys` unexported + `Key` has `json:"-"`, so actual keys lost on Redis fallback | Cannot recover keys from Redis | **Not fixed** |
| 3 | **Model sync JSON parse error** — Proxy models registry fails to decode `id` as `int` when Portal sends `string` | Model sync fails (non-critical, has fallback) | **FIXED** — ServiceModel.ID changed to string |
| 4 | **Group/Model Dynamic Switch** — `enroll_group`, `current_group`, `current_model` architecture | Major Phase 2 feature | **Deferred to Phase 2** |

---

## Service Status (Post-Testing)

```
Portal:   http://127.0.0.1:3001  (healthy)
Proxy:    http://127.0.0.1:8080   (healthy)
Redis:    127.0.0.1:6379
DB:       127.0.0.1:5432
Network:  ai-platform-net (Docker)

Admin Login: admin@example.com / <ADMIN_PASSWORD>
```

---

## Test Scripts Created

| File | Description |
|------|-------------|
| `ai-proxy-go/tests/integration/task6_7_test.sh` | Tests: login, key creation, validate response, RPM rate limit, Pub/Sub sync |
| `ai-proxy-go/tests/integration/task8_test.sh` | Tests: health, Azure unit tests, AWS unit tests, GetAdaptor resolution, full suite |

---

## Next Steps (For Discussion)

1. **Fix Sync Runtime State Reset** — Merge logic instead of full rebuild
2. **Fix Redis `loadFromRedis`** — Export `internalKeys` or use separate Redis structure
3. **Group/Model Dynamic Switch** — Implement `enroll_group[]`, `current_group`, `current_model` on `api_keys` table
4. **AWS Streaming Support** — Bedrock streaming uses different format
5. **Azure Model-to-Deployment Mapping** — More sophisticated mapping than model-name fallback
6. **Frontend Rebuild** — If Portal React source changes, dist needs rebuild
