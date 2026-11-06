# Model Index Proposal: O(1) Model→Key Reverse Index for GetAllKeysForModel

**Date**: 2026-06-19 12:05 UTC  
**Status**: ✅ IMPLEMENTED  
**Scope**: Optimize `GetAllKeysForModel` hot path by replacing O(N×M) scan with O(1) model→key index  
**Reference**: new-api `group2model2channels` reverse index pattern

---

## 1. Problem

`GetAllKeysForModel` (manager.go:2137) runs on every chat completion request. Its hot path:

```
for each provider (N):
  for each key (M):
    containsMatch(key.Models, requestedModel)   ← 3-pass string matching
    IsKeyAvailable(key)                          ← runtime filter
    checkUpstreamAvailable(key)                  ← runtime filter
```

**Current cost**: O(N×M) with each `containsMatch` doing up to 3 passes across the key's model list. With ~100 keys and ~5 models each, that's ~500 model→key evaluations per request.

**The model-matching part is pure static data** — it depends only on `key.Models []string`, which changes only during Portal sync. The runtime filters (cooldown, RPM/TPM, rate-limit) remain dynamic and cannot be indexed.

---

## 2. Proposal: `atomic.Pointer[ModelIndex]`

Add a precomputed reverse index to `Manager`:

```go
// Manager struct — new field
modelIndex atomic.Pointer[ModelIndex]

// ModelIndex maps model name → keys that support that model (exact match only)
type ModelIndex struct {
    mu        sync.RWMutex
    // model → list of (provider, key pointer)
    index     map[string][]*KeyInternal
    // For containsMatch semantics, also store normalized → exact mappings
    exact     map[string]string  // normalized_name → canonical_name
}
```

### Build Index

Rebuilt at the end of `updatePoolsFromSync` (manager.go:842), after all provider pools are finalized:

```go
func (m *Manager) updatePoolsFromSync(keys []Key) {
    // ... existing merge logic ...
    
    // After line ~950 (pools fully updated), rebuild model index:
    m.rebuildModelIndex()
}

func (m *Manager) rebuildModelIndex() {
    m.poolsMu.RLock()
    defer m.poolsMu.RUnlock()
    
    idx := newModelIndex()
    for provider, pool := range m.pools {
        for _, key := range pool.internalKeys {
            for _, model := range key.Models {
                idx.add(model, provider, key)
                // Also add normalized variants for fuzzy matching
                norm := normalizeModelName(model)
                if norm != model {
                    idx.add(norm, provider, key)
                }
                // Prefix entries require scanning — handled inline below
            }
        }
    }
    m.modelIndex.Store(idx)
}
```

### Use Index

Modify `GetAllKeysForModel` to use the index as a pre-filter — eliminate the `containsMatch` O(m) scan per key, then apply runtime filters as before:

```go
func (m *Manager) GetAllKeysForModel(model string, excludedProviders map[string]bool) (high, normal, economy []*CandidateKey) {
    idx := m.modelIndex.Load()
    
    // Step 1: O(1) index lookup — get candidate keys
    candidates := idx.lookup(model)  // returns []*KeyInternal
    
    // Step 2: Runtime filtering (same as before)
    for _, key := range candidates {
        if excludedProviders[key.Provider] {
            continue
        }
        if !m.IsKeyAvailable(key.Key) {
            continue
        }
        if !m.checkUpstreamAvailable(key) {
            continue
        }
        // ... same priority-based append as current code
    }
    return
}
```

### Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| **Index granularity** | Per `*KeyInternal` pointer, not per provider | Min diff from existing code; no provider-level grouping needed |
| **Mutex vs atomic.Pointer** | `atomic.Pointer` for the whole index | Entire index is rebuilt atomically; no partial reads. RWMutex inside index is for `lookup` concurrent safety during pointer swap |
| **Normalized entries** | Add normalized model names as separate index entries | Covers `normalizeModelName` (dot↔hyphen) without scan. Single normalization on insert vs N×M per request |
| **Prefix matching** | Keep inline fallback for prefix matches | Prefix rules (`HasPrefix`) don't fit O(1) map lookup; they're rare (dated model variants like `-20260512`) — keep as fallback |

---

## 3. Three Options

### Option A — Minimal (extract model match only)

Replace only the `containsMatch` call with an index lookup. All runtime filtering stays untouched.

**Changes**:
- Add `ModelIndex` struct + `atomic.Pointer` to `Manager`
- `rebuildModelIndex()` called at end of `updatePoolsFromSync`
- Modify `GetAllKeysForModel` to index-lookup instead of `containsMatch`
- Prefix-match fallback stays inline

**Effort**: ~1 file, ~80 lines  
**Benefit**: Eliminates O(N×M) model string scanning; runtime filter cost unchanged  
**Tradeoff**: Does not help with the 3-pass match inside `containsMatch` when the model IS in the index (exact/normalized hits skip scanning anyway)

### Option B — Full Semantic Index

Index ALL 3 match strategies:
- **Exact**: `model → [keys]` direct mapping
- **Normalized**: `normalizeModelName(model) → [keys]` with dedup
- **Prefix**: Build a prefix tree (trie) for models, e.g. `"claude-opus-4-6"` → `["claude-opus-4-6-20260512", "claude-opus-4-6-thinking"]`

**Changes**: Same as A + prefix trie data structure  
**Effort**: ~1 file, ~150 lines  
**Benefit**: Fully O(1) model→key match including prefix; eliminates all inline scanning  
**Tradeoff**: Overengineered — prefix matches are rare (dated model variants), and the fallback is cheap

### Option C — Full Index Rebuilt on Sync (same as B, different trigger)

Same as Option B, but specifically for the current codebase rhythm: Portal sync is the only mutation point, so the index is rebuilt exactly once per sync cycle (typically every 30–60s).

**Changes**: Identical to B, but explicitly documents the rebuild boundary  
**Effort**: ~1 file, ~150 lines  
**Benefit**: Clear invalidation semantics; no stale reads  
**Tradeoff**: Index can lag up to one sync interval if models change dynamically (they don't currently)

---

## 4. Rebuild Logic Details

The index must be rebuilt after all pools are finalized in `updatePoolsFromSync`. The natural insertion point is after line 950 (pool cleanup), before `updatePoolsFromSync` returns:

```go
// In updatePoolsFromSync, after pools are fully merged:

// Rebuild model index with all current pools
// (poolsMu is already unlocked at this point in the current code)
m.poolsMu.RLock()
idx := newModelIndex()
for provider, pool := range m.pools {
    for _, key := range pool.internalKeys {
        for _, model := range key.Models {
            idx.add(model, key)
        }
    }
}
m.poolsMu.RUnlock()
m.modelIndex.Store(idx)
```

**Note**: Ensure this happens BEFORE `poolsMu` is released if using `poolsMu.RLock`. The current code unlocks `poolsMu` at line 950 — the index rebuild should happen while the lock is still held, or acquire it again.

---

## 5. Safety & Memory Considerations

| Concern | Mitigation |
|---------|------------|
| **Stale index after sync** | Rebuilt atomically at end of every `updatePoolsFromSync` call; index always reflects most recent sync |
| **Read-write race on index during rebuild** | `atomic.Pointer` ensures readers see either old or new index — no intermediate state |
| **Memory overhead** | Each `*KeyInternal` pointer is 8 bytes; 100 keys × 5 models = 500 entries × ~64 bytes ≈ 32KB |
| **GC pressure** | Old index: one old `ModelIndex` struct is unreferenced after `Store()` and collected next GC cycle |

---

## 6. Interaction with Runtime Filters

The index only replaces the **model matching** step. After index lookup, the same runtime filters apply:

```
Index lookup:  O(1) model → candidate keys
IsKeyAvailable:   per-key check (same as before)
checkUpstreamAvailable: per-key RPM/TPM check (same as before)
Priority tiering: per-key sort (same as before)
```

This means the hot path goes from:
- **Before**: O(N×M) model scan + O(k) runtime filters
- **After**: O(1) model lookup + O(k) runtime filters

Where k = candidate keys for the requested model (typically 5–20, not 100).

---

## 7. Comparison to new-api

| Aspect | new-api `group2model2channels` | This proposal |
|--------|-------------------------------|---------------|
| **Index key** | (group, model) → channel | model → key |
| **Isolation mechanism** | User group → channel group + Ability table | Per-key model allowlist (unchanged) |
| **Runtime filter** | Channel status (enabled/disabled) | Cooldown, RPM/TPM, rate-limit, upstream check |
| **Rebuild trigger** | Periodic DB poll + cache refresh | Portal sync (updatePoolsFromSync) |
| **Rebuild atomicity** | channelSyncLock.RLock/RLock during refresh | atomic.Pointer swap |
| **Prefix matching** | None (exact match only) | Optional trie (Option B/C) |

The key difference: new-api's index is the **primary source of truth** for routing (group+model determines channel definitively). In ai-proxy-go, the index is only an **optimization** over per-key model allowlists, because final routing also depends on runtime state.

---

## 8. Recommendation

**Option A (Minimal)** is recommended as the first step:

1. **Highest ROI**: The O(N×M) string scan is the dominant cost; exact matches are the common case
2. **Lowest risk**: ~80 lines, no new data structures, prefix fallback unchanged
3. **Easily extensible**: If prefix trie is needed later, it can be added inside the same `ModelIndex` struct
4. **Fits the codebase rhythm**: Rebuilt once per sync, same as existing `updatePoolsFromSync` lifecycle

Estimated impact: ~99% reduction in model-matching time for the common case (exact/normalized model names), reducing total `GetAllKeysForModel` latency from O(N×M) to O(k) where k = candidate keys per model.

---

---

## 9. Priority-Tier Retry (new-api §2.2 inspiration)

### 9.1 Current State

In new-api, the `retry` parameter controls which priority tier is selected:

```
retry=0 → pick highest priority tier → if relay fails, retry=1 → next tier
```

Each retry call to `getChannel()` is O(1) (re-enters the same index), not O(N×M).

In ai-proxy-go, the flow is reversed — **the handler has an ad-hoc retry via try-different-track fallback** (e.g. bedrock → standard), but there is **no retry within the same routing domain**:

```
resolveProviderAndKey (handlers.go:1229)
  → GetAllKeysForModel (O(N×M) scan)
  → smartRoute() sorts candidates multi-dimensionally
  → picks candidates[0]
  → if it fails (upstream error) → NO retry within the same domain
```

The retry patterns that DO exist:
- **Track fallback**: Bedrock → Standard (different provider track, not priority tier)
- **Per-handler re-route**: Some handlers (e.g., `handleImageGeneration` at line 2302, `handleEmbeddings` at line 1530) call `resolveProviderAndKey` again after a track fails — but this is a full re-scan, not a tier walk

**No mechanism exists to walk priority tiers on failure within the same model/provider domain.**

### 9.2 Proposed: Priority-Tier Walk

Changes on top of the Model Index (Option A) — once the index eliminates O(N×M) scanning, retry becomes cheap enough to run on every upstream failure:

#### Step 1: Make `GetAllKeysForModel` accept a `priorityTier` filter

```go
// New signature — tier filter added
func (m *Manager) GetAllKeysForModel(model string, excludedProviders map[string]bool, tierFilter string) (high, normal, economy []*CandidateKey) {
    idx := m.modelIndex.Load()
    candidates := idx.lookup(model)  // O(1)

    for _, key := range candidates {
        if excludedProviders[key.Provider] {
            continue
        }
        // Apply tier filter
        if tierFilter != "" && key.Tier != tierFilter {
            continue
        }
        if !m.IsKeyAvailable(key.Key) {
            continue
        }
        if !m.checkUpstreamAvailable(key) {
            continue
        }
        // append by priority as before
    }
    return
}
```

#### Step 2: Add a `retry` parameter to `resolveProviderAndKey`

```go
func resolveProviderAndKey(ctx context.Context, model, userPriority, preferredKeyID string, retry int) (string, *keypool.KeyInternal, string, bool, error) {
    // ... existing logic ...

    // Determine which priority tier to use based on retry count
    // retry=0 → all tiers (current behavior, cross-provider competition)
    // retry=1 → exclude "economy" (fallback to normal+high only)
    // retry=2 → exclude "economy"+"normal" (high-tier only, last resort)
    var tierFilter string
    switch retry {
    case 0:
        tierFilter = ""  // all tiers
    case 1:
        tierFilter = "premium"  // try premium tier only on first retry
    case 2:
        tierFilter = "enterprise"
    }

    high, normal, economy := keyPoolManager.GetAllKeysForModel(model, excludedProviders, tierFilter)
    // ... rest of smartRoute unchanged ...
}
```

#### Step 3: Add a retry loop in the handler

```go
// In handleChatCompletions, after initial routing failure:
maxRetries := 2
for attempt := 0; attempt <= maxRetries; attempt++ {
    provider, upstreamKey, upstreamModelName, isFallback, err = resolveProviderAndKey(ctx, req.Model, userPriority, preferredKeyID, attempt)
    if err != nil {
        continue  // try next tier
    }

    // Create adaptor and make request
    adaptor := relay.GetAdaptor(provider, &relay.AdaptorConfig{
        BaseURL: resolveBaseURL(upstreamKey, provider),
        APIKey:  upstreamKey.ActualKey,
    })
    providerReq := adaptor.ToProvider(req)
    resp, err := adaptor.DoRequest(providerReq, isStream)

    if err != nil {
        // On upstream failure, record error and retry with next tier
        keyPoolManager.RecordUpstreamFailure(provider, upstreamKey.ID)
        if attempt < maxRetries {
            continue  // retry with next priority tier
        }
    }
    // Success — process response
    break
}
```

### 9.3 Tier Progression

The retry walks **priority tiers from broad to narrow**, not the other way around:

| retry | Tier filter | Rationale |
|-------|-------------|-----------|
| 0 | All (default) | Normal operation — cheapest keys compete |
| 1 | `premium` (or fallback to `enterprise`) | First retry: spend more for reliability |
| 2 | `enterprise` or `default` | Last resort: use any surviving key of mid-tier |

This is the **inverse** of new-api's approach (which goes high→low). The reasoning:
- ai-proxy-go's `Tier` is a **cost** signal (premium > enterprise > default > economy), not a QoS signal
- Default behavior should prefer efficient (lower cost) keys
- **Only on failure** should we be willing to burn premium quota to get the request through
- Economy keys are excluded first; premium keys are the safety net

### 9.4 Interaction with smartRoute

smartRoute already sorts by `tierRank` (Step 2 at handlers.go:1371). Under the tier-retry model, smartRoute's tier sort becomes **partially redundant** for the retry path — because the `tierFilter` in `GetAllKeysForModel` pre-filters to a single tier before smartRoute sees them. But the tier sort still matters at `retry=0` (all tiers competing), where smartRoute's tier sort is the primary ordering.

No conflict: `tierFilter=""` at retry=0 → smartRoute sorts by tier as before. `tierFilter="premium"` at retry=1 → smartRoute sees only premium keys, tier sort is a no-op.

### 9.5 Where to Insert the Retry Loop

The retry loop should live **inside `resolveProviderAndKey`**, not in each handler separately. This avoids duplicating the retry pattern across 5 handlers (chat, embeddings, image generation, responses API, hub):

```go
func resolveProviderAndKey(ctx context.Context, model, userPriority, preferredKeyID string) (string, *keypool.KeyInternal, string, bool, error) {
    // ... existing logic (excluded providers, circuit breakers) ...

    for retry := 0; retry <= maxRetries; retry++ {
        tierFilter := priorityTierForRetry(retry)
        high, normal, economy := keyPoolManager.GetAllKeysForModel(model, excludedProviders, tierFilter)

        // ... existing candidate selection and smartRoute ...

        selected := candidates[0]

        // Before returning, do a lightweight upstream ping to verify availability
        // This catches keys that pass RPM/TPM check but are actually dead
        if retry < maxRetries && !quickHealthCheck(selected) {
            selected.Key.TrackFailure()  // mark for cooldown
            continue  // try next tier
        }

        selected.Key.SetLastUsed(time.Now())
        // ... rest of routing ...
        return selected.Provider, selected.Key, upstreamModelName, isFallback, nil
    }
    return "", nil, "", false, fmt.Errorf("no available upstream keys for model: %s after %d retries", model, maxRetries)
}
```

**Handlers remain unchanged** — they call `resolveProviderAndKey` once as before. The retry is transparent.

### 9.6 Options for Priority-Tier Retry

#### Option P1 — Tier filter at `GetAllKeysForModel` level (recommended)

As described above: the index (Option A) is a prerequisite. The `tierFilter` parameter trivially filters during the index walk. No new data structures.

**Prerequisite**: Model Index (Option A)  
**Effort**: ~60 lines (tierFilter param + retry loop in resolveProviderAndKey)  
**Benefit**: Adds retry path at effectively zero marginal compute cost (O(k) filter on already-reduced candidate set)

#### Option P2 — Separate tier-indexed `[]*CandidateKey` slices

Build a precomputed `map[string][]*KeyInternal` per tier in `ModelIndex`, so `GetAllKeysForModel` can return `high/normal/economy` directly from the index without re-filtering.

**Effort**: ~80 lines (tier dimension in index)  
**Benefit**: Eliminates even the tier-filter scan — truly O(1) tier filtering  
**Tradeoff**: Over-engineered for current scale; P1's linear scan over ~20 candidates is negligible

#### Option P3 — Weighted random within tier (new-api style)

Replace smartRoute's deterministic multi-factor sort with weighted random selection within the priority tier:

```go
func pickKeyFromTier(candidates []*CandidateKey) *CandidateKey {
    totalWeight := 0
    weights := make([]int, len(candidates))
    for i, ck := range candidates {
        w := getKeyWeight(ck)  // based on health, latency, quota
        weights[i] = w
        totalWeight += w
    }
    // weighted random selection
    r := rand.Intn(totalWeight)
    // ...
}
```

**Effort**: ~100 lines (weight function + random selection)  
**Prerequisite**: Model Index (Option A) + P1  
**Benefit**: More predictable load distribution; avoids thundering herd when smartRoute always picks the same top key  
**Tradeoff**: Requires rethinking smartRoute's current deterministic chain — a larger behavioral change

### 9.7 Comparison to new-api

| Aspect | new-api | ai-proxy-go (proposed) |
|--------|---------|----------------------|
| **Retry mechanism** | retry counter → priority tier selection | retry counter → tier filter → re-route |
| **Tier direction** | High priority first → lower on retry | Economy-first → premium on retry (cost-aware) |
| **Selection within tier** | Weighted random | smartRoute (sort-based, unchanged) |
| **When retry fires** | On relay failure (HTTP error) | On upstream failure + quick health check |
| **Scope of retry** | Per request in relay loop | Inside resolveProviderAndKey (transparent to handlers) |
| **Prerequisite** | None (O(1) index already exists) | Model Index (Option A) needed for cheap retry |

### 9.8 Recommendation

**Implement P1 after Option A.**

Sequence:
1. First implement Model Index (Option A) — eliminates O(N×M) scan
2. Then add `tierFilter` parameter to `GetAllKeysForModel` (~20 lines)
3. Then wrap `resolveProviderAndKey` content in a retry loop with tier progression (~40 lines)

Total incremental effort after Option A: ~60 lines. No new data structures. Retry is transparent to all handlers.

---

## References

- `GetAllKeysForModel` — manager.go:2137
- `containsMatch` — manager.go:159 (3-pass: exact → normalized → prefix)
- `resolveProviderAndKey` — handlers.go:1229
- `smartRoute` — handlers.go:1338
- `updatePoolsFromSync` — manager.go:842
- `Manager` struct — manager.go:321
- `Key.Models []string` — KeyStatic.keypool.go:92
- `CandidateKey` — manager.go:2126
- new-api comparison report: `docs/reports/new-api-comparison-202606191155.md`, §2.2, §7
- Audit report: `docs/reports/key-sync-routing-audit-202606181345.md`
