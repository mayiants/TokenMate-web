# smartRoute Key-Selection Optimization — Design

**Doc:** smartroute-optimization-design-202610251045
**Created:** 2026-10-25 10:45 UTC
**Status:** ACTIVE (design; b+c approved for implementation)
**Scope:** `ai-proxy-go` — `internal/server.smartRoute` + `pkg/keypool` health
**Supersedes for the selection chain:** the smartRoute description in
[`proxy-routing-and-prompt-cache-202607141007.md`](./proxy-routing-and-prompt-cache-202607141007.md)
(still valid for affinity/prompt-cache). Related:
[`cross-provider-fallback-routing-202605161900.md`](./cross-provider-fallback-routing-202605161900.md),
[`upstream-retry-failover-design-202610251430.md`](./upstream-retry-failover-design-202610251430.md).
Prior doc [`routing-strategy-design-202605071700.md`] is OUTDATED.

---

## 1. Current selection chain (as implemented)

Entry: `resolveProviderAndKey(Excluding/Filtered)` (`internal/server/handlers_common.go`):

```
GetAllKeysForModel(model, excludedProviders, tierFilter)   # candidate pool
   → per key: IsKeyAvailable (enabled && not cooldown && not rate-limited)
   → per key: checkUpstreamAvailable (RPM/TPM window)
   → catalog/model-name gate, tier filter
   ↓
(optional) per-request exclusion set + provider predicate   # failover
   ↓
manual routing?  →  sort by model_provider_routing.priority, tie-break LRU
no manual config → smartRoute:
   HEALTH filter (keyHealth.ErrorRate>0.80 || Status=="unhealthy")
   AFFINITY (preferredKeyID wins outright)
   composite sort: TIER → LATENCY bucket → QUOTA → LRU(oldest LastUsed first)
   ↓
selected = candidates[0]; SetLastUsed(now)
```

## 2. Defects found (2026-10-25 review)

Root-caused from a production incident: a dead OpenRouter key (account deleted,
401 "User not found") was **repeatedly preferred** over 10 healthy keys, so ~1/N
claude requests hit it and got a non-retryable 401.

| # | Defect | Effect |
|---|---|---|
| D1 | Candidate availability (`IsKeyAvailable`) only excludes **disabled / cooled-down / rate-limited** keys. A key that is **failing right now but not yet disabled/cooled** is still a candidate. | Freshly-bad keys stay routable. |
| D2 | `smartRoute` HEALTH uses `keyHealth` which is populated **only from Portal sync** (`pkg/keypool/sync.go`, aggregated from `usage_records`), not from live in-proxy outcomes. | A key failing *now* shows stale/zero ErrorRate for minutes → HEALTH can't filter it. |
| D3 | Final tie-break is **LRU = oldest `LastUsed` first**. A key that never succeeds tends to have the oldest `LastUsed`, so LRU **actively prefers** the perpetually-failing key. | The bad key sorts to position 0. |
| D4 | The per-key `Failures` counter (`ReportKeyFailure`, 3-strike → 60s cooldown) is **not an input** to `smartRoute` ordering/filtering; manual-routing path also tie-breaks on LRU. | No live-failure signal influences ordering until a hard cooldown trips. |

D1+D2 let the bad key into the sorted set; D3 puts it first. The two fixes
already shipped (sync `deleted_at IS NULL`; `isDefinitelyKeyInvalid` matches
"user not found") remove *this* dead key fast, but the **structural bias
remains** for any key that fails transiently before cooldown.

## 3. Design goals

- A key with recent **live** failures must not be preferred over healthy keys,
  regardless of Portal-sync staleness.
- LRU must **not reward** never-successful keys.
- Keep affinity + tier + latency + quota semantics intact; changes are additive
  to ordering, not a rewrite.
- No new Portal round-trip; signal must be live and in-proxy.

## 4. Changes

### 4.1 Live per-key health signal (addresses D1/D2/D4)

Extend the in-proxy key runtime (`pkg/keypool` `KeyInternal`/`KeyRuntime`) with:
- `lastSuccess time.Time` — set by `ReportKeySuccess` (already called from the
  success path via `recordUpstreamUsageAsync`).
- `lastFailure time.Time` — set by `ReportKeyFailure` / `ReportKeyRateLimited`.
- `consecutiveFailures int` — incremented on failure, **reset to 0 on success**
  (reuse the existing `Failures` field, which `ReportKeySuccess` already resets
  per the 2026-10-25 fix; expose a getter).

These are already partially present (`Failures`, `LastFailure`); add
`LastSuccess` and getters. No Portal involvement.

### 4.2 smartRoute ordering (addresses D3/D4) — **plan b + c**

Insert a **failure bucket before LRU** and make LRU success-aware:

```
composite sort key (earlier dominates):
  1. TIER rank                      (unchanged)
  2. LATENCY bucket                 (unchanged)
  3. QUOTA score                    (unchanged)
  4. HEALTH bucket (NEW, plan c):   healthy(0) vs recently-failed(1)
       recentlyFailed = consecutiveFailures>0 OR lastFailure after lastSuccess
       within a short window (e.g. lastFailure within 60s and >= lastSuccess)
  5. LRU tie-break (CHANGED, plan b):
       order by max(lastSuccess, zero) ascending  → prefer the key least-recently
       *successfully* used, NOT least-recently *touched*. A never-successful key
       has lastSuccess==zero and is pushed to the BACK of its bucket, not the front.
```

Rationale:
- **c (failure bucket)**: healthy keys (`consecutiveFailures==0`) always sort
  ahead of any key with a live failure streak, so a transiently-bad key is only
  chosen when no healthy key exists.
- **b (success-aware LRU)**: tie-break on `lastSuccess` removes the perverse
  "oldest-touch wins" that rewarded never-successful keys; it still spreads load
  across healthy keys (round-robin on successful use).

### 4.3 HEALTH pre-filter (keep, augment)

Keep the Portal-sync HEALTH exclusion (coarse, cross-restart signal), and add a
**live** guard: if `consecutiveFailures >= N` (e.g. 3) the key is already put
into cooldown by `ReportKeyFailure`, so `IsKeyAvailable` excludes it — no extra
filter needed. The new bucket (4.4) handles the 1..N-1 window the cooldown
doesn't cover.

## 5. Non-goals / out of scope

- No change to affinity key format, prompt-cache, or billing.
- No change to manual-routing priority semantics (but the same success-aware
  LRU tie-break is applied there too, for consistency).
- No active health-probing loop (recovery stays lazy; cooldown expiry + success
  reset handle fail-back).

## 6. Risk & verification

- **Low risk**: additive ordering fields + two sort comparators; no change to the
  candidate set or to failover. Behavior for all-healthy pools is unchanged
  except the LRU tie-break now keys on lastSuccess (still fair round-robin).
- Unit tests: (a) a key with a live failure sorts behind a healthy key; (b) a
  never-successful key (lastSuccess==zero) is not preferred by LRU; (c) healthy
  round-robin still rotates by lastSuccess; (d) manual-routing tie-break uses the
  same rule.
- `make test` + transform E2E + CI; deploy; observe that a transiently-failing
  key is deprioritized (proxy logs / usage_records is_fallback + provider spread).

## 7. Implementation order

1. `pkg/keypool`: add `LastSuccess` + getters; set it in `ReportKeySuccess`;
   ensure `Failures`/`LastFailure` reset/stamp are correct (mostly done).
2. `smartRoute`: add the HEALTH bucket (c) + success-aware LRU (b); apply the
   same LRU rule in the manual-routing sort.
3. Tests + CI + deploy.

Plan (a)'s broader "recent-failure window exclusion" is subsumed by the bucket
in 4.2.4; a separate time-window exclusion can be a later refinement if needed.

---

## 8. Follow-up (2026-10-25, commit 801e72d) — starvation fix + 3-tier live health

Two issues raised after shipping b+c:

**Q1 new-key starvation (bug introduced by the first b+c cut):** the first
`lessBySuccessThenLRU` ranked every succeeded key strictly ahead of any
never-successful key, so a brand-new key (lastSuccess==0) could be starved
forever while any succeeded key was available. **Fix:** `effectiveLRUTime =
max(lastSuccess, lastUsed)`; round-robin on that. A fresh key participates by its
pool-entry/last-touch time; perpetually-failing keys are demoted by the health
tier, not by this comparator. (Overheating is a non-issue: a selected key's
timestamp advances to now and sorts last next time.)

**Q2 health too weak / inconsistent with status page:** the Portal-synced
`keyHealth` that smartRoute's HEALTH filter uses is computed from
`upstream_key_usage` aggregated over the **whole current day** with a flat 0.80
threshold — so a key that failed earlier looks unhealthy long after recovery
(false positive), and a key with lots of earlier success looks healthy while
failing right now (false negative — the dead-key case). It also diverges from
the finer `DeriveInternalStatus` used by the status/monitor pages. A true
per-key sliding window cannot be built Portal-side without a schema change
(`upstream_key_usage` is day-granular; `provider_health_stats` has minute buckets
but no key dimension). **Decision:** strengthen the signal **in the proxy**,
where every success/failure is observed live. New `liveHealthTier` (3-level,
mirroring DeriveInternalStatus's spirit) from in-proxy state:

- 0 healthy — no failure streak, last outcome success (or fresh key)
- 1 suspect — 1-2 consecutive failures, or last outcome a failure within 2 min
- 2 failing — 3+ consecutive failures (usually already cooled down)

smartRoute and the manual-routing tie-break order by `liveHealthTier` first,
then `lessBySuccessThenLRU`. The Portal-synced keyHealth HEALTH pre-filter is
kept as a coarse, cross-restart backstop but is no longer the sole/only signal.
