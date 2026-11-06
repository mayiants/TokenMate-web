# 模型能力元数据暴露方案

> **Status:** COMPLETE (Phase 1-2 implemented)  
> 解决下游客户端无法感知模型能力（上下文窗口、thinking、speed tier、并发限制等）的痛点。  
> 提出：2026-07-23 15:00 UTC  
> 完成：Phase 1 `/v1/models` 已上线并返回 capabilities/key_limits；Phase 2 capabilities 通过 sync 同步到 Proxy（normalizeCapabilities）；Phase 3（client SDK）无需额外工作，标准 OpenAI SDK 已兼容。

---

## 1. 背景与痛点

当前下游调用 WOODGIT 时面临以下问题：

1. **没有标准的 `/v1/models` 端点**：Proxy 的 `/v1` 路由组未注册 `GET /v1/models`，下游无法像调用 OpenAI 一样获取自己 key 可用的模型列表。
2. **上下文窗口不可知**：`service_models` 表没有统一字段描述上下文窗口；`capabilities` JSONB 字段存在，但未同步到 Proxy。
3. **并发能力不可知**：`concurrent_limit` 仅用于内部限流判断，未作为元数据返回给下游。
4. **Thinking / Speed / Reasoning effort 不可知**：数据库仅有 `reasoning_price` 和 `reasoning_intensity` 记录，没有模型是否支持 thinking、支持哪些 effort 级别、speed tier 等元数据。

---

## 2. 现状分析

### 2.1 Proxy 端点现状

`ai-proxy-go/internal/server/server.go:248-266` 当前 `/v1` 下仅注册：

```go
/v1/chat/completions
/v1/completions
/v1/embeddings
/v1/images/generations
/v1/audio/transcriptions
/v1/audio/translations
/v1/messages          // Claude 原生
/v1/responses         // OpenAI Responses
```

**缺少 `GET /v1/models`**。

### 2.2 模型元数据现状

| 字段/表 | 位置 | 现状 |
|---------|------|------|
| `service_models.capabilities` | Portal DB | 存在（JSONB），但基本是空的或仅用于 `buildModelNameMap` 的布尔标记 |
| `service_models.context_window` | Portal DB | **不存在** |
| `openrouter_models.context_window` | Portal DB | 存在，但与 `service_models` 分离 |
| `model_products.context_window` | Portal DB | 存在，但与 `service_models` 分离 |
| `user_groups.concurrent_limit` | Portal DB | 存在，仅内部限流使用 |
| `subscription_plans.concurrent_limit` | Portal DB | 存在，仅内部限流使用 |
| `user_subscriptions.concurrent_limit` | Portal DB | 存在，仅内部限流使用 |
| `service_models.reasoning_price` | Portal DB | 存在，仅计费使用 |

### 2.3 同步链路现状

- `/api/admin/models/sync`（`ai-portal-go/internal/routes/sync.go:438-496`）返回给 Proxy 的字段中**没有 `capabilities`**；
- Proxy 的 `ServiceModel` struct（`ai-proxy-go/pkg/models/registry.go:17-50`）中**没有 `Capabilities` 字段**；
- 因此即便 Portal DB 中写了能力数据，Proxy 也拿不到，更无法暴露给下游。

---

## 3. 目标

1. 在 Portal 层建立统一的模型能力描述规范；
2. 把能力元数据同步到 Proxy；
3. 在 Proxy 实现 `GET /v1/models`，按 key 权限过滤后返回模型列表及能力；
4. 把 key/订阅级别的并发限制作为元数据返回给下游；
5. 保持对 OpenAI `/v1/models` 格式的兼容，同时扩展 `capabilities` 字段。

---

## 4. 方案设计

### 4.1 Portal 层：统一 `capabilities` 结构

在 `service_models.capabilities` JSONB 中采用以下规范结构：

```json
{
  "context_window": 1048576,
  "max_output_tokens": 8192,
  "max_input_tokens": 1048576,
  "supports_thinking": true,
  "reasoning_efforts": ["low", "medium", "high"],
  "speed_tiers": ["default", "high"],
  "supports_vision": true,
  "supports_audio": false,
  "supports_tool_calls": true,
  "supports_json_mode": true,
  "supports_parallel_tool_calls": true,
  "supports_streaming": true,
  "supported_languages": ["en", "zh", "ja"],
  "knowledge_cutoff": "2025-04-01"
}
```

字段说明：

| 字段 | 类型 | 说明 |
|------|------|------|
| `context_window` | int | 模型最大上下文窗口（总 token 数） |
| `max_output_tokens` | int | 单次请求最大输出 token 数 |
| `max_input_tokens` | int | 单次请求最大输入 token 数 |
| `supports_thinking` | bool | 是否支持思维链/extended thinking |
| `reasoning_efforts` | string[] | 支持的 reasoning effort 级别，如 low/medium/high |
| `speed_tiers` | string[] | 支持的 speed tier，如 default/high/max |
| `supports_vision` | bool | 是否支持图片/视觉输入 |
| `supports_audio` | bool | 是否支持音频输入/输出 |
| `supports_tool_calls` | bool | 是否支持 function calling |
| `supports_json_mode` | bool | 是否支持 JSON mode / structured output |
| `supports_parallel_tool_calls` | bool | 是否支持并行 tool calls |
| `supports_streaming` | bool | 是否支持 SSE 流式输出 |
| `supported_languages` | string[] | 主要支持的语言代码（可选） |
| `knowledge_cutoff` | string | 知识截止日期（可选） |

### 4.2 Portal 层：新增 `context_window` 字段（可选增强）

为了便于查询和校验，建议在 `service_models` 中新增显式字段：

```sql
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS context_window INTEGER;
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS max_output_tokens INTEGER;
```

这两个字段与 `capabilities` 中的同名字段保持同步：
- Admin UI 填写时同时写入字段和 `capabilities`；
- 若仅存在字段，同步到 Proxy 时自动补入 `capabilities`。

### 4.3 Portal 层：修改 `/api/admin/models/sync`

在 `ai-portal-go/internal/routes/sync.go:438-496` 的同步接口中，增加 `capabilities` 字段：

```go
models = append(models, gin.H{
    "id":                  id,
    "provider":            provider,
    "productId":           productID,
    "apiModelName":        apiModelNameStr,
    "status":              status,
    "inputPrice":          inputPrice,
    "outputPrice":         outputPrice,
    "inputPriceCacheHit":  inputPriceCacheHit,
    "inputPriceCacheMiss": inputPriceCacheMiss,
    "reasoningPrice":      reasoningPrice,
    "aimodelsData":        aimodelsData,
    "showInStore":         showInStore,
    "capabilities":        capabilities, // 新增
})
```

### 4.4 Proxy 层：`ServiceModel` 增加 `Capabilities`

在 `ai-proxy-go/pkg/models/registry.go` 中：

```go
type ServiceModel struct {
    ID           string `json:"id"`
    Provider     string `json:"provider"`
    ProductID    string `json:"productId"`
    APIModelName string `json:"apiModelName"`
    Status       string `json:"status"`

    // Pricing ...

    Capabilities ModelCapabilities `json:"capabilities"` // 新增
}

type ModelCapabilities struct {
    ContextWindow             int      `json:"context_window"`
    MaxOutputTokens           int      `json:"max_output_tokens"`
    MaxInputTokens            int      `json:"max_input_tokens"`
    SupportsThinking          bool     `json:"supports_thinking"`
    ReasoningEfforts          []string `json:"reasoning_efforts"`
    SpeedTiers                []string `json:"speed_tiers"`
    SupportsVision            bool     `json:"supports_vision"`
    SupportsAudio             bool     `json:"supports_audio"`
    SupportsToolCalls         bool     `json:"supports_tool_calls"`
    SupportsJSONMode          bool     `json:"supports_json_mode"`
    SupportsParallelToolCalls bool     `json:"supports_parallel_tool_calls"`
    SupportsStreaming         bool     `json:"supports_streaming"`
}
```

### 4.5 Proxy 层：实现 `GET /v1/models`

在 `ai-proxy-go/internal/server/server.go` 注册：

```go
proxy.GET("/models", service.handleListModels)
```

实现要点：

1. 通过 auth middleware 获取当前 key 的 `allowed_models`；
2. 从 `ModelRegistry` 中过滤出 `status = active` 且在该 key 允许列表中的模型；
3. 返回 OpenAI 兼容格式，并在每个 model 对象中附加 `capabilities`：

```json
{
  "object": "list",
  "data": [
    {
      "id": "claude-sonnet-4-6",
      "object": "model",
      "created": 1735689600,
      "owned_by": "amazon",
      "capabilities": {
        "context_window": 1048576,
        "max_output_tokens": 8192,
        "supports_thinking": true,
        "reasoning_efforts": ["low", "medium", "high"],
        "speed_tiers": ["default", "high"],
        "supports_vision": true,
        "supports_tool_calls": true,
        "supports_json_mode": true,
        "supports_streaming": true
      }
    }
  ]
}
```

### 4.6 Key 级别并发限制暴露

在 `GET /v1/models` 响应的根对象中，或单独通过响应头返回：

**方案 A（推荐）：在 `/v1/models` 根对象中返回 key limits**

```json
{
  "object": "list",
  "data": [...],
  "key_limits": {
    "concurrent": 5,
    "rpm": 60,
    "tpm": 100000
  }
}
```

> 注：OpenAI 官方 `/v1/models` 不返回 `key_limits`，但这是扩展字段，不会破坏兼容。

**方案 B：通过响应头返回**

```http
X-RateLimit-Concurrent-Limit: 5
X-RateLimit-Limit-Requests: 60
X-RateLimit-Limit-Tokens: 100000
```

建议同时实现方案 A（主数据）和方案 B（便于中间件/客户端统一读取）。

---

## 5. 实施步骤

### Phase 1：最小可用（MVP）

目标：让下游能拿到模型列表和基础上下文窗口。

1. Portal DB：为 `service_models` 补充 `context_window` 和 `capabilities` 数据；
2. Portal `/api/admin/models/sync`：增加 `capabilities` 字段返回；
3. Proxy `ServiceModel`：增加 `Capabilities` 字段；
4. Proxy：实现 `GET /v1/models`，仅返回 `id`、`object`、`created`、`owned_by` + `capabilities.context_window`。

### Phase 2：完整能力暴露

1. Admin UI：在模型管理页面增加能力编辑表单；
2. Portal：把 `capabilities` 中的所有字段都同步到 Proxy；
3. Proxy `/v1/models`：返回完整 `capabilities`；
4. Proxy：在响应中附加 `key_limits` 并发/限流信息。

### Phase 2.5：自动预填 capabilities（减少人工输入）

为了降低 admin 手动维护 `capabilities` 的错误率和遗漏，在 Phase 2 的 UI 编辑能力基础上，增加从上游数据源自动预填机制：

1. **OpenRouter 候选表同步**
   - `openrouter_models` 表已扩展 `max_output_tokens INTEGER` 和 `capabilities JSONB`。
   - 调用 `GET https://openrouter.ai/api/v1/models` 时，解析 `architecture`（modalities）、`supported_parameters`（tools / reasoning / json_mode 等）、`reasoning`、`top_provider.max_completion_tokens`。
   - 数据写入 `openrouter_models`，不直接覆盖 `service_models`。
   - 新增后台定时任务，每 6 小时同步一次 OpenRouter 候选表（advisory-lock 保护，避免多实例重复执行）。

2. **从候选表预填到 service_models**
   - 在 `addProviderModelsToService` 把上游模型加入 `service_models` 时，按以下顺序匹配 OpenRouter 候选数据：
     - 直接匹配 `provider/api_model_name` 或 `provider/product_id`；
     - 对多段 `product_id`（如 `Pro/moonshotai/Kimi-K2.6`）取最后一段再匹配；
     - 归一化名称模糊匹配（忽略大小写、分隔符、点号）。
   - 命中后预填 `context_window`、`max_output_tokens`、`capabilities`。

3. **max_output_tokens 兜底策略**
   - 优先使用 OpenRouter `top_provider.max_completion_tokens`；
   - 缺失时默认 `context_window / 8`，admin 可在 UI 中修正。

4. **Bedrock 从 ListFoundationModels 预填**
   - `aws.ValidateBedrockKeyWithCapabilities` 解析 `ListFoundationModels` 返回的 `inputModalities`、`outputModalities`、`responseStreamingSupported`。
   - `syncBedrockModels` 为 Bedrock Runtime 模型生成 `capabilities`：
     - `supports_vision`：input/output modalities 包含 `IMAGE`；
     - `supports_audio`：input/output modalities 包含 `AUDIO`；
     - `supports_streaming`：`responseStreamingSupported = true`；
     - `supports_text`：output modalities 包含 `TEXT`。
   - 为避免覆盖 admin 已手动编辑的内容，冲突更新时使用 `COALESCE(NULLIF(service_models.capabilities, '{}'::jsonb), EXCLUDED.capabilities)`。

5. **Admin 复核**
   - Admin UI 在 capabilities 编辑区标注“auto”来源提示；
   - 自动预填仅作为初始值，admin 可在启用模型前 review 并修改。

> 已知风险：OpenRouter 的模型命名（如 `kimi-k2-5`）与我们的 `provider+product_id/api_model_name`（如 `kimi-k2.5`）可能不完全一致，需要通过实测不断调优模糊匹配规则。

### Phase 3：客户端适配

1. ✅ 更新 Portal 前端模型卡片，显示 context window、thinking、vision 等标签；
2. ✅ 在 API Keys 页面提示“该 key 可用模型及能力”；
3. ✅ 更新错误码文档，说明当请求超出能力范围时（如超过 context window）返回的 `code`。

---

## 6. 兼容性考虑

1. **OpenAI 兼容**：`/v1/models` 返回的字段与 OpenAI 一致，`capabilities` 是扩展字段，不影响标准客户端；
2. **Anthropic 兼容**：Anthropic 客户端通常不走 `/v1/models`，但如需要可在 `/v1/models` 中返回 `product_id` 别名供内部使用；
3. **旧 Proxy 兼容**：增加 `Capabilities` 字段后，旧版本 Proxy 忽略该字段即可；
4. **空能力降级**：若 `capabilities` 为空，Proxy 返回时省略该字段或填默认值，避免客户端误判。

---

## 7. 相关文件

- `ai-portal-go/internal/db/schema.go` — `service_models` 表结构
- `ai-portal-go/internal/routes/sync.go` — `/api/admin/models/sync`
- `ai-portal-go/internal/routes/admin_models.go` — 模型管理接口
- `ai-proxy-go/pkg/models/registry.go` — Proxy 模型注册表
- `ai-proxy-go/internal/server/server.go` — 路由注册
- `ai-proxy-go/internal/server/handlers_chat.go` — 主处理逻辑（参考 allowed_models 过滤）
- `ai-proxy-go/internal/ratelimit/ratelimit.go` — 并发/限流逻辑

---

## 8. 模型可用性与验证机制（Phase 2/3 后待决策）

### 8.1 当前验证机制

| 验证级别 | 机制 | 触发方式 | 周期 | 结果存储 |
|---------|------|---------|------|---------|
| **Verify（上游 key）** | `ValidateUpstreamKeysBackground` | 后台定时任务 | 每 6 小时 | `upstream_keys.validation_status` |
| **Validate（模型 API 名）** | `ModelValidator.ValidateModel` | Admin 手动触发 | 无 | `service_models.validation_status` |
| **Lifecycle cleanup** | `CleanupAllInvalidModels` | 后台定时任务 | 每天 | 清理 `user_groups.models` / `api_keys.selected_models` |

> 注：`CleanupAllInvalidModels` 仅移除 `eol/discontinued/ignored` 模型，不验证模型是否真正可调用。

### 8.2 风险

上线目标为 `/v1/models` 返回：

```
allowed_models ∩ active_models ∩ has_upstream_key
```

但目前 `service_models.validation_status` 是手动维护的。如果 admin 未运行 validate 就把模型加入 group，Proxy 无法判断该模型是否真实可用，可能导致 `/v1/models` 列出“理论上允许、实际调用失败”的模型。

### 8.3 候选方案

**方案 A：复用现有 Verify 周期做轻量探测（推荐）**

在现有的 6 小时 `ValidateUpstreamKeysBackground` 周期中，对每个 `validation_status != 'invalid'` 的 active model 做一次轻量可用性探测（例如极小请求或 provider-specific health check），把结果写回 `service_models.validation_status` 或新建 `service_model_availability` 表。

优点：
- 不新增定时器，复用现有 advisory-lock 保护的 background job；
- 与上游 key 验证天然联动，key 失效时对应模型自动下线。

**方案 B：返回可用性元数据**

`/v1/models` 不追求完美可用性，而是给每个 model 附加 `availability` 字段：

```json
{
  "id": "claude-sonnet-4-6",
  "capabilities": {...},
  "availability": {
    "validated": true,
    "has_upstream_key": true,
    "last_checked_at": "2026-07-23T16:00:00Z"
  }
}
```

客户端自行优先选择高可用模型，未验证模型仍可被 direct request 尝试。

**方案 C：保守过滤**

`/v1/models` 只返回 `validation_status = 'valid'` 的模型；未验证模型不进入列表，但 direct request 仍允许。这能倒逼 admin 跑 validate，但可能让新加入的模型暂时不可见。

### 8.4 决策建议

- **Phase 1/2/3 期间**：保持 `/v1/models` 按 `selected_models` / `current_model` 返回（当前已实现），不阻塞能力暴露。
- **上线前再评估**：在 Phase 2/3 完成后，根据 admin 实际 validate 覆盖率和运维成本，决定采用方案 A（自动探测）还是方案 C（保守过滤），或两者结合。

---

## 9. 参考

- [OpenAI Models API](https://platform.openai.com/docs/api-reference/models)
- [Anthropic Model List](https://docs.anthropic.com/en/api/models-list)
- `ai-portal-go/docs/downstream-error-code-spec-202607231431.md` — 下游错误码规范
