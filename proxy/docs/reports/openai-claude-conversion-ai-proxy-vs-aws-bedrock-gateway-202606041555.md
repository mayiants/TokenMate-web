# OpenAI ↔ Claude 格式转换对比：ai-proxy-go vs AWS Bedrock Access Gateway

> **HISTORICAL** — June 2026 conversion format comparison. Implementation complete.
>
> 日期: 2026-06-04
> 分析范围: ai-proxy-go `pkg/convert/` + `pkg/relay/channel/claude/` vs bedrock-access-gateway `src/api/models/bedrock.py`
> 前置文档: `bedrock-gateway-openai-claude-format-conversion_202606041545.md`

---

## 一、项目状态概览

### ai-proxy-go
- 分支: `main` — clean, up to date with origin
- 无待提交变更

### ai-portal-go
- 分支: `main` — up to date with origin
- 未跟踪文件: `docs/reports/bedrock-gateway-openai-claude-format-conversion_202606041545.md`, `scripts/batch_key_load_test.py`
- 修改未暂存: `e2e/screenshots/01-dashboard.png`

### ai-proxy-go 近期关键文档

| 文档 | 核心内容 |
|---|---|
| `proxy-adapter-upgrade-scenario-analysis-v2-202605311830.md` | 12 场景矩阵：Anthropic/Responses/OpenAI × Anthropic/OpenAI/Gemini/Bedrock 的转换路径与工作量评估 |
| `proxy-architecture-design-202605022300.md` | 请求处理完整流程：Auth → RateLimit → KeyPool → Adapter → 上游 |
| `cc-switch-api-transform-analysis-202605311700.md` | CC Switch 的 Hub-and-Spoke 架构分析，Anthropic ↔ OpenAI Chat/Responses/Gemini 转换 |
| `model-name-translation-202605171430.md` | `api_model_name` 作为跨 Provider 统一模型标识的 4 阶段方案 |

### ai-portal-go 近期关键文档

| 文档 | 核心内容 |
|---|---|
| `tokenrouter-research-202606031600.md` | TokenRouter 竞品分析（$10 亿估值，极简设计风格，智能路由） |

---

## 二、ai-proxy-go 转换架构

### 2.1 整体数据流

```
                    Client Request
                         │
                         ▼
              ┌─────────────────────┐
              │   Format Detector   │  ← detector.go
              │ URL + Header + Body │
              └────────┬────────────┘
                       ▼
         ┌─────────────────────────────┐
         │      Client Converter      │  ← anthropic.go / responses.go
         │ Anthropic/Responses → OpenAI│
         └────────────┬────────────────┘
                      ▼
         ┌─────────────────────────────┐
         │   GeneralOpenAIRequest      │  ← 内部 Hub 格式
         └────────────┬────────────────┘
                      ▼
         ┌─────────────────────────────┐
         │     Provider Adapter        │  ← claude/claude.go / openai/openai.go
         │    OpenAI → Provider        │
         └────────────┬────────────────┘
                      ▼
                Upstream Provider
```

ai-proxy-go 采用 **Hub-and-Spoke** 架构，以 `GeneralOpenAIRequest` 为内部 Hub 格式。与 CC Switch 以 Anthropic 为 Hub 不同，ai-proxy-go 选择 OpenAI 格式作为内部标准。

### 2.2 关键文件

| 文件 | 职责 |
|---|---|
| `pkg/convert/detector.go` | 客户端格式检测（URL path / Header / Body） |
| `pkg/convert/anthropic.go` | Anthropic Messages API ↔ GeneralOpenAIRequest 完整双向转换 |
| `pkg/convert/claude.go` | GeneralOpenAIRequest → Claude Request 单向转换（简化版） |
| `pkg/convert/streaming.go` | OpenAI SSE ↔ Anthropic SSE 流式状态机转换 |
| `pkg/convert/common.go` | 角色映射、StopReason 映射、Usage 转换、消息合并 |
| `pkg/convert/gemini.go` | Gemini 协议转换 |
| `pkg/convert/responses.go` | OpenAI Responses API ↔ GeneralOpenAIRequest |
| `pkg/relay/channel/claude/claude.go` | Claude Provider Adapter（ToProvider / FromProvider / ExtractUsage / TransformStreamChunk） |
| `dto/claude.go` | Claude 协议 DTO 定义 |
| `dto/openai.go` | OpenAI 协议 DTO 定义 |

### 2.3 两层转换设计

ai-proxy-go 存在**两层 OpenAI → Claude 转换**：

1. **`pkg/convert/claude.go` — `RequestOpenAI2Claude()`**：通用转换函数，被多处使用，覆盖基本字段映射
2. **`pkg/relay/channel/claude/claude.go` — `ToProvider()`**：Claude Adaptor 的接口实现，包含更完整的业务逻辑（默认 max_tokens、reasoning_effort → thinking 等）

两者功能有**重叠**但实现不完全一致，存在潜在的维护风险。

---

## 三、消息转换对比

### 3.1 系统消息处理

| 维度 | AWS Bedrock Gateway | ai-proxy-go |
|---|---|---|
| 提取方式 | 遍历 messages 收集 system/developer 角色 | 遍历 messages 收集 system 角色 |
| 合并策略 | 列表形式 `[{"text": "..."}]` 保留独立语义 | **字符串拼接**：`strings.Join(systemMessages, "\n\n")` |
| developer 角色 | ✅ 支持（与 system 等同处理） | ❌ 不支持 |
| Prompt Caching | ✅ cachePoint 注入，支持 ENV/extra_body 控制 | ❌ 无内置支持 |
| 格式 | Bedrock `system` 参数：`list[dict]` | Claude `system` 字段：`string` 或 `json.RawMessage` |

**差异分析**：AWS 的实现更完善，尤其是 Prompt Caching 支持。ai-proxy-go 的字符串拼接方式丢失了 Claude System API 支持数组格式的能力（多条 system 分别设置 cache_control）。

### 3.2 消息角色转换

| OpenAI Role | AWS Bedrock | ai-proxy-go (Claude Adapter) |
|---|---|---|
| `system` | 从 messages 移除，进入 `system` 参数 | 从 messages 移除，进入 `system` 字段 |
| `user` | `user` | `user` |
| `assistant` | `assistant`（分离 content 和 toolUse） | `assistant`（分离 content 和 tool_use） |
| `tool` | `user` + `toolResult` 包裹 | `user` + `tool_result` 包裹 |
| `developer` | 等同于 system | ❌ 未处理 |

两者映射逻辑一致，均遵循 Claude API 的约束。

### 3.3 同角色消息合并

| 维度 | AWS Bedrock Gateway | ai-proxy-go |
|---|---|---|
| 方法 | `_reframe_multi_payload()` | `MergeMessages()` |
| 合并方式 | content 作为 **list 合并**（保留独立块结构） | content 作为 **字符串拼接** `"\n"` 连接 |
| 适用场景 | Bedrock Converse API 要求角色交替，但保留 content 数组结构 | 仅合并纯文本消息 |
| 对多媒体消息的处理 | 正确保留 image 块 | 字符串拼接会**丢失** image content |
| Assistant Prefill | 针对 `NO_ASSISTANT_PREFILL_MODELS` 自动追加 user 消息 | 无对应处理 |
| Prompt Caching 注入 | 最后一条 user 消息注入 cachePoint | 无 |

**差异分析**：AWS 的合并实现更健壮，保留原始 content 结构（list of blocks），而 ai-proxy-go 的字符串拼接在遇到多模态消息时有数据丢失风险。

---

## 四、Tool 转换对比

### 4.1 Tool Definition

| 维度 | AWS Bedrock Gateway | ai-proxy-go |
|---|---|---|
| 转换格式 | `{"toolSpec": {"name", "description", "inputSchema": {"json": params}}}` | `{"name", "description", "input_schema": params}` |
| Schema 清理 | 无 | `StripFormatFromSchema()` 移除不兼容 format 字段 |
| tool_choice 转换 | `"required"` → `{"any": {}}`, `"auto"` → `{"auto": {}}` | `"required"` → `"any"`, `"auto"` → `"auto"` |

### 4.2 Tool Call（请求侧 Assistant → Tool Use）

| 维度 | AWS | ai-proxy-go |
|---|---|---|
| 格式 | `{"toolUse": {"toolUseId", "name", "input"}}` | `{"tool_use": {"id", "name", "input"}}` |
| arguments 处理 | `json.loads()` 反序列化为对象 | `json.RawMessage` 直接透传 |

### 4.3 Tool Result（请求侧 Tool → 上游）

| 维度 | AWS | ai-proxy-go |
|---|---|---|
| 目标角色 | `user` | `user` |
| 包裹格式 | `{"toolResult": {"toolUseId", "content": [{"text": ...}]}}` | `{"tool_result": {"tool_use_id", "content": ...}}` |
| content 提取 | 支持 string / list / dict JSON 解析 | 支持 string / MediaContent 类型 |

---

## 五、推理参数对比

### 5.1 Reasoning Effort

| 维度 | AWS Bedrock Gateway | ai-proxy-go |
|---|---|---|
| Claude 通道 | `reasoning_config: {"type": "enabled", "budget_tokens": N}` | `thinking: {"type": "enabled", "budget_tokens": N}` |
| Low budget | `max_tokens * 0.3`（动态比例） | **固定 2000** |
| Medium budget | `max_tokens * 0.6`（动态比例） | **固定 8000** |
| High budget | `max_tokens - 1`（利用所有 token） | **固定 20000** |
| 其他模型 | DeepSeek: reasoning_config 字符串 | ❌ 无 |

**差异分析**：AWS 使用动态比例计算 budget_tokens（基于实际的 max_tokens），更合理；ai-proxy-go 使用固定值，可能对短响应浪费、长响应不足。

### 5.2 Temperature / TopP 冲突

| 维度 | AWS | ai-proxy-go |
|---|---|---|
| 检测 | 针对特定模型列表自动移除 topP | 无冲突检测 |
| 受影响的模型 | Sonnet 4.5/4.6, Haiku 4.5, Opus 4.5 | 未处理 |

---

## 六、流式转换对比

### 6.1 转换方向

| 方向 | AWS Bedrock Gateway | ai-proxy-go |
|---|---|---|
| Bedrock SSE → OpenAI SSE | ✅ `_create_response_stream()` | ❌ （不直接对接 Bedrock） |
| Claude SSE → OpenAI SSE | ❌ （对接 Bedrock） | ✅ `TransformStreamChunk()` |
| OpenAI SSE → Anthropic SSE | ❌ （仅单向） | ✅ `OpenAIChunkToAnthropicEvent()` |

### 6.2 流式事件处理

**Claude SSE → OpenAI SSE**（ai-proxy-go `claude.go:435-563`）：

| Claude 事件 | OpenAI 输出 |
|---|---|
| `message_start` | 记录 ID / model |
| `content_block_start` (text) | 无输出（等待 delta） |
| `content_block_start` (tool_use) | `choices[0].delta.tool_calls[{id, name}]` |
| `content_block_delta` (text_delta) | `choices[0].delta.content` |
| `content_block_delta` (input_json_delta) | `choices[0].delta.tool_calls[{arguments}]` |
| `content_block_delta` (thinking_delta) | `choices[0].delta.reasoning_content` |
| `content_block_stop` | tool 状态重置 |
| `message_delta` | finish_reason + usage |
| `message_stop` | 重置全局 streamState |

**OpenAI SSE → Anthropic SSE**（ai-proxy-go `streaming.go:26-193`）：

| OpenAI 输入 | Anthropic 事件序列 |
|---|---|
| 首个 chunk | `message_start` → `content_block_start` (text) |
| `delta.content` | `content_block_delta` (text_delta) |
| `delta.reasoning_content` | `content_block_delta` (thinking_delta) |
| `delta.tool_calls[].id + name` | `content_block_stop` → `content_block_start` (tool_use) |
| `delta.tool_calls[].arguments` | `content_block_delta` (input_json_delta) |
| `finish_reason` | `content_block_stop` → `message_delta` → `message_stop` |

**Bedrock SSE → OpenAI SSE**（AWS `bedrock.py:1005-1156`）：

| Bedrock 事件 | OpenAI 输出 |
|---|---|
| `messageStart` | 初始化 role |
| `contentBlockStart` (toolUse) | `delta.tool_calls[{id, name}]` |
| `contentBlockDelta` (text) | `delta.content`，含 `</think>` 关闭逻辑 |
| `contentBlockDelta` (reasoningContent) | `<think>` 包裹，状态机跟踪 |
| `contentBlockDelta` (toolUse.input) | `delta.tool_calls[{arguments}]` |
| `messageStop` | finish_reason + think 标签安全检查 |
| `metadata` | usage chunk（含 cache metrics） |

**差异分析**：
- AWS 的 reasoning content 包装（`<think>...</think>`）在流式场景下用 `self.think_emitted` 状态机精确控制，ai-proxy-go 直接透传 `reasoning_content` 字段
- ai-proxy-go 有完整的状态机管理 tool call 的 start/delta/stop 生命周期
- AWS 在 `messageStop` 时有额外的安全检查（关闭未闭合的 `<think>` 标签）

---

## 七、响应转换对比

### 7.1 Stop Reason 映射

| 上游值 | AWS 输出 | ai-proxy-go 输出 |
|---|---|---|
| `end_turn` | `stop` | `stop` |
| `max_tokens` | `length` | `length` |
| `stop_sequence` | `stop` | `stop` |
| `content_filtered` | `content_filter` | `content_filter` |
| `tool_use` | `tool_calls` | `tool_calls` |
| `finished` | `stop` | ❌ 未处理 |
| `complete` | `stop` | ❌ 未处理 |

AWS 的映射更完整，覆盖了更多 Bedrock 特有的 finish_reason 值。

### 7.2 Token 统计

| 维度 | AWS | ai-proxy-go |
|---|---|---|
| 计算方式 | `totalTokens - outputTokens = prompt_tokens` | `input_tokens + output_tokens = total_tokens` |
| prompt_tokens | 从 totalTokens 反推 | 直接使用 input_tokens |
| cache_read | `cacheReadInputTokens` → `cached_tokens` | `cache_read_input_tokens` → `cached_tokens` |
| cache_write | `cacheWriteInputTokens` → `cached_tokens` | `cache_creation_input_tokens` → `cached_creation_tokens` |
| 分时 cache | ❌ | ✅ `ephemeral_5m` / `ephemeral_1h` 分桶 |
| Reasoning tokens | tiktoken 估算 | ❌ 无 |

AWS 需要反推是因为 Bedrock Converse API 的 totalTokens 包含了 cache 相关 token。ai-proxy-go 直接对接 Anthropic API，可以获取准确的 input/output tokens。但 ai-proxy-go 对 Claude cache 的分时统计（5min/1h）更细致。

---

## 八、特殊兼容处理对比

| 场景 | AWS Bedrock Gateway | ai-proxy-go |
|---|---|---|
| GPT 模型名兜底 | ✅ `gpt-*` → DEFAULT_MODEL | ✅ model name translation 层 |
| 连续同角色消息 | ✅ 列表合并（保留结构） | ⚠️ 字符串拼接（丢失多媒体） |
| Tool 角色→User 角色 | ✅ | ✅ |
| Assistant 消息结尾 | ✅ 自动追加 user continuation（针对特定模型） | ❌ |
| Temperature + TopP 冲突 | ✅ 自动移除 TopP（针对特定模型） | ❌ |
| Reasoning Effort | ✅ 动态比例预算（Claude/DeepSeek） | ⚠️ 固定值预算（仅 Claude） |
| Prompt Caching 注入 | ✅ system + messages + ENV/extra_body 控制 | ⚠️ 仅 tracking，无注入 |
| Billing Header 剥离 | ❌（不适用） | ✅ `StripLeadingAnthropicBillingHeader()` |
| Model 动态发现 | ✅ Bedrock API `list_foundation_models()` + Profiles | ✅ service_models DB + admin 管理 |
| 多模态图片 | ✅ URL 下载 + base64 bytes | ✅ data URI + URL 两种 |
| Schema 清理 | ❌ | ✅ `StripFormatFromSchema()` |
| Cross-Region Inference | ✅ 自动检测 Profile → Foundation Model | ❌（不适用） |

---

## 九、架构差异总结

| 维度 | AWS Bedrock Access Gateway | ai-proxy-go |
|---|---|---|
| **语言** | Python (FastAPI + boto3) | Go (Gin + net/http) |
| **定位** | OpenAI → Bedrock 单向网关 | 多格式 × 多 Provider 双向网关 |
| **Hub 格式** | 无（直接转换） | GeneralOpenAIRequest |
| **转换层数** | 1 层（`_parse_request` → Bedrock） | 2 层（ClientConverter + ProviderAdapter） |
| **支持的客户端格式** | OpenAI Chat Completions | Anthropic Messages / OpenAI Chat / OpenAI Responses |
| **支持的上游** | AWS Bedrock（Claude, Nova, DeepSeek, Llama, etc.） | Anthropic, OpenAI, Gemini, Bedrock, Azure, 自定义 Provider |
| **流式转换方向** | 单向：Bedrock → OpenAI | 双向：Claude ↔ OpenAI |
| **Prompt Caching** | ✅ 完整支持（注入 + 控制） | ⚠️ Usage tracking only |
| **Model 管理** | 动态 API 发现 | 静态 DB + Admin 维护 |
| **代码规模** | ~1500 行 Python（bedrock.py） | ~1000 行 Go（convert/ + channel/claude/） |
| **测试覆盖** | 无单元测试 | ✅ `convert_test.go` + `claude_test.go` |

---

## 十、关键发现

### 10.1 ai-proxy-go 的亮点

1. **多格式双向转换**：Anthropic ↔ OpenAI 的完整双向转换，包括流式状态机，这是 AWS 实现完全不具备的能力
2. **Billing Header 剥离**：`StripLeadingAnthropicBillingHeader()` 是 Claude Code 特有的兼容处理，直接影响 prompt cache 命中率
3. **分时 Cache 统计**：区分 ephemeral_5m 和 ephemeral_1h 的 cache creation tokens，比 AWS 更细致
4. **Go 语言性能**：静态类型 + 并发模型，在高并发场景下更有优势

### 10.2 AWS Bedrock Gateway 的亮点

1. **动态 Budget 计算**：reasoning_effort 的 budget_tokens 基于 max_tokens 动态计算，比固定值更合理
2. **Prompt Caching 完整方案**：从系统提示词到消息级别的 cachePoint 注入，配合 ENV/extra_body 双重控制
3. **Assistant Prefill 兼容**：自动检测不支持 assistant prefill 的模型并追加消息
4. **Temperature/TopP 冲突自动修复**：对已知冲突模型自动移除 TopP
5. **安全的流式状态机**：`think_emitted` 标志位 + messageStop 安全检查，防止标签泄露

### 10.3 ai-proxy-go 的改进建议

1. **合并策略**：`MergeMessages()` 应从字符串拼接升级为 Content Block 数组合并，避免多模态消息丢失
2. **Reasoning Budget**：从固定值改为基于 max_tokens 的动态比例计算
3. **Prompt Caching**：考虑在 Claude 通道增加 cache_control 注入
4. **Temp/TopP 冲突**：增加针对 Claude 4.5/4.6 系列的参数冲突检测
5. **代码去重**：`convert/claude.go` 和 `channel/claude/claude.go` 的功能重叠应整合

### 10.4 AWS Bedrock Gateway 的改进建议

1. **分时 Cache 统计**：借鉴 ai-proxy-go 的 ephemeral_5m/1h 分桶
2. **Schema 清理**：借鉴 `StripFormatFromSchema()` 处理不兼容的 JSON Schema format 字段
3. **测试覆盖**：增加单元测试
