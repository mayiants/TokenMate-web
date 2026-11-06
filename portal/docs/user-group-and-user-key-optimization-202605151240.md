> **⚠️ PARTIALLY OUTDATED** — 2026-05-15 起草，2026-05-16/17 架构重构后部分论述已过时。
>
> **关键变更**: `api_model_name` 已取代 `product_id` 成为跨 provider 的统一模型标识。`selected_models`、`allowed_models`、`user_groups.models` 实际存储的是 `api_model_name`（而非本文档所述的 `product_id`）。`product_id` 仅用于上游 API 调用时的模型名翻译。
>
> **仍有效的部分**: 跨组 fallback 设计思路（但未实现）、pricing_ratio 计费链路、双层权限模型概念。
>
> **已过时的部分**: §2.2-2.6 中关于 `product_id` 作为存储标识的论述、§3 实施优先级表中标记为 ✅ 的部分。

# User Group & User Key Optimization Design

基于与 AccessAPI (new-api) 的对比分析，以及用户组定价、模型路由、跨组 fallback 的设计讨论，梳理当前代码架构的缺口和优化方案。

---

## 0. 与 AccessAPI (new-api) 的核心区别

### 0.1 通用模型名 vs 真实模型名

| 维度 | AccessAPI (new-api) | 我们 |
|------|---------------------|------|
| 模型调用方式 | **必须使用真实模型名**（如 `gpt-4o`, `claude-sonnet-4-20250514`） | **支持通用模型名 `wgit-maas-latest`** + 真实模型名 |
| 模型切换 | 用户每次请求必须指定具体模型名 | `wgit-maas-latest` 自动路由，用户无需修改代码即可切换底层模型 |
| 稳定性保障 | 无 — 模型下线后用户必须手动修改 | 真实模型名提供稳定性保障，通用名提供便捷性 |

**我们的优势**：用户可以用一个不变的 `wgit-maas-latest` 接入所有模型，无需每次修改 model name。这是 AccessAPI 没有的功能，也是我们平台的差异化卖点。

**实施思路**：
- `wgit-maas-latest` → 从 `selected_models`（用户主动选择的3-5个模型）路由
- 真实模型名 → 从 `allowed_models`（Group 允许的全部模型）校验放行
- 双层权限模型：selected_models 是路由选择，allowed_models 是权限边界

### 0.2 跨组 Fallback

| 维度 | AccessAPI (new-api) | 我们 |
|------|---------------------|------|
| 跨组重试 | `cross_group_retry` — 简单布尔开关 | `group_priority` — 有序的组优先级列表（最多3组） |
| Fallback 粒度 | 开/关二元选择 | 按优先级顺序逐级 fallback，更精细 |
| Fallback 计费 | 未明确区分组计费 | **按实际使用的组计费**（pricing_ratio 动态切换） |
| Fallback 模型策略 | 无 | **取交集（默认）/ 保留原选（可配置）+ 保底组兜底** |

**我们的优势**：
1. 有序优先级比布尔开关更灵活 — 用户可以精细控制 fallback 顺序
2. 按实际组计费更公平 — 用了 economy 渠道就付 economy 价格
3. 模型交集策略避免 fallback 后路由到不存在的模型
4. 保底组兜底确保服务不中断

**实施思路**：
- 默认策略：取交集（保证至少服务可用）
- 用户可配置：保留原选（对模型精确度要求高的场景）
- 保底机制：交集为空时回落到最后一个 group（保底组），提供极其有限但至少能工作的模型

### 0.3 用户模型选择

| 维度 | AccessAPI (new-api) | 我们 |
|------|---------------------|------|
| 模型范围 | 组的全部模型 = 用户可用的全部模型 | **用户从组模型中主动选择3-5个** |
| 模型选择 | 无选择机制，组内所有模型平等可用 | 创建 Key 时必须至少选1个模型 |
| 路由范围 | 无（每次请求指定具体模型） | selected_models 控制路由范围 |

**我们的优势**：当平台有几十个模型时，用户不需要全部模型。主动选择3-5个业务需要的模型，路由范围可控、体验可预期。

**实施思路**：
- 创建 Key 时 UI 强制至少选1个模型
- 选中 Group 后动态展示该 Group 的模型列表供勾选
- 不选则默认全选（selected_models = group.models），但 UI 鼓励主动选择

### 0.4 计费体系

| 维度 | AccessAPI (new-api) | 我们 |
|------|---------------------|------|
| 基础计费 | 倍率制（model_ratio × completion_ratio × group_ratio） | 倍率制 ✅ 已实现 |
| 组倍率 | group_ratio 乘到最终价格 | pricing_ratio 字段已有，**但未接入计费公式** |
| 缓存计费 | cache_ratio + create_cache_ratio | Proxy 已完整实现 ✅ |
| 按次计费 | quota_type=1 + model_price | billing_mode='request' 字段已有，逻辑未实现 |
| Fallback 计费 | 无区分 | **按实际组计费 + 记录 is_fallback** |

**我们的优势**：计费体系架构已完备，只需 1 行代码让 pricing_ratio 生效。缓存计费已完整实现（包括 5min/1h 区分），比 AccessAPI 更细致。

---

## 1. 核心设计原则

### 1.1 用户必须主动选择模型

用户创建 Key 时，必须从所属 Group 的模型列表中至少选择 1 个模型填入 `selected_models`。

**原因**：
- Group 可能有几十个模型，但用户业务通常只需要 3-5 种
- 如果 `wgit-maas-latest` fallback 到整个 `allowed_models`，round_robin 会在几十个模型间乱跳，体验极差
- 用户主动选择 = 明确的路由范围 = 可预期的行为

### 1.2 双层模型权限模型

```
selected_models  → 路由选择层（用户主动选的模型，用于 wgit-maas-latest 路由）
allowed_models   → 权限边界层（Group 允许的全部模型，用于真实模型名校验）
```

使用场景：
- `wgit-maas-latest` → `resolveModel()` 从 `selected_models` 范围内路由
- 真实模型名 → 校验是否在 `allowed_models`（Group.models）中 → 放行或拒绝

### 1.3 跨组 Fallback 按实际组计费

当主组渠道失败，fallback 到次优先组时：
- **按实际使用的组计费**（pricing_ratio 切换到新组）
- **理由**：公平性 — 用了 economy 的渠道就该付 economy 的价格
- **激励对齐**：频繁 fallback 说明主组不稳定，运营需要关注
- **透明性**：usage_records 记录实际 group_id，用户可在账单明细中看到

---

## 2. 当前代码架构缺口

### 2.1 pricing_ratio 未接入计费链路

**现状**：
- `user_groups.pricing_ratio` 字段已存在（默认 1.0000）
- Portal `validateKey` 已返回 `pricing_ratio`（从 `COALESCE(g.pricing_ratio, 1.0)` 查询）
- Proxy `Claims.PricingRatio` 已接收并传入 `UsageRecord.PricingRatio`
- Portal webhook 处理时已应用 `cost = cost * pricingRatio`（`webhook.go:522/1188/1268`）

**状态：✅ 已实现**

**待修改**：1 行核心代码
```go
totalCost := (inputCost + outputCost + cacheReadCost + cacheWriteCost + reasoningCost) * pricing.GroupRatio
```

### 2.2 allowed_models 数据来源不完整

**现状**：
- `validateKey` 返回的 `allowed_models` 来自 `ak.allowed_models`（Key 自身字段）
- Key 创建时，`allowed_models` 的赋值逻辑：
  - 如果用户指定了 `req.Models` → 用用户指定的
  - 否则 → 用 Group 的 `models` 字段（`groupModels`）
- **问题**：Key 的 `allowed_models` 是创建时的快照，Group 模型列表更新后不会自动同步

**优化方向**：
- `validateKey` 应优先使用 Group 的实时 `models`，而非 Key 的静态 `allowed_models`
- 或者：Key 的 `allowed_models` 为空时，自动从 Group 继承

### 2.3 selected_models 未强制要求

**现状**：
- Key 创建时，`round_robin` 和 `priority` 模式要求 `selected_models` 非空
- `single` 模式不要求
- **问题**：用户可以创建一个没有任何模型配置的 Key，使用 `wgit-maas-latest` 时会报错

**状态：🟡 部分实现**

`ai-portal-go/internal/routes/apikeys.go:271-275` 仅对 `round_robin`/`priority` 模式强制要求 `selected_models`：
```go
if (req.ModelRoutingMode == "round_robin" || req.ModelRoutingMode == "priority") && len(req.SelectedModels) == 0 {
    c.JSON(http.StatusBadRequest, gin.H{"error": "selected_models is required for round_robin/priority mode"})
    return
}
```
**`single` 模式仍允许空 `selected_models`**，需补充校验。

**待优化**：
- 创建 Key 时，UI 强制要求至少选择 1 个模型（无论 routing mode）
- 后端增加校验：`selected_models` 不能为空（覆盖 single 模式）

### 2.4 真实模型名校验逻辑不完整 → 已修复

**现状**（`resolveModel()` at `handlers.go:1403-1406`）：
- ~~`single` 模式：真实模型名必须等于 `current_model`~~
- ~~`round_robin/priority` 模式：真实模型名必须在 `selected_models` 中~~
- ~~**问题**：真实模型名应该从 `allowed_models`（Group.models）匹配，而非 `selected_models`~~

**状态：✅ 已实现**

`ai-proxy-go/internal/server/handlers.go:1403-1406` 已统一从 `allowedModels` 校验：
```go
// 2. Real model name validation
if len(allowedModels) > 0 && !containsString(allowedModels, reqModel) {
    return "", fmt.Errorf("model '%s' is not available in your group. ...", reqModel)
}
```

### 2.5 跨组 Fallback 路由逻辑未实现

**现状**：
- `api_keys.group_priority` 字段已存在（最多3个组的优先级列表）
- `Claims.GroupPriority` 已从 Portal 同步到 Proxy
- **但 Proxy 的渠道选择逻辑没有实现跨组 fallback**

**状态：✅ 已实现**（`selected_models` 字段已存在于 user_groups 表，Admin API 和前端均支持读写）

`ai-proxy-go/internal/server/handlers.go:368` 的渠道选择仅使用 `provider + userPriority`，`group_priority` 虽然被 auth middleware 解析并存入 context，但从未在 fallback/retry 逻辑中使用。

### 2.6 Usage Records 缺少组级信息 → 结构已定义，未填充

**现状**：

**状态：✅ 已实现 (2026-05-24)**

1. **Proxy 侧**：`recordUsageAsync` 已填充 `GroupID`、`ActualGroupID`、`IsFallback`、`PricingRatio`。
   - `resolveProviderAndKey` 检测 provider fallback，设置 `is_fallback` / `actual_group_id` 到 gin context
   - `recordUsageAsyncFromContext` 读取并传递到 `UsageRecord`

2. **Portal 侧**：`usage_records` 表已有 `group_id`、`actual_group_id`、`is_fallback`、`pricing_ratio_at_request` 字段（迁移 `202605151400_usage_records_group_fields.sql`）。
   - `processRecord` / `processStats` / `processBillingBatch` 优先信任 proxy 发送的 `PricingRatio`
   - `actual_group_id` 优先使用 `record.ActualGroupID`，回退到 key/user 的 group
   - `is_fallback` 保存到 `usage_records.is_fallback`

---

## 3. 优化方案

### 3.1 P0：pricing_ratio 接入计费

**状态：✅ 已实现**

Portal webhook 处理时已应用 `cost = cost * pricingRatio`（`webhook.go:522/1188/1268`），Proxy 端 `PricingRatio` 已传入 `UsageRecord`。

| 文件 | 修改 | 状态 |
|------|------|------|
| `ai-proxy-go/internal/server/handlers.go` | 将 Claims.PricingRatio 传入 UsageRecord | ✅ |
| `ai-portal-go/internal/routes/webhook.go` | Portal 端 pricing_ratio 应用 | ✅ |

**数据流**：
```
Portal validateKey → pricing_ratio (from user_groups)
→ Proxy Claims.PricingRatio
→ Proxy UsageRecord.PricingRatio
→ Portal webhook: cost = cost * pricingRatio
→ 余额扣减 (已含组倍率)
```

### 3.2 P0：真实模型名从 allowed_models 校验

**状态：✅ 已实现**

| 文件 | 修改 | 行数 |
|------|------|------|
| `ai-proxy-go/internal/server/handlers.go:1403-1406` | resolveModel 真实模型名校验改为 allowedModels | ✅ 已完成 |
| `ai-proxy-go/internal/server/handlers.go` | 传入 allowedModels 参数到 resolveModel | ✅ 已完成 |

**resolveModel 签名变更**：
```go
func resolveModel(reqModel, routingMode, currentModel string,
    selectedModels, allowedModels []string, keyHash string) (string, error)
```

### 3.3 P1：创建 Key 时强制选择模型 + UI 改进

**Portal 端修改**：

| 文件 | 修改 | 行数 |
|------|------|------|
| `ai-portal-go/internal/routes/apikeys.go` | createKey 校验 selected_models 非空 | ~5行 |
| `ai-portal-go/internal/web/pages.go` | Key 创建页面增加模型选择 UI | ~30行 |
| `ai-portal-go/web/templates/` | 创建 Key 模板增加 Group 模型列表 | ~50行 |

**UI 交互流程**：
```
1. 用户选择 Group → 下拉框/对话框展示该 Group 的所有模型
2. 用户勾选至少1个模型 → 填入 selected_models
3. 如果用户不选 → 默认全选（等于 selected_models = group.models）
4. 后端校验：selected_models ⊆ group.models
```

### 3.4 P1：跨组 Fallback 路由 + 计费

**Proxy 端修改**：

| 文件 | 修改 | 行数 |
|------|------|------|
| `ai-proxy-go/internal/server/handlers.go` | 渠道选择逻辑增加 group_priority fallback | ~50行 |
| `ai-proxy-go/pkg/billing/billing.go` | UsageRecord 增加 group_id, actual_group_id, is_fallback, pricing_ratio | ~10行 |
| `ai-proxy-go/internal/server/handlers.go` | fallback 时切换 GroupRatio | ~10行 |

**Portal 端修改**：

| 文件 | 修改 | 行数 |
|------|------|------|
| `ai-portal-go/migrations/` | usage_records 增加 group_id, actual_group_id, is_fallback, pricing_ratio_at_request | 新迁移 |
| `ai-portal-go/internal/routes/webhook.go` | 接收并记录 fallback 信息 | ~15行 |

**Fallback 逻辑**：

```
请求到达 → Auth middleware 设置 group_id, group_priority, pricing_ratio
→ 渠道选择：从主组 (group_priority[0]) 的渠道中选
→ 主组所有渠道失败 → fallback 到 group_priority[1]
→ 切换：allowed_models = 新组.models, pricing_ratio = 新组.pricing_ratio
→ selected_models fallback 策略（见 3.5）
→ 计费：totalCost × 新组.pricing_ratio
→ UsageRecord: actual_group_id = 新组.id, is_fallback = true
```

### 3.5 P1：selected_models Fallback 策略

当 fallback 到次优先组时，`selected_models` 的处理策略：

**默认策略：取交集（方案A）**
- `fallback_selected_models = selected_models ∩ new_group.models`
- 保证路由到的模型在新组中确实可用
- 避免莫名其妙的失败

**用户可配置策略：不取交集（方案B）**
- `fallback_selected_models = selected_models`（不变）
- 如果路由到的模型在新组不存在，请求失败
- 适合对模型精确度要求高的用户

**保底机制**：
- 如果两个组完全没有模型重叠（交集为空）
- 回落到最后一个 group（保底 group）
- 保底 group 提供极其有限但至少能工作的模型
- 保底 group 应在系统配置中标记（如 `is_default = true` 或 `is_fallback = true`）

**数据结构**：
```go
// api_keys 表增加字段
fallback_strategy VARCHAR(20) DEFAULT 'intersect'  -- 'intersect' | 'preserve'
```

### 3.6 P2：allowed_models 实时化

**当前问题**：Key 的 `allowed_models` 是创建时的快照，Group 模型更新后不同步

**优化方向**：
- `validateKey` 返回时，优先使用 Group 的实时 `models` 作为 `allowed_models`
- Key 的 `allowed_models` 字段改为"覆盖/限制"语义：
  - 空 → 从 Group 继承（实时）
  - 非空 → 用 Key 自身的（用户手动限制）

**修改点**：

| 文件 | 修改 | 行数 |
|------|------|------|
| `ai-portal-go/internal/routes/apikeys.go:620` | validateKey SQL 优先取 g.models | ~10行 |

```sql
-- 当前：
ak.allowed_models, ...
-- 修改为：
CASE WHEN ak.allowed_models IS NULL OR ak.allowed_models = '[]'::jsonb
     THEN g.models
     ELSE ak.allowed_models
END as allowed_models, ...
```

---

## 4. 实施优先级

| 优先级 | 项目 | 修改难度 | 影响范围 |
|--------|------|---------|---------|
| P0 | pricing_ratio 接入计费 | **低**（1行核心+5行传递） | ✅ 已实现 | 计费准确性 |
| P0 | 真实模型名从 allowed_models 校验 | **低**（~15行） | ✅ 已完成 | 模型权限正确性 |
| P1 | 创建 Key 强制选模型 + UI | **中**（前端改动较多） | 🟡 部分实现（single 模式未覆盖） | 用户体验 |
| P1 | 跨组 Fallback 路由 + 计费 | **中**（Proxy 渠道逻辑+计费记录） | ⬜ 待实现 | 服务可用性 |
| P1 | selected_models fallback 策略 | **低**（新增字段+逻辑） | ✅ 已实现 | fallback 体验 |
| P2 | allowed_models 实时化 | **低**（SQL 修改） | ✅ 已实现 | 数据一致性 |

---

## 5. 关键代码位置

### Proxy 端 (ai-proxy-go)
- 计费公式：`pkg/billing/billing.go:89-149`（CalculateCostWithCache）
- PriceData 结构：`types/relay.go:80-94`（GroupRatio 等字段）
- 模型路由：`internal/server/handlers.go:1347-1384`（resolveModel）
- Auth Claims：`internal/auth/auth.go:102-141`（PricingRatio, GroupPriority, AllowedModels, SelectedModels）
- UsageRecord：`pkg/billing/billing.go:417-435`（缺少 group_id, is_fallback）

### Portal 端 (ai-portal-go)
- Key 验证：`internal/routes/apikeys.go:565-786`（validateKey）
- Key 创建：`internal/routes/apikeys.go:240-390`（createKey, selected_models 校验）
- 组管理：`internal/routes/groups.go`（CRUD, pricing_ratio）
- Webhook 计费：`internal/routes/webhook.go:238-367`（已有 pricing_ratio 应用 ✅）
- Usage Records：`migrations/002_usage_and_records.sql`（缺少 group_id, actual_group_id）

---

## 6. 数据流全景图

```
用户创建 Key
  ├─ 选择 Group → allowed_models = group.models（权限边界）
  ├─ 勾选模型 → selected_models ⊆ allowed_models（路由范围）
  ├─ 配置 group_priority → [premium-group, economy-group, default-group]
  └─ 配置 fallback_strategy → intersect（默认）或 preserve

用户使用 Key
  ├─ wgit-maas-latest → resolveModel(selected_models, routing_mode)
  │   ├─ single → current_model
  │   ├─ round_robin → 轮询 selected_models
  │   └─ priority → selected_models[0]
  │
  ├─ 真实模型名 → 校验 ∈ allowed_models → 放行
  │
  └─ 渠道选择 → 主组渠道
      ├─ 成功 → 计费 × 主组.pricing_ratio
      │   → UsageRecord: group_id=主组, is_fallback=false
      │
      └─ 失败 → fallback 到次组
          ├─ 切换 allowed_models = 次组.models
          ├─ selected_models fallback:
          │   ├─ intersect → selected_models ∩ 次组.models
          │   ├─ preserve → selected_models 不变
          │   └─ 保底 → 交集为空时回落到 default-group
          ├─ 计费 × 次组.pricing_ratio
          └─ UsageRecord: actual_group_id=次组, is_fallback=true, pricing_ratio_at_request=次组.pricing_ratio
```