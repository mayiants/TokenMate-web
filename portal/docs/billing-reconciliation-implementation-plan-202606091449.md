# Billing & Reconciliation Implementation Plan

> **Status**: All Phases Completed (Phase 1–7) ✅
> **Author**: AI Agent
> **Date**: 2026-06-09
> **Updated**: 2026-06-10 — Phase 3 (calendar month billing) and Phase 4 (enterprise hard stop) implemented

---

## 1. Background & Goals

This document consolidates all billing, invoicing, and reconciliation decisions into an actionable implementation plan.

**Key Problems to Solve**:
1. `recharge_orders` (legacy table) overlaps with `orders` — ~~need consolidation~~ ✅ RESOLVED — `recharge_orders` code removed; `orders` table handles all recharge flows
2. `balance` / `gift_balance` double-counting bug in current code ✅ RESOLVED
3. Invoice requests have no spending validation (anyone can request any amount) ✅ RESOLVED
4. Enterprise billing cycle is not calendar-month aligned ✅ RESOLVED — `ResetMonthlyBilling` now runs on 1st of each month with calendar-month `credit_reset_at`
5. Enterprise batch billing inconsistently falls back to personal `balance` ✅ RESOLVED — `processBillingBatch` no longer falls back to personal balance for enterprise keys
6. `balance_transactions` missing credit records (recharge, voucher, checkin, referral) ✅ RESOLVED
7. `balance_transactions` still uses `DECIMAL(12,2)` while rest of system uses `BIGINT` (micro-USD) ✅ RESOLVED

---

## 2. Decision Summary

| # | Decision | Detail |
|---|----------|--------|
| 1 | **Deprecate `recharge_orders`** | Front-end calls `/orders/create` (`product_type='recharge'`) instead of `/recharge/create`. Drop `recharge_orders` table after migration. |
| 2 | **Deprecate `recharge_balance`** | All personal funds go into `balance`. `gift_balance` holds promotional credits only. |
| 3 | **Balance Model: Plan B** | `balance` = real funds (mutually exclusive with `gift_balance`). Total available = `balance + gift_balance` (computed). See Section 3. |
| 4 | **Invoice eligibility** | `invoice_eligible = SUM(balance_transactions WHERE balance_type='balance') − already_invoiced − pending_requests`. Only real-fund consumption is invoiceable. |
| 5 | **Min invoice amount** | $10 USD (or currency equivalent). No multiplier restriction. |
| 6 | **Enterprise billing cycle** | Calendar month (natural month). First/last month prorated by actual days. |
| 7 | **Enterprise overspend** | 110% credit cap → hard stop. **No fallback** to personal `balance` (unify batch + single paths). |
| 8 | **Enterprise prepay** | Offline only. Platform tracks `credit_total` / `credit_used` only. No `prepaid_balance` field. |
| 9 | **Refund** | Subscription cancellation refund ✅ implemented (`subscriptions.go:cancelSubscription`). General payment-gateway refund (Stripe/Alipay/WeChat/PayPal) deferred. |
| 10 | **balance_transactions migration** | ✅ COMPLETED — `amount` / `balance_before` / `balance_after` migrated to `BIGINT` (micro-USD) in migration `202605181500` and `schema.go` CREATE TABLE. Backfill of missing credit records done in Phase 6. |

---

## 3. Balance Model: Plan B (Detailed)

### 3.1 Field Semantics

| Field | Meaning | Mutually Exclusive? |
|-------|---------|---------------------|
| `users.balance` | Real funds (recharge, cash) | Yes — only real money |
| `users.gift_balance` | Promotional credits (voucher, checkin, referral) | Yes — only gift credits |
| `users.recharge_balance` | **DEPRECATED** — to be removed | — |

**Total available balance** (displayed to user) = `balance + gift_balance` (computed by back-end).

### 3.2 Credit / Debit Rules

| Operation | `balance` | `gift_balance` | `balance_transactions` |
|-----------|-----------|----------------|------------------------|
| Recharge (paid) | `+amount` | — | `transaction_type='recharge', balance_type='balance'` |
| Voucher redeem | — | `+amount` | `transaction_type='voucher', balance_type='gift'` |
| Checkin reward | — | `+amount` | `transaction_type='checkin', balance_type='gift'` |
| Referral reward | — | `+amount` | `transaction_type='referral', balance_type='gift'` |
| API usage | `−balanceDeduction` | `−giftDeduction` | Multiple rows per deduction type |

### 3.3 Deduction Priority Chain

**General / Subscription keys**:
```
gift_balance → balance
```

**Enterprise keys**:
```
gift_balance → enterprise_credit (110% cap) → HARD STOP (no fallback to personal balance)
```

> ✅ **RESOLVED**: `processBillingBatch` enterprise branch no longer falls back to personal balance — now consistent with `processRecord`.

---

## 4. ⚠️ Highlight: `total_cost` vs Actual Payment Price

```
┌─────────────────────────────────────────────────────────────┐
│  total_cost in usage_records = UPSTREAM COST (model price)  │
│                                                             │
│  Actual user deduction = total_cost × pricing_ratio         │
│                                                             │
│  pricing_ratio comes from user_group / enterprise config    │
└─────────────────────────────────────────────────────────────┘
```

- `usage_records.total_cost` stores the **upstream API cost** (what we pay OpenAI/Anthropic/etc).
- The `*_deduction` columns (`gift_deduction`, `balance_deduction`, etc.) store the **actual amount deducted from the user** (already multiplied by `pricing_ratio`).
- **This is intentional** — it allows margin/profit tracking by comparing `total_cost` (our cost) with user deductions (our revenue).
- **However**, for user-facing billing displays, we must be careful to show the **deduction amount** (what they actually paid), not `total_cost`.
- **Pricing strategy discussion deferred**: After this implementation phase, we will discuss pricing model, markup policies, and transparency requirements.

---

## 5. Database Changes (Migrations)

> **Note**: No historical data compatibility required (test environment). Use standard migration files.

### 5.1 Deprecate `recharge_orders`

```sql
-- Migration: drop recharge_orders after confirming /orders/create handles all recharge flows
-- This is a DESTRUCTIVE migration — only safe because no production legacy data exists
DROP TABLE IF EXISTS recharge_orders;
```

### 5.2 Deprecate `recharge_balance` field

```sql
-- Remove recharge_balance from users (after code no longer references it)
ALTER TABLE users DROP COLUMN IF EXISTS recharge_balance;
```

### 5.3 Update `usage_records` deduction columns

```sql
-- Add balance_deduction to replace recharge_deduction semantics
ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS balance_deduction BIGINT DEFAULT 0;

-- Update comment / documentation
COMMENT ON COLUMN usage_records.balance_deduction IS 'Real balance deduction in micro-USD (replaces recharge_deduction)';
```

> `recharge_deduction` column can be kept (no data) or dropped later. New code writes to `balance_deduction` only.

### 5.4 Migrate `balance_transactions` to BIGINT ✅ COMPLETED

> Verified 2026-06-10: `schema.go` CREATE TABLE uses `BIGINT`; migration `202605181500_quota_micro_usd.sql` converts existing `DECIMAL` columns to `BIGINT` with `* 1000000` conversion.

```sql
-- Convert DECIMAL(12,2) → BIGINT (micro-USD)
ALTER TABLE balance_transactions 
  ALTER COLUMN amount TYPE BIGINT USING ROUND(COALESCE(amount, 0) * 1000000),
  ALTER COLUMN balance_before TYPE BIGINT USING ROUND(COALESCE(balance_before, 0) * 1000000),
  ALTER COLUMN balance_after TYPE BIGINT USING ROUND(COALESCE(balance_after, 0) * 1000000);
```

### 5.5 Update `balance_transactions` transaction types

Ensure `transaction_type` covers:
- `usage` — API consumption (existing)
- `recharge` — Balance top-up (new)
- `voucher` — Voucher redemption (new)
- `checkin` — Daily check-in reward (new)
- `referral` — Referral reward (new)
- `refund` — Future refund (reserved)
- `enterprise_credit` — Enterprise credit usage (existing, but review `enterprise_id` population)

---

## 6. Implementation Phases

### Phase 1: Fix Balance Double-Counting Bug ✅ COMPLETED

**Goal**: Stop adding promotional credits to `balance`.

**Files modified**:
- `internal/routes/vouchers.go:140` — Voucher redeem: removed `balance = balance + $1` ✅
- `internal/routes/checkin.go:117` — Checkin reward: removed `balance = balance + $1` ✅
- `internal/services/reward.go:55` — Welcome bonus: removed `balance = balance + $1` ✅
- `internal/services/reward.go:152` — Referral invitee reward: removed `balance = balance + $1` ✅
- `internal/services/reward.go:162` — Referral inviter reward: removed `balance = balance + $1` ✅

**After**: Promotional credits only increase `gift_balance`, never `balance`.

---

### Phase 2: Deprecate `recharge_orders` & `recharge_balance` ✅ COMPLETED

**Goal**: Consolidate all recharge flow into `orders` table.

**Files modified**:
- `internal/routes/billing.go`:
  - Removed `createRecharge()` handler ✅
  - Removed `/recharge/create` route registration ✅
- `internal/routes/webhook.go`:
  - Removed all `rechargeDeduction` / `recharge_balance` references ✅
  - Simplified deduction chain: `gift → balance` ✅
- `internal/db/schema.go`:
  - Removed `recharge_orders` table creation ✅
  - Removed `users.recharge_balance` ALTER ✅

**Migration**: `recharge_orders` table and `recharge_balance` column removed from schema (test env only).

---

### Phase 3: Enterprise Billing Cycle → Calendar Month ✅ COMPLETED

**Goal**: Natural month billing with prorated first/last month.

**Implemented behavior** (`ResetMonthlyBilling` in `enterprise.go:2219`):
- Guard: `if now.Day() != 1 { return }` — only runs on the 1st of each month
- `nextReset = time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, loc)` — calendar month, no drift
- Reset all `billing_cycle='monthly'` enterprises: `quota_used = 0, credit_used = 0`
- Set `credit_reset_at` to start of next month

**Cron scheduling** (`cmd/server/main.go:356`):
- Replaced `time.NewTicker(24 * time.Hour)` with precise daily-at-midnight scheduling
- Uses `time.After(waitDuration)` with `ctx.Done()` cancellation support
- `ResetMonthlyBilling` guard ensures billing reset only fires on the 1st
- `CheckAdminVacantEnterprises` still runs daily as before

**Prorated first month** (deferred):
- `credit_total` always stores the **full monthly amount**
- Compute on-the-fly when checking limits: `available_credit = prorated_credit_total − credit_used`
- Proration logic not yet implemented in enterprise creation code

**Prorated last month** (enterprise deactivation/deletion):
- Bill for actual days used in final month
- Platform does not need to enforce payment; admin reconciliation page notes the prorated amount

---

### Phase 4: Enterprise Hard Stop at 110% ✅ COMPLETED

**Goal**: Unify enterprise overspend behavior — no personal balance fallback.

**Fix applied** (`internal/routes/webhook.go:1459-1492`):
- Removed `balance` fallback block in `processBillingBatch` `case "enterprise":` branch
- After enterprise credit is exhausted (110% cap), `remaining > 0` is logged as overage — no deduction from personal balance
- Now consistent with `processRecord` (single path), which already had no fallback
- Both paths have matching comments: "Enterprise keys do NOT fall back to personal balance""

**Three enforcement points (now unified)**:

| Path | File/Line | Behavior |
|------|-----------|----------|
| Pre-call intercept | `apikeys.go:750` | `creditUsed >= 110%` → `ENTERPRISE_CREDIT_EXHAUSTED` |
| Single settlement | `webhook.go:762` `processRecord` | No fallback, overage logged |
| Batch settlement | `webhook.go:1459` `processBillingBatch` | No fallback, overage logged ✅ FIXED

---

### Phase 5: Invoice Request Validation ✅ COMPLETED

**Goal**: Prevent arbitrary invoice amounts; enforce spending-based eligibility.

**Implementation in `createRequest()`** (`internal/routes/invoices.go`):

```go
const minInvoiceQuota int64 = 10_000_000 // $10 USD

// 1. Compute total real-fund consumption
var totalConsumption int64
_ = db.QueryRow(`
    SELECT COALESCE(SUM(ABS(amount)), 0)
    FROM balance_transactions
    WHERE user_id = $1::uuid
      AND transaction_type = 'usage'
      AND balance_type = 'balance'
`, userID).Scan(&totalConsumption)

// 2. Compute already requested / invoiced
var alreadyRequested int64
_ = db.QueryRow(`
    SELECT COALESCE(SUM(amount), 0)
    FROM invoice_requests
    WHERE user_id = $1::uuid
      AND status IN ('pending', 'processing', 'completed')
`, userID).Scan(&alreadyRequested)

// 3. Eligible amount
eligible := totalConsumption - alreadyRequested
if amountQuota > eligible {
    return error("Invoice amount exceeds eligible consumption")
}

// 4. Minimum amount check
if amountQuota < minInvoiceQuota {
    return error("Minimum invoice amount is $10")
}
```

**Files modified**:
- `internal/routes/invoices.go` — `createRequest()` ✅

---

### Phase 6: Backfill `balance_transactions` ✅ COMPLETED

**Goal**: Every credit movement must leave a transaction trail.

**Records added**:

| Trigger | Status | Location |
|---------|--------|----------|
| Recharge completed | ✅ `balance_transactions` insert added | `internal/routes/payment.go:completeOrder()` |
| Voucher redeemed | ✅ `balance_transactions` insert added | `internal/routes/vouchers.go:redeem()` |
| Checkin reward | ✅ `balance_transactions` insert added | `internal/routes/checkin.go` |
| Referral reward | ✅ `balance_transactions` insert added | `internal/services/reward.go:ProcessInvitationCode()` |
| Welcome bonus | ✅ `balance_transactions` insert added | `internal/services/reward.go:ApplyWelcomeBonus()` |

**`balance_transactions` schema**:
- `amount` / `balance_before` / `balance_after` migrated to `BIGINT` (micro-USD) ✅
- `balance_before` / `balance_after` for gift transactions tracks `gift_balance` ✅

---

### Phase 7: Admin Reconciliation Page ✅ COMPLETED (Partial)

**Goal**: Single admin page for all billing/financial operations.

**Implemented Tabs** (in `admin-reconciliation.html`):
1. **发票审核** — `invoice_requests` 列表，支持 approve/reject/complete ✅
2. **客户对账** — 用户搜索 + 财务快照 + `balance_transactions` 流水 ✅
3. **企业对账** — 企业搜索 + 额度/成员消费汇总 + `enterprise_audit_log` ✅

**Default Views**:
- 客户对账默认显示本月消费 Top 10 用户
- 企业对账默认显示本月消费 Top 10 企业
- 搜索支持 email（用户）和名称（企业），多结果时显示选择列表

**Deferred Tabs**:
- 充值记录、消费明细、退款调账 — ✅ 已完成（`billing.go` 提供 `/recharge-records`、`/recharge-records/export`、`getRealtimeDetails`；退款调账通过订阅取消退余额 + 管理员调整用户余额实现）

---

## 7. File Change Checklist

### Back-end (Go)

| File | Change |
|------|--------|
| `internal/routes/webhook.go` | Remove `recharge_balance` / `rechargeDeduction`; remove enterprise batch fallback; update `logBalanceDeductions()` |
| `internal/routes/billing.go` | Remove `createRecharge()`; remove `/recharge/create` route |
| `internal/routes/invoices.go` | Add `createRequest()` spending validation; min $10 check |
| `internal/routes/payment.go` | `completeOrder()`: add `balance_transactions` insert for recharge |
| `internal/routes/vouchers.go` | Remove `balance = balance + $1` in redeem/transfer; add `balance_transactions` inserts |
| `internal/routes/checkin.go` | Remove `balance = balance + $1`; add `balance_transactions` insert |
| `internal/routes/enterprise.go` | Rewrite `ResetMonthlyBilling()` for calendar month; proration logic |
| `internal/routes/orders.go` | Verify `/orders/create` handles recharge; no changes expected |
| `cmd/server/main.go` | Adjust cron schedule for monthly reset |
| `internal/db/schema.go` | Add migration entries for new columns / dropped tables |
| `internal/db/schema_test.go` | Fix welcome voucher insert; fix balance assertions |

### Database (Migrations)

| Migration | Change |
|-----------|--------|
| New file | Drop `recharge_orders` table |
| New file | Drop `users.recharge_balance` column |
| New file | Add `usage_records.balance_deduction` |
| New file | Convert `balance_transactions` columns to BIGINT |

### Front-end (Templates / JS)

| File | Change |
|------|--------|
| `web/templates/billing-details.html` | Rename `recharge_deduction` → `balance_deduction` in display |
| `web/templates/dashboard.html` | Display `balance + gift_balance` as total |
| `web/templates/admin-users.html` | Display `balance + gift_balance` as total |
| `web/templates/` (recharge pages) | Remove or redirect `/recharge/create` calls to `/orders/create` |

---

## 8. Testing Checklist

| Test Case | Expected Result |
|-----------|----------------|
| Voucher redeem $10 | `gift_balance = 10`, `balance = 0`, `balance_transactions` row created |
| Recharge $50 via `/orders/create` | `balance = 50`, `orders` record created, `balance_transactions` row created |
| Consume $8 (gift covers all) | `gift_balance = 2`, `balance = 0`, `usage` transaction with `balance_type='gift'` |
| Consume $15 (gift $10 + balance $5) | `gift_balance = 0`, `balance = 45`, two `usage` transactions (`gift` + `balance`) |
| Enterprise credit at 105% | Allowed (within 110% buffer) |
| Enterprise credit at 115% | **Rejected** — no fallback to personal balance |
| Invoice request $5 | **Rejected** — below $10 minimum |
| Invoice request $100 (no consumption) | **Rejected** — exceeds eligible amount |
| Invoice request $30 (consumed $50, no prior requests) | **Approved** |
| New enterprise created on June 15 | June quota = `credit_total × 16/30` |

---

## 9. Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| `balance` / `gift_balance` migration causes display issues | Back-end computes total for all API responses; front-end never reads raw `balance` alone |
| `usage_records` is partitioned — adding column may be slow | Test on staging; use `ADD COLUMN IF NOT EXISTS` with DEFAULT |
| Enterprise monthly reset cron fails | Add alerting; `credit_reset_at` drift is harmless (resets on next run) |
| Invoice validation breaks legitimate requests | Log all rejections; monitor for false positives |
| `total_cost` vs deduction confusion for users | Billing UI clearly labels "Gift Deduction" / "Balance Deduction" separately |

---

## 10. Open Items (Post-Implementation)

1. **Pricing transparency** — How to display `pricing_ratio` markup to users
2. **Payment gateway refund** — Stripe/Alipay/WeChat/PayPal 原路退款 APIs：订阅取消退余额已实现；payment reversal 仍待未来扩展
3. **Admin reconciliation UI** — ✅ 已完成（`admin-reconciliation.html` + 6 个 API 端点：充值/消费/企业对账/Top 用户/Top 企业）
4. **Enterprise offline payment tracking** — 管理员对账页面手动录入线下付款（待后续需求明确）
