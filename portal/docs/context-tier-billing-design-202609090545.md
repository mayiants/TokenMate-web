# Context-Length Tiered Billing Design

> Status: ACTIVE (implemented 2026-09-09; see `tiered_context` in `internal/services/pricing.go`)
> Date: 2026-09-09
> Author: Kimi Code
> Scope: ai-portal-go billing engine (`internal/services/pricing.go`)

## 1. Problem

laozhang (new-api `tiered_expr`) prices a request **as a whole** by its context length:

```
len <= 272000 ? tier("≤ 272K", p*10 + c*50 + cr*1 + cc*12.5)
              : tier("> 272K", p*20 + c*75 + cr*2 + cc*25)
```

Once `len` crosses the threshold, **every token** (input, output, cache read/write)
is billed at the high-tier price.

Our existing `billing_mode='tiered'` (`calculateTieredCost`) implements
**progressive marginal brackets** (tax-bracket style): only the tokens *beyond*
a threshold pay the higher price. Configuring laozhang's models this way
(current state of `laozhang/grok-4.6` and `laozhang/gpt-6-astra`) undercharges
over-threshold requests on the base segment — a real loss on a cost-pass-through
(≈0 margin) price book. Example: a 300K-token request should bill
`300K × $20/1M = $6.00` at laozhang; marginal brackets bill
`272K × $10 + 28K × $20 = $3.28` — a 45% undercharge.

## 2. Proposal

Add a new billing mode `tiered_context` that reuses the existing `tiers` jsonb
schema but interprets it as laozhang does: pick **one** tier by total request
length, then price all components flat at that tier's prices.

### 2.1 Tier selection

- `total_len = input_tokens + output_tokens` (final, settle-time values).
  Rationale: laozhang's `len` is undocumented; input+output is the closest
  match to "context length" and is the conservative (higher) choice versus
  input-only.
- Tiers sorted by `max_tokens` ascending (nil = catch-all).
- Select the **first** tier where `total_len <= max_tokens`; if none matches,
  use the catch-all. Empty/invalid tiers → fall back to flat `prices`.

### 2.2 Cost computation

After tier selection, compute exactly like flat mode with the tier's prices:

```
Input      = effective_input_tokens × tier.input        / 1e6
Output     = output_tokens       × tier.output          / 1e6
CacheHit   = cached_tokens       × tier.cache_hit       / 1e6
CacheWrite = cache_creation      × tier.cache_write     / 1e6
Reasoning  = reasoning_tokens    × tier.reasoning       / 1e6
Fixed      = tier.fixed_request  × request_count
```

`effective_input_tokens` semantics stay identical to `calculateFlatCost`
(whatever the flat path does today — no behavior drift for non-tiered models).

### 2.3 Pre-consume (proxy) — unchanged

Proxy freezes using `worstCasePrices` (max component price across tiers), so a
large request is frozen at the high tier. Small requests are over-frozen
temporarily and settle at the correct (low) tier, same as today's `tiered`.
No proxy change needed.

### 2.4 Aggregates / dashboards

`EstimateComponentCostForAggregate` intentionally uses base flat `prices` —
keep as-is; document that reconciliation dashboards approximate context-tiered
models at their base tier.

## 3. Changes

| File | Change |
|---|---|
| `internal/services/pricing.go` | `case "tiered_context":` in `calculateCostForRule`; new `calculateContextTierCost(record, tiers)`; tier-selection helper `selectContextTier(total int64, tiers []TokenTier) *TokenTier` |
| `internal/services/pricing_algorithm_test.go` | Unit tests: boundary (≤ vs >), catch-all, empty tiers fallback, cache components at high tier, fixed fee per request |
| DB (post-deploy, manual SQL) | `UPDATE model_pricing_rules SET billing_mode='tiered_context' WHERE provider='laozhang' AND product_id IN ('grok-4.6','gpt-6-astra')` |

No admin API change: `billing_mode` is a free-form string (no whitelist in
`admin_pricing.go`). Admin UI has no tiers editor anyway — tiered rules are
SQL-administered; document the mode in `migrations/README.md` or runbook.

`MatchRule` / rule caching / sync-to-proxy (`SyncViewForProxy`) are
mode-agnostic — no changes.

## 4. Edge cases & decisions

- **Boundary**: `total_len == max_tokens` bills at the **low** tier (laozhang
  uses `<=`).
- **Streaming**: tokens are only final at settle; pre-consume already freezes
  worst-case. No streaming-specific work.
- **Zero tiers or all tiers without catch-all and total above last max**:
  fall back to the rule's base flat `prices` (defensive; avoid zero-cost bills).
- **`len` semantics risk**: if laozhang actually means input-only, we
  over-bill borderline requests (output pushes `total_len` over). Mitigation:
  reconciliation report comparing billed vs laozhang `realCost` for
  over-threshold requests; adjust to input-only if systematically high.
- **Media/time_based modes**: unaffected.

## 5. Verification plan

1. Unit tests (boundary, catch-all, fallback, cache components).
2. Staging-ish e2e: temporarily set a tiny threshold (e.g. `max_tokens=50`)
  on a cheap laozhang rule (`grok-4.1-fast`-class), fire one short and one
  padded request, assert the usage_records amounts differ as expected, then
  restore the real threshold. Probe key `probe-0909` / group `tr-probe-0908`
  can be used (balance $100 test funds).
3. After real rules are flipped to `tiered_context`, spot-check one natural
  >204.8K `grok-4.6` request against laozhang `realCost`
  (`GET https://api.laozhang.ai/api/log/self` or the `/api/pricing`-style
  usage endpoint used in the RPM probe).

## 6. Alternatives considered

- **Keep marginal brackets** (status quo): undercharges over-threshold
  requests ~45% on the base segment. Rejected — cost-pass pricing makes any
  undercharge a direct loss.
- **Per-component progressive with compensation multiplier**: tune tier-2
  prices above laozhang's to make expected cost match. Rejected — opaque,
  still wrong per-request, breaks price transparency.
- **Change `calculateTieredCost` itself**: would silently alter semantics of
  any existing tiered rules. Rejected — there are production `tiered` rules
  conceptually written as marginal brackets; a new mode is explicit and
  backward-compatible.
