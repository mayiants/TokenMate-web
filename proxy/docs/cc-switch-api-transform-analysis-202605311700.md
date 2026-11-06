> **ACTIVE** - CC Switch API 格式转换实现分析

# CC Switch API 格式转换实现分析

> 日期: 2026-05-31
> 分析版本: cc-switch v3.16.0
> 源码路径: `src-tauri/src/proxy/providers/`

---

## 一、整体架构

CC Switch 采用 **Hub-and-Spoke** 模式，以 **Anthropic Messages API** 作为内部 Hub 格式：

```
客户端请求 (Anthropic)
        │
        ▼
   CC Switch 本地代理
        │
        ├── api_format = "anthropic"      → 直通
        ├── api_format = "openai_chat"    → 转换 → OpenAI Chat Completions
        ├── api_format = "openai_responses" → 转换 → OpenAI Responses API
        └── api_format = "gemini_native"  → 转换 → Gemini generateContent
        │
        ▼
   上游 Provider
```

**关键文件**：

| 文件 | 行数 | 职责 |
|------|------|------|
| `transform.rs` | 1-1200+ | Anthropic ↔ OpenAI Chat Completions |
| `transform_responses.rs` | 1-1350+ | Anthropic ↔ OpenAI Responses API |
| `transform_gemini.rs` | 1-1450+ | Anthropic ↔ Gemini generateContent |
| `adapter.rs` | 1-69 | ProviderAdapter trait 定义 |
| `claude.rs` | 1-863+ | API 格式检测 + 转换分发 |
| `mod.rs` | 1-261 | ProviderType 枚举 + 适配器工厂 |

---

## 二、Anthropic ↔ OpenAI Chat Completions

### 2.1 请求转换 (`anthropic_to_openai`, transform.rs:115-235)

| Anthropic 字段 | OpenAI Chat 字段 | 备注 |
|----------------|------------------|------|
| `system` (string/array) | `messages[0].role: "system"` | 合并多条 system 为一条 |
| `messages[].content[].type: "text"` | `messages[].content` | 单条直接展开为字符串 |
| `messages[].content[].type: "image"` | `messages[].content[].type: "image_url"` | base64 → `data:` URI |
| `messages[].content[].type: "tool_use"` | `messages[].tool_calls[]` | arguments 用 canonical JSON |
| `messages[].content[].type: "tool_result"` | 独立 `role: "tool"` 消息 | tool_call_id 映射 |
| `messages[].content[].type: "thinking"` | `reasoning_content` | 仅 Moonshot/DeepSeek 兼容模式 |
| `max_tokens` | `max_completion_tokens` | 仅 o-series 模型 |
| `stop_sequences` | `stop` | 直接映射 |
| `tools[].input_schema` | `tools[].function.parameters` | 递归清理 schema |
| `tool_choice.any` | `tool_choice: "required"` | "any" → "required" |
| `thinking.budget_tokens` | `reasoning_effort` | 按阈值映射 low/medium/high |

**特殊处理**：

1. **Billing Header 剥离** (line 18-47)：Claude Code 会在 system prompt 开头插入 `x-anthropic-billing-header:` 行，包含动态 `cch=` 值。这会破坏 prompt cache 命中率。转换层只剥离**开头**的 billing header，保留后续出现的。

2. **System 消息合并** (line 274-344)：多条 system 消息合并为一条，用 `\n` 连接。合并时处理 `cache_control` 冲突：全部一致才保留。

3. **o-series 模型检测** (line 51-55)：`max_tokens` → `max_completion_tokens` 仅对 o1/o3/o4 系列生效。

4. **reasoning_effort 映射** (line 82-112)：
   - `output_config.effort` 直接映射（优先级最高）
   - `thinking.type: "adaptive"` → `xhigh`
   - `thinking.type: "enabled"` + `budget_tokens` → 按阈值：`<4000` → low，`4000-15999` → medium，`≥16000` → high

### 2.2 响应转换 (`openai_to_anthropic`, transform.rs:524-700)

| OpenAI Chat 字段 | Anthropic 字段 | 备注 |
|------------------|----------------|------|
| `choices[0].message.content` | `content[].type: "text"` | 字符串或数组 |
| `choices[0].message.tool_calls[]` | `content[].type: "tool_use"` | arguments JSON → 对象 |
| `choices[0].message.reasoning_content` | `content[].type: "thinking"` | DeepSeek 特有字段 |
| `choices[0].message.refusal` | `content[].type: "text"` | 拒绝内容映射为 text |
| `choices[0].finish_reason: "stop"` | `stop_reason: "end_turn"` | |
| `choices[0].finish_reason: "tool_calls"` | `stop_reason: "tool_use"` | |
| `choices[0].finish_reason: "length"` | `stop_reason: "max_tokens"` | |
| `usage.prompt_tokens` | `usage.input_tokens` | |
| `usage.completion_tokens` | `usage.output_tokens` | |
| `usage.prompt_tokens_details.cached_tokens` | `usage.cache_read_input_tokens` | 标准 OpenAI 嵌套格式 |
| `usage.cache_read_input_tokens` | 直接透传 | 兼容直接字段 |

### 2.3 流式转换

CC Switch 为流式 SSE 提供逐事件转换器 `create_anthropic_sse_stream`，将 OpenAI 的 `data: {"choices":[{"delta":...}]}` 逐行转换为 Anthropic 的 `event: content_block_delta` 格式。

---

## 三、Anthropic ↔ OpenAI Responses API

### 3.1 请求转换 (`anthropic_to_responses`, transform_responses.rs:51-206)

| Anthropic 字段 | Responses API 字段 | 备注 |
|----------------|-------------------|------|
| `system` | `instructions` | 字符串或数组合并为 `\n\n` |
| `messages[]` | `input[]` | tool_use/tool_result "提升"为顶层 |
| `max_tokens` | `max_output_tokens` | Responses 统一使用此字段 |
| `tools[].input_schema` | `tools[].parameters` | 过滤 BatchTool |
| `tool_choice.any` | `tool_choice: "required"` | |
| `thinking.type` | `reasoning.effort` | 与 Chat 共享映射逻辑 |

**核心差异 — tool 提升（Lifting）**：

Anthropic 的 `tool_use` 和 `tool_result` 嵌套在 message content 数组中。Responses API 将它们提升为独立的顶层 input item：

```json
// Anthropic (嵌套)
{"role": "assistant", "content": [
  {"type": "text", "text": "Let me check"},
  {"type": "tool_use", "id": "call_1", "name": "get_weather", "input": {...}}
]}

// Responses API (提升)
[
  {"role": "assistant", "content": [{"type": "output_text", "text": "Let me check"}]},
  {"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": "..."}
]
```

**Codex OAuth 特殊处理** (line 166-203)：

ChatGPT Plus/Pro 反代后端有严格协议约束：
- 强制 `store: false`（不允许服务端持久化）
- 必须包含 `reasoning.encrypted_content`（保持多轮 reasoning 上下文）
- 必须删除 `max_output_tokens`/`temperature`/`top_p`（codex-rs 结构体不支持）
- 强制 `stream: true`（codex-rs 硬编码）

### 3.2 响应转换 (`responses_to_anthropic`, transform_responses.rs:504-607)

| Responses API 字段 | Anthropic 字段 | 备注 |
|-------------------|----------------|------|
| `output[].type: "message"` → `.content[].type: "output_text"` | `content[].type: "text"` | |
| `output[].type: "function_call"` | `content[].type: "tool_use"` | |
| `output[].type: "reasoning"` → `.summary[].text` | `content[].type: "thinking"` | |
| `status: "completed"` + has tool_use | `stop_reason: "tool_use"` | |
| `status: "completed"` | `stop_reason: "end_turn"` | |
| `status: "incomplete"` + max_tokens reason | `stop_reason: "max_tokens"` | |
| `usage.input_tokens` | `usage.input_tokens` | 命名一致 |
| `usage.input_tokens_details.cached_tokens` | `usage.cache_read_input_tokens` | 嵌套格式 |

---

## 四、Anthropic ↔ Gemini generateContent

### 4.1 请求转换 (`anthropic_to_gemini`, transform_gemini.rs:42-105)

| Anthropic 字段 | Gemini 字段 | 备注 |
|----------------|-------------|------|
| `system` + `messages[role=system]` | `systemInstruction.parts[].text` | 合并所有 system 来源 |
| `messages[role=user]` | `contents[].role: "user"` | |
| `messages[role=assistant]` | `contents[].role: "model"` | "assistant" → "model" |
| `content[].type: "tool_use"` | `contents[].parts[].functionCall` | args 字段（非 input） |
| `content[].type: "tool_result"` | `contents[].parts[].functionResponse` | 需要解析 name |
| `content[].type: "image"` | `contents[].parts[].inlineData` | base64 only |
| `tools[].input_schema` | `tools[].functionDeclarations[].parametersJsonSchema` | 富 schema 用此字段 |
| `tool_choice: {type: "tool", name}` | `toolConfig.functionCallingConfig: {mode: "ANY", allowedFunctionNames}` | |
| `max_tokens` | `generationConfig.maxOutputTokens` | |
| `temperature` | `generationConfig.temperature` | |
| `top_p` | `generationConfig.topP` | |

**核心难点 — Shadow Store**：

Gemini 2.x 的 `functionCall` 不返回 `id` 字段（尤其并行调用时），且要求后续 `functionResponse` 回放 `thoughtSignature`。CC Switch 实现了 `GeminiShadowStore`：

1. **记录**：每次 Gemini 响应后，记录 assistant turn 的完整 parts + tool call metadata
2. **回放**：下一次请求时，从 shadow store 中找到匹配的 assistant turn，用原始 parts 替换转换后的 parts
3. **合成 ID**：为缺少 id 的 functionCall 合成 `gemini_synth_{uuid}`，在发回 Gemini 前剥离

### 4.2 响应转换 (`gemini_to_anthropic`, transform_gemini.rs:129-272)

| Gemini 字段 | Anthropic 字段 | 备注 |
|-------------|----------------|------|
| `candidates[0].content.parts[].text` | `content[].type: "text"` | 跳过 `thought: true` |
| `candidates[0].content.parts[].functionCall` | `content[].type: "tool_use"` | 合成缺失的 id |
| `candidates[0].finishReason: "STOP"` | `stop_reason: "end_turn"` | |
| `candidates[0].finishReason: "MAX_TOKENS"` | `stop_reason: "max_tokens"` | |
| `candidates[0].finishReason: "SAFETY"` | `stop_reason: "refusal"` | |
| `usageMetadata.promptTokenCount` | `usage.input_tokens` | |
| `usageMetadata.totalTokenCount - input` | `usage.output_tokens` | Gemini 不区分 |
| `usageMetadata.cachedContentTokenCount` | `usage.cache_read_input_tokens` | |
| `promptFeedback.blockReason` | `stop_reason: "refusal"` + error text | |

**Tool args 修正** (`rectify_tool_call_args`, line 839-944)：

Gemini 可能返回的 tool args 与 Anthropic schema 不匹配。修正逻辑：
- 如果 args 有 `parameters` 嵌套但 schema 期望扁平化，提取内部字段
- 如果缺少 required 字段但有 unexpected 字段，重命名
- 特殊处理 `skill` 字段（`name` → `skill`）

---

## 五、Adapter 模式设计

### 5.1 ProviderAdapter Trait (adapter.rs:16-57)

```rust
pub trait ProviderAdapter: Send + Sync {
    fn name(&self) -> &'static str;
    fn extract_base_url(&self, provider: &Provider) -> Result<String, ProxyError>;
    fn extract_auth(&self, provider: &Provider) -> Option<AuthInfo>;
    fn build_url(&self, base_url: &str, endpoint: &str) -> String;
    fn get_auth_headers(&self, auth: &AuthInfo) -> Result<Vec<(HeaderName, HeaderValue)>, ProxyError>;
    fn needs_transform(&self, _provider: &Provider) -> bool { false }
    fn transform_request(&self, body: Value, _provider: &Provider) -> Result<Value, ProxyError> { Ok(body) }
    fn transform_response(&self, body: Value) -> Result<Value, ProxyError> { Ok(body) }
}
```

**设计亮点**：
- `needs_transform()` 延迟判断，避免不必要的转换
- `transform_request/response` 默认直通，子类按需覆写
- Auth header 以 `(HeaderName, HeaderValue)` 元组返回，保持 header 顺序

### 5.2 ProviderRouter (provider_router.rs)

路由决策链：
1. **Failover 开关**：关闭时只用当前 provider，开启时按优先级队列
2. **Circuit Breaker**：每个 `app_type:provider_id` 独立熔断
3. **Provider 选择**：`select_providers(app_type)` 返回有序列表

### 5.3 RequestForwarder (forwarder.rs)

`forward_with_retry()` 核心循环：
1. 获取 `ActiveConnectionGuard`（RAII 并发控制）
2. 遍历 provider 列表
3. 检查熔断器 `allow_provider_request()`
4. 应用 pre-send 优化器（thinking optimizer、cache injection）
5. 调用 adapter 构建请求并发送
6. 失败时应用 rectifier 重试（thinking signature、budget）
7. 记录成功/失败到熔断器和 DB 健康记录

---

## 六、与 ai-proxy-go 的对比

详见 `cc-switch-vs-ai-proxy-go-comparison-20260531.md`。

---

*分析完成*
