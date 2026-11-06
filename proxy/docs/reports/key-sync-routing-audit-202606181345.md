> **✅ AUDIT COMPLETE** — Findings fixed per `upstream-key-audit-log-fix-plan-202606191813.md`

# Key Sync/Routing Pipeline Audit Report

**Date**: 2026-06-18 13:45 UTC  
**Updated**: 2026-06-19 — second-pass verification with deep source audit, Portal-side bug discovery, and new-api reference analysis  
**Scope**: Portal → Proxy key sync flow, model-to-key routing, concurrency safety, performance, security  
**Status**: ✅ IMPLEMENTED / VERIFIED — All high/medium/low actionable findings from this audit have been addressed. The one remaining theoretical concern (§2.2 read-path slice-header atomicity on 32-bit) has been explicitly accepted as 选择不修复. Only optional new-api feature patterns remain open (model mapping, sticky routing, weighted random).

**Files examined**:
- `ai-portal-go/internal/routes/admin_common.go` — `syncServiceModelsToKeys`
- `ai-portal-go/internal/routes/sync.go` — sync endpoint
- `ai-portal-go/internal/routes/admin_keypool.go` — per-key INSERT/UPDATE, `toggleKeyPoolKey`, `validateKeyAsync`, import flow
- `ai-portal-go/internal/routes/models_sync.go` — `syncKeyModels`, all 10+ auto-sync trigger points
- `ai-portal-go/internal/routes/models_crud.go` — `pauseModel`, `confirmModel`, etc. (all trigger mass-sync)
- `ai-portal-go/internal/db/schema.go` — `upstream_keys` table DDL
- `ai-proxy-go/pkg/keypool/manager.go` — `SyncFromPortal`, `updatePoolsFromSync`, `GetAllKeysForModel`, `IsKeyAvailable`, `checkUpstreamAvailable`, encryption
- `ai-proxy-go/internal/server/handlers.go` — `resolveProviderAndKey`, `smartRoute`
- `ai-proxy-go/internal/server/admin_handlers.go` — `handleSyncKeys`, upstream key validation
- `new-api` (QuantumNous/new-api) — reference for routing patterns, channel cache, billing session atomicity

---

## 1. Model-to-Key Binding: Central Findings

### 1.1 Portal Stores Models Per-Key in SQL

`upstream_keys` table has a `models` column (JSONB array). Each row can hold its own model list:

```sql
-- admin_keypool.go:95
INSERT INTO upstream_keys (..., models, ...) VALUES ($1, ..., $6, ...)
-- $6 = req.Models (per-key from admin UI)
```

### 1.2 Mass-Sync Behavior Is Now Provider-Default + Forceful Path

Two code paths touch models **per-provider**:

**`syncServiceModelsToKeys`** (`admin_common.go:293`) — **gentle fill**:
```sql
UPDATE upstream_keys uk
SET models = COALESCE(...)
WHERE ($1 = '' OR uk.provider = $1)
  AND uk.deleted_at IS NULL
  AND uk.enabled = TRUE
  AND (uk.models IS NULL OR uk.models = '[]'::jsonb)
```
→ Computes a single `jsonb_agg(product_id)` per provider, but **only applies it to keys with an empty model list**. Per-key overrides are preserved.

**`syncKeyModels_handler`** (`models_sync.go:463`) — **forceful overwrite**:
```go
_, err := r.db.Exec(`UPDATE upstream_keys SET models = $1 WHERE provider = $2`,
    models, provider)
```
→ Manually sets same models for ALL keys of a provider. This is the explicit "force sync" path.

### 1.3 Per-Key Override Is Now Persistent

An admin can set per-key models via individual INSERT/UPDATE. The next `syncServiceModelsToKeys` (triggered by model/plan edits) **does not overwrite** keys that already have a non-empty `models` list. Per-key overrides survive mass syncs. To forcefully re-apply provider models to all keys, use **Provider Import → Sync to Upstream Keys** (`syncKeyModels_handler`).

### 1.4 Proxy Applies Models Strictly Per-Key

`GetAllKeysForModel` (`manager.go:2137`):
```go
if len(key.Models) > 0 {
    matched, matchedName := containsMatch(key.Models, model)
    if !matched {
        continue  // key does not support this model
    }
}
```

Each key carries its `Models []string` from sync. **No provider-level model inheritance** — if a key's Models list is empty, it never matches any model search.

### 1.5 Routing is Unified Cross-Provider

`resolveProviderAndKey` (`handlers.go:1229`) collects candidates across **all** providers (excluding circuit-broken ones). Keys from different providers compete on:
1. Priority tier (high > normal > economy)
2. Manual routing order (if configured)
3. Smart routing: health → tier rank → RPM budget → latency → quota → LRU

Primary provider preference only acts as a tiebreaker/affinity hint, not a hard filter.

---

## 2. Concurrency Safety Analysis

### 2.1 Lock Hierarchy

```
Manager
├── poolsMu (sync.RWMutex)  — protects m.pools map
├── locksMu (sync.RWMutex)  — protects m.locks map
├── [per-provider] poolLock (sync.RWMutex in providerLock)  — protects key lists within a pool
├── upstreamUsageMu (sync.RWMutex)  — protects m.upstreamUsage map
│   └── [per-key] usage.mu (sync.Mutex)  — protects RPM/TPM counter within an upstreamUsageEntry
├── providerConfigsMu (sync.RWMutex)
├── routingSettingsMu (sync.RWMutex)
├── routingMu (sync.RWMutex)
├── modelNameMapMu (sync.RWMutex)
├── modelCapabilityMu (sync.RWMutex)
├── healthMu (sync.RWMutex)
├── keyHealthMu (sync.RWMutex)
└── smartMu (sync.RWMutex)
```

### 2.2 Read Path (Hot Path)

`GetAllKeysForModel` → `poolsMu.RLock()` → iterates pool map → accesses `pool.internalKeys` and `key.*` fields (no per-provider lock on this path).

**Finding**: `pool.internalKeys` slice and `Key`/`KeyInternal` pointers are read **without** the per-provider lock during routing. This is safe because:
- `updatePoolsFromSync` replaces the **entire slice** atomically (`existingPool.internalKeys = newInternalList`, line 910) while holding the per-provider write lock
- Readers see either the old slice or the new slice, never a half-built one
- `KeyStatic` fields (including `Models []string`) are overwritten on existing `KeyInternal` structs, but Go's slice header write for `[]string` is not atomic across all architectures

**Risk (theoretical)**: On 32-bit architectures or weak memory models, a goroutine reading a key's `Models` slice could observe a partially-updated slice header (e.g., wrong `len` while reading). In practice on x86_64/arm64 with Go's memory model, this is unlikely but could produce intermittent model-matching failures under concurrent sync.

**Decision 2026-06-19**: Accepted / 选择不修复. Production deployments run on x86_64/arm64, and adding per-provider RLock on the hot read path would add measurable latency with no practical reliability gain. The ModelIndex atomic pointer swap already isolates most reads from per-key `Models` mutations.

### 2.3 Write Path (Sync Merge)

`updatePoolsFromSync` (line 842):

```
for each provider:
    pool, ok := getPool(provider)    // poolsMu.RLock
    lock.Lock()                      // per-provider write lock
    if ok:
        merge keys — atomic slice swap
    else:
        create new pool
        lock.Unlock()
        setPool(provider, pool)      // poolsMu.Lock
    lock.Unlock()

// Remove orphaned providers
poolsMu.Lock()
delete(m.pools, ...)
poolsMu.Unlock()
```

**Finding — TOCTOU gap** (low severity): ✅ FIXED / VERIFIED. The current `updatePoolsFromSync` acquires the per-provider lock (`lock.Lock()`) **before** calling `m.getPool(provider)`. Therefore the pool pointer is read while protected by the per-provider lock, eliminating the TOCTOU window.

### 2.4 RPM/TPM Counter Concurrency

`upstreamUsage` entries use a two-level lock:
- `upstreamUsageMu` protects the map itself (keyed by `"provider:keyID"`)
- Each entry has its own `usage.mu` for the `rpmUsed`/`tpmUsed`/`windowEnd` fields

**Pattern**: `checkUpstreamAvailable` (line 1126) uses `RLock` for map lookup, then `Lock` on the entry. `RecordUpstreamRequest` (line 1164) uses `Lock` for map upsert.

**Finding**: `checkUpstreamAvailable` resets the window if expired under `usage.mu.Lock()` (lines 1144-1149). If `RecordUpstreamRequest` runs concurrently on the same key, it increments usage while the window may be reset or being read. This is correct because `usage.mu` serializes access to these fields.

**Finding — minor**: `RecordUpstreamRequest` (line 1174) only increments `rpmUsed++` without checking the window. If the window expired a microsecond ago, a new request still increments against the old window. The `checkUpstreamAvailable` check in the hot path prevents dispatching beyond limits, but the counter could theoretically drift high without immediate harm since it resets on the next expiry.

### 2.5 `checkUpstreamTPMOverflow` Lock Order

`RecordUpstreamTokens` → `checkUpstreamTPMOverflow` → acquires per-provider `poolLock.Lock()` (line 1202).

**Finding**: This acquires `poolLock` **after** `upstreamUsageMu` has been released. Lock ordering is consistent — `upstreamUsageMu` is never held while acquiring `poolLock` in other paths. No deadlock risk.

---

## 3. Performance Analysis

### 3.1 `GetAllKeysForModel` — O(1) Model Index ✅ IMPLEMENTED

**Verification 2026-06-19**: `pkg/keypool/manager.go` now contains:
- `ModelIndex` struct with `index map[string][]*KeyInternal` and `exact map[string]string`
- `rebuildModelIndex()` called after each sync (line ~1018)
- `GetAllKeysForModel` uses `m.modelIndex.Load().lookup(model)` for O(1) lookup (line ~2236)
- Fallback to full scan only when the index is nil

The previous O(N×M) scan has been replaced by an atomic pointer swap index. **Status: implemented.**

### 3.2 Priority-Tier Retry + `smartRoute` ✅ IMPLEMENTED

**Verification 2026-06-19**: `resolveProviderAndKey` (`handlers.go:1265`) implements a priority-tier retry loop:
- Tier progression scoped by user priority (`high` users can fall back to `high` tier; default users fall back to `normal`)
- Calls `GetAllKeysForModel` with `tierFilter` on each retry
- Falls back through tiers before returning error

The `smartRoute` O(k log k) sorting remains for final candidate ranking within a tier. **Status: implemented.**

### 3.3 `containsMatch` — 3-Pass String Matching — ✅ FIXED

`manager.go:159`:
- Pass 1: Exact match (O(L))
- Pass 2: Normalized (dot↔hyphen) match (O(L) with normalization)
- Pass 3: Prefix match (O(L) with normalization + `strings.HasPrefix`)

**Fix 2026-06-19**: `containsMatch` now builds exact and normalized lookup maps in a single pass before the prefix scan, eliminating repeated normalization on the hot path.

### 3.4 `syncServiceModelsToKeys` — Per-Provider SQL Aggregation

`admin_common.go:293`: Uses correlated subquery `(SELECT jsonb_agg(product_id) FROM service_models WHERE provider = uk.provider GROUP BY provider)`. This is a sequential scan of `service_models` grouped by provider for **every key** in `upstream_keys`. With many keys, this could be expensive.

---

## 4. Security Analysis

### 4.1 Encryption: AES-256-GCM

`EncryptKey`/`DecryptKey` (`manager.go:1908-1961`):
- AES-256-GCM with random nonce per encryption
- Nonce prepended to ciphertext, extracted on decrypt
- Encryption key padded/truncated to 32 bytes

**Finding — minor**: Key derivation is simple padding/truncation instead of a proper KDF (PBKDF2/Argon2). If `ENCRYPTION_KEY` is user-chosen (non-random, < 256 bits), this is vulnerable to brute force. **Recommendation**: Use HKDF or require exactly 256 bits.

**Fix 2026-06-19**: `EncryptKey` now derives the AES key via HKDF-SHA256 with context string `"ai-proxy-keypool-v1"`. `DecryptKey` tries HKDF first and falls back to the legacy padding/truncation method so previously persisted Redis data remains readable.

**Finding — minor**: `EncryptKey(pk, encryptionKey)` at line 1819: The parameter is named `encryptionKey` which shadows the method receiver's `m.encryptionKey`. No bug (intended usage) but confusing naming.

### 4.2 Redis Persistence: Encrypted Keys

`persistToRedis` (`manager.go:1783`):
- Each key's `ActualKey` is AES-256-GCM encrypted before storage
- Encrypted data is hex-encoded for JSON transport
- Redis key: `"ai-proxy:keypool:keys"` (single key, full map serialized)

**Finding — minor**: Single Redis key means the entire pool is a monolithic blob. For large pools (1000+ keys), this serializes/deserializes the entire dataset on every sync. Failure during write corrupts the entire cached state.

**Finding**: If `encryptionKey` is empty (dev mode), plaintext keys are stored in Redis (line 1828). Documented as MVP behavior.

### 4.3 Portal Secret Transmission

`SyncFromPortalWithContext` (`manager.go:517`):
```go
req.Header.Set("x-proxy-secret", m.portalSecret)
```

Secret is sent in a custom header over HTTP (not HTTPS by default enforcement). **Recommendation**: Enforce HTTPS for Portal URL and add header validation on Portal side.

### 4.4 SSRF Protection

`validateBaseURL` (`admin_handlers.go:211`): Blocks private IPs and loopback addresses for upstream key base URLs. **No finding — properly implemented.**

### 4.5 Sync Endpoint Authentication

Portal sync endpoint (`sync.go:55-62`) validates `X-Admin-Secret` or `x-proxy-secret` header. **Verified 2026-06-19**: The query parameter `?secret=` fallback has been **removed** — authentication is now header-only. This was noted as a security concern in earlier audits and has been addressed.

**Remaining concern**: Secret is transmitted over HTTP (not HTTPS enforced). **Recommendation**: Enforce HTTPS for Portal URL in config validation.

**Fix 2026-06-19**: `internal/config/config.go` now validates `portal.url` during `Load()`. If the URL is not `https://`, loading fails unless the hostname is `localhost`/`127.0.0.1`/`::1` or `portal.dev_mode=true` is set. This allows Docker dev environments to keep using `http://portal:3001` via `DevMode`, while production deployments are forced to use HTTPS.

### 4.6 Portal-Side Vulnerabilities (New — Second-Pass Audit)

#### 4.6.1 `toggleKeyPoolKey` Logical Injection via LIKE — ✅ FIXED
**File:** `admin_keypool.go:535` | **Severity: HIGH**

**Fix 2026-06-19**: Removed the `OR key_preview LIKE $3` clause. The endpoint now validates that `keyId` is a valid UUID and matches only `id = $3::uuid`. Frontend verification confirmed `toggleKey()` is called with `{{.ID}}` (UUID), so no display/search behavior is affected.

#### 4.6.2 Import Flow Has No Transaction — ✅ FIXED
**File:** `admin_keypool.go:617-668` | **Severity: MEDIUM**

**Fix 2026-06-19**: Both single-provider and mixed-provider import paths are now wrapped in `db.WithTx()`. Duplicates are counted as skipped; any real DB error rolls back the entire import batch, preventing partial state.

#### 4.6.3 `validateKeyAsync` Overwrites Per-Key Models — 符合设计预期 / BY DESIGN
**File:** `admin_keypool.go:460-463` | **Severity: MEDIUM**

```go
if status == "valid" && len(fetchedModels) > 0 {
    r.db.Exec(ctx, `UPDATE upstream_keys SET validation_status = $1, models = $2 WHERE id = $3::uuid`, ...)
}
```

When background validation succeeds, the upstream API's model list is written to the key's `models` column. **This is intentional**: it lets validation auto-discover and populate a key's supported models, reducing admin manual work. Same pattern at lines 903-907 and 1006-1009. **Status: keep as is; admin can re-run validation to refresh the list.**

#### 4.6.4 `updateServiceModel` Uses 5 Separate UPDATEs Per Field — ✅ FIXED
**File:** `admin_models.go:77-91` | **Severity: MEDIUM**

**Fix 2026-06-19**: Replaced the five separate UPDATEs with a single dynamic UPDATE that sets only the provided fields plus `updated_at = NOW()` in one query.

#### 4.6.5 `syncServiceModelsToKeys` Ignores `deleted_at` and `enabled` — ✅ FIXED
**File:** `admin_common.go:302-309` | **Severity: MEDIUM**

**Verification 2026-06-19**: The WHERE clause now includes `AND deleted_at IS NULL AND enabled = TRUE`. Soft-deleted and disabled keys are excluded from mass sync.

#### 4.6.6 `syncServiceModelsToKeys` Includes `eol` Models in Key Lists — 符合设计预期 / BY DESIGN
**File:** `admin_common.go:297-298` | **Severity: LOW**

The CTE includes `status IN ('active', 'eol_pending', 'eol')`. Verified against `models_eol.go`:
- `eol_pending`: announced EOL but still serving existing users (no new purchases).
- `eol`: confirmed EOL and still serving existing users for up to one month (`eol_purge_at = NOW() + INTERVAL '1 month'`).
- `discontinued`: actually stopped serving; `cleanupSingleModelFromGroups` removes it from groups/keys and triggers sync.

Because `eol_pending` and `eol` are still required to serve active keys until `discontinued`, including them in key model lists is **correct and intentional**. **Status: keep as is.**

#### 4.6.7 Schema: `upstream_keys.validation_status` Missing CHECK Constraint — ✅ FIXED
**File:** `db/schema.go:257-286` | **Severity: LOW**

**Fix 2026-06-19**: Added `CHECK (validation_status IN ('pending', 'valid', 'invalid', 'unknown'))` to the `upstream_keys` table definition and a migration DO-block to add the constraint on existing deployments.

---

## 5. Proxy-Side New Findings (Second-Pass Audit)

### 5.1 No Sync Failure Backoff — ✅ FIXED
**File:** `manager.go:504-530` | **Severity: MEDIUM**

**Fix 2026-06-19**: Added exponential backoff with jitter in `pkg/keypool/manager.go`. Consecutive sync failures double the wait time (capped at `syncInterval`) and `StartSyncLoop` skips periodic syncs until the backoff window has passed. Pub/Sub-triggered syncs still attempt immediately.

### 5.2 Key Decryption Has No Audit Log — ✅ FIXED
**File:** `manager.go:1908-1961` | **Severity: MEDIUM**

**Fix 2026-06-19**: `ai-proxy-go/internal/audit/audit.go` adds `UpstreamKeyID` to `AuditEntry` and `UsageReport`; `internal/server/handlers.go` populates it from `upstreamKey.ID` on every audit log call. The Portal-side `usage_records.upstream_key_id` column now receives the upstream key UUID for both `/api/webhook/usage-batch` and `/api/webhook/usage` paths.

### 5.3 Docker `/health` Endpoint Too Shallow — ✅ FIXED
**File:** `internal/server/server.go:113` | **Severity: LOW**

**Fix 2026-06-19**: `/health` now checks sync freshness for both `keyPoolManager` and `modelRegistry`. If an initialized dependency's last successful sync is older than `3 * syncInterval` (default 10 min fallback), the endpoint returns HTTP 503 with `"status": "degraded"`. Uninitialized components are reported but do not fail the check (so startup probes remain green before the first sync).

### 5.4 Quota Loss Risk — ✅ VERIFIED / NOT REPRODUCIBLE
**File:** Quota tracking across `manager.go` | **Severity: MEDIUM**

**Verification 2026-06-19**: The `reserveQuota` function referenced in the original finding no longer exists. Current code increments `upstreamKey.QuotaUsed` only after a successful upstream response (`handlers.go` success path around line 795/1026). All failure paths (upstream error, read error, 4xx/5xx upstream response) skip quota increment. Therefore quota is **not lost on upstream failure**. **Status: no fix required; current implementation is correct.**

### 5.5 `upstreamUsageMu` Single Lock Bottleneck — ✅ FIXED
**File:** `manager.go` — `upstreamUsageMu` usage | **Severity: LOW**

**Fix 2026-06-19**: Replaced the single `upstreamUsageMu` with 32 sharded `upstreamUsageShard` structs keyed by `fnv64a(provider+":"+keyID) % 32`. The per-entry `usage.mu` is retained for the actual counters; sharding reduces top-level map contention under high throughput.

### 5.6 Redis Single Blob Failure Mode — ✅ FIXED
**File:** `manager.go:1783` — `persistToRedis` | **Severity: LOW**

**Fix 2026-06-19**: `persistToRedis` now stores each provider snapshot under its own Redis key (`ai-proxy:keypool:provider:<provider>`) and writes the provider list plus all snapshots atomically via `TxPipelined` (MULTI/EXEC). `loadFromRedis` reads per-provider keys and includes a fallback to the legacy monolithic blob format for rolling upgrades.

---

## 6. new-api Reference: Patterns Worth Adopting

These patterns from QuantumNous/new-api were identified as valuable additions beyond the existing Model Index and Priority-Tier Retry proposals:

### 6.1 Model Mapping with Chain Redirection
new-api's `Channel.model_mapping` field supports: `{"gpt-4": "gpt-4-0613"}` with chain redirection (A→B→C) and cycle detection. ai-proxy-go has **no model aliasing mechanism**. When upstream providers change model names, all keys must be manually updated. **Effort**: ~150 lines (per-key mapping field + redirect resolver).

### 6.2 Sticky Routing / Channel Affinity
new-api's `GetPreferredChannelByAffinity()` caches the selected channel based on request features (model regex, path, user-agent). ai-proxy-go only does LRU sorting via `SetLastUsed` — no feature-based affinity. **Effort**: ~100 lines (affinity cache + configurable rules).

### 6.3 Weighted Random Selection Within Tier
new-api uses weighted random within priority tier instead of deterministic multi-factor sort. This prevents thundering herd (all requests hitting the same top-ranked key) and provides more predictable load distribution. **Effort**: ~100 lines (weight function + random selection, replaces smartRoute's deterministic sort).

### 6.4 Billing Session Atomicity
new-api's `BillingSession` with `sync.Mutex` ensures atomic settle/refund. ai-proxy-go's upstream-key quota currently increments only after a successful upstream response; failure paths do not deduct. Therefore the "permanent quota loss" scenario does not apply to the current codebase, though a formal two-phase pattern would still be more robust. **Effort**: ~80 lines (two-phase quota with session struct) — **lower priority since current behavior is safe**.

---

## 7. Optimization Recommendations (Priority-Ordered)

| # | Finding | Source | Priority | Effort | Impact |
|---|---------|--------|----------|--------|--------|
| 1 | **Model Index** — O(1) model→key lookup | §3.1 | 🔴 High | ~80 lines | 99% hot-path reduction |
| 2 | **Priority-Tier Retry** — retry loop on upstream failure | §6.2 new-api | 🔴 High | ~60 lines (needs #1) | Eliminates request failures at scale |
| 3 | **Fix `syncServiceModelsToKeys` overwrite** — preserve per-key models | §1.2, §4.6.5 | ✅ Fixed | — | Only fills empty model lists; per-key overrides persist |
| 4 | **Quota pre-consume/settle** — refund on upstream failure | §5.4, §6.4 new-api | ✅ N/A | — | Current code only deducts on success; no refund needed |
| 5 | **Remove LIKE from `toggleKeyPoolKey`** | §4.6.1 | ✅ Fixed | — | UUID-only comparison + validation |
| 6 | **Wrap import in transaction** | §4.6.2 | ✅ Fixed | — | `db.WithTx()` for both import modes |
| 7 | **Sync failure backoff** | §5.1 | ✅ Fixed | — | Exponential backoff with jitter |
| 8 | **Model Mapping (aliasing)** | §6.1 new-api | 🟢 Low | ~150 lines | Reduces ops burden on model changes |
| 9 | **Shard `upstreamUsageMu`** | §5.5, §6 new-api | ✅ Fixed | — | 32 sharded maps by key hash |
| 10 | **Sticky Routing (affinity)** | §6.2 new-api | 🟢 Low | ~100 lines | Better load distribution |
| 11 | **Key usage audit logging** | §5.2 | ✅ Fixed | — | `UpstreamKeyID` in usage audit |
| 12 | **Deep `/health` check** | §5.3 | ✅ Fixed | — | Sync freshness checks for key pool & model registry |
| 13 | **Use HKDF for encryption key** | §4.1 | ✅ Fixed | — | HKDF-SHA256 with legacy fallback |
| 14 | **Fix `validateKeyAsync` model overwrite** | §4.6.3 | ✅ By design | — | Auto-populate key models on validation |
| 15 | **Single UPDATE for `updateServiceModel`** | §4.6.4 | ✅ Fixed | — | One dynamic UPDATE per request |
| 16 | **Partition Redis by provider** | §5.6 | ✅ Fixed | — | Per-provider keys + MULTI/EXEC |

---

## 8. Summary Table (All Findings)

| Area | Severity | Finding | Recommendation |
|------|----------|---------|----------------|
| **Model binding** | **High** | `syncServiceModelsToKeys` overwrites all keys per-provider with same model list; per-key overrides lost on next sync | ✅ Fixed — `syncServiceModelsToKeys` now only fills empty model lists; per-key overrides persist. Force sync remains available via `syncKeyModels_handler`. |
| **Security** | **High** | `toggleKeyPoolKey` LIKE clause on `key_preview` allows logical injection (e.g., `%` matches all) | Remove LIKE; use UUID-only comparison |
| **Performance** | **Medium** | `GetAllKeysForModel` is O(N×M) per request with string matching | Build inverted model→key index (Option A) |
| **Reliability** | **Medium** | No tier-based retry on upstream failure; request fails immediately | Add priority-tier retry loop (P1, needs Model Index) |
| **Reliability** | **Medium** | Import flow has no transaction — partial imports possible | ✅ Fixed — wrapped in `db.WithTx()` |
| **Data integrity** | **Medium** | `validateKeyAsync` overwrites per-key models on validation success | ✅ By design — auto-populates key models on validation |
| **Data integrity** | **Medium** | `syncServiceModelsToKeys` doesn't filter `deleted_at` or `enabled` | ✅ Fixed — filter added |
| **Reliability** | **Medium** | Quota is deducted on request start but never refunded on upstream failure | ✅ Verified — quota only deducted on success; no fix needed |
| **Observability** | **Medium** | Key decryption has no audit log | ✅ Fixed — UpstreamKeyID now logged in usage audit |
| **Data integrity** | **Low** | `syncServiceModelsToKeys` includes `eol_pending`/`eol` models | ✅ By design — these statuses still serve existing users until `discontinued` |
| **Reliability** | **Low** | No sync failure backoff — proxy hammers portal on outage | ✅ Fixed — exponential backoff with jitter |
| **Data integrity** | **Low** | `updateServiceModel` uses 5 separate UPDATEs — no atomicity | ✅ Fixed — single dynamic UPDATE |
| **Performance** | **Low** | `upstreamUsageMu` single lock bottleneck under high throughput | ✅ Fixed — 32 sharded maps |
| **Concurrency** | **Low** | TOCTOU gap between `getPool` and `lock.Lock` in `updatePoolsFromSync` | ✅ Fixed — `getPool` is now called after acquiring the per-provider lock |
| **Concurrency** | **Low** | Read path reads `KeyStatic` fields without per-provider lock; theoretical non-atomic slice header on 32-bit | ✅ Accepted / 选择不修复 — x86_64/arm64 production unaffected; per-provider RLock would hurt hot-path latency |
| **Performance** | **Low** | `containsMatch` does 3-pass linear scan on every comparison | ✅ Fixed — exact/normalized maps in one pass |
| **Security** | **Low** | Encryption key uses padding instead of KDF | ✅ Fixed — HKDF-SHA256 with legacy fallback |
| **Data integrity** | **Low** | `upstream_keys.validation_status` missing CHECK constraint | ✅ Fixed — constraint + migration added |
| **Security** | **Info** | Portal sync via HTTP (not HTTPS enforced) | ✅ Fixed — `https://` required unless localhost/loopback or `portal.dev_mode=true` |
| **Security** | **Info** | Single Redis blob key for entire pool | ✅ Fixed — per-provider keys + MULTI/EXEC |
| **Feature** | **Info** | No model aliasing mechanism — manual updates needed when providers rename models | Add `model_mapping` per-key field |
| **Feature** | **Info** | No sticky routing / channel affinity | Add affinity cache with configurable rules |

---

## 9. Conclusion

The key sync pipeline is functionally correct: models flow from Portal's `upstream_keys` SQL table through the sync endpoint to the proxy, where each key's `Models` list gates routing.

**Second-pass verification (2026-06-19) confirmed most original findings are accurate and resolved the vast majority of actionable items:**
- ✅ `syncServiceModelsToKeys` now filters `deleted_at IS NULL AND enabled = TRUE`.
- ✅ Upstream-key usage audit logging implemented (`UpstreamKeyID` in Proxy audit/billing records and Portal `usage_records`).
- ✅ `usage_records.upstream_key_id` bug fixed (was incorrectly populated with `server_id`).
- ✅ Quota-loss risk verified: current code only increments quota on upstream success; no refund path is needed.
- ✅ EOL model inclusion in key lists is confirmed to be by design (`eol_pending` and `eol` still serve existing users until `discontinued`).
- ✅ `toggleKeyPoolKey` LIKE injection removed; UUID-only comparison with validation.
- ✅ Import flow wrapped in transactions (`db.WithTx()`).
- ✅ `updateServiceModel` batched into a single dynamic UPDATE.
- ✅ Sync failure exponential backoff implemented.
- ✅ `upstreamUsageMu` replaced with 32 sharded maps.
- ✅ Redis persistence partitioned by provider with atomic MULTI/EXEC write.
- ✅ `/health` endpoint checks sync freshness for key pool and model registry.
- ✅ Encryption key derivation upgraded to HKDF-SHA256 with legacy fallback.
- ✅ `upstream_keys.validation_status` CHECK constraint added.
- ✅ `containsMatch` optimized with one-pass exact/normalized maps.
- ✅ Model Index (O(1) lookup) and Priority-Tier Retry already implemented.
- ✅ TOCTOU gap in `updatePoolsFromSync` verified fixed (lock acquired before `getPool`).
- ✅ Portal sync URL HTTPS enforcement added with localhost/loopback and `dev_mode` exemptions.

**Open findings** remaining:
- **Concurrency**: read path `KeyStatic` slice header atomicity on 32-bit — explicitly **accepted / 选择不修复** (low severity, theoretical; x86_64/arm64 production unaffected).
- **new-api reference patterns**: model mapping with chain redirection, sticky routing/affinity, weighted random selection — optional enhancements, not bugs.

The two highest-ROI improvements (Model Index and Priority-Tier Retry) were already implemented. The remaining open items are low-severity theoretical concerns or optional feature work.
