# Stripe API 升级与优化方案

## 背景

当前项目使用直接 HTTP 调用 Stripe `/v1/checkout/sessions` API，未锁定 API 版本，且存在若干与最新 Stripe Dahlia 版本（2026-03-25.dahlia）不兼容的风险。

本文档基于 2026-06-23 对 Stripe 官方文档的审查，列出需要修改的问题和具体方案。

---

## 涉及文件

- `pkg/payment/stripe.go` — Stripe 网关实现
- `pkg/payment/gateway.go` — 配置结构体

---

## 修改项

### 1. 添加 Stripe-Version 头（高优先级）

**问题**：当前请求未携带 `Stripe-Version` 头，Stripe 使用账户默认版本。一旦账户被升级到 Dahlia，旧参数可能立即失效。

**方案**：

在 `StripeConfig` 中增加 `APIVersion` 字段，请求时携带 `Stripe-Version` 头：

```go
type StripeConfig struct {
    // ...existing fields...
    APIVersion string `json:"api_version"` // 如 "2026-03-25.dahlia"
}
```

```go
if g.config.APIVersion != "" {
    req.Header.Set("Stripe-Version", g.config.APIVersion)
}
```

---

### 2. 移除 payment_method_types[] 硬编码（中优先级）

**问题**：代码中 `params.Set("payment_method_types[]", "card")` 硬编码仅支持卡支付。从 API 版本 2023-08-16 起，`automatic_payment_methods` 已成为默认行为，Stripe 会根据 Dashboard 配置自动展示所有可用支付方式（卡、Apple Pay、Google Pay、Link 等）。

**方案**：删除 `payment_method_types[]` 参数行。

```diff
-params.Set("payment_method_types[]", "card")
```

这将自动启用 Dashboard 中配置的所有支付方式，提高支付转化率。

---

### 3. Webhook 签名验证增加时间戳容差（中优先级）

**问题**：当前 `VerifySignature` 仅验证 HMAC 正确性，未检查 webhook 签名中的时间戳是否过期。攻击者可重放旧的合法 webhook 请求。

**方案**：拒绝时间戳超过 5 分钟的 webhook：

```go
ts, err := strconv.ParseInt(timestamp, 10, 64)
if err != nil {
    return false
}
if time.Now().Unix()-ts > 300 {
    return false
}
```

---

### 4. 添加 integration_identifier（低优先级）

**背景**：Dahlia 版新增 `integration_identifier` 参数，用于在 Stripe Dashboard 中分组追踪 Checkout Sessions。

**方案**：

```go
params.Set("integration_identifier", "ai-portal-go")
```

---

### 5. 注意 ui_mode 枚举变更（信息记录）

Dahlia 版对 Checkout Session 的 `ui_mode` 枚举值做了 breaking change：

| 旧值 | 新值 |
|------|------|
| `hosted` | `hosted_page` |
| `embedded` | `embedded_page` |
| `custom` | `elements` |

当前代码未显式传 `ui_mode`（默认 `hosted_page`），暂不受影响。如未来需要嵌入式 Checkout，须使用新枚举值。

---

## 实施计划

| 步骤 | 内容 | 风险 |
|------|------|------|
| 1 | 添加 `Stripe-Version` 头，配置设为 `"2026-03-25.dahlia"` | 低（锁定版本后行为可预测） |
| 2 | 移除 `payment_method_types[]` 硬编码 | 低（需确认 Dashboard 已启用所需支付方式） |
| 3 | Webhook 增加时间戳容差检查 | 无 |
| 4 | 添加 `integration_identifier` | 无 |
| 5 | 测试环境验证全部改动 | — |

---

## 参考

- [Stripe Dahlia Changelog](https://docs.stripe.com/changelog/dahlia)
- [Checkout Session UI Mode 变更](https://docs.stripe.com/changelog/dahlia/2026-03-25/updates-available-checkout-session-ui-modes)
- [Automatic Payment Methods](https://docs.stripe.com/changelog/2023-08-16/automatic-payment-methods)
- [Stripe API 升级指南](https://docs.stripe.com/upgrades)

---

## 6. 建议集成 Stripe 官方 Go SDK

### 现状

当前项目未使用任何 Stripe 依赖，完全通过 `net/http` 手动调用 REST API 并解析 JSON。

### 官方 SDK

Stripe 维护了官方 Go SDK：[github.com/stripe/stripe-go](https://github.com/stripe/stripe-go)

- 最新版本：**v86.0.0**（2026-06-06 发布）
- 包路径：`github.com/stripe/stripe-go/v86`
- 要求 Go 1.22+
- MIT 协议

### 迁移收益

| 自行实现 | 官方 SDK |
|---------|---------|
| 手动管理 API 版本头 | SDK 内部自动锁定对应 API 版本 |
| 手动构建 form 参数、解析 JSON | 强类型结构体，编译期发现错误 |
| 自己实现 webhook HMAC + timestamp | `webhook.ConstructEvent()` 一行完成 |
| 无重试机制 | 内置幂等自动重试（连接错误、409等） |
| API 变更需手动跟代码 | 升级 SDK 版本号即可 |
| ~150 行实现 | ~30 行实现 |

### 集成示例

```bash
go get github.com/stripe/stripe-go/v86
```

创建 Checkout Session：

```go
import (
    "github.com/stripe/stripe-go/v86"
    "github.com/stripe/stripe-go/v86/checkout/session"
)

sc := stripe.NewClient(secretKey)

params := &stripe.CheckoutSessionCreateParams{
    Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
    SuccessURL: stripe.String(successURL + "?order_id=" + order.OrderID),
    CancelURL:  stripe.String(cancelURL + "?order_id=" + order.OrderID),
    LineItems: []*stripe.CheckoutSessionLineItemParams{{
        PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
            Currency:   stripe.String(order.Currency),
            UnitAmount: stripe.Int64(order.Amount),
            ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
                Name: stripe.String(order.ProductName),
            },
        },
        Quantity: stripe.Int64(1),
    }},
}
params.AddMetadata("order_id", order.OrderID)
params.AddMetadata("user_id", order.UserID)

s, err := sc.V1CheckoutSessions.Create(ctx, params)
// s.URL 即支付页面链接
```

Webhook 验证：

```go
import "github.com/stripe/stripe-go/v86/webhook"

event, err := webhook.ConstructEvent(payload, signatureHeader, webhookSecret)
// 自动完成 HMAC 校验 + 时间戳容差检查（默认 5 分钟）
```

### 建议

**推荐集成官方 SDK**。理由：

1. 消除当前所有已知问题（版本锁定、payment_method_types 硬编码、webhook 时间戳校验缺失）
2. 后续 Stripe API 升级只需 bump 依赖版本，无需改业务代码
3. 官方 SDK 由 Stripe API spec 自动生成，覆盖全部 API 端点，类型安全
4. 内置 mock 支持，便于单元测试
