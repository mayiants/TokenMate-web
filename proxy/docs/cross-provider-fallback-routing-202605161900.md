> **ACTIVE** — Cross-Provider Key Pool Fallback Routing

# Cross-Provider Key Pool Fallback Routing

**文档编号:** cross-provider-fallback-routing-202605161900
**创建时间:** 2026-05-16 19:00
**状态:** ✅ 全部已实现

---

## 1. 问题

当前 Proxy 的路由逻辑是**单 provider 直连**：

```
请求模型 claude-opus-4-7
  → GetProviderFromModel("claude-opus-4-7") = "anthropic"
  → keyPoolManager.GetKeyByPriority("anthropic", ...)
  → 没有 anthropic 的 upstream key → 返回 "No available upstream keys"
```

这意味着即使第三方 provider（如 accessapi）有能力转发 claude 模型的请求，Proxy 也不会使用它。模型名 → provider 的映射是硬编码的，不考虑 key pool 的实际可用性。

**实际场景**：用户使用 `wgit-maas-latest` 路由到 `claude-opus-4-7`，但 key pool 里只有 accessapi 的 key（能转发 claude），没有 anthropic 的直连 key。请求直接失败。

---

## 2. 解决思路

当首选 provider 没有可用 key 时，Proxy 应从**所有能提供该模型的 provider** 中 fallback 查找可用 key。

keypool manager 已有 `GetProvidersForModel(model)` 方法，能返回所有支持某模型的 provider 列表。只需在 handler 层面串联使用。

---

## 3. 方案

### 3.1 核心改动：`handleChatCompletions` 中的 provider 选择逻辑

**当前逻辑** (`handlers.go:388-437`):

```go
provider := relay.GetProviderFromModel(req.Model)  // 硬编码映射
upstreamKey := keyPoolManager.GetKeyByPriority(ctx, provider, userPriority, req.Model, preferredKeyID)
if upstreamKey == nil {
    c.JSON(503, "No available upstream keys for provider: " + provider)
    return
}
```

**改为**:

```go
// Step 1: 尝试首选 provider（模型名对应的原始 provider）
provider := relay.GetProviderFromModel(req.Model)
upstreamKey := keyPoolManager.GetKeyByPriority(ctx, provider, userPriority, req.Model, preferredKeyID)

// Step 2: 首选 provider 无可用 key → fallback 到其他能提供该模型的 provider
if upstreamKey == nil {
    fallbackProviders := keyPoolManager.GetProvidersForModel(req.Model)
    for _, fp := range fallbackProviders {
        if fp == provider {
            continue // 已尝试过
        }
        upstreamKey = keyPoolManager.GetKeyByPriority(ctx, fp, userPriority, req.Model, preferredKeyID)
        if upstreamKey != nil {
            provider = fp
            break
        }
    }
}

// Step 3: 所有 provider 都没有可用 key
if upstreamKey == nil {
    c.JSON(503, "No available upstream keys for model: " + req.Model)
    return
}
```

### 3.2 需要改动的 handler 函数

| 函数 | 说明 |
|------|------|
| `handleChatCompletions` (line 388) | 非流式 + 流式 chat |
| `handleEmbeddings` (line 870) | embeddings 请求 |
| `handleImageGenerations` (line 983) | 图片生成 |
| `handleAudioTranscriptions` (line 1071) | 音频转录 |
| `handleAudioTranslations` (line 1148) | 音频翻译 |

每个 handler 都有 `provider := relay.GetProviderFromModel(req.Model)` + `keyPoolManager.GetKeyByPriority` 的组合，需要统一加入 fallback 逻辑。

### 3.3 提取公共函数避免重复

将 fallback 逻辑提取为一个辅助函数：

```go
// resolveProviderAndKey finds an available upstream key for the requested model.
// Tries the primary provider first, then falls back to any provider that
// supports the model and has available keys.
func resolveProviderAndKey(ctx context.Context, model, userPriority, preferredKeyID string) (provider string, upstreamKey *keypool.KeyInternal) {
    provider = relay.GetProviderFromModel(model)
    upstreamKey = keyPoolManager.GetKeyByPriority(ctx, provider, userPriority, model, preferredKeyID)

    if upstreamKey == nil && keyPoolManager != nil {
        fallbackProviders := keyPoolManager.GetProvidersForModel(model)
        for _, fp := range fallbackProviders {
            if fp == provider {
                continue
            }
            upstreamKey = keyPoolManager.GetKeyByPriority(ctx, fp, userPriority, model, preferredKeyID)
            if upstreamKey != nil {
                provider = fp
                break
            }
        }
    }
    return provider, upstreamKey
}
```

各 handler 调用此函数替代原来的两步逻辑。

### 3.4 Adaptor 选择

当前 `relay.GetProviderConfig(provider)` 返回 adaptor 配置（base URL、请求格式等）。当 fallback 到 accessapi 时，provider 变为 `"accessapi"`，但 relay 里没有 accessapi 的配置。

**解决**：accessapi 是 OpenAI-compatible provider，应使用 OpenAI adaptor。需要在 `GetProviderConfig` 中增加 fallback：

```go
// 如果 provider 不是已知原生 provider，尝试用 OpenAI-compatible adaptor
func GetProviderConfig(provider string) (ProviderConfig, bool) {
    if cfg, ok := nativeProviders[provider]; ok {
        return cfg, true
    }
    // Unknown provider → OpenAI-compatible adaptor
    return ProviderConfig{
        BaseURL: "",  // 由 upstream key 的 base_url 提供
        Type:    "openai",
    }, true
}
```

或者更简单：在 `resolveProviderAndKey` 返回后，如果 provider 不是原生 provider，直接使用 OpenAI adaptor + upstream key 的 base_url。

### 3.5 Circuit Breaker

当前 circuit breaker 按 provider 名隔离。fallback 到 accessapi 时，应检查 accessapi 的 circuit breaker 状态，而不是 anthropic 的。`resolveProviderAndKey` 在 fallback 循环中应跳过 circuit breaker 已打开的 provider：

```go
for _, fp := range fallbackProviders {
    if fp == provider {
        continue
    }
    if !providerBreakers.Allow(fp) {
        continue  // circuit breaker open, skip
    }
    upstreamKey = keyPoolManager.GetKeyByPriority(ctx, fp, userPriority, model, preferredKeyID)
    if upstreamKey != nil {
        provider = fp
        break
    }
}
```

### 3.6 错误信息优化

将 "No available upstream keys for provider: anthropic" 改为 "No available upstream keys for model: claude-opus-4-7"，因为用户关心的是模型不可用，不是哪个 provider 没有 key。

---

## 4. 不改动的部分

- `GetProviderFromModel` 不改 — 它仍然返回首选 provider，用于优先路由
- `GetProvidersForModel` 不改 — 已有实现，直接使用
- Portal 侧不改 — key pool 同步逻辑不变，provider 和 key 的数据结构不变
- 计费逻辑不变 — provider 字段记录实际使用的 provider（可能是 fallback provider）

---

## 5. 测试方式

### 5.1 场景 1：首选 provider 有 key（正常路由）

```bash
# minimax 有 key，mimo-v2.5 是 minimax 的模型
curl -X POST https://www.example.com/v1/chat/completions \
  -H "Authorization: Bearer sk-wgit-xxx" \
  -d '{"model":"mimo-v2.5","messages":[{"role":"user","content":"hi"}]}'
# 期望：直接路由到 minimax，不走 fallback
```

### 5.2 场景 2：首选 provider 无 key，fallback 到第三方（核心场景）

```bash
# claude-opus-4-7 → 首选 anthropic（无 key）→ fallback 到 accessapi（有 key）
curl -X POST https://www.example.com/v1/chat/completions \
  -H "Authorization: Bearer sk-wgit-xxx" \
  -d '{"model":"claude-opus-4-7","messages":[{"role":"user","content":"hi"}]}'
# 期望：成功路由到 accessapi，返回 claude-opus-4-7 的响应
# 验证：response.model 应为 "claude-opus-4-7" 或类似
```

### 5.3 场景 3：所有 provider 都无 key

```bash
# gemini-2.5-pro → 首选 google（无 key）→ 无其他 provider 有 key
curl -X POST https://www.example.com/v1/chat/completions \
  -H "Authorization: Bearer sk-wgit-xxx" \
  -d '{"model":"gemini-2.5-pro","messages":[{"role":"user","content":"hi"}]}'
# 期望：返回 503 "No available upstream keys for model: gemini-2.5-pro"
```

### 5.4 场景 4：Circuit breaker 打开时跳过

```bash
# 如果 anthropic 的 circuit breaker 打开（连续失败），应跳过
# 如果 accessapi 正常，应 fallback 到 accessapi
# 验证：日志中应看到 "fallback from anthropic to accessapi"
```

### 5.5 场景 5：wgit-maas-latest 路由

```bash
# wgit-maas-latest → selected_models 中包含 claude-opus-4-7
# 应 fallback 到 accessapi 的 key
curl -X POST https://www.example.com/v1/chat/completions \
  -H "Authorization: Bearer sk-wgit-xxx" \
  -d '{"model":"wgit-maas-latest","messages":[{"role":"user","content":"hi"}]}'
# 期望：成功路由
```

### 5.6 日志验证

部署后检查 Proxy 日志，确认 fallback 路径有记录：

```
Fallback from anthropic to accessapi for model claude-opus-4-7
```

---

## 6. 风险评估

| 风险 | 说明 | 缓解 |
|------|------|------|
| 第三方 provider 响应格式差异 | accessapi 可能不完全兼容 OpenAI 格式 | accessapi 已验证是 OpenAI-compatible |
| 延迟增加 | 第三方转发链路更长 | 已有 provider health 监控，延迟数据可见 |
| 计费准确性 | 不同 provider 的 token 计数可能有差异 | Portal 侧统一计费，不依赖 Proxy 的 cost |
| Fallback 顺序 | 当前按 GetProvidersForModel 返回顺序 | 后续可加入延迟/成功率排序 |

---

## 7. 实施范围

| 文件 | 改动 |
|------|------|
| `internal/server/handlers.go` | 新增 `resolveProviderAndKey()` 函数，替换 5 个 handler 的 provider+key 选择逻辑 |
| `pkg/relay/interface.go` | `GetProviderConfig` 增加 unknown provider → OpenAI-compatible fallback |
| `pkg/relay/providers.go` | 新增 `IsNativeProvider()` 判断函数 |

预计改动行数：~80 行新增，~30 行替换。

---

## 8. 问题：Fallback 顺序随机导致路由不稳定

### 8.1 问题

初始实现中，`resolveProviderAndKey` 通过 `GetProvidersForModel` 获取 provider 列表后逐个尝试。但 `GetProvidersForModel` 返回的是 Go map 遍历顺序——**随机且不确定**。这导致：

1. **路由不稳定**：同一用户连续请求同一模型，可能每次落到不同 provider 的不同 key，上游 cache 和 session affinity 完全失效
2. **优先级形同虚设**：`GetKeyByPriority` 只在单个 provider 内选 key，但 provider 间的选择是随机的。如果 anthropic 有 high key 但无可用 key，accessapi 有 normal key，anotherapi 有 high key——随机可能先选到 accessapi 的 normal key，跳过了 anotherapi 的 high key
3. **亲和性不可控**：`preferredKeyID` 只在单个 provider 内检查，跨 provider 时无法保证

### 8.2 根因

核心原因是**"先选 provider，再在 provider 内选 key"**的两步逻辑。正确的做法应该是**"跨所有 provider 统一选 key"**。

### 8.3 修复：统一跨 Provider Key 选择

将 `resolveProviderAndKey` 从"逐 provider 查找"改为"收集所有候选 key 后统一按优先级选择"：

1. `GetAllKeysForModel` 从所有非 circuit-broken 的 provider 中收集候选 key，按 high/normal/economy 分组
2. 根据用户 priority 级别，跨 provider 统一筛选候选集
3. 亲和性优先（preferredKeyID 在全量候选集中查找）
4. 确定性排序：primary provider 的 key 排前面，同 provider 内按 LRU

---

## 9. 问题：Provider 顺序应由 Admin 配置，而非随机或硬编码

### 9.1 分析

用户选择的是 model name（如 `claude-opus-4-7`），不关心背后是 anthropic 直连还是 accessapi 转发。让用户配置 provider 顺序既不合理也不可行——用户根本不知道有哪些 provider。

Admin 知道每个 provider 的价格、延迟、稳定性，应该由 Admin 决定服务优先级。比如：
- accessapi 转发 claude 比 anthropic 直连便宜 → Admin 可能优先选 accessapi
- anthropic 直连延迟更低 → Admin 可能优先选 anthropic
- 不同 Admin 的商业策略不同 → 需要可配置

### 9.2 设计原则

| 层级 | 谁控制 | 控制什么 |
|------|--------|---------|
| 模型别名→具体模型 | User（通过 UI） | 选哪些 model，**顺序可见可调** |
| 同模型多 provider 优先级 | Admin（通过后台） | provider fallback 顺序（按价格/质量/延迟） |
| Key 优先级 | Admin（key 的 priority 字段） | high/normal/economy |

### 9.3 方案：新建 `model_provider_routing` 表

在 Portal DB 中新建表，存储 Admin 配置的每个模型的 provider 优先级：

```sql
CREATE TABLE model_provider_routing (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_name VARCHAR(100) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,  -- 数值越小优先级越高
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(model_name, provider)
);
```

### 9.4 数据流

```
Admin 在 Portal 后台配置 model_provider_routing
  → Portal sync 响应新增 model_routing 字段
  → Proxy 接收后存入 keypool.Manager.modelRouting (map[string][]string)
  → resolveProviderAndKey 使用 GetRoutingOrder(model) 获取有序 provider 列表
  → 候选 key 按 Admin 配置的 provider 顺序排序
```

### 9.5 Admin API

| Method | Path | 说明 |
|--------|------|------|
| GET | `/admin/model-routing` | 列出所有路由规则，支持 `?model=` 过滤 |
| POST | `/admin/model-routing` | 添加路由规则 |
| PATCH | `/admin/model-routing/:id` | 更新优先级或启用/禁用 |
| DELETE | `/admin/model-routing/:id` | 删除路由规则 |
| POST | `/admin/model-routing/reorder` | 批量更新优先级（拖拽排序保存） |

### 9.6 Admin UI

在 admin-models 页面新增"模型路由配置"区域：
- 表格展示所有路由规则（模型名称、Provider、优先级、状态）
- 支持按模型名过滤
- 添加规则：选择模型 + 选择 Provider + 设置优先级
- 拖拽排序（Sortable.js）
- 启用/禁用/删除

### 9.7 Proxy 侧改动

`resolveProviderAndKey` 的排序逻辑从"primary provider 优先"改为"Admin 配置的 provider 顺序优先"：

```go
providerOrder := keyPoolManager.GetRoutingOrder(model)
providerRank := make(map[string]int)
if providerOrder != nil {
    for i, p := range providerOrder {
        providerRank[p] = i  // Admin 配置的顺序
    }
} else {
    providerRank[primaryProvider] = 0  // 无配置时 fallback
}

sort.SliceStable(candidates, func(i, j int) bool {
    iRank := providerRank[candidates[i].Provider]  // 缺省为 defaultRank
    jRank := providerRank[candidates[j].Provider]
    if iRank != jRank {
        return iRank < jRank  // 数值越小越优先
    }
    return candidates[i].Key.LastUsed.Before(candidates[j].Key.LastUsed)
})
```

---

## 10. 用户端：多模型选择顺序可视化

### 10.1 问题

`apikeys.html` 的多模型选择只有 checkbox 卡片，用户选完后看不到顺序。对于"优先级"和"轮询"模式，模型顺序直接影响路由结果，但用户无法感知和调整。

### 10.2 方案

在 checkbox 卡片下方新增"已选模型顺序"面板：

1. 上面是 checkbox 卡片（选哪些模型）
2. 下面是**有序列表**（显示选择顺序，支持拖拽调整）
3. 勾选 checkbox → 追加到列表末尾
4. 取消勾选 → 从列表移除
5. 拖拽调整顺序 → 更新列表
6. 提交时 `selected_models` 数组按列表顺序构建
7. **轮询和优先级模式都使用同样的 UI**

### 10.3 技术实现

- 引入 Sortable.js（本地静态文件 `web/static/js/sortable.min.js`，非 CDN）
- `selectedModels` 数组维护有序状态
- `renderSelectedModelList()` 渲染有序列表并初始化 Sortable
- 设置弹窗（`renderSettingsForm`）同样使用有序列表
- 使用 `go:embed` 嵌入前端文件到 Go 二进制，修改后需 `start.sh mvp` 重新编译部署

### 10.4 Sortable.js 拖拽 Bug 修复

初始实现中拖拽不工作，根因有三个：

1. **Sortable 实例重复创建**：每次 `renderSelectedModelList` 调用都 `Sortable.create()`，但未销毁旧实例，导致多个实例冲突使拖拽失效。修复：引入 `_sortableInstance`/`_settingsSortableInstance`/`_routingSortableInstance` 变量，重新创建前先 `destroy()` 旧实例。

2. **handle 选择器不可靠**：原来用 `span[style*="cursor:grab"]` 匹配拖拽手柄，这种 CSS 属性子串匹配不稳定。修复：改用 `.drag-handle` class 选择器，拖拽手柄元素加 `<span class="drag-handle">` class。

3. **表格行拖拽缺少 draggable 配置**：admin-models.html 的 `<tbody>` 上 Sortable 缺少 `draggable: 'tr'` 配置，导致 `<tr>` 行拖拽不生效。修复：添加 `draggable: 'tr'` 配置项。

4. **序号更新逻辑错误**：原来 `item.querySelector('span').textContent` 更新序号，但拖拽手柄是第一个 `<span>`，序号是第二个。修复：改为 `item.querySelectorAll('span')[1].textContent`。

---

## 11. 实施记录

| 改动 | 文件 | 状态 |
|------|------|------|
| `resolveProviderAndKey` 统一跨 provider key 选择 | `internal/server/handlers.go` | ✅ |
| `GetAllKeysForModel` 收集跨 provider 候选 key | `pkg/keypool/manager.go` | ✅ |
| `GetProviderConfig` 未知 provider → OpenAI-compatible | `pkg/relay/providers.go` | ✅ |
| `model_provider_routing` 表 | `migrations/202605171600_model_provider_routing.sql` | ✅ |
| Admin API (CRUD + reorder) | `internal/routes/models.go` | ✅ |
| Portal sync 新增 model_routing | `internal/routes/sync.go` | ✅ |
| `ModelRoutingRule` + `GetRoutingOrder` | `pkg/keypool/manager.go` | ✅ |
| `resolveProviderAndKey` 使用 Admin 路由顺序 | `internal/server/handlers.go` | ✅ |
| Admin UI 路由配置 | `web/templates/admin-models.html` | ✅ |
| 用户端可排序模型选择 | `web/templates/apikeys.html` | ✅ |
| Sortable.js 本地引入 | `web/static/js/sortable.min.js` + `layout.html` | ✅ |
| Sortable 拖拽 bug 修复（实例重复创建 + handle 选择器 + draggable 配置 + 序号更新） | `apikeys.html` + `admin-models.html` | ✅ |
| Sync JSONB models 字段修复（pgx 返回 `[]interface{}` 而非 `[]byte`） | `ai-portal-go/internal/routes/sync.go` | ✅ |
| 模型名点号/短横线模糊匹配（`claude-opus-4.6` ↔ `claude-opus-4-6`） | `ai-proxy-go/pkg/keypool/manager.go` | ✅ |
| Admin 编辑模型弹窗增加 product_id 和 api_model_name 字段 | `ai-portal-go/web/templates/admin-models.html` + `internal/routes/models.go` | ✅ |

---

## 12. Bug 修复：Sync JSONB models 字段为空

### 12.1 问题

Proxy 的 keypool 中所有 key 的 `Models` 字段为空数组，导致 `len(key.Models) == 0` 条件成立，所有 key 被当作"支持所有模型"。Fallback 路由因此会选到实际不支持该模型的 provider（如 minimax 不支持 claude），上游返回 400/403 错误。

### 12.2 根因

Portal 使用 pgx v5 驱动。pgx 将 PostgreSQL JSONB 数组扫描为 Go 的 `[]interface{}` 类型，而非 `[]byte` 或 `string`。sync.go 中解析 models 字段的 switch 语句只处理了 `[]byte` 和 `string` 两种类型，漏掉了 `[]interface{}`，导致 `modelList` 始终为空。

### 12.3 修复

在 sync.go 的 models 解析 switch 中增加 `[]interface{}` case，将每个元素断言为 `string` 后加入 `modelList`。

---

## 13. Bug 修复：模型名点号/短横线不匹配

### 13.1 问题

OpenRouter 的模型名使用点号格式（如 `claude-opus-4.6`），而 Anthropic 等原生 provider 使用短横线格式（如 `claude-opus-4-6`）。当用户请求 `claude-opus-4.6` 时，accessapi key 的 models 列表中是 `claude-opus-4-6`，`contains(key.Models, model)` 精确匹配失败，key 被跳过。

### 13.2 修复

在 keypool manager 的 `contains` 函数中增加模型名归一化匹配：将点号替换为短横线后比较。`normalizeModelName` 函数将 `claude-opus-4.6` 归一化为 `claude-opus-4-6`，使两种格式可以互相匹配。

---

## 14. 功能：Admin 编辑模型时修改 model name

### 14.1 问题

从 OpenRouter 添加模型时，`product_id` 和 `api_model_name` 使用 OpenRouter 的命名规则（点号格式）。但实际 provider 可能使用不同命名（短横线格式）。Admin 需要在编辑和确认模型时修改这些名称。

### 14.2 修复

- 编辑弹窗增加 `product_id` 和 `api_model_name` 输入框，带说明文字
- 后端 PATCH 接口增加 `product_id` 字段支持
- `editModel` 函数加载并填充这两个字段
- `submitEditModel` 函数提交这两个字段