# Bedrock Access Gateway: OpenAI → Bedrock (Claude) 格式转换分析

> 源码仓库: https://github.com/aws-samples/bedrock-access-gateway.git
> 分析日期: 2026-06-04

---

## 一、项目概述

Bedrock Access Gateway 是一个 API 网关，在 AWS 上提供兼容 OpenAI API 的接口，底层对接 Amazon Bedrock 的各种模型（Claude、Nova、DeepSeek、Llama 等）。本文档聚焦于其 **如何将 OpenAI 格式的 Chat Completions 请求转换为 Bedrock Converse API 格式**。

---

## 二、整体架构

```
OpenAI Request → ChatRequest (Pydantic Schema) → _parse_request() → Bedrock Converse API
                                                        ↓
                                              _parse_messages()
                                              _parse_system_prompts()
                                              _parse_content_parts()
                                                        ↓
                                              Bedrock Response → ChatResponse (OpenAI 格式)
```

### 关键文件

| 文件 | 作用 |
|---|---|
| `src/api/schema.py` | Pydantic 数据模型，定义 OpenAI 格式的请求/响应结构 |
| `src/api/routers/chat.py` | FastAPI 路由入口，接收 `/chat/completions` 的 POST 请求 |
| `src/api/models/bedrock.py` | **核心转换逻辑**，包含 `BedrockModel` 类 |
| `src/api/models/base.py` | 抽象基类，定义 Chat/Embeddings 模型接口 |

---

## 三、请求入口

### `chat.py:21-45`

```python
@router.post("/completions", ...)
async def chat_completions(chat_request: ChatRequest):
    if chat_request.model.lower().startswith("gpt-"):
        chat_request.model = DEFAULT_MODEL  # 替换为默认 Bedrock 模型
    model = BedrockModel()
    model.validate(chat_request)
    if chat_request.stream:
        return StreamingResponse(content=model.chat_stream(chat_request), ...)
    return await model.chat(chat_request)
```

- Pydantic 自动将 JSON body 反序列化为 `ChatRequest` 对象并校验
- 以 `gpt-` 开头的 model 名自动替换为默认模型
- 根据 `stream` 参数选择流式或非流式处理

---

## 四、消息转换（核心）

### 4.1 消息角色映射 — `_parse_messages()` (bedrock.py:530-616)

| OpenAI Role | Bedrock Role | 转换说明 |
|---|---|---|
| `user` | `user` | 直接映射，content 通过 `_parse_content_parts()` 转换 |
| `assistant` | `assistant` | 文本内容放入 content；tool_calls 转为 `toolUse` 块 |
| `tool` | `user` | **Bedrock 不支持 tool 角色**，转为 user 消息，内容包裹在 `toolResult` 中 |
| `system` / `developer` | **跳过** | 不放入 messages 列表，由 `_parse_system_prompts()` 单独处理 |

#### Tool Call 转换示例

```
OpenAI:
  {"role": "assistant", "tool_calls": [{"id": "xxx", "function": {"name": "f", "arguments": "{}"}}]}

Bedrock:
  {"role": "assistant", "content": [{"toolUse": {"toolUseId": "xxx", "name": "f", "input": {}}}]}
```

#### Tool Result 转换示例

```
OpenAI:
  {"role": "tool", "tool_call_id": "xxx", "content": "result"}

Bedrock:
  {"role": "user", "content": [{"toolResult": {"toolUseId": "xxx", "content": [{"text": "result"}]}}]}
```

### 4.2 同角色消息合并 — `_reframe_multi_payload()` (bedrock.py:669-765)

**核心问题**：OpenAI 允许连续出现同一角色的多条消息，而 Claude 不允许。

**解决方案**：遍历消息列表，将连续相同角色的消息合并为一条，content 合并为一个 list。

```
OpenAI 输入:
  [user: "Hello"], [user: "Who are you?"]

Bedrock 输出:
  [user: [{"text": "Hello"}, {"text": "Who are you?"}]]
```

**额外处理**：

1. **Assistant Prefill 兼容性** (lines 728-738)：针对 `claude-opus-4-6` 等模型，若对话以 assistant 消息结尾，自动追加一条 user 消息：
   ```json
   {"role": "user", "content": [{"text": "Please continue your response from where you left off."}]}
   ```
   不在此列表中的模型（如 Sonnet 4.5、Haiku 4.5）支持 assistant prefill，无需追加。

2. **Prompt Caching** (lines 743-763)：若启用，在最后一条 user 消息的 content 末尾添加 `{"cachePoint": {"type": "default"}}`。

### 4.3 内容解析 — `_parse_content_parts()` (bedrock.py:1187-1224)

将 OpenAI 的 content 转为 Bedrock content block 列表：

| OpenAI Content 类型 | Bedrock 转换 |
|---|---|
| 纯文本 `str` | `[{"text": "..."}]` |
| `TextContent` | `[{"text": "..."}]` |
| `ImageContent` (base64) | `[{"image": {"format": "jpeg/png/...", "source": {"bytes": ...}}}]` |
| `ImageContent` (URL) | 通过 HTTP GET 下载图片，转为 base64 字节后同上 |

---

## 五、System Prompt 处理

### `_parse_system_prompts()` (bedrock.py:465-528)

将 OpenAI 的 `system` / `developer` 角色消息提取，转为 Bedrock 的 `system` 参数：

```
OpenAI messages:
  [{"role": "system", "content": "You are helpful."}]

Bedrock system parameter:
  [{"text": "You are helpful."}]
```

**Prompt Caching 支持**：
- 通过环境变量 `ENABLE_PROMPT_CACHING` 或 `extra_body.prompt_caching.system` 按请求控制
- 开启后在 system prompt 列表末尾添加 `{"cachePoint": {"type": "default"}}`
- Nova 模型有 20,000 token 的缓存上限，Claude 模型无明确限制

---

## 六、推理参数转换

### `_parse_request()` (bedrock.py:767-893)

| OpenAI 参数 | Bedrock 参数 | 说明 |
|---|---|---|
| `temperature` | `inferenceConfig.temperature` | 直接映射 |
| `top_p` | `inferenceConfig.topP` | 直接映射 |
| `max_tokens` / `max_completion_tokens` | `inferenceConfig.maxTokens` | `max_completion_tokens` 优先 |
| `stop` | `inferenceConfig.stopSequences` | 字符串自动包装为列表 |
| `reasoning_effort` | `additionalModelRequestFields.reasoning_config` | 见下方详解 |
| `tools` | `toolConfig.tools` | 转 `toolSpec` 格式 |
| `tool_choice` | `toolConfig.toolChoice` | `"required"` → `{"any": {}}`，`"auto"` → `{"auto": {}}` |
| `extra_body` | `additionalModelRequestFields` | 合并（过滤掉内部字段 `prompt_caching`） |

### Temperature / TopP 冲突处理 (lines 800-805)

以下模型**不支持同时设置 temperature 和 topP**，同时传入时保留 temperature、移除 topP：

- `claude-sonnet-4-5`
- `claude-sonnet-4-6`
- `claude-haiku-4-5`
- `claude-opus-4-5`

### Reasoning Effort 转换 (lines 819-853)

**Claude 模型** — 使用 `reasoning_config` 对象：
```python
# budget_tokens 按比例计算:
# low:    max_tokens * 0.3
# medium: max_tokens * 0.6
# high:   max_tokens - 1 (至少 1024)
args["additionalModelRequestFields"] = {
    "reasoning_config": {"type": "enabled", "budget_tokens": budget_tokens}
}
```

**DeepSeek V3 模型** — 使用字符串格式：
```python
args["additionalModelRequestFields"] = {
    "reasoning_config": "low"  # 或 "medium" / "high"
}
```

---

## 七、响应转换

### 7.1 非流式响应 — `_create_response()` (bedrock.py:912-1003)

**finish_reason 映射**：

| Bedrock | OpenAI |
|---|---|
| `end_turn` / `finished` / `stop_sequence` / `complete` | `stop` |
| `max_tokens` | `length` |
| `tool_use` | `tool_calls` |
| `content_filtered` | `content_filter` |

**Reasoning Content 处理**：
- Bedrock 的 `reasoningContent` → OpenAI 的 `reasoning_content` 字段
- 同时包装为 `<think>...</think>` 嵌入到 `content` 中，保持 OpenAI SDK 兼容

**Token 统计**：
- Bedrock 的 `totalTokens` 包含所有 token
- 通过 `totalTokens - outputTokens` 反推 `prompt_tokens`
- 提取 `cacheReadInputTokens` 和 `cacheWriteInputTokens` 映射到 OpenAI 的 `prompt_tokens_details.cached_tokens`

### 7.2 流式响应 — `_create_response_stream()` (bedrock.py:1005-1156)

处理 Bedrock 流式事件类型：

| Bedrock 事件 | 处理 |
|---|---|
| `messageStart` | 初始化 assistant 角色消息 |
| `contentBlockStart` | 检测 tool use 开始，创建 ToolCall 结构 |
| `contentBlockDelta` (text) | 文本增量 → 输出到 stream |
| `contentBlockDelta` (reasoningContent) | Reasoning 增量 → `<think>` 标签开始/结束 |
| `contentBlockDelta` (toolUse.input) | Tool 参数增量 |
| `messageStop` | 获取 `stopReason`，安全关闭 thinking 标签 |
| `metadata` | 提取 token 统计，生成 usage chunk |

**`<think>` 标签状态机**：通过 `self.think_emitted` 标志跟踪推理内容的开闭状态，确保流式输出中正确生成 OpenAI 风格的 `<think>...</think>` 标签。

---

## 八、特殊兼容处理汇总

| 兼容场景 | 处理方式 |
|---|---|
| GPT 模型名 | 自动替换为 `DEFAULT_MODEL` |
| 连续同角色消息 | 合并为单条消息 |
| Tool 角色消息 | 转为 user + toolResult |
| Assistant 消息结尾 (部分模型) | 追加 user continuation 消息 |
| Temperature + TopP 同时设置 (部分模型) | 移除 TopP |
| Reasoning Effort (Claude) | 计算 budget_tokens |
| Reasoning Effort (DeepSeek) | 直接传递字符串 |
| Reasoning Content → OpenAI 格式 | `<think>` 标签包装 |
| Bedrock token 统计 → OpenAI | 反推 prompt_tokens |
| Prompt Caching | 通过 extra_body 控制，透明映射 |

---

## 九、关键代码路径

- **消息转换入口**: `_parse_request()` → `_parse_messages()` → `_reframe_multi_payload()`
- **System prompt**: `_parse_system_prompts()`
- **内容解析**: `_parse_content_parts()`
- **Tool 转换**: `_convert_tool_spec()`
- **Response 创建**: `_create_response()` / `_create_response_stream()`
- **模型列表**: `list_bedrock_models()` — 合并 On-Demand、Cross-Region Inference Profile、Application Inference Profile
