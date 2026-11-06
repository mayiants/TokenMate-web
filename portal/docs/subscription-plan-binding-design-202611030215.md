# Subscription Overhaul: Plan Model Gating + Subscription-Bound Package Keys

**Status**: ACTIVE
**Date**: 2026-11-03
**Scope**: ai-portal-go (control plane). ai-proxy-go unchanged (enforcement flows through the existing validate response).
**Commits**: `5344697`, `f8ad188`, `53b0986`, `327d80e`, `5870644`, `08a9f6a`, `4867a88`, `7bba795`, `070289d` (+ UI/audit fixups `2b5fa47`, `0ea7cba`, `53713d2`/`4330f1a`)

---

## 1. Background

The subscription system shipped in July 2026 had four preset token plans
(Lite $5 / Standard $10 / Pro $30 / Max $70 per 30 days, 50M–3000M token
quotas). Three problems accumulated:

1. **Pricing drifted from industry norms.** By late 2026, consumer AI
   subscriptions converged on *code plans* (windowed request quotas: Claude
   Pro/Max, ChatGPT tiers, Copilot credits) with **model tiering by plan**,
   while pure token bundles survived only among API resellers. Our plans were
   flat token buckets with no model differentiation — a Max user spending the
   full quota on Opus-class models would cost the platform ~250x the plan
   price in upstream fees (adverse selection).
2. **No plan-level model gating.** `subscription_plans.model_permissions`
   existed in the schema and admin API but was never enforced, and the admin
   update endpoint silently dropped the field.
3. **"Newest subscription" heuristic.** Validation and billing both resolved
   a user's subscription via `ORDER BY created_at DESC LIMIT 1`, so a user
   holding both a code plan and a token plan had model gating and quota
   deduction pinned to whichever was newest — the other plan was inert.

## 2. Industry Research (2026 H2, informs plan redesign)

| Provider | Entry | Mid | Top | Gating |
|---|---|---|---|---|
| Claude | Pro $20 (Sonnet, ~45 prompts/5h) | Max $100 (5x) | Max $200 (20x) | Opus only on Max |
| ChatGPT | Go ~$8 | Plus ~$20 | Pro $100–200 | flagship models on Pro |
| Copilot | $10 | $19 | $39 | premium-request credits |

Common pattern: windowed request quotas (5-hour blocks — our `rp5h_limit`
already supports this) + model tiering. Token-quota bundles are gone from
consumer plans.

## 3. Changes

### 3.1 Plan-level model allowlist (`5344697`)

- `subscription_plans.model_permissions` (jsonb array of model names) is now
  enforced: `validate` joins the plan and intersects with the key's allowed
  models. Applies only to `key_type=subscription`; PAYG keys unaffected.
  Empty list = unrestricted (backward compatible).
- Admin UI: searchable two-column model picker on plan create/edit drawers
  (apikeys-style selected-model rows on top, catalog below; counts shown).
- Storefront: plan cards show the allowed-model list when restricted; the
  public plans API returns `model_permissions`.
- Bug fix: `updatePlan` accepted but silently dropped `model_permissions`
  (never added to the SET clause).

### 3.2 Seed duplication fix (`53b0986`)

The plan seeder matched existing rows by `name + plan_type='token'`, so
converting a preset plan to a code plan made the next boot insert a duplicate
token plan. Existence is now checked by name alone (regression test included).
A duplicate Lite row produced in production was removed.

### 3.3 Package keys bind a chosen subscription (`070289d`)

Package key creation no longer involves subscription groups:

- The creation form asks for the **billing subscription** (dropdown of the
  user's active subscriptions); model options follow the chosen plan's
  allowlist. `api_keys.subscription_id` stores the binding; `group_id` is NULL.
- `validate` prefers `ak.subscription_id` when resolving the subscription
  (legacy unbound keys fall back to newest).
- Billing: quota deduction and rate-window counters target the bound
  subscription — code plans and token plans held by one user now each meter
  their own keys.
- Renewal-safe: renewals/repurchases extend the existing `user_subscriptions`
  row in place, so the binding never orphans.

### 3.4 Subscription groups retired

`group_type='subscription'` user_groups were the old model/limit carrier for
package keys. Zero such groups ever existed in production and no real keys
depend on them, so the concept was removed rather than deprecated:

- Removed from group create/edit forms (filter dropdown kept for history).
- Removed the dormant `POST /api/admin/groups/from-template` endpoint.
- Removed the "subscription keys require a subscription group" check.
- The DB value remains tolerated (no CHECK constraint breakage).

### 3.5 Related audit-visibility fixes (same sprint)

- Reconciliation top-users/top-enterprises: Top 10 → **Top 20**; soft-deleted
  users/enterprises remain listed with Deleted/Disabled tags (deleting an
  account must not hide its spend).
- `/api/admin/users` and `/api/admin/enterprises` accept `include_deleted=1`
  (used by reconciliation pickers; defaults unchanged).
- admin/groups: Platform Inherited badge + RPM/TPM column (0 = Unlimited).

## 4. Data Flow (after)

```
plan (model_permissions, limits)
   │
   ▼  purchase
user_subscriptions row (per user per plan instance)
   │
   ▼  package key creation binds subscription_id
api_keys (group_id = NULL)
   │
   ▼  proxy validate (cached ~5min)
allowed_models = key ∩ bound-plan models;  sub_* limits from bound subscription
   │
   ▼  usage webhook
quota deduction + rate windows on the bound subscription row
```

## 5. Economics Notes (why model gating matters)

Sell-price per 1M tokens at a typical coding mix (80% in / 20% out, 90% cache
hits): glm-5.3-flash $0.067 · deepseek-v4-flash $0.27 · glm-5.3 $1.18 ·
kimi-k3 $3.46 · opus-4.6 $6.62. Flat token plans price at $0.023–0.10/M, so:

- DeepSeek-class light users (< ~19M tokens/month) are better off on PAYG —
  that is a legitimate niche, not a plan defect.
- Premium-model heavy users were subsidized up to ~250x real cost. Model
  gating by tier is the industry-standard fix and is now available.

## 6. Verification

- Unit: `TestJsonbModelList`, `TestIntersectModelAllowlist`,
  `TestCreateKey_PackageKeyPlanBinding` (binding / missing / foreign /
  out-of-plan cases), seed regression for type-changed plans.
- Full DB suites (`-p 1`), web/template smoke, i18n strict audit, schema
  drift check — all green; CI green on all commits.
- Production E2E: a plan gated to `["glm-5.3"]` produced a package key whose
  validate response carried exactly `allowed_models=["glm-5.3"]`; admin and
  member views verified for inherited groups; reconciliation shows
  soft-deleted accounts with tags.

## 7. Follow-ups (not in scope)

- Rebuild the four presets as tiered plans (Lite = economy models only,
  Pro/Max add premium) using the new gating — an admin-UI-only task.
- Consider launching code plans (rp5h windowed) as the consumer-facing form.
- Renewal UX could offer switching plan tier on renew.
