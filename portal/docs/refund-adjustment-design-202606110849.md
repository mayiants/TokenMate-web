# Refund & Adjustment Design Document

> **Status**: Draft  
> **Date**: 2026-06-11  
> **Scope**: ai-portal-go billing & reconciliation  
> **Author**: AI Agent

---

## 1. Background & Constraints

### 1.1 Why Gateway Refund Is Not Viable Now

| Constraint | Detail | Impact |
|------------|--------|--------|
| **Upstream non-refundable** | OpenAI, Anthropic, Google, Azure, Alibaba Cloud, etc. bill by actual API call. Once tokens are consumed, the cost is irreversible. | We cannot get money back from model providers. |
| **Gateway untested** | Stripe / Alipay / WeChat Pay refund APIs are not implemented or tested in `pkg/payment/`. | High risk of production failure if enabled without testing. |
| **Refund fees** | Stripe keeps the processing fee (~2.9% + $0.30) even on refunds. Alipay/WeChat also charge reversal fees. | Every gateway refund costs us real money on top of the irreversible upstream cost. |
| **Cash-flow mismatch** | User pays us → we pay upstream. Upstream bill is settled. Refunding user means we bear the upstream cost. | Financially unsustainable for high-volume API consumption. |

**Conclusion**: Gateway refund (原路退款) is **deferred** to Phase 5 (future). The immediate solution must work **without payment-gateway integration**.

---

## 2. Refund Pathway Matrix

| # | Pathway | User Gets | Gateway Needed? | Fee | Speed | Best For |
|---|---------|-----------|-----------------|-----|-------|----------|
| 1 | **Balance Credit** | `users.balance` increase | ❌ No | $0 | Instant | Service credits, billing-error fixes, small amounts |
| 2 | **Voucher Credit** | Reusable voucher code | ❌ No | $0 | Instant | Promotional goodwill, user-retention campaigns |
| 3 | **Quota Credit** | Subscription or enterprise quota | ❌ No | $0 | Instant | Subscription cancellations, enterprise overcharges |
| 4 | **Offline Transfer** | Bank / Alipay manual transfer | ❌ No | Bank fee only | 1–3 days | Large amounts, regulatory requirements, persistent users |
| 5 | **Gateway Refund** | Money back to original card/wallet | ✅ Yes | Gateway fee | 3–15 days | **Deferred** — only when legally required |

> **Design principle**: Default to **Balance Credit** for all automated refunds. Use **Offline Transfer** only for exceptional cases requiring audit trail.

---

## 3. What Is Refundable vs. Not

### 3.1 Refundable Scenarios

| Scenario | Refund Path | Example |
|----------|-------------|---------|
| **Duplicate recharge** | Balance Credit | User clicks pay twice, two completed orders. |
| **Unused balance** | Balance Credit | User recharges $100, consumes $0 within 30 days. |
| **Billing error** | Balance Credit + Audit Log | System double-charged a request; we reverse the deduction. |
| **Subscription cancellation** | Quota Credit → Balance Credit (existing) | `cancelSubscription` already prorates unused quota to `balance`. |
| **Enterprise overcharge** | Quota Credit | Admin mistakenly set `credit_total` too high; adjust downward. |
| **Service outage compensation** | Voucher Credit | Platform downtime; give users a voucher as apology. |

### 3.2 Non-Refundable (By Policy)

| Scenario | Reason |
|----------|--------|
| **Consumed API calls** | Upstream cost already incurred and non-reversible. |
| **Gift balance / voucher / check-in rewards** | Promotional credits have no cash value. |
| **Expired subscription** | Time-based service already rendered. |
| **Orders > 90 days old** | Accounting period closed; requires manual CFO approval (offline transfer only). |

---

## 4. Data Model

### 4.1 New Table: `refund_requests`

```sql
CREATE TABLE IF NOT EXISTS refund_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    order_id VARCHAR(50),                 -- original order (optional for billing-error refunds)
    amount BIGINT NOT NULL,               -- micro-USD, always positive
    reason TEXT NOT NULL,                 -- user-submitted reason
    refund_type VARCHAR(20) NOT NULL      -- 'balance' | 'voucher' | 'quota' | 'offline' | 'gateway'
        CHECK (refund_type IN ('balance', 'voucher', 'quota', 'offline', 'gateway')),
    status VARCHAR(20) DEFAULT 'pending'  -- 'pending' | 'approved' | 'rejected' | 'completed' | 'cancelled'
        CHECK (status IN ('pending', 'approved', 'rejected', 'completed', 'cancelled')),
    admin_id UUID REFERENCES users(id),   -- who approved/rejected
    admin_note TEXT,                      -- internal note
    completed_at TIMESTAMP,               -- when compensation was executed
    rejection_reason TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_refund_requests_user ON refund_requests(user_id);
CREATE INDEX IF NOT EXISTS idx_refund_requests_status ON refund_requests(status);
CREATE INDEX IF NOT EXISTS idx_refund_requests_type ON refund_requests(refund_type);
```

### 4.2 New Table: `adjustments`

```sql
CREATE TABLE IF NOT EXISTS adjustments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    enterprise_id UUID REFERENCES enterprises(id),  -- optional, for enterprise credit adjustments
    amount BIGINT NOT NULL,                         -- positive = credit, negative = debit
    adjustment_type VARCHAR(20) NOT NULL            -- 'manual_credit' | 'manual_debit' | 'billing_correction' | 'compensation'
        CHECK (adjustment_type IN ('manual_credit', 'manual_debit', 'billing_correction', 'compensation')),
    balance_type VARCHAR(20) NOT NULL               -- 'balance' | 'gift' | 'credit'
        CHECK (balance_type IN ('balance', 'gift', 'credit')),
    description TEXT NOT NULL,
    admin_id UUID NOT NULL REFERENCES users(id),
    reference_id VARCHAR(255),                      -- e.g. trace_id for billing correction
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_adjustments_user ON adjustments(user_id);
CREATE INDEX IF NOT EXISTS idx_adjustments_admin ON adjustments(admin_id);
```

### 4.3 Reuse Existing Tables

| Existing Table | How We Reuse It |
|----------------|-----------------|
| `orders` | Insert a row with `product_type = 'balance_refund'` or `'adjustment_credit'` to keep an immutable financial record. Amount is positive (credit to user). |
| `balance_transactions` | Insert `transaction_type = 'refund'` or `'adjustment'`, `balance_type = 'balance'`/`'gift'`/`'credit'`, with `balance_before` / `balance_after` snapshots. |
| `user_subscriptions` | For quota refunds, update `quota_remaining` directly (existing pattern in `cancelSubscription`). |
| `enterprises` | For enterprise adjustments, update `credit_total` or `credit_used` directly. |

### 4.4 Balance Transaction Semantics

```
transaction_type = 'refund'    → user-initiated refund request, approved by admin
transaction_type = 'adjustment' → admin-initiated correction (debit or credit)

balance_type = 'balance'  → real money (users.balance)
balance_type = 'gift'     → promotional (users.gift_balance)
balance_type = 'credit'   → enterprise quota (enterprises.credit_total / credit_used)
```

**Important**: `amount` is always stored as **positive for credit, negative for debit** in `balance_transactions` to stay consistent with existing `recharge` (+) and `usage` (−) patterns.

---

## 5. Business Rules & Workflows

### 5.1 Refund Eligibility Engine (Backend Validation)

```go
func (r *RefundRoutes) validateRefundEligibility(
    ctx context.Context,
    userID string,
    orderID string,
    requestedAmount int64,
) (eligibleAmount int64, reason string, ok bool) {
    // Rule 1: Order must exist and be completed
    var orderStatus string
    var orderAmount, orderCreatedDays int64
    r.db.QueryRow(ctx,
        `SELECT status, amount, EXTRACT(DAY FROM NOW() - created_at)::int
         FROM orders WHERE order_id = $1 AND user_id = $2::uuid`,
        orderID, userID,
    ).Scan(&orderStatus, &orderAmount, &orderCreatedDays)

    if orderStatus != "completed" {
        return 0, "Order not completed", false
    }

    // Rule 2: 90-day window
    if orderCreatedDays > 90 {
        return 0, "Order exceeds 90-day refund window", false
    }

    // Rule 3: Cannot refund more than order amount
    if requestedAmount > orderAmount {
        return 0, "Refund exceeds order amount", false
    }

    // Rule 4: For recharge orders, check if balance was already consumed
    // Eligible = orderAmount - already_refunded - consumed_from_this_recharge
    var alreadyRefunded int64
    r.db.QueryRow(ctx,
        `SELECT COALESCE(SUM(amount), 0) FROM refund_requests
         WHERE order_id = $1 AND status IN ('approved', 'completed')`,
        orderID,
    ).Scan(&alreadyRefunded)

    // Simplified: assume FIFO consumption. All balance is pooled, so we use
    // a heuristic: if total_usage > total_recharge - requestedAmount, deny.
    var totalRecharge, totalUsage int64
    r.db.QueryRow(ctx,
        `SELECT COALESCE(SUM(amount), 0) FROM balance_transactions
         WHERE user_id = $1::uuid AND transaction_type = 'recharge'`,
        userID,
    ).Scan(&totalRecharge)
    r.db.QueryRow(ctx,
        `SELECT COALESCE(SUM(ABS(amount)), 0) FROM balance_transactions
         WHERE user_id = $1::uuid AND transaction_type = 'usage' AND balance_type = 'balance'`,
        userID,
    ).Scan(&totalUsage)

    remainingNetBalance := totalRecharge - totalUsage - alreadyRefunded
    if requestedAmount > remainingNetBalance {
        return remainingNetBalance,
            "Refund exceeds remaining un-consumed balance", false
    }

    return requestedAmount, "", true
}
```

> **Note**: The exact "which recharge paid for which usage" attribution is inherently ambiguous because all balances are pooled. We use **net-balance heuristic** (total recharge − total real-fund usage). This is the same logic already used by the invoice eligibility check in `invoices.go`.

### 5.2 Workflow: User-Initiated Refund

```
[User] submits refund request → POST /refunds
    ↓
[Backend] validate eligibility (§5.1)
    ↓
[Database] INSERT refund_requests (status='pending')
    ↓
[Admin] reviews in Admin Reconciliation → PUT /admin/refunds/:id/approve
    ↓
[Backend] execute compensation based on refund_type
    ↓
[Database] UPDATE refund_requests (status='completed')
           INSERT orders (product_type='balance_refund')
           INSERT balance_transactions (transaction_type='refund')
    ↓
[User] receives balance / voucher / quota immediately
```

### 5.3 Workflow: Admin-Initiated Adjustment (调账)

```
[Admin] opens Admin Reconciliation → "调账录入" tab
    ↓
[Admin] selects user, enters amount (+ or −), reason, type
    ↓
[Backend] requires dual-approval if |amount| > $500 USD
    ↓
[Database] INSERT adjustments
           INSERT balance_transactions (transaction_type='adjustment')
           UPDATE users.balance (or enterprises.credit_total)
    ↓
[Audit] audit.Log() + email notification to user
```

### 5.4 Dual-Approval Gate

| Threshold | Requirement |
|-----------|-------------|
| ≤ $100 USD | Single admin approval |
| $100 – $500 USD | Admin + system_admin approval |
| > $500 USD | Admin + system_admin + audit log + email to finance team |

---

## 6. API Design

### 6.1 User Endpoints

```
POST   /api/refunds              → create refund request
GET    /api/refunds              → list my refund requests
GET    /api/refunds/:id          → get refund request detail
DELETE /api/refunds/:id          → cancel pending request (user)
```

### 6.2 Admin Endpoints

```
GET    /api/admin/refunds                 → list all refund requests (filter by status, type)
PUT    /api/admin/refunds/:id/approve     → approve + execute compensation
PUT    /api/admin/refunds/:id/reject      → reject with reason
POST   /api/admin/adjustments             → create manual adjustment (调账)
GET    /api/admin/adjustments             → list adjustments
GET    /api/admin/adjustments/summary     → daily/weekly adjustment totals
```

---

## 7. Admin Reconciliation Page Integration

Two new tabs added to the existing `admin-reconciliation.html`:

### Tab 4: 退款审核 (Refund Review)

| Column | Description |
|--------|-------------|
| 申请时间 | `refund_requests.created_at` |
| 用户 | email + name |
| 原订单 | order_id (clickable) |
| 申请金额 | `amount` in USD |
| 退款方式 | balance / voucher / quota / offline |
| 原因 | `reason` |
| 状态 | pending / approved / rejected / completed |
| 操作 | Approve / Reject / View Detail |

**Actions**:
- **Approve**: Opens modal to confirm refund type and amount → executes compensation → marks completed.
- **Reject**: Modal for rejection reason → marks rejected.

### Tab 5: 调账录入 (Adjustment Entry)

Form fields:
- 用户搜索 (email / user_id)
- 调整类型: 手动加款 / 手动扣款 / 计费修正 / 服务补偿
- 资金类型: 真实余额 / 赠送余额 / 企业额度
- 金额 (USD)
- 原因 / 备注
- 关联 Trace ID (for billing corrections)

**Submit**: Creates `adjustments` + `balance_transactions` row.

---

## 8. Implementation Phases

### Phase 1: Balance-Credit Refund (P0 — No Gateway Dependency)

**Goal**: Enable the most common refund path immediately.

- [ ] Add `refund_requests` table to `schema.go`
- [ ] Add `balance_refund` to `orders.product_type` enum handling
- [ ] Implement `RefundRoutes` (user create/list/cancel)
- [ ] Implement admin approve/reject endpoints
- [ ] On approve: `UPDATE users SET balance = balance + $amount` + `INSERT balance_transactions`
- [ ] On approve: `INSERT orders` with `product_type = 'balance_refund'`
- [ ] Add "退款审核" tab to `admin-reconciliation.html`
- [ ] Eligibility validation (§5.1)

### Phase 2: Adjustment System (P1 — Admin Correction)

**Goal**: Allow admins to fix billing errors without user involvement.

- [ ] Add `adjustments` table to `schema.go`
- [ ] Implement `POST /admin/adjustments` (credit/debit)
- [ ] Dual-approval gate for amounts > $100 / $500
- [ ] Add "调账录入" tab to `admin-reconciliation.html`
- [ ] Audit logging for every adjustment
- [ ] Email notification to user on adjustment

### Phase 3: Voucher & Quota Compensation (P1)

**Goal**: Non-cash compensation paths.

- [ ] Voucher refund: auto-create a voucher via existing `vouchers.go` logic
- [ ] Quota refund: extend subscription or enterprise credit (leverage existing `cancelSubscription` pattern)
- [ ] Update `refund_requests.refund_type` validation to allow `voucher` / `quota`

### Phase 4: Offline Transfer Tracking (P2)

**Goal**: Support large refunds that must leave the platform.

- [ ] Add `offline_transfer_proof` TEXT column (URL to uploaded image/PDF)
- [ ] Admin uploads transfer receipt before marking completed
- [ ] Update `orders` with `payment_method = 'offline_transfer'`
- [ ] Require CFO-level admin role for offline refunds

### Phase 5: Gateway Refund (Deferred — P3)

**Goal**: True original-path refund when legally required.

- [ ] Implement `Refund()` method in `pkg/payment/stripe.go`, `alipay.go`, `wechat.go`
- [ ] Add gateway-specific error handling (partial refunds, expired refund windows)
- [ ] Test in sandbox for each gateway
- [ ] Enable only for orders where user explicitly demands cash refund and admin approves

---

## 9. Financial & Compliance Safeguards

### 9.1 Anti-Fraud Measures

| Measure | Implementation |
|---------|----------------|
| **Rate limit** | Max 1 refund request per user per 7 days. |
| **Amount cap** | Single refund ≤ original order amount. Cumulative refunds ≤ net un-consumed balance. |
| **Cooldown** | Cannot refund an order within 24 hours of completion (prevents instant arbitrage). |
| **New-user block** | Users registered < 7 days cannot request refunds (prevents abuse). |
| **IP / device fingerprint** | Flag if multiple accounts request refunds from same IP. |

### 9.2 Audit Trail

Every refund or adjustment produces **3 immutable records**:

1. `refund_requests` or `adjustments` row — the administrative decision
2. `orders` row — the financial record (product_type = `balance_refund` / `adjustment_credit`)
3. `balance_transactions` row — the ledger entry with before/after balances

All three share the same `reference_id` for cross-referencing.

### 9.3 Accounting Period Safety

- Refunds for orders in a **closed accounting month** require `system_admin` or `super_admin` approval.
- Monthly reconciliation report must include:
  - Total refunds issued
  - Total adjustments (credit + debit)
  - Net impact on revenue

---

## 10. Risk Matrix

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| User abuse (charge-then-refund loop) | Medium | Revenue loss | 24h cooldown, 7-day rate limit, net-balance check. |
| Admin error (wrong amount credited) | Low | Revenue loss | Dual-approval gate, before/after balance snapshots, undo adjustment function. |
| Invoice + Refund double-dip | Medium | Tax/accounting issue | Deduct refunded amount from invoice eligibility (`invoices.go` already uses `balance_transactions`). |
| Cash-flow drain from offline refunds | Low | Liquidity | Require CFO approval, cap monthly offline refund budget. |
| User disputes "balance credit" vs "cash" | Medium | Support burden | Clear messaging: "Refund issued as platform credit. Contact support for cash refund." |

---

## 11. Open Questions

1. **Should balance-credit refunds expire?** Some platforms expire refunded credits after 12 months to prevent liability buildup.
2. **Do we need a separate `refundable_balance` field?** To prevent users from spending the exact amount they just requested for refund.
3. **Enterprise refund ownership**: If an enterprise admin requests refund for a member's overcharge, who receives the credit — enterprise or member?
4. **Currency handling**: If user paid in CNY via Alipay but requests refund 60 days later when exchange rate moved, do we refund in CNY at original rate or current USD equivalent?

---

## Appendix A: Existing Refund-Related Code

| File | Line | What It Does |
|------|------|--------------|
| `subscriptions.go` | 657–759 | `cancelSubscription` — prorates unused quota to `users.balance`, creates `orders` row with `product_type='subscription_refund'`. |
| `payment.go` | 623–688 | `completeOrder` — credits `users.balance` on recharge, logs `balance_transactions` with `transaction_type='recharge'`. |
| `webhook.go` | 937–958 | `logBalanceDeductions` — writes `balance_transactions` for every API usage deduction. |
| `invoices.go` | 455–489 | `createRequest` — validates invoice amount against real-fund consumption from `balance_transactions`. |
