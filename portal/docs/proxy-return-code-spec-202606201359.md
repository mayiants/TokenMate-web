# Proxy 上行/下行返回值定义说明

> 本文档记录当前 `ai-proxy-go` 对下游（API 消费者）返回的 HTTP 状态码、错误体，以及对上游（upstream provider/key）返回值的内部归类方式。
> 适用版本：基于 2026-06-20 代码状态的实现。
> **状态**: 已按当前代码归档；第 4 节“已知限制与后续改进方向”尚未实施，后续若改进上下游返回码透传/映射，需同步更新本文档。

---

## 1. 下游返回值（Proxy → API 消费者）

所有 OpenAI 兼容端点（`/v1/chat/completions`、`/v1/embeddings`、`/v1/images/generations` 等）以及 Claude 原生端点（`/v1/messages`）统一使用以下返回值约定。

| HTTP 状态码 | 触发场景 | 典型返回体 | 主要代码位置 |
|---|---|---|---|
| **200 OK** | 请求成功；`/v1/models`、`/health` 正常 | OpenAI 标准响应，例如 `chat.completion`、`models list` | `internal/server/handlers.go` |
| **400 Bad Request** | 请求体读取失败、JSON 解析失败、model 解析/校验失败、必填参数缺失 | `{"error":{"message":"Invalid request: ...","type":"invalid_request_error","code":"MODEL_RESOLUTION_ERROR"}}` | `internal/server/handlers.go:470-533` |
| **401 Unauthorized** | 缺少 `Authorization` header、API key 格式非法、Portal 校验失败、admin 接口缺少 `x-proxy-secret` | `{"error":{"message":"Missing API key","type":"authentication_error","code":"MISSING_API_KEY"}}` | `internal/auth/auth.go:175-238` |
| **402 Payment Required** | 用户余额耗尽；上游返回 402 且 Proxy 成功读取响应 | `{"error":{"message":"Insufficient balance. Please top up your account.","type":"billing_error","code":"BALANCE_EXHAUSTED"}}` | `internal/auth/auth.go:203-312` |
| **403 Forbidden** | 请求模型不在 `allowed_models` 列表；admin 接口 timestamp/nonce/signature/secret 校验失败 | `{"error":{"message":"Model 'xxx' is not in your allowed models list...","type":"model_not_allowed","code":"MODEL_NOT_ALLOWED"}}` | `internal/server/handlers.go:537-543`、`internal/auth/auth.go:320-425` |
| **429 Too Many Requests** | RPM / RPH / RP5H / RPW / RPD / 并发 / TPM / 配额超限 | `{"error":{"message":"Rate limit exceeded (60 RPM)...","type":"rate_limit_error","code":"RATE_LIMIT_EXCEEDED"}}`，并返回 `Retry-After` header | `internal/ratelimit/ratelimit.go:267-399`、`internal/auth/auth.go:215-224` |
| **502 Bad Gateway** | 上游连接失败、读取上游响应失败、上游 5xx、响应转换/解析失败 | `{"error":{"message":"Failed to read response","type":"upstream_error"}}` | `internal/server/handlers.go:696-766`、`internal/server/handlers.go:880-884` |
| **503 Service Unavailable** | 没有可用上游 key、依赖（Redis/DB）异常、`/health` degraded、并发限流 server busy | `{"error":{"message":"no available upstream keys for model: gpt-4o-mini","type":"service_error"}}` 或 `{"error":{"message":"server busy, try again later","type":"server_busy"}}` | `internal/server/handlers.go:575-613`、`internal/server/server.go:48-60`、`internal/server/server.go:181-189` |
| **500 Internal Server Error** | 响应格式转换失败、`streaming not supported`、其他未处理内部异常 | `{"error":{"message":"Failed to transform response","type":"internal_error"}}` | `internal/server/handlers.go:785-789`、`internal/server/handlers.go:915` |

### 1.1 关于上游 4xx/5xx 的转发行为

- **上游 4xx**：Proxy 在 `handlers.go:730-766` 尝试保留上游 4xx 状态码，并直接转发上游 JSON body。但如果 `adaptor.ReadResponse()` 解析失败（例如上游返回的错误体不符合该 provider 的响应 schema），会退化成 **502**。
- **上游 5xx**：统一降级为 **502**，避免把上游 500 直接透传给下游。

### 1.2 `/health` 返回值

- **200**：Proxy 自身、key pool、model registry 均健康。
- **503**：依赖异常或同步过旧，返回 `{"status":"degraded","dependencies":{...}}`。

---

## 2. 上行返回值内部归类（Proxy → Portal 健康统计）

Proxy 把每次上游请求的结果归类为 `ProviderErrorType`，通过 `ProviderHealthBuffer` 批量上报到 Portal 的 `provider_health_stats*` 表。

### 2.1 错误类型枚举

定义位置：`ai-proxy-go/pkg/billing/billing.go:567-582`

| ProviderErrorType | 含义 | 触发条件 |
|---|---|---|
| `""` | 成功 | HTTP 2xx |
| `rate_limit` | 限流 / 配额耗尽 | HTTP 429 |
| `auth_error` | 认证/鉴权失败 | HTTP 401 / 403 |
| `invalid_request` | 请求参数错误 | HTTP 400 |
| `error_4xx` | 其他 4xx 错误 | 402 / 404 / 405 等 |
| `error_5xx` | 上游服务器错误 | HTTP 5xx |
| `timeout` | 请求阶段超时 | `context.DeadlineExceeded` 或系统判定超时 |
| `response_timeout` | 已连接但读响应超时 | 读响应阶段超时 |
| `network_error` | 网络层失败 | DNS / TLS / TCP 连接失败等 |
| `content_mismatch` | 内容校验失败 | HTTP 200 但返回内容不符合预期 |
| `upstream_error` | 兜底类型 | 其他无法归类的上游错误 |

### 2.2 归类函数

`ClassifyProviderError(statusCode int, transportErr error, body string) ProviderErrorType`

当前逻辑（`ai-proxy-go/pkg/billing/billing.go:585-609`）：

1. 优先判断 `transportErr`：
   - 超时 → `timeout`
   - 其他 → `network_error`
2. 无传输错误时按 HTTP 状态码：
   - 2xx → 成功
   - 429 → `rate_limit`
   - 401/403 → `auth_error`
   - 400 → `invalid_request`
   - 其他 4xx → `error_4xx`
   - 5xx → `error_5xx`
   - 其他 → `upstream_error`

### 2.3 Portal 侧计数器

Portal 表 `provider_health_stats`、`provider_health_stats_model`、`model_group_health_stats` 均包含以下计数列：

- `success_count`
- `failure_count`
- `timeout_count`
- `rate_limit_count`
- `error_4xx_count`
- `error_5xx_count`
- `auth_error_count`
- `network_error_count`
- `response_timeout_count`
- `content_mismatch_count`
- `invalid_request_count`

错误类型与计数列一一对应，通过 `/api/admin/upstream-ops/errors` 可以查看 model × group 维度的错误细分。

---

## 3. Admin / 内部接口的认证返回值

| HTTP 状态码 | 场景 | 返回体 |
|---|---|---|
| **401 Unauthorized** | `/api/admin/*` 缺少 `x-proxy-secret` header | `{"error":{"message":"Missing x-proxy-secret header"}}` |
| **403 Forbidden** | `x-proxy-secret` 不正确；admin HMAC 签名/时间戳/nonce 校验失败 | `{"error":{"message":"Invalid proxy secret",...}}` / `{"error":{"message":"Invalid request signature","code":"INVALID_SIGNATURE"}}` |
| **400 Bad Request** | admin 接口请求体非法 | `{"error":{"message":"Invalid request body"}}` |

---

## 4. 已知限制与后续改进方向

1. **上游错误码未完全透传**：当前多数上游 4xx（如 402、429）会因为 `adaptor.ReadResponse()` 解析失败而变成 502 返回给下游。后续可考虑：
   - 在 `adaptor.ReadResponse()` 之前先按状态码判断错误响应，避免解析失败。
   - 统一把上游 4xx 透传为对应 4xx，5xx 统一为 502。
2. **ProviderErrorType 未覆盖部分业务错误**：例如上游 402 目前进入 `error_4xx`，如需区分 `insufficient_balance`，可新增独立类型。
3. **Portal 缺少 provider 级错误码明细**：目前只有聚合计数，未记录具体上游 provider 返回的原始状态码分布，后续可扩展。

---

## 5. 相关文件索引

- `ai-proxy-go/internal/auth/auth.go` — API key / balance / admin 认证
- `ai-proxy-go/internal/ratelimit/ratelimit.go` — RPM/RPH/RP5H/RPW/RPD/并发/TPM 限流
- `ai-proxy-go/internal/server/handlers.go` — 主业务逻辑、上游错误处理与转发
- `ai-proxy-go/internal/server/server.go` — `/health`、并发限流、路由注册
- `ai-proxy-go/pkg/billing/billing.go` — `ProviderErrorType`、`ClassifyProviderError`
- `ai-portal-go/internal/routes/webhook_health.go` — Portal 接收 health batch 并写入统计表
- `ai-portal-go/internal/routes/admin_upstream_ops.go` — `/api/admin/upstream-ops/*` 查询接口
