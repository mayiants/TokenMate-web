> **⚠️ PARTIALLY OUTDATED** — 本文档中关于 `product_id` 作为唯一标识的论述已过时。2026-05-16/17 架构重构后，`api_model_name` 已成为跨 provider 的统一模型标识，`product_id` 降级为仅用于上游 API 调用的 provider 特定名称。详见 [model-name-translation-202605171430.md](../ai-proxy-go/docs/model-name-translation-202605171430.md)。
>
> **仍有效的部分**: 模型生命周期状态机、自动清理机制、EOL 通知、protected 字段。
>
> **已过时的部分**: §1.2-1.4（product_id 作为唯一标识）、§3.2-3.3（user_groups.models / api_keys.selected_models 存储的是 `api_model_name`，而非 `product_id`）。

# 模型生命周期与 Group 模型更新集成设计

**文档编号:** model-lifecycle-group-integration-202605151500
**创建时间:** 2026-05-15 15:00
**状态:** 80% 已实现 ⚠️ 部分语义已过时（见上方警告）

---

## 0. 问题背景

### 0.1 别名隐患：多版本共存冲突

当前 `service_models` 表的 `provider_aliases` 字段（JSONB 数组）用于存储模型别名，例如：
- `claude-3-5-sonnet-20241022` → 别名 `claude-3.5-sonnet`

**隐患场景：** 当同一 provider 的多个版本长期共存时（如 opus-4.5 和 opus-4.7 共存一年），它们完全靠费用和服务能力区分。如果都加入同一个 group：
- `product_id = "claude-opus-4-5"` → 别名可能包含 `claude-opus`
- `product_id = "claude-opus-4-7"` → 别名也可能包含 `claude-opus`
- 别名冲突！Group 的 `models` 字段（JSONB 数组）存的是 model name 字符串，无法区分别名属于哪个版本

### 0.2 Group 模型膨胀问题

当 admin 调整 group 中支持的 model（如模型升级 v2.5→v2.7），如果只要有 user 使用就无法从 group 中删除，那 group.models 列表会越来越大，包含大量已停服的旧版本。

### 0.3 核心设计原则（用户确认）

> "有 model 模块来管理模型是否正常 service，而在 group 模块就是单纯引用有效的 model name 即可"

---

## 1. 别名设计重构：product_id 是唯一标识

### 1.1 当前别名机制的问题

| 问题 | 说明 |
|------|------|
| 别名与 product_id 耦合 | `provider_aliases` 绑定在单条 `service_models` 记录上，但别名可能跨版本冲突 |
| Group models 用 model name | `user_groups.models = ["claude-opus", "gpt-4o"]` — 如果别名冲突，无法路由到正确版本 |
| Proxy 路由依赖 model name | Proxy 的 `resolveModel()` 用 model name 匹配，别名冲突会导致路由错误 |

### 1.2 新设计：product_id 作为 Group 层唯一标识

**核心规则：Group 的 `models` 字段只存 `product_id`，不存别名。**

```
service_models:
  id=uuid-1, provider=anthropic, product_id="claude-opus-4-5", api_model_name="claude-opus-4-5-20250920", status=active
  id=uuid-2, provider=anthropic, product_id="claude-opus-4-7", api_model_name="claude-opus-4-7-20260515", status=active

user_groups.models = ["claude-opus-4-5", "claude-opus-4-7", "gpt-4o", "deepseek-chat"]
```

**别名的作用域限定：**

| 层级 | 标识 | 作用 |
|------|------|------|
| **Group 层** | `product_id` | 权限边界、计费分组、用户选择 |
| **Proxy 层** | `product_id` → `api_model_name` | 实际 API 调用时的模型名映射 |
| **别名层** | `provider_aliases` | 仅用于用户搜索/显示友好名，**不参与路由决策** |

### 1.3 多版本共存路由规则

**必须精准匹配 product_id，不使用别名路由。**

- 用户请求 `claude-opus-4-5` → 精准匹配，正常路由
- 用户请求 `claude-opus`（模糊名）→ **返回错误**，不自动匹配任何版本
- 原因：不同版本价格差异太大（如 opus 4.5 vs 4.7），模糊匹配容易引起计费纠纷
- **别名不参与路由决策，仅用于 UI 搜索/显示**

### 1.4 别名在 Group 模型列表中的处理

**方案：别名仅作为 UI 搜索/筛选辅助，不存入 group.models，不参与路由。**

前端创建 Key 选择模型时：
- 搜索框支持别名匹配（如输入 "opus" 可搜到 claude-opus-4-5 和 claude-opus-4-7）
- 但最终存入 `selected_models` 的始终是 `product_id`
- UI 显示：`claude-opus-4-5` + 价格信息，让用户明确选择
- **不使用别名作为 API 请求的 model 参数**

---

## 2. 模型生命周期状态机与 Group 自动清理

### 2.1 当前状态机

```
pending → active → paused → active (恢复)
                 → eol_pending → eol → discontinued
                 → ignored (不纳入服务)
```

| 状态 | 含义 | 是否可路由 | 是否在 Group 中 |
|------|------|-----------|----------------|
| `pending` | 新同步，待确认 | ❌ | ❌ |
| `active` | 正常服务 | ✅ | ✅ |
| `paused` | 临时暂停 | ❌ | ✅（保留，可能恢复） |
| `eol_pending` | EOL 已宣布，仍在服务 | ✅（有限期） | ✅（带 EOL 提示） |
| `eol` | 已停止服务 | ❌ | ❌（自动移除） |
| `discontinued` | 完全停服 | ❌ | ❌（自动移除） |
| `ignored` | 不纳入服务 | ❌ | ❌ |

### 2.2 自动清理机制设计

**核心逻辑：Model 模块管理服务状态，Group 模块引用有效 model name。**

#### 2.2.1 触发时机

| 触发方式 | 说明 |
|----------|------|
| **定时任务（24h）** | 与现有 `syncAIModels` 刷新周期同步，每次刷新后执行清理 |
| **状态变更事件** | admin 确认 EOL / discontinue 时，立即触发清理 |
| **手动触发** | admin 可在 Dashboard 手动触发 "清理无效模型" |

#### 2.2.2 清理流程

```
Step 1: 找出所有不可路由的 model product_id
  SELECT product_id FROM service_models
  WHERE status IN ('eol', 'discontinued', 'ignored', 'pending')
    AND provider || '/' || product_id NOT IN (保留列表)

Step 2: 从所有 group.models 中移除这些 product_id
  UPDATE user_groups
  SET models = (
    SELECT jsonb_agg(elem)
    FROM jsonb_array_elements_text(models) elem
    WHERE elem NOT IN (不可路由列表)
  )
  WHERE models IS NOT NULL

Step 3: 级联清理受影响 key 的 selected_models
  UPDATE api_keys
  SET selected_models = (
    SELECT jsonb_agg(elem)
    FROM jsonb_array_elements_text(selected_models) elem
    WHERE elem NOT IN (不可路由列表)
  )
  WHERE selected_models IS NOT NULL
    AND group_id IN (受影响的 group ID 列表)

Step 4: 通知受影响用户
  - 查询哪些 user 的 key 的 selected_models 被移除了模型
  - 发送邮件通知：模型 X 已停服，请登录 Dashboard 调整

Step 5: 处理 selected_models 变空的 key
  - 自动 fallback 到 group.models 的第一个模型（保底可用）
  - 发送邮件提醒用户：您的 Key 因模型停服已自动调整，请登录 Dashboard 确认
  - **不暂停 Key** — 保证服务可用性，但必须通知用户
```

#### 2.2.3 EOL_pending 特殊处理

`eol_pending` 状态的模型仍在服务，不应立即从 group 移除。但需要：
- 在 Group 模型列表中标记 EOL 状态和截止日期
- 在用户 Key 选择模型时显示 EOL 提示
- 到 `eol_service_at` 日期后，自动转为 `eol`，触发清理

```sql
-- EOL_pending 模型在 group 列表中的标记方式（前端展示）
SELECT sm.product_id, sm.status, sm.eol_service_at
FROM service_models sm
WHERE sm.product_id IN (SELECT jsonb_array_elements_text(g.models) FROM user_groups g WHERE g.id = $1)
  AND sm.status = 'eol_pending'
```

### 2.3 EOL_pending 通知机制

模型进入 `eol_pending` 后，在停止服务前分阶段提醒用户：

| 时间点 | 通知内容 | 触发方式 |
|--------|----------|----------|
| 进入 eol_pending 时 | 模型即将停服，请在截止日期前调整 | 状态变更时即时发送 |
| 停服前 7 天 | 模型将在 7 天后停服 | 定时任务检查 |
| 停服前 3 天 | 模型将在 3 天后停服，请尽快调整 | 定时任务检查 |
| 停服前 1 天 | 模型将在明天停服，请立即调整 | 定时任务检查 |
| 停服当天 | 模型已停服，Key 已自动调整 | 清理流程触发 |

通知逻辑：
```sql
-- 查询需要发送提醒的 eol_pending 模型
SELECT sm.product_id, sm.eol_service_at,
       EXTRACT(DAY FROM sm.eol_service_at - NOW()) as days_remaining
FROM service_models sm
WHERE sm.status = 'eol_pending'
  AND sm.eol_service_at IS NOT NULL
  AND sm.eol_service_at > NOW()
  AND sm.eol_service_at - NOW() <= INTERVAL '7 days'
  AND (
    -- 未发送过对应天数的通知
    sm.last_eol_notified_at IS NULL
    OR (
      EXTRACT(DAY FROM sm.eol_service_at - sm.last_eol_notified_at) != EXTRACT(DAY FROM sm.eol_service_at - NOW())
      AND sm.last_eol_notified_at < NOW() - INTERVAL '12 hours'  -- 避免重复发送
    )
  )
```

service_models 表扩展：
```sql
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS eol_service_at TIMESTAMP;
COMMENT ON COLUMN service_models.eol_service_at IS 'Planned service end date for EOL models';

ALTER TABLE service_models ADD COLUMN IF NOT EXISTS last_eol_notified_at TIMESTAMP;
COMMENT ON COLUMN service_models.last_eol_notified_at IS 'Last EOL notification sent timestamp';
```

### 2.4 保留列表（Safety Net）

某些模型即使状态异常也不应自动移除（如核心模型、admin 手动标记的保留模型）：

```sql
-- 在 service_models 表增加字段
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS protected BOOLEAN DEFAULT false;
COMMENT ON COLUMN service_models.protected IS 'Admin-marked: never auto-remove from groups even if EOL';
```

---

## 3. 数据模型变更

### 3.1 service_models 表扩展

**状态：✅ 已实现**

迁移文件：`migrations/202605151600_model_lifecycle_group_integration.sql`

```sql
-- 1. 保护标记（防止核心模型被误删）
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS protected BOOLEAN DEFAULT false;
COMMENT ON COLUMN service_models.protected IS 'Admin-marked: never auto-remove from groups even if EOL';

-- 2. 最后清理时间记录
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS last_group_cleanup_at TIMESTAMP;
COMMENT ON COLUMN service_models.last_group_cleanup_at IS 'Last time this model was cleaned from groups';

-- 3. EOL 停服日期
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS eol_service_at TIMESTAMP;
COMMENT ON COLUMN service_models.eol_service_at IS 'Planned service end date for EOL models';

-- 4. EOL 通知记录
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS last_eol_notified_at TIMESTAMP;
COMMENT ON COLUMN service_models.last_eol_notified_at IS 'Last EOL notification sent timestamp';
```

> **注意**：`protected` 字段虽然在 DB 和清理 SQL 中已使用，但**尚无 Admin API 接口**可以读取或修改该字段。当前为只读状态。

### 3.2 user_groups.models 语义明确化

**当前：** `user_groups.models` 是 JSONB 数组，存的是 model name（可能是 product_id 或别名）
**改为：** `user_groups.models` 只存 `product_id`，语义明确

```sql
COMMENT ON COLUMN user_groups.models IS 'JSON array of service_models.product_id — only valid/servable models';
```

### 3.3 api_keys.selected_models 语义明确化

同理，`selected_models` 也只存 `product_id`：

```sql
COMMENT ON COLUMN api_keys.selected_models IS 'JSON array of service_models.product_id selected by user';
```

---

## 4. 代码实现要点

### 4.1 Portal 端（ai-portal-go）

#### 4.1.1 模型清理定时任务

**状态：✅ 已实现**

`internal/routes/models.go` 已实现两个清理函数：

- `cleanupSingleModelFromGroups(ctx, productID)` — 针对单个模型的即时清理
- `CleanupAllInvalidModels()` — 批量清理所有 `eol`/`discontinued`/`ignored` 且非 `protected` 的模型

核心清理 SQL（已上线）：
```go
// 从所有 group.models 中移除无效模型
UPDATE user_groups SET models = (
    SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
    FROM jsonb_array_elements_text(COALESCE(models, '[]'::jsonb)) elem
    WHERE elem NOT IN (SELECT product_id FROM service_models
                       WHERE status IN ('eol', 'discontinued', 'ignored')
                       AND protected = false)
), updated_at = NOW()
WHERE models IS NOT NULL

// 级联清理受影响 key 的 selected_models
UPDATE api_keys SET selected_models = (
    SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
    FROM jsonb_array_elements_text(COALESCE(selected_models, '[]'::jsonb)) elem
    WHERE elem NOT IN (不可路由列表)
), updated_at = NOW()
WHERE selected_models IS NOT NULL
```

清理后自动 fallback：若 `selected_models` 被清空，自动回退到所属 group 的第一个可用模型，并发送邮件通知用户。

#### 4.1.2 状态变更即时清理

**状态：✅ 已实现**

`confirmEOL` 和 `discontinue` handler 中已触发即时清理：

```go
// internal/routes/models.go:338-344
func (r *ModelRoutes) confirmEOL(c *gin.Context) {
    // ... existing status update to 'eol' ...
    r.cleanupSingleModelFromGroups(c.Request.Context(), productID)
}

// internal/routes/models.go:347-353
func (r *ModelRoutes) discontinue(c *gin.Context) {
    // ... existing status update to 'discontinued' ...
    r.cleanupSingleModelFromGroups(c.Request.Context(), productID)
}
```

#### 4.1.3 邮件通知

**状态：✅ 已实现**

`pkg/mail/mail.go` 已新增两个通知方法：

- `SendModelDiscontinuedNotification(to, modelName, dashboardURL)` — 模型已停服通知
- `SendEOLReminderNotification(to, modelName, daysRemaining, eolDate, dashboardURL)` — EOL 倒计时提醒

调用位置：
- `internal/routes/models.go:621` — 停服后立即通知受影响用户
- `internal/routes/models.go:714` — 定时任务发送 EOL 提醒（7/3/1 天）

### 4.2 Proxy 端（ai-proxy-go）

#### 4.2.1 product_id → api_model_name 映射

**状态：✅ 已实现（Portal 侧）**

Portal 端已完成核心映射同步：
- `internal/routes/sync.go:463` 的 `buildModelNameMap()` 构建 `(provider, product_id, api_model_name)` 映射列表。
- `GET /api/admin/models/sync` 返回映射数据，供 ai-proxy-go 拉取同步。
- `apikeys_validate.go` 中 `allowed_models` 已按 `COALESCE(api_model_name, product_id)` 过滤。

Proxy 端是否已消费该映射并维护本地映射表，需在 `ai-proxy-go` 仓库中单独确认。

#### 4.2.2 别名路由规则

**状态：✅ 已实现（精确匹配拒绝）**

`resolveModel` 当前仅支持精确匹配 `allowed_models` 列表中的 `product_id`，非精确匹配直接返回错误。已实现：
- 精确匹配校验（非 `product_id` 的请求直接拒绝，返回明确错误信息）
- wgit-maas-latest 别名路由（single/round_robin/priority 三种模式）

尚未实现（P2，低优先级）：
- 别名歧义检测（如 `"claude-opus"` 同时匹配多个版本时返回明确错误）
- 模糊匹配拦截（当前已通过精确匹配间接实现）

---

## 5. 实施优先级与状态

| 优先级 | 任务 | 状态 | 说明 |
|--------|------|------|------|
| **P0** | Group.models 语义明确化：只存 product_id | ✅ 已实现 | 迁移 + 注释 + 代码逻辑已统一 |
| **P0** | 禁止别名路由：模糊名必须返回错误 | ✅ 已实现 | 精确匹配校验，非 allowed_models 直接拒绝 |
| **P1** | 模型清理定时任务 | ✅ 已实现 | `CleanupAllInvalidModels()` + 24h 定时触发 |
| **P1** | 状态变更即时清理 | ✅ 已实现 | `confirmEOL`/`discontinue` 即时调用 `cleanupSingleModelFromGroups()` |
| **P1** | EOL 分阶段提醒 | ✅ 已实现 | 停服前 7/3/1 天邮件 + 停服当天通知 |
| **P1** | Key selected_models 清空时 fallback + 邮件 | ✅ 已实现 | 自动 fallback 到 group 第一个模型 + 邮件通知 |
| **P2** | protected 标记 | 🟡 部分实现 | DB 字段 + 清理 SQL 已使用，但无 Admin API 可读写 |
| **P2** | Proxy product_id → api_model_name 映射同步 | ⬜ 待实现 | 需 Portal → Proxy 同步映射表 |

---

## 6. 与 AccessAPI 的区别

| 维度 | AccessAPI (new-api) | 我们的方案 |
|------|---------------------|-----------|
| 模型标识 | 混用别名和真实名 | **product_id 是唯一标识，别名仅用于 UI** |
| 多版本共存 | 无明确机制 | **歧义检测 + 版本明确选择** |
| EOL 处理 | 手动删除 | **状态机 + 自动清理 + 邮件通知** |
| Group 模型管理 | 静态列表 | **动态引用有效模型，自动同步** |
| 用户通知 | 无 | **邮件通知 + Key 暂停保护** |

---

## 7. 别名问题的最终结论

**别名（provider_aliases）的定位：**
- ✅ 用于 UI 搜索/筛选（用户输入 "opus" 搜到 claude-opus-4-5 和 claude-opus-4-7）
- ✅ 用于显示友好名（`claude-opus-4-5 (Opus 4.5)`）
- ❌ **不用于 Group.models 存储**
- ❌ **不用于 Proxy 路由决策**
- ❌ **不用于 API 请求的 model 参数**
- ❌ **不用于模糊匹配** — 必须精准匹配 product_id，避免计费纠纷

**多版本共存的核心规则：**
- Group.models 存 `["claude-opus-4-5", "claude-opus-4-7"]`（明确的 product_id）
- 用户创建 Key 时必须选择具体版本（4.5 或 4.7），不能选模糊的 "claude-opus"
- API 请求必须传入完整 product_id，模糊名返回错误
- 每个版本有独立的定价（input_price, output_price），用户根据费用和服务能力选择
- **不使用别名路由** — 不同版本价格差异太大，模糊匹配容易引起计费纠纷