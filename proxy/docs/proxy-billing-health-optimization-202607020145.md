# Proxy 计费与健康管理优化方案

**日期：** 2026-07-02  
**状态：** 部分已实施（2026-07-18 复核更新）  
**影响范围：** ai-proxy-go 的 key pool 管理、计费流程、模型路由

---

## 背景

E2E 测试中暴露三个系统性问题：

1. **401 误杀**：accessapi 对不支持的模型返回 401 "无效的令牌"，proxy 将其视为 key 无效并 inactive 整个 key，导致所有模型不可用
2. **模型列表虚报**：上行 key 的 `/v1/models` 返回 142 个模型，但部分实际不可用，请求后触发错误和 cooldown
3. **Timeout 未计费**：thinking 等慢模型超时后 proxy 没有 usage 数据，无法向用户收费，但上游 accessapi 已扣费

---

## 一、细分 401 消息判断（Key Health 优化）

### 现状

```go
// pkg/keypool/manager.go:1773
if httpStatus == 401 || httpStatus == 403 ||
    strings.Contains(errorType, "authentication") ||
    strings.Contains(errorType, "invalid") {
    key.Status = "inactive"  // 立即永久禁用
}
```

所有 401/403 一刀切 → key inactive → 24h 后 purge。

### 行业标准（来自 OpenAI/Anthropic/Google 官方文档）

| 状态码 | OpenAI 含义 | Anthropic 含义 | 正确处理 |
|--------|------------|---------------|---------|
| **401** | Key 无效/过期/撤销 | Key 有问题/AWS 凭证错误 | **通常**=key 无效 |
| **403** | 地区不支持/IP 白名单 | Key 无权访问该资源 | 可能是模型权限而非 key 无效 |
| **400** | 请求格式/参数错误 | invalid_request_error | 请求级错误 |
| **404** | 资源不存在 | not_found_error（模型不存在） | 模型级错误 |

**关键洞察：** OpenAI/Anthropic 原生 API 中 401 **确实**意味着 key 无效。但通过中间网关（如 accessapi）时，网关可能对"模型不可用"也返回 401，这是**网关的 bug**。

### 修改方案

```go
func (m *Manager) ReportKeyFailure(provider, keyID string, httpStatus int, errorCode, errorType, errorMsg string) {
    // ...
    
    // 401/403: 需要细分判断
    if httpStatus == 401 || httpStatus == 403 {
        if m.isDefinitelyKeyInvalid(httpStatus, errorCode, errorType, errorMsg) {
            // 确定 key 无效 → inactive
            key.Status = "inactive"
        } else {
            // 可能是模型级/权限级错误 → 仅记录 failure，不 inactive
            key.Failures++
            m.logger.Warn("Ambiguous auth error, treating as model-level failure",
                zap.Int("status", httpStatus),
                zap.String("error", errorMsg),
            )
        }
        return
    }
}

// isDefinitelyKeyInvalid 判断 401/403 是否确实代表 key 无效
func (m *Manager) isDefinitelyKeyInvalid(status int, code, errType, msg string) bool {
    msgLower := strings.ToLower(msg)
    
    // 明确的 key 无效信号
    keyInvalidSignals := []string{
        "invalid api key",
        "incorrect api key",
        "api key not found",
        "key has been revoked",
        "key has been deactivated",
        "authentication_error",
        "invalid x-api-key",
        "expired",
    }
    for _, signal := range keyInvalidSignals {
        if strings.Contains(msgLower, signal) {
            return true
        }
    }
    
    // 明确的 model/permission 级错误信号 → 非 key 无效
    modelSignals := []string{
        "model",
        "not found",
        "not supported",
        "not available",
        "permission",
        "not permitted",
        "not allowed",
        "access denied for model",
    }
    for _, signal := range modelSignals {
        if strings.Contains(msgLower, signal) {
            return false
        }
    }
    
    // 默认：401 视为 key 无效（保守策略），403 视为模型级
    return status == 401
}
```

### 测试用例

| 上游响应 | 判断结果 | 处理 |
|---------|---------|------|
| `401 "Incorrect API key provided"` | key 无效 | inactive |
| `401 "无效的令牌 (model babbage-002)"` | 模型级错误 | failure +1 |
| `401 "authentication_error"` | key 无效 | inactive |
| `403 "model not available in your plan"` | 模型级 | failure +1 |
| `403 "Country not supported"` | key 级 | inactive |

---

## 二、Model-Level Health Tracking

### 现状

当前只有 **key 级别**的健康追踪（failures → cooldown → inactive）。一个模型报错会影响整个 key 的所有模型。

### 设计方案

引入 `ModelHealth` 结构，在 key pool manager 中追踪每个 key+model 组合的健康状态：

```go
// pkg/keypool/model_health.go

type ModelHealth struct {
    mu      sync.RWMutex
    entries map[string]*ModelHealthEntry // key: "{keyID}:{model}"
}

type ModelHealthEntry struct {
    Failures     int
    LastFailure  time.Time
    LastSuccess  time.Time
    Disabled     bool       // 模型级禁用
    DisabledAt   time.Time
    RecoverAt    time.Time  // 自动恢复时间
    LastError    string
}

const (
    modelFailureThreshold = 3         // 连续 3 次失败 → 禁用该模型
    modelRecoveryInterval = 24 * time.Hour // 24h 后自动恢复（重新探测）
    modelProbeInterval    = 6 * time.Hour  // 每 6h 探测被禁用的模型
)
```

### 路由逻辑修改

```go
// resolveProviderAndKey 中增加模型级过滤
func (m *Manager) IsModelAvailable(keyID, model string) bool {
    entry := m.modelHealth.Get(keyID, model)
    if entry == nil {
        return true // 无记录 = 可用
    }
    if entry.Disabled && time.Now().Before(entry.RecoverAt) {
        return false
    }
    // 超过恢复时间 → 重新启用（允许探测）
    if entry.Disabled && time.Now().After(entry.RecoverAt) {
        entry.Disabled = false
        entry.Failures = 0
    }
    return true
}
```

### 触发条件

| 上游响应 | 动作 |
|---------|------|
| 200 OK | `modelHealth.RecordSuccess(keyID, model)` → 清除 failures |
| 400 "model not found/supported" | `modelHealth.RecordFailure(keyID, model)` |
| 401 判定为模型级 | `modelHealth.RecordFailure(keyID, model)` |
| 404 | `modelHealth.RecordFailure(keyID, model)` |
| 429 / 5xx / timeout | **不计入** model health（是暂时性问题） |

当 `failures >= modelFailureThreshold` 时：
- 禁用该模型（`Disabled = true`）
- 设置 `RecoverAt = now + 24h`
- 从 key 的路由决策中排除（但 key 本身保持 active）

### 管理 API

```
GET  /api/admin/key-pool/model-health        # 查看所有模型级健康状态
POST /api/admin/key-pool/model-health/reset   # 重置某模型的健康状态
GET  /api/admin/key-pool/model-health/probe   # 手动探测被禁用的模型
```

---

## 三、预扣费 + 修正计费

### 现状问题

```
用户请求 → proxy 转发到上游 → [timeout/error] → 无 usage 数据 → 无法计费
                                                    ↓
                                        但上游已消耗 input tokens 并扣费
```

结果：上游向我们收费，但我们没向用户收费 → **平台亏损**。

### 计费单位说明

Portal 内部使用 **micro-USD（微美元）** 作为 quota 单位：

```
1 quota unit = $0.000001 (百万分之一美元)
$1.00 = 1,000,000 quota units
$0.01 = 10,000 quota units
```

| 概念 | 值 | 换算 |
|------|-----|------|
| 1 micro-USD | 1 quota unit | $0.000001 |
| 1 cent | 10,000 quota units | $0.01 |
| 1 dollar | 1,000,000 quota units | $1.00 |

**admin API 创建用户的 balance 字段**直接是 micro-USD 整数值。例如：
- `balance: 10000000` = $10.00
- `balance: 1000000` = $1.00
- `balance: 10000` = $0.01

**service_models 表的价格字段**（`input_price`, `output_price`）单位是 **美元/百万 tokens**（$/M）：
- `input_price: 3.0` 表示 $3.00 per million input tokens
- `output_price: 15.0` 表示 $15.00 per million output tokens

**计费公式：**
```
cost_micro_usd = (input_tokens / 1,000,000) × input_price × 1,000,000
               + (output_tokens / 1,000,000) × output_price × 1,000,000

简化：
cost_micro_usd = input_tokens × input_price + output_tokens × output_price
```

例：100 input tokens × $3/M + 20 output tokens × $15/M
= 100 × 3 + 20 × 15 = 300 + 300 = 600 micro-USD = $0.0006

### 预扣费方案

```
┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐
│ 收到请求  │ ──→ │ 预扣 input │ ──→ │ 转发上游  │ ──→ │  结算调整  │
│          │     │ token 费用 │     │          │     │          │
└──────────┘     └──────────┘     └──────────┘     └──────────┘
                       │                                  │
                       │ 预扣额 = estimated_input ×       │ 成功: 按实际 usage 调整
                       │         input_price              │ 失败: 保留 input 预扣
                       │                                  │ 超时: 保留 input 预扣
                       ▼                                  ▼
               balance -= pre_deduct              balance += (pre_deduct - actual_cost)
```

### 实现步骤

#### 1. Proxy 端：估算 input tokens 并发送预扣请求

```go
// internal/server/handlers_chat.go

func (s *HandlerService) handleChatCompletions(c *gin.Context) {
    // ... parse request ...
    
    // Phase: Pre-consume (estimate input and deduct)
    estimatedInputTokens := 0
    if s.handlerDeps.TokenCounter != nil {
        estimatedInputTokens = s.handlerDeps.TokenCounter.EstimateMessages(req.Messages, req.Model)
    }
    
    if s.cfg.Billing.PreConsumeEnabled && estimatedInputTokens > 0 {
        preConsumeResult := s.handlerDeps.Billing.PreConsume(ctx, PreConsumeRequest{
            UserID:       userID,
            KeyHash:      keyHash,
            Model:        logicalModel,
            Provider:     provider,
            InputTokens:  estimatedInputTokens,
            RequestID:    traceID,
        })
        if preConsumeResult.Rejected {
            // 余额不足
            c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{
                "message": "Insufficient balance",
                "type":    "billing_error",
            }})
            return
        }
        c.Set("pre_consume_amount", preConsumeResult.Amount)
    }
    
    // ... forward to upstream ...
    
    // Phase: Settle
    if preConsumeAmount > 0 {
        s.handlerDeps.Billing.Settle(ctx, SettleRequest{
            RequestID:    traceID,
            PreConsumed:  preConsumeAmount,
            ActualUsage:  usage,  // nil if timeout/error
            Success:      resp.StatusCode == 200,
        })
    }
}
```

#### 2. Portal 端：预扣费 API

```go
// POST /api/webhook/pre-consume
type PreConsumeRequest struct {
    UserID      string `json:"user_id"`
    KeyHash     string `json:"key_hash"`
    Model       string `json:"model"`
    Provider    string `json:"provider"`
    InputTokens int    `json:"input_tokens"`
    RequestID   string `json:"request_id"`
}

type PreConsumeResponse struct {
    Rejected    bool  `json:"rejected"`
    Amount      int64 `json:"amount"`       // 预扣金额 (micro-USD)
    Remaining   int64 `json:"remaining"`    // 剩余余额
}

// POST /api/webhook/settle
type SettleRequest struct {
    RequestID   string     `json:"request_id"`
    PreConsumed int64      `json:"pre_consumed"` // 预扣金额
    ActualUsage *Usage     `json:"actual_usage"` // 实际 usage，nil=timeout/error
    Success     bool       `json:"success"`
}
```

#### 3. 结算逻辑

| 场景 | 处理 |
|------|------|
| **成功** (有完整 usage) | `actual_cost = calc(usage)`; `refund = pre_consumed - actual_cost`; 退还多扣 |
| **超时** (无 usage) | 保留 `pre_consumed`（因为上游已消耗 input） |
| **上游 4xx** (请求被拒) | 退还 `pre_consumed`（上游没有消耗） |
| **上游 5xx** (服务端错误) | 退还 `pre_consumed`（不确定是否消耗，偏向用户） |

**注意：** 对于 thinking 模型超时，上游实际可能已消耗大量推理 tokens。保守做法是保留 input 预扣。更精确的做法需要后续从上游对账数据中补收差额。

### 配置项

```yaml
billing:
  pre_consume_enabled: true          # 启用预扣费
  pre_consume_margin: 1.2            # 预扣额 = 估算 × 1.2（20% 安全边际）
  settle_timeout_refund: false       # timeout 时是否退还（false=保留）
  settle_5xx_refund: true            # 上游 5xx 时退还
  settle_4xx_refund: true            # 上游 4xx 时退还
```

---

## 四、模型价格维护

### 当前问题

142 个 service models 中有 **111 个的 input_price 为 NULL**（只设了 output_price）。

当 `input_price_cache_miss = 0`（COALESCE 默认值）时，input tokens 的计费为 0，导致只收 output 费用。

### 建议

1. **为所有模型补全 input_price**：参考各家官方定价
2. **Portal UI 批量编辑**：在 admin/models 页面支持批量导入价格表
3. **价格同步**：定期从 aimodels.dev 或官方 API 抓取最新价格

### 参考定价（$/M tokens，2026 年 7 月）

| 模型 | Input | Output | 备注 |
|------|-------|--------|------|
| gpt-5.5 | 2.50 | 10.00 | |
| gpt-5.4-pro | 10.00 | 30.00 | |
| gpt-5.4-mini | 0.40 | 1.60 | |
| gpt-5.4-nano | 0.15 | 0.60 | |
| gpt-4o | 2.50 | 10.00 | |
| gpt-4o-mini | 0.15 | 0.60 | |
| claude-opus-4-6 | 15.00 | 75.00 | |
| claude-sonnet-4-5 | 3.00 | 15.00 | |
| claude-haiku-4-5 | 0.80 | 4.00 | |
| gemini-2.5-pro | 1.25 | 10.00 | |
| gemini-2.5-flash | 0.15 | 0.60 | |
| gemini-3-pro-preview | 2.50 | 15.00 | |
| o3 | 10.00 | 40.00 | reasoning 另计 |
| o4-mini | 1.10 | 4.40 | reasoning 另计 |

---

## 六、Key 生命周期与 Purge 条件

### 状态流转图

```
                  ┌────────────────────────────────────────────────────┐
                  │                                                    │
                  ▼                                                    │
┌──────────┐   failure×3   ┌──────────┐   60s elapsed   ┌──────────┐ │
│  active  │ ────────────→ │ cooldown │ ──────────────→ │  active  │─┘
└──────────┘               └──────────┘                 └──────────┘
      │
      │ isDefinitelyKeyInvalid() = true
      │ OR 10 failures in 10 min
      ▼
┌──────────┐   24h elapsed   ┌──────────┐
│ inactive │ ──────────────→ │  purged  │  ← 从 pool 中彻底移除
└──────────┘                 └──────────┘
```

### Purge 触发条件（PR1 + PR2 之后）

一个 key 被从 pool 中彻底删除（purged）的**唯一路径**是：先标记为 `inactive`，然后 HealthCheck（每 30s）检测到 inactive 超过 24 小时。

#### 什么会导致 key → inactive？

| 条件 | 处理 | 会 purge？ |
|------|------|-----------|
| 401/403 + **明确 key 无效信号**（"incorrect api key", "revoked", "expired"） | 立即 inactive | ✅ 24h 后 purge |
| 401/403 + **模型级信号**（"无效的令牌", "model not found", "not supported"） | 仅 failure +1 | ❌ 不会 inactive |
| **10 分钟内累计 10 次 failure**（任何非 quota 错误） | 标记 inactive | ✅ 24h 后 purge |
| **模型连续失败 3 次** | 仅禁用该模型（Model Health） | ❌ key 不受影响 |
| **Timeout / 5xx** | failure +1 | ⚠️ 累计到 10 次/10min 才会 inactive |
| **429 / quota exceeded** | cooldown 60s 或次日恢复 | ❌ 不计入 failure |

#### 什么**不会**触发 purge？

- 模型不支持（401 含 "model" 关键词）→ model health 禁用该模型，key 保持 active
- 偶发 timeout（<10次/10min）→ cooldown 后自动恢复
- Rate limit 429 → cooldown 后恢复，不计入 failure

### Model Health vs Key Health 对比

| 维度 | Model Health (PR2) | Key Health (原有) |
|------|-------------------|------------------|
| 粒度 | key + model | key |
| 禁用条件 | 3 次连续模型级失败 | 10 次/10min 或明确 key 无效 |
| 禁用影响 | 仅该模型不再路由到该 key | 整个 key 所有模型不可用 |
| 恢复方式 | 24h 自动恢复或成功请求恢复 | 不恢复（purge） |
| 适用场景 | 上游不支持某模型、参数错误 | Key 被撤销/禁用 |

### 测试验证要点

1. **模型级 401 不应 purge key：** 发送不支持的模型 3 次，验证该模型被禁用但 key 仍 active，其他模型正常路由
2. **明确 key 无效应 inactive：** 模拟 "Incorrect API key" 响应，验证 key 立即 inactive
3. **cooldown 恢复：** 3 次 failure 后验证 60s cooldown，60s 后验证自动恢复
4. **model health 恢复：** 模型禁用后发送成功请求，验证立即恢复

---

## 七、实施计划

| 阶段 | 内容 | 预计工时 | 优先级 | 状态（2026-07-18 复核） |
|------|------|---------|--------|------------------------|
| P0-A | 细分 401 判断逻辑 | 2h | 🔴 | ✅ 已实施（`pkg/keypool/health.go:isDefinitelyKeyInvalid` + `pkg/keypool/key_failure_test.go`） |
| P0-B | 补全所有模型 input_price | 1h | 🔴 | ⏳ 待实施（`service_models` 60/60 行的 `input_price` 仍为 NULL/0；计费实际走 `model_pricing_rules`，但 proxy 本地统计展示仍缺 input price） |
| P1-A | Model-level health tracking | 4h | 🟡 | ✅ 已实施（`pkg/keypool/model_health.go` + `model_health_test.go`；路由已按模型健康过滤） |
| P1-B | 启用 pre-consume 计费 | 4h | 🟡 | ✅ 已实施（`pkg/billing/preconsume.go`；freeze + charge-on-timeout 逻辑已落地） |
| P1-C | settle 结算 API | 3h | 🟡 | ✅ 已实施（`SettleRequest`/`SettleResponse` 结构 + portal webhook settle 路径） |
| P2 | 模型自动探测 & 恢复 | 3h | 🟢 | ✅ 已实施（`ModelHealth` 24h 自动恢复 + 成功请求即时恢复） |
| P2 | 管理 API (model-health) | 2h | 🟢 | ⏳ 待实施（proxy 内部已实现 `ModelHealth`，但无 `/api/admin/key-pool/model-health` 等管理端点暴露） |

**交付状态**：
- 核心 401 细分、模型健康、预扣费/结算已落地。
- 剩余：**P0-B 补全 `service_models.input_price`**（影响 proxy 本地成本统计精度）和 **管理 API 暴露**（运营视角查看/重置模型健康状态）。

**历史交付节奏**：
1. PR1: P0-A + P0-B（P0-A 已交付，P0-B 待补）
2. PR2: P1-A（已交付）
3. PR3: P1-B + P1-C（已交付）

---

## 八、Usage Records 增强字段（TTFT / GeoIP / Reasoning Intensity）

**状态：** 已实施（2026-07-18）

为让 Portal 的 usage logs、对账、运营分析能拿到更细粒度的请求数据，Proxy 在每次请求成功后向 Portal 上报的 `UsageRecord` 中增加了三个字段：

| 字段 | 含义 | 来源 | 适用场景 |
|------|------|------|---------|
| `ttftMs` | Time To First Token，首 token 延迟（毫秒） | streaming 请求中首个 content-bearing chunk 到达的时间 | 流式请求性能分析 |
| `clientGeo` | 客户端 IP 的 ISO 国家码（如 `CN`、`US`） | MaxMind GeoLite2 Country MMDB | 按地区统计调用 / 合规 / 风控 |
| `reasoningIntensity` | 推理强度（如 `low` / `medium` / `high`） | OpenAI `reasoning_effort` 或 Anthropic `thinking` 配置 | 分析 reasoning 成本分布 |

### 技术实现

- `pkg/geoip/geoip.go`：基于 `github.com/oschwald/geoip2-golang`，从环境变量 `GEOIP_DB_PATH` 加载 MMDB。
- `internal/server/handlers_common.go`：`recordUsageAsyncFromContext` 统一读取 `clientIP`、`reasoning_intensity`、`first_token_latency_ms` 并写入 `UsageRecord`。
- `internal/server/handlers_chat.go` / `handlers_responses.go`：在 streaming handler 中修正了 `first_token_latency_ms` 的写入时机，确保 TTFT 在 usage 记录生成前已写入 gin context。
- Portal 侧：`usage_records` 表新增 `ttft_ms`、`client_geo`、`reasoning_intensity` 三列，webhook 三个写入路径均解析并落库。

### GeoIP MMDB 使用说明

Proxy 通过 `GEOIP_DB_PATH` 环境变量定位 MMDB 文件：

```bash
# docker-compose.proxy.yml 中已默认配置
GEOIP_DB_PATH: "/data/geoip/GeoLite2-Country.mmdb"
```

本地/部署目录结构：

```
ai-proxy-go/
├── docker-compose.proxy.yml
└── data/
    └── geoip/
        └── GeoLite2-Country.mmdb
```

部署时只需把 `.mmdb` 文件放到 `data/geoip/` 目录下即可，无需重建镜像（已通过 volume 挂载进容器）。如果未来需要更新 GeoIP 数据，直接替换该文件，新请求会立即生效（Proxy 在启动时一次性加载 Reader，但文件句柄支持热替换；稳妥起见可重启容器）。

**获取 MMDB 文件：**

- 推荐：从 MaxMind 官网下载 GeoLite2-Country（需免费账号和 license key）。
- 快速测试：使用公开镜像，例如
  ```bash
  mkdir -p data/geoip
  curl -L -o data/geoip/GeoLite2-Country.mmdb \
    "https://github.com/P3TERX/GeoLite.mmdb/raw/download/GeoLite2-Country.mmdb"
  ```

> 注意：GeoIP 数据是可选的。如果 `GEOIP_DB_PATH` 未设置或文件不存在，`clientGeo` 会留空字符串，不会导致请求失败。

### 验证方式

在 Portal 侧执行：

```sql
SELECT trace_id, model, client_ip, client_geo, ttft_ms, reasoning_intensity, latency_ms
FROM usage_records
ORDER BY created_at DESC
LIMIT 10;
```

对流式请求，`ttft_ms` 应大于 0；非流式请求 `ttft_ms` 为 0。`clientGeo` 取决于 MMDB 是否命中该 IP。

---

*文档作者：Kiro*  
*审核：待定*
