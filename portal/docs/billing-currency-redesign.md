# 计费与币种架构重构方案

> 状态：**已完成** ✅  
> 作者：AI Assistant  
> 日期：2026-05-09；状态更新 2026-06-11
>
> 后端 schema（`exchange_rates`、`users.preferred_currency`、`orders.currency/exchange_rate`、`service_models.cache_creation_price`）、`ExchangeService`、币种偏好 API、充值订单币种支持、按模型定价计费均已实现。前端货币统一模块已落地：在 `app-common.js` 中实现 `window.Currency` 统一 API，`layout.html` 全局注入用户币种与汇率，6 个模板中的重复 JS 函数已清理。

---

## 一、现状问题诊断

### 1.1 当前计费链路

```
Proxy (多区域)
  ├─ 计算抽象 ratio → cost=5.0（无单位，无币种含义）
  ├─ 通过 webhook 发送 cost 到 Portal
  └─ 无汇率、无实际定价参与

Portal (中心节点)
  ├─ 直接 trust Proxy 传来的 cost
  ├─ cost * 100 → 当作 CNY 分扣费
  ├─ fallback: tokens * 0.00001（硬编码，无视模型定价差异）
  └─ service_models 中 USD/1M 定价完全未参与计费
```

### 1.2 核心问题

| # | 问题 | 影响 |
|---|------|------|
| 1 | Proxy 计算的 `cost` 是抽象 ratio，Portal 直接当作 CNY | 计费金额无实际意义，不同模型同价 |
| 2 | Portal 拥有 `service_models` 的 USD 定价，但不参与计费 | 定价数据形同虚设 |
| 3 | `system_settings.usd_exchange_rate = 720`（7.20）存在但未被读取 | 汇率配置无效 |
| 4 | 多 Proxy 部署时，每个 Proxy 各自计算 cost | 计费逻辑分散，难以统一管控 |
| 5 | 无币种概念，用户无法选择结算货币 | 只能 CNY，海外用户受限 |

### 1.3 设计原则

1. **Proxy 只上报流量，不做计费** — 计费是 Portal 的核心职责
2. **Portal 统一读取定价 + 汇率 → 计算成本 → 扣费** — 单点管控
3. **语言与币种解耦** — 界面语言 ≠ 结算币种
4. **汇率自动更新** — 每日/每8小时自动获取最新汇率
5. **充值币种可选** — 支付宝/微信默认 CNY，Stripe/PayPal 默认 USD

---

## 二、Proxy 端改造

### 2.1 Webhook Payload 变更

**当前 Proxy 发送（ai-proxy-go）：**
```go
type UsageReport struct {
    TraceID        string  `json:"traceId"`
    APIKeyHash     string  `json:"apiKeyHash"`
    Model          string  `json:"model"`
    Provider       string  `json:"provider"`
    InputTokens    int     `json:"inputTokens"`
    OutputTokens   int     `json:"outputTokens"`
    CachedTokens   int     `json:"cachedTokens"`    // cache hit tokens
    Cost           float64 `json:"cost"`             // ❌ 抽象 ratio，废弃
    LatencyMs      int     `json:"latencyMs"`
    Timestamp      string  `json:"timestamp"`
    KeyID          string  `json:"keyId"`
    ServerID       string  `json:"serverId"`
    RequestCount   int     `json:"requestCount"`
    StatusCode     int     `json:"statusCode"`
    CacheCreation  int     `json:"cacheCreation"`    // cache write tokens
    Reasoning      int     `json:"reasoning"`        // reasoning tokens
}
```

**改造后 Proxy 发送：**
```go
type UsageReport struct {
    TraceID            string  `json:"traceId"`
    APIKeyHash         string  `json:"apiKeyHash"`
    Model              string  `json:"model"`              // e.g. "gpt-4o"
    Provider           string  `json:"provider"`           // e.g. "openai"
    InputTokens        int     `json:"inputTokens"`        // 原始 input tokens
    OutputTokens       int     `json:"outputTokens"`       // completion tokens
    CachedTokens       int     `json:"cachedTokens"`       // prompt cache hit
    CacheCreationTokens int    `json:"cacheCreationTokens"` // prompt cache write
    ReasoningTokens    int     `json:"reasoningTokens"`    // reasoning tokens
    // ❌ 移除 Cost 字段
    LatencyMs          int     `json:"latencyMs"`
    Timestamp          string  `json:"timestamp"`
    KeyID              string  `json:"keyId"`
    ServerID           string  `json:"serverId"`
    RequestCount       int     `json:"requestCount"`
    StatusCode         int     `json:"statusCode"`
}
```

**关键变化：**
- **删除 `cost` 字段** — Proxy 不再计算成本
- **新增 `cacheCreationTokens`** — 明确区分 cache read vs cache write
- **保留 `cachedTokens`** — 即 cache hit tokens，用于 cache hit 定价
- **保留 `reasoningTokens`** — 用于 reasoning 定价

### 2.2 Proxy 代码改动点

**文件：ai-proxy-main/ai-proxy-go/pkg/billing/billing.go**
- 保留 `CalculateCost` 函数（用于内部监控/统计），但不再将其写入 webhook
- 或完全删除 cost 计算逻辑，仅保留 token 统计

**文件：ai-proxy-main/ai-proxy-go/internal/audit/audit.go**
- 修改 `UsageReport` struct，删除 `Cost` 字段
- 确保 `cacheCreation` 和 `reasoning` 字段正确上报

**文件：ai-proxy-main/ai-proxy-go/internal/server/handlers.go**
- 删除 `cost = deps.Billing.CalculateCost(...)` 调用
- audit entry 中不再包含 cost

---

## 三、Portal 端改造

### 3.1 Webhook 接收改造

**文件：ai-portal-go/internal/routes/webhook.go**

**Step 1: 修改 `UsageRecord` struct**
```go
type UsageRecord struct {
    TraceID              string  `json:"traceId"`
    APIKeyHash           string  `json:"apiKeyHash"`
    Model                string  `json:"model"`
    Provider             string  `json:"provider"`
    InputTokens          int     `json:"inputTokens"`
    OutputTokens         int     `json:"outputTokens"`
    CachedTokens         int     `json:"cachedTokens"`        // cache hit
    CacheCreationTokens  int     `json:"cacheCreationTokens"` // cache write
    ReasoningTokens      int     `json:"reasoningTokens"`
    // Cost 字段已删除
    LatencyMs            int     `json:"latencyMs"`
    RequestCount         int     `json:"requestCount"`
    ServerID             string  `json:"serverId"`
    Timestamp            string  `json:"timestamp"`
    CacheHit             bool    `json:"cacheHit"`
    Error                string  `json:"error"`
}
```

**Step 2: 替换 `processRecord` 中的 cost 计算逻辑**

```go
// 原代码（废弃）
cost := record.Cost
if cost == 0 && (record.InputTokens > 0 || record.OutputTokens > 0) {
    cost = float64(record.InputTokens+record.OutputTokens) * 0.00001
}

// 新代码 — Portal 根据模型定价 + 汇率计算 CNY 成本
func (r *WebhookRoutes) calculateCost(ctx context.Context, record *UsageRecord) (float64, error) {
    // 1. 查询模型定价（USD/1M tokens）
    var inputPrice, outputPrice, cacheHitPrice, cacheMissPrice, reasoningPrice float64
    err := r.db.QueryRow(ctx,
        `SELECT COALESCE(input_price_cache_hit, 0),
                COALESCE(input_price_cache_miss, 0),
                COALESCE(output_price, 0),
                COALESCE(reasoning_price, 0)
         FROM service_models
         WHERE provider = $1 AND (api_model_name = $2 OR product_id = $2)
         LIMIT 1`,
        record.Provider, record.Model,
    ).Scan(&cacheHitPrice, &cacheMissPrice, &outputPrice, &reasoningPrice)
    if err != nil {
        // 模型未找到，fallback 到默认费率
        r.logger.Warn("Model pricing not found, using default",
            zap.String("model", record.Model),
            zap.String("provider", record.Provider))
        cacheMissPrice = 0.01  // $0.01 / 1M tokens 兜底
        outputPrice = 0.03
    }

    // 2. 计算 USD 成本
    // cache hit: 使用 input_price_cache_hit
    // cache miss: 使用 input_price_cache_miss
    // cache write: 通常按 cache miss 计费（或单独定价字段）
    inputCost := 0.0
    if record.CachedTokens > 0 {
        inputCost += float64(record.CachedTokens) / 1_000_000.0 * cacheHitPrice
    }
    cacheMissTokens := record.InputTokens - record.CachedTokens
    if cacheMissTokens > 0 {
        inputCost += float64(cacheMissTokens) / 1_000_000.0 * cacheMissPrice
    }
    if record.CacheCreationTokens > 0 {
        // cache write 暂按 cache miss 价格计费，后续可扩展独立定价
        inputCost += float64(record.CacheCreationTokens) / 1_000_000.0 * cacheMissPrice
    }

    outputCost := float64(record.OutputTokens) / 1_000_000.0 * outputPrice
    reasoningCost := float64(record.ReasoningTokens) / 1_000_000.0 * reasoningPrice

    totalUSDCost := inputCost + outputCost + reasoningCost

    // 3. 查询汇率并换算为 CNY
    exchangeRate := r.getExchangeRate(ctx)  // 从 system_settings 或缓存读取
    totalCNYCost := totalUSDCost * exchangeRate

    return totalCNYCost, nil
}
```

### 3.2 数据库 Schema 变更

**新增 `exchange_rates` 表（用于历史汇率记录）：**
```sql
CREATE TABLE IF NOT EXISTS exchange_rates (
    id          SERIAL PRIMARY KEY,
    from_currency VARCHAR(3) NOT NULL DEFAULT 'USD',
    to_currency   VARCHAR(3) NOT NULL DEFAULT 'CNY',
    rate        NUMERIC(10, 6) NOT NULL,
    source      VARCHAR(50),           -- e.g. 'exchangerate-api.com', 'manual'
    fetched_at  TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at  TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_exchange_rates_currencies ON exchange_rates(from_currency, to_currency, fetched_at DESC);
```

**修改 `users` 表（增加币种偏好）：**
```sql
ALTER TABLE users ADD COLUMN IF NOT EXISTS preferred_currency VARCHAR(3) DEFAULT 'CNY';
```

**修改 `orders` 表（记录订单币种）：**
```sql
ALTER TABLE orders ADD COLUMN IF NOT EXISTS currency VARCHAR(3) DEFAULT 'CNY';
ALTER TABLE orders ADD COLUMN IF NOT EXISTS exchange_rate NUMERIC(10, 6);  -- 下单时的汇率
```

**修改 `service_models` 表（增加 cache write 定价）：**
```sql
-- 可选：如果 cache write 需要独立定价
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS cache_creation_price NUMERIC(10, 6) DEFAULT 0;
```

### 3.3 汇率自动更新服务

**实现方式：后台定时任务（Go routine + ticker）**

```go
// internal/services/exchange.go

package services

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    "github.com/ai-proxy/ai-portal-go/internal/db"
    "go.uber.org/zap"
)

type ExchangeService struct {
    db     *db.DB
    logger *zap.Logger
    // 支持多个汇率源，主源失败时 fallback
    sources []ExchangeRateSource
}

type ExchangeRateSource interface {
    Name() string
    Fetch(ctx context.Context, from, to string) (float64, error)
}

// 每小时或每 8 小时更新一次
const ExchangeRateUpdateInterval = 8 * time.Hour

func (s *ExchangeService) Start(ctx context.Context) {
    ticker := time.NewTicker(ExchangeRateUpdateInterval)
    defer ticker.Stop()

    // 启动时立即更新一次
    s.updateRates(ctx)

    for {
        select {
        case <-ticker.C:
            s.updateRates(ctx)
        case <-ctx.Done():
            return
        }
    }
}

func (s *ExchangeService) updateRates(ctx context.Context) {
    rate, err := s.fetchWithFallback(ctx, "USD", "CNY")
    if err != nil {
        s.logger.Error("Failed to fetch exchange rate", zap.Error(err))
        return
    }

    // 更新 system_settings
    _ = s.db.Exec(ctx,
        `INSERT INTO system_settings (key, value, category, description)
         VALUES ('usd_exchange_rate', $1, 'operation', 'USD to CNY exchange rate (cents)')
         ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
        fmt.Sprintf("%.0f", rate*100),  // 以分为单位存储，如 720 = 7.20
    )

    // 记录历史
    _ = s.db.Exec(ctx,
        `INSERT INTO exchange_rates (from_currency, to_currency, rate, source, fetched_at)
         VALUES ($1, $2, $3, $4, NOW())`,
        "USD", "CNY", rate, s.sources[0].Name(),
    )

    s.logger.Info("Exchange rate updated", zap.Float64("usd_to_cny", rate))
}

func (s *ExchangeService) fetchWithFallback(ctx context.Context, from, to string) (float64, error) {
    for _, src := range s.sources {
        rate, err := src.Fetch(ctx, from, to)
        if err == nil {
            return rate, nil
        }
        s.logger.Warn("Exchange source failed, trying next",
            zap.String("source", src.Name()),
            zap.Error(err))
    }
    return 0, fmt.Errorf("all exchange rate sources failed")
}

// ========== 汇率源实现 ==========

// ExchangerateAPI 免费汇率源（exchangerate-api.com）
type ExchangerateAPI struct{}

func (e *ExchangerateAPI) Name() string { return "exchangerate-api.com" }

func (e *ExchangerateAPI) Fetch(ctx context.Context, from, to string) (float64, error) {
    url := fmt.Sprintf("https://api.exchangerate-api.com/v4/latest/%s", from)
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return 0, err
    }
    defer resp.Body.Close()

    var result struct {
        Rates map[string]float64 `json:"rates"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return 0, err
    }
    rate, ok := result.Rates[to]
    if !ok {
        return 0, fmt.Errorf("currency %s not found in response", to)
    }
    return rate, nil
}
```

**注册到 main.go：**
```go
// cmd/server/main.go
exchangeService := services.NewExchangeService(database, logger)
go exchangeService.Start(ctx)
```

### 3.4 用户币种偏好设置

**API 端点：**
```
GET  /api/user/currency              → 获取当前用户币种偏好
PUT  /api/user/currency              → 修改币种偏好（CNY/USD）
```

**实现逻辑：**
```go
func (r *UserRoutes) updateCurrency(c *gin.Context) {
    userID := c.GetString("user_id")
    var req struct {
        Currency string `json:"currency" binding:"required,oneof=CNY USD"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid currency"}})
        return
    }

    _ = r.db.Exec(c.Request.Context(),
        `UPDATE users SET preferred_currency = $1, updated_at = NOW() WHERE id = $2::uuid`,
        req.Currency, userID)

    c.JSON(http.StatusOK, gin.H{"success": true})
}
```

**前端展示适配：**
- 所有金额展示读取 `user.preferred_currency`
- `formatCurrency` helper 根据币种显示 `¥` 或 `$`
- 汇率换算发生在展示层，不改变数据库中的原始金额（始终以 CNY 分为单位存储）

### 3.5 充值币种处理

| 充值渠道 | 默认币种 | 用户可否修改 | 说明 |
|---------|---------|-------------|------|
| 支付宝 | CNY | ✅ 可选 CNY/USD | 国内默认 CNY，海外支付宝国际版可 USD |
| 微信支付 | CNY | ✅ 可选 CNY/USD | 国内默认 CNY，海外微信可 USD |
| Stripe | USD | ✅ 可选 CNY/USD | 国际渠道，默认 USD |
| PayPal | USD | ✅ 可选 CNY/USD | 国际渠道，默认 USD |

**充值订单创建逻辑：**
```go
func (r *OrderRoutes) createOrder(c *gin.Context) {
    // ...
    currency := req.Currency  // 用户选择的币种
    if currency == "" {
        // 根据支付方式默认
        switch req.PaymentMethod {
        case "alipay", "wechat":
            currency = "CNY"
        case "stripe", "paypal":
            currency = "USD"
        default:
            currency = "CNY"
        }
    }

    // 如果充值币种 ≠ CNY，需要汇率换算
    amountCNY := req.Amount  // 用户看到的金额
    if currency == "USD" {
        exchangeRate := getCurrentExchangeRate()
        amountCNY = int64(float64(req.Amount) * exchangeRate)  // USD → CNY
    }

    // 创建订单，记录原始币种和汇率
    _, err := r.db.QueryRow(ctx,
        `INSERT INTO orders (user_id, amount, currency, exchange_rate, product_type, ...)
         VALUES ($1::uuid, $2, $3, $4, 'recharge', ...)
         RETURNING id::text`,
        userID, amountCNY, currency, exchangeRate,
    ).Scan(&orderID)
    // ...
}
```

**订单展示：**
```
充值订单 #ORD-20260509-001
充值金额：$100.00 USD
汇率：1 USD = 7.20 CNY
到账金额：¥720.00 CNY
```

---

## 四、计费公式详解

### 4.1 Token 分类

| 类型 | 来源 | 定价字段 |
|------|------|---------|
| **Input (cache hit)** | `cachedTokens` | `input_price_cache_hit` |
| **Input (cache miss)** | `inputTokens - cachedTokens` | `input_price_cache_miss` |
| **Input (cache write)** | `cacheCreationTokens` | 暂用 `input_price_cache_miss`（或新增 `cache_creation_price`）|
| **Output** | `outputTokens` | `output_price` |
| **Reasoning** | `reasoningTokens` | `reasoning_price` |

### 4.2 计费公式

```
inputCost  = (cachedTokens / 1M) * input_price_cache_hit
           + (cacheMissTokens / 1M) * input_price_cache_miss
           + (cacheCreationTokens / 1M) * input_price_cache_miss

outputCost = (outputTokens / 1M) * output_price
reasoningCost = (reasoningTokens / 1M) * reasoning_price

totalUSD = inputCost + outputCost + reasoningCost
totalCNY = totalUSD * exchangeRate
totalCents = int64(totalCNY * 100)
```

### 4.3 订阅配额抵扣优先级

```
1. 检查用户是否有活跃 subscription
   - 按 plan_type 匹配：token plan 抵扣 tokens，request/code plan 抵扣 request_count
2. 订阅额度内：只扣 subscription quota，不扣余额
3. 超出订阅额度：剩余部分按上述公式计算 CNY 成本，从余额扣除
```

---

## 五、实施计划

### Phase 1: Proxy 改造（先动 Proxy，再动 Portal）

1. **ai-proxy-go**
   - [ ] 修改 `UsageReport` struct：删除 `Cost`，重命名/新增 token 字段
   - [ ] 删除 `handlers.go` 中的 `CalculateCost` 调用
   - [ ] 确保 `cacheCreation` 和 `reasoning` 正确统计并上报
   - [ ] 更新 webhook payload 文档

2. **测试**
   - [ ] 部署新 Proxy，验证 webhook payload 格式正确
   - [ ] 确认 Portal 仍能正常接收（兼容旧 `cost` 字段）

### Phase 2: Portal 计费改造

1. **数据库** ✅
   - [x] 创建 `exchange_rates` 表
   - [x] 给 `users` 表增加 `preferred_currency`
   - [x] 给 `orders` 表增加 `currency` 和 `exchange_rate`
   - [x] 可选：给 `service_models` 增加 `cache_creation_price`

2. **后端** ✅
   - [x] 实现 `ExchangeService` 定时汇率更新
   - [x] 重写 `processRecord`：按模型定价 + 汇率计算成本
   - [x] 删除硬编码 fallback `tokens * 0.00001`
   - [x] 实现用户币种偏好 API
   - [x] 修改充值订单创建逻辑，支持币种选择

3. **前端** ✅
   - [x] 用户设置页增加币种偏好选择
   - [x] 所有金额展示根据用户币种显示 `¥` 或 `$`
   - [x] 充值页根据支付方式默认币种，允许 Stripe/PayPal 切换
   - [x] 订单详情展示原始币种、汇率、到账金额
   - [x] 在 `app-common.js` 实现统一 `Currency` 模块（替代独立 `currency.js`）
   - [x] 在 `layout.html` 统一注入 Currency 配置
   - [x] 清理各模板中重复的 JS 函数

### Phase 3: 联调与验证

1. [ ] 端到端测试：请求 → Proxy → Portal → 计费 → 扣费
2. [ ] 验证 cache hit/miss/reasoning 分别按正确价格计费
3. [ ] 验证汇率更新后新请求使用新汇率
4. [ ] 验证 USD 充值订单正确换算为 CNY 余额
5. [ ] 验证多币种展示正确

---

## 六、风险与回退方案

| 风险 | 缓解措施 |
|------|---------|
| 汇率源不可用 | 多个汇率源 fallback，本地缓存最近有效汇率 |
| 模型定价缺失 | 使用默认兜底费率，同时告警通知管理员补全 |
| Proxy 升级期间 webhook 格式不一致 | Portal 同时兼容新旧 payload（先读 cost，无 cost 则自己算） |
| 用户币种切换后历史订单显示混乱 | 历史订单展示原始币种 + 汇率，余额始终按 CNY 存储 |
| 汇率波动导致充值金额争议 | 订单快照保存下单时的汇率，作为争议依据 |

---

## 七、附录

### A. 汇率源备选

| 源 | 免费额度 | 限制 | URL |
|---|---------|------|-----|
| exchangerate-api.com | 免费 | 每小时限制 | `https://api.exchangerate-api.com/v4/latest/USD` |
| open.er-api.com | 免费 | 无限制 | `https://open.er-api.com/v6/latest/USD` |
| 中国人民银行 | 免费 | 工作日更新 | `http://www.pbc.gov.cn/zhengcehuobisi/11111/index.html`（需解析 HTML）|
| 自建管理后台手动输入 | 免费 | 完全可控 | 管理员在 `/admin/settings` 中修改 |

### B. 字段映射表

| 业务概念 | Proxy JSON 字段 | Portal DB 字段 | 说明 |
|---------|----------------|---------------|------|
| 请求输入 tokens | `inputTokens` | `usage_records.input_tokens` | 原始 input |
| 输出 tokens | `outputTokens` | `usage_records.output_tokens` | completion |
| Cache hit tokens | `cachedTokens` | `usage_records.cached_tokens` | 命中缓存 |
| Cache write tokens | `cacheCreationTokens` | `usage_records.cache_creation_tokens` | 写入缓存 |
| Reasoning tokens | `reasoningTokens` | `usage_records.reasoning_tokens` | 推理 tokens |
| 用户偏好币种 | — | `users.preferred_currency` | CNY/USD |
| 当前汇率 | — | `system_settings.usd_exchange_rate` |  cents，如 720 = 7.20 |
| 订单币种 | — | `orders.currency` | 下单时选择的币种 |

---

## 八、前端货币显示统一方案

> 日期：2026-05-12

### 8.1 现状问题

当前前端货币显示存在严重的代码重复和不一致：

**后端 Go 函数（`internal/web/helpers.go`）：**

| 函数 | 签名 | 行为 |
|------|------|------|
| `formatCurrency` | `(cents, currency)` | 不做汇率转换，只加符号 |
| `formatMoney` | `(cents, currency, exchangeRate)` | 做汇率转换 |

**前端 JS 函数（6个模板各自复制粘贴）：**

| 模板 | 函数名 | 输入单位 | 汇率转换 |
|------|--------|---------|---------|
| `subscriptions.html` | `formatPrice(cents)` | 分 | ✅ USD时转换 |
| `my-subscriptions.html` | `formatPrice(cents)` | 分 | ✅ USD时转换 |
| `orders.html` | `formatAmount(cents, currency, exchangeRate)` | 分 | ✅ 双向转换 |
| `invoices.html` | `formatPrice(cents)` | 分 | ✅ USD时转换 |
| `redemption.html` | `formatPrice(cents, noConversion)` | 分 | ⚠️ 需手动传noConversion |
| `billing-details.html` | `formatAmount(yuan)` | **元** | ✅ USD时转换（5位小数）|

**核心问题：**
1. `formatCurrency` 和 `formatMoney` 混用，导致有的地方做了转换有的没做
2. 每个模板复制粘贴相同的 JS 函数，逻辑略有差异
3. `billing-details.html` 输入单位是元而非分，与其他模板不一致
4. `redemption.html` 需要特殊 `noConversion` 参数

### 8.2 设计原则

> **内部统一存储为 CNY 分，前端统一负责显示转换**

- 后端数据一律以 **CNY 分** 存储和传输
- 前端提供统一的货币格式化工具
- 两种显示模式：
  - `format(cnyCents)` — 输入 CNY 分，自动汇率转换（用于余额、订单、费用等）
  - `formatRaw(cents)` — 输入已是目标币种的分，不转换（用于兑换码面值等）

### 8.3 实施方案（已落地）

**Step 1: 在 `app-common.js` 实现统一 `Currency` 模块**

> 注：原方案计划创建独立 `currency.js`，因项目已有全局共享库 `app-common.js`（由 `layout.html` 统一加载），为减少 HTTP 请求和保持架构一致，最终把货币模块直接加入 `app-common.js`。

```js
// Global defaults; layout.html injects the user's actual currency/rate.
window.userCurrency = window.userCurrency || 'CNY';
window.exchangeRate = window.exchangeRate || 7.20;

window.Currency = {
  get code() { return window.userCurrency || 'CNY'; },
  get rate() { return window.exchangeRate || 7.20; },

  symbol: function(currency) {
    return (currency || this.code) === 'USD' ? '$' : '¥';
  },

  unit: function(currency) {
    return (currency || this.code) === 'USD' ? 'USD' : _t('元');
  },

  // Convert micro-USD quota to a display amount in the target currency.
  toAmount: function(quota, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var rate = opts.exchangeRate || this.rate;
    var usd = Number(quota || 0) / 1000000;
    return currency === 'USD' ? usd : usd * rate;
  },

  // Numeric value only (no symbol).
  formatValue: function(quota, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var decimals = opts.decimals;
    if (decimals === undefined) {
      decimals = currency === 'USD' ? 6 : 2;
    }
    return this.toAmount(quota, opts).toFixed(decimals);
  },

  // Full formatted string with symbol.
  format: function(quota, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var symbol = opts.symbol !== false ? this.symbol(currency) : '';
    return symbol + this.formatValue(quota, opts);
  },

  // Most common UI display: 2 decimals.
  formatShort: function(quota, opts) {
    return this.format(quota, Object.assign({ decimals: 2 }, opts || {}));
  },

  // Input is already in target currency's smallest unit (no conversion).
  formatRaw: function(cents, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var amount = Number(cents || 0) / 100;
    var decimals = opts.decimals !== undefined ? opts.decimals : 2;
    var symbol = opts.symbol !== false ? this.symbol(currency) : '';
    return symbol + amount.toFixed(decimals);
  },

  // Parse user input back to micro-USD quota.
  parseToQuota: function(amount, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var rate = opts.exchangeRate || this.rate;
    var val = parseFloat(amount) || 0;
    if (currency === 'USD') {
      return Math.round(val * 1000000);
    }
    return Math.round((val / rate) * 1000000);
  }
};
```

同时 `formatQuota()` 已改为委托给 `Currency.formatShort()`，保证旧调用点行为一致。

**Step 2: 在 `layout.html` 统一注入**

```html
<script src="/static/js/app-common.js?v=6"></script>
<script>
  // Global user preferences (used by app-common.js Currency helpers)
  window.userCurrency = {{.Currency | json}};
  window.exchangeRate = {{.ExchangeRate | json}};
</script>
```

**Step 3: 各模板统一使用**

| 场景 | 旧代码 | 新代码 |
|------|--------|--------|
| 余额/金额显示 | `getCurrencySymbol() + formatPrice(v.balance)` | `Currency.formatShort(v.balance)` |
| 金额数值（无符号） | `formatPrice(v.amount)` | `Currency.formatValue(v.amount)` |
| 兑换码面值（已是目标币种） | `getCurrencySymbol() + formatPrice(v.amount, true)` | `Currency.formatRaw(v.amount)` |
| 订单金额 | `getCurrencySymbol() + formatAmount(v.amount)` | `Currency.format(v.amount)` |
| 币种符号 | `getCurrencySymbol()` / `currencySymbol()` | `Currency.symbol()` |
| 币种单位 | `currencyUnit()` | `Currency.unit()` |
| 金额 → quota | 手写 `* 1000000` / `/ exchangeRate` | `Currency.parseToQuota(amount)` |

**Step 4: 已清理重复 JS 函数的模板**

- `subscriptions.html`
- `my-subscriptions.html`
- `invoices.html`
- `plan-and-group.html`
- `usage-logs.html`
- `billing-details.html`（保留两个薄包装：`formatAmount` 因历史 5 位小数 USD 精度；`formatRechargeAmount` 因订单币种特殊逻辑，二者均委托给 `Currency`）

删除的重复定义：
- `getCurrencySymbol()` / `currencySymbol()`
- `formatPrice()` / `formatAmount()` / `formatCurrency()`
- `currencyUnit()`
- 各模板中重复的 `window.userCurrency` / `window.exchangeRate` 赋值

**Step 5: 后端 Go 函数**

保留 `formatCurrency` / `formatMoney` 作为服务端渲染的统一函数（已支持汇率参数）。模板中继续按需使用：
```go
{{formatCurrency .Amount $.Currency $.ExchangeRate}}
{{formatMoney .Amount $.Currency $.ExchangeRate}}
```

### 8.4 特殊场景处理

| 场景 | 处理方式 |
|------|---------|
| 兑换码面值（已是目标币种） | 使用 `Currency.formatRaw(cents)` |
| 订单金额（可能存储了下单时的币种） | 使用 `Currency.format(cents)` 并确保后端传 CNY 分 |
| 管理后台显示（不关心用户币种） | 使用 `formatMoney` 并传固定 CNY |

### 8.5 实施清单

- [x] 在 `app-common.js` 实现统一 `Currency` 模块（替代原独立 `currency.js` 方案）
- [x] 修改 `layout.html` 全局注入 Currency 配置
- [x] 修改 `subscriptions.html` 使用 `Currency`
- [x] 修改 `my-subscriptions.html` 使用 `Currency`
- [x] 修改 `invoices.html` 使用 `Currency`
- [x] 修改 `plan-and-group.html` 使用 `Currency`
- [x] 修改 `usage-logs.html` 使用 `Currency`
- [x] 修改 `billing-details.html` 使用 `Currency`
- [x] 清理各模板中重复的 JS 函数
- [x] 后端模板继续统一使用 `formatCurrency` / `formatMoney`（`internal/web/helpers.go` 已支持汇率参数）

---

## 九、已实施的修复（2026-05-12）

> 本节记录已实际完成的代码修改

### 9.1 核心原则

**存储永远是 CNY 分，输入和显示根据用户币种自动换算**

| 场景 | 逻辑 |
|------|------|
| 用户输入 50 USD | 存储 `50 × 汇率 × 100` CNY 分 |
| 用户输入 50 CNY | 存储 `50 × 100` CNY 分 |
| 显示给 USD 用户 | `CNY分 / 100 / 汇率` |
| 显示给 CNY 用户 | `CNY分 / 100` |

### 9.2 后端 Go 函数修复

**`internal/web/helpers.go` — `formatCurrency` 增加汇率参数**

```go
// 旧签名：formatCurrency(cents, currency) — 不做汇率转换，BUG
// 新签名：formatCurrency(cents, currency, exchangeRate ...float64) — 支持可选汇率参数
func formatCurrency(cents interface{}, currency string, exchangeRate ...float64) string {
    rate := 7.20
    if len(exchangeRate) > 0 && exchangeRate[0] > 0 {
        rate = exchangeRate[0]
    }
    if currency == "USD" {
        return fmt.Sprintf("$%.2f", val/100/rate)
    }
    return fmt.Sprintf("¥%.2f", val/100)
}
```

### 9.3 订单/充值创建修复

**`internal/web/actions.go` — HandleRecharge**

```go
// 转换为 CNY 分存储
amountCents := amount * 100
if currency == "USD" {
    amountCents = int(float64(amount) * exchangeRate * 100)
}
// INSERT ... amountCents, currency, exchangeRate
```

**`internal/routes/orders.go` — createOrder**

```go
// 转换为 CNY 分存储
amountCents := req.Amount * 100
if currency == "USD" {
    amountCents = int(float64(req.Amount) * exchangeRate * 100)
}
// INSERT ... amountCents, currency, exchangeRate
```

**`internal/routes/billing.go` — createRecharge**

```go
// 转换为 CNY 分存储
amountCents := req.Amount * 100
if currency == "USD" {
    amountCents = int64(float64(req.Amount) * exchangeRate * 100)
}
// INSERT ... amountCents
```

**`internal/routes/subscriptions.go` — purchasePlan**

```go
// 查询时处理 NULL 值
COALESCE(daily_limit, 0)  // 避免 Scan 失败
```

### 9.4 模板显示修复

**Go 模板 — 传递汇率参数**

| 模板 | 旧代码 | 新代码 |
|------|--------|--------|
| `dashboard.html` | `{{formatCurrency .Balance .Currency}}` | `{{formatCurrency .Balance .Currency $.ExchangeRate}}` |
| `admin-subscriptions.html` | `{{formatCurrency .Price $.Currency}}` | `{{formatCurrency .Price $.Currency $.ExchangeRate}}` |
| `admin-redemption.html` | `{{formatCurrency .Amount $.Currency}}` | `{{formatCurrency .Amount $.Currency $.ExchangeRate}}` |
| `billing/checkout.html` | `{{formatCurrency $order.Amount $order.Currency}}` | `{{formatCurrency $order.Amount $order.Currency $order.ExchangeRate}}` |

**JS 模板 — 加入汇率转换**

| 模板 | 旧代码 | 新代码 |
|------|--------|--------|
| `subscriptions.html` | `return (priceCents / 100).toFixed(2)` | USD 时 `/ 100 / exchangeRate` |
| `my-subscriptions.html` | `return (priceCents / 100).toFixed(2)` | USD 时 `/ 100 / exchangeRate` |
| `invoices.html` | `return (cents / 100).toFixed(2)` | USD 时 `/ 100 / exchangeRate` |
| `redemption.html` | `return (cents / 100).toFixed(2)` | USD 时 `/ 100 / exchangeRate` |

### 9.5 其他修复

| 文件 | 修复内容 |
|------|---------|
| `admin-groups.html` | 类型显示改用纯文字，移除 badge 样式 |
| `admin-subscriptions.html` | 创建/编辑后自动刷新页面 |
| `subscriptions.html` | 修复 `daily_limit` NULL 导致列表为空 |
| `admin-redemption.html` | 兑换码金额使用 `formatCurrency` 不做转换 |

### 9.6 套餐管理货币转换修复（2026-05-12）

**问题：管理员创建套餐/兑换码时，输入金额按管理员币种理解，但直接存入 DB 未转换为 CNY 分。导致 USD 用户输入 999 存为 999 分，显示时 `formatCurrency` 按 CNY 分处理 ÷汇率 变成 $1.47。**

**新增辅助函数 `internal/web/admin_handlers.go`：**

```go
// parsePriceToCNYCents 将输入价格按管理员币种转换为 CNY 分
// CNY: "999" → 99900 分
// USD: "999" → 999 × 7.20 × 100 = 719280 分
func parsePriceToCNYCents(priceStr string, currency string, exchangeRate float64) int {
    inputCents := parsePriceToCents(priceStr)
    if currency == "USD" && exchangeRate > 0 {
        return int(float64(inputCents) * exchangeRate)
    }
    return inputCents
}

// cnyCentsToDisplayYuan 将 CNY 分转为管理员币种的元（用于编辑表单回显）
func cnyCentsToDisplayYuan(cents int64, currency string, exchangeRate float64) float64 {
    if currency == "USD" && exchangeRate > 0 {
        return float64(cents) / 100.0 / exchangeRate
    }
    return float64(cents) / 100.0
}
```

**修复的 handler 列表：**

| Handler | 修复内容 |
|---------|---------|
| `HandleCreatePlan` | `parsePriceToCents` → `parsePriceToCNYCents`，根据管理员币种转换 |
| `HandleUpdatePlan` | 同上 |
| `HandleGetPlanDetail` | 编辑回显用 `cnyCentsToDisplayYuan` 转换 |
| `HandleCreateRedemptionCode` | 同 `parsePriceToCNYCents` |
| `HandleBatchCreateRedemptionCodes` | 同上 |
| `redemption.go createCodes` (API) | 新增 `getAdminCurrencyAndRate`，USD 时 `amount × rate × 100` |

### 9.7 套餐 Soft Delete（2026-05-12）

**问题：真实删除套餐违反 FK 约束（subscription_purchases.plan_id NOT NULL），且删除购买记录违反审计要求。**

**方案：Soft delete — 设 `status='inactive'` + `deleted_at=NOW()`，不删行。**

**数据库变更：**
```sql
ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE subscription_purchases ALTER COLUMN plan_id DROP NOT NULL;  -- 允许 NULL 以备将来
```

**所有查询加 `deleted_at IS NULL` 过滤：**

| 文件 | 查询 | 影响 |
|------|------|------|
| `subscriptions.go` | `listPublicPlans` | 防止购买已删除套餐 |
| `subscriptions.go` | `purchasePlan` | 防止购买已删除套餐 |
| `subscriptions.go` | `renewSubscription` | 防止续费已删除套餐 |
| `subscriptions.go` | `ProcessAutoRenewals` | JOIN 加 `deleted_at IS NULL` |
| `subscriptions.go` | `listPlans` (admin API) | 排除已删除 |
| `admin_handlers.go` | `HandleTogglePlan` | 不能操作已删除套餐 |
| `admin_handlers.go` | `HandleGetPlanDetail` | 不显示已删除套餐 |
| `admin_handlers.go` | `HandleUpdatePlan` | 不能编辑已删除套餐 |
| `admin_handlers.go` | `HandleCreateRedemptionCode` | 不能关联已删除套餐 |
| `data.go` | `GetAdminPlans` | 管理员表格显示已删除（半透明）+ 筛选 |

**管理后台 UI：**
- 状态筛选增加"已删除"选项
- 已删除套餐行半透明 + "(已删除)" 标签
- 已删除套餐不显示操作按钮
- 删除操作写入 `audit_logs`（含套餐及关联记录快照）

### 9.8 充值订单币种修复（2026-05-12）

**问题1：JS 发送 `amount * 100`（如 200→20000），后端又 `* 100`，双重转换。**
**问题2：后端从 DB 读 `preferred_currency`，但 DB 可能是 CNY 而 cookie 是 USD，导致金额被当成 RMB 存储。**
**问题3：订单列表 `formatAmount` 按 `order.currency` 判断转换方向，但 amount 始终是 CNY cents。**

**修复：**

| 位置 | 修改 |
|------|------|
| `billing.html` JS | 发送原始金额（去掉 `* 100`），传 `currency` 字段 |
| `orders.go createOrder` | 接受前端传入 `currency` 字段，优先于 DB 值 |
| `actions.go HandleRecharge` | 使用 `h.getCurrency(c)`（cookie→DB 优先级） |
| `orders.html formatAmount` | 简化：amount 始终是 CNY cents，USD 用户直接 `÷100÷汇率` |
| `orders.html editModal` | 回显按用户币种转换，保存时逆向转换 |
| `billing.html showOrderModal` | 按用户币种转换显示 |

### 9.9 Profile 余额显示移除（2026-05-12）

移除 `/profile` 页面的"余额: $xx.xx"显示，因余额和套餐是独立记账体系，余额对用户无实际参考意义。

### 9.10 术语统一（2026-05-12）

- 侧边栏 + 页面标题 "套餐中心" → "订阅中心"，与 admin dashboard 描述一致

### 9.11 已完成（2026-06-11）

- [x] 在 `app-common.js` 实现统一 `Currency` 模块
- [x] 在 `layout.html` 统一注入 Currency 配置
- [x] 清理各模板中重复的 JS 函数（`formatPrice`/`formatAmount`/`getCurrencySymbol`/`formatCurrency`/`currencySymbol`/`currencyUnit` 等）
- [x] 统一 `billing-details.html` 的 `formatAmount` 函数（已委托给 `Currency`）
