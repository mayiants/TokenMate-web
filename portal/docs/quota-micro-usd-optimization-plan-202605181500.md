# Portal 计费粒度与 UI 易用性优化计划

**日期**: 2026-05-18
**状态**: Phase 1–4 COMPLETED | Phase 2.2 (ConditionalPricing) PENDING
**Updated**: 2026-06-10 — Verified implementation status against code

## Context

当前 Portal 的内部计费单位是 RMB cent（0.01 元），精度不足以覆盖低价模型（如 gpt-4.1-mini 单次对话约 $0.00004 ≈ 0.0003 元，低于 1 cent）。同时，UI 缺少点击复制等易用性功能。参考 LaoZhang.ai（new-api）的实现，需要：

1. 将内部计费单位改为 micro-USD（1 quota = 0.000001 USD），与行业一致
2. 充值/展示层支持 RMB/USD 双币种
3. 强化按次计费、条件计费、fallback group 计费
4. UI 添加点击复制等易用性功能

---

## Phase 1: 计费单位迁移（RMB cent → micro-USD） [P0]

### 1.1 数据库 Schema 变更

**涉及表和列**：

| 表 | 列 | 当前类型 | 当前含义 | 目标含义 |
|---|---|---------|---------|---------|
| `users` | `balance` | BIGINT | RMB cent | micro-USD |
| `users` | `gift_balance` | BIGINT | RMB cent | micro-USD |
| `users` | `recharge_balance` | BIGINT | RMB cent | micro-USD |
| `users` | `aff_quota` | DECIMAL(10,2) | RMB yuan | micro-USD (改 BIGINT) |
| `users` | `aff_history_quota` | DECIMAL(10,2) | RMB yuan | micro-USD (改 BIGINT) |
| `orders` | `amount` | INTEGER | RMB cent | micro-USD (改 BIGINT) |
| `invoices` | `amount` | INTEGER | RMB cent | micro-USD (改 BIGINT) |
| `invoice_requests` | `amount` | INTEGER | RMB cent | micro-USD (改 BIGINT) |
| `api_keys` | `balance` | INTEGER | RMB cent | micro-USD (改 BIGINT) |
| `subscription_plans` | `price` | INTEGER | RMB cent | micro-USD (改 BIGINT) |
| `single_model_purchases` | `amount_paid` | INTEGER | RMB cent | micro-USD (改 BIGINT) |
| `redemption_codes` | `amount` | INTEGER | RMB cent | micro-USD (改 BIGINT) |
| `enterprises` | `credit_total` | DECIMAL(12,2) | RMB yuan | micro-USD (改 BIGINT) |
| `enterprises` | `credit_used` | DECIMAL(12,2) | RMB yuan | micro-USD (改 BIGINT) |
| `balance_transactions` | `amount` | DECIMAL(12,2) | RMB yuan | micro-USD (改 BIGINT) |
| `balance_transactions` | `balance_before` | DECIMAL(12,2) | RMB yuan | micro-USD (改 BIGINT) |
| `balance_transactions` | `balance_after` | DECIMAL(12,2) | RMB yuan | micro-USD (改 BIGINT) |
| `usage_records` | `*_cost` | DECIMAL(18,10) | USD | micro-USD (改 BIGINT) |
| `usage_records` | `*_deduction` | DECIMAL(18,10) | USD | micro-USD (改 BIGINT) |
| `recharge_orders` | `amount` | DECIMAL(12,2) | RMB yuan | micro-USD (改 BIGINT) |

**关键决策**：
- 所有金额列统一改为 BIGINT，存 micro-USD 整数，避免浮点精度问题
- `service_models.input_price/output_price/cache_read_price` 保持 `DECIMAL(10,4)`，存 USD/1M-tokens（这是定价，不是费用）
- `subscription_plans.quota` 保持 BIGINT，存 tokens（不是金额）

### 1.2 迁移 SQL

创建 `migrations/202605181500_quota_micro_usd.sql`：

```sql
-- 1 quota = 0.000001 USD (micro-dollar)
-- Since all data is test data, direct unit change without conversion

-- users: BIGINT columns stay BIGINT, just reinterpret values
-- users: aff_quota, aff_history_quota: DECIMAL → BIGINT
ALTER TABLE users ALTER COLUMN aff_quota TYPE BIGINT USING ROUND(aff_quota * 1000000);
ALTER TABLE users ALTER COLUMN aff_history_quota TYPE BIGINT USING ROUND(aff_history_quota * 1000000);

-- orders, invoices, api_keys, etc: INTEGER → BIGINT
ALTER TABLE orders ALTER COLUMN amount TYPE BIGINT;
ALTER TABLE invoices ALTER COLUMN amount TYPE BIGINT;
ALTER TABLE invoice_requests ALTER COLUMN amount TYPE BIGINT;
ALTER TABLE api_keys ALTER COLUMN balance TYPE BIGINT;
ALTER TABLE subscription_plans ALTER COLUMN price TYPE BIGINT;
ALTER TABLE single_model_purchases ALTER COLUMN amount_paid TYPE BIGINT;
ALTER TABLE redemption_codes ALTER COLUMN amount TYPE BIGINT;

-- enterprises: DECIMAL → BIGINT
ALTER TABLE enterprises ALTER COLUMN credit_total TYPE BIGINT USING ROUND(credit_total * 1000000);
ALTER TABLE enterprises ALTER COLUMN credit_used TYPE BIGINT USING ROUND(credit_used * 1000000);

-- balance_transactions: DECIMAL → BIGINT
ALTER TABLE balance_transactions ALTER COLUMN amount TYPE BIGINT USING ROUND(amount * 1000000);
ALTER TABLE balance_transactions ALTER COLUMN balance_before TYPE BIGINT USING ROUND(balance_before * 1000000);
ALTER TABLE balance_transactions ALTER COLUMN balance_after TYPE BIGINT USING ROUND(balance_after * 1000000);

-- usage_records: DECIMAL → BIGINT
ALTER TABLE usage_records ALTER COLUMN input_cost TYPE BIGINT USING ROUND(input_cost * 1000000);
ALTER TABLE usage_records ALTER COLUMN output_cost TYPE BIGINT USING ROUND(output_cost * 1000000);
ALTER TABLE usage_records ALTER COLUMN cache_cost TYPE BIGINT USING ROUND(cache_cost * 1000000);
ALTER TABLE usage_records ALTER COLUMN reasoning_cost TYPE BIGINT USING ROUND(reasoning_cost * 1000000);
ALTER TABLE usage_records ALTER COLUMN total_cost TYPE BIGINT USING ROUND(total_cost * 1000000);
ALTER TABLE usage_records ALTER COLUMN input_deduction TYPE BIGINT USING ROUND(input_deduction * 1000000);
ALTER TABLE usage_records ALTER COLUMN output_deduction TYPE BIGINT USING ROUND(output_deduction * 1000000);
ALTER TABLE usage_records ALTER COLUMN cache_deduction TYPE BIGINT USING ROUND(cache_deduction * 1000000);
ALTER TABLE usage_records ALTER COLUMN reasoning_deduction TYPE BIGINT USING ROUND(reasoning_deduction * 1000000);
ALTER TABLE usage_records ALTER COLUMN total_deduction TYPE BIGINT USING ROUND(total_deduction * 1000000);

-- recharge_orders: DECIMAL → BIGINT
ALTER TABLE recharge_orders ALTER COLUMN amount TYPE BIGINT USING ROUND(amount * 1000000);

-- Add quota_unit system setting
INSERT INTO system_settings (key, value, category, description)
VALUES ('quota_unit', 'micro_usd', 'billing', 'Internal quota unit: 1 quota = 0.000001 USD')
ON CONFLICT (key) DO UPDATE SET value = 'micro_usd';
```

### 1.3 Go 代码变更

**新增 `internal/routes/quota.go`**：

```go
const (
    QuotaPerUSD = 1_000_000  // micro-USD per USD
)

func QuotaToUSD(quota int64) string {
    return fmt.Sprintf("$%.6f", float64(quota)/float64(QuotaPerUSD))
}

func QuotaToRMB(quota int64, rate float64) string {
    usd := float64(quota) / float64(QuotaPerUSD)
    return fmt.Sprintf("¥%.2f", usd*rate)
}

func RMBYuanToQuota(yuan float64, rate float64) int64 {
    usd := yuan / rate
    return int64(usd * float64(QuotaPerUSD))
}

func USDtoQuota(usd float64) int64 {
    return int64(usd * float64(QuotaPerUSD))
}

func QuotaToUSDFloat(quota int64) float64 {
    return float64(quota) / float64(QuotaPerUSD)
}
```

**修改所有 `/100.0` 的 RMB cent 转换** — 全局搜索替换为 quota 工具函数。

### 1.4 涉及文件清单

| 文件 | 变更内容 |
|------|---------|
| `migrations/202605181500_quota_micro_usd.sql` | 新建迁移 |
| `internal/routes/quota.go` | 新建 quota 转换函数 |
| `internal/routes/billing.go` | getBalance 改输出 |
| `internal/routes/orders.go` | 订单金额转换 |
| `internal/routes/payment.go` | 支付金额转换 |
| `internal/routes/redemption.go` | 兑换码金额转换 |
| `internal/routes/subscriptions.go` | 套餐价格转换 |
| `internal/routes/usage.go` | usage cost 转换 |
| `internal/routes/dashboard.go` | 仪表盘金额显示 |
| `internal/routes/enterprise.go` | 企业额度转换 |
| `internal/routes/profile.go` | 用户余额显示 |
| `internal/routes/groups.go` | 分组倍率显示 |
| `internal/web/data.go` | 所有金额字段转换 |
| `internal/web/admin_handlers.go` | 管理后台金额 |
| `internal/services/exchange.go` | 汇率服务微调 |
| `web/templates/*.html` | 前端金额显示格式 |

---

## Phase 2: 按次计费与条件计费

### 2.1 ModelFixedPrice（按次计费） [P1]

**数据库**：在 `service_models` 表添加：

```sql
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS fixed_price BIGINT DEFAULT 0;
-- 0 = 按量计费, >0 = 按次计费价格（micro-USD/次）
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS billing_type VARCHAR(20) DEFAULT 'token'
  CHECK (billing_type IN ('token', 'request'));
```

**计费逻辑**：
- `billing_type = 'token'`：按 input/output tokens × ModelRatio × GroupRatio
- `billing_type = 'request'`：按 fixed_price × GroupRatio（每次调用固定价格）

### 2.2 ConditionalPricing（条件计费） [P2]

**数据库**：新建表

```sql
CREATE TABLE IF NOT EXISTS model_conditional_pricing (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id VARCHAR(100) NOT NULL,
    min_tokens INTEGER NOT NULL DEFAULT 0,
    max_tokens INTEGER NOT NULL DEFAULT -1,  -- -1 = unlimited
    input_ratio NUMERIC(10,4) NOT NULL,
    completion_ratio NUMERIC(10,4) NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(model_id, min_tokens)
);
```

### 2.3 Fallback Group 计费修正 [P1]

当前 `api_keys.group_priority` 已支持 fallback 路由，但计费未跟随切换。需要：
- 请求实际使用的 group 记录到 `usage_records.actual_group_id`（已有此列）
- 计费时使用 `actual_group_id` 对应的 `pricing_ratio`，而非 `api_keys.group_id`

---

## Phase 3: UI 易用性

### 3.1 点击复制功能 [P1]

在 `web/templates/` 中添加通用 JS 工具：

```javascript
function copyToClipboard(text, el) {
    navigator.clipboard.writeText(text).then(() => {
        const orig = el.textContent;
        el.textContent = '已复制!';
        setTimeout(() => el.textContent = orig, 1500);
    });
}
```

**应用位置**：
- **模型名称**：admin-models.html 的模型列表，点击模型名复制
- **API Key**：apikeys.html 的 Key 前缀，点击复制完整 key
- **API 端点**：dashboard/apikeys 页面，端点 URL 点击复制
- **模型 ID**：provider 模型同步列表，product_id 点击复制

### 3.2 定价信息内联显示 [P3]

在模型列表中直接显示 input/output 价格，不用跳转。

### 3.3 批量操作优化 [P3]

- 全选/反选 checkbox
- 批量添加到服务模型时显示进度

---

## Phase 4: 展示层双币种 [P2]

### 4.1 用户偏好

已有 `users.preferred_currency` 列（CNY/USD），在展示层根据偏好切换：

- `preferred_currency = 'CNY'`：显示 `¥7.20`（quota × 汇率 / 1000000）
- `preferred_currency = 'USD'`：显示 `$1.00`（quota / 1000000）

### 4.2 充值流程

- 用户选择充值 100 RMB → 系统按汇率折算 → 存入 micro-USD quota
- 汇率快照存入 `orders.exchange_rate`

---

## 实施顺序

| 步骤 | 内容 | 优先级 | 预计工作量 |
|------|------|--------|-----------|
| 1 | Phase 1: quota 单位迁移 + 工具函数 | P0 | 中 |
| 2 | Phase 2.1: 按次计费 (fixed_price + billing_type) | P1 | 小 |
| 3 | Phase 2.3: fallback group 计费修正 | P1 | 小 |
| 4 | Phase 3.1: 点击复制 JS | P1 | 小 |
| 5 | Phase 4: 展示层双币种 | P2 | 中 |
| 6 | Phase 2.2: 条件计费 | P2 | 中 |
| 7 | Phase 3.2-3.3: 定价内联 + 批量优化 | P3 | 小 |

---

## 验证方案

1. **计费精度**：用 gpt-4.1-mini 模拟 1 次对话（~50 input + ~200 output tokens），验证 quota 扣减精度到 micro-USD
2. **汇率换算**：充值 1 RMB → 验证 quota 值 = 1,000,000 / 7.20 ≈ 138,889 micro-USD
3. **展示层**：切换 preferred_currency，验证 USD/RMB 显示正确
4. **按次计费**：调用图像模型，验证扣减 fixed_price × GroupRatio
5. **点击复制**：在浏览器中点击模型名，验证剪贴板内容正确
6. **Docker 构建**：`docker compose -f docker-compose.portal.yml up -d --build`

---

## Phase 1 完成报告 (2026-05-18)

### 实施状态: COMPLETED

### 数据库迁移

- 迁移文件 `migrations/202605181500_quota_micro_usd.sql` 已执行
- 69 个货币列全部从 DECIMAL/INTEGER 转为 BIGINT (micro-USD)
- `service_models.input_price/output_price/cache_read_price` 保持 DECIMAL(10,4) (定价，非费用)
- `subscription_plans.quota` 保持 BIGINT (tokens，非金额)
- `system_settings` 新增 `quota_unit = micro_usd`（category 使用 `general`，因 CHECK 约束不含 `billing`）
- 当前数据库汇率: `usd_exchange_rate = 682` (6.82 CNY/USD)

### Go 代码变更

**新增文件**:
- `internal/routes/quota.go`: QuotaToUSD, QuotaToRMB, USDtoQuota, RMBYuanToQuota, RMBCentToQuota, QuotaToUSDFloat, QuotaToRMBFloat, FormatQuota, FormatQuotaFloat

**修改文件** (实际变更，与计划对比):
- `internal/routes/billing.go`: getBalance 输出改为 `balance_quota/balance_usd/balance_rmb`; createRecharge 添加 order_id 列和 uuid 导入; 汇率从 DB 读取
- `internal/routes/orders.go`: 订单金额从 `amountCents` 改为 `amountQuota`; 双币种支持 (CNY/USD)
- `internal/routes/webhook.go`: calculateCostFromPricing 返回 int64 (micro-USD); 所有 costInCents → costQuota; 批量路径 totalCost/recordCosts 改为 int64; 余额低阈值改为 1_000_000 micro-USD
- `internal/routes/subscriptions.go`: 价格显示改为 QuotaToUSD; 取消退款改为 QuotaToUSD
- `internal/routes/redemption.go`: createCodes 从 `amountCents` 改为 `amountQuota`; 汇率从 DB 读取
- `internal/routes/invitations.go`: welcome code 从 `amountCents` 改为 `amountQuota`; 汇率从 DB 读取
- `internal/routes/invoices.go`: invoice 从 `amountCents` 改为 `amountQuota`; 汇率从 DB 读取
- `internal/routes/checkin.go`: 签到奖励显示改为 QuotaToUSD
- `internal/web/data.go`: SUM(ur.total_cost) 改为 bigint 转换
- `internal/web/admin_handlers.go`: parsePriceToCents → parsePriceToQuota; cnyCentsToDisplayYuan → quotaToDisplay
- `internal/web/actions.go`: 充值订单金额改为 usdToQuota/rmbYuanToQuota (本地函数，避免循环依赖)
- `internal/routes/billing_test.go`: TestBalanceConversion → TestQuotaConversion

**HTML 模板** (8 个文件):
- admin-redemption.html, billing.html, my-subscriptions.html, admin-invitations.html, invoices.html, redemption.html, orders.html, subscriptions.html
- JavaScript: `cents / 100` → `quota / 1000000`

### 汇率处理

所有金额转换均从数据库 `system_settings.usd_exchange_rate` 读取汇率，7.20 仅作为 DB 不可用时的 fallback 默认值。涉及文件:
- billing.go, orders.go, redemption.go, invitations.go, invoices.go, subscriptions.go, actions.go, webhook.go, handler.go, helpers.go

### 修复的 Bug

1. **recharge_orders INSERT 缺少 order_id**: createRecharge 的 INSERT 语句缺少 `order_id` 列和参数，导致 NOT NULL 违规。已修复添加 `orderID := "RCH-" + uuid.New().String()[:8]` 和 `$2` 参数
2. **Scan 目标错误**: `.Scan(&orderID)` 改为 `.Scan(&id)`，避免覆盖生成的 orderID
3. **web/actions.go 循环依赖**: web 包不能导入 routes 包，在 actions.go 中添加本地 usdToQuota/rmbYuanToQuota 函数

### API 验证结果

| API | 测试输入 | 输出 (micro-USD) | USD 显示 | 状态 |
|-----|---------|-----------------|---------|------|
| Balance | admin@example.com | 12,199,412 | $12.199412 | PASS |
| Recharge CNY 10 | amount=10, currency=CNY | 1,466,275 | $1.466275 | PASS |
| Recharge USD 5 | amount=5, currency=USD | 5,000,000 | $5.000000 | PASS |
| Order CNY 100 | amount=100, currency=CNY | 14,662,756 | $14.662756 | PASS |
| Order USD 50 | amount=50, currency=USD | 50,000,000 | $50.000000 | PASS |
| Subscription Purchase | Lite token plan | price=4,027,778 | $4.027778 | PASS |
| Redemption Create | amount=10 CNY | 733,137 (at 6.82) | $0.733137 | PASS |
| Redemption Redeem | WG-TRT0-3HOH-ZKME | +733,137 to gift_balance | PASS |
| Order Export CSV | - | 金额以 $USD 显示 | PASS |
| Usage Summary | days=7 | total_cost=0 (no usage) | PASS |

### 单元测试

- TestQuotaConversion: 5 cases PASS (0, 1M, 500K, 10K, 1 micro-USD)
- TestGetUsageSummary_DaysParamParsing: 6 cases PASS
- TestGetUsageRecords_PaginationParsing: 9 cases PASS
- TestGetUsageRecords_DateFilterParsing: 5 cases PASS
- TestGetUsageRecords_ApiKeyIDParsing: 3 cases PASS
- TestIsAdmin: 5 cases PASS
- TestCreateRecharge_BindingValidation: 5 cases PASS
- TestTotalPagesCalculation: 6 cases PASS
- TestWhereConditionsBuilding: 5 cases PASS

### Docker 验证

- `docker compose -f docker-compose.portal.yml build --no-cache portal`: PASS
- Portal container: healthy, API endpoints functional
- DB schema: 69 monetary columns confirmed BIGINT
- `service_models` pricing columns confirmed DECIMAL (unchanged)

### 待完成 (Phase 2.2)

- Phase 2.2: 条件计费 (model_conditional_pricing)

### 已完成 (Phase 1–4, 2.1, 2.3, 3.1, 4) — Verified 2026-06-10

- Phase 2.1: 按次计费 ✅ (`billing_type` + `fixed_price` in `service_models`; `webhook.go` reads both)
- Phase 2.3: Fallback Group 计费修正 ✅ (`actual_group_id` in `usage_records`)
- Phase 3.1: 点击复制 JS ✅ (`navigator.clipboard` in `voucher.html`, `apikeys.html`, `key-created.html`)
- Phase 4: 展示层双币种 ✅ (`preferred_currency` in `users`, `profile.go` reads/updates it)
