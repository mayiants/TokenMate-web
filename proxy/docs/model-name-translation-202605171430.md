> **ACTIVE** — Model Name Translation & Provider-Agnostic Routing

# Model Name Translation & Provider-Agnostic Routing

**文档编号:** model-name-translation-202605171430
**创建时间:** 2026-05-17 14:30
**状态:** Phase 1 ✅ 已实现 | Phase 2 ✅ 已实现 | Phase 3 待实现 | Phase 4 待实现

---

## 1. 问题

### 1.1 核心问题：同一逻辑模型在不同 provider 有不同的名字

用户请求 `claude-opus-4-6`，但不同 provider 认识的名字完全不同：

| Provider | 该 provider 的 model name | 差异类型 |
|----------|--------------------------|----------|
| anthropic (直连) | `claude-opus-4-6` | 无差异 |
| accessapi | `claude-opus-4-6` | 无差异 |
| openrouter | `claude-opus-4.6` | 点号 vs 短横线 |
| provider-x | `claude-opus-4-6-20260512` | 日期后缀 |
| provider-y | `claude-opus-4-6-20251201-thinking` | 日期 + thinking 后缀 |

当前 Proxy 的 `resolveProviderAndKey` 只返回 `(provider, key)`，**不返回应该发给上游的 model name**。handler 层直接把用户请求的 `req.Model` 原样发送给上游，导致上游不认识这个名字，返回 400/403。

### 1.2 当前架构的局限

**现状**：`service_models` 表的 UNIQUE 约束是 `(provider, product_id)`，即"每个 provider 的每个模型名唯一"。这意味着：

- 同一个逻辑模型（如 claude-opus-4-6）在不同 provider 下是**不同的记录**
- 但当前只有 anthropic 有 claude 的 service_model 记录，accessapi **没有**
- `api_model_name` 字段虽然存在，但没有被 Proxy 使用

**Proxy 侧**：
- `resolveProviderAndKey` 用 `relay.GetProviderFromModel(model)` 硬编码推断 provider（如 `claude-*` → `anthropic`）
- key 的 `Models` 列表存的是该 provider 侧的名字（如 accessapi key 的 models 是 `["claude-opus-4-6"]`）
- `contains()` 函数只做了点号/短横线归一化，无法处理日期后缀和 thinking 等变体

### 1.3 连锁问题：Admin 改 model name 后用户 key 不更新

`api_keys.selected_models` 和 `user_groups.models` 存的是 model name 字符串。Admin 在 `service_models` 中修改 `product_id` 或 `api_model_name` 后，已创建的 key/group 中的名字不会自动更新。

当前数据示例：
```
user_groups.models = ["claude-opus-4.5", "claude-opus-4.6", ...]  -- OpenRouter 格式
api_keys.selected_models = ["claude-opus-4.7", "claude-opus-4.7-fast", ...]  -- 混合格式
```

### 1.4 Admin 维护 model name 的工作量问题

不同 provider 的 model name 差异很大，Admin 手动为每个 provider 维护 `product_id` 不现实。需要自动化手段获取各 provider 实际支持的 model name。

---

## 2. 核心思路：`api_model_name` 成为唯一稳定标识

**关键转变**：`api_model_name` 从"provider 的 API 模型名"升级为**跨 provider 的统一模型标识**。

| 概念 | 之前 | 之后 |
|------|------|------|
| `api_model_name` | provider 侧的 API 名（可变） | **用户侧统一标识**（稳定，不轻易改） |
| `product_id` | OpenRouter 的 product ID | **该 provider 实际调用的 model name** |
| `provider` | 路由依据（硬编码映射） | key pool 分组标签（仅用于 key 管理） |
| `service_models` UNIQUE | `(provider, product_id)` | `(provider, product_id)` 不变，但同一 `api_model_name` 可跨多 provider |

**数据示例**（改造后）：

| provider | product_id（发给上游的） | api_model_name（统一标识） |
|----------|--------------------------|------------------------------|
| anthropic | `claude-opus-4-6` | `claude-opus-4-6` |
| accessapi | `claude-opus-4-6` | `claude-opus-4-6` |
| openrouter | `claude-opus-4.6` | `claude-opus-4-6` |
| provider-x | `claude-opus-4-6-20260512` | `claude-opus-4-6` |
| provider-y | `claude-opus-4-6-20251201-thinking` | `claude-opus-4-6` |

用户始终用 `api_model_name`（如 `claude-opus-4-6`），Proxy 转发时替换为对应 provider 的 `product_id`。

---

## 3. 实施方案

### 3.1 Phase 1：Proxy 模型名翻译层 — ✅ 已实现

**目标**：`resolveProviderAndKey` 返回 `upstreamModelName`，handler 层替换 `req.Model`。

#### 3.1.1 数据来源：key 的 Models 列表

当前 key 的 `Models` 列表已经存了该 provider 认识的 model name。例如 accessapi key 的 models 是 `["claude-opus-4-6", "claude-opus-4-6-thinking", ...]`。

**思路**：`resolveProviderAndKey` 选中某个 key 后，从该 key 的 `Models` 列表中找到与用户请求的 `api_model_name` 匹配的那个名字，作为 `upstreamModelName`。

#### 3.1.2 改动：`contains` 函数返回匹配到的名字

```go
// containsMatch checks if a string slice contains a given string.
// Returns (matched bool, matchedName string).
// Also matches with dot/hyphen normalization and prefix matching.
func containsMatch(slice []string, item string) (bool, string) {
    normalized := normalizeModelName(item)
    for _, s := range slice {
        // 精确匹配
        if s == item {
            return true, s
        }
        // 归一化匹配 (claude-opus-4.6 ↔ claude-opus-4-6)
        if normalizeModelName(s) == normalized {
            return true, s
        }
    }
    // 前缀匹配 (claude-opus-4-6 → claude-opus-4-6-20260512)
    // 只在归一化后 item 是 s 的前缀时匹配
    for _, s := range slice {
        normS := normalizeModelName(s)
        if strings.HasPrefix(normS, normalized + "-") {
            return true, s
        }
    }
    return false, ""
}
```

#### 3.1.3 改动：`GetAllKeysForModel` 返回匹配的 upstream model name

`CandidateKey` 增加 `UpstreamModelName` 字段：

```go
type CandidateKey struct {
    Provider           string
    Key                *KeyInternal
    UpstreamModelName  string  // 该 provider 实际调用的 model name
}
```

`GetAllKeysForModel` 在 `contains` 匹配时记录匹配到的名字：

```go
if len(key.Models) > 0 {
    matched, matchedName := containsMatch(key.Models, model)
    if !matched {
        continue
    }
    ck := &CandidateKey{Provider: provider, Key: key, UpstreamModelName: matchedName}
} else {
    // key.Models 为空 = 支持所有模型，upstream name = 用户请求的 name
    ck := &CandidateKey{Provider: provider, Key: key, UpstreamModelName: model}
}
```

#### 3.1.4 改动：`resolveProviderAndKey` 返回 upstreamModelName

```go
func resolveProviderAndKey(ctx context.Context, model, userPriority, preferredKeyID string) (provider string, upstreamKey *keypool.KeyInternal, upstreamModelName string, err error) {
    // ... 现有逻辑 ...
    return selected.Provider, selected.Key, selected.UpstreamModelName, nil
}
```

#### 3.1.5 改动：handler 层替换 req.Model

```go
provider, upstreamKey, upstreamModelName, err := resolveProviderAndKey(...)
// ...
// 替换 model name 为该 provider 实际认识的名字
req.Model = upstreamModelName
providerReq := adaptor.ToProvider(req)
```

**影响范围**：5 个 handler 函数都需要改：
- `handleChatCompletions` (line 399)
- `handleEmbeddings` (line 957)
- `handleImageGenerations` (line 1060)
- `handleAudioTranscriptions`
- `handleAudioTranslations`

#### 3.1.6 前缀匹配的歧义处理

前缀匹配可能产生歧义：`claude-opus-4-6` 可能匹配到 `claude-opus-4-6` 和 `claude-opus-4-6-thinking`。

**规则**：
1. 精确匹配优先（`claude-opus-4-6` 精确匹配 `claude-opus-4-6`）
2. 归一化精确匹配次之（`claude-opus-4.6` 归一化后匹配 `claude-opus-4-6`）
3. 前缀匹配最后（只在无精确匹配时使用）
4. 前缀匹配选**最短**的那个（`claude-opus-4-6` 优先于 `claude-opus-4-6-20260512`）

**注意**：`-fast` 和 `-thinking` 的映射（如 anthropic 的 `claude-opus-4-6-fast` 对应 accessapi 的 `claude-opus-4-6-thinking`）**无法通过字符串规则解决**。这需要 Phase 2 的 `service_models` 映射表来处理。Phase 1 的前缀匹配只能处理"无后缀 → 有日期后缀"的情况。

### 3.2 Phase 2：service_models 多 provider 映射 — ✅ 已实现

**目标**：每个 provider 对同一逻辑模型有独立的 `service_model` 记录，`product_id` 存该 provider 实际调用的名字。

#### 3.2.1 service_models 数据变化

当前只有 anthropic 有 claude 的记录。需要为 accessapi 也添加：

```sql
-- 为 accessapi 添加 claude 模型记录
INSERT INTO service_models (provider, product_id, api_model_name, status, input_price, output_price, show_in_store)
VALUES
  ('accessapi', 'claude-opus-4-6', 'claude-opus-4-6', 'active', 15.0, 75.0, true),
  ('accessapi', 'claude-opus-4-6-thinking', 'claude-opus-4-6-fast', 'active', 15.0, 75.0, true),
  ('accessapi', 'claude-opus-4-7', 'claude-opus-4-7', 'active', 15.0, 75.0, true),
  ('accessapi', 'claude-sonnet-4-6', 'claude-sonnet-4-6', 'active', 3.0, 15.0, true),
  ('accessapi', 'claude-sonnet-4-6-thinking', 'claude-sonnet-4-6-fast', 'active', 3.0, 15.0, true);
```

注意 `claude-opus-4-6-thinking` 的 `api_model_name` 是 `claude-opus-4-6-fast`——这就是 `-thinking` → `-fast` 的映射。

#### 3.2.2 Portal sync 传递映射数据

当前 `syncKeyPool` 已经返回 `model_routing`。增加 `model_name_map`：

```json
{
  "model_name_map": [
    {"provider": "anthropic", "product_id": "claude-opus-4-6", "api_model_name": "claude-opus-4-6"},
    {"provider": "accessapi", "product_id": "claude-opus-4-6", "api_model_name": "claude-opus-4-6"},
    {"provider": "accessapi", "product_id": "claude-opus-4-6-thinking", "api_model_name": "claude-opus-4-6-fast"},
    {"provider": "openrouter", "product_id": "claude-opus-4.6", "api_model_name": "claude-opus-4-6"}
  ]
}
```

#### 3.2.3 Proxy 消费映射数据

Proxy 的 keypool Manager 增加 `ModelNameMap`：

```go
// ModelNameMap: (provider, apiModelName) → productId
type ModelNameMap map[string]map[string]string  // provider → apiModelName → productId

func (m *Manager) ResolveUpstreamModelName(provider, apiModelName string) string {
    if m.modelNameMap != nil {
        if providerMap, ok := m.modelNameMap[provider]; ok {
            if productId, ok := providerMap[apiModelName]; ok {
                return productId
            }
        }
    }
    return apiModelName  // fallback: 无映射时原样返回
}
```

`resolveProviderAndKey` 优先使用 `ModelNameMap`，fallback 到 Phase 1 的 `containsMatch`。

#### 3.2.4 Admin UI 变化

**admin-models 页面**：
- 当前：模型列表按 provider 分组，每个模型属于一个 provider
- 改后：模型列表按 `api_model_name` 分组，同一 `api_model_name` 下展示多个 provider 的 `product_id`
- 新增"从 Provider 同步模型名"按钮（Phase 3）

**admin-keypool 页面**：
- key 的 `Models` 列表改为**只读**（从 provider 的 `/v1/models` 自动同步，不再手动编辑）
- 或者保持手动编辑但增加"从 Provider 同步"按钮

### 3.3 Phase 3：Provider Model List API 自动发现 — ⏳ 待实现

**目标**：Admin 一键拉取 provider 的模型列表，自动建议 `product_id`。

#### 3.3.1 API

```
POST /admin/models/verify-provider-models
Body: { "provider": "accessapi" }

Response: {
  "provider": "accessapi",
  "remote_models": ["claude-opus-4-6", "claude-opus-4-6-thinking", ...],
  "suggestions": [
    {
      "api_model_name": "claude-opus-4-6",
      "current_product_id": null,        // accessapi 还没这条记录
      "match_type": "exact",             // exact | normalized | prefix | none
      "suggested_product_id": "claude-opus-4-6",
      "candidates": []
    },
    {
      "api_model_name": "claude-opus-4-6-fast",
      "current_product_id": null,
      "match_type": "prefix",
      "suggested_product_id": "claude-opus-4-6-thinking",
      "candidates": ["claude-opus-4-6-thinking"]  // 前缀匹配的候选
    },
    {
      "api_model_name": "claude-opus-4-5",
      "current_product_id": null,
      "match_type": "prefix",
      "suggested_product_id": "claude-opus-4-5-20251101",
      "candidates": ["claude-opus-4-5-20251101", "claude-opus-4-5-20251101-thinking"]
    }
  ]
}
```

#### 3.3.2 匹配算法

对 `service_models` 中每个 `api_model_name`，在 provider 返回的 remote models 中查找：

1. **精确匹配**：`remote == api_model_name` → `match_type: "exact"`
2. **归一化匹配**：`normalize(remote) == normalize(api_model_name)` → `match_type: "normalized"`
3. **前缀匹配**：`normalize(remote)` 以 `normalize(api_model_name) + "-"` 开头 → `match_type: "prefix"`，列出所有候选
4. **无匹配**：`match_type: "none"`，该 provider 可能不支持此模型

#### 3.3.3 Provider Models API 适配

| Provider | API | 格式 |
|----------|-----|------|
| OpenAI-compatible (accessapi, deepseek, moonshot, ...) | `GET {base_url}/v1/models` | `{data: [{id: "..."}]}` |
| Anthropic | `GET https://api.anthropic.com/v1/models` | `{data: [{id: "..."}]}` |
| Google Gemini | `GET https://generativelanguage.googleapis.com/v1beta/models?key=xxx` | `{models: [{name: "..."}]}` |
| AWS Bedrock | 无标准 API | 需手动配置 |

#### 3.3.4 Admin UI

在 admin-models 页面增加"验证模型名"功能：
- 选择 provider → 调用 verify API
- 展示对比表：api_model_name | current product_id | suggested product_id | match_type
- Admin 逐个确认或调整
- 一键应用所有建议

### 3.4 Phase 4：selected_models 改用 api_model_name — ⏳ 待实现

**目标**：确保 `api_keys.selected_models` 和 `user_groups.models` 中存的是 `api_model_name`（统一标识），而非 `product_id`（provider 特定）。

**当前数据问题**：
```
user_groups.models = ["claude-opus-4.5", "claude-opus-4.6", ...]  -- OpenRouter 的 product_id 格式
api_keys.selected_models = ["claude-opus-4.7", "claude-opus-4.7-fast", ...]  -- 混合
```

**修复**：
1. 数据迁移：将 `selected_models` 和 `models` 中的 `product_id` 替换为对应的 `api_model_name`（详见 8.5 节迁移 SQL）
2. UI：checkbox 的 value 改为 `api_model_name`
3. Proxy 的 `resolveModel`：用户请求的 model name 通过 `api_model_name` 查找，不再依赖 `product_id`

### 3.5 OpenRouter 同步降级

**调整后，OpenRouter 同步的意义大幅降低**：

| 之前 | 之后 |
|------|------|
| OpenRouter 定期同步 → 自动创建/更新 `service_models` | Provider `/v1/models` 同步 → 自动填充 key 的 `models` 列表 |
| `product_id` 来自 OpenRouter | `product_id` 来自 provider `/v1/models` 的 model id |
| `api_model_name` 从 `product_id` 自动生成（点号→短横线） | `api_model_name` 由 Admin 创建模型时确定，稳定不变 |
| 定价来自 OpenRouter | 定价由 Admin 在 `service_models` 中手动设置（可参考 OpenRouter） |

**OpenRouter 保留的价值**：
- **参考定价**：偶尔看看 OpenRouter 的定价趋势，但不作为权威
- **新模型发现**：OpenRouter 有新模型时提醒 Admin，但实际可用性由 provider 决定
- **模型元数据**：`aimodels_data`（context window, capabilities）对 store 展示有参考价值

**建议**：OpenRouter 同步从"定期自动"降级为"Admin 手动触发"，频率大幅降低。

---

## 4. 改动范围汇总

### Phase 1（紧急）

| 仓库 | 文件 | 改动 |
|------|------|------|
| proxy | `pkg/keypool/manager.go` | `contains` → `containsMatch` 返回匹配名；`CandidateKey` 增加 `UpstreamModelName`；`GetAllKeysForModel` 记录匹配名 |
| proxy | `internal/server/handlers.go` | `resolveProviderAndKey` 返回 `upstreamModelName`；5 个 handler 替换 `req.Model` |

### Phase 2（中期）

| 仓库 | 文件 | 改动 |
|------|------|------|
| portal | `internal/routes/sync.go` | sync 响应增加 `model_name_map` |
| portal | `internal/routes/models.go` | Admin API 支持为同一 `api_model_name` 添加多 provider 记录 |
| portal | `web/templates/admin-models.html` | 模型列表按 `api_model_name` 分组展示 |
| proxy | `pkg/keypool/manager.go` | 增加 `ModelNameMap`，`ResolveUpstreamModelName()` |
| proxy | `internal/server/handlers.go` | `resolveProviderAndKey` 优先使用 `ModelNameMap` |

### Phase 3（长期）

| 仓库 | 文件 | 改动 |
|------|------|------|
| portal | `internal/routes/models.go` | 新增 `verifyProviderModels` API |
| portal | `web/templates/admin-models.html` | "验证模型名" UI |

### Phase 4（可选）

| 仓库 | 文件 | 改动 |
|------|------|------|
| portal | 迁移 SQL | `selected_models` / `models` 中 product_id → api_model_name |
| portal | `web/templates/apikeys.html` | checkbox value 改为 api_model_name |

---

## 5. 测试用例

### 5.1 Phase 1 测试

#### 场景 1：精确匹配（无翻译）

```
用户请求: claude-opus-4-6
选中 key: accessapi, models=["claude-opus-4-6", ...]
containsMatch 结果: (true, "claude-opus-4-6")
upstreamModelName: "claude-opus-4-6" (与请求相同，无需翻译)
预期: 请求成功，accessapi 返回正常响应
```

#### 场景 2：归一化匹配（点号→短横线）

```
用户请求: claude-opus-4.6 (来自 OpenRouter)
选中 key: accessapi, models=["claude-opus-4-6", ...]
containsMatch 结果: (true, "claude-opus-4-6")
upstreamModelName: "claude-opus-4-6" (翻译: 4.6 → 4-6)
预期: 请求成功，accessapi 收到 model="claude-opus-4-6"
```

#### 场景 3：前缀匹配（无后缀→有日期后缀）

```
用户请求: claude-opus-4-5
选中 key: provider-x, models=["claude-opus-4-5-20251101", "claude-opus-4-5-20251101-thinking"]
containsMatch 结果: (true, "claude-opus-4-5-20251101") (选最短前缀匹配)
upstreamModelName: "claude-opus-4-5-20251101"
预期: 请求成功，provider-x 收到 model="claude-opus-4-5-20251101"
```

#### 场景 4：key.Models 为空（支持所有模型）

```
用户请求: claude-opus-4-6
选中 key: some-provider, models=[]
upstreamModelName: "claude-opus-4-6" (原样返回)
预期: 请求发送 model="claude-opus-4-6"，由上游判断是否支持
```

#### 场景 5：无匹配

```
用户请求: gemini-2.5-pro
选中 key: accessapi, models=["claude-opus-4-6", ...] (无 gemini)
containsMatch 结果: (false, "")
预期: 该 key 被跳过，不会选到 accessapi
```

#### 场景 6：wgit-maas-latest 路由

```
用户请求: wgit-maas-latest → resolveModel → claude-opus-4-7
resolveProviderAndKey: provider=accessapi, upstreamModelName="claude-opus-4-7"
预期: 请求成功，accessapi 收到 model="claude-opus-4-7"
```

### 5.2 Phase 2 测试

#### 场景 7：ModelNameMap 精确翻译

```
ModelNameMap: (accessapi, claude-opus-4-6-fast) → claude-opus-4-6-thinking
用户请求: claude-opus-4-6-fast
resolveProviderAndKey: provider=accessapi
ResolveUpstreamModelName("accessapi", "claude-opus-4-6-fast") = "claude-opus-4-6-thinking"
预期: accessapi 收到 model="claude-opus-4-6-thinking"
```

#### 场景 8：ModelNameMap 无映射时 fallback

```
ModelNameMap: 无 (accessapi, some-new-model) 的映射
用户请求: some-new-model
ResolveUpstreamModelName("accessapi", "some-new-model") = "some-new-model" (原样返回)
预期: fallback 到 Phase 1 的 containsMatch 逻辑
```

### 5.3 Phase 3 测试

#### 场景 9：Provider Models API 验证

```
Admin 点击"验证 accessapi 模型名"
后端调用 GET https://provider.example.com/v1/models
返回 123 个模型，其中 20 个 claude
对 service_models 中的 api_model_name 做匹配:
  claude-opus-4-6 → exact match → product_id=claude-opus-4-6 ✅
  claude-opus-4-6-fast → prefix match → candidates=["claude-opus-4-6-thinking"] ⚠️
  claude-opus-4-5 → prefix match → candidates=["claude-opus-4-5-20251101", "claude-opus-4-5-20251101-thinking"] ⚠️
  claude-opus-4-7 → exact match → product_id=claude-opus-4-7 ✅
预期: Admin 看到对比表，确认建议的 product_id
```

---

## 8. 讨论结论（2026-05-17）

### 8.1 核心共识

1. **`api_model_name` 成为唯一稳定标识**，用户侧、group、selected_models 统一使用 `api_model_name`
2. **`provider` 保留为 key pool 分组标签**，继续使用小写公司名（anthropic、accessapi 等），不改动
3. **`product_id` 降级为 provider 侧的调用名**，仅 Proxy 转发时使用，用户不可见
4. **同一 `api_model_name` 可跨多 provider**，每个 provider 有独立的 `service_model` 记录

### 8.2 keypool 管理策略

**问题**：accessapi 20 个 key，每个 key 支持的模型子集不同（10 个全模型、5 个 20 种、5 个单一模型），模型交叉重叠。Admin 手动维护不现实。

**方案**：key 的 `models` 列表改为**自动同步**
- Admin 在 keypool 页面点"从 Provider 同步"
- 后端用该 key 调 provider 的 `/v1/models`
- 自动填充/更新 `models` 列表
- Admin 只需维护 key 本身，不需要手动编辑模型列表
- 定期自动同步（如每次 Portal sync 时顺便刷新 key 的 models）

### 8.3 group 简化

- group 只需选 `api_model_name`，不关心 provider
- Proxy 路由时自动找有该模型 key 的 provider
- UI：checkbox value 改为 `api_model_name`

### 8.4 计费细化

**问题**：同一 `api_model_name` 走不同 provider 成本不同（anthropic 直连 $15/$75，accessapi 转发 $18/$90）。

**方案**：
- `service_models` 表中每个 `(provider, api_model_name)` 组合已有独立的 `input_price` / `output_price`
- `usage_records` 已有 `upstream_key_id`，可反查 provider
- 计费按 `(api_model_name, provider)` 组合定价
- 用户侧看到的是 `api_model_name` 的统一价格（可取最低价或加权平均）
- Admin 侧可按 provider 查看实际成本

### 8.5 数据库迁移范围

**需要迁移的表**（有实际数据）：

| 表 | 字段 | 迁移内容 | 数据量 |
|---|---|---|---|
| `api_keys` | `selected_models` (jsonb) | 点号格式 → 短横线格式 (product_id → api_model_name) | 3 条 |
| `api_keys` | `current_model` (varchar) | 点号格式 → 短横线格式 | 3 条 |
| `user_groups` | `models` (jsonb) | 点号格式 → 短横线格式 | 2 条 |

**不需要迁移的表**：
- `upstream_keys.models` — 这是 provider 侧的名字（即 product_id），保持不变
- `usage_records.model` — 历史数据，可选迁移（不改也不影响功能）
- `upstream_key_usage.model` — 已经是正确格式
- 其他表（model_pricing, pricing_sources, model_products, orders, subscription_plans 等）— 当前为空

**迁移 SQL 示例**：
```sql
-- 将 api_keys.selected_models 中的点号格式替换为短横线格式
UPDATE api_keys
SET selected_models = (
    SELECT jsonb_agg(replace(elem::text, '.', '-')::jsonb)
    FROM jsonb_array_elements_text(selected_models) elem
)
WHERE selected_models IS NOT NULL
  AND selected_models != '[]'::jsonb;

-- 将 api_keys.current_model 中的点号替换为短横线
UPDATE api_keys
SET current_model = replace(current_model, '.', '-')
WHERE current_model IS NOT NULL AND current_model != '';

-- 将 user_groups.models 中的点号格式替换为短横线格式
UPDATE user_groups
SET models = (
    SELECT jsonb_agg(replace(elem::text, '.', '-')::jsonb)
    FROM jsonb_array_elements_text(models) elem
)
WHERE models IS NOT NULL
  AND models != '[]'::jsonb;
```

---

## 9. 风险评估

| 风险 | 说明 | 缓解 |
|------|------|------|
| 前缀匹配误匹配 | `claude-opus-4` 可能匹配到 `claude-opus-4-6` | 选最短匹配；Phase 2 用 ModelNameMap 替代 |
| `-fast` ↔ `-thinking` 映射 | 字符串规则无法解决 | Phase 2 的 ModelNameMap 显式映射 |
| key.Models 为空 | 所有 key 被当作支持所有模型 | 已有逻辑，Phase 1 不改 |
| Phase 1 到 Phase 2 过渡 | Phase 1 用 containsMatch，Phase 2 用 ModelNameMap | ModelNameMap 优先，containsMatch fallback |
| 数据迁移 | selected_models 中的 product_id 需要替换为 api_model_name | Phase 4 可选，Proxy 可同时接受两种格式 |

---

## 10. 实际数据参考

### 7.1 accessapi /v1/models 返回的 claude 模型（2026-05-17）

```
claude-3-5-sonnet-20240620
claude-3-haiku-20240307
claude-3-sonnet-20240229
claude-haiku-4-5-20251001
claude-haiku-4-5-20251001-thinking
claude-opus-4-1-20250805
claude-opus-4-1-20250805-thinking
claude-opus-4-20250514
claude-opus-4-20250514-thinking
claude-opus-4-5-20251101
claude-opus-4-5-20251101-thinking
claude-opus-4-6
claude-opus-4-6-thinking
claude-opus-4-7
claude-sonnet-4-20250514
claude-sonnet-4-20250514-thinking
claude-sonnet-4-5-20250929
claude-sonnet-4-5-20250929-thinking
claude-sonnet-4-6
claude-sonnet-4-6-thinking
```

### 7.2 当前 service_models 中的 claude 模型

| provider | product_id | api_model_name |
|----------|------------|----------------|
| anthropic | claude-opus-4.5 | claude-opus-4-5 |
| anthropic | claude-opus-4.6 | claude-opus-4-6 |
| anthropic | claude-opus-4.6-fast | claude-opus-4-6-fast |
| anthropic | claude-opus-4.7 | claude-opus-4-7 |
| anthropic | claude-opus-4.7-fast | claude-opus-4-7-fast |
| anthropic | claude-sonnet-4.5 | claude-sonnet-4-5 |
| anthropic | claude-sonnet-4.6 | claude-sonnet-4-6 |

### 7.3 当前 upstream_keys 中 accessapi 的 models

```json
["claude-opus-4-6", "claude-opus-4-6-thinking", "claude-sonnet-4-6-thinking", "claude-sonnet-4-6", "claude-opus-4-7"]
```

### 7.4 当前用户数据中的 model name

```
user_groups.models (accessapi group):
  ["claude-opus-4.5", "claude-opus-4.6", "claude-opus-4.6-fast", "claude-opus-4.7", "claude-opus-4.7-fast", "claude-sonnet-4.5", "claude-sonnet-4.6"]

api_keys.selected_models (accessapi key):
  ["claude-opus-4.7", "claude-opus-4.7-fast", "claude-opus-4.6", "claude-opus-4.6-fast", "claude-opus-4.5", "claude-sonnet-4.6", "claude-sonnet-4.5"]
```

**问题**：用户数据中存的是 OpenRouter 的 `product_id` 格式（点号），而非 `api_model_name` 格式（短横线）。Phase 4 需要迁移。
