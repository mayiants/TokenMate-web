# 计费币种一致性审计与修复计划

> 日期：2026-05-18  
> 状态：Phase 1 (micro-USD 迁移) 审计完成，待修复  
> 背景：存储单位已从 CNY 0.01 分迁移到 USD 0.000001 (micro-USD)。Admin 页面全部统一 USD 计价；User 页面根据 `preferred_currency` 双币种显示/输入。当前处于测试阶段，数据可清空，无需兼容历史数据。

---

## 一、核心原则（重申）

| 场景 | 输入 | 存储 | 显示 |
|------|------|------|------|
| **Admin 页面** | USD 元（如 5.00） | micro-USD（5,000,000） | USD（$5.00） |
| **User 页面（CNY）** | CNY 元（如 36.00） | micro-USD（按汇率换算） | CNY（¥36.00） |
| **User 页面（USD）** | USD 元（如 5.00） | micro-USD（5,000,000） | USD（$5.00） |

**关键决策：**
1. **套餐定价**：必须以 USD 计价存储，不受汇率波动影响（否则每天汇率变，套餐 USD 金额跟着变）。
2. **Welcome 码 / Invite 码**：创建后币种固定，UI 限制显示（admin 统一 USD，user 按偏好）。
3. **Admin 金额显示**：精确到 6 位小数（$5.000000）可接受，admin 需要看到 micro-USD 精度。
4. **测试阶段**：所有数据可删除/清空，无需兼容历史数据。

---

## 二、已确认修改正确（与代码一致）

| 文件 | 修改内容 | 状态 |
|------|---------|------|
| `admin-invitations.html` | 元 → $, USD → $, 0.69USD → $0.69, reward_amount+'元' → reward_amount | ✅ |
| `admin-redemption.html` | 金额 (USD/元) → 金额 ($), 0.69USD → $0.69 | ✅ |
| `admin-subscriptions.html` | 价格 (USD/元) → 价格 ($)（input label） | ✅ |
| `partials/plan-detail.html` | 价格 (USD/元) → 价格 ($) | ✅ |
| `orders.html` | 金额 (USD/元) → 金额 ($)（admin edit modal） | ✅ |
| `invitation.html` | reward_amount+'元' → reward_amount, 已转入 USD → 已转入 $ | ✅ |
| `invitations.go` | getWelcomeCode int→int64+amount_display, createOrUpdateWelcomeCode RMBYuanToQuota→USDtoQuota | ✅ |

---

## 三、🔴 CRITICAL — 会导致金额计算错误

### 3.1 `invitations.go: generateAffCode` — 邀请奖励金额放大 ~100万×汇率倍

**位置**：`internal/routes/invitations.go:80-108`

**问题**：`admin-invitations.html` 的 `saveAllSettings` 已经把 `invitation_inviter_reward` / `invitation_invitee_reward` 存为 **micro-USD**（`parseFloat(value) * 1000000`）。但 `generateAffCode` 仍把它们当 **RMB yuan** 处理：

```go
inviterRewardQuota := RMBYuanToQuota(inviterReward, exchangeRate)
```

如果 DB 存的是 `5000000`（$5），`fmt.Sscanf("5000000", "%f")` 读到 `5000000.0`，再 `RMBYuanToQuota(5000000, 7.20)` = **694,444,444,444 micro-USD**。

**修复**：直接读取为 `int64` micro-USD，不再做汇率转换。默认值从 `'5'` 改为 `'5000000'`。

```go
var inviterRewardQuota, inviteeRewardQuota int64
var s string
_ = r.db.QueryRow(...).Scan(&s)
fmt.Sscanf(s, "%d", &inviterRewardQuota)
if inviterRewardQuota == 0 {
    inviterRewardQuota = 5_000_000 // $5 default
}
```

---

### 3.2 `invitations.go: transferQuota` — 硬编码 RMB 转换

**位置**：`internal/routes/invitations.go:266`

```go
transferQuota := RMBYuanToQuota(req.Amount, 7.20)
```

User 前端输入 USD 金额（页面无币种切换），后端当成 RMB 元转。输入 $10 → 只转 ~1.39M micro-USD（应为 10M）。

**修复**：查询用户 `preferred_currency`，USD 时用 `USDtoQuota`，CNY 时用 `RMBYuanToQuota`。

---

### 3.3 `billing-details.html: formatAmount()` — 缺少 `/1000000`

**位置**：`web/templates/billing-details.html:342`

```js
function formatAmount(yuan) {
  var val = parseFloat(yuan);
  if (window.userCurrency === 'USD') {
    return (val / window.exchangeRate).toFixed(5);  // ❌ 没除 1000000
  }
  return val.toFixed(5);  // ❌ 没除 1000000
}
```

API 返回 micro-USD，但这里只做了汇率转换。**数值会大 100万倍**。

**修复**：
```js
function formatAmount(quota) {
  var val = parseFloat(quota) / 1000000;
  if (window.userCurrency === 'USD') {
    return '$' + val.toFixed(5);
  }
  return '¥' + (val * window.exchangeRate).toFixed(2);
}
```

---

### 3.4 `admin_handlers.go: parsePriceToQuota / quotaToDisplay` — Admin 仍按个人币种分支

**位置**：`internal/web/admin_handlers.go:1022-1037`

```go
func parsePriceToQuota(priceStr string, currency string, exchangeRate float64) int64 {
    priceFloat := parsePriceToFloat(priceStr)
    if currency == "CNY" && exchangeRate > 0 {
        return routes.RMBYuanToQuota(priceFloat, exchangeRate)
    }
    return routes.USDtoQuota(priceFloat)
}
```

Admin 页面标签已全改为 `$`，但如果 admin 的 cookie/DB `preferred_currency = CNY`，后端会把 `$` 标签下的输入当成 **CNY 元** 处理 → `RMBYuanToQuota` 转换，导致静默存储错误金额。

**影响范围**：
- `HandleCreatePlan` (line 506)
- `HandleUpdatePlan` (line 619)
- `HandleCreateRedemptionCode` (line 790)
- `HandleBatchCreateRedemptionCodes` (line 861)
- `HandleGetPlanDetail` (line 588)

**修复**：Admin 价格处理统一为 USD，不再读取 admin 的 `preferred_currency`。

```go
// Admin 统一 USD
func parseAdminPriceToQuota(priceStr string) int64 {
    return routes.USDtoQuota(parsePriceToFloat(priceStr))
}
func adminQuotaToDisplay(quota int64) float64 {
    return routes.QuotaToUSDFloat(quota)
}
```

---

### 3.5 `invoices.go: createRequest` — 硬编码 RMB

**位置**：`internal/routes/invoices.go:373`

```go
amountQuota := RMBYuanToQuota(req.Amount, exchangeRate)
```

不检查用户 `preferred_currency`，USD 用户输入 $100 会被当成 ¥100 处理。

**修复**：查询用户 `preferred_currency`，按币种选择转换函数。

---

## 四、🟠 HIGH — 数据/显示不一致

### 4.1 `orders.go: updateOrder` — 直接存储原始值

**位置**：`internal/routes/orders.go:368`

```go
args = append(args, req.Amount)  // 没有转换到 micro-USD
```

用户发 `amount: 100`，直接存为 100 micro-USD（$0.0001）。

**修复**：根据用户 `preferred_currency` 转换到 micro-USD。

---

### 4.2 `subscriptions.go: cancelSubscription` — 退款订单币种错误

**位置**：`internal/routes/subscriptions.go:660`

```go
VALUES ($1::uuid, $2, $3, 'CNY', 1, 'completed', 'subscription_refund', ...)
```

硬编码 `currency='CNY', exchange_rate=1`，但 `refundCents`（变量名也误导）实际是 **micro-USD**。

**修复**：存储正确的 `currency` 和 `exchange_rate`（或统一用 USD）。

---

### 4.3 `billing.go: createRecharge` — 默认 CNY

**位置**：`internal/routes/billing.go:410`

```go
if req.Currency == "" {
    req.Currency = "CNY"
}
```

未检查用户 `preferred_currency` 作为默认值。

**修复**：从 DB 读取用户 `preferred_currency` 作为默认值。

---

### 4.4 `admin_handlers.go: HandleDeletePlan` — audit log 字段名错误

**位置**：`internal/web/admin_handlers.go:672`

```go
"price_cny_cents":%d
```

值是 micro-USD，字段名却写 `cny_cents`。

**修复**：改为 `"price_micro_usd":%d`。

---

## 五、🟡 MEDIUM — 显示不一致

### 5.1 `admin-subscriptions.html` tier labels

**位置**：`web/templates/admin-subscriptions.html:44-54`

```html
{{if eq .Currency "USD"}}$39{{else}}¥39{{end}}
```

Admin 应统一显示 `$39`。

**修复**：去掉 `{{if}}`，固定 `$39`。

---

### 5.2 `admin-subscriptions.html` plan list

**位置**：`web/templates/admin-subscriptions.html:103`

```html
{{formatCurrency .Price $.Currency $.ExchangeRate}}
```

Admin 列表应固定显示 USD。

**修复**：`{{formatCurrency .Price "USD" 1.0}}`

---

### 5.3 `admin-users.html` balance

**位置**：`web/templates/admin-users.html:47`

```html
{{formatMoney .Balance $.Currency $.ExchangeRate}}
```

Admin 看用户余额应统一显示 USD。

**修复**：`{{formatMoney .Balance "USD" 1.0}}`

---

### 5.4 CSV export header 与数据不符

| 文件 | 位置 | Header | 数据 |
|------|------|--------|------|
| `orders.go` | line 167 | `金额(元)` | `QuotaToUSD()` → USD |
| `billing.go` | line 684 | `消费（￥）` | `QuotaToUSD()` → USD |
| `billing.go` | line 716 | `消费（￥）` | `QuotaToUSD()` → USD |

**修复**：Header 统一改为 `金额(USD)` / `消费(USD)`。

---

### 5.5 `invitation.html` transfer success — 双 `$`

**位置**：`web/templates/invitation.html:206`

```js
showMessage('成功', '已转入 $' + data.amount);
```

后端 `transferQuota` 返回 `data.amount = QuotaToUSD(...)` = `"$10.000000"`（已含 $），前端再加 `$` → **显示 `$$10.000000`**。

**修复**：去掉多余的 `$`，改为 `'已转入 ' + data.amount`。

---

## 六、修复文件清单与顺序

| # | 文件 | 优先级 | 修复内容 |
|---|------|--------|---------|
| 1 | `invitations.go` | 🔴 | `generateAffCode`: 直接读 micro-USD；`transferQuota`: 查用户币种 |
| 2 | `billing-details.html` | 🔴 | `formatAmount()`: 增加 `/1000000` |
| 3 | `admin_handlers.go` | 🔴 | `parsePriceToQuota`/`quotaToDisplay`: 去 CNY 分支；所有 admin price handler 统一 USD |
| 4 | `invoices.go` | 🔴 | `createRequest`: 查用户 preferred_currency |
| 5 | `orders.go` | 🟠 | `updateOrder`: 按用户币种转换到 micro-USD；CSV header 修正 |
| 6 | `subscriptions.go` | 🟠 | `cancelSubscription`: 退款订单存正确 currency/exchange_rate |
| 7 | `billing.go` | 🟠 | `createRecharge`: 默认 currency 从用户 preferred_currency 读取；CSV header 修正 |
| 8 | `admin_handlers.go` | 🟠 | `HandleDeletePlan`: audit log 字段名改 `price_micro_usd` |
| 9 | `admin-subscriptions.html` | 🟡 | tier labels 固定 `$`；plan list 固定 USD |
| 10 | `admin-users.html` | 🟡 | balance 固定 USD |
| 11 | `invitation.html` | 🟡 | 去掉多余的 `$` prefix |
| 12 | `redemption.go` | 🟡 | `createCodes`: admin API 统一 USD（去掉 CNY 分支） |

---

## 七、Admin 金额显示精度说明

当前 `QuotaToUSD()` 返回 6 位小数（`$5.000000`）。对于 admin 页面：

- **套餐列表**、**兑换码列表**、**邀请码列表**、**用户余额** 等显示 `$101824.633431` 这种长数字。
- **建议**：admin 保持 6 位小数精度，因为 admin 需要确认 micro-USD 存储值与输入值一致。可在未来加 `QuotaToUSDShort()` 返回 2 位小数用于展示，但 admin 后台保留 6 位。
- **当前实现**：继续使用 `QuotaToUSD()` / `QuotaToUSDFloat()` 的 6 位精度，不做修改。
