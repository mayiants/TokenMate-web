# Rate Limit 链路对齐与加固（2026-07-24）

## 背景

之前的 rate limit 实现存在以下问题：

1. **TPM/TPH/TPMonth 只在请求返回后记录**，无法真正在请求前阻断超大请求。
2. **RPD 会在请求前阻断**，但产品策略希望每日维度只作为监控/展示，不阻断正常流量。
3. **upstream key 的 RPM/TPM 记账只覆盖非流式 `/v1/chat/completions`**，流式 chat、Responses、Embeddings、Images、Audio 等端点未记账，导致上游 key 的配额/利用率统计失真。
4. **admin/rate-limits 页面**未区分 hard/soft 维度，展示语义与 proxy 实际执行不一致。

本文档记录本次重构后的最终执行策略与实现要点。

---

## 生效矩阵

| 维度 | 类型 | 执行时机 | 阻断？ | 说明 |
|------|------|----------|--------|------|
| RPM | Request | 请求前 | **Hard** | 最小值：key / subscription / group |
| RPH | Request | 请求前 | **Hard** | 最小值：subscription / group |
| RP5H | Request | 请求前 | **Hard** | 最小值：subscription / group |
| RPW | Request | 请求前 | **Hard** | 最小值：subscription / group |
| RPD | Request | 请求前 | **Soft** | 仅记录计数器，不阻断，用于 admin 展示 |
| RPMonth | Request | — | **Soft** | 仅 admin 展示/路由参考，proxy 不执行 |
| TPM | Token | 请求前（估算）+ 返回后（实际差额） | **Hard** | 最小值：key / subscription / group |
| TPH | Token | 请求前（估算）+ 返回后（实际差额） | **Hard** | 最小值：subscription / group |
| TPD | Token | 返回后 | **Soft** | 仅记录计数器，不阻断 |
| TPW | Token | 返回后 | **Soft** | 仅记录计数器，不阻断 |
| TPMonth | Token | 请求前（估算）+ 返回后（实际差额） | **Hard** | 最小值：subscription / group |
| Concurrent | Request | 请求前 | **Hard** | subscription key: `max(sub, group)`；general/economy/enterprise key: `group` |
| Upstream quota | Token | — | **Soft** | 仅影响路由偏好，不硬阻断 |
| Subscription daily quota | Token | Portal `/api/keys/validate` | **Hard** | 由 Portal 在 validate 阶段阻断，proxy 不重复处理 |

> 配置为 `0` 表示不限制。

---

## Token 维度执行细节

### 请求前估算

在 `ratelimit.Handler()` 中，当 `effectiveTPM > 0 || effectiveTPH > 0 || effectiveTPMonth > 0` 时，读取请求体并估算本次可能消耗的 token 数：

- **Chat Completions**（`/v1/chat/completions`）：`EstimateMessages(messages, model) + max_tokens`
- **Responses**（`/v1/responses`）：转换为 OpenAI Chat 格式后估算 `EstimateMessages(...) + max_output_tokens`
- **Embeddings**（`/v1/embeddings`）：`CountTextToken(input, model)`，支持 `input` 为 string 或 string 数组
- **Images**（`/v1/images/generations`）：固定经验值 `1000`
- **Audio**（`/v1/audio/*`）：固定经验值 `256`

估算失败返回 `0`，避免误杀。

### 返回后校准

请求完成后，`tokens_consumed` 由下游 handler 写入 gin context。Proxy 用 **实际 token 数 - 估算 token 数** 的差额更新 hard 计数器：

- 实际 > 估算：补扣差额
- 实际 ≤ 估算：不补扣（保留保守预扣）

这样既能请求前硬阻断，又能让利用率计数器尽量接近真实值。

---

## Concurrent 策略

```go
if keyType == "subscription" {
    effectiveConcurrent = max(subConcurrent, groupConcurrent)
} else {
    effectiveConcurrent = groupConcurrent
}
```

- general / economy / enterprise key：只受 group 并发限制
- subscription key：取 subscription 与 group 的较大值，保障预付费用户体验

---

## Upstream Key 记账覆盖范围

| 端点 | 请求记账 | Token 记账 | Quota 记账 |
|------|----------|------------|------------|
| `/v1/chat/completions` 非流式 | ✅ | ✅ | ✅ |
| `/v1/chat/completions` 流式 | ✅ 新增 | ✅ 新增 | ✅ 已有 |
| `/v1/responses` convert 非流式 | ✅ 新增 | ✅ 新增 | ✅ 新增 |
| `/v1/responses` convert 流式 | ✅ 新增 | ✅ 新增 | ✅ 新增 |
| `/v1/responses` passthrough 非流式 | ✅ 新增 | ✅ 新增 | ✅ 新增 |
| `/v1/responses` passthrough 流式 | ✅ 新增 | ✅ 新增 | ✅ 新增 |
| `/v1/embeddings` | ✅ 新增 | ✅ 新增 | — |
| `/v1/images/generations` | ✅ 新增 | — | — |
| `/v1/audio/transcriptions` | ✅ 新增 | ✅ 经验值 256 | — |
| `/v1/audio/translations` | ✅ 新增 | ✅ 经验值 256 | — |

> 所有成功 dispatch（HTTP 2xx）后才记账；4xx/5xx 不记账。

---

## Admin 页面展示（`/admin/rate-limits`）

### Users 标签

- Minute / Hour / 5 Hours / Weekly：标注 `HARD`
- Daily：RPD / TPD 均标注 `SOFT`
- Monthly：RPMonth 标注 `SOFT`，TPMonth 标注 `HARD`
- Quota：标注 `SOFT`（月度配额在 Portal validate 硬阻断）
- Concurrent：显示生效值

### Groups 标签

- Monthly 维度：RPMonth `SOFT`，TPMonth `HARD`

### API Keys 标签

- Utilization 基于 **effective limit**（`min(key, group, subscription)`）计算
- tooltip 展示 key / group / effective 三级数值

### Upstream Keys 标签

- Quota 列标注 `SOFT`（仅影响路由偏好）

---

## 数据库变更

`user_subscriptions` 表新增：

- `tpmonth_limit BIGINT DEFAULT 0`
- `tpmonth_used BIGINT DEFAULT 0`
- `tpmonth_reset_at TIMESTAMP`

Portal validate 接口返回新增字段 `sub_tpmonth_limit`，Proxy auth claims 同步解析。

---

## 已知限制

1. **User 级别无独立 rate limit**：所有 limit 最终来自 key / subscription / group。
2. **Token 预估算保守**：估算失败按 0 处理，超大请求若解析失败不会被 TPM 预检拦截。
3. **Monthly 利用率不精确**：RPMonth / TPMonth 的 `used` 主要依赖 subscription 计数器或 usage_records，窗口边界可能与自然月不完全对齐。
4. **Upstream quota soft**：上游配额仅作路由参考，不硬阻断，避免上游侧配额临时调整导致误杀。

---

## 相关文件

### Proxy
- `ai-proxy-go/internal/ratelimit/ratelimit.go`
- `ai-proxy-go/internal/auth/auth.go`
- `ai-proxy-go/internal/server/handlers_chat.go`
- `ai-proxy-go/internal/server/handlers_responses.go`
- `ai-proxy-go/internal/server/handlers_embeddings.go`
- `ai-proxy-go/internal/server/handlers_images.go`
- `ai-proxy-go/internal/server/handlers_audio.go`

### Portal
- `ai-portal-go/internal/db/schema.go`
- `ai-portal-go/internal/routes/apikeys_common.go`
- `ai-portal-go/internal/routes/apikeys_validate.go`
- `ai-portal-go/internal/routes/admin_usage.go`
- `ai-portal-go/web/templates/admin-rate-limits.html`
