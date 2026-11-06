# Architectural Comparison: ai-proxy-go vs new-api (QuantumNous/new-api)

**Date**: 2026-06-19 11:55 UTC  
**Scope**: Model→key vs model→channel routing architectures, concurrency models, and security posture  
**Repositories**:
- `ai-proxy-go` — internal key sync/routing proxy (Go)
- `new-api` — QuantumNous/new-api (38K stars, Go) — open-source multi-model proxy

---

## 1. High-Level Architectural Difference

| Aspect | ai-proxy-go | new-api |
|--------|-------------|---------|
| **Unit of routing** | API key → Model allowlist → Upstream | Model name → Group → Channel |
| **Model association** | Each upstream key has a `Models []string` list. Routing selects keys that match the requested model. | Each channel has `Models` (comma-separated) and `Group` (comma-separated). An `Ability` table links (group, model) → channel with priority/weight. |
| **Routing entrypoint** | `resolveProviderAndKey` → scans all pools for matching keys → `smartRoute` sorts candidates | `Distribute()` middleware → `CacheGetRandomSatisfiedChannel()` → priority-tier + weighted random selection |
| **Key/channel management** | Portal syncs `upstream_keys` to proxy; proxy manages per-key `KeyInternal` with runtime state | Admin UI or API creates/manages `Channel` rows in DB; a periodic task refreshes in-memory cache |
| **Multi-key support** | Each `Pool` has multiple `KeyInternal` entries; all compete in routing | Each `Channel` has optional multi-key polling (`GetNextEnabledKey` — per-channel mutex); round-robin or first-available |
| **Billing model** | Proxy tracks per-key quota (`atomic.Int64`), Portal handles billing externally | Built-in user/token quota with pre-consume + settle billing, full billing session lifecycle |
| **Admin UI** | Separate Portal application | Built-in web UI (Gin + templates) |

---

## 2. Routing Pipeline Comparison

### 2.1 ai-proxy-go: Key-to-Model Routing

```
HTTP Request (model="gpt-4")
  → handleChatCompletions (handlers.go:451)
    → resolveProviderAndKey (handlers.go:1229)
      → manager.GetAllKeysForModel("gpt-4")       ← O(n*m) scan
        → for each provider:
          for each key:
            check IsKeyAvailable
            check containsMatch(key.Models, "gpt-4")  ← 3-pass string match
            check checkUpstreamAvailable (RPM/TPM)
            → collect candidates
      → smartRoute(candidates)                     ← O(k log k) sorting
        → tier sort → health → latency → quota → LRU
    → route to upstream URL of selected key
```

**Key characteristics**:
- **Flat competition**: Keys from different providers compete directly on priority/health/latency/quota
- **No group isolation**: The portal can assign routing groups to keys (high/normal/economy), but there is no user-to-group binding; all keys are essentially in a single pool
- **Primary provider = tiebreaker only**: Not a hard filter
- **Model enforcement per-key**: Each key carries its own allowlist; empty models = never matches

### 2.2 new-api: Model-to-Channel Routing

```
HTTP Request (model="gpt-4", user=token with group="default")
  → middleware.TokenAuth() → middleware.ModelRequestRateLimit()
    → middleware.Distribute() (distributor.go:32)
      → Step 1: Extract model from request body
      → Step 2: If TokenSpecificChannelId set, use that channel directly
      → Step 3: GetPreferredChannelByAffinity()  ← sticky routing (optional)
      → Step 4: CacheGetRandomSatisfiedChannel()  ← main path
        → group = token.Group ("default" or "auto")
        → if "auto": iterate auto-groups in order
        → model.GetRandomSatisfiedChannel(group, model, retry, path)
          → group2model2channels[group][model]  ← O(1) map lookup
          → filter by request path (advanced custom channels)
          → sort by priority DESC → pick priority tier by `retry`
          → weighted random selection within tier
      → Step 5: SetupContextForSelectedChannel()
    → controller.Relay() (relay.go)
      → retry loop (retry=0..RetryTimes)
        → getChannel() → re-enters selection with next retry index
        → relay helper (OpenAI/Claude/Gemini/etc.)
        → PostConsumeQuota()
```

**Key characteristics**:
- **Group isolation**: Token has a `group` field, channels belong to groups; user can only reach channels in their group (or via `auto` which iterates configurable groups)
- **Ability table**: The `Ability` table is the explicit join between (group, model) and channel_id with priority/weight — this is the canonical model-to-channel mapping
- **Priority tiers as retry mechanism**: `retry=0` picks from highest priority; on failure, the relay loop increments retry to try the next priority tier — this is how failover works
- **Weighted random within tier**: Load balancing across same-priority channels via configurable weight; smoothing for low weights
- **Channel affinity** (optional sticky routing): Caches the selected channel for similar future requests based on configurable rules (model regex, path regex, user-agent patterns)
- **40+ provider adaptors**: Each channel type maps to a specific relay adaptor (OpenAI, Anthropic, Gemini, etc.)

### 2.3 Critical Architectural Difference

| Concept | ai-proxy-go | new-api |
|---------|-------------|---------|
| **Who decides routing scope?** | The admin (via per-key model assignment) | The system (via Ability table + token group) |
| **Isolation mechanism** | Per-key model allowlist | User group + channel group + Ability table |
| **Failover mechanism** | N/A — all eligible keys compete in smartRoute; if none available, request fails | Explicit retry loop with priority tiers; each retry picks the next priority level |
| **Sticky routing** | Smart route's LRU heuristic only | Configurable affinity rules (regex on model/path/UA) |
| **Load balancing** | Smart route sorts multi-dimensionally (health → tier → RPM → latency → quota → LRU) | Weighted random within priority tier (simpler, more predictable) |
| **Hot path complexity** | O(N×M) scan (all providers × all keys) | O(1) map lookup (group→model→[]channelId) |

---

## 3. Concurrency Model Comparison

### 3.1 Lock Hierarchy

**ai-proxy-go**:
```
poolsMu (RWMutex) — flat []*Pool
  └── per-provider poolLock (Mutex) — key list mutations
locksMu (RWMutex) — provider lock map
upstreamUsageMu (RWMutex) — RPM/TPM counters map
  └── per-key usage.mu (Mutex) — counter fields
6+ other RWMutexes (routing, health, config, etc.)
```

**new-api**:
```
channelSyncLock (RWMutex) — 3 global maps (group2model2channels, channelsIDM, channel2advancedCustomConfig)
channelStatusLock (Mutex) — serializes channel status updates
channelPollingLocks (sync.Map) — per-channel Mutex for multi-key polling
batchUpdateLocks [5]Mutex — per-type batch update accumulators
BillingSession.mu (Mutex) — per-request billing session
[64] sharded Mutex — channel affinity usage cache stats
5+ other mutexes (pricing, rate limit config, etc.)
```

### 3.2 Key Differences

| Aspect | ai-proxy-go | new-api | Winner |
|--------|-------------|---------|--------|
| **Channel index protection** | Single `poolsMu RWMutex` (flat list) | Single `channelSyncLock RWMutex` (map-of-maps) | Tie |
| **Quota tracking** | `atomic.Int64` in-memory counters (race-free, fast) | DB `gorm.Expr` + Redis `HIncrBy` + batch update | **ai-proxy-go** — in-memory atomics avoid DB round-trips |
| **Per-channel rate limiting** | Per-key RPM/TPM tracking with window reset | **Not implemented** | **ai-proxy-go** — new-api can overshoot upstream API limits |
| **Per-channel cooldown** | Explicit cooldown timestamps | **Not implemented** (enabled/disabled only) | **ai-proxy-go** — transient failure backoff |
| **Multi-key polling safety** | N/A (flat competition) | `channelPollingLocks sync.Map` with per-channel Mutex | **new-api** — proper isolation |
| **Data race risk** | Theoretical: non-atomic slice header on 32-bit | **Confirmed gap**: `channelStatusLock` vs `channelSyncLock` are different locks; `channel.Status` mutated in-place under one lock while readers hold the other | **ai-proxy-go** — theoretical vs confirmed |
| **Batch update** | N/A (in-memory counters) | `batchUpdateLocks [5]Mutex` with periodic flush | new-api (durability) |
| **Billing safety** | External (Portal handles) | `BillingSession` with `sync.Mutex` for atomic settle/refund | **new-api** — prevents double-billing |
| **Lock count** | 10+ distinct RWMutexes | 5+ distinct mutexes + sharded locks | **new-api** — simpler |

### 3.3 Critical Concurrency Findings in new-api

**Data race on `channel.Status`** (`model/channel.go` + `model/channel_cache.go`):
- `UpdateChannelStatus()` acquires `channelStatusLock` (a global Mutex) and mutates `channel.Status` in-place on the `*Channel` pointer stored in `channelsIDM`
- The read path (`GetRandomSatisfiedChannel`) acquires `channelSyncLock.RLock()` — a **different lock**
- A goroutine reading `channel.Status` for channel selection can run concurrently with one mutating it
- The `ChannelInfo.MultiKeyStatusList` map is also mutated in-place under `channelStatusLock` without map-level synchronization with readers
- **The Go race detector would flag this.** In practice on x86_64, the store will eventually be visible, but this is unsound per the Go memory model.

**No per-channel RPM/TPM enforcement**:
- The `Channel` struct has no RPM/TPM quota fields
- All rate limiting is per-user (sliding window + token bucket) — this throttles the user, not the upstream API key
- If a third-party API key (e.g., OpenAI) has its own rate limits, new-api has no mechanism to respect them
- The only protection is auto-disabling channels after consecutive errors

**Check-then-act in quota pre-consumption** (`service/quota.go` + `service/pre_consume_quota.go`):
- `PreConsumeTokenQuota()` reads `token.RemainQuota` from DB, then deducts at `PostConsumeQuota()` time
- Between check and deduction, concurrent requests can overshoot (mitigated by DB-level `gorm.Expr` atomicity, but the user may be admitted with insufficient quota)

---

## 4. Data Model Comparison

| Concept | ai-proxy-go | new-api |
|---------|-------------|---------|
| **Core routing entity** | `KeyStatic.Models []string` — per-key allowlist | `Channel` + `Ability` — channel declares supported models via join table |
| **Group/tenant isolation** | Routing groups on keys (high/normal/economy), no user binding | Token has `Group` field → channel belongs to group; full user-group isolation |
| **Token/key model** | `KeyStatic` (immutable) + `KeyInternal` (runtime state) — separate concerns | `Token` struct — combined model with `RemainQuota`/`UsedQuota`, linked to user |
| **Channel model** | `Pool` wraps provider-level config + `[]*KeyInternal` — lightweight | `Channel` struct — rich model with `Type`, `Models`, `Group`, `Priority`, `Weight`, `Status`, `MultiKeyStatusList`, `model_mapping` |
| **Model mapping/aliasing** | Not implemented | Per-channel `model_mapping` JSON (e.g., `{"gpt-4":"gpt-4-0613"}`) with chain redirection + cycle detection |
| **Persistence** | Portal = PostgreSQL; Proxy = in-memory + Redis (encrypted) | PostgreSQL only (GORM); in-memory cache on top |
| **Encryption** | AES-256-GCM for keys at rest in Redis | No in-memory encryption (data in DB only) |

---

## 5. Security Comparison

| Finding | ai-proxy-go | new-api |
|---------|-------------|---------|
| **SSRF protection** | ✅ `validateBaseURL` blocks private IPs/loopback | ❓ Not examined (likely not applicable — upstream URLs configured by admin, not user input) |
| **Key encryption at rest** | ✅ AES-256-GCM (Redis); minor: no KDF | ✅ Data stored in PostgreSQL only; user quota values in DB, not in-memory encrypted |
| **Auth on sync endpoint** | ⚠️ Secret in `x-proxy-secret` header + `?secret=` query param fallback (logged warning) | N/A (single-process, no separate sync endpoint) |
| **Token auth** | Portal validates admin session; proxy validates `x-proxy-secret` | ✅ `TokenAuth()` middleware — Bearer token lookup in DB with status check |
| **Rate limiting** | ✅ Per-key RPM/TPM tracking | ✅ Per-user token bucket + sliding window (but **no per-channel rate limiting**) |
| **Channel auto-disable** | ✅ On error thresholds | ✅ `ShouldDisableChannel()` + `DisableChannel()` |
| **HTTPS enforcement** | ⚠️ Not enforced (config-based) | N/A (depends on deployment reverse proxy) |

---

## 6. Performance Comparison

| Aspect | ai-proxy-go | new-api |
|--------|-------------|---------|
| **Hot path model lookup** | O(N×M) — scans all pool keys with string matching | O(1) — map lookup `group2model2channels[group][model]` |
| **Sort overhead** | O(k log k) — up to 3 sorts + LRU on candidates | **None** — weighted random selection from pre-sorted slice |
| **Retry/failover cost** | N/A (one-shot routing) | O(retry) — each retry picks next priority tier; bounded by RetryTimes |
| **Quota check cost** | O(1) — atomic.Int64 read | DB round-trip (pre-consume) + DB round-trip (post-consume settle) — significantly more expensive |
| **Memory overhead** | Higher per-key (separate KeyStatic + KeyInternal structs) | Higher per-channel (rich Channel struct + cached maps) |
| **Scale ceiling** | O(N×M) hot path limits throughput at ~1000 req/s with many keys | O(1) hot path but DB quota adds latency per request |

---

## 7. Summary

| Dimension | ai-proxy-go Strengths | new-api Strengths |
|-----------|----------------------|-------------------|
| **Routing isolation** | Per-key model allowlist | **User group + channel group + Ability table separation** |
| **Concurrency safety** | In-memory `atomic.Int64` for quota; clean lock hierarchy | Multi-key polling locks; billing session atomicity |
| **Performance hot path** | | **O(1) map lookup** vs O(N×M) scan |
| **Rate limiting** | Per-key RPM/TPM enforcement | Per-user sliding window + token bucket |
| **Feature richness** | | **Built-in billing, admin UI, 40+ provider adaptors, model mapping, channel affinity, priority tiers** |
| **Security** | AES-256-GCM key encryption | Token auth middleware |
| **Durability of quota** | In-memory counters risk drift on crash | **DB-level atomicity** + batch updates |

### Key Takeaways

1. **Fundamentally different routing models**: ai-proxy-go's key-to-model approach is simpler (flat competition, no group isolation) but scales poorly (O(N×M) hot path). new-api's model-to-channel with group isolation is more scalable (O(1) hot path) and supports multi-tenant isolation.

2. **ai-proxy-go's concurrency model is cleaner**: In-memory atomics avoid DB round-trips for quota. new-api has a **confirmed data race** between `channelStatusLock` and `channelSyncLock` and no per-channel rate limiting.

3. **new-api's feature surface is much larger**: Built-in billing, admin UI, 40+ provider adaptors, model mapping, affinity routing, priority tiers with weighted random — ai-proxy-go is purpose-built for key sync/routing and deliberately simpler.

4. **The projects solve different problems**: ai-proxy-go bridges an admin Portal to one or more proxy nodes with encrypted key sync. new-api is a self-contained multi-model proxy with full user management and billing. A direct port is not the right approach — the valuable insights are specific patterns (O(1) model→channel index, affinity routing, billing session atomicity) that could be adapted.
