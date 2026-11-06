# Billing Logic Test Guide

> **ACTIVE** — Current guide for billing implementation and testing

## Overview

This document describes the Portal billing implementation, including cost calculation logic, group pricing ratio, balance deduction priority, and end-to-end test procedures.

The Proxy reports raw token usage via webhook; the Portal calculates cost from model pricing and deducts from user balances.

---

## Implementation Logic

### 1. Webhook Flow

```
Proxy sends usage report → Portal webhook handler → Redis queue → Queue processor
                                         ↓
                                PostgreSQL (usage_records + balance update)
```

The webhook handler (`handleUsage`) only queues the record into Redis. A background goroutine (`processQueue`) pops records and calls `processRecord()` within a database transaction.

### 2. Model Pricing Query

The Portal reads pricing from `service_models` by matching the model name from the webhook payload.

**Two-step matching** (to handle provider mismatch between Proxy and DB):

```go
// Step 1: Exact match with provider + model name
SELECT input_price_cache_hit, input_price_cache_miss, output_price, reasoning_price
FROM service_models
WHERE provider = $provider AND (api_model_name = $model OR product_id = $model)
LIMIT 1

// Step 2 (fallback): Match by model name only
SELECT input_price_cache_hit, input_price_cache_miss, output_price, reasoning_price
FROM service_models
WHERE api_model_name = $model OR product_id = $model
LIMIT 1
```

If no pricing is found, a default fallback is used:
- `inputPriceCacheMiss = $0.01/1M`
- `outputPrice = $0.03/1M`

### 3. Cost Calculation

```
inputCostUSD  = cachedTokens/1e6 × inputPriceCacheHit
              + (inputTokens - cachedTokens)/1e6 × inputPriceCacheMiss
              + cacheCreationTokens/1e6 × inputPriceCacheMiss

outputCostUSD = outputTokens/1e6 × outputPrice
reasoningCostUSD = reasoningTokens/1e6 × reasoningPrice

totalUSDCost = inputCostUSD + outputCostUSD + reasoningCostUSD
totalCNYCost = totalUSDCost × exchangeRate × pricingRatio
```

**Exchange rate**: Read from `system_settings` table (`key = 'usd_exchange_rate'`, stored as integer cents, e.g. `681` = 6.81). Default fallback: `7.20`.

**Pricing ratio** (group multiplier):
- Priority: `api_keys.group_id` → `users.group_id` → default `1.0`
- Read from `user_groups.pricing_ratio`
- `1.0` = standard price, `0.5` = 50% discount, `2.0` = double price

### 4. Balance Deduction Priority

All deductions happen within a single PostgreSQL transaction (`FOR UPDATE` lock on user row).

#### Normal User (`users.enterprise_id IS NULL`)

```
Step 1: Subscription quota (if active)
Step 2: Gift balance      (platform赠送,优先使用)
Step 3: Recharge balance  (用户充值)
Step 4: Balance           (用户总余额,兜底)
```

#### Enterprise User (`users.enterprise_id IS NOT NULL`)

Enterprise users are post-paid; they typically do not have recharge balance.

```
Step 1: Subscription quota (if active)
Step 2: Gift balance       (platform赠送)
Step 3: Enterprise credit  (企业统付额度)
Step 4: Balance            (个人余额兜底)
```

Enterprise credit updates `enterprises.credit_used`. Each deduction type is logged to `balance_transactions` with `balance_type`: `gift` | `recharge` | `balance`.

### 5. Database Tables

| Table | Key Fields | Purpose |
|-------|-----------|---------|
| `service_models` | `api_model_name`, `product_id`, `provider`, `input_price_cache_hit`, `input_price_cache_miss`, `output_price`, `reasoning_price` | Model pricing (USD per 1M tokens) |
| `users` | `balance`, `gift_balance`, `recharge_balance`, `enterprise_id`, `group_id` | User balances |
| `user_groups` | `pricing_ratio`, `billing_mode` | Group-level billing multiplier |
| `enterprises` | `credit_total`, `credit_used` | Enterprise post-paid credit |
| `api_keys` | `group_id`, `key_hash` | API key → group mapping |
| `usage_records` | `total_cost`, `input_tokens`, `output_tokens`, `cached_input_tokens` | Usage history |
| `balance_transactions` | `amount`, `balance_type`, `reference_id` | Audit trail |
| `system_settings` | `usd_exchange_rate` | USD→CNY rate (integer cents) |

---

## Test Prerequisites

1. Portal running on `:3001`
2. PostgreSQL database accessible
3. A test model configured in `service_models` with known pricing
4. A valid API key for the test user

## Test 1: Basic Billing Accuracy

### Setup

Ensure the test model has pricing configured:

```sql
-- Example: test model at $10/1M for all token types
INSERT INTO service_models (provider, product_id, api_model_name, status,
    input_price_cache_hit, input_price_cache_miss, output_price, reasoning_price)
VALUES ('other', 'test', 'test', 'active', 10.0, 10.0, 10.0, 10.0);
```

Get the API key hash:

```sql
SELECT key_hash FROM api_keys WHERE user_id = (
    SELECT id FROM users WHERE email = 'admin@example.com'
);
```

### Run Test

```bash
PORTAL_URL="http://127.0.0.1:3001"
SECRET="<PLACEHOLDER_ENCRYPTION_KEY>"
API_KEY_HASH="<your-api-key-hash>"

# Generate request
TRACE_ID=$(python3 -c "import uuid; print(str(uuid.uuid4()))")
TIMESTAMP=$(date -u +%s)

PAYLOAD=$(cat <<EOF
{
  "traceId": "${TRACE_ID}",
  "apiKeyHash": "${API_KEY_HASH}",
  "model": "test",
  "provider": "openai",
  "inputTokens": 1000000,
  "outputTokens": 500000,
  "cachedTokens": 500000,
  "cacheCreationTokens": 0,
  "reasoningTokens": 0,
  "latencyMs": 500,
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "keyId": "test-key-id",
  "serverId": "test-server",
  "requestCount": 1,
  "statusCode": 200
}
EOF
)

# Sign request: HMAC-SHA256(timestamp + "." + body)
SIGNATURE=$(printf '%s.%s' "$TIMESTAMP" "$PAYLOAD" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $2}')

# Send webhook
curl -s -X POST "${PORTAL_URL}/api/webhook/usage" \
  -H "Content-Type: application/json" \
  -H "X-Timestamp: ${TIMESTAMP}" \
  -H "X-Signature: ${SIGNATURE}" \
  -d "$PAYLOAD"
```

### Expected Result

**Cost calculation** (with USD/CNY rate = 6.81):
```
Cache hit:  500K tokens × $10/1M = $5.00
Cache miss: 500K tokens × $10/1M = $5.00
Output:     500K tokens × $10/1M = $5.00
Total USD:  $15.00
Total CNY:  $15.00 × 6.81 = 102.15 CNY
```

**Balance deduction**:
```
Before balance: 135600 cents (1356.00 CNY)
After balance:  125385 cents (1253.85 CNY)
Diff:           10215 cents = 102.15 CNY ✅
```

### Verification Queries

```sql
-- Check usage record
SELECT trace_id, model, input_tokens, output_tokens, cached_input_tokens, total_cost
FROM usage_records WHERE trace_id = '<trace-id>'::uuid;

-- Check balance
SELECT email, balance, gift_balance, recharge_balance FROM users WHERE email = 'admin@example.com';

-- Check transaction log
SELECT transaction_type, amount, balance_type, balance_before, balance_after
FROM balance_transactions WHERE reference_id = '<trace-id>';
```

---

## Test 2: Group Pricing Ratio

### Setup

Create a group with a pricing ratio (e.g. 0.5 for 50% discount):

```sql
UPDATE user_groups SET pricing_ratio = 0.5 WHERE name = '默认群组';
```

Assign the user's API key to this group:
```sql
UPDATE api_keys SET group_id = (
    SELECT id FROM user_groups WHERE name = '默认群组'
) WHERE key_hash = '<your-api-key-hash>';
```

### Expected Result

With `pricing_ratio = 0.5`, the same request should cost:
```
Base cost: 102.15 CNY
After ratio: 102.15 × 0.5 = 51.075 CNY
Balance deduction: 5108 cents ✅
```

---

## Test 3: Balance Deduction Priority

### Normal User (no enterprise_id)

Priority: `Gift → Recharge → Balance`

```sql
-- Setup test balances
UPDATE users SET gift_balance = 5000, recharge_balance = 3000, balance = 10000
WHERE email = 'admin@example.com';
```

For a 150.00 CNY request:
```
Step 1: Gift      -5000 cents (50.00 CNY)  → remaining 100.00 CNY
Step 2: Recharge  -3000 cents (30.00 CNY)  → remaining 70.00 CNY
Step 3: Balance   -7000 cents (70.00 CNY)  → remaining 0
```

### Enterprise User (has enterprise_id)

Priority: `Gift → Enterprise Credit → Balance`

```sql
-- Setup enterprise
UPDATE users SET enterprise_id = '<enterprise-uuid>', gift_balance = 2000, balance = 5000
WHERE email = 'enterprise-user@example.com';

UPDATE enterprises SET credit_total = 10000, credit_used = 0
WHERE id = '<enterprise-uuid>';
```

For a 80.00 CNY request:
```
Step 1: Gift            -2000 cents (20.00 CNY) → remaining 60.00 CNY
Step 2: Enterprise Credit -6000 cents (60.00 CNY) → remaining 0
 enterprises.credit_used += 60.00
```

---

## Cost Formula Summary

```
Total Cost = (
    cachedTokens/1e6 × inputPriceCacheHit +
    (inputTokens - cachedTokens)/1e6 × inputPriceCacheMiss +
    cacheCreationTokens/1e6 × inputPriceCacheMiss +
    outputTokens/1e6 × outputPrice +
    reasoningTokens/1e6 × reasoningPrice
) × exchangeRate × pricingRatio
```

| Component | Description |
|-----------|-------------|
| `inputPriceCacheHit` | USD per 1M cached (hit) tokens |
| `inputPriceCacheMiss`| USD per 1M input (miss) tokens |
| `outputPrice` | USD per 1M output tokens |
| `reasoningPrice` | USD per 1M reasoning tokens |
| `exchangeRate` | USD → CNY rate (from `system_settings.usd_exchange_rate`) |
| `pricingRatio` | Group discount/multiplier (from `user_groups.pricing_ratio`) |

---

## Troubleshooting

### "Model pricing not found"
- Check `service_models` table for the model
- Provider mismatch: Proxy sends `provider` in webhook, DB may have different value
- Fallback query matches by `api_model_name` or `product_id` only

### "User balance insufficient" with balance not changing
- Check `api_keys` table: `key_hash` must match the webhook's `apiKeyHash`
- If no matching key, `user_id` is empty and no balance deduction occurs

### Sync error: `column "avg_latency_ms" does not exist`
- Fixed: `sync.go` query removed non-existent columns
- Verify `sync.go` uses only existing `service_models` columns
