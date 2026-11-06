# Models → Keys Sync 重构方案

**Date**: 2026-06-19 09:28 UTC  
**Updated**: 2026-06-19 — 已实施  
**Status**: ✅ IMPLEMENTED  
**Scope**: 修复 `syncServiceModelsToKeys` 覆写问题 + 解决 Anthropic 等零返回 provider 的上行 key 无法工作问题

---

## 1. 现状分析

### 1.1 数据表

```
upstream_keys  (上行 key)          service_models  (服务模型)
├── id                             ├── id
├── provider ──────────────────────├── provider
├── models (JSONB) ← 问题字段      ├── product_id
├── enabled                        ├── status (active/paused/eol...)
├── validation_status              └── ...
└── ...
```

### 1.2 当前 models 填充路径

```
路径 A: Key 创建 → verify → 上游返回 model list → validateKeyAsync 写入 models
       适用: OpenAI, DeepSeek, Moonshot 等（返回 /v1/models）

路径 B: Key 创建 → verify → 上游不返回 model list → models 保持 NULL
       适用: Anthropic, AWS Bedrock 等

路径 C: syncServiceModelsToKeys → 聚合 service_models 按 provider → 
       UPDATE upstream_keys SET models = <聚合> WHERE provider = $1
       触发: 11 个 model 生命周期事件 + "Manual Model Sync" 按钮
       效果: 覆写该 provider 下**所有** key 的 models 字段
```

### 1.3 UI 现状

| 页面 | 功能 | models 字段 |
|------|------|------------|
| admin/keypool 创建 key | provider, key, rpm, tpm, quota, tier, base_url | **无** |
| admin/keypool 编辑 key | provider, tier, rpm, tpm, quota, base_url | **无** |
| admin/keypool 列表 | 显示 Verify / Edit / Enable / Delete 按钮 | 不显示 |
| admin/models-general | "Manual Model Sync" → syncServiceModelsToKeys("") | 按 provider 聚合 |
| admin/models-general | "Sync Selected to Upstream Keys" → syncKeyModels | 按 provider 覆写 |

**结论：Admin 完全无法在 UI 中看到或编辑单个 key 的 models 字段。**

### 1.4 Proxy 端的 models 语义

```go
// manager.go → GetAllKeysForModel
if len(key.Models) > 0 {
    matched, _ := containsMatch(key.Models, model)
    if !matched { continue }
} else {
    continue  // ← models 为空 = 此 key 永远无法匹配任何请求
}
```

**models 被当作路由的必要条件：空的 model 列表 = key 不可用。**

---

## 2. 核心问题

### 问题 2.1: syncServiceModelsToKeys 强制覆写

`admin_common.go:302-309`：
```sql
UPDATE upstream_keys uk
SET models = COALESCE(
    (SELECT am.models_json FROM active_models am WHERE am.provider = uk.provider),
    '[]'::jsonb
)
WHERE ($1 = '' OR uk.provider = $1)
```

- 每次触发，该 provider 下所有 key 的 models 被替换为同一个聚合列表
- 路径 A 中 verify 自动填充的 models 会在下次 sync 时被覆写
- 如果 admin 通过 SQL 手动设置了某个 key 的 models，也会被覆写

### 问题 2.2: Anthropic 等 provider 的 key 无法工作

```
Key 创建 (models=NULL)
  → verify → Anthropic 不返回 model list → models 仍为 NULL
  → syncServiceModelsToKeys → 
      ├─ 如果 service_models 有 anthropic 数据 → models 被填充 ✅
      └─ 如果 service_models 无 anthropic 数据 → models 保持 NULL ❌
  → Proxy 路由: models 为空 → continue → key 永远不可用
```

admin 必须在 models 页面先导入 Anthropic 的 service_models，然后再触发 sync，key 才能工作。如果导入的 model 列表不全，key 只能路由部分请求。

### 问题 2.3: 维护成本

- 目前 22 条上行 key，分布在 ~10 个 provider
- 每个 provider 的每个 key 都需要 service_models 数据支持
- Anthropic 等不返回 model list 的 provider 需要 admin 手动维护 service_models
- 新增一条 Anthropic key 后，必须确保 service_models 已导入 + 触发 sync

---

## 3. 建议方案

### 核心思路：改变 `key.Models` 的语义

| | 当前语义 | 建议语义 |
|---|---------|---------|
| `key.Models` 为空 | key 不支持任何模型 → 跳过 | 回退到 provider 级 model 匹配 → key 支持该 provider 的**所有**已知模型 |
| `key.Models` 有值 | 精确匹配列表 → 仅路由列表中模型 | **不变** — 作为 allowlist/override |
| syncServiceModelsToKeys | 强制覆写所有 key | 只填充 models 为空的 key |

这样：
- Anthropic key 的 models 为空 → Proxy 自动从 ModelRegistry 查找 Anthropic 支持的模型 → 正常路由 ✅
- OpenAI key 被 verify 自动填充了 models → sync 不覆写 ✅
- Admin 想限制某个 key 只服务特定模型 → 通过 SQL 手动设置 models → 生效且不被覆写 ✅

### 3.1 实施步骤

#### Step 1: Portal 端 — SQL WHERE 加条件（~1 行）

`ai-portal-go/internal/routes/admin_common.go:302-309`:

```sql
-- 当前
WHERE ($1 = '' OR uk.provider = $1)

-- 改为
WHERE ($1 = '' OR uk.provider = $1)
  AND uk.deleted_at IS NULL
  AND uk.enabled = TRUE
  AND (uk.models IS NULL OR uk.models = '[]'::jsonb)
```

同时修改 `models_sync.go:480` 的 `syncKeyModels`:
```sql
-- 当前
WHERE provider = $2

-- 改为
WHERE provider = $2
  AND deleted_at IS NULL
  AND enabled = TRUE
  AND (models IS NULL OR models = '[]'::jsonb)
```

#### Step 2: Proxy 端 — 空 models 回退到 provider 级（~15 行）

`ai-proxy-go/pkg/keypool/manager.go` → `GetAllKeysForModel`:

```go
// 当前
if len(key.Models) > 0 {
    matched, matchedName := containsMatch(key.Models, model)
    if !matched { continue }
} else {
    continue  // 空 models = 无法匹配
}

// 改为
if len(key.Models) > 0 {
    matched, matchedName := containsMatch(key.Models, model)
    if !matched { continue }
}
// 空 models → 回退到 provider 级：key 支持该 provider 的所有已知模型
// 不跳过，继续执行后续路由逻辑
```

即：删除 `else { continue }` 分支。空 models 不再意味着 "跳过"，而是 "不限制"。

#### Step 3: Proxy 端 — ModelRegistry 提供 provider→models 查询（可选增强）

如果 Step 2 不够（proxy 需要知道哪些模型属于哪个 provider 来做 upstream model name 映射），可以在 `resolveProviderAndKey` 中通过 `ModelRegistry` 查找：

```go
// 如果 key 的 UpstreamModelName 为空（因为跳过了 containsMatch），
// 从 ModelRegistry 获取该 provider 的候选模型名
if ck.UpstreamModelName == "" {
    ck.UpstreamModelName = modelsRegistry.ResolveUpstreamModelName(provider, model)
}
```

`ModelRegistry` 已经通过 `/api/admin/models/sync` 同步了 service_models 数据，包含 provider → product_id 映射。

### 3.2 不做的事

- ❌ 不新增 `models_overridden` 列（当前 UI 无 models 编辑功能，标记无意义）
- ❌ 不修改 Keypool UI 加 models 字段（维护成本太高，不必需）
- ❌ 不在 Proxy 中引入 per-provider model cache（ModelRegistry 已有）

---

## 4. 场景验证

### 场景 A: OpenAI key（verify 返回 model list）

| 步骤 | models 状态 | 行为 |
|------|------------|------|
| 创建 | NULL | — |
| verify | = [gpt-4, gpt-3.5, ...] | validateKeyAsync 写入 |
| syncServiceModelsToKeys | 不变（非空，跳过） | ✅ 不受影响 |
| Proxy 路由 | 正常匹配 | ✅ |

### 场景 B: Anthropic key（verify 不返回 model list）

| 步骤 | models 状态 | 行为 |
|------|------------|------|
| 创建 | NULL | — |
| verify | NULL（无返回） | validateKeyAsync 不修改 |
| syncServiceModelsToKeys | 检查 service_models 有无 anthropic | |
| → 有 | = [claude-sonnet, ...] | 自动填充 ✅ |
| → 无 | NULL | 保持 NULL |
| Proxy 路由 | NULL → 回退 provider 级匹配 | ✅ 通过 ModelRegistry 查找 anthropic 模型 |

### 场景 C: 同一模型多 provider（如 gpt-4 有 openai + azure）

| 步骤 | 行为 |
|------|------|
| openai key models = [gpt-4] | 正常路由 |
| azure key models = NULL | 回退 provider 级 → azure 的 service_models 中有 gpt-4 |
| Proxy 收到 gpt-4 请求 | 两个 key 都进入候选池 ✅ |
| smartRoute 决策 | 按 health/latency/quota 选择 ✅ |

### 场景 D: Admin 想限制某 key 只服务特定模型

| 步骤 | 行为 |
|------|------|
| 通过 SQL 手动设置 models = [claude-sonnet] | — |
| syncServiceModelsToKeys | 跳过（非空） ✅ |
| Proxy 路由 | 仅匹配 claude-sonnet ✅ |

---

## 5. 工作量

| Step | 文件 | 改动 |
|------|------|------|
| Portal SQL WHERE | `admin_common.go:302-309` | ~3 字符 |
| Portal SQL WHERE | `models_sync.go:480` | ~2 行 |
| Proxy 空 models 回退 | `manager.go:2253-2275` | 删除 `else { continue }` + 可选 upstreamModelName 回退 |
| 测试 | 两端 | build + vet + 现有测试 |
| **合计** | | **~10 行代码** |

---

## 6. 与备选方案对比

| 方案 | 改动量 | 维护成本 | 风险 |
|------|--------|---------|------|
| **A: 本方案** (空 models = 回退 provider 级) | ~10 行 | 低 （无需 admin 额外操作） | 低 |
| B: `models_overridden` 列 + UI | ~100 行 + migration | 高 （admin 需逐 key 配置） | 中 |
| C: 始终使用 provider 级 model 匹配，忽略 key.Models | ~5 行 | 最低 | 中 （失去 per-key allowlist 能力） |
| D: 移除 per-key models，全改用 provider 级 | ~50 行 + migration | 最低 | 高 （破坏性变更） |

**推荐方案 A** — 最小改动，最大收益，保留 per-key allowlist 的灵活性（通过 SQL 手动设置）。

---

## 7. 是否需要 per-key model 编辑 UI？

### 7.1 分析

用户提出两个场景：

**场景 1**: Admin 想限制某 key 只服务特定模型（如 enterprise key 只服务 gpt-4）。

**场景 2**: 同一模型有多个 provider（如 claude-sonnet-4.6 有 anthropic / aws bedrock / 3rd-party），admin 需要控制哪个 key 服务哪个模型。

### 7.2 当前已有机制

`service_models` 表结构：

```sql
provider VARCHAR(50) NOT NULL,     -- 区分不同 provider
product_id VARCHAR(100) NOT NULL,   -- provider 特定的模型 ID
api_model_name VARCHAR(100)         -- 用户侧模型名
```

同一模型在不同 provider 下是**不同行**：
```
provider='anthropic' | product_id='claude-sonnet-4-6-20250514'
provider='amazon'    | product_id='us.anthropic.claude-sonnet-4-6-20250514-v1:0'
provider='3rd-party' | product_id='claude-sonnet-4-6'
```

`syncServiceModelsToKeys` **已按 provider 聚合**，每个 provider 的 key 只得到**该 provider 自己的**模型列表。Admin 在 UI 中对每个 provider 分别操作：
1. 选择 provider → "Fetch Model List" 从 `/v1/models` 拉取
2. 勾选需要的模型 → "Add" 添加到 service_models
3. 自动触发 syncServiceModelsToKeys(该provider)

### 7.3 per-key 模型限制的真需求分析

| 需求 | 是否真需要 per-key 配置 | 替代方案 |
|------|----------------------|---------|
| 限制某 key 只服务特定模型 | **极少需要**。如果 key 的上游不支持某模型，应反映在 service_models 中，而非每个 key 单独配置 | 在 service_models 中精确维护该 provider 的模型列表 |
| 3rd-party key 只支持部分模型 | 同上。service_models 中该 3rd-party provider 的行应只列出其真正支持的模型 | 维护准确的 service_models 数据 |
| enterprise key 服务所有模型，economy key 只服务部分 | 用 tier + routing priority 控制，而非 per-key model allowlist | tier 决定候选池，smartRoute 决定最终选择 |

**结论：当前阶段不需要 per-key model 编辑 UI。** 正确的做法是在 `service_models` 表中精确维护每个 provider 的模型列表，sync 自动将正确的模型列表分配到该 provider 的 key。

### 7.4 如果未来需要：UI 增强方案（Phase 2）

当确有需求时（如 enterprise 客户需要专属的 model allowlist），可以新增：

- Keypool 编辑抽屉增加 "Models" 多选组件，数据源为对应 provider 的 service_models
- 有值的 models → 作为 allowlist；空的 models → 回退到 provider 级
- 不需要 `models_overridden` 列 — "有值" 即 "自定义"

---

## 8. 实施记录 (2026-06-19)

### 8.1 已实施改动

| # | 文件 | 改动 | 说明 |
|---|------|------|------|
| 1 | `ai-portal-go/.../admin_common.go:308` | WHERE 加 `deleted_at IS NULL AND enabled AND (models IS NULL OR models = '[]'::jsonb)` | `syncServiceModelsToKeys` 自动触发时只填空 |
| 2 | `ai-portal-go/.../models_sync.go:480` | 保持 `WHERE provider = $2 AND deleted_at IS NULL AND enabled = TRUE`（**不加** empty guard） | `syncKeyModels` admin 手动触发时强制覆盖 |
| 3 | `ai-proxy-go/.../manager.go:2253-2285` | 空 `key.Models` 不再 `continue` + 增加 `providerKnown` 守卫 | 空 models = 不限制（非"不可用"） |

### 8.2 两个 sync 函数的分工

| 函数 | 触发方式 | UI 按钮 | 行为 |
|------|---------|---------|------|
| `syncServiceModelsToKeys` | 自动（11 个 model 生命周期事件） | "Manual Model Sync" | **温和**：只填充空的 |
| `syncKeyModels` | 手动（admin 选择 provider + 模型后点击） | "Sync Selected to Upstream Keys" | **强制**：覆盖该 provider 所有 key |

model 列表更新流程：
```
1. admin/models → 选 provider → "Fetch Model List" → 勾选新模型
2. "Sync Selected to Upstream Keys" → syncKeyModels → 强制更新
3. Proxy 下次 sync 拿到更新后的 models
```

### 8.3 Proxy 端 providerKnown 守卫

`GetAllKeysForModel` 中空 models key 的处理逻辑：

```
Index 路径 (idx != nil):
  key.Models 有值 → 在 candidates 中查找 → 找到? 通过 : 跳过
  key.Models 为空 → 检查 candidates 中是否有同 provider 的其他 key
    → 有? provider 已知支持此模型 → 通过
    → 无? provider 不支持此模型 → 跳过（防止 catch-all fallback）

Fallback 路径 (idx == nil, 首次 sync 前):
  key.Models 有值 → containsMatch → 正常匹配
  key.Models 为空 → 跳过（无 index 无法验证 provider 支持，保守处理）
```

这确保空 models 的 key 永远不会被路由到其 provider 不支持的模型上。

### 8.4 相关优化

| 优化 | 状态 |
|------|------|
| Model Index (O(1) model→key 查找) | ✅ 已实施 |
| Priority-Tier Retry (层级重试，tier 按用户类型限制) | ✅ 已实施 |
| Models→Keys Sync 重构（本方案） | ✅ 已实施 |
| Per-key model 编辑 UI | ⏳ Phase 2（暂无需求） |

---

## 9. 待确认

1. 是否需要为 Anthropic/AWS 类 provider 预置 service_models 种子数据，减少 admin 手动导入步骤？
2. 当前 `containsMatch` 的 3-pass 匹配 (exact → normalized → prefix) 在 provider 级回退时是否仍然需要？（需要，用于 upstream model name 解析）
