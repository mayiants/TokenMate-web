> **⚠️ PARTIALLY OUTDATED** — 2026-05-06 起草，用户已确认。2026-05-16/17 架构重构后，模型标识已从 `product_id` 迁移至 `api_model_name`。本文档中关于模型存储/选择使用 `product_id` 的描述已过时。详见 `../../ai-proxy-go/docs/model-name-translation-202605171430.md`。

# Phase 2: Dashboard 配置页面 + Admin Group 管理（修订版 v2）

**Date:** 2026-05-06 18:00 UTC  
**Status:** ACTIVE — user confirmed 2026-05-06 ⚠️ 部分语义已过时  
**Scope:** `ai-portal-go` — Dashboard frontend + Admin backend/frontend  
**Depends on:** Phase 1 (`phase1-implementation-plan-202605061500.md`)  
**Tech Stack:** Go HTML template + HTMX + vanilla JS  

---

## 1. 用户反馈整合（本版本核心变更）

| 反馈项 | 原始设计 | 修订后 |
|--------|----------|--------|
| **Key 类型 UI** | economy / general / subscription / enterprise 四种 | general（含 economy + general group）、subscription、enterprise 三种 |
| **模型路由** | 单模型 `current_model` | 三种模式：single / round_robin / priority，新增 `selected_models` |
| **Group 优先级** | 未限定数量 | 最多 3 个 group，最后一个是 fallback |
| **计费** | 单一 group 计费 | 支持跨 group 混合计费（billing 模块需适配） |
| **Economy 描述** | "85折计费" | "折扣计费"，折扣率可配置 |
| **限流** | rpm + tpm 两个参数 | rpm/rph/rp5h/rpw/rpmonth + tpm/tph/tpd/tpw/tpmonth，留空不约束 |
| **模型多选框** | 简单多选 | 按 provider 分组折叠 + 搜索过滤 |
| **Role 修改** | 列表页下拉直接改 | 仅编辑弹窗内修改 + 二次确认，不可提为 super-admin |
| **模型显示名** | 人类易读名 | `product_id`（实际 model name，用户可直接复制配置） |
| **Group 删除** | 简单删除 | default group 不可删；其他需确认无关联 key |
| **Subscription** | admin 创建 | 用户购买 + admin 批量创建 |
| **Enterprise** | Phase 2 | Phase 3 |

---

## 2. 数据库 Schema 变更（Phase 2 迁移）

```sql
-- Phase 2: Dashboard 模型路由模式支持
-- Migration: 202605061800_phase2_dashboard.sql

-- ============================================
-- 1. api_keys 表扩展：模型路由模式
-- ============================================

ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS model_routing_mode VARCHAR(20) DEFAULT 'single'
    CHECK (model_routing_mode IN ('single', 'round_robin', 'priority')),
  ADD COLUMN IF NOT EXISTS selected_models JSONB DEFAULT '[]';

COMMENT ON COLUMN api_keys.model_routing_mode IS
  'Model routing strategy: single=hit current_model, round_robin=rotate selected_models, priority=fallback selected_models';
COMMENT ON COLUMN api_keys.selected_models IS
  'JSON array of selected model names for round_robin/priority mode';

-- ============================================
-- 2. user_groups 约束：保留至少一个 default group
-- ============================================

-- Add a marker column to identify the system default fallback group
ALTER TABLE user_groups
  ADD COLUMN IF NOT EXISTS is_default BOOLEAN DEFAULT false;

COMMENT ON COLUMN user_groups.is_default IS
  'System default fallback group, cannot be deleted';

-- Set the first general group as default if none is marked
UPDATE user_groups SET is_default = true
WHERE is_default = false AND group_type = 'general'
  AND id = (SELECT id FROM user_groups WHERE group_type = 'general' ORDER BY created_at LIMIT 1);

-- ============================================
-- 3. 为现有 key 设置默认值
-- ============================================

UPDATE api_keys SET model_routing_mode = 'single' WHERE model_routing_mode IS NULL;
UPDATE api_keys SET selected_models = '[]' WHERE selected_models IS NULL;

-- 将现有 current_model 迁移到 selected_models（单元素数组）
UPDATE api_keys
SET selected_models = jsonb_build_array(current_model)
WHERE current_model IS NOT NULL AND current_model != ''
  AND (selected_models IS NULL OR selected_models = '[]');
```

---

## 3. Dashboard — "我的 API Keys" 页面

### 3.1 页面路由与模板

| 文件 | 路由 | 说明 |
|------|------|------|
| `web/templates/apikeys.html` | `/apikeys` | 现有页面，大幅扩展 |

### 3.2 Key 列表展示

```
┌─────────────────────────────────────────────────────────────────────────┐
│  我的 API Keys                                              [+ 新建Key] │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │  sk-wgit-abcd...  │  标准  │  glm5  │  Single  │  活跃  │ [设置] │   │
│  │  生产环境          │       │当前模型│  单模型   │       │        │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │  sk-wgit-tp-efgh.. │  套餐  │  3模型 │ RoundRobin│  活跃  │ [设置] │   │
│  │  Token Plan        │       │       │  轮询均衡  │       │        │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

**每行显示字段：**
- `key_prefix` — 前缀（如 `sk-wgit-abcd...`）
- `key_type` badge — 标准(general) / 套餐(subscription) / 企业(enterprise)
- `current_model` 或 `selected_models` 数量 — 当前模型或 "N 模型"
- `model_routing_mode` — Single / Round Robin / Priority
- `status` badge — 活跃 / 已禁用
- Group 名称（从 `group_id JOIN user_groups` 获取）
- 操作按钮：设置、复制 key、删除

### 3.3 新建 Key 弹窗

```
┌─────────────────────────────────────────────────────────┐
│  新建 API Key                                            │
├─────────────────────────────────────────────────────────┤
│  名称: [________________________] *                     │
│                                                          │
│  Key 类型:                                               │
│  ● 标准 (sk-wgit-)       — 标准/折扣计费，多Group可选   │
│  ○ 套餐 (sk-wgit-tp-)    — Token/Code Plan 预付费套餐   │
│  ○ 企业 (sk-wgit-en-)    — 企业客户（需管理员授权）      │
│                                                          │
│  模型路由模式:                                           │
│  ● 单一模型    ○ 轮询负载均衡    ○ 优先级顺序            │
│                                                          │
│  ── 加入 Group（最多3个，按优先级排列） ──               │
│  优先级 1: [general-1  ▼]  ← 主Group，计费按此Group     │
│  优先级 2: [economy-1  ▼]  ← Fallback 1                 │
│  优先级 3: [— 不选 ▼]     ← Fallback 2（可选）          │
│  (仅显示与Key类型兼容的Group：general/economy/sub/ent)   │
│                                                          │
│  ── 选择模型 ──                                          │
│  当选择"单一模型"时，从主Group的模型中选一个:             │
│  [glm5 ▼]                                                │
│  当选择"多模型"时，从主Group的模型中多选:                │
│  [☑] glm5  [☑] kimi-k2.5  [☐] deepseek-v3.2            │
│                                                          │
│  [取消]                          [确认创建]              │
└─────────────────────────────────────────────────────────┘
```

**关键逻辑：**

1. **Key 类型与可选 Group 的映射：**
   | Key 类型 UI | 实际 key_type | 可加入的 group_type |
   |------------|---------------|---------------------|
   | 标准 | general | general, economy |
   | 套餐 | subscription | subscription |
   | 企业 | enterprise | enterprise |

2. **Group 选择规则：**
   - 最多 3 个 group，按优先级排列
   - 优先级 1 是主 group，决定计费倍率和默认模型列表
   - 优先级 2/3 是 fallback group
   - 创建时 `group_id = 优先级1`，`group_priority = [优先级1, 优先级2, 优先级3]`

3. **模型选择规则：**
   - 单模型模式：`current_model = 选中的模型`，`selected_models = [该模型]`
   - 多模型模式（round_robin/priority）：`selected_models = 选中的多个模型`，`current_model = 第一个模型`

4. **计费问题：**
   - 创建时 key 绑定到优先级 1 的 group，计费按此 group 的 `pricing_ratio`
   - 如果 Proxy fallback 到优先级 2/3 的 group，计费仍然按优先级 1 的 group（简化设计）
   - 如需按实际使用的 group 计费，需在 billing 模块中记录每个请求实际命中的 group

### 3.4 Key 设置弹窗

```
┌─────────────────────────────────────────────────────────┐
│  设置: sk-wgit-abcd...                                   │
├─────────────────────────────────────────────────────────┤
│  模型路由模式:                                           │
│  ● 单一模型    ○ 轮询负载均衡    ○ 优先级顺序            │
│                                                          │
│  选择模型:                                               │
│  ┌─────────────────────────────────┐                    │
│  │ ○ qwen3.6                       │                    │
│  │ ○ minimax-m2.5                  │                    │
│  │ ● glm5        ← 已选            │                    │
│  │ ○ deepseek-v3.2                 │                    │
│  └─────────────────────────────────┘                    │
│  (模型列表来自主Group: general-1)                        │
│                                                          │
│  Group 优先级:                                           │
│  1. general-1  [主Group]  [×移除]                        │
│  2. economy-1  [Fallback] [×移除]                        │
│  3. —          [添加]                                    │
│  (最多3个，移除后自动滑入下一优先级)                      │
│                                                          │
│  [删除 Key]      [保存]  [取消]                         │
└─────────────────────────────────────────────────────────┘
```

**关键逻辑：**
- 修改 `model_routing_mode` / `current_model` / `selected_models` → `PATCH /api/keys/:id`
- 修改 `group_priority` → `PATCH /api/keys/:id`
- 移除某个 group 后，后续 group 自动前移
- 保存后 2-3 分钟内 Proxy 通过 sync/validate 获取新配置

---

## 4. Admin — Group 管理页面

### 4.1 页面路由与模板

| 文件 | 路由 | 说明 | 状态 |
|------|------|------|------|
| `web/templates/admin-groups.html` | `/admin/groups` | Group 列表 + CRUD | 新增 |
| `web/templates/admin-group-edit.html` | `/admin/groups/:id/edit` | Group 编辑表单 | 新增 |

### 4.2 Group 列表页面

```
┌─────────────────────────────────────────────────────────────────────────┐
│  用户组管理                                    [+ 新建] [从模板创建]     │
├─────────────────────────────────────────────────────────────────────────┤
│  筛选: [全部 ▼] [类型: 全部 ▼] [计费: 全部 ▼] [状态: 全部 ▼]            │
├─────────────────────────────────────────────────────────────────────────┤
│  名称        │ 类型       │ 计费   │ 倍率  │ 模型数 │ 成员 │ 状态  │ 操作 │
│  ─────────────────────────────────────────────────────────────────────  │
│  general-1   │ 标准       │ token  │ 1.00  │ 5      │ 45   │ 活跃  │ 编辑 │
│  economy-1   │ 折扣       │ token  │ 0.85  │ 5      │ 12   │ 活跃  │ 编辑 │
│  sub-39-t    │ 套餐       │ token  │ 1.20  │ 4      │ 8    │ 活跃  │ 编辑 │
│  sub-99-c    │ 套餐       │ request│ 1.20  │ 4      │ 3    │ 活跃  │ 编辑 │
│  ent-corp-A  │ 企业       │ token  │ 0.90  │ 10     │ 25   │ 活跃  │ 编辑 │
└─────────────────────────────────────────────────────────────────────────┘
```

**操作按钮规则：**
- `is_default=true` 的 group：不可删除，仅可编辑/禁用
- 有 key 关联的 group：不可直接删除，需先移除所有 key 关联
- 其他 group：可编辑/禁用/删除

### 4.3 创建/编辑 Group 表单

```
┌─────────────────────────────────────────────────────────┐
│  新建用户组                                              │
├─────────────────────────────────────────────────────────┤
│  名称:           [________________] *                   │
│  描述:           [________________]                     │
│                                                          │
│  Group 类型:     [标准 ▼]                               │
│    - 标准:    标准计费 (sk-wgit-)                       │
│    - 折扣:    折扣计费 (sk-wgit-)                       │
│    - 套餐:    Token/Code Plan (sk-wgit-tp-)             │
│    - 企业:    企业客户 (sk-wgit-en-)                    │
│                                                          │
│  计费模式:       [按Token ▼]                            │
│    - 按Token: 按输入/输出 token 计费                     │
│    - 按Request: 按请求次数计费                           │
│                                                          │
│  计费倍率:       [1.00]                                 │
│  (折扣类型默认0.85，可修改；标准默认1.00)                 │
│                                                          │
│  ── 限流配置（留空 = 不限制） ──                         │
│  请求限流:                                               │
│  RPM:    [60  ]  RPH:    [600 ]  RP5H:   [1200]        │
│  RPW:    [9000]  RPMonth:[18000]                        │
│  Token限流:                                              │
│  TPM:    [1M  ]  TPH:    [10M ]  TPD:    [30M ]        │
│  TPW:    [80M ]  TPMonth:[1600M]                        │
│                                                          │
│  ── 模型列表（按Provider分组） ──                        │
│  [🔍 搜索模型...]                                        │
│  ┌─ Zhipu (2) ─────────────────────────┐               │
│  │ [☑] glm5    [☑] glm5.1              │               │
│  └──────────────────────────────────────┘               │
│  ┌─ Moonshot (2) ──────────────────────┐               │
│  │ [☑] kimi-k2.5  [☐] kimi-k2.6        │               │
│  └──────────────────────────────────────┘               │
│  ┌─ DeepSeek (1) ──────────────────────┐               │
│  │ [☑] deepseek-v3.2                   │               │
│  └──────────────────────────────────────┘               │
│  (数据源: service_models.product_id, 按 provider 分组)   │
│                                                          │
│  状态:           [活跃 ▼]                               │
│                                                          │
│  [取消]                          [创建]                 │
└─────────────────────────────────────────────────────────┘
```

**关键逻辑：**
- 模型列表按 `provider` 分组折叠，支持搜索过滤
- 显示的是 `product_id`（实际 model name），不是人类易读名
- 创建 subscription group 时，可从模板快速填充限流参数
- `is_default` 字段仅系统设置，admin 不可修改

### 4.4 从模板创建 Subscription Group

```
┌─────────────────────────────────────────────────────────┐
│  从模板创建套餐 Group                                     │
├─────────────────────────────────────────────────────────┤
│  套餐类型:                                               │
│  ● Token Plan (按Token计费)                              │
│  ○ Code Plan  (按请求计费)                               │
│                                                          │
│  档次:                                                   │
│  ● ¥39    ○ ¥99    ○ ¥399    ○ ¥799                    │
│                                                          │
│  自动填充参数预览:                                       │
│  ┌─────────────────────────────────────┐                │
│  │ 计费倍率: 1.20                       │                │
│  │ 限流: RPM=60, TPM=1M                │                │
│  │ 模型: 4个高级模型                   │                │
│  └─────────────────────────────────────┘                │
│                                                          │
│  Group 名称: [sub-token-39] (可修改)                     │
│                                                          │
│  [取消]                          [创建]                 │
└─────────────────────────────────────────────────────────┘
```

**8 个预定义模板：**

| 模板 | 类型 | 档次 | 倍率 | 计费模式 | RPM | TPM | RPH | RP5H | RPW | RpMonth |
|------|------|------|------|----------|-----|-----|-----|------|-----|---------|
| sub-token-39 | Token | ¥39 | 1.20 | token | 60 | 1M | 600 | 1200 | 9000 | 18000 |
| sub-token-99 | Token | ¥99 | 1.20 | token | 120 | 3M | 1200 | 2400 | 18000 | 36000 |
| sub-token-399 | Token | ¥399 | 1.20 | token | 300 | 10M | 3000 | 6000 | 45000 | 90000 |
| sub-token-799 | Token | ¥799 | 1.20 | token | 600 | 30M | 6000 | 12000 | 90000 | 180000 |
| sub-code-39 | Code | ¥39 | 1.20 | request | 60 | — | 600 | 1200 | 9000 | 18000 |
| sub-code-99 | Code | ¥99 | 1.20 | request | 120 | — | 1200 | 2400 | 18000 | 36000 |
| sub-code-399 | Code | ¥399 | 1.20 | request | 300 | — | 3000 | 6000 | 45000 | 90000 |
| sub-code-799 | Code | ¥799 | 1.20 | request | 600 | — | 6000 | 12000 | 90000 | 180000 |

---

## 5. Admin — 用户管理增强

### 5.1 用户列表修改

**修改 `admin-users.html`：**
- 去掉列表页 role 下拉直接修改（删除 `onchange="this.form.submit()"`）
- 增加 Group 列（显示 group_name）
- 点击 "编辑" 打开弹窗

```
│  用户      │ 邮箱           │ 角色   │ 用户组    │ 类型    │ 余额    │ 操作  │
│  ───────────────────────────────────────────────────────────────────────  │
│  张三      │ z@example.com  │ user   │ general-1 │ general │ ¥128.50 │ 编辑  │
│  李四      │ l@example.com  │ user   │ sub-39-t  │ sub     │ ¥0.00   │ 编辑  │
│  王五      │ w@example.com  │ admin  │ —         │ —       │ ¥999.00 │ 编辑  │
```

### 5.2 编辑用户弹窗（二次确认）

```
┌─────────────────────────────────────────────────────────┐
│  编辑用户: 张三                                          │
├─────────────────────────────────────────────────────────┤
│  名称:     [张三]                                       │
│  邮箱:     [z@example.com]       (不可修改)             │
│  用户组:   [general-1 ▼]      ← 修改后影响新创建的 key │
│  用户类型: [general ▼]                                  │
│  余额:     [128.50]                                     │
│                                                          │
│  ── 角色修改（需谨慎） ──                                │
│  当前角色: user                                          │
│  新角色:   [user ▼]  (不可选 super_admin)               │
│                                                          │
│  [取消]                          [保存]                 │
└─────────────────────────────────────────────────────────┘
```

**关键逻辑：**
- Role 修改在编辑弹窗内进行，列表页仅展示
- 修改 role 时弹出二次确认："确定要将用户角色从 user 修改为 enterprise_admin 吗？"
- 下拉选项中**不显示 super_admin**（仅当前 super_admin 编辑自己时可见）
- 修改 `users.group_id` 只影响**新创建的 key**，已有 key 的 group 不变

---

## 6. 后端 API 变更

### 6.1 新增/修改端点汇总

| 方法 | 端点 | 说明 | 状态 |
|------|------|------|------|
| `GET` | `/api/keys` | Phase 1 已增强 | ✅ |
| `POST` | `/api/keys` | Phase 1 已增强，Phase 2 需支持 `model_routing_mode`/`selected_models` | 🔧 |
| `PATCH` | `/api/keys/:id` | Phase 1 已增强，Phase 2 需支持 `model_routing_mode`/`selected_models` | 🔧 |
| `GET` | `/api/admin/groups` | Phase 1 已增强 | ✅ |
| `POST` | `/api/admin/groups` | Phase 1 已增强 | ✅ |
| `PUT` | `/api/admin/groups/:id` | Phase 1 已增强 | ✅ |
| `GET` | `/api/admin/users` | **增强** — 返回 group 名称 | 🔧 |
| `PUT` | `/api/admin/users/:id` | **增强** — 支持修改 group_id | 🔧 |
| `GET` | `/api/admin/models` | **新增** — 返回 service_models 列表（按 provider 分组） | 🆕 |
| `POST` | `/api/admin/groups/from-template` | **新增** — 从 subscription 模板创建 | 🆕 |

### 6.2 API 详细定义

#### `POST /api/keys`（Phase 2 扩展）

Request:
```json
{
  "name": "生产环境",
  "key_type": "general",
  "model_routing_mode": "single",
  "current_model": "glm5",
  "selected_models": ["glm5"],
  "group_priority": ["uuid-general-1", "uuid-economy-1"]
}
```

#### `PATCH /api/keys/:id`（Phase 2 扩展）

Request:
```json
{
  "name": "新名称",
  "current_model": "kimi-k2.5",
  "model_routing_mode": "round_robin",
  "selected_models": ["glm5", "kimi-k2.5", "deepseek-v3.2"],
  "group_priority": ["uuid-general-1", "uuid-economy-1"]
}
```

#### `GET /api/admin/models`

Response:
```json
{
  "providers": [
    {
      "name": "zhipu",
      "models": [
        {"id": "uuid", "product_id": "glm5", "api_model_name": "glm5", "input_price": 0.001, "output_price": 0.002},
        {"id": "uuid", "product_id": "glm5.1", "api_model_name": "glm5.1", "input_price": 0.002, "output_price": 0.004}
      ]
    },
    {
      "name": "moonshot",
      "models": [
        {"id": "uuid", "product_id": "kimi-k2.5", "api_model_name": "kimi-k2.5", "input_price": 0.001, "output_price": 0.002}
      ]
    }
  ]
}
```

#### `POST /api/admin/groups/from-template`

Request:
```json
{
  "template_type": "subscription-token",
  "tier": 99,
  "name": "sub-token-99"
}
```

Template types: `subscription-token`, `subscription-code`  
Tiers: `39`, `99`, `399`, `799`

---

## 7. Proxy 后端适配（Phase 2）

### 7.1 模型路由模式适配

Proxy `handleChatCompletions` 需要根据 `model_routing_mode` 处理请求：

| 模式 | 请求 model | Proxy 处理 |
|------|-----------|-----------|
| `single` | `wgit-maas-latest` | 替换为 `claims.CurrentModel` |
| `single` | 真实模型名 | 验证 `req.Model == claims.CurrentModel` |
| `round_robin` | `wgit-maas-latest` | 在 `claims.SelectedModels` 中轮询选择一个 |
| `round_robin` | 真实模型名 | 验证 `req.Model ∈ claims.SelectedModels` |
| `priority` | `wgit-maas-latest` | 在 `claims.SelectedModels` 中按优先级选择第一个可用 |
| `priority` | 真实模型名 | 验证 `req.Model ∈ claims.SelectedModels` |

### 7.2 Group Priority Fallback

当主 group 的模型不可用时，Proxy 按 `group_priority` 顺序 fallback：

```
1. 在优先级 1 的 group 中查找模型
2. 未找到 → 在优先级 2 的 group 中查找
3. 未找到 → 在优先级 3 的 group 中查找
4. 仍未找到 → 返回 400 "Model not available in any configured group"
```

### 7.3 计费标记

每个请求需记录实际使用的 group，用于 billing：
- 命中 group 1 → 按 group 1 的 pricing_ratio 计费
- fallback 到 group 2 → 按 group 2 的 pricing_ratio 计费
- billing 模块通过 `X-Actual-Group-ID` header 或 context 获取

---

## 8. 实施顺序

```
Phase 2 实施顺序
├── Step 1: 数据库迁移 (30min)
│   ├── 202605061800_phase2_dashboard.sql
│   │   ├── api_keys: model_routing_mode, selected_models
│   │   ├── user_groups: is_default
│   │   └── 数据默认值填充
│   └── 预计: 0.5h
│
├── Step 2: 后端 API 增强 (2.5h)
│   ├── POST /api/keys — 支持 model_routing_mode, selected_models, group_priority
│   ├── PATCH /api/keys/:id — 同上
│   ├── GET /api/admin/users — 返回 group_name
│   ├── PUT /api/admin/users/:id — 支持 group_id, role 二次确认
│   ├── GET /api/admin/models — 按 provider 分组返回
│   └── POST /api/admin/groups/from-template — 模板创建
│   └── 预计: 2.5h
│
├── Step 3: Admin Group 管理页面 (4h)
│   ├── admin-groups.html — Group 列表（表格 + 筛选 + 分页）
│   ├── admin-group-edit.html — Group 创建/编辑表单
│   │   ├── 模型多选框（按 provider 分组折叠 + 搜索）
│   │   ├── 限流参数输入（5档 RPM + 5档 TPM）
│   │   └── 折扣倍率可配置
│   ├── admin.html — 导航添加 "用户组" 入口
│   └── 预计: 4h
│
├── Step 4: Admin 用户管理增强 (1.5h)
│   ├── admin-users.html — 增加 group 列
│   ├── admin-users.html — 去掉列表页 role 下拉直接修改
│   ├── 编辑弹窗 — role 修改 + 二次确认
│   └── 预计: 1.5h
│
├── Step 5: Dashboard "我的 API Keys" 重构 (5h)
│   ├── apikeys.html — 列表展示增强（key_type badge, 模型数, 路由模式）
│   ├── 新建 Key 弹窗 — key_type 选择 + group 优先级（最多3个）
│   ├── 模型选择器 — single/round_robin/priority 模式切换
│   ├── Key 设置弹窗 — 模型/路由模式/Group 优先级修改
│   └── 预计: 5h
│
├── Step 6: Subscription 模板 (1.5h)
│   ├── POST /api/admin/groups/from-template 后端
│   ├── Admin 页面 "从模板创建" 弹窗
│   └── 预计: 1.5h
│
├── Step 7: Proxy 路由适配 (2h)
│   ├── round_robin / priority 模式实现
│   ├── group_priority fallback 路由
│   ├── 计费 group 标记（供 billing 模块使用）
│   └── 预计: 2h
│
└── 总计预计: 17h（可拆分为 3 个迭代，每迭代约 6h）
```

**建议迭代拆分：**

| 迭代 | 内容 | 预计 |
|------|------|------|
| **迭代 1** | Step 1-2（迁移 + 后端 API）+ Step 4（Admin 用户增强） | 4.5h |
| **迭代 2** | Step 3（Admin Group 页面）+ Step 6（Subscription 模板） | 5.5h |
| **迭代 3** | Step 5（Dashboard API Keys 重构）+ Step 7（Proxy 路由） | 7h |

---

## 9. 与 Phase 1 的衔接验证清单

| Phase 1 已完成 | Phase 2 验证点 |
|----------------|---------------|
| `api_keys.group_id` | Dashboard 新建 key 时正确设置 group_id（优先级1） |
| `api_keys.key_type` | Dashboard key_type 选择正确映射到前缀 |
| `api_keys.current_model` | single 模式下正常，round_robin/priority 模式下作为首选 |
| `api_keys.billing_mode` | Admin Group 表单正确配置 |
| `user_groups.group_type` | Admin 创建 Group 时正确选择 |
| `user_groups.pricing_ratio` | Admin 可配置折扣倍率 |
| `user_groups.models` | Admin 模型多选框数据来源 |
| Proxy `wgit-maas-latest` → `current_model` | single 模式正常；round_robin/priority 扩展 |
| Proxy Claims group 字段 | Proxy 需扩展 `SelectedModels` / `ModelRoutingMode` |

---

## 10. Open Questions（已回答 / 待确认）

| # | 问题 | 回答/状态 |
|---|------|----------|
| 1 | Dashboard 框架 | ✅ Go HTML template + HTMX + vanilla JS |
| 2 | 模型显示名称 | ✅ `product_id`（实际 model name） |
| 3 | Group 删除策略 | ✅ default 不可删；其他需确认无关联 key；移除后自动滑入下一优先级 |
| 4 | Subscription 购买流程 | ✅ 用户购买 + admin 批量创建 |
| 5 | Enterprise 邀请 | ✅ Phase 3 |
| 6 | **待确认：混合计费** | 跨 group fallback 时按哪个 group 计费？（当前设计：按实际命中的 group 计费） |
| 7 | **待确认：模型多选框** | 按 provider 分组折叠 + 搜索过滤是否足够？ |
| 8 | **待确认：group 数量上限** | 最多 3 个 group（含 fallback）是否满足需求？ |
| 9 | **待确认：Proxy 轮询状态存储** | round_robin 的轮询计数器存在哪里？（Redis / 内存 / 无需持久化？） |


---

## 11. 数据流设计（新增）

### 11.1 新建 Key 完整链路

```
用户登录 Dashboard → 点击 "我的 API Keys" → 点击 "+ 新建"
    ↓
前端弹窗:
    → 选择 key_type (general/subscription/enterprise)
    → 选择 model_routing_mode (single/round_robin/priority)
    → 选择 Group 优先级（最多3个，从兼容的 group_type 中筛选）
    → 选择模型（从主Group的 models 列表中）
    → 点击确认
    ↓
POST /api/keys
    → 验证 group_priority 中所有 group 的 group_type 与 key_type 兼容
    → group_id = group_priority[0]（主Group）
    → 从主Group继承: billing_mode, pricing_ratio
    → 从主Group继承默认值: rate_limit_rpm, rate_limit_tpm
    → INSERT api_keys (...)
    → 返回完整 key（仅展示一次）
    ↓
用户看到新 key，可点击 "设置" 修改模型/路由模式/Group优先级
    ↓
PATCH /api/keys/:id
    → 更新 model_routing_mode / current_model / selected_models / group_priority
    ↓
用户调用 API: Bearer sk-wgit-xxx, model: "wgit-maas-latest"
    ↓
[Proxy] validateWithPortalAPI
    → 返回 Claims（含 model_routing_mode, selected_models, current_model, group_priority）
    ↓
[Proxy] handleChatCompletions
    → 根据 model_routing_mode 解析/验证模型
    → single: wgit-maas-latest → current_model
    → round_robin: wgit-maas-latest → selected_models 轮询
    → priority: wgit-maas-latest → selected_models 按优先级
    → 根据模型确定 provider → 选择 upstream key → 转发请求
    ↓
请求完成 → billing 模块根据实际使用的 group 计费
```

### 11.2 Admin 创建 Group 完整链路

```
Admin 登录 → "用户组管理" → "+ 新建"
    ↓
填写表单: 名称、类型、计费模式、倍率、限流参数、模型列表
    → 模型多选框: GET /api/admin/models → 按 provider 分组返回
    → 选择模型（product_id 列表）
    ↓
POST /api/admin/groups
    → INSERT user_groups (name, group_type, billing_mode, pricing_ratio, models, rpm, rph, ...)
    → 返回 group ID
    ↓
Admin 在列表中看到新 group
    → 可选: "从模板创建" → 选择 subscription plan + 档次 → 自动填充所有参数
    → 可选: "添加成员" → 选择用户 → UPDATE users SET group_id = ...
```

---

## 12. Proxy 路由适配详细设计

### 12.1 Claims 扩展（Proxy `internal/auth/auth.go`）

```go
type Claims struct {
    // Phase 1 字段
    UserID, APIKeyID, KeyHash, Role, AccessMode string
    AllowedModels []string
    RateLimitRPM, RateLimitTPM int
    GroupID, KeyType, CurrentModel, BillingMode string
    PricingRatio float64
    GroupPriority []string
    Models []string
    // Phase 2 新增
    ModelRoutingMode string   `json:"model_routing_mode"`
    SelectedModels   []string `json:"selected_models"`
    jwt.RegisteredClaims
}
```

### 12.2 模型解析函数

```go
// resolveModel 根据 model_routing_mode 解析请求中的 model
func resolveModel(reqModel string, claims *auth.Claims) (string, error) {
    // 1. wgit-maas-latest 别名解析
    if reqModel == "wgit-maas-latest" {
        switch claims.ModelRoutingMode {
        case "single":
            if claims.CurrentModel == "" {
                return "", fmt.Errorf("MODEL_NOT_CONFIGURED: current_model is empty")
            }
            return claims.CurrentModel, nil
        case "round_robin":
            if len(claims.SelectedModels) == 0 {
                return "", fmt.Errorf("MODEL_NOT_CONFIGURED: no models selected")
            }
            // 从 Redis 获取轮询计数器，选择下一个模型
            model := selectRoundRobin(claims.APIKeyID, claims.SelectedModels)
            return model, nil
        case "priority":
            if len(claims.SelectedModels) == 0 {
                return "", fmt.Errorf("MODEL_NOT_CONFIGURED: no models selected")
            }
            // 选择第一个可用模型（后续实现可用性检查）
            return claims.SelectedModels[0], nil
        default:
            return claims.CurrentModel, nil // fallback to single
        }
    }

    // 2. 真实模型名验证
    switch claims.ModelRoutingMode {
    case "single":
        if claims.CurrentModel != "" && reqModel != claims.CurrentModel {
            return "", fmt.Errorf("MODEL_MISMATCH: request=%s, config=%s", reqModel, claims.CurrentModel)
        }
    case "round_robin", "priority":
        if len(claims.SelectedModels) > 0 && !contains(claims.SelectedModels, reqModel) {
            return "", fmt.Errorf("MODEL_NOT_ALLOWED: %s not in selected_models", reqModel)
        }
    }

    return reqModel, nil
}
```

### 12.3 Group Priority Fallback

```go
// resolveProviderWithGroupPriority 按 group_priority 查找模型对应的 provider
func resolveProviderWithGroupPriority(model string, groupPriority []string, portalURL string) (string, error) {
    // 1. 在 group_priority 中的每个 group 查找模型
    for _, groupID := range groupPriority {
        // 查询该 group 的 models 列表（可从缓存或 Portal API 获取）
        groupModels := getGroupModels(groupID, portalURL)
        if contains(groupModels, model) {
            // 找到模型，返回对应的 provider
            provider := relay.GetProviderFromModel(model)
            return provider, nil
        }
    }

    // 2. 所有 group 都没找到
    return "", fmt.Errorf("MODEL_NOT_IN_GROUPS: %s not available in any configured group", model)
}
```

### 12.4 Round Robin 状态存储

**方案：纯 Redis 计数器（已确认）**

```go
func selectRoundRobin(keyID string, models []string) string {
    if len(models) == 0 {
        return ""
    }
    if len(models) == 1 {
        return models[0]
    }

    // Redis key: "rr:{key_id}"
    redisKey := "rr:" + keyID
    count, _ := redisClient.Incr(ctx, redisKey).Result()
    // TTL 24h，过期后自动重置
    redisClient.Expire(ctx, redisKey, 24*time.Hour)

    index := int(count-1) % len(models)
    return models[index]
}
```

**理由（已确认）：**
- ✅ Redis 性能足够，无需内存备份
- 无需持久化：轮询状态丢失后从 0 重新开始即可，不影响正确性
- Redis TTL 自动清理过期 key，无需手动维护
- 分布式环境下多个 Proxy 实例共享同一个 Redis，轮询一致

---

## 13. 回滚策略

Phase 2 涉及数据库 schema 变更和新增前端页面，回滚方式：

### 13.1 数据库回滚

```sql
-- 删除 Phase 2 新增的列
ALTER TABLE api_keys DROP COLUMN IF EXISTS model_routing_mode;
ALTER TABLE api_keys DROP COLUMN IF EXISTS selected_models;
ALTER TABLE user_groups DROP COLUMN IF EXISTS is_default;
```

**注意：** 回滚会丢失 `model_routing_mode` 和 `selected_models` 数据，建议回滚前导出受影响的数据。

### 13.2 代码回滚

- 删除新增模板文件：`admin-groups.html`, `admin-group-edit.html`
- 恢复修改的模板文件：`apikeys.html`, `admin-users.html`, `admin.html`
- 删除新增后端路由处理函数
- 恢复 Phase 1 版本的 `apikeys.go`, `groups.go`

### 13.3 灰度策略

建议按迭代分批上线：
1. **迭代 1**（后端 API + Admin 用户增强）：影响最小，可随时回滚
2. **迭代 2**（Admin Group 页面）：管理员功能，可独立回滚
3. **迭代 3**（Dashboard + Proxy 路由）：影响用户，建议在小范围用户中测试后再全量上线

---

## 14. Open Questions — 已确认答案

| # | 问题 | 确认答案 | 影响 |
|---|------|----------|------|
| 6 | **混合计费** | ✅ 按实际 hit 的 group 计费。每个请求记录 `actual_group_id`，billing 按该 group 的 `pricing_ratio` 计算。**billing 模块较复杂，Proxy→Portal 同步需强化（30-60秒）** | billing 模块设计需支持 per-request group 标记 |
| 7 | **模型多选框** | ✅ 按 provider 分组折叠 + 搜索过滤足够。Group 创建后短时间不修改，admin 有充足时间配置 | 无需额外优化 |
| 8 | **Group 数量上限** | ✅ 最多 3 个（1 主 + 2 fallback）足够。5+5 模型 = 10 个模型，round/priority 服务足够 | 固定上限为 3，前端验证 |
| 9 | **Proxy 轮询状态存储** | ✅ Redis 计数器即可（TTL 24h），无需内存备份。Redis 性能足够 | 纯 Redis 实现，代码见 §12.4 |

---

## 15. 与 Phase 3 的衔接

| Phase 2 交付物 | Phase 3 在此基础上添加 |
|----------------|------------------------|
| Group 管理系统 | Enterprise group（企业客户专属 group） |
| Subscription 模板 | 支付流程集成（Stripe/支付宝/微信） |
| model_routing_mode | 更复杂的负载均衡策略（按 token 价格/延迟选择） |
| Admin 用户管理 | Enterprise admin 邀请流程（邮件邀请 + 验证链接） |
| Dashboard API Keys | Enterprise Dashboard（企业管理员视角） |
