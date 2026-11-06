# Key Pool Structure & Sync Mechanism Design

**Date:** 2026-05-06 04:04 UTC  
**Scope:** `ai-proxy-go/pkg/keypool` — upstream key representation, sync behavior, and 100K RPM capacity planning  
**Status:** Design document — **KeyStatic/KeyRuntime split ✅ 已实现** (见 `pkg/keypool/manager.go` 第 70–125 行)。
> **P0 状态报告 (Key Pool Status)**: ✅ 已实现 — 见 `provider-health-monitoring-implementation-202605161600.md`
> **P1 上游用量统计**: ✅ 已实现 — 见 `upstream-usage-implementation-202605161100.md`
> **P2 Redis 持久化修复**: 仍待实现

---

## 1. Current Problem

### `updatePoolsFromSync` rebuilds pools from scratch

Every 60s (Portal sync interval), `updatePoolsFromSync()` in `manager.go` creates **brand new** `ProviderPool` and `KeyInternal` instances for every provider. This destroys all runtime state accumulated since the last sync.

**Fields lost on every sync:**

| Field | Meaning | Impact of reset |
|-------|---------|-----------------|
| `Failures` | Consecutive failure count | Keys that should be excluded are re-admitted |
| `CooldownUntil` | Manual/auto cooldown deadline | Keys in cooldown become instantly available |
| `RateLimitUntil` | Upstream RPM/TPM exhaustion cooldown | Rate-limited keys resurrect immediately |
| `LastUsed` | LRU timestamp | Load balancing degrades to random selection |
| `StatusHistory` | State transition audit log | Operational visibility lost |
| `FailureHistory` | Failure event log | Root-cause analysis data erased |
| `TotalRequests` | Cumulative request count | Usage statistics归零 |
| `AvgLatencyMs` | Moving average latency | Performance metrics归零 |
| `InactiveInfo` | Inactive tracking (24h purge) | Purge logic may misfire |

### Why this was not visible before

- Portal upstream-key mutation rate is low in normal usage
- The 60s sync interval masked the issue
- Redis Pub/Sub (Task 7) now triggers **instant** sync on every admin action → the bug is now much more visible

---

## 2. Root Cause

```go
// manager.go:432 — current implementation
func (m *Manager) updatePoolsFromSync(keys []Key) {
    for provider, pkeys := range providerKeys {
        pool := &ProviderPool{
            Provider:     provider,
            Keys:         make([]*Key, 0, len(pkeys)),
            internalKeys: make([]*KeyInternal, 0, len(pkeys)),
        }
        // Every Key and KeyInternal is NEW — zero runtime state
        for i := range pkeys {
            key := &pkeys[i]
            internal := &KeyInternal{Key: key, ActualKey: key.Key}
            pool.internalKeys = append(pool.internalKeys, internal)
        }
        m.setPool(provider, pool)  // Replaces old pool entirely
    }
}
```

**The `Key` struct mixes static data (from Portal) and runtime data (local state) in one flat structure.** When sync rebuilds the struct, there is no place to preserve the runtime fields.

---

## 3. Proposed Solution: Embedded Structs

### Design Principle

Separate **static** and **runtime** fields **logically** while keeping them **physically contiguous** in memory. This gives us:

- ✅ Cache locality (all key data in adjacent memory)
- ✅ Zero pointer-chasing overhead on the hot path
- ✅ Clean merge logic at sync time (`key.KeyStatic = fresh`)
- ✅ Minimal GC pressure (no extra heap objects)

### New Type Layout

```go
// KeyStatic — fields sourced from Portal sync (read-only at runtime)
type KeyStatic struct {
    ID             string   // Portal-assigned UUID
    Provider       string   // e.g. "openai", "microsoft"
    Key            string   `json:"-"`  // Actual API key (sensitive)
    KeyPreview     string   // e.g. "sk-abcd...xyz"
    Tier           string   // "premium" | "enterprise" | "economy"
    Priority       string   // "high" | "normal" | "economy"
    BaseURL        string   // Custom endpoint override
    Models         []string // Whitelist of supported models
    Notes          string
    Enabled        bool     // Portal toggle
    IsShared       bool     // Shared across proxies?
    AssignmentMode string   // "exclusive" | "shared"
    EconomyTiers   []string
    RPMLimit       int      // Upstream RPM cap
    TPMLimit       int      // Upstream TPM cap
}

// KeyRuntime — fields maintained locally by the Proxy
type KeyRuntime struct {
    Status         string        // "active" | "cooldown" | "inactive" | "purged"
    StatusHistory  []StatusEntry // Audit trail of state changes
    Failures       int           // Consecutive failure counter
    FailureHistory []FailureEntry
    LastFailure    time.Time
    CooldownUntil  time.Time     // Until when this key is unavailable
    RateLimitUntil time.Time     // Until when upstream RPM/TPM resets
    InactiveInfo   *InactiveInfo // 24h purge tracking
    PurgedFromPool bool
    PurgedAt       time.Time
    RPM            int           // Current window RPM usage
    TPM            int64         // Current window TPM usage
    TotalRequests  int64         // Lifetime request count
    AvgLatencyMs   int           // Moving average latency
    LastUsed       time.Time     // For LRU selection
}

// Key — single struct, zero-overhead embedding
type Key struct {
    KeyStatic
    KeyRuntime
}

// KeyInternal — wrapper with the decrypted actual key
type KeyInternal struct {
    *Key       // Embedded Key (static + runtime)
    ActualKey  string  // The real upstream API key
}
```

### Memory Layout Visualization

```
KeyInternal (heap object)
├─ *Key ──────────────────────► Key (heap object)
│   ├─ KeyStatic (inline)
│   │   ├─ ID          string
│   │   ├─ Provider    string
│   │   ├─ ...         (contiguous)
│   │   └─ RPMLimit    int
│   │
│   └─ KeyRuntime (inline)
│       ├─ Status      string
│       ├─ Failures    int
│       ├─ ...         (contiguous)
│       └─ LastUsed    time.Time
│
└─ ActualKey  string
```

**All fields of `KeyStatic` and `KeyRuntime` live in the same contiguous memory block.** Accessing `key.LastUsed` or `key.Enabled` is a single base-pointer + offset operation — no indirection.

---

## 4. Sync Merge Algorithm

### Goal

On every sync, replace **only** the `KeyStatic` portion while preserving **all** `KeyRuntime` state for keys that still exist.

### Algorithm

```go
func (m *Manager) updatePoolsFromSync(syncedKeys []Key) {
    // Index fresh keys by ID for O(1) lookup
    freshByID := make(map[string]KeyStatic, len(syncedKeys))
    for _, k := range syncedKeys {
        freshByID[k.ID] = k.KeyStatic
    }

    m.poolsMu.Lock()
    defer m.poolsMu.Unlock()

    for provider, pool := range m.pools {
        lock := m.getLock(provider)
        lock.Lock()

        // ---- Pass 1: Update existing keys, remove deleted keys ----
        var survivors []*KeyInternal
        for _, internal := range pool.internalKeys {
            if freshStatic, ok := freshByID[internal.ID]; ok {
                // Key still exists in Portal — merge static data
                internal.KeyStatic = freshStatic
                // KeyRuntime is preserved automatically (same struct, untouched fields)
                survivors = append(survivors, internal)
                delete(freshByID, internal.ID) // Mark as processed
            } else {
                // Key deleted from Portal — drop it entirely
                // (KeyInternal and *Key become GC-eligible)
            }
        }

        // ---- Pass 2: Add newly created keys ----
        for id, freshStatic := range freshByID {
            if freshStatic.Provider != provider {
                continue // Belongs to another provider
            }
            newKey := &Key{KeyStatic: freshStatic}
            newInternal := &KeyInternal{Key: newKey, ActualKey: freshStatic.Key}
            survivors = append(survivors, newInternal)
            delete(freshByID, id)
        }

        pool.internalKeys = survivors
        pool.Keys = rebuildKeysSlice(survivors) // For JSON serialization
        pool.TotalKeys = len(survivors)
        pool.ActiveKeys = countActive(survivors)

        lock.Unlock()
    }

    // ---- Pass 3: Create pools for new providers ----
    for id, freshStatic := range freshByID {
        // ... create new ProviderPool
    }
}
```

### What happens to each field category

| Category | Sync Behavior | Example fields |
|----------|--------------|----------------|
| **Portal source of truth** | Overwritten with fresh data | `Enabled`, `BaseURL`, `Models`, `Tier`, `RPMLimit`, `TPMLimit` |
| **Runtime preserved** | Never touched by sync | `Failures`, `CooldownUntil`, `RateLimitUntil`, `LastUsed`, `TotalRequests`, `AvgLatencyMs` |
| **Runtime reset on change** | Conditionally cleared | `Status` → reset to `"active"` if Portal re-enables a disabled key; `Failures` → reset to 0 if `Tier` changes |

---

## 5. Performance Analysis @ 100K RPM

### Target

- **100,000 RPM** = **1,667 requests/second**
- Single-machine deployment (Portal + Proxy + Redis + PostgreSQL)

### Memory Footprint

```
Key struct:
  KeyStatic  ~250 bytes  (strings are references; actual string data lives elsewhere)
  KeyRuntime ~280 bytes  (time.Time × 6 = 144B; slices = 48B; ints = 64B)
  ─────────────────────
  Total Key  ~530 bytes

KeyInternal wrapper:
  *Key        8 bytes
  ActualKey  ~64 bytes  (average upstream key string length)
  ─────────────────────
  Total      ~600 bytes / key

@ 250 keys: 250 × 600 B = 150 KB
@ 500 keys: 500 × 600 B = 300 KB
```

**Key pool memory is < 0.3 MB — negligible compared to HTTP client (~50 MB) and Redis client (~10 MB).**

### CPU: `GetKeyByPriority` Hot Path

Called once per request. Complexity per call:

```
1. m.getPool(provider)          — map lookup              ~ 50 ns
2. lock.Lock()                   — mutex acquire            ~100 ns (no contention)
3. Loop over internalKeys:
   - check Enabled               — field read               ~  1 ns
   - check CooldownUntil         — time.Compare             ~  5 ns
   - check RateLimitUntil        — time.Compare             ~  5 ns
   - check Models (slice loop)   — 0-3 iterations           ~ 10 ns
   - check LastUsed (compare)    — time.Compare             ~  5 ns
   ─────────────────────────────────────────────────────────────
   Per key: ~30 ns
   
   @ 15 keys/provider average: 15 × 30 ns = 450 ns
4. lock.Unlock()                 — mutex release            ~ 50 ns

Total per request: ~650 ns
```

**@ 1,667 RPS: 1,667 × 650 ns = 1.08 ms CPU time / second = 0.1% of one core.**

### CPU: Sync Merge

Every 60s, merge ~250 keys:

```
Per key merge:
  - map lookup (freshByID)       ~ 50 ns
  - KeyStatic assignment (copy)  ~ 50 ns  (250 bytes of value copy)
  
250 keys × 100 ns = 25,000 ns = 25 μs
```

**Sync merge blocks the provider lock for ~25 microseconds every 60 seconds — completely negligible.**

### Conclusion: Key Pool is NOT the Bottleneck

| Component | Estimated Load | Risk |
|-----------|---------------|------|
| Key pool struct access | 0.1% CPU | ✅ None |
| Key pool memory | 0.3 MB | ✅ None |
| Sync merge lock hold | 25 μs / 60s | ✅ None |
| **Provider-level mutex** | 1,667 RPS ÷ ~20 providers = **83 RPS/provider avg** | ⚠️ Moderate — hot providers (e.g. OpenAI at 500+ RPS) will see contention |
| **Redis rate limit sync** | 1,667 writes/s | ⚠️ Moderate — single Redis instance handles ~10K ops/s easily, but should use pipeline |
| **HTTP connection pool** | 1,667 concurrent upstream requests | 🔴 High — current `MaxIdleConns=100` is too low for 100K RPM |
| **GC pressure** | Per-request transient objects | 🔴 High — request/response bodies need `sync.Pool` |

---

## 6. Upstream Key Count Estimation @ 100K RPM

### Assumptions

| Supplier | Avg RPM / Key | Mix Ratio | Weighted RPM |
|----------|--------------|-----------|--------------|
| OpenAI (Tier 2-3) | 2,000 | 30% | 600 |
| Azure OpenAI | 1,000 | 20% | 200 |
| Anthropic | 1,000 | 20% | 200 |
| AWS Bedrock | 1,000 | 15% | 150 |
| Domestic (China) | 500 | 15% | 75 |
| **Weighted average** | | | **1,225** |

### Calculation

```
Raw keys needed = 100,000 RPM ÷ 1,225 RPM/key = ~82 keys

With availability buffer (20% unavailable at any time):
  82 ÷ 0.80 = ~102 keys

With diversity buffer (don't concentrate on one provider):
  +20% = ~125 keys
```

**Conservative estimate: 125 ~ 250 upstream keys.**

If primarily using high-tier keys (3,000+ RPM), this drops to **50 ~ 100 keys**.

---

## 7. Implementation Checklist

### Phase A: Structural Refactor (blocking)

- [ ] Define `KeyStatic` and `KeyRuntime` structs
- [ ] Refactor `Key` to embed both
- [ ] Refactor `KeyInternal` to hold `*Key`
- [ ] Update all field accessors across the codebase (`key.Enabled` → still works via embedding)
- [ ] Update JSON serialization: `Key.Key` should still have `json:"-"`

### Phase B: Sync Merge Logic (blocking)

- [ ] Rewrite `updatePoolsFromSync` with merge algorithm
- [ ] Handle new keys (add to pool)
- [ ] Handle deleted keys (remove from pool)
- [ ] Handle existing keys (replace `KeyStatic`, preserve `KeyRuntime`)
- [ ] Add conditional reset: if Portal re-enables a key → reset `Failures` to 0

### Phase C: 100K RPM Hardening (non-blocking, can be deferred)

- [ ] Tune `MaxIdleConns` from 100 → 500+
- [ ] Add `sync.Pool` for request/response byte buffers
- [ ] Batch Redis rate limit sync with pipeline
- [ ] Consider lock-free or sharded-lock optimization for hot providers

---

## 8. Open Questions

1. **Conditional Runtime Reset:** Should `Failures` be reset to 0 when `Tier` changes (e.g. Portal upgrades a key from economy to premium)?
2. **Inactive Purge Interaction:** If a key is `purged_from_pool=true` locally but still exists in Portal, should sync resurrect it or leave it purged?
3. **Redis Persistence:** `persistToRedis` / `loadFromRedis` also need updating — `Key` has `json:"-"` on the `Key` field so actual keys are lost. Should we persist `KeyInternal` separately or fix JSON tags?

---

## Appendix A: Current `Key` Struct (for reference)

```go
// pkg/keypool/manager.go:53
type Key struct {
    ID            string    `json:"id"`
    Provider      string    `json:"provider"`
    Key           string    `json:"-"`
    KeyPreview    string    `json:"key_preview"`
    Tier          string    `json:"tier"`
    Priority      string    `json:"priority"`
    BaseURL       string    `json:"baseUrl,omitempty"`
    Models        []string  `json:"models,omitempty"`
    Notes         string    `json:"notes,omitempty"`
    Enabled       bool      `json:"enabled"`
    AssignmentMode string   `json:"assignmentMode"`
    IsShared      bool      `json:"isShared"`
    EconomyTiers  []string  `json:"economyTiers,omitempty"`
    Status        string    `json:"status"`
    StatusHistory []StatusEntry `json:"status_history,omitempty"`
    Failures      int       `json:"failures"`
    FailureHistory []FailureEntry `json:"failure_history,omitempty"`
    LastFailure   time.Time `json:"last_failure,omitempty"`
    CooldownUntil time.Time `json:"cooldown_until,omitempty"`
    RateLimitUntil time.Time `json:"rate_limit_until,omitempty"`
    InactiveInfo  *InactiveInfo `json:"inactive_info,omitempty"`
    PurgedFromPool bool   `json:"purged_from_pool"`
    PurgedAt      time.Time `json:"purged_at,omitempty"`
    RPM           int       `json:"rpm"`
    TPM           int       `json:"tpm"`
    RPMLimit      int       `json:"rpm_limit"`
    TPMLimit      int       `json:"tpm_limit"`
    TotalRequests int64     `json:"total_requests"`
    AvgLatencyMs  int       `json:"avg_latency_ms"`
    LastUsed      time.Time `json:"last_used"`
}
```

---

## Appendix B: Relevant Files

| File | Purpose |
|------|---------|
| `pkg/keypool/manager.go` | Core pool logic, `updatePoolsFromSync`, `GetKeyByPriority` |
| `pkg/keypool/manager.go:53` | `Key` struct definition |
| `pkg/keypool/manager.go:165` | `KeyInternal` struct definition |
| `pkg/keypool/manager.go:432` | `updatePoolsFromSync` (buggy) |
| `pkg/keypool/manager.go:1440` | `StartSyncLoop` (60s ticker) |
| `internal/server/handlers.go:220` | `AdaptorConfig` creation with upstream key |
| `internal/ratelimit/ratelimit.go` | User-level RPM/TPM rate limiting |


---

## 9. Post-Implementation Discovery: Upstream Runtime Visibility Gap

**Date:** 2026-05-06 13:30 UTC  
**Context:** After implementing the `KeyStatic`/`KeyRuntime` merge, we verified that 9 real upstream keys (Moonshot, Minimax, Xiaomi) are correctly pooled and serving live traffic. During this verification, two structural deficiencies in the Portal↔Proxy data flow were identified.

---

### 9.1 Problem Statement

The current data flow is **unidirectional and incomplete**:

```
Portal ──sync──► Proxy              Proxy ──audit──► Portal
(static only)                       (user-level only)
  KeyStatic                            UsageReport
  RPMLimit                             apiKeyHash
  Models                               inputTokens
  Enabled                              outputTokens
                                       cost
                                       latencyMs
```

**What is missing:**

1. **Proxy → Portal: Upstream key runtime state** — Dashboard cannot see which upstream key is under pressure, in cooldown, or failing
2. **Proxy → Portal: Upstream key usage records** — No independent record exists to cross-check provider invoices

---

### 9.2 Deficiency 1: Dashboard is Blind to Upstream Pressure

#### Current State

Portal has a `key_pool_status` table:

```sql
CREATE TABLE key_pool_status (
    provider     VARCHAR(50) NOT NULL,
    key_id       VARCHAR(50) NOT NULL,
    key_preview  VARCHAR(20),
    rpm          INT DEFAULT 0,
    tpm          INT DEFAULT 0,
    status       VARCHAR(20) DEFAULT 'active',
    updated_at   TIMESTAMP DEFAULT NOW(),
    PRIMARY KEY (provider, key_id)
);
```

And a webhook endpoint `/api/webhook/key-pool-status` exists in Portal (`ai-portal-go/internal/routes/webhook.go:31`).

But **Proxy never sends data to this endpoint**. The Proxy-side handler (`ai-proxy-go/internal/server/admin_handlers.go:428`) is a no-op stub that only returns `{"success": true}`.

#### Impact

| What Admin wants to see | Can Portal show it? |
|------------------------|---------------------|
| "Which provider is at 90% RPM capacity right now?" | ❌ No |
| "Which upstream key is in cooldown?" | ❌ No |
| "What's the failure rate of my Minimax keys this hour?" | ❌ No |
| "Should I add more OpenAI keys?" | ❌ No data to support the decision |

#### Solution: P0 — Periodic Key Pool Status Report

**Proxy side** (`pkg/keypool/manager.go`):

Add a `ReportKeyPoolStatus()` method invoked by a 30s ticker:

```go
// KeyPoolStatusReport — snapshot of a single key's runtime state
type KeyPoolStatusReport struct {
    Provider      string `json:"provider"`
    KeyID         string `json:"keyId"`
    KeyPreview    string `json:"keyPreview"`
    RPM           int    `json:"rpm"`
    TPM           int    `json:"tpm"`
    Status        string `json:"status"`          // active | cooldown | inactive
    Failures      int    `json:"failures"`
    AvgLatencyMs  int    `json:"avgLatencyMs"`
    TotalRequests int64  `json:"totalRequests"`
    CooldownUntil int64  `json:"cooldownUntil"`   // Unix timestamp, 0 if not in cooldown
}

func (m *Manager) reportKeyPoolStatus() {
    m.poolsMu.RLock()
    allReports := make([]KeyPoolStatusReport, 0, 256)
    for provider, pool := range m.pools {
        // Note: we only RLock the pool; we read atomic-ish int fields
        for _, ik := range pool.internalKeys {
            k := ik.Key
            var cooldown int64
            if !k.CooldownUntil.IsZero() {
                cooldown = k.CooldownUntil.Unix()
            }
            allReports = append(allReports, KeyPoolStatusReport{
                Provider:      provider,
                KeyID:         k.ID,
                KeyPreview:    k.KeyPreview,
                RPM:           k.RPM,
                TPM:           k.TPM,
                Status:        k.Status,
                Failures:      k.Failures,
                AvgLatencyMs:  k.AvgLatencyMs,
                TotalRequests: k.TotalRequests,
                CooldownUntil: cooldown,
            })
        }
    }
    m.poolsMu.RUnlock()

    // POST to Portal in a background goroutine
    go m.sendKeyPoolStatusToPortal(allReports)
}
```

**Portal side** (`internal/routes/webhook.go`):

`handleKeyPoolStatus()` already exists. It needs to be wired to write into `key_pool_status`:

```go
func (r *WebhookRoutes) handleKeyPoolStatus(c *gin.Context) {
    var req struct {
        Statuses []struct {
            Provider      string `json:"provider"`
            KeyID         string `json:"keyId"`
            KeyPreview    string `json:"keyPreview"`
            RPM           int    `json:"rpm"`
            TPM           int    `json:"tpm"`
            Status        string `json:"status"`
            Failures      int    `json:"failures"`
            AvgLatencyMs  int    `json:"avgLatencyMs"`
            TotalRequests int64  `json:"totalRequests"`
            CooldownUntil int64  `json:"cooldownUntil"`
        } `json:"statuses"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
        return
    }

    for _, s := range req.Statuses {
        _ = r.db.Exec(c.Request.Context(),
            `INSERT INTO key_pool_status (provider, key_id, key_preview, rpm, tpm, status, updated_at)
             VALUES ($1, $2, $3, $4, $5, $6, NOW())
             ON CONFLICT (provider, key_id) DO UPDATE SET
               key_preview = EXCLUDED.key_preview,
               rpm = EXCLUDED.rpm,
               tpm = EXCLUDED.tpm,
               status = EXCLUDED.status,
               updated_at = EXCLUDED.updated_at`,
            s.Provider, s.KeyID, s.KeyPreview, s.RPM, s.TPM, s.Status,
        )
    }
    c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(req.Statuses)})
}
```

---

### 9.3 Deficiency 2: No Independent Upstream Usage Records

#### Current State

Proxy's `audit.Logger` sends `UsageReport` to Portal's `/api/webhook/usage-batch`. Each report includes:

```go
type UsageReport struct {
    TraceID       string  // Request trace
    APIKeyHash    string  // USER's API key hash (billing target)
    Model         string
    Provider      string  // Provider name (e.g. "openai")
    InputTokens   int
    OutputTokens  int
    Cost          float64
    LatencyMs     int
    KeyID         string  // Upstream key ID (currently only stored as string in usage_records)
    ...
}
```

Portal's `processRecord()` (`webhook.go:189`):
- Deducts user subscription / balance
- Inserts into `usage_records` (user-level)
- Updates `api_keys.total_requests` (user key stats)

**It does NOT:**
- Update any column in `upstream_keys`
- Maintain any upstream-key-level accumulated statistics

#### Impact

| Scenario | Current Capability |
|----------|-------------------|
| OpenAI invoice says "Key sk-abc... used $500 in June" | ❌ Cannot verify — no upstream key-level cost accumulation |
| Provider double-charges for cached tokens | ❌ Cannot detect — no independent token count per upstream key |
| Need to know which upstream key is most cost-efficient | ❌ No data — `AvgLatencyMs` and `TotalRequests` only live in Proxy memory |
| Proxy restarts | ❌ All local runtime stats reset (Redis `loadFromRedis` is currently broken) |

#### Solution: P1 — Upstream Usage Accumulation

**Step 1: New table in Portal**

```sql
CREATE TABLE upstream_usage_stats (
    upstream_key_id     VARCHAR(50) PRIMARY KEY,
    provider            VARCHAR(50) NOT NULL,
    total_requests      BIGINT DEFAULT 0,
    total_input_tokens  BIGINT DEFAULT 0,
    total_output_tokens BIGINT DEFAULT 0,
    total_cost          NUMERIC(12,4) DEFAULT 0,
    avg_latency_ms      INT DEFAULT 0,
    last_used_at        TIMESTAMP,
    updated_at          TIMESTAMP DEFAULT NOW()
);

-- For time-series analysis (optional, can be added later)
CREATE TABLE upstream_usage_daily (
    date            DATE NOT NULL,
    upstream_key_id VARCHAR(50) NOT NULL,
    provider        VARCHAR(50) NOT NULL,
    requests        BIGINT DEFAULT 0,
    input_tokens    BIGINT DEFAULT 0,
    output_tokens   BIGINT DEFAULT 0,
    cost            NUMERIC(12,4) DEFAULT 0,
    PRIMARY KEY (date, upstream_key_id)
);
```

**Step 2: Extend Portal `processRecord()`**

In `webhook.go:processRecord()`, after inserting `usage_records`, add:

```go
// Update upstream key accumulated stats
if record.KeyID != "" {
    _, _ = tx.Exec(ctx,
        `INSERT INTO upstream_usage_stats
           (upstream_key_id, provider, total_requests, total_input_tokens,
            total_output_tokens, total_cost, avg_latency_ms, last_used_at, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
         ON CONFLICT (upstream_key_id) DO UPDATE SET
           provider = EXCLUDED.provider,
           total_requests = upstream_usage_stats.total_requests + EXCLUDED.total_requests,
           total_input_tokens = upstream_usage_stats.total_input_tokens + EXCLUDED.total_input_tokens,
           total_output_tokens = upstream_usage_stats.total_output_tokens + EXCLUDED.total_output_tokens,
           total_cost = upstream_usage_stats.total_cost + EXCLUDED.total_cost,
           avg_latency_ms = (upstream_usage_stats.avg_latency_ms * upstream_usage_stats.total_requests
                             + EXCLUDED.avg_latency_ms * EXCLUDED.total_requests)
                            / (upstream_usage_stats.total_requests + EXCLUDED.total_requests),
           last_used_at = EXCLUDED.last_used_at,
           updated_at = NOW()`,
        record.KeyID, record.Provider, record.RequestCount,
        record.InputTokens, record.OutputTokens, record.Cost, record.LatencyMs,
    )
}
```

**Step 3: Ensure Proxy audit logger sends `KeyID`**

Verify `handlers.go` populates `KeyID` in the `AuditEntry` passed to `AuditLogger.Log()`:

```go
// In handleChatCompletions, after selecting upstream key
deps.AuditLogger.Log(audit.AuditEntry{
    KeyID:         upstreamKey.ID,        // ← must be set
    Provider:      provider,
    InputTokens:   usage.InputTokens,
    OutputTokens:  usage.OutputTokens,
    LatencyMs:     latency,
    ...
})
```

---

### 9.4 P0 and P1 are Independent

| | P0 | P1 |
|---|---|---|
| **Trigger** | 30s timer | Per-request (post-completion) |
| **Data type** | State snapshot | Event accumulation |
| **Example payload** | `{"rpm": 120, "status": "cooldown"}` | `{"inputTokens": 256, "cost": 0.002}` |
| **Portal storage** | `key_pool_status` (current state, overwritten) | `upstream_usage_stats` (cumulative, incremented) |
| **Purpose** | Dashboard real-time pressure | Invoice reconciliation |
| **Can work alone?** | ✅ Yes | ✅ Yes |
| **Dependencies** | None | None |

**Recommended: implement both.** They solve different problems and can be developed in parallel.

---

### 9.5 Implementation Plan

| Phase | Task | Files | Effort |
|-------|------|-------|--------|
| **P0-A** | Add `reportKeyPoolStatus()` to Manager | `pkg/keypool/manager.go` | Small |
| **P0-B** | Wire `handleKeyPoolStatus` in Portal webhook | `internal/routes/webhook.go` | Small |
| **P0-C** | Add 30s ticker in `StartSyncLoop` | `pkg/keypool/manager.go` | Small |
| **P1-A** | Create `upstream_usage_stats` migration | `migrations/` | Small |
| **P1-B** | Extend `processRecord()` with UPSERT | `internal/routes/webhook.go` | Small |
| **P1-C** | Verify `KeyID` is set in all audit calls | `internal/server/handlers.go` | Small |
| **P2** | Fix `loadFromRedis` (broken persistence) | `pkg/keypool/manager.go` | Medium |

---

### 9.6 Security Considerations

1. **KeyPoolStatus webhook** must use the same `x-proxy-secret` authentication as the sync endpoint
2. **KeyPoolStatus payload** must NOT include `ActualKey` — only `KeyPreview` and `KeyID` are safe to transmit
3. **Upstream usage stats** should not be exposed to end users — only system admins should see provider-level cost data

---

## 10. Updated Open Questions

1. **P0 granularity:** Should we report ALL keys every 30s, or only keys whose status changed since last report?
2. **P1 latency window:** The `avg_latency_ms` in `upstream_usage_stats` is a lifetime average. Should we also keep a 24h rolling average for operational alerting?
3. **P2 priority:** `loadFromRedis` is currently broken (unexported `internalKeys` + `json:"-"` on `Key` field). Should this be fixed before P0/P1, or is in-memory-only acceptable for now?
4. **Provider invoice reconciliation:** Should we add a `provider_invoice_id` column to `upstream_usage_daily` for explicit cross-referencing?
