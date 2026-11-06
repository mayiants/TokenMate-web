# Routing & Billing Optimization Proposal

> **Date:** 2026-05-24 | **Status:** ACTIVE — Mostly Complete (9/11 items implemented)
>
> **Update 2026-05-25:** Tasks #1-#9 have been implemented and deployed. Tasks #10 (E2E test suite) ✅ and #11 (document update — this file) ✅ completed.
>
> **Update 2026-05-24:** Fallback group billing bug fixed. Proxy now sends `ActualGroupID` + `IsFallback`; Portal webhook handlers trust proxy-sent `PricingRatio` and use `ActualGroupID` for billing. Per-request billing support added (`billing_type`/`fixed_price` on `service_models`).

## Current Issues

### Issue A: Provider Usage Attribution

**Problem:** When multiple providers (laozhang, accessapi, deepseek) all support the same model, the proxy's routing logic selects one based on priority rules. Only the winning provider's stats appear in `upstream_key_usage` and `provider_health`. If laozhang's key is never ranked first for `deepseek-v4-flash` (because deepseek's own key pool has higher priority), laozhang's usage stats remain zero — even though their key IS available and working.

**Root cause in code:**

```
handlers.go:812  primaryProvider := relay.GetProviderFromModel(model)  // "deepseek" for deepseek-*
handlers.go:827  candidates = keyPoolManager.GetAllKeysForModel(model, ...)  // searches ALL providers
handlers.go:871  routingOrder = keyPoolManager.GetRoutingOrder(model)  // admin-configured priority
handlers.go:877  // If no admin config: primaryProvider gets rank 0, others get rank 1
handlers.go:878  candidates sorted by rank, then LRU
handlers.go:898  selected = candidates[0]  ← only the winner is used
```

All providers that support the model are found, but only one wins. The others' keys are never exercised, so no usage/history data accumulates for them.

### Issue B: Routing Priority Doesn't Scale

**Current state:**
- Admin must manually configure `model_provider_routing` per model → per provider (N×M entries)
- With 100+ models × 6+ providers = 600+ manual configurations
- No automatic cost, health, or latency weighting
- `provider_health_stats` data exists but is never consumed by routing decisions

---

## Refined Proposal

### Design Principle: Manual-First, Smart-Fallback

```
                    ┌─────────────────────────┐
                    │   User Request (model)   │
                    └───────────┬─────────────┘
                                │
                    ┌───────────▼─────────────┐
                    │ Admin Manual Config?     │
                    │ (model_provider_routing) │
                    └───────┬───────┬─────────┘
                            │ YES   │ NO
                    ┌───────▼──┐ ┌──▼──────────┐
                    │ Manual   │ │ Smart Routing│
                    │ Priority │ │ (auto-pilot) │
                    └───────┬──┘ └──┬───────────┘
                            │        │
                    ┌───────▼────────▼──────────┐
                    │   Select Best Upstream    │
                    │   Key & Forward Request   │
                    └───────────────────────────┘
```

- **Manual (`model_provider_routing`)**: Admin sets per-model provider order. "I want claude-opus-4-6 to prefer AWS, then fallback to accessapi." This ALWAYS takes priority when configured.
- **Smart Routing**: Kicks in when no manual config exists for a model. Applies automatic rules.

### Smart Routing Rule Chain

Each rule eliminates or ranks candidates. Rules are applied in strict order.

**All metrics (HEALTH, RPM, LATENCY, QUOTA) are per-upstream-key, not per-provider.** Each upstream key tracks its own health (via usage_records), its own RPM consumption, and its own latency. Keys within the same provider may have different metrics.

Each rule has a **toggle switch** in proxy config. Default: all ON. Admin can disable individual rules.

```yaml
routing:
  smart:
    enabled: true
    rules:
      health:  true    # exclude unhealthy keys
      tier:    true    # prefer higher tier (premium > enterprise > default > economy)
      rpm:     true    # exclude keys at RPM capacity
      latency: true    # prefer lower avg latency bucket
      quota:   true    # prefer keys with more remaining quota (only when quota_limit > 0)
      lru:     true    # final tiebreaker (always on, not toggleable)
```

```
Step 1: HEALTH     → Exclude keys with error_rate > 50% (per-key, not per-provider)
Step 2: TIER       → Prefer higher upstream key tier (premium > enterprise > default > economy)
Step 3: RPM        → Exclude keys at RPM/TPM capacity
Step 4: LATENCY    → Prefer lower avg_latency_ms bucket (per-key)
Step 5: QUOTA      → Prefer keys with more remaining quota (only when quota_limit IS SET)
Step 6: LRU        → Least recently used within same bucket (final tiebreaker, always on)
```

**Why Priority > Weighted Distribution:**

| Approach | Pros | Cons |
|----------|------|------|
| **Priority (ordered list)** | Deterministic, easy to debug, consistent results, simple implementation | May overuse one provider, doesn't naturally distribute |
| **Weighted (percentage)** | Good load distribution, natural load balancing | Complex state tracking, hard to tune, unstable with dynamic health changes |

**Decision: Priority-based. Reasons:**
1. In a multi-provider setup, being deterministic is critical for debugging billing discrepancies
2. LRU already provides natural distribution within same-tier keys — over time, all keys get roughly equal usage
3. Health auto-exclusion + RPM capacity check provide adaptive behavior without complex weighting
4. Weighted distribution can be added later as a config option (`routing_strategy: weighted`) if needed
5. Priority is simpler to reason about when troubleshooting "why did this request go to provider X?"

### Rule Details

#### Step 1: HEALTH

Uses `provider_health_stats` synced from Portal every 30s:

```go
type ProviderHealth struct {
    Status       string  // healthy | degraded | unhealthy
    ErrorRate    float64 // 0.0 - 1.0
    AvgLatencyMs int64
}

// Exclude entirely if error_rate >= 50% (status = unhealthy)
// Exclude entirely if error_rate >= 80% regardless of status
if health.ErrorRate >= 0.80 || health.Status == "unhealthy" {
    candidates = exclude(candidates, provider)
}
```

Circuit breaker remains as a separate, faster-reacting safety net (opens after 5 failures within 30s).

#### Step 2: TIER (Upstream Key Priority Tier)

Uses the **existing** `upstream_keys.tier` column (already synced to Proxy). No new column needed.

```sql
-- Already exists in upstream_keys:
-- tier VARCHAR(20) DEFAULT 'default'
-- Values: 'premium', 'enterprise', 'default', 'economy'
```

Mapping to routing priority:

| tier | Priority | Meaning |
|------|----------|---------|
| `premium` | 1 (highest) | Direct official API, best quality |
| `enterprise` | 2 | Enterprise-grade reseller |
| `default` | 3 | Standard reseller |
| `economy` | 4 (lowest) | Budget reseller, higher latency ok |

This is NOT the same as user `access_mode`. The user's access_mode determines which KEY TIERS they can use (e.g., economy users only use economy keys). The TIER in routing determines preference WITHIN the user's eligible key pool.

**Why COST_TIER before RPM:** A premium key that's temporarily at RPM capacity should fall through to normal-tier keys, not stay stuck. If RPM were before TIER, a premium key near capacity would still be selected (and potentially rejected), wasting a request round-trip.

#### Step 3: RPM Capacity (per-key)

Each upstream key has `rpm_limit` and `tpm_limit` configured by admin. The proxy tracks current RPM usage per key.

```go
// Exclude keys with no remaining budget in the current 1-min window
// Only applies when rpm_limit > 0 (admin has configured it)
if key.RPMLimit > 0 && key.RPMRemaining <= 0 {
    candidates = exclude(candidates, key)
}
if key.TPMLimit > 0 && key.TPMRemaining <= 0 {
    candidates = exclude(candidates, key)
}
```

**Toggle:** `routing.smart.rules.rpm: true/false`. Disable if admin doesn't want RPM gating.

#### Step 4: LATENCY (per-key)

Uses per-key `avg_latency_ms` aggregated from `usage_records.latency_ms`. Portal computes per-key average over 5-min rolling window and exposes via health summary endpoint.

**How latency is tracked:**
- Proxy records `latency_ms` per upstream request (already implemented in `handlers.go:482`)
- Aggregated by Portal into `provider_health_stats` per 5-min bucket with `avg_latency_ms`
- Proxy pulls the latest `avg_latency_ms` per provider every 30s from Portal

**Bucket-based comparison** (prevents provider flapping from minor fluctuations):

```go
func latencyBucket(avgMs int64) int {
    switch {
    case avgMs <= 0:   return 0  // unknown, no data
    case avgMs < 800:  return 1  // fast
    case avgMs < 2000: return 2  // normal
    case avgMs < 5000: return 3  // slow
    default:           return 4  // very slow
    }
}

// Prefer lower bucket number. Within same bucket → next rule.
```

**Why buckets not raw ms:** Latency varies ±200ms between requests. Buckets prevent oscillating between providers when latencies are close.

**Cold start:** New providers with no latency data get bucket 0 (unknown) — treated same as "fast" to give them a chance to prove themselves. After 5 minutes of traffic, their real latency is measured.

#### Step 5: QUOTA (per-key)

`upstream_keys.quota_limit` exists in the schema but is currently NULL for all keys. When admin sets a quota limit:

```go
// Only activates when quota_limit > 0 (admin configured)
if key.QuotaLimit > 0 {
    quotaRemaining := key.QuotaLimit - key.QuotaUsed
    // Higher quotaRemaining = preferred
}
// When quota_limit IS NULL → skip this rule entirely
```

**Toggle:** `routing.smart.rules.quota: true/false`. Default ON but has no effect until admin sets `quota_limit` on keys.

#### Step 6: LRU

Final tiebreaker: prefer the least recently used key. This naturally distributes traffic across all keys that survive the filter chain, ensuring ALL providers get exercised.

### Affinity Cache Treatment

**Current behavior:** `AffinityCache` remembers the last key used for (user, model, preferredKeyID) and prefers it on next request. This creates sticky routing — good for cache hit rates, bad for provider distribution.

**With smart routing:** Affinity cache is demoted to the FINAL step within the LRU tiebreaker:

```go
// Within LRU candidates, prefer affinity-matched key
if affinityMatch != nil && affinityMatch.Provider == candidate.Provider {
    return affinityMatch  // same provider, reuse key
}
// Otherwise, use the LRU candidate (may be different provider)
```

**Impact analysis:**

| Concern | Verdict |
|---------|---------|
| Will affinity prevent provider distribution? | No — health/tier/rpm/latency rules run FIRST. Affinity only affects final key selection within the same provider. |
| Will users notice provider changes? | Slightly — if tier or health changes, the request may go to a different provider. This is acceptable; the user's model still works. |
| Cache hit rate impact | Minimal — most API traffic is streaming (no cache). For cached responses, affinity within same provider still works. |
| Cross-provider affinity | Explicitly disabled — if routing selects a different provider than the cached one, the new provider's LRU key is used. |

---

## Billing Impact Assessment

### Current Billing Flow

```
Request → Proxy validates key (Portal) → Route to upstream → Record usage
                                                              │
                                                    ┌─────────▼──────────┐
                                                    │ usage_records        │
                                                    │ - user_id           │
                                                    │ - model             │
                                                    │ - provider (upstream)│ ← records which upstream provider was used
                                                    │ - input/output tokens│
                                                    │ - total_cost         │
                                                    │ - pricing_ratio      │
                                                    └─────────────────────┘
```

### Does smart routing break billing?

**No.** Each request records:
- `user_id` — the end user (unchanged)
- `model` — the model they requested (unchanged)  
- `provider` — the upstream provider that served it (already correctly recorded from `selected.Provider`)
- `tokens`, `cost` — calculated per-request (unchanged)

The billing system doesn't care WHICH upstream provider served the request — it only cares about:
1. How many tokens the end user consumed → deduct from user balance
2. What the cost was → `CalculateCostWithCache()` uses `service_models` pricing

**Upstream cost tracking** (provider reconciliation) is handled separately:
- `upstream_key_usage` records per-provider-per-model-per-day token counts
- `provider_health_stats` records per-provider health metrics
- These already work correctly regardless of routing decisions

### Recommended Billing Enhancement (Future)

Add a `provider_cost_estimate` field to `usage_records` for reconciliation:

```sql
ALTER TABLE usage_records ADD COLUMN upstream_cost_estimate BIGINT DEFAULT 0;
```

This estimates what the upstream provider would charge, enabling margin analysis. Non-blocking.

---

## Implementation Plan

| # | Task | Effort | Risk | Files |
|---|------|--------|------|-------|
| 1 | Portal exposes `GET /api/admin/provider-health/summary` (per-key health + latency) | 20min | Low | admin.go, sync.go |
| 2 | Proxy pulls health+latency data every 30s into `keyHealthMap` | 30min | Low | manager.go, handlers.go |
| 3 | Add `routing.smart.rules.*` toggle config to proxy config struct | 15min | Low | config.go |
| 4 | Refactor `resolveProviderAndKey()`: manual-first, then smart chain | 1.5h | Medium | handlers.go, manager.go |
| 5 | Smart chain with toggle gates: HEALTH → TIER → RPM → LATENCY → QUOTA → LRU | 1.5h | Medium | handlers.go, manager.go |
| 6 | Demote affinity cache to LRU final step (same-provider only) | 20min | Low | handlers.go |
| 7 | Add `quota_limit` field to upstream key create/edit UI (`admin/keypool`) | 20min | Low | admin template, admin_handlers.go |
| 8 | Add upstream key EDIT support (`admin/keypool`) | 30min | Low | admin template, admin_handlers.go |
| 9 | Expand `admin/models` 模型路由配置 — add smart routing toggles + per-model manual provider order | 45min | Low | models.html, models.go, admin.go |
| 10 | E2E test: multi-provider same model, verify distribution | 30min | Low | test script |
| 11 | Update docs | 15min | Low | this doc |

**Total: ~6 hours**

**Data sources (per-key, all already tracked):**
- RPM: `upstream_keys.rpm_limit` + proxy tracks per-key RPM consumption (existing)
- LATENCY: `usage_records.latency_ms` aggregated per-key (existing, needs summary endpoint)
- QUOTA: `upstream_keys.quota_limit` (column exists but UI field missing, currently NULL = unused)
- HEALTH: derived from per-key error rate in `provider_health_stats` (existing)

**No migration needed** — all columns exist. Health summary endpoint is the only new API.

---

## UI Design Notes

### admin/keypool — Upstream Key Editor

Add missing `quota_limit` field + edit support:

```
┌─────────────────────────────────────────────┐
│  添加上行Key / 编辑上行Key                      │
│                                              │
│  Provider:  [laozhang           ▼]           │
│  API Key:   [sk-xxxxxx          ]  (masked)  │
│  Base URL:  [https://api.laozhang.ai/v1   ]  │
│  Models:    [deepseek-v4-flash, gpt-4o, ...]  │
│  Tier:      [default            ▼]           │
│  RPM Limit: [120               ]             │
│  TPM Limit: [                  ] (optional)  │
│  Quota Limit:[                  ] (optional) │ ← NEW
│  Enabled:   [✓]                              │
│                                              │
│  [保存]  [取消]                                │
└─────────────────────────────────────────────┘
```

### admin/models — 模型路由配置 (Enhanced)

Expand existing page to include smart routing controls:

```
┌──────────────────────────────────────────────────────────┐
│  模型路由配置                                              │
│                                                          │
│  ─── 智能路由 (Smart Routing) ───                          │
│  ☑ 启用智能路由 (未手动配置的模型自动使用智能路由)            │
│                                                          │
│  规则开关:                                                 │
│  ☑ HEALTH  排除不健康的上行key                              │
│  ☑ TIER    优先选择高级别key (premium > enterprise > ...)   │
│  ☑ RPM     排除已达RPM/TPM上限的key                         │
│  ☑ LATENCY 优先选择延迟更低的key                             │
│  ☑ QUOTA   优先选择剩余配额更多的key                          │
│                                                          │
│  ─── 手动路由 (Manual Routing) ───                          │
│  手动配置的模型优先级高于智能路由                              │
│                                                          │
│  [+ 添加手动路由规则]                                        │
│                                                          │
│  Model                    │ Provider Order              │ 操作 │
│  claude-opus-4-6         │ aws → accessapi → laozhang  │ 编辑 删除 │
│  qwen3.6-flash           │ alibaba → laozhang          │ 编辑 删除 │
│  ...                      │ ...                         │ ...    │
│                                                          │
│  [保存]                                                    │
└──────────────────────────────────────────────────────────┘
```

---

## Expected Outcome

- laozhang, accessapi, deepseek, alibaba ALL get exercised for models they support
- `upstream_key_usage` and `provider_health` show non-zero stats for all active providers
- No manual admin configuration needed (smart routing default)
- Admin can override per-model via existing `model_provider_routing`
- Unhealthy providers auto-excluded; healthy providers auto-recover
- Cost-aware routing prefers cheaper providers
- Affinity cache still works but doesn't block provider distribution
- Billing works correctly regardless of routing decisions
