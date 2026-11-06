# AWS Bedrock Runtime vs Mantle: 验证、路由与模型映射

> 日期：2026-07-02  
> 状态：实施中  
> 关联文件：`internal/aws/sigv4.go`, `internal/aws/bedrock.go`, `internal/routes/admin_common.go`

## 问题背景

AWS Bedrock 平台有两个不同的 API 端点，服务不同类型的模型：

| 维度 | Bedrock Runtime | Bedrock Mantle |
|------|----------------|----------------|
| 端点 | `bedrock-runtime.{region}.amazonaws.com` | `bedrock-mantle.{region}.api.aws` |
| 协议 | Converse API (AWS 专有) | OpenAI 兼容 `/v1/chat/completions` |
| 认证 | SigV4 签名 | Bearer Token (Bedrock API Key) |
| 模型 | Anthropic, Amazon Nova, Meta, Mistral, Cohere, Stability, AI21 | OpenAI GPT-5, DeepSeek, Qwen, Nvidia, xAI, MiniMax, etc. |
| Provider ID | `amazon-{region}` (如 `amazon-us-east-1`) | `amazon-mantle-{region}` (如 `amazon-mantle-us-east-1`) |

## 发现的问题

### 1. 模型列表混淆 (Critical)

**症状**：Bedrock Runtime key 的 `upstream_keys.models` 字段包含了 Mantle 模型。

**根因**：当验证 Bedrock Runtime key 时，调用 `ListFoundationModels` + `ListInferenceProfiles` API，这两个 AWS API 返回**账号中所有可用模型**，不区分是通过 Runtime 还是 Mantle 访问的。

**影响**：
- Proxy 的 `GetAllKeysForModel("deepseek.v3.2")` 会同时匹配 Bedrock Runtime key 和 Mantle key
- 如果路由选择了 Runtime key，会用 Converse API 去调 Mantle 模型 → 400 "model identifier invalid"
- 186 个 Runtime 模型中有 31 个实际是 Mantle 模型

### 2. 模型名称映射错误 (High)

**症状**：`claude-sonnet-4-6` 通过 Bedrock Runtime 调用时返回 "model identifier invalid"。

**根因**：
- 2024 年后 Bedrock 不再允许直接使用裸 model ID (`anthropic.claude-sonnet-4-6`)
- 必须使用 Inference Profile ID (`global.anthropic.claude-sonnet-4-6` 或 `us.anthropic.claude-sonnet-4-6`)
- 原硬编码映射使用了过时的带日期后缀的 ID (`anthropic.claude-sonnet-4-6-20251022-v1:0`)

### 3. SigV4 验证依赖 Proxy (Medium)

**症状**：如果 Proxy 未运行，Portal 无法验证 Bedrock Runtime key。

**根因**：SigV4 签名实现只在 Proxy 中，Portal 通过内部 API 调用 Proxy 完成验证。

### 4. Routing 冲突 (Medium)

**症状**：用户请求 `moonshotai.kimi-k2.5` 时路由到了 Bedrock Runtime 而非 Mantle。

**根因**：两个 provider 的 key 都包含该模型，routing 算法无法区分哪个 key 的哪个模型属于哪个端点。

## 解决方案

### 方案 1：模型列表自动过滤（已实施）

在 Portal 的 Bedrock key 验证流程中，根据 vendor prefix 自动过滤模型列表。

**Bedrock Runtime 原生 vendor prefix：**
```
anthropic, amazon, meta, mistral, cohere, stability, ai21
```

**Inference Profile region prefix：**
```
us.*, eu.*, ap.*, global.*
```

不在上述列表中的 vendor（如 `openai.*`, `deepseek.*`, `qwen.*`, `nvidia.*` 等）会被从 Runtime key 的模型列表中移除。

**代码位置：** `internal/aws/bedrock.go` → `FilterBedrockRuntimeModels()`, `IsBedrockRuntimeModel()`

### 方案 2：SigV4 验证移入 Portal（已实施）

将 AWS SigV4 签名和 `ListFoundationModels`/`ListInferenceProfiles` 调用直接实现在 Portal 中，不再依赖 Proxy。

**代码位置：**
- `internal/aws/sigv4.go` — SigV4 签名（纯标准库，无 AWS SDK 依赖）
- `internal/aws/bedrock.go` — `ValidateBedrockKey()`, `ListFoundationModels()`, `ListInferenceProfiles()`
- `internal/routes/admin_common.go` — 调用 `aws.ValidateBedrockKey()` 替代 `validateViaProxy()`

### 方案 3：Proxy 侧 Bedrock 模型 ID 解析（已在 ai-proxy-go 实施）

在 Proxy 的 `GetEndpoint()` 中实现智能的模型 ID 解析链：

```
用户请求 model name
    ↓
1. model_name_map 查找 (Portal 配置的 api_model_name → product_id)
    ↓
2. resolveBedrockModelID:
   - 已有 region prefix (global.*, us.*) → 直接使用
   - 裸 vendor prefix (anthropic.*) → 提升为 global.* (最高 quota)
   - 用户友好名 (claude-sonnet-4-6) → MapModelToBedrock 映射表
    ↓
3. 最终调用 Bedrock Converse API
```

**Inference Profile 优先级：**
```
global.*  → 跨区域，quota 最高（优先）
us.*      → 单区域 US
eu.*      → 单区域 Europe
ap.*      → 单区域 Asia-Pacific
```

## 数据流（修改后）

```
┌─────────── Key Validation ───────────┐
│                                       │
│  amazon-us-east-1 key:               │
│    Portal 自己做 SigV4 → ListModels  │
│    → 自动过滤掉 Mantle 模型          │
│    → 只保留 Runtime 原生模型          │
│                                       │
│  amazon-mantle-us-east-1 key:        │
│    Portal 调 /v1/models (Bearer)     │
│    → 返回的就是 Mantle 模型          │
│                                       │
│  accessapi / 其他 key:               │
│    Portal 调 /v1/models (Bearer)     │
└───────────────────────────────────────┘
          ↓
┌─────────── upstream_keys.models ──────┐
│  各 key 的 models 列表互不重叠        │
│  （同一模型不会出现在两种 key 中）     │
└───────────────────────────────────────┘
          ↓
┌─────────── service_models ────────────┐
│  Admin 配置:                          │
│  - api_model_name (用户可见名)        │
│  - product_id (上游实际 ID)           │
│  - input_price / output_price         │
│  - status (active/inactive)           │
└───────────────────────────────────────┘
          ↓
┌─────────── Proxy Routing ─────────────┐
│  model_name_map: 名称 → 上游 ID      │
│  model_routing:  名称 → provider 优先级│
│  GetAllKeysForModel: 精确匹配         │
│  → 不会再出现跨 endpoint 误路由       │
└───────────────────────────────────────┘
```

## Admin 工作流变化

| 操作 | 修改前 | 修改后 |
|------|--------|--------|
| 添加 Runtime Key | 需 Proxy 运行 | Portal 独立完成 |
| 验证后模型列表 | 含 Mantle 模型（需手动清理） | 自动只含 Runtime 模型 |
| Mantle Key 模型列表 | 可能需手动配 | Verify 后自动获取 |
| 跨 Provider 重复模型 | 需手动配 routing rules | 自动互斥，无重复 |
| 模型定价 | 手动配置 | 仍需手动（可选参考 OpenRouter） |
| 无 model list 的 Key | 需手动添加模型 | 仍需手动（service_models） |

## Bedrock Runtime Vendor Prefix 维护

如 AWS 未来新增 Bedrock Runtime 原生 vendor，需更新 `internal/aws/bedrock.go` 中的 `BedrockRuntimeVendorPrefixes` 列表：

```go
var BedrockRuntimeVendorPrefixes = []string{
    "anthropic",
    "amazon",
    "meta",
    "mistral",
    "cohere",
    "stability",
    "ai21",
}
```

## 遗留问题

1. **已存在的脏数据**：已添加的 Bedrock Runtime key 的 models 列表仍然包含 Mantle 模型。需要重新 validate 这些 key 以触发过滤。
2. **Mantle 模型定价缺失**：`amazon-mantle-us-east-1` provider 在 `service_models` 表中无记录，导致计费 `total_cost = 0`。需要 admin 配置。
3. **`containsMatch` prefix 匹配过严**：`claude-haiku-4-5` 无法匹配 `claude-haiku-4-5-20251001`，因为前缀匹配逻辑排除了 `-` 分隔的后缀。通过在 `service_models` 添加短别名解决。
