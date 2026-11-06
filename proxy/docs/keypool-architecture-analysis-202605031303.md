> **⚠️ PARTIALLY OUTDATED** — 2026-05-03 起草。2026-05-16/17 架构重构后，模型路由已从 "provider-centric" 迁移至 "api_model_name-centric"。本文档中关于模型通过 provider 硬编码路由的描述已过时。当前路由基于 `resolveProviderAndKey` + `ModelRoutingRule` / `GetRoutingOrder`（跨 provider 统一按 `api_model_name` 路由）。详见 `model-name-translation-202605171430.md` 和 `cross-provider-fallback-routing-202605161900.md`。
>
> **仍有效的部分**: Key Pool 结构、Rate Limit、Affinity Cache、Failure Handling、Circuit Breaker。
> **已过时的部分**: 模型路由链路（§一、§二中关于 provider 硬编码映射的描述）。

# Proxy Key Pool 完整路由架构深度分析

**Status:** ACTIVE ⚠️ 部分章节已过时  
**Created:** 2026-05-03 13:03 UTC  
**Author:** Kimi Code CLI  
**Scope:** `ai-proxy-go` — Key Pool, Rate Limit, Affinity Cache, Failure Handling

---

## 一、请求全链路：User Key → Upstream Key

```
User Request
    │
    ▼
[Auth Middleware] ──► Portal API 验证 (5min缓存, 30min优雅降级)
    │                    └── 返回: user_id, access_mode, key_hash, allowed_models
    ▼
[Rate Limit Middleware] ──► Token Bucket (本地 O(1), 异步 Redis 备份)
    │
    ▼
[handlerChatCompletions]
    │
    ├── 1. Model → Provider 解析 (GetProviderFromModel)
    │      └── "gpt-4o" → "openai", "claude-3" → "anthropic", 未知 → "openai"
    │
    ├── 2. 用户 Priority 判定 (getUserPriority)
    │      ├── enterprise/subscription/single_model → "high"
    │      ├── economy → "economy"
    │      └── default → "normal"
    │
    ├── 3. Affinity Cache 查询 (可选)
    │      └── 看是否有历史成功的 channel
    │
    ├── 4. Key Pool 选择 (GetKeyByPriority)
    │      └── 返回: upstreamKey (含 ActualKey, BaseURL, Provider)
    │
    ├── 5. 创建 Adaptor (GetAdaptor)
    │      └── openai/claude/gemini/openai-compatible
    │
    ├── 6. 请求转换 (ToProvider)
    │      └── Claude/Gemini 做格式转换，OpenAI 透传
    │
    ├── 7. 上游请求 (DoRequest)
    │
    ├── 8. 响应转换 (FromProvider)
    │      └── 统一转回 OpenAI 格式
    │
    ├── 9. 计费 (CalculateCost)
    │
    ├── 10. 审计日志 (AuditLogger)
    │
    └── 11. 返回用户
```

---

## 二、Service 等级（Priority + Tier）

### 2.1 两个维度的等级

| 维度 | 来源 | 作用 |
|---|---|---|
| **Priority** | `access_mode` → `getUserPriority()` | **决定用户能用哪些 key** |
| **Tier** | Portal sync 的 `SyncKey.Tier` | **决定 key 的 RPM/TPM 限额** |

### 2.2 Priority 过滤逻辑（`GetKeyByPriority`）

```go
switch userPriority {
case "high":
    // 企业/订阅用户：可用 high + normal 优先级的 key
    allowed = ["high", "normal", ""]

case "economy":
    // 经济版用户：优先 economy key，没有则 fallback 到 normal
    firstTry = ["economy"]
    fallback = ["normal", ""]

default: // "normal"
    // 普通用户：只能用 normal 优先级的 key
    allowed = ["normal", ""]
}
```

**全局兜底**：如果按 priority 过滤后没有可用 key，则**放开限制，任意可用 key 都可以选**。

### 2.3 Tier 的 RPM/TPM 限额

```go
func getTierLimits(tier string) (rpm, tpm int) {
    switch tier {
    case "premium":    return 10000, 1000000
    case "enterprise": return 5000,  500000
    case "standard":   return 1000,  100000
    case "basic":      return 300,   30000
    case "economy":    return 60,    6000
    default:           return 1000,  100000
    }
}
```

> ⚠️ **注意**：Tier 的 `rpm_limit` / `tpm_limit` 存储在 key 的元数据中，但**当前没有在 proxy 中实际 enforced**。这些值只在 key pool 状态展示时使用。

---

## 三、Key 选择算法（LRU）

在通过 Priority 过滤后，从候选 key 中选择 **Least Recently Used**：

```go
var selected *KeyInternal
for _, key := range filtered {
    if selected == nil || key.LastUsed.Before(selected.LastUsed) {
        selected = key
    }
}
if selected != nil {
    selected.LastUsed = time.Now()  // ← 更新使用时间
}
```

> 🐛 **Bug #1**: 更新 `LastUsed` 时持有的是 `RLock()`（读锁），应该是 `Lock()`（写锁）。位置：`pkg/keypool/manager.go:536`

### Key 的可用性判定（`IsKeyAvailable`）

```go
if !key.Enabled              → 不可用（手动禁用或认证错误）
if key.PurgedFromPool        → 不可用（24h未恢复，已清理）
if key.CooldownUntil > now   → 不可用（冷却中）
if key.RateLimitUntil > now  → 不可用（限流中）
```

---

## 四、Failure Handling（故障处理）

当上游返回错误时，`ReportKeyFailure` 被调用：

| 错误类型 | 处理 |
|---|---|
| **Daily quota 耗尽** | `CooldownUntil = 明天零点`, `RateLimitUntil = 明天零点` |
| **普通 rate limit (429)** | `CooldownUntil = now + 60s`, `RateLimitUntil = now + 60s` |
| **401/403 / 认证错误** | **立即** `Enabled = false`, `Status = "inactive"` |
| **10 分钟内失败 ≥10 次** | `Enabled = false`, `Status = "inactive"` |
| **累计失败 ≥3 次** | `CooldownUntil = now + 60s`, `Status = "cooldown"` |

> 🐛 **Bug #2**: 同一请求可能同时触发 `UpdateKeyUsage` 和 `ReportKeyFailure`，导致 failures 被 double count。

> 🐛 **Bug #3**: 每次 Portal sync 会完全重建 pool，所有 runtime 状态（failures, cooldown, inactive）被重置。位置：`pkg/keypool/manager.go:353-405`

---

## 五、RPM / TPM 限速

### 5.1 RPM — ✅ 已实现

| 属性 | 说明 |
|---|---|
| 算法 | Token Bucket（本地内存） |
| 时间复杂度 | O(1) |
| Key | `ratelimit:user:<keyHash>` |
| 预设档位 | 60, 120, 300, 1000, 5000, 10000, 50000, 100000 |
| 超限时 | 立即返回 429，无队列 |
| Redis | 异步 INCR（100ms ticker），仅作多实例参考 |

### 5.2 TPM — ❌ 未实现

**Token Per Minute 限速当前完全缺失。** `internal/ratelimit/ratelimit.go` 中没有 TPM 的任何 enforcement 逻辑。

---

## 六、Affinity Cache（通道亲和缓存）

### 6.1 目的

把**相似的请求路由到同一个 upstream key**，让该 key 的 prompt cache 保持热态，降低延迟和成本。

### 6.2 Cache Key 生成

有两套 key 生成策略：

| 方法 | 使用场景 | Key 内容 |
|---|---|---|
| `buildCacheKeyWithPriority` | **写入** (Record) | `priority:model:group:sha1(body)[:8]` |
| `buildSimpleCacheKey` | **读取** (Get) | `hash(priority:model:keyID)` → int |

> 🐛 **Bug #6**: 读和写用了不同的 key 格式！
> - 写入时：`"high:gpt-4o:default:a3f7b2d1"`（含请求 body fingerprint）
> - 读取时：`hash("high:gpt-4o:key_abc123")`（不含 body，只含 keyID）
>
> 这导致 affinity cache **实际上几乎不可能命中**。

### 6.3 当前行为

```go
// Handler 中：
channelID = affinityCache.GetChannelWithPriority(keyID, req.Model, req.Messages, priority)
// 返回的 channelID 只参与审计日志记录，不改变 upstream key 的选择！
```

**Affinity cache 目前只起"记录"作用，不影响实际路由决策。** Upstream key 仍由 `GetKeyByPriority` 独立选择。

---

## 七、关键 Bug 汇总

| # | Bug | 影响 | 位置 |
|---|---|---|---|
| 1 | `LastUsed` 在 `RLock()` 下被写入 | 竞态条件 | `pkg/keypool/manager.go:536` |
| 2 | Failure double count | 错误计数翻倍 | `pkg/keypool/manager.go` + `internal/server/handlers.go` |
| 3 | Sync 重置所有 runtime 状态 | Cooldown/Inactive key 被复活 | `pkg/keypool/manager.go:353-405` |
| 4 | Redis load 后 `internalKeys` 为空 | Redis fallback 无效 | `pkg/keypool/manager.go:1081-1108` |
| 5 | `persistToRedis` data race | JSON marshal 时并发修改 | `pkg/keypool/manager.go:1056-1078` |
| 6 | Affinity 读写 key 不一致 | Cache 几乎永不命中 | `pkg/cache/affinity.go` |
| 7 | TPM 未实现 | 无 Token 级限流 | `internal/ratelimit/ratelimit.go` |
| 8 | Gemini streaming 未适配 | 流式响应格式错误 | `pkg/relay/gemini.go` |
| 9 | `pool.ActiveKeys` 不同步 | 计数器不准确 | `pkg/keypool/manager.go` |
| 10 | 健康检查只清理 cooldown key | 1-2 次失败永不清理 | `pkg/keypool/manager.go:959-1021` |

---

## 八、架构层面问题

1. **Affinity Cache 目前是个"半吊子"** — 写了但不读，或者读写 key 不一致。要么修复让它真正影响路由，要么暂时移除减少复杂度。

2. **Portal sync 重置 runtime 状态** — 这是一个设计冲突。如果 sync 间隔是 60 秒，那么 60 秒 cooldown 几乎无效。建议 sync 时合并状态而非完全重建。

3. **TPM 限速缺失** — 对于 Token 密集型应用（如长文本模型），这是必须的。

4. **Key 选择没有重试** — 如果选中的 key 失败了，当前请求直接 502，不会自动尝试同 provider 的其他 key。

---

## 九、Portal 代码实际实现深度分析

> 以下分析基于对 `ai-portal-go` 源代码的直接阅读（`internal/routes/sync.go`、`admin.go`、`models.go`、迁移文件等），与 Portal 侧设计文档做交叉验证。

### 9.1 Sync 接口实际实现 (`internal/routes/sync.go`)

#### 9.1.1 认证方式

Portal sync 端点支持三种认证方式（按优先级）：
1. `X-Admin-Secret` header
2. `x-proxy-secret` header
3. `secret` query 参数

全部对比 `r.cfg.Portal.SyncSecret`。

#### 9.1.2 Key Pool Sync 返回结构

```go
type SyncKeyResponse struct {
    ID             string   `json:"id"`
    Provider       string   `json:"provider"`
    Key            string   `json:"key"`           // ← 已解密的上游密钥
    KeyPreview     string   `json:"key_preview"`
    Tier           string   `json:"tier"`
    Priority       string   `json:"priority"`      // ← 从 tier 推导
    BaseURL        string   `json:"baseUrl,omitempty"`
    Models         []string `json:"models,omitempty"`
    Enabled        bool     `json:"enabled"`
    AssignmentMode string   `json:"assignmentMode"`
    IsShared       bool     `json:"isShared"`      // ← assignmentMode != "exclusive"
}
```

**Priority 推导规则：**
- `premium` / `enterprise` → `"high"`
- `economy` → `"economy"`
- 其他 → `"normal"`

**Proxy 过滤规则：**
- 只同步 `enabled = true` 的 key
- `exclusive` 模式且指定了 `assigned_proxy` 的 key：如果请求带了 `proxyId` 参数，只返回匹配该 proxyId 的 key

**解密逻辑：**
- 使用 `crypto.Decrypt()`（AES-256-GCM）解密 `key_encrypted`
- 如果解密失败，**fallback 到原始加密值**（这可能导致 Proxy 收到无效的密钥字符串）
- 如果没有配置 `ENCRYPTION_KEY`，直接发送原始存储值

#### 9.1.3 Providers 同步

Portal 在 sync 响应中确实返回了 `providers` 数组：
```go
providers = append(providers, gin.H{
    "id": id, "name": name, "baseUrl": baseURLStr, "enabled": enabled,
})
```

**但 Proxy 的 `updatePoolsFromSync()` 只消费 `keys`，完全忽略 `providers`。**

#### 9.1.4 Models Sync 返回结构

```go
{
    "id":           string,
    "provider":     string,
    "productId":    string,      // ← 用户可见的模型ID
    "apiModelName": string,      // ← 实际调用上游时使用的模型名
    "status":       string,
    "inputPrice":   *float64,
    "outputPrice":  *float64,
    "aimodelsData": interface{},
    "showInStore":  bool,
}
```

**Proxy 的 `syncModels()` 消费了这个端点，但只用于 `allowed_models` 检查，没有做 `productId` → `apiModelName` 的映射转换。**

### 9.2 `upstream_keys` 实际表结构

```sql
CREATE TABLE IF NOT EXISTS upstream_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider        VARCHAR(50) NOT NULL,
    key_encrypted   TEXT NOT NULL,
    key_preview     VARCHAR(20) NOT NULL,
    tier            VARCHAR(20) DEFAULT 'default',
    base_url        TEXT,
    models          JSONB,                    -- ← Key 支持的模型列表
    notes           TEXT,
    enabled         BOOLEAN DEFAULT true,
    metadata        JSONB,
    assignment_mode VARCHAR(20) DEFAULT 'exclusive'  -- ← 默认 exclusive！
        CHECK (assignment_mode IN ('exclusive', 'shared', 'unassigned')),
    assigned_proxy  VARCHAR(50),
    allowed_regions JSONB,
    last_synced_at  TIMESTAMP,
    created_at      TIMESTAMP DEFAULT NOW(),
    updated_at      TIMESTAMP DEFAULT NOW()
);
```

**202605051330 迁移追加的字段：**
```sql
rpm_limit INT,
tpm_limit INT,
quota_limit DECIMAL(10,2),
validation_status VARCHAR(20) DEFAULT 'pending',
validation_at TIMESTAMP
```

### 9.3 设计文档 vs 实际代码偏差

#### 9.3.1 `assignment_mode` 默认值不一致

| 来源 | 默认值 |
|---|---|
| `upstream-keypool-management-202605051321.md` 设计文档 | `'shared'` |
| 实际代码 + 迁移约束 | `'exclusive'` |

**影响：** 设计意图是所有 key 默认共享，但实际每个 key 默认独占。这会导致多 Proxy 部署时，如果不手动把 assignment_mode 改成 `shared`，大部分 key 不会被其他 Proxy 看到。

#### 9.3.2 Provider Base URL 多处不一致

| Provider | 设计文档 | `providerDefaultURLs` 代码 | `DefaultProviderConfigs` (Proxy) |
|---|---|---|---|
| meta | `https://api.llama.meta.com/v1` | `https://llama-api.meta.com/v1` | `https://api.llama.com/compat/v1` |
| microsoft | `https://api.openai.azure.com/v1` | `https://azure-openai.azure.com` | `https://azure-openai.azure.com/openai/deployments` |
| tencent | `https://api.hunyuan.cloud.tencent.com/v1` | `https://hunyuan.tencentcloudapi.com` | `https://api.hunyuan.cloud.tencent.com/v1` |
| baidu | `https://aip.baidubce.com/rpc/2.0/ai_custom/v1` | `https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop` | `https://qianfan.baidubce.com/v2` |
| xai | `https://api.x.ai/v1` | key 为 `'x-ai'`（非 `'xai'`） | `https://api.x.ai/v1` |
| alibaba | `https://dashscope.aliyuncs.com/compatible-mode/v1` | 一致 | 一致 |
| cohere | `https://api.cohere.ai/v1` | 一致 | `https://api.cohere.ai/compatibility/v1` |

**关键发现：**
- Portal 的 `providerDefaultURLs` 中 **xai 的 key 是 `'x-ai'`**，但 Proxy 的 `GetProviderFromModel()` 和 `DefaultProviderConfigs` 中用的都是 `'xai'`。**这会导致 Portal 默认 URL 查找不到 xai 的 provider。**
- Portal 设计文档和代码之间、Portal 代码和 Proxy 代码之间，**meta / microsoft / tencent / baidu 的 base URL 全部不一致**。
- Portal 初始化迁移 `009_seed_providers.sql` 中的 base_url 与 `providerDefaultURLs` 又不完全一致。

#### 9.3.3 Provider 列表不一致

| Provider | Portal `providers` 表 | Proxy `DefaultProviderConfigs` | Portal `providerDefaultURLs` |
|---|---|---|---|
| openai | ✅ | ✅ | ✅ |
| anthropic | ✅ | ✅ | ✅ |
| google | ✅ | ✅ | ✅ |
| meta | ✅ | ✅ | ✅ |
| mistral | ✅ | ✅ | ✅ |
| cohere | ✅ | ✅ | ✅ |
| amazon | ✅ | ✅ | ✅ |
| microsoft | ✅ | ✅ | ✅ |
| xai | ✅ | ✅ | ❌ (key='x-ai') |
| deepseek | ✅ | ✅ | ✅ |
| alibaba | ✅ | ✅ | ✅ |
| moonshot | ✅ | ✅ | ✅ |
| zhipu | ✅ | ✅ | ✅ |
| bytedance | ✅ | ✅ | ✅ |
| tencent | ✅ | ✅ | ✅ |
| minimax | ✅ | ✅ | ✅ |
| baidu | ✅ | ✅ | ✅ |
| perplexity | ✅ | ✅ | ✅ |
| **azure** | ✅ | ❌ | ❌ |
| **openrouter** | ✅ | ❌ | ❌ |
| **groq** | ✅ | ❌ | ✅ |
| **xiaomi** | ❌ | ❌ | ✅ |

Portal `providers` 表有 18+ 家（含 azure, openrouter），但 Proxy 只有 18 家。如果 Portal 配置了 azure/openrouter 的 key，Proxy 会找不到对应的 adaptor。

#### 9.3.4 `openrouter_models` 表缺失

设计文档中描述了 `openrouter_models` 表，但 **迁移文件中没有任何创建该表的 SQL**。代码中引用了这个表（`admin.go:syncOpenRouterLive` 和 `addOpenRouterModelToService`），如果数据库是新创建的，这些功能会运行时出错。

### 9.4 Portal 侧明显 Bug

| # | Bug | 影响 | 位置 |
|---|---|---|---|
| P-1 | `trimSpace` 是 no-op：`func trimSpace(s string) string { return s }` | 导入时不会去除 key 前后空格，可能导致解密失败或认证失败 | `internal/routes/admin.go` |
| P-2 | Legacy web handler 使用错误列名 `api_key_encrypted` 且**不加密**存储 | 通过旧 Web UI 添加的 key 以明文存储且列名错误，可能导致后续查询失败 | `internal/web/admin_handlers.go` |
| P-3 | `providerDefaultURLs` 中 xai 的 key 是 `'x-ai'` 而非 `'xai'` | 默认 URL 查找失败 | `internal/routes/admin.go:1262-1284` |
| P-4 | `providerDefaultURLs` 中 meta 的 URL 是 `https://llama-api.meta.com/v1` 而非设计文档的 `https://api.llama.meta.com/v1` | 与实际 Llama API 地址不一致 | `internal/routes/admin.go` |
| P-5 | `providerDefaultURLs` 中 tencent 的 URL 是 `https://hunyuan.tencentcloudapi.com` 而非 `https://api.hunyuan.cloud.tencent.com/v1` | 与实际腾讯混元 API 地址不一致 | `internal/routes/admin.go` |
| P-6 | `getKeyPool` 返回大量重复字段（camelCase + snake_case + 首字母大写） | API 响应冗余，增加带宽 | `internal/routes/admin.go:458-562` |
| P-7 | `createUpstreamKey` 默认 `assignment_mode = "exclusive"`，与设计文档的 `shared` 矛盾 | 多 Proxy 场景下 key 默认不可共享 | `internal/routes/admin.go:219-271` |
| P-8 | 没有 HTTP 级别的集成测试 | 关键 API（sync、keypool CRUD、import/export）无自动化测试覆盖 | 整个 `internal/routes/` |
| P-9 | `openrouter_models` 表在迁移中未创建 | OpenRouter 同步功能在新数据库上会崩溃 | 迁移文件缺失 |
| P-10 | `getKeyPool` 不查询 `key_pool_status` 表 | 前端看不到 Proxy 上报的 key 使用量、RPM 等动态数据 | `internal/routes/admin.go:458-562` |

### 9.5 Portal ↔ Proxy 数据流对齐矩阵

| 数据 | Portal 输出 | Proxy 消费 | 状态 |
|---|---|---|---|
| `keys[].key` (已解密上游密钥) | ✅ sync 发送 | ✅ `SyncKey.Key` 接收 | ✅ 对齐 |
| `keys[].provider` | ✅ | ✅ | ✅ 对齐 |
| `keys[].priority` (从 tier 推导) | ✅ | ✅ `GetKeyByPriority` | ✅ 对齐 |
| `keys[].baseUrl` | ✅ | ✅ `upstreamKey.BaseURL` | ✅ 对齐 |
| `keys[].models` (key 支持的模型) | ✅ | ❌ **Proxy 未按模型过滤 key** | ❌ 偏差 |
| `keys[].assignmentMode` | ✅ | ❌ **Proxy 无此概念** | ❌ 偏差 |
| `keys[].isShared` | ✅ | ❌ **Proxy 无此概念** | ❌ 偏差 |
| `keys[].tier` | ✅ | ❌ **Proxy 未用于 RPM/TPM 限额** | ❌ 偏差 |
| `keys[].enabled` | ✅ (只发 enabled=true) | ✅ | ✅ 对齐 |
| `providers[]` (baseUrl, enabled) | ✅ | ❌ **Proxy 完全忽略** | ❌ 偏差 |
| `models[].productId` | ✅ models/sync | ❌ **Proxy 未做映射** | ❌ 偏差 |
| `models[].apiModelName` | ✅ models/sync | ❌ **Proxy 直接发 productId 给上游** | ❌ 偏差 |
| `models[].provider` | ✅ | ✅ `GetProviderFromModel` fallback | ⚠️ 部分对齐 |
| `rpm_limit` / `tpm_limit` | ✅ 表中有 | ❌ **Proxy 未 enforced** | ❌ 偏差 |
| `validation_status` | ✅ 表中有 | ❌ **Proxy 未过滤** | ❌ 偏差 |
| `quota_limit` | ✅ 表中有 | ❌ **Proxy 未使用** | ❌ 偏差 |

---

## 十、改进思路与实施方案

### 10.1 立即修复（P0）

#### 10.1.1 Proxy 消费 Portal 的 `providers[]` 数据

**意图**：消除 Proxy 硬编码的 `DefaultProviderConfigs`，让 Portal 成为 Provider 配置的唯一来源。

**方案**：
1. Proxy `SyncKey` 结构体添加 `BaseURL` 字段（已有）
2. `updatePoolsFromSync()` 在解析 `keys` 的同时，解析 `providers` 数组
3. 构建 `providerConfigs` map（provider ID → ProviderConfig），覆盖或替代 `DefaultProviderConfigs`
4. 如果 Portal 未返回 providers（兼容旧版本），fallback 到 `DefaultProviderConfigs`

**代码变更**：`pkg/keypool/manager.go` 的 `updatePoolsFromSync()`

#### 10.1.2 Model 名称映射（`productId` → `apiModelName`）

**意图**：用户请求中的模型名是 `productId`（如 Portal 定义的 `gpt-4o`），但上游实际需要的是 `apiModelName`（如 OpenAI 的 `gpt-4o-2024-08-06`）。

**方案**：
1. Proxy 在 `syncModels()` 时，构建 `modelMapping` map：`productId` → `apiModelName`
2. 在 `handleChatCompletions()` 中，在发送给 adaptor 之前，将 `req.Model` 从 `productId` 转换为 `apiModelName`
3. 如果 `apiModelName` 为空，fallback 到 `productId`（向后兼容）

**代码变更**：`internal/server/handlers.go` + `pkg/keypool/manager.go`

#### 10.1.3 Key 的 `models` 过滤

**意图**：如果 key 的 `models` 字段非空，只将该 key 用于支持该模型的请求。

**方案**：
1. 在 `GetKeyByPriority()` 的过滤逻辑中，增加一步：
   ```go
   if len(key.Models) > 0 && !slices.Contains(key.Models, reqModel) {
       continue
   }
   ```
2. `SyncKey` / `KeyInternal` 结构体需要保留 `Models []string`
3. 需要 Go 1.21+ 的 `slices` 包，或自己写 contains 函数

**代码变更**：`pkg/keypool/manager.go`

#### 10.1.4 修复 6 个 Key Pool 核心 Bug

| Bug | 修复方案 | 文件 |
|---|---|---|
| RLock 写 `LastUsed` | `RLock()` → `Lock()`，`RUnlock()` → `Unlock()` | `manager.go:536` |
| Failure double count | `UpdateKeyUsage` 中移除 `Failures++` | `manager.go` + `handlers.go` |
| Sync 重置 runtime 状态 | `updatePoolsFromSync` 改为 merge：保留旧 pool 的 runtime 字段，只更新静态字段 | `manager.go:353-405` |
| Redis load 丢失 `internalKeys` | 使用自定义序列化结构（exportable），或改用 Redis Hash 分字段存储 | `manager.go:1081-1108` |
| `persistToRedis` data race | 在 lock 内完成 `json.Marshal` 后再 `Set` | `manager.go:1056-1078` |
| `ActiveKeys` 不同步 | 在 `ReportKeyFailure`（key 变 inactive）和恢复逻辑中增减计数 | `manager.go` |

### 10.2 重要改进（P1）

#### 10.2.1 TPM 限速实现

**意图**：当前只有 RPM 限速，Token 密集型场景无保护。

**方案**：
1. 扩展 token bucket，每个 bucket 同时跟踪 RPM 和 TPM
2. 从 upstream key 的 `rpm_limit` / `tpm_limit` 读取限额（而非当前硬编码的 8 档）
3. `Allow(keyHash, tokens int)` 同时消耗 request count 和 token count
4. 超额时返回 429

**代码变更**：`internal/ratelimit/ratelimit.go`

#### 10.2.2 `validation_status` 过滤

**意图**：`pending` / `invalid` 的 key 不应该被选中用于生产流量。

**方案**：
1. Portal sync 响应中增加 `validationStatus` 字段（当前 `SyncKeyResponse` 没有）
2. Proxy 在 `GetKeyByPriority` 的 `IsKeyAvailable` 中过滤掉 `validation_status != "valid"` 的 key
3. 或者 Portal 在 sync 时就不发送 non-valid key（当前 Portal 只过滤 `enabled = true`，不过滤 validation_status）

**推荐**：Portal 侧在 sync 查询中增加 `AND validation_status = 'valid'`，这样 invalid key 根本不会被同步到 Proxy。

#### 10.2.3 Assignment Mode / 多 Proxy 支持

**意图**：Portal 已经支持 `exclusive` / `shared` / `unassigned`，Proxy 需要消费这些字段。

**方案**：
1. Proxy 启动时配置自己的 `proxyId`
2. sync 请求中带上 `?proxyId=proxy-a`
3. `GetKeyByPriority` 无需改动（Portal 已经过滤了）
4. 但 Proxy 的 Redis fallback（`loadFromRedis`）需要知道哪些 key 是分配给它的

**代码变更**：`pkg/keypool/manager.go` + Proxy 配置

### 10.3 架构层面建议（P2）

#### 10.3.1 统一 Provider Base URL 来源

**当前问题**：Portal `providers` 表、`providerDefaultURLs` map、设计文档、Proxy `DefaultProviderConfigs` 四处的 base URL 各不相同。

**建议**：
1. **Portal `providers` 表是唯一来源**
2. 删除 Portal `providerDefaultURLs` 硬编码 map，改为从 `providers` 表查询
3. Proxy 消费 Portal sync 的 `providers[]`，不再硬编码任何 base URL
4. 所有 Provider 的 base_url 由 Admin 在 Portal 中统一管理

#### 10.3.2 Affinity Cache 重构

**当前问题**：读写 key 不一致，且不影响路由决策。

**建议**：
1. 统一 key 格式：`"priority:model:group:sha1(body)[:8]"`
2. `GetChannelWithPriority` 返回的 channel ID 应**参与路由决策**：如果有 affinity match，优先使用该 key
3. 增加 `maxEntries` LRU 淘汰（当前 100K 上限未 enforced）

#### 10.3.3 Key 级别重试机制

**当前问题**：选中的 key 失败即返回 502，不会自动尝试同 provider 的其他 key。

**建议**：
1. 在 `handleChatCompletions` 中增加一次同 provider 的 key 重试
2. 重试条件：非 4xx 错误（5xx、timeout、connection error）
3. 重试时排除已失败的 key，从剩余可用 key 中重新选择

#### 10.3.4 统一 Provider ID 命名

**当前问题**：Portal 有 `'x-ai'`（providerDefaultURLs）、Proxy 有 `'xai'`（DefaultProviderConfigs + GetProviderFromModel）。

**建议**：统一为 `'xai'`（与 providers 表 ID 一致），修正 `providerDefaultURLs` 的 key。

#### 10.3.5 `openrouter_models` 表迁移

Portal 需要补一个迁移文件创建 `openrouter_models` 表，否则 OpenRouter 同步功能无法使用。

---

## 十一、实施优先级建议

| 优先级 | 事项 | 预估工作量 | 影响范围 |
|---|---|---|---|
| **P0-1** | 修复 RLock 写 race | 5min | `manager.go` |
| **P0-2** | 修复 Failure double count | 10min | `manager.go` + `handlers.go` |
| **P0-3** | 修复 persistToRedis data race | 10min | `manager.go` |
| **P0-4** | 修复 sync 重置 runtime 状态 | 2h | `manager.go` |
| **P0-5** | Proxy 消费 providers[] | 2h | `manager.go` + `providers.go` |
| **P0-6** | Model 名称映射 | 2h | `handlers.go` + `manager.go` |
| **P0-7** | Key models 过滤 | 1h | `manager.go` |
| **P1-1** | TPM 限速 | 4h | `ratelimit.go` |
| **P1-2** | validation_status 过滤 | 1h | Portal `sync.go` + Proxy `manager.go` |
| **P1-3** | ActiveKeys 同步修复 | 30min | `manager.go` |
| **P1-4** | Redis load 修复 | 3h | `manager.go` |
| **P2-1** | Affinity Cache 重构 | 4h | `affinity.go` |
| **P2-2** | Key 级别重试 | 3h | `handlers.go` |
| **P2-3** | 统一 Provider ID / Base URL | 2h | Portal + Proxy |
| **P2-4** | 多 Proxy assignment_mode | 4h | `manager.go` + 配置 |

---

*文档结束。以上分析基于 `ai-proxy-go` 和 `ai-portal-go` 源代码的直接阅读，以及 Portal 侧设计文档的交叉验证。*
