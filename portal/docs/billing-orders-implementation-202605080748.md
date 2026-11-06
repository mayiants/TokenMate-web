# Billing & Orders Pages Implementation Design

> **ACTIVE** - Current implementation design document

Created: 2026-05-08 07:48

## Overview

This document describes the implementation design for billing-related pages in AI Portal, including:
- 充值明细 (Recharge Details) - `/orders`
- 计费明细 (Billing Details) - `/billing-details`
- 兑换券管理 (Redemption Management) - `/redemption`
- 账户充值 (Account Recharge) - `/billing`

---

## 1. Page Filter Style Rules

All pages with filtering capabilities should follow a consistent pattern.

### 1.1 Filter Bar Structure

```html
<div class="filters-bar mt-4">
  <!-- Date Range Filter -->
  <div class="filter-group">
    <label class="filter-label">时间范围</label>
    <div class="filter-inputs">
      <input type="date" id="filter-start-date" class="form-input form-input-sm" placeholder="开始日期">
      <span class="filter-separator">至</span>
      <input type="date" id="filter-end-date" class="form-input form-input-sm" placeholder="结束日期">
    </div>
  </div>
  
  <!-- Status Dropdown Filter -->
  <div class="filter-group">
    <label class="filter-label">状态</label>
    <select id="filter-status" class="form-select form-select-sm" onchange="applyFilters()">
      <option value="">全部</option>
      <!-- Status options -->
    </select>
  </div>
  
  <!-- Action Buttons -->
  <div class="filter-actions">
    <button class="btn btn-primary btn-sm" onclick="applyFilters()">筛选</button>
    <button class="btn btn-secondary btn-sm" onclick="resetFilters()">重置</button>
  </div>
</div>
```

### 1.2 CSS Styles (Mandatory)

```css
.filters-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  align-items: flex-end;    /* Important: align items to bottom */
  padding: 16px;
  background: var(--bg-hover);
  border-radius: var(--radius);
}
.filter-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.filter-label {
  font-size: 0.8125rem;
  font-weight: 500;
  color: var(--text-muted);
}
.filter-inputs {
  display: flex;
  align-items: center;
  gap: 8px;
}
.filter-separator {
  font-size: 0.875rem;
  color: var(--text-muted);
}
.filter-actions {
  display: flex;
  gap: 8px;
  align-items: flex-end;
}
.form-input-sm,
.form-select-sm {
  padding: 6px 10px;      /* Must be same for both */
  font-size: 0.875rem;
}
```

### 1.3 JavaScript Behavior Rules

| Element | Behavior | Trigger |
|---------|----------|---------|
| Status dropdown | Auto-refresh | `onchange="applyFilters()"` |
| Date inputs | Manual refresh | Click "筛选" button |
| 筛选 button | Apply all filters | `onclick="applyFilters()"` |
| 重置 button | Clear all, reload | `onclick="resetFilters()"` |

### 1.4 JavaScript Template

```javascript
function applyFilters() {
  loadRecords();  // or loadOrders(), loadData(), etc.
}

function resetFilters() {
  document.getElementById('filter-start-date').value = '';
  document.getElementById('filter-end-date').value = '';
  document.getElementById('filter-status').value = '';
  loadRecords();
}

async function loadRecords() {
  var startDate = document.getElementById('filter-start-date').value;
  var endDate = document.getElementById('filter-end-date').value;
  var status = document.getElementById('filter-status').value;

  var url = '/api/endpoint';
  var params = [];
  if (startDate) params.push('start_date=' + startDate);
  if (endDate) params.push('end_date=' + endDate);
  if (status) params.push('status=' + status);
  if (params.length > 0) url += '?' + params.join('&');

  // Fetch and render...
}
```

---

## 2. Traffic Statistics Analysis

### 2.1 Token Tracking Dimensions

The `usage_records` table tracks tokens in 5 dimensions:

| Field | Description | Billing Impact |
|-------|-------------|----------------|
| `input_tokens` | Total input tokens sent | Primary billing unit |
| `output_tokens` | Total output tokens received | Primary billing unit |
| `cached_input_tokens` | Tokens served from cache | Lower cost (cache hit discount) |
| `effective_input_tokens` | Non-cached input tokens | `input_tokens - cached_input_tokens` |
| `reasoning_tokens` | Chain-of-thought tokens | Higher cost (premium pricing) |

### 2.2 Request Tracking

Each API call creates one `usage_record` with:
- `request_count`: Default 1, can be batched
- `latency_ms`: Response time
- `cache_hit`: Boolean flag
- `error`: Error message if failed

### 2.3 Total Token Calculation

For billing summaries:
```sql
total_tokens = input_tokens + output_tokens
```

Note: `cached_input_tokens` are tracked separately and represent cost savings, not additional tokens.

---

## 3. Billing Logic Analysis

### 3.1 Two Billing Modes

| Mode | Unit | Use Case |
|------|------|----------|
| `token` | Token count | General keys, most subscriptions |
| `request` | Request count | Code plans, some subscriptions |

The billing mode is determined by `user_groups.billing_mode` and inherited by API keys.

### 3.2 Cost Calculation Flow

```
API Request → Proxy → Webhook → Portal
                                    ↓
                            1. Look up API key
                            2. Check subscription quota (if any)
                            3. Calculate cost:
                               - token mode: cost = tokens * price_per_token * pricing_ratio
                               - request mode: cost = requests * price_per_request * pricing_ratio
                            4. Deduct from balance
                            5. Insert usage record
```

### 3.3 Pricing Ratio

**Important**: Pricing ratio is **admin-configurable**, not fixed. Changes take effect immediately for new requests, but **do not affect the current billing cycle** for subscriptions.

| Group Type | Typical Ratio | Meaning |
|------------|---------------|---------|
| `economy` | 0.85 | 15% discount |
| `general` | 1.0 | Standard pricing |
| `subscription` | 1.20 | 20% premium (higher limits) |
| `enterprise` | Custom | Negotiated rate |

**Subscription Billing Cycle Rule**:
- When admin modifies pricing ratio, existing subscriptions continue at the old rate until the current billing cycle ends
- New subscriptions created after the change use the new ratio
- This prevents mid-cycle billing surprises for customers

### 3.4 Balance Deduction

Balance is stored in **cents** (int64) in `users.balance`.

```go
costInCents = int64(cost * 100)
newBalance = currentBalance - costInCents
UPDATE users SET balance = newBalance
```

---

## 4. Redemption Code Billing Interaction

### 4.1 Code Types

| Type | Effect | Storage |
|------|--------|---------|
| `balance` | Adds to user balance | `users.balance` (same field) |
| `token_plan` | Grants subscription | `user_subscriptions` table |
| `code_plan` | Grants request quota | `user_subscriptions` table |
| `welcome` | Welcome bonus | Adds to balance |
| `promo` | Promotional | Adds to balance |

### 4.2 Redemption Process (叠加/Addition)

```go
// 1. Validate code (active, not expired, not exhausted)
// 2. Check user hasn't used this code before
// 3. ADD to balance (叠加)
UPDATE users SET balance = balance + amount WHERE id = userID
// 4. Record redemption
INSERT INTO redemption_records (user_id, code_id, amount, balance_before, balance_after)
```

### 4.3 Balance Interaction (扣除/Deduction)

**Current Implementation**: Single unified balance - no separation between recharged and gifted funds.

```
User Balance = Recharge Amount + Redemption Amount + Check-in Rewards - API Usage Costs
```

**Future Enhancement Needed**: Split balance tracking for:
- 充值账户 (Recharge account) - from actual payments
- 赠送账户 (Gift account) - from redemption codes, check-ins

This is needed for:
1. Invoice calculations (only recharge amounts are invoiceable)
2. Refund policies (only recharge amounts are refundable)
3. Expiration rules (gift amounts may expire sooner)

---

## 4.1 Deduction Priority (关键：扣费优先级)

**All customer types follow this deduction order**:

```
1. 兑换券余额 (Redemption/Gift Balance) - 最先扣除
2. 充值余额 (Recharge Balance) - 其次扣除  
3. Credit额度 (Credit Limit) - 仅Enterprise客户
```

**Implementation Logic**:
```go
func deductCost(userID string, costCents int64) error {
    // 1. Try redemption balance first
    giftDeducted := deductFromGiftBalance(userID, costCents)
    remaining := costCents - giftDeducted
    
    if remaining <= 0 {
        return nil // Fully covered by gift
    }
    
    // 2. Try recharge balance
    rechargeDeducted := deductFromRechargeBalance(userID, remaining)
    remaining -= rechargeDeducted
    
    if remaining <= 0 {
        return nil // Fully covered by recharge
    }
    
    // 3. Try credit (enterprise only)
    if isEnterpriseUser(userID) {
        creditDeducted := deductFromCredit(userID, remaining)
        remaining -= creditDeducted
    }
    
    if remaining > 0 {
        return ErrInsufficientBalance // Service should be stopped
    }
    return nil
}
```

**Key Points**:
- Redemption balance is consumed first (customer gets value from codes)
- Recharge balance is invoiceable, so consumed second
- Credit is enterprise-only, consumed last
- If all sources exhausted, service stops

---

## 5. Invoice Amount Calculation

### 5.1 Current Implementation

Invoice amounts are **user-specified**, not auto-calculated:

```go
// User provides amount when creating invoice request
amountCents = int(req.Amount * 100)
INSERT INTO invoice_requests (user_id, amount, ...)
```

### 5.2 What Should Be Invoiceable

**Invoiceable** (充值金额):
- Direct recharges (orders with payment_method = alipay/wechat/stripe/paypal)
- NOT from redemption codes
- NOT from check-in rewards

**Non-Invoiceable** (赠送金额):
- Redemption code amounts
- Check-in rewards
- Promotional bonuses

### 5.3 Required Calculation (Future Implementation)

```sql
-- Invoiceable amount = Total recharges - Total invoiced
SELECT 
  COALESCE(SUM(amount), 0) as total_recharged
FROM orders 
WHERE user_id = $1 
  AND status = 'completed'
  AND payment_method IN ('alipay', 'wechat', 'stripe', 'paypal');

-- Non-invoiceable amount
SELECT 
  COALESCE(SUM(rr.amount), 0) as total_gifted
FROM redemption_records rr
WHERE rr.user_id = $1;

-- Check-in rewards
SELECT 
  COALESCE(SUM(reward), 0) as total_checkin
FROM checkin_records
WHERE user_id = $1;
```

---

## 6. Three Key Types Billing Separation

### 6.1 Key Type Overview

| Type | Prefix | Billing | Rate Limits | Pricing |
|------|--------|---------|-------------|---------|
| `general` | `sk-wgit-` | Token-based | Standard (60 RPM) | 1.0x |
| `subscription` | `sk-wgit-tp-` | Token or Request | Premium (120+ RPM) | 1.2x |
| `enterprise` | `sk-wgit-en-` | Custom | Custom | Negotiated |

### 6.2 General Keys

- **Billing**: Pure pay-as-you-go from `users.balance`
- **No quota**: Unlimited usage (subject to balance)
- **Group**: `general` type group
- **Deduction**: Direct balance deduction per request

### 6.3 Subscription Keys

- **Two-tier deduction**:
  1. First deduct from subscription quota (tokens or requests)
  2. Then deduct remaining cost from balance
- **Rate limits**: Per-plan TPM/TPH/RPM/RPH limits
- **Auto-renewal**: Optional, deducts from balance 7 days before expiry

```go
// Subscription deduction flow
if subscription exists {
  if quota_remaining >= tokens_used {
    // Deduct from quota only
    quota_remaining -= tokens_used
  } else {
    // Deduct remaining from balance
    remaining_tokens = tokens_used - quota_remaining
    quota_remaining = 0
    balance -= remaining_tokens * price
  }
} else {
  // Direct balance deduction
  balance -= tokens_used * price
}
```

### 6.4 Enterprise Keys

**Enterprise Billing Model**: Post-paid (后付费), NOT pre-paid balance.

**Payment Flow**:
1. Enterprise uses API services throughout the month
2. At month end, system generates invoice based on actual usage
3. Enterprise pays via **bank transfer** (银行转账)
4. Admin manually confirms payment in the system

**Enterprise Pre-charge Option**:
- Enterprise-admin can also pre-charge (充值) to enterprise account
- Pre-charged funds are consumed first (similar to redemption codes)
- This allows enterprises to avoid per-month invoicing overhead

**Visibility Rules**:
| Role | Can See |
|------|---------|
| `enterprise_admin` | Enterprise orders + billing + all member usage |
| `enterprise_user` | Only own general + subscription orders/billing |
| Regular `user` | Only own general + subscription orders/billing |

**Enterprise Credit System**:
- `quota`: Monthly token limit (quota_total, quota_used)
- `credit`: Monthly spending limit in yuan (credit_total, credit_used)
- Both set by admin when creating/modifying enterprise
- Either reaching 100% triggers service stop

### 6.4.1 Enterprise Quota & Credit Alert System

**Quota**: Monthly token limit (tokens)
**Credit**: Monthly spending limit (yuan)

**Alert Thresholds**:
| Threshold | Action |
|-----------|--------|
| 80% | Email warning to enterprise-admin |
| 90% | Email warning to enterprise-admin |
| 100% | **Service STOPPED** + email notification |

**Service Stop Logic**:
```go
// Check before processing each request
func checkEnterpriseLimits(enterpriseID string, tokensRequested int, costCents int64) error {
    var quotaTotal, quotaUsed int64
    var creditTotal, creditUsed int64
    
    // Get enterprise limits
    db.QueryRow(`SELECT quota_total, quota_used, credit_total, credit_used 
                 FROM enterprises WHERE id = $1`, enterpriseID).Scan(...)
    
    // Check quota (tokens)
    if quotaUsed + tokensRequested > quotaTotal {
        return ErrQuotaExceeded // Service stopped
    }
    
    // Check credit (spending)
    if creditUsed + costCents > creditTotal {
        return ErrCreditExceeded // Service stopped
    }
    
    // Check thresholds for alerts
    quotaPercent := float64(quotaUsed) / float64(quotaTotal) * 100
    creditPercent := float64(creditUsed) / float64(creditTotal) * 100
    
    if quotaPercent >= 80 || creditPercent >= 80 {
        sendAlertEmail(enterpriseID, quotaPercent, creditPercent)
    }
    
    return nil
}
```

**Email Alerts Include**:
- Current usage percentage
- Remaining quota/credit
- Estimated days until exhaustion (based on current usage rate)
- Link to enterprise billing dashboard

### 6.5 Display Separation Requirements

For the billing details page, usage should be separated by key type:

```
计费明细
├── General Key 用量
│   ├── Input Tokens: xxx
│   ├── Output Tokens: xxx
│   └── Cost: ¥xxx
├── Subscription Key 用量
│   ├── Quota Used: xxx tokens
│   ├── Quota Remaining: xxx tokens
│   ├── Over-quota Cost: ¥xxx
│   └── Cost: ¥xxx
└── Enterprise Key 用量
    ├── Input Tokens: xxx
    ├── Output Tokens: xxx
    └── Cost: ¥xxx (billed to enterprise)
```

### 6.6 Invoice Separation Options

Users should be able to choose:

1. **合并开票 (Combined)**: Single invoice for all key types
2. **分开开票 (Separate)**: Individual invoices per key type

Implementation requires:
- Track `key_type` on each `usage_record` (already exists via `user_api_key_id` → `api_keys.key_type`)
- Allow invoice requests to specify `key_type` filter
- Calculate invoiceable amounts per key type

---

## 7. Database Schema Summary

### Core Tables

| Table | Key Fields | Purpose |
|-------|------------|---------|
| `users` | `balance` (cents), `group_id`, `user_type` | User accounts |
| `api_keys` | `key_type`, `billing_mode`, `group_id` | API keys |
| `user_groups` | `group_type`, `billing_mode`, `pricing_ratio` | Billing config |
| `usage_records` | `input_tokens`, `output_tokens`, `total_cost` | Usage tracking (partitioned) |
| `user_subscriptions` | `quota_remaining`, `daily_limit` | Subscription quotas |
| `redemption_codes` | `type`, `amount`, `status` | Redemption codes |
| `redemption_records` | `user_id`, `code_id`, `amount` | Redemption history |
| `orders` | `amount`, `status`, `payment_method` | Payment orders |
| `invoice_requests` | `amount`, `status` | Invoice requests |
| `enterprises` | `quota_total`, `quota_used`, `credit_total`, `credit_used` | Enterprise accounts |

### Amount Storage Convention

| Field | Unit | Example |
|-------|------|---------|
| `users.balance` | Cents (int64) | 10000 = ¥100.00 |
| `orders.amount` | Cents (int) | 5000 = ¥50.00 |
| `redemption_codes.amount` | Cents (int) | 2000 = ¥20.00 |
| `usage_records.total_cost` | Yuan (float64) | 0.004 = 0.4分 |
| `invoice_requests.amount` | Cents (int64) | 100000 = ¥1000.00 |

---

## 8. Implementation Roadmap

### Phase 1 (Current)
- [x] Basic billing pages
- [x] Order listing with filters
- [x] Redemption management
- [x] Basic usage statistics

### Phase 2 (Next)
- [ ] Split balance tracking (recharge vs gift)
- [ ] Per-key-type billing separation
- [ ] Invoice amount auto-calculation
- [ ] Detailed billing breakdown (input/output/cache/reasoning)
- [ ] Enterprise credit system implementation
- [ ] Enterprise quota/credit alerts at 80%/90%/100%

### Phase 3 (Future)
- [ ] Invoice split options (combined vs separate)
- [ ] Enterprise billing portal with post-paid invoicing
- [ ] Subscription auto-renewal management
- [ ] Usage alerts and budgets
- [ ] Enterprise admin payment confirmation workflow

---

## 9. API Endpoints Reference

### Billing APIs (`/api/billing`)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/balance` | GET | Get user balance |
| `/usage-summary` | GET | Daily usage summary |
| `/usage-records` | GET | Paginated usage records |
| `/usage-by-key` | GET | Usage grouped by API key |
| `/usage-by-model` | GET | Usage grouped by model |
| `/realtime` | GET | Today's realtime usage |
| `/monthly` | GET | Monthly usage summary |
| `/requests` | GET | Request-level details |
| `/realtime/export` | GET | Export realtime CSV |
| `/monthly/export` | GET | Export monthly CSV |
| `/requests/export` | GET | Export requests CSV |

### Order APIs (`/api/orders`)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | List orders with filters |
| `/export` | GET | Export orders CSV |
| `/:id` | GET | Get order details |
| `/create` | POST | Create new order |
| `/:id/cancel` | POST | Cancel pending order |

### Redemption APIs (`/api/redemption`)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/redeem` | POST | Redeem a code |
| `/my-records` | GET | User's redemption history |
| `/admin/codes` | GET | Admin: list all codes |
| `/admin/codes` | POST | Admin: create codes |

---

## 10. Detailed Implementation Plan

### 10.1 Current State Analysis

**Problems Found**:
1. `users.balance` is a single BIGINT (cents) - no separation between recharge/gift
2. `enterprises` has only `quota_total`/`quota_used` - no credit fields
3. `usage_records` has no fund source tracking columns
4. `billing.go` references non-existent columns: `project_name`, `project_id`, `api_key_name`
5. `recharge_orders` table referenced in code but has no migration

**Existing Schema Issues**:
```sql
-- users table: single balance field
balance BIGINT DEFAULT 0  -- in cents (分)

-- enterprises table: no credit fields
quota_total BIGINT DEFAULT 0
quota_used BIGINT DEFAULT 0
```

---

### 10.2 Database Migration Plan

#### Migration: Balance Splitting

```sql
-- Step 1: Add new balance fields to users table
ALTER TABLE users ADD COLUMN gift_balance BIGINT DEFAULT 0;
ALTER TABLE users ADD COLUMN recharge_balance BIGINT DEFAULT 0;

-- Step 2: Migrate existing balance (optional, test phase can skip)
-- UPDATE users SET recharge_balance = balance WHERE balance > 0;

-- Step 3: Keep old balance field for backward compatibility during transition
-- Will be removed after full migration
```

#### Migration: Enterprise Credit System

```sql
-- Step 1: Add credit fields to enterprises table
ALTER TABLE enterprises ADD COLUMN credit_total DECIMAL(12,2) DEFAULT 0;
ALTER TABLE enterprises ADD COLUMN credit_used DECIMAL(12,2) DEFAULT 0;
ALTER TABLE enterprises ADD COLUMN credit_reset_at TIMESTAMP;

-- Step 2: Add admin payment confirmation fields
ALTER TABLE enterprises ADD COLUMN last_payment_confirmed_at TIMESTAMP;
ALTER TABLE enterprises ADD COLUMN last_payment_confirmed_by UUID REFERENCES users(id);
```

#### Migration: Fund Source Tracking

```sql
-- Step 1: Add deduction tracking to usage_records
ALTER TABLE usage_records ADD COLUMN gift_deduction DECIMAL(18,10) DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN recharge_deduction DECIMAL(18,10) DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN credit_deduction DECIMAL(18,10) DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN subscription_deduction DECIMAL(18,10) DEFAULT 0;

-- Step 2: Add missing columns referenced in code
ALTER TABLE usage_records ADD COLUMN project_name VARCHAR(255);
ALTER TABLE usage_records ADD COLUMN project_id VARCHAR(255);
ALTER TABLE usage_records ADD COLUMN api_key_name VARCHAR(255);
```

#### Migration: Balance Transaction Log

```sql
-- New table for tracking all balance changes
CREATE TABLE balance_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    enterprise_id UUID REFERENCES enterprises(id),
    transaction_type VARCHAR(50) NOT NULL,  -- 'recharge', 'redemption', 'checkin', 'usage', 'refund', 'admin_adjust'
    amount DECIMAL(12,2) NOT NULL,          -- positive = credit, negative = debit
    balance_type VARCHAR(20) NOT NULL,      -- 'gift', 'recharge', 'credit'
    balance_before DECIMAL(12,2) NOT NULL,
    balance_after DECIMAL(12,2) NOT NULL,
    reference_id VARCHAR(255),              -- order_id, redemption_id, usage_record_id, etc.
    description TEXT,
    created_by UUID REFERENCES users(id),   -- admin who performed action (if applicable)
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_balance_transactions_user ON balance_transactions(user_id);
CREATE INDEX idx_balance_transactions_enterprise ON balance_transactions(enterprise_id);
CREATE INDEX idx_balance_transactions_type ON balance_transactions(transaction_type);
CREATE INDEX idx_balance_transactions_created ON balance_transactions(created_at);
```

---

### 10.3 Deduction Priority Implementation

#### Current Flow (webhook.go processRecord)

```
1. Look up API key -> get userID
2. Check subscription quota -> deduct tokens
3. Convert cost to cents
4. SELECT balance FROM users FOR UPDATE
5. UPDATE users SET balance = balance - costInCents
6. INSERT usage_record
```

#### New Flow (with priority)

```
1. Look up API key -> get userID, keyType
2. Check subscription quota -> deduct tokens (if subscription key)
3. Convert cost to cents
4. BEGIN TRANSACTION
5. Calculate deduction breakdown:
   - gift_deduction = min(costCents, gift_balance)
   - remaining = costCents - gift_deduction
   - recharge_deduction = min(remaining, recharge_balance)
   - remaining -= recharge_deduction
   - credit_deduction = min(remaining, credit_available)  -- enterprise only
   - remaining -= credit_deduction
6. UPDATE users SET 
     gift_balance = gift_balance - gift_deduction,
     recharge_balance = recharge_balance - recharge_deduction
7. UPDATE enterprises SET credit_used = credit_used + credit_deduction  -- if enterprise
8. INSERT usage_record with deduction breakdown
9. INSERT balance_transactions for each deduction
10. COMMIT TRANSACTION
11. If remaining > 0, log warning (user can go negative)
```

#### Async Billing Error Analysis

**Current System**:
- Redis queue: 1-second ticker, 100 records/tick = max 100 records/sec
- Direct processing: Synchronous transaction per request
- No retry mechanism for failed records
- No idempotency (same traceId can be inserted multiple times)

**Error Range Estimation**:

| Scenario | Error Impact | Mitigation |
|----------|--------------|------------|
| Queue delay (1 sec) | User can use ~100 requests before balance check | Acceptable for most use cases |
| Proxy crash before webhook | Usage recorded but not billed | Monitor for orphaned records |
| DB transaction failure | Usage not recorded, balance not deducted | Retry mechanism needed |
| Duplicate traceId | Double billing possible | Add UNIQUE constraint on trace_id |

**Recommended Mitigations**:
1. Add `UNIQUE(trace_id)` constraint to prevent double billing
2. Implement retry mechanism for failed queue records
3. Add periodic reconciliation job to detect orphaned usage records
4. Set reasonable negative balance limits (e.g., -100 yuan)

---

### 10.4 API Endpoints Design

#### Extend Existing Endpoints

**`GET /api/billing/balance`** - Add gift/recharge breakdown:
```json
{
  "balance": 10000,           // Total (backward compat)
  "balanceInCents": 10000,
  "gift_balance": 2000,       // NEW
  "recharge_balance": 8000,   // NEW
  "gift_balance_yuan": 20.00,
  "recharge_balance_yuan": 80.00
}
```

**`GET /api/billing/realtime`** - Add deduction source:
```json
{
  "records": [
    {
      "product_name": "completion",
      "product_type": "模型推理",
      "total_cost": 0.00401,
      "gift_deduction": 0.00401,      // NEW
      "recharge_deduction": 0.00000,  // NEW
      "credit_deduction": 0.00000     // NEW
    }
  ]
}
```

**`GET /api/billing/requests`** - Add deduction source per request:
```json
{
  "records": [
    {
      "trace_id": "xxx",
      "model": "gpt-4o",
      "total_cost": 0.005,
      "gift_deduction": 0.002,        // NEW
      "recharge_deduction": 0.003,    // NEW
      "credit_deduction": 0.000       // NEW
    }
  ]
}
```

#### New Endpoints

**Enterprise Billing** (under `/api/enterprise/billing`):

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/summary` | GET | Enterprise billing summary (quota, credit, usage) |
| `/credit-history` | GET | Credit usage history |
| `/quota-history` | GET | Quota usage history |
| `/reset-credit` | POST | Admin: Reset credit after payment |
| `/reset-quota` | POST | Admin: Reset quota after payment |

**Admin Enterprise Management** (under `/api/admin/enterprise`):

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/:id/credit` | PUT | Update enterprise credit limit |
| `/:id/quota` | PUT | Update enterprise quota limit |
| `/:id/confirm-payment` | POST | Confirm enterprise payment |
| `/:id/reset-limits` | POST | Reset quota/credit after payment |

---

### 10.5 Enterprise Credit/Quota Alert System

#### Alert Thresholds

```go
type AlertLevel int

const (
    AlertNone    AlertLevel = 0
    AlertWarning AlertLevel = 80  // 80% - email warning
    AlertDanger  AlertLevel = 90  // 90% - email warning
    AlertStop    AlertLevel = 100 // 100% - service stopped
)
```

#### Alert Email Template

```
Subject: [AI Portal] Enterprise Quota/Credit Alert - {enterprise_name}

Dear Enterprise Admin,

Your enterprise account has reached {percent}% of its {limit_type}.

Current Status:
- {limit_type} Total: {total}
- {limit_type} Used: {used}
- {limit_type} Remaining: {remaining}
- Estimated days until exhaustion: {days_remaining}

{if percent >= 100}
⚠️ SERVICE SUSPENDED
Your API access has been suspended due to reaching the {limit_type} limit.
Please contact support or make a payment to resume service.
{else}
Please consider upgrading your plan or reducing usage to avoid service interruption.
{end}

View your enterprise dashboard: {dashboard_url}
```

#### Implementation in Webhook

```go
func checkEnterpriseLimits(ctx context.Context, enterpriseID string, tokens int64, costCents int64) error {
    var quotaTotal, quotaUsed int64
    var creditTotal, creditUsed float64
    
    err := db.QueryRow(ctx, `
        SELECT quota_total, quota_used, credit_total, credit_used 
        FROM enterprises WHERE id = $1`, enterpriseID).Scan(...)
    
    // Check if limits are set (0 = unlimited)
    if quotaTotal > 0 {
        if quotaUsed + tokens > quotaTotal {
            return ErrQuotaExceeded
        }
        // Check thresholds
        percent := float64(quotaUsed+tokens) / float64(quotaTotal) * 100
        if percent >= 80 {
            go sendQuotaAlert(enterpriseID, percent)
        }
    }
    
    if creditTotal > 0 {
        costYuan := float64(costCents) / 100
        if creditUsed + costYuan > creditTotal {
            return ErrCreditExceeded
        }
        percent := (creditUsed+costYuan) / creditTotal * 100
        if percent >= 80 {
            go sendCreditAlert(enterpriseID, percent)
        }
    }
    
    return nil
}
```

#### Admin Warning for Unlimited Limits

When admin sets quota_total=0 or credit_total=0, show confirmation dialog:

```
⚠️ WARNING: Unlimited {limit_type}

You are setting {limit_type} to unlimited (0). This means:
- The enterprise will have no usage restrictions
- This could lead to unexpected high charges
- Only do this for trusted enterprise customers

Are you sure you want to continue?

[Cancel] [Confirm]
```

---

### 10.6 Implementation Sequence

#### Phase 1: Database Schema (Priority: High)

1. Create migration for balance splitting
2. Create migration for enterprise credit fields
3. Create migration for fund source tracking
4. Create migration for balance_transactions table

#### Phase 2: Core Logic (Priority: High)

1. Update webhook.go processRecord() with new deduction priority
2. Add balance_transactions logging
3. Update balance API to return gift/recharge breakdown
4. Add enterprise limit checking in webhook

#### Phase 3: API Endpoints (Priority: Medium)

1. Extend existing billing endpoints with deduction source
2. Add enterprise billing endpoints
3. Add admin enterprise management endpoints

#### Phase 4: Frontend (Priority: Medium)

1. Update billing pages to show gift/recharge breakdown
2. Add enterprise billing dashboard
3. Add admin enterprise management UI

#### Phase 5: Alerts & Monitoring (Priority: Low)

1. Implement enterprise quota/credit alerts
2. Add reconciliation job for orphaned records
3. Add monitoring for negative balances

---

### 10.7 Testing Checklist

- [ ] Balance splitting: gift_balance and recharge_balance update correctly
- [ ] Deduction priority: gift > recharge > credit
- [ ] Enterprise credit: stops service at 100%
- [ ] Enterprise quota: stops service at 100%
- [ ] Unlimited limits (0): admin warning shown
- [ ] Fund source tracking: each usage_record has correct deduction breakdown
- [ ] Balance transactions: all balance changes logged
- [ ] Alert emails: sent at 80%/90%/100% thresholds
- [ ] Async billing: error range within acceptable limits
- [ ] Idempotency: duplicate trace_id handled correctly
