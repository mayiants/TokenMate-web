# Upstream Key 使用审计修复方案

> **Date**: 2026-06-19 18:13 UTC
> **Status**: ✅ IMPLEMENTED
> **Scope**: `ai-proxy-go` + `ai-portal-go`
> **Background**: `key-sync-routing-audit-202606181345.md` §5.2 指出 Proxy 侧缺少 upstream key 使用审计， dispute 时无法追溯 "which key was used, when, by whom"。

---

## 1. 问题现状

Proxy 当前有两条数据上报通道：

| 通道 | Endpoint | 数据结构 | 粒度 | 是否含 upstream_key_id |
|---|---|---|---|---|
| 用户用量上报 | `/api/webhook/usage-batch` | `UsageRecord` | 单次请求 | ❌ 不含 |
| 上游用量聚合 | `/api/webhook/upstream-usage-batch` | `UpstreamUsageRecord` | 按 key+model+date 聚合 | ✅ 含 |

### 1.1 单次请求审计缺失

`UsageRecord` 只包含用户 API Key（`apiKeyHash` / `user_api_key_id`），没有记录该请求实际使用了哪把 upstream key。当用户/上游供应商就某次调用产生纠纷时，无法从 `usage_records` 单条记录直接定位到具体上游 key。

### 1.2 Portal 端已有字段被误用

在 `ai-portal-go/internal/routes/webhook_usage.go` 的 `processRecord` 中：

```go
`INSERT INTO usage_records (..., upstream_key_id, ...)`
VALUES ($1, $2::uuid, $3::uuid, $4, ...)
```

第 4 个参数对应 `upstream_key_id`，但当前传入的是 `record.ServerID`（Proxy 节点 ID），导致该列实际存的是 Proxy server ID，而非 upstream key UUID。

---

## 2. 修复目标

1. 让每次 API 调用的审计记录都携带 `upstream_key_id`。
2. Portal 正确将 `upstream_key_id` 写入 `usage_records`。
3. 保持现有 `upstream_key_usage` 聚合表不变，继续用于供应商对账。

---

## 3. 实施方案

### 3.1 Proxy：`internal/audit/audit.go`

在 `AuditEntry` 和 `UsageReport` 中新增字段：

```go
type AuditEntry struct {
    // ... existing fields ...
    UpstreamKeyID string
}

type UsageReport struct {
    // ... existing fields ...
    UpstreamKeyID string `json:"upstreamKeyId"`
}
```

在 `Log()` 中把 `entry.UpstreamKeyID` 复制到 `UsageReport`。

### 3.2 Proxy：`internal/server/handlers.go`

在所有 `deps.AuditLogger.Log(audit.AuditEntry{...})` 调用处，若作用域内有 `upstreamKey` 变量，补充：

```go
UpstreamKeyID: upstreamKey.ID,
```

涉及路径：
- `/v1/chat/completions`（非流式）
- `/v1/chat/completions`（流式）
- `/v1/completions`
- `/v1/embeddings`
- `/v1/audio/transcriptions`
- `/v1/audio/translations`
- `/v1/images/generations`
- Anthropic `/v1/messages`
- 各错误分支的审计记录

### 3.3 Portal：`internal/routes/webhook_common.go`

在 `UsageRecord` 结构体新增：

```go
UpstreamKeyID string `json:"upstreamKeyId"`
```

### 3.4 Portal：`internal/routes/webhook_usage.go`

修改 `processRecord` 的 INSERT 参数：

```go
record.TraceID, nullStr(userID), nullStr(keyID), record.UpstreamKeyID, ...
```

使第 4 个参数从 `record.ServerID` 改为 `record.UpstreamKeyID`。

### 3.5 测试验证

- Proxy：`go test ./internal/audit/...`
- Portal：`go test ./internal/routes/...` 中 webhook 相关测试
- 端到端：发起一次 chat completion，检查 `usage_records.upstream_key_id` 是否正确写入。

---

## 4. 相关审计报告中其他待确认项

本修复只针对 §5.2 "Key Decryption Has No Audit Log"。同份审计报告中提到的以下问题需另行核实是否已修复：

- `syncServiceModelsToKeys` 是否已过滤 `deleted_at` / `enabled`
- 上游失败时 quota 是否有 refund 机制
- `toggleKeyPoolKey` 的 LIKE 注入风险
- `validateKeyAsync` 成功后是否仍覆盖 per-key models

---

## 5. 影响面

- **新增字段**：Proxy `UsageReport` 增加 `upstreamKeyId` JSON 字段；Portal `UsageRecord` 增加同名字段。旧版本 Proxy 不发送该字段，Portal 会收到空字符串，不影响现有逻辑。
- **DB 列**：`usage_records.upstream_key_id` 已存在，无需 migration。
- **下游查询**：依赖 `upstream_key_id` 的管理/对账页面可获得完整追溯能力。
