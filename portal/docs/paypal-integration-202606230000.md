# PayPal 支付网关集成说明

## 背景

PayPal 没有官方维护的 Go SDK。我们选用社区最成熟的 SDK：

- **库**：[github.com/plutov/paypal/v4](https://github.com/plutov/paypal)
- **版本**：v4.17.0（2025-10 发布）
- **Stars**：775+，活跃维护
- **API 规范**：直接引用 [PayPal 官方 REST API 规范](https://github.com/paypal/paypal-rest-api-specifications) 作为 git submodule
- **覆盖**：Orders v2、Payments、Subscriptions、Webhooks、Payouts 等

PayPal 官方（github.com/paypal）仅提供 JavaScript（前端 JS SDK）、Ruby、Android、iOS SDK，**Go 没有官方 SDK**。`plutov/paypal` 是 Go 生态下的事实标准。

---

## 集成方式

### 安装

```bash
go get github.com/plutov/paypal/v4@v4.17.0
```

### 配置结构

```go
type PayPalConfig struct {
    ClientID  string `json:"client_id"`   // PayPal App Client ID
    Secret    string `json:"secret"`      // PayPal App Secret
    Mode      string `json:"mode"`        // "sandbox" 或 "live"
    ReturnURL string `json:"return_url"`  // 支付成功返回URL
    CancelURL string `json:"cancel_url"`  // 支付取消返回URL
}
```

对应 UI 管理后台 `super/payment.html` 中的 PayPal 配置表单。

### 支付流程

PayPal Checkout 使用 **redirect-based** 流程（不同于 Stripe 的 hosted page 直接返回 URL）：

```
1. 服务端调用 CreateOrder → 获得 approval link
2. 用户重定向到 PayPal 完成支付
3. PayPal 重定向回 return_url?order_id=xxx&status=success&gateway=paypal
4. 服务端调用 CaptureOrder 完成扣款
```

### 核心代码

创建订单：
```go
c, _ := paypal.NewClient(clientID, secret, paypal.APIBaseSandBox)

units := []paypal.PurchaseUnitRequest{{
    Amount: &paypal.PurchaseUnitAmount{
        Currency: "USD",
        Value:    "10.00",
    },
    CustomID: orderID,
}}

appCtx := &paypal.ApplicationContext{
    ReturnURL: returnURL,
    CancelURL: cancelURL,
}

order, _ := c.CreateOrder(ctx, paypal.OrderIntentCapture, units, nil, appCtx)
// order.Links 中找 rel="approve" 的链接即为支付页
```

捕获支付（用户返回后）：
```go
capture, _ := c.CaptureOrder(ctx, paypalOrderID, paypal.CaptureOrderRequest{})
// capture.Status == "COMPLETED" 表示成功
```

---

## 与项目的集成点

| 文件 | 说明 |
|-----|------|
| `pkg/payment/paypal.go` | PayPal 网关实现（实现 `Gateway` 接口） |
| `pkg/payment/gateway.go` | 新增 `PayPalConfig` 结构体 |
| `pkg/payment/factory.go` | 注册 `"paypal"` case |
| `internal/routes/payment.go` | 添加 `/webhook/paypal` 路由 |
| `web/templates/super/payment.html` | 管理后台 PayPal 配置表单（已有，无需修改） |

---

## PayPal vs Stripe 流程差异

| | Stripe | PayPal |
|---|---|---|
| 支付页面 | Stripe Hosted Checkout（返回 URL 直接跳转） | PayPal Approval Page（redirect） |
| 扣款时机 | Checkout 完成后自动扣款 | 需要服务端显式 `CaptureOrder` |
| 确认方式 | Webhook (`checkout.session.completed`) | 重定向回调 + `CaptureOrder` API |
| Webhook | 必需（异步确认） | 可选（备用确认） |

因此，PayPal 的主要确认路径是通过 `handleBillingCallback` → `queryGatewayAndComplete` → `QueryOrder` 完成。`QueryOrder` 方法内部会自动调用 `CaptureOrder` 完成实际扣款。

---

## Sandbox 测试

1. 在 [PayPal Developer Portal](https://developer.paypal.com/) 创建 Sandbox App
2. 获取 Client ID 和 Secret
3. 在管理后台 Payment Configuration 中填入，Mode 选择 "Sandbox"
4. 使用 PayPal 提供的 Sandbox 测试账号完成支付

---

## 注意事项

- `plutov/paypal` 要求 Go 1.25+（项目已升级）
- PayPal 金额单位是 **元**（如 "10.00"），不是分。代码内部已做 `amount / 100` 转换
- `CustomID` 字段用于存储我们的 `order_id`，PayPal 会在回调中原样返回
- 生产环境需将 Mode 设为 `"live"`，使用正式 API 凭证
