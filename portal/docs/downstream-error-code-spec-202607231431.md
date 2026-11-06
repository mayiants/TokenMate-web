# WOODGIT 下游错误码与返回语句规范

> 本文档定义 WOODGIT（ai-proxy-go / ai-portal-go）对下游 API 消费者返回的 HTTP 状态码、错误体结构、错误码（`code`）以及标准化错误消息。
> 
> 适用对象：所有通过 OpenAI 兼容协议或 Anthropic 原生协议调用 WOODGIT 的客户端、第三方工具及内部页面。
> 
> 设计目标：
> 1. 错误信息对人类用户友好，对机器可解析；
> 2. 不泄露上游 provider 内部细节（如上游 key、内部 URL、原始账号信息）；
> 3. 同一类错误使用一致的 `type` + `code` + `message` 三元组；
> 4. 客户端可以仅根据 `code` 实现重试、降级或提示策略。

---

## 1. 错误体结构

所有错误返回统一采用 OpenAI 风格的 `error` 信封：

```json
{
  "error": {
    "message": "Human-readable description in English",
    "type":    "error_category",
    "code":    "MACHINE_READABLE_CODE",
    "param":   "optional_field_name",
    "retry":   false
  }
}
```

字段说明：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `message` | string | 是 | 面向终端用户的简短英文说明，避免技术栈细节，可包含纠正建议。 |
| `type` | string | 是 | 错误大类，决定客户端处理策略。见第 2 节。 |
| `code` | string | 是 | 机器可读的错误码，同一 `code` 的语义在不同接口保持一致。 |
| `param` | string | 否 | 触发错误的请求参数名，用于 400 类请求格式错误。 |
| `retry` | bool | 否 | 是否建议客户端重试。默认按 `type` 推断，必要时显式覆盖。 |

> **注意**：上游 provider 返回的原始错误体不会被透传给下游，原始内容仅在服务端日志、审计日志及上游 key 对账中使用。

---

## 2. HTTP 状态码与错误类型速查

| HTTP | `type` | 触发场景 | 是否建议重试 |
|------|--------|----------|--------------|
| **400** | `invalid_request_error` | 请求体无法解析、参数非法、模型名无法解析、`model=auto` 不可用等 | 否，需修改请求 |
| **401** | `authentication_error` | 缺少/非法 API key、Portal 校验失败 | 否，需检查凭证 |
| **402** | `billing_error` | 余额耗尽、企业额度耗尽、成员配额耗尽、订阅额度耗尽 | 否，需充值/升级/等待重置 |
| **403** | `permission_error` / `model_not_allowed` | 模型不在允许列表、管理员签名/时间戳/nonce 校验失败 | 否，需调整权限 |
| **429** | `rate_limit_error` | RPM / RPH / RP5H / RPW / RPD / TPM / TPH / TPW / 并发 / 套餐窗口超限 | 是，按 `Retry-After` 等待后重试 |
| **502** | `upstream_error` | 上游连接失败、上游 5xx、上游响应解析失败 | 是，指数退避重试 |
| **503** | `service_error` / `server_busy` | 无可用上游 key、依赖（Redis/DB）异常、健康检查 degraded | 是，稍后重试 |
| **500** | `internal_error` | 响应转换失败、未捕获内部异常 | 是，稍后重试 |

---

## 3. 认证错误（HTTP 401）

请求未携带有效凭证，或凭证已失效/被撤销。**修复凭证前无需重试。**

### 3.1 缺少 API key

```json
{
  "error": {
    "message": "Missing API key. Please provide a valid API key in the Authorization header.",
    "type": "authentication_error",
    "code": "MISSING_API_KEY"
  }
}
```

触发位置：`internal/auth/auth.go`（Proxy）、`internal/routes/apikeys_validate.go`（Portal `/api/keys/validate`）

### 3.2 API key 格式非法

```json
{
  "error": {
    "message": "Invalid API key format. The key must start with 'sk-' and be at least 16 characters.",
    "type": "authentication_error",
    "code": "INVALID_API_KEY_FORMAT"
  }
}
```

### 3.3 API key 无效或已禁用

```json
{
  "error": {
    "message": "The API key appears to be invalid or may have expired. Please verify your credentials and try again.",
    "type": "authentication_error",
    "code": "INVALID_API_KEY"
  }
}
```

Portal `/api/keys/validate` 在 key 不存在、被禁用、用户被暂停、企业被暂停或 key 过期时返回 `valid: false`：

```json
{
  "valid": false,
  "error": "Key is disabled"
}
```

或

```json
{
  "valid": false,
  "error": "Key expired"
}
```

---

## 4. 计费错误（HTTP 402）

身份已验证，但账户/企业/订阅额度不足。**重试无意义**，需充值、升级或等待窗口重置。

### 4.1 个人余额耗尽

```json
{
  "error": {
    "message": "Insufficient balance. Please top up your account.",
    "type": "billing_error",
    "code": "BALANCE_EXHAUSTED"
  }
}
```

触发位置：`internal/auth/auth.go:203-214`、`internal/routes/apikeys_validate.go:242-249`、`internal/server/handlers_chat.go:368-372`

### 4.2 请求级余额不足（预扣费失败）

```json
{
  "error": {
    "message": "Insufficient balance for this request",
    "type": "billing_error",
    "code": "INSUFFICIENT_BALANCE"
  }
}
```

### 4.3 企业额度耗尽（110% 硬上限）

```json
{
  "valid": false,
  "error": "Enterprise credit exhausted (110% limit reached)",
  "error_code": "ENTERPRISE_CREDIT_EXHAUSTED"
}
```

### 4.4 企业成员配额耗尽

```json
{
  "valid": false,
  "error": "Member quota limit exceeded",
  "error_code": "MEMBER_QUOTA_EXCEEDED"
}
```

---

## 5. 权限错误（HTTP 403）

身份已验证，但当前账号无权执行该操作。

### 5.1 模型不在允许列表

```json
{
  "error": {
    "message": "Model 'claude-sonnet-4-6' is not in your allowed models list. Contact your administrator to add this model.",
    "type": "model_not_allowed",
    "code": "MODEL_NOT_ALLOWED"
  }
}
```

> 注：`type` 使用 `model_not_allowed` 而非 `permission_error`，方便客户端识别这是模型权限问题。

### 5.2 管理员接口签名/时间戳/nonce 校验失败

```json
{
  "error": {
    "message": "Invalid request signature",
    "type": "permission_error",
    "code": "INVALID_SIGNATURE"
  }
}
```

相关 code：

| code | 场景 |
|------|------|
| `TIMESTAMP_REQUIRED` | 缺少 `X-Timestamp` |
| `INVALID_TIMESTAMP` | 时间戳格式非法 |
| `TIMESTAMP_EXPIRED` | 时间戳过期或超前 |
| `NONCE_REQUIRED` | 缺少 `X-Nonce` |
| `NONCE_REPLAY` | nonce 已使用（重放攻击） |
| `SIGNATURE_REQUIRED` | 缺少 `X-Signature` |
| `INVALID_SIGNATURE` | HMAC 签名不匹配 |

---

## 6. 请求格式错误（HTTP 400）

请求内容本身有问题，服务端在解析或校验阶段即拒绝。**修改请求后重发，无需等待。**

### 6.1 请求体读取/解析失败

```json
{
  "error": {
    "message": "Invalid request: failed to parse JSON body",
    "type": "invalid_request_error",
    "code": "INVALID_REQUEST"
  }
}
```

### 6.2 模型名无法解析

```json
{
  "error": {
    "message": "Failed to resolve model. Check the model name and routing configuration.",
    "type": "invalid_request_error",
    "code": "MODEL_RESOLUTION_ERROR"
  }
}
```

### 6.3 `model=auto` 无可用模型

```json
{
  "error": {
    "message": "model=auto: no available model found for your request. Configure scene_routing in group settings.",
    "type": "invalid_request_error",
    "code": "AUTO_MODEL_UNAVAILABLE"
  }
}
```

### 6.4 `model=auto` 未配置

```json
{
  "error": {
    "message": "model=auto is not available. Your group does not have scene routing configured.",
    "type": "invalid_request_error",
    "code": "AUTO_NOT_CONFIGURED"
  }
}
```

### 6.5 模型能力超限

当请求参数超出模型声明的能力范围时返回。当前 Proxy 主要依赖上游 provider 的校验结果进行映射；未来可在 Proxy 入口增加主动校验，统一返回 `MODEL_CAPABILITY_EXCEEDED`。

#### 6.5.1 当前行为：上游 provider 拒绝

上游返回的 context length、max output tokens、不支持的 modality（vision/audio/tool）等错误，按上游 HTTP 状态映射：

| 上游状态 | 下游 `type` | 下游 `code` | 说明 |
|----------|-------------|-------------|------|
| 400 | `invalid_request_error` | `UPSTREAM_BAD_REQUEST` | 如 `context_length_exceeded`、`max_tokens` 超过模型上限 |
| 404 | `invalid_request_error` | `MODEL_NOT_FOUND` | 模型在 provider 侧不存在或已下线 |

示例（上游 context length 超限映射后）：

```json
{
  "error": {
    "message": "The request was rejected by the upstream provider. Check the model name and request parameters.",
    "type": "invalid_request_error",
    "code": "UPSTREAM_BAD_REQUEST"
  }
}
```

> 注意：当前实现不解析上游错误体中的具体原因码，因此客户端无法区分是参数格式错误还是能力超限。服务端日志中保留了原始上游响应，供排查使用。

#### 6.5.2 建议新增：Proxy 主动校验（待实现）

在 Proxy 入口根据 `/v1/models` 返回的 `capabilities` 对请求做预检，统一返回以下错误体：

```json
{
  "error": {
    "message": "Request exceeds model capability 'context_window' (max 1048576 tokens). Reduce the input length or choose a model with a larger context window.",
    "type": "invalid_request_error",
    "code": "MODEL_CAPABILITY_EXCEEDED",
    "param": "messages"
  }
}
```

对应场景与 `param` 建议：

| 场景 | `code` | `param` | 示例消息 |
|------|--------|---------|----------|
| 输入总 token 数超过 `context_window` | `MODEL_CAPABILITY_EXCEEDED` | `messages` | `Input tokens exceed model context window (max N).` |
| `max_tokens` 超过 `max_output_tokens` | `MODEL_CAPABILITY_EXCEEDED` | `max_tokens` | `max_tokens exceeds model maximum output tokens (max N).` |
| 请求了不支持的 vision 输入 | `MODEL_CAPABILITY_EXCEEDED` | `messages` | `Model does not support image input.` |
| 请求了不支持的 audio 输入 | `MODEL_CAPABILITY_EXCEEDED` | `messages` | `Model does not support audio input.` |
| 请求了不支持的 tool calls | `MODEL_CAPABILITY_EXCEEDED` | `tools` | `Model does not support function calling.` |
| 请求了不支持的 JSON mode | `MODEL_CAPABILITY_EXCEEDED` | `response_format` | `Model does not support JSON mode.` |
| 请求了不支持的 reasoning effort | `MODEL_CAPABILITY_EXCEEDED` | `reasoning_effort` | `Model does not support the requested reasoning effort.` |

---

## 7. 限流与配额错误（HTTP 429）

请求频次或 token 量超出限制。所有 429 响应均携带 `Retry-After` 响应头（秒）。

### 7.1 RPM 超限

```json
{
  "error": {
    "message": "Rate limit exceeded (60 RPM). Please slow down your requests.",
    "type": "rate_limit_error",
    "code": "RATE_LIMIT_EXCEEDED"
  }
}
```

`Retry-After: 1`

### 7.2 RPH 超限

```json
{
  "error": {
    "message": "Hourly rate limit exceeded (1000 RPH). Please try again later.",
    "type": "rate_limit_error",
    "code": "HOURLY_RATE_LIMIT_EXCEEDED"
  }
}
```

### 7.3 RPD 超限

```json
{
  "error": {
    "message": "Rate limit exceeded (5000 requests per day). Please try again tomorrow.",
    "type": "rate_limit_error",
    "code": "DAILY_RATE_LIMIT_EXCEEDED"
  }
}
```

### 7.4 并发请求超限

```json
{
  "error": {
    "message": "Too many concurrent requests (5 max). Please wait and retry.",
    "type": "rate_limit_error",
    "code": "CONCURRENT_LIMIT_EXCEEDED"
  }
}
```

`Retry-After: 1`

### 7.5 订阅/套餐窗口配额耗尽

```json
{
  "error": {
    "message": "Rate limit quota exceeded for current time window. Please try again later.",
    "type": "rate_limit_error",
    "code": "QUOTA_EXCEEDED"
  }
}
```

Portal `/api/keys/validate` 同时返回 `quota_exceeded: true` 与 `quota_window`：

```json
{
  "valid": true,
  ...,
  "quota_exceeded": true,
  "quota_window": "monthly"
}
```

`quota_window` 取值：`tpm`、`tph`、`tpw`、`rpm`、`rph`、`rpw`、`rp5h`、`rpd`、`daily`、`monthly`。

---

## 8. 上游 Provider 错误（HTTP 502）

上游 provider 返回 5xx、连接失败、TLS 失败、读取超时或响应无法解析。Proxy 不会把上游原始错误体透传给下游。

### 8.1 上游内部错误

```json
{
  "error": {
    "message": "The upstream provider returned an internal error. Please retry later.",
    "type": "upstream_error",
    "code": "UPSTREAM_ERROR"
  }
}
```

### 8.2 上游不可达

```json
{
  "error": {
    "message": "Failed to reach the upstream provider. Please retry later.",
    "type": "upstream_error",
    "code": "UPSTREAM_UNREACHABLE"
  }
}
```

### 8.3 上游响应解析失败

```json
{
  "error": {
    "message": "Failed to parse upstream response",
    "type": "upstream_error",
    "code": "UPSTREAM_ERROR"
  }
}
```

### 8.4 上游 4xx 映射

| 上游状态 | 下游状态 | `code` | 说明 |
|----------|----------|--------|------|
| 400 | 400 | `UPSTREAM_BAD_REQUEST` | 上游拒绝请求参数 |
| 401/403 | 401 | `UPSTREAM_ACCESS_DENIED` | 上游拒绝访问该模型 |
| 404 | 404 | `MODEL_NOT_FOUND` | 上游不存在该模型 |
| 408 | 504 | `UPSTREAM_TIMEOUT` | 上游请求超时 |
| 429 | 429 | `UPSTREAM_RATE_LIMITED` | 上游限流 |
| 5xx | 502 | `UPSTREAM_ERROR` | 上游内部错误 |

> 注：当前实现中，部分上游 4xx 可能因 adaptor 解析失败而退化为 502。后续改进见第 10 节。

---

## 9. 服务不可用（HTTP 503）

Proxy 自身或其依赖暂时不可用，**与上游 provider 无关**。

### 9.1 无可用上游 key

```json
{
  "error": {
    "message": "no available upstream keys for provider: amazon-us-east-1",
    "type": "service_error"
  }
}
```

### 9.2 Portal 校验服务不可用

```json
{
  "error": {
    "message": "Authentication service temporarily unavailable. Please try again later.",
    "type": "service_unavailable",
    "code": "PORTAL_UNAVAILABLE"
  }
}
```

### 9.3 服务繁忙

```json
{
  "error": {
    "message": "server busy, try again later",
    "type": "server_busy"
  }
}
```

---

## 10. 内部错误（HTTP 500）

Proxy 内部发生未预期错误，**不由请求内容或账户状态引起**。通常等待后重试即可恢复。

```json
{
  "error": {
    "message": "Failed to transform response",
    "type": "internal_error"
  }
}
```

---

## 11. 错误码与消息文案规范

### 11.1 错误码命名规则

- 使用全大写蛇形命名；
- 以业务领域前缀区分：`MISSING_`、`INVALID_`、`UPSTREAM_`、`RATE_LIMIT_`、`BALANCE_`、`ENTERPRISE_`、`MEMBER_`、`MODEL_`、`AUTO_`；
- 同一错误码在同一类接口中语义保持一致；
- 避免使用上游 provider 的原始错误码作为下游 `code`。

### 11.2 消息文案规则

1. **英文输出**：下游错误消息统一使用英文，便于第三方客户端稳定解析；
2. **无内部细节**：不返回上游 key、provider 账号、内部 URL、堆栈、SQL 错误；
3. **可行动**：在合理范围内给出下一步操作建议（如 `Please top up`、`Please try again later`、`Contact your administrator`）；
4. **模型名可显式**：返回 `MODEL_NOT_ALLOWED` 时可包含模型名，帮助用户自查；
5. **限流类必须带窗口**：RPM/RPH/RP5H/RPW/RPD 等错误消息中应体现窗口与阈值。

### 11.3 `type` 取值清单

| `type` | 用途 |
|--------|------|
| `authentication_error` | 凭证缺失/非法/失效 |
| `billing_error` | 余额/额度/配额耗尽 |
| `permission_error` | 签名、nonce、管理员权限 |
| `model_not_allowed` | 模型不在允许列表 |
| `invalid_request_error` | 请求格式、参数、模型解析 |
| `rate_limit_error` | 各类限流与并发 |
| `upstream_error` | 上游 provider 失败 |
| `service_error` / `service_unavailable` | Proxy 依赖不可用 |
| `server_busy` | 并发限流导致的繁忙 |
| `internal_error` | Proxy 内部异常 |

---

## 12. 客户端处理建议

1. **先读 HTTP 状态码，再读 `code`**：状态码决定重试策略；`code` 决定具体提示文案；
2. **401/402/403/400 不重试**：分别对应凭证、计费、权限、请求内容问题；
3. **429 按 `Retry-After` 等待**：未提供时默认退避 1-5 秒；
4. **502/503/500 可指数退避重试**：建议最多 3 次，首次等待 1 秒；
5. **流式请求遇错断开**：SSE 流中若收到非 `[DONE]` 事件或连接中断，应按非流式错误处理流程重试；
6. **日志中保留 `code`**：便于排查与对账，但不要向终端用户展示原始上游错误体。

---

## 13. 已知限制与后续改进方向

1. **上游 4xx 透传不完整**：当前部分上游 4xx 在 `adaptor.ReadResponse()` 解析失败时退化为 502。建议：
   - 在解析响应体前先按 HTTP 状态码分类，避免解析失败丢失语义；
   - 统一上游 4xx → 下游对应 4xx，上游 5xx → 502。
2. **错误码未完全覆盖流式中断场景**：当前流式空响应、finish reason 异常等通过 SSE 事件或 500 返回，建议新增 `STREAM_INTERRUPTED`、`EMPTY_RESPONSE` 等 code。
3. **消息国际化**：当前仅支持英文，后续可在 Portal 页面层根据 `Locale` 做二次映射，API 层保持英文稳定。
4. **Portal 验证接口与 Proxy 错误语义对齐**：`/api/keys/validate` 的 `valid: false` 形式与 Proxy 的 `error` 信封不完全一致，建议统一为 `error` 信封，或在前端消费层做显式转换。

---

## 14. 相关文件索引

- `ai-proxy-go/internal/auth/auth.go` — API key / balance / admin 认证错误
- `ai-proxy-go/internal/ratelimit/ratelimit.go` — RPM/RPH/RP5H/RPW/RPD/并发/TPM 限流错误
- `ai-proxy-go/internal/server/handlers_chat.go` — 主业务逻辑、上游错误处理与转发
- `ai-proxy-go/internal/server/handlers_common.go` — 上游请求公共逻辑与埋点
- `ai-proxy-go/internal/server/upstream_error.go` — 上游错误分类与脱敏
- `ai-proxy-go/internal/server/server.go` — `/health`、并发限流、路由注册
- `ai-proxy-go/pkg/billing/billing.go` — `ProviderErrorType`、`ClassifyProviderError`
- `ai-portal-go/internal/routes/apikeys_validate.go` — Portal key 校验与余额/配额错误
- `ai-portal-go/internal/routes/webhook_health.go` — Portal 接收 health batch
- `ai-portal-go/internal/routes/admin_upstream_ops.go` — `/api/admin/upstream-ops/*` 查询接口

---

## 参考

- [Kimi Code Error Reference](https://www.kimi.com/code/docs/kimi-code/error-reference.html) — 本文档的文案与分类风格参考来源。
- `ai-portal-go/docs/proxy-return-code-spec-202606201359.md` — 早期 Proxy 上行/下行返回值归档。
