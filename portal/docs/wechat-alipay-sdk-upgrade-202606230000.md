# 微信支付 V3 与支付宝海外版 官方 SDK 集成方案

## 概述

本平台注册于新加坡，应集成对应的海外版支付接口。本文档说明两个支付网关的官方 SDK 迁移方案。

---

## 一、微信支付：V2 → V3 官方 SDK

### 现状问题

当前 `wechat.go` 使用微信支付 **V2 API**：
- XML 协议（已过时）
- 对称密钥 HMAC-SHA256 签名（安全性较弱）
- 调用 `/pay/unifiedorder`（V2 端点）
- 手动构建 XML、手动验签

### 官方 SDK

- **仓库**：[wechatpay-apiv3/wechatpay-go](https://github.com/wechatpay-apiv3/wechatpay-go)
- **性质**：✅ 微信支付官方维护
- **Stars**：1.3k
- **版本**：v0.2.21（2025-07）
- **协议**：Apache-2.0
- **要求**：Go 1.18+

### V3 API 相比 V2 的优势

| V2 (当前) | V3 (目标) |
|-----------|-----------|
| XML 协议 | JSON 协议 |
| HMAC-SHA256 对称签名 | RSA-SHA256 非对称签名 |
| 手动管理 API 密钥 | 平台证书自动轮换 |
| 回调无加密 | 回调 AES-256-GCM 加密 |
| 无官方 SDK | 官方 Go SDK 支持 |
| 新功能不再更新 | 所有新功能仅 V3 发布 |

### 配置变更

V2 配置（旧）：
```json
{
  "app_id": "wx...",
  "mch_id": "16...",
  "api_key": "32位密钥"
}
```

V3 配置（新）：
```json
{
  "app_id": "wx...",
  "mch_id": "16...",
  "mch_serial_no": "商户证书序列号",
  "mch_private_key": "-----BEGIN PRIVATE KEY-----\n...",
  "apiv3_key": "APIv3密钥（用于回调解密）"
}
```

### 安装

```bash
go get github.com/wechatpay-apiv3/wechatpay-go
```

### 核心代码示例

初始化 Client：
```go
import (
    "github.com/wechatpay-apiv3/wechatpay-go/core"
    "github.com/wechatpay-apiv3/wechatpay-go/core/option"
    "github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
    "github.com/wechatpay-apiv3/wechatpay-go/utils"
)

mchPrivateKey, _ := utils.LoadPrivateKey(privateKeyPEM)

client, _ := core.NewClient(ctx, []core.ClientOption{
    option.WithWechatPayAutoAuthCipher(mchID, mchSerialNo, mchPrivateKey, apiV3Key),
}...)
```

Native 下单（扫码支付）：
```go
svc := native.NativeApiService{Client: client}
resp, _, _ := svc.Prepay(ctx, native.PrepayRequest{
    Appid:       core.String(appID),
    Mchid:       core.String(mchID),
    Description: core.String(order.ProductName),
    OutTradeNo:  core.String(order.OrderID),
    NotifyUrl:   core.String(notifyURL),
    Amount:      &native.Amount{Total: core.Int64(order.Amount)},
})
// resp.CodeUrl 即二维码支付链接
```

回调验签与解密：
```go
import "github.com/wechatpay-apiv3/wechatpay-go/core/notify"

handler := notify.NewNotifyHandler(apiV3Key, verifiers.NewSHA256WithRSAVerifier(certVisitor))
transaction := new(payments.Transaction)
_, err := handler.ParseNotifyRequest(ctx, httpRequest, transaction)
// transaction.OutTradeNo, transaction.TransactionId 等
```

---

## 二、支付宝：国内版 → 海外版（Global）官方 SDK

### 现状问题

当前 `alipay.go` 使用支付宝**国内版**接口：
- 调用 `alipay.trade.wap.pay`（国内 API）
- 网关 `openapi.alipay.com`（中国大陆）
- 不适用于新加坡注册平台的跨境场景

### 官方 SDK

- **仓库**：[alipay/global-open-sdk-go](https://github.com/alipay/global-open-sdk-go)
- **性质**：✅ 蚂蚁集团（Alipay）官方维护，GitHub Verified Organization
- **版本**：v1.2.45（2026-05-20）
- **协议**：MIT
- **要求**：Go 1.22.5+

### 海外版 vs 国内版差异

| 国内版 (当前) | 海外版 (目标) |
|--------------|--------------|
| `openapi.alipay.com` | `open.alipayplus.com` |
| `alipay.trade.wap.pay` | `/ams/api/v1/payments/pay` |
| CNY 人民币 | 支持 HKD/SGD/USD/EUR 等多币种 |
| RSA2 签名（自己实现） | SDK 内置签名/验签 |
| 仅支持支付宝 | 支持 Alipay+（Alipay HK、GCash、TNG 等） |

### 配置变更

旧配置（国内版）：
```json
{
  "app_id": "2021...",
  "private_key": "-----BEGIN RSA PRIVATE KEY-----...",
  "public_key": "-----BEGIN PUBLIC KEY-----...",
  "notify_url": "...",
  "return_url": "...",
  "sandbox": false
}
```

新配置（海外版）：
```json
{
  "client_id": "SANDBOX_5Y...",
  "merchant_private_key": "-----BEGIN PRIVATE KEY-----...",
  "alipay_public_key": "-----BEGIN PUBLIC KEY-----...",
  "gateway_url": "https://open.alipayplus.com",
  "notify_url": "...",
  "return_url": "..."
}
```

### 安装

```bash
go get github.com/alipay/global-open-sdk-go
```

### 核心代码示例

初始化 Client：
```go
import "github.com/alipay/global-open-sdk-go/com/alipay/api"

client := api.NewDefaultAlipayClient(
    "https://open.alipayplus.com",
    clientID,
    merchantPrivateKey,
    alipayPublicKey,
)
```

创建支付：
```go
import (
    "github.com/alipay/global-open-sdk-go/com/alipay/api/request/pay"
    "github.com/alipay/global-open-sdk-go/com/alipay/api/model"
)

payRequest, request := pay.NewAlipayPayRequest()
request.PaymentRequestId = order.OrderID
request.Order = &model.Order{
    OrderDescription: order.ProductName,
    ReferenceOrderId: order.OrderID,
    OrderAmount:      model.NewAmount(amountStr, "SGD"),
}
request.PaymentAmount = model.NewAmount(amountStr, "SGD")
request.PaymentNotifyUrl = notifyURL
request.PaymentRedirectUrl = returnURL
request.ProductCode = model.CASHIER_PAYMENT

resp, err := client.Execute(payRequest)
// resp.NormalUrl 为支付页面 URL
```

---

## 三、UI 变更

### 微信支付配置表单

需要替换原有字段：

| 旧字段 | 新字段 |
|-------|--------|
| App ID | App ID（保留） |
| Merchant ID | Merchant ID（保留） |
| API Key | ❌ 删除 |
| — | 商户证书序列号（新增） |
| — | 商户私钥 PEM（新增） |
| — | APIv3 密钥（新增） |

### 支付宝配置表单

需要替换原有字段：

| 旧字段 | 新字段 |
|-------|--------|
| App ID | Client ID（改名） |
| Private Key | Merchant Private Key（保留） |
| Alipay Public Key | Alipay Public Key（保留） |
| Sandbox toggle | ❌ 删除（通过 Client ID 前缀 `SANDBOX_` 自动判断） |

---

## 四、实施步骤

1. 安装依赖：`wechatpay-apiv3/wechatpay-go` + `alipay/global-open-sdk-go`
2. 重写 `wechat.go`：使用 V3 SDK，Native 支付下单 + 回调通知解析
3. 重写 `alipay.go`：使用 Global SDK，Cashier Payment + 回调通知
4. 更新 `gateway.go` 配置结构体
5. 更新 `factory.go` 网关创建逻辑
6. 更新 `super/payment.html` 配置表单
7. 编译测试

---

## 参考

- [微信支付 APIv3 官方 Go SDK](https://github.com/wechatpay-apiv3/wechatpay-go)
- [微信支付 APIv3 接口文档](https://pay.weixin.qq.com/doc/global/v3/en/4012356376)
- [Alipay Global Open SDK Go](https://github.com/alipay/global-open-sdk-go)
- [Alipay+ 开发者文档](https://global.alipay.com/docs/ac/ams_overview)
