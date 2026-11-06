# Phase 3: 企业功能 + 支付集成 + 高级路由

**Date:** 2026-05-07  
**Status:** ✅ **已完成** — 所有核心功能已实现并测试通过  
**Scope:** `ai-portal-go` + `ai-proxy-go`  
**Depends on:** Phase 1 + Phase 2 完成  

---

## 1. Phase 2 遗留项（已完成）

| 遗留项 | 状态 | 说明 |
|--------|------|------|
| Proxy round_robin 分布式轮询 | ✅ 已完成 | Redis 计数器已实现并验证 |
| Proxy priority fallback | ✅ 已完成 | 按 group_priority 顺序查找模型已验证 |

---

## 2. 已知问题（已修复）

| 问题 | 状态 | 修复说明 |
|------|------|----------|
| `cache_read_price` 列不存在 | ✅ 已修复 | 从 sync.go 查询中移除此列 |
| KeyPool API 返回0条 | ✅ 已修复 | 移除不存在的 `notes` 字段 |

---

## 3. Phase 3 功能清单

### 3.1 企业功能（P0）— ✅ 已完成

| 功能 | 状态 | 说明 |
|------|------|------|
| **Enterprise Admin 邀请** | ✅ | Super Admin 发送邮件邀请，受邀者点击链接注册为企业管理员 |
| **Enterprise Dashboard 增强** | ✅ | 企业管理员查看成员用量、配额消耗、Key 管理 |
| **Enterprise 成员管理** | ✅ | 企业管理员邀请/移除成员，成员自助加入 |
| **Enterprise 配额管理** | ✅ | 按企业分配配额，超额预警，配额充值 |

### 3.2 支付集成（P1）— ✅ 已完成

| 功能 | 状态 | 说明 |
|------|------|------|
| **Stripe 集成** | ✅ | Checkout Session + Webhook，支持信用卡支付 |
| **支付宝集成** | ✅ | 手机网站支付 + 异步通知 |
| **微信支付集成** | ✅ | Native支付 + 回调处理 |
| **订单流程完善** | ✅ | 创建→支付→确认→充值 全链路 |
| **Admin 支付配置** | ✅ | Stripe/支付宝/微信的密钥配置页面 |

### 3.3 Subscription 购买流程（P1）— ✅ 已完成

| 功能 | 状态 | 说明 |
|------|------|------|
| **用户自助购买** | ✅ | Dashboard 展示套餐列表，选择套餐→创建订单→支付→激活 |
| **套餐对比页面** | ✅ | 展示各套餐的限流、价格、模型权限对比 |
| **自动续费** | ✅ | 到期前 N 天自动扣款续费 |
| **兑换码购买** | ✅ | 用户用兑换码兑换套餐 |

### 3.4 高级路由（P2）— ✅ 已完成

| 功能 | 说明 | 工作量 |
|------|------|--------|
| **按价格路由** | 选择同模型中价格最低的 provider | 3h |
| **按延迟路由** | 选择历史平均延迟最低的 provider | 3h |
| **故障转移** | provider 返回错误时自动切换到下一个 | 2h |
| **路由策略配置** | Admin 配置全局路由策略优先级 | 2h |

---

## 3. 技术方案

### 3.1 Enterprise Admin 邀请流程

```
Super Admin 输入邮箱 → 创建邀请记录 → 发送邮件（含 token 链接）
    ↓
受邀者点击链接 → 验证 token → 跳转注册页面
    ↓
注册完成 → 自动关联到企业 → 角色设为 enterprise_admin
    ↓
企业管理员登录 → 看到企业管理菜单
```

**数据库设计：**

```sql
CREATE TABLE enterprise_invitations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enterprise_id UUID NOT NULL REFERENCES enterprises(id),
    email VARCHAR(255) NOT NULL,
    token VARCHAR(64) NOT NULL UNIQUE,
    role VARCHAR(20) DEFAULT 'enterprise_admin',
    status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'expired', 'cancelled')),
    invited_by UUID REFERENCES users(id),
    expires_at TIMESTAMP NOT NULL,
    accepted_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_enterprise_invitations_token ON enterprise_invitations(token);
CREATE INDEX idx_enterprise_invitations_email ON enterprise_invitations(email);
```

**API 端点：**

| 方法 | 端点 | 说明 |
|------|------|------|
| POST | `/api/admin/enterprise/:id/invite` | 发送邀请 |
| GET | `/api/enterprise/accept?token=xxx` | 验证邀请 token |
| POST | `/api/enterprise/accept` | 接受邀请（注册/登录后） |
| DELETE | `/api/admin/enterprise/invite/:id` | 取消邀请 |

### 3.2 支付集成架构

```
用户选择套餐/充值金额 → 创建订单（status=pending）
    ↓
选择支付方式 → 调用支付网关创建支付会话
    ↓
用户完成支付 → 支付网关回调 Webhook
    ↓
Webhook 验证签名 → 更新订单状态 → 充值余额/激活套餐
    ↓
通知用户（页面跳转/邮件）
```

**支付网关抽象层：**

```go
// PaymentGateway 支付网关接口
type PaymentGateway interface {
    // CreatePayment 创建支付会话
    CreatePayment(order *Order) (paymentURL string, err error)
    // HandleWebhook 处理回调
    HandleWebhook(payload []byte, signature string) (*WebhookResult, error)
    // VerifySignature 验证签名
    VerifySignature(payload []byte, signature string) bool
}

// WebhookResult 回调结果
type WebhookResult struct {
    OrderID       string
    Status        string // success, failed, refunded
    PaymentID     string
    Amount        int64
    PaidAt        time.Time
}
```

**Stripe 实现示例：**

```go
type StripeGateway struct {
    secretKey      string
    webhookSecret  string
    successURL     string
    cancelURL      string
}

func (g *StripeGateway) CreatePayment(order *Order) (string, error) {
    params := &stripe.CheckoutSessionParams{
        PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
        LineItems: []*stripe.CheckoutSessionLineItemParams{
            {
                PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
                    Currency: stripe.String("cny"),
                    ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
                        Name: stripe.String(order.ProductName),
                    },
                    UnitAmount: stripe.Int64(order.Amount),
                },
                Quantity: stripe.Int64(1),
            },
        },
        Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
        SuccessURL: stripe.String(g.successURL + "?order_id=" + order.OrderID),
        CancelURL:  stripe.String(g.cancelURL + "?order_id=" + order.OrderID),
        Metadata: map[string]string{
            "order_id": order.OrderID,
            "user_id":  order.UserID,
        },
    }
    session, err := session.New(params)
    return session.URL, err
}
```

**支付宝实现要点：**
- 使用 `alipay-sdk-go` 官方 SDK
- 手机网站支付 (`alipay.trade.wap.pay`)
- 异步通知验签（RSA2）

**微信支付实现要点：**
- 使用 `wechatpay-go` 官方 SDK
- Native支付（二维码）
- 回调签名验证（SHA256-RSA2048）

### 3.3 订单流程完善

**订单状态机：**

```
pending → paid → completed
   ↓        ↓
cancelled  refunded
```

**数据库变更：**

```sql
-- 扩展 orders 表
ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS product_name VARCHAR(255),
  ADD COLUMN IF NOT EXISTS payment_id VARCHAR(255),
  ADD COLUMN IF NOT EXISTS paid_at TIMESTAMP,
  ADD COLUMN IF NOT EXISTS refunded_at TIMESTAMP,
  ADD COLUMN IF NOT EXISTS refund_amount BIGINT DEFAULT 0,
  ADD COLUMN IF NOT EXISTS metadata JSONB DEFAULT '{}';

-- 支付配置表（已有 system_settings，但独立表更清晰）
CREATE TABLE IF NOT EXISTS payment_gateways (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    gateway VARCHAR(50) NOT NULL UNIQUE, -- stripe, alipay, wechat
    config JSONB NOT NULL,
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
```

### 3.4 Subscription 购买流程

```
Dashboard "套餐中心" → 展示可用套餐列表
    ↓
用户选择套餐 → 创建订单（product_type=subscription, plan_id=xxx）
    ↓
选择支付方式 → 支付 → Webhook 确认
    ↓
自动创建 user_subscription 记录
自动创建/更新 API Key 的 group 绑定
自动设置配额和限流
```

**API 端点：**

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/api/subscriptions/plans` | 获取公开套餐列表 |
| POST | `/api/subscriptions/purchase` | 购买套餐（创建订单） |
| GET | `/api/subscriptions/my` | 我的订阅 |
| POST | `/api/subscriptions/:id/cancel` | 取消订阅 |
| POST | `/api/subscriptions/:id/renew` | 续费 |

### 3.5 高级路由策略

**Proxy 路由决策流程：**

```
请求到达 → 解析 model name
    ↓
查找 service_models 获取可用 provider 列表
    ↓
按路由策略排序：
  - priority: 按 provider priority 排序
  - lowest_price: 按 input_price + output_price 排序
  - lowest_latency: 按 channel_health.avg_latency 排序
  - round_robin: 轮询
    ↓
选择第一个可用 provider → 选择 upstream key → 转发请求
    ↓
如果失败 → 自动 fallback 到下一个 provider
```

**配置存储：**

```sql
-- 全局路由策略配置
INSERT INTO system_settings (key, value, category, description) VALUES
  ('routing_strategy', 'priority', 'proxy', '路由策略: priority/lowest_price/lowest_latency/round_robin'),
  ('routing_fallback_enabled', 'true', 'proxy', '是否启用故障自动转移'),
  ('routing_max_retries', '3', 'proxy', '最大重试次数'),
  ('routing_retry_delay_ms', '100', 'proxy', '重试间隔(毫秒)');
```

---

## 4. 实施计划

### 迭代 1：企业功能（预计 12h）

| 任务 | 工作量 | 依赖 |
|------|--------|------|
| 数据库迁移（enterprise_invitations） | 1h | 无 |
| Enterprise Admin 邀请 API | 3h | 迁移 |
| 邮件发送集成 | 2h | SMTP 已配置 |
| Enterprise Dashboard 增强 | 3h | 无 |
| Enterprise 成员管理 API | 2h | 无 |
| 前端页面（邀请/成员管理） | 1h | API |

### 迭代 2：支付集成（预计 17h）

| 任务 | 工作量 | 依赖 |
|------|--------|------|
| 支付网关抽象层 | 2h | 无 |
| Stripe 集成 | 4h | 抽象层 |
| 支付宝集成 | 3h | 抽象层 |
| 微信支付集成 | 3h | 抽象层 |
| Webhook 处理 | 2h | 支付集成 |
| 订单流程完善 | 2h | Webhook |
| Admin 支付配置页面 | 1h | 无 |

### 迭代 3：Subscription + 高级路由（预计 17h）

| 任务 | 工作量 | 依赖 |
|------|--------|------|
| 用户套餐购买流程 | 4h | 支付集成 |
| 套餐对比页面 | 2h | 无 |
| 自动续费逻辑 | 3h | 购买流程 |
| Proxy 路由策略抽象 | 3h | 无 |
| 按价格/延迟路由 | 4h | 路由抽象 |
| 故障转移机制 | 1h | 路由抽象 |

**总计预计：46h（约 6 个工作日）**

---

## 5. 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 支付回调延迟/丢失 | 用户支付成功但未到账 | Webhook 重试 + 主动查询对账 |
| 企业配额溢出 | 超额使用 | 请求前检查配额，超额返回 402 |
| 路由策略导致延迟增加 | 用户体验下降 | 设置超时阈值，超时自动 fallback |
| 邮件发送失败 | 邀请无法送达 | 重试机制 + 手动复制邀请链接 |

---

## 6. 与现有系统的兼容性

| 组件 | 兼容性 |
|------|--------|
| ai-proxy-go | 路由策略通过 sync API 下发，Proxy 无需重启 |
| 数据库 | 所有变更为 ADD COLUMN / CREATE TABLE，不破坏现有数据 |
| 前端 | 新增页面，不修改现有页面结构 |
| API | 新增端点，不修改现有端点行为 |

---

## 7. 测试策略

| 模块 | 测试方式 |
|------|----------|
| 企业邀请 | 单元测试 + 邮件发送测试 |
| 支付集成 | Stripe Test Mode / 支付宝沙箱 / 微信支付沙箱 |
| 订单流程 | 端到端测试（创建→支付→确认） |
| 路由策略 | 单元测试 + Mock Provider |
| Webhook | 本地 Stripe CLI / ngrok 测试 |

---

## 8. 待确认问题

| # | 问题 | 建议 |
|---|------|------|
| 1 | 支持哪些支付方式？ | Stripe(国际) + 支付宝/微信(国内)，后续可扩展 |
| 2 | 企业配额是预付费还是后付费？ | 建议预付费（充值配额），简化计费 |
| 3 | 自动续费周期？ | 建议按月，到期前 3 天扣款 |
| 4 | 路由策略是全局还是 per-key？ | Phase 3 先做全局，Phase 4 可扩展为 per-key |
| 5 | 故障转移的最大重试次数？ | 建议 3 次，总超时 30 秒 |
