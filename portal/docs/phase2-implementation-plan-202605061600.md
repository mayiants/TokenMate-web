> **⚠️ PARTIALLY OUTDATED** — 2026-05-06 起草。2026-05-16/17 架构重构后，模型标识已从 `product_id` 迁移至 `api_model_name`。本文档中关于模型存储/选择使用 `product_id` 的描述已过时。详见 `../../ai-proxy-go/docs/model-name-translation-202605171430.md`。

# Phase 2: Dashboard 配置页面 + Admin Group 管理

**Date:** 2026-05-06 16:00 UTC  
**Status:** DRAFT — pending discussion  
**Scope:** `ai-portal-go` — Dashboard frontend + Admin backend/frontend  
**Depends on:** Phase 1 (`phase1-implementation-plan-202605061500.md`)  

---

## 1. 目标与范围

Phase 2 的核心目标是：**让用户和管理员能够通过 Web 界面管理 API Key 的 group 配置和模型选择**，不再依赖直接操作数据库或 API。

### 1.1 用户侧（Dashboard）

| 功能 | 说明 | 优先级 |
|------|------|--------|
| 我的 API Keys | 列表展示所有 key，显示类型、group、当前模型、状态 | P0 |
| 创建新 Key | 选择 key_type（economy/general/subscription/enterprise），自动继承 group 配置 | P0 |
| 模型选择器 | 点击选择当前模型，只显示 group 支持的模型列表 | P0 |
| Group 优先级配置 | 为 key 设置 group_priority fallback 顺序 | P1 |
| Key 使用统计 | 展示每个 key 的请求数、token 数、费用 | P1 |

### 1.2 管理侧（Admin）

| 功能 | 说明 | 优先级 |
|------|------|--------|
| Group 列表 | 表格展示所有 user_groups，支持筛选/搜索 | P0 |
| 创建 Group | 表单配置：名称、类型、倍率、计费模式、模型列表、限流参数 | P0 |
| 编辑 Group | 修改 group 配置，查看成员数 | P0 |
| 用户管理增强 | 用户列表显示 group 列，编辑时支持分配 group | P1 |
| Subscription 套餐模板 | 预定义 8 个 subscription group（2 种 plan × 4 个档次） | P1 |

---

## 2. 功能详细设计

### 2.1 Dashboard — "我的 API Keys" 页面

#### 页面布局

```
┌─────────────────────────────────────────────────────────────────┐
│  我的 API Keys                                        [+ 新建] │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ sk-wgit-abcd...  │  general  │  glm5  │  活跃  │  [设置] │   │
│  │ 标准计费key        │  Group-2  │当前模型 │       │        │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ sk-wgit-tp-efg... │ subscription │ kimi-k2.6 │  活跃    │   │
│  │ 套餐key             │  Group-3     │ 当前模型  │  [设置] │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

#### 新建 Key 弹窗

```
┌─────────────────────────────────────────┐
│  新建 API Key                            │
├─────────────────────────────────────────┤
│  名称: [________________]               │
│                                          │
│  Key 类型:                               │
│  ○ 标准 (sk-wgit-)    — 标准计费         │
│  ○ 经济 (sk-wgit-)    — 85折计费         │
│  ● 套餐 (sk-wgit-tp-) — Token/Code Plan │
│  ○ 企业 (sk-wgit-en-) — 企业客户         │
│                                          │
│  选择 Group: [Group-2 ▼]                │
│  (仅显示与用户类型匹配的 group)           │
│                                          │
│  [取消]              [确认创建]          │
└─────────────────────────────────────────┘
```

**关键逻辑：**
- 用户选择 `key_type` 后，前端过滤可选的 group 列表（只显示 `group_type` 匹配的）
- 创建后 key 继承 group 的 `models`/`pricing_ratio`/`rate_limit_rpm` 等配置
- 创建成功后显示完整 key（仅一次），然后只显示前缀

#### Key 设置弹窗

```
┌─────────────────────────────────────────┐
│  设置: sk-wgit-abcd...                   │
├─────────────────────────────────────────┤
│  当前模型:                               │
│  ┌─────────────────────────────────┐    │
│  │ ○ qwen3.6                       │    │
│  │ ○ minimax-m2.5                  │    │
│  │ ● glm5        ← 已选            │    │
│  │ ○ deepseek-v3.2                 │    │
│  └─────────────────────────────────┘    │
│  (仅显示当前 group 支持的模型)           │
│                                          │
│  Group 优先级:                           │
│  1. [Group-2 ▼]  [x]                    │
│  2. [Group-1 ▼]  [x]                    │
│  [+ 添加 fallback group]                │
│                                          │
│  [删除 Key]      [保存]  [取消]         │
└─────────────────────────────────────────┘
```

**关键逻辑：**
- 模型列表从 `api_keys.group_id → user_groups.models` 获取
- 选择模型后调用 `PATCH /api/keys/:id` 更新 `current_model`
- Group 优先级更新 `group_priority` JSONB 数组
- 保存后 2-3 分钟内 Proxy 通过 sync 获取新配置

### 2.2 Dashboard — 模型选择器（独立组件）

#### 组件接口

```javascript
// ModelSelector.vue
props: {
  keyId: String,        // API Key ID
  groupId: String,      // 当前 key 的 group
  currentModel: String, // 当前已选模型
}

// 获取可选模型列表
async function fetchAvailableModels() {
  // GET /api/keys/:id → 返回 key 的 group.models
  // 或者 GET /api/groups/:id → 返回 group 的 models
}
```

#### 合法性检查

| 检查项 | 规则 | 错误提示 |
|--------|------|----------|
| 模型是否在 group 中 | `model ∈ user_groups.models` | "该模型不在您当前 group 的支持列表中" |
| 模型是否有效 | `model` 存在于 `service_models` | "无效模型名称" |
| 用户余额 | `balance > 0` | "余额不足，请先充值" |

**注意：** Dashboard 是点击选择（不是输入），所以理论上不会出现 "模型不在 group 中" 的情况（除非 group 配置被 admin 修改）。

### 2.3 Admin — Group 管理页面

#### Group 列表页面 (`admin-groups.html`)

```
┌─────────────────────────────────────────────────────────────────┐
│  用户组管理                                    [+ 新建用户组]    │
├─────────────────────────────────────────────────────────────────┤
│  筛选: [全部 ▼] [类型: 全部 ▼] [状态: 全部 ▼]                   │
├─────────────────────────────────────────────────────────────────┤
│  名称      │ 类型        │ 倍率  │ 计费  │ 成员 │ 状态  │ 操作  │
│  ─────────────────────────────────────────────────────────────  │
│  economy-1 │ economy     │ 0.85  │ token │ 12   │ 活跃  │ 编辑  │
│  general-1 │ general     │ 1.00  │ token │ 45   │ 活跃  │ 编辑  │
│  sub-39-t  │ subscription│ 1.20  │ token │ 8    │ 活跃  │ 编辑  │
│  sub-99-c  │ subscription│ 1.20  │ req   │ 3    │ 活跃  │ 编辑  │
└─────────────────────────────────────────────────────────────────┘
```

#### 创建/编辑 Group 表单

```
┌─────────────────────────────────────────────────────────┐
│  新建用户组                                              │
├─────────────────────────────────────────────────────────┤
│  名称:           [________________] *                   │
│  描述:           [________________]                     │
│  Group 类型:     [general ▼]                            │
│    - economy: 85折计费                                  │
│    - general: 标准计费                                  │
│    - subscription: 套餐产品                             │
│    - enterprise: 企业客户                               │
│  计费模式:       [token ▼]                              │
│  消费倍率:       [1.00]                                 │
│  限流 (RPM):     [60]                                   │
│  限流 (TPM):     [100000]                               │
│  模型列表:                                               │
│  [☑] qwen3.6    [☑] kimi-k2.5   [☑] minimax-m2.5      │
│  [☑] glm5       [☑] deepseek-v3.2                      │
│  [☐] kimi-k2.6  [☐] minimax-m2.7  [☐] glm5.1          │
│  (模型列表从 service_models 同步)                        │
│  状态:           [活跃 ▼]                               │
│                                                          │
│  [取消]                          [创建]                 │
└─────────────────────────────────────────────────────────┘
```

**关键逻辑：**
- 模型多选框的数据源是 `service_models` 表（通过 `/api/admin/models` 获取）
- 创建 subscription group 时，自动计算并填充对应的限流参数（rpm/rph/rp5h/rpw/rpmonth 或 tpm/tph/tpd/tpw/tpmonth）
- 创建后可通过 `POST /api/admin/groups/:id/members` 批量添加用户

### 2.4 Admin — 用户管理增强

#### 用户列表 (`admin-users.html`)

在用户列表表格中增加 "Group" 列：

```
│  用户      │ 邮箱           │ 角色   │ 用户组    │ 余额    │ 操作  │
│  ───────────────────────────────────────────────────────────────  │
│  张三      │ z@example.com  │ user   │ general-1 │ ¥128.50 │ 编辑  │
│  李四      │ l@example.com  │ user   │ sub-39-t  │ ¥0.00   │ 编辑  │
│  王五      │ w@example.com  │ admin  │ —         │ ¥999.00 │ 编辑  │
```

#### 编辑用户弹窗

增加 Group 下拉选择器：

```
│  编辑用户: 张三                                          │
├─────────────────────────────────────────────────────────┤
│  名称:     [张三]                                       │
│  邮箱:     [z@example.com]       (不可修改)             │
│  角色:     [user ▼]                                     │
│  用户组:   [general-1 ▼]      ← 新增                    │
│  用户类型: [general ▼]                                  │
│  余额:     [128.50]                                     │
│                                                          │
│  [取消]                          [保存]                 │
└─────────────────────────────────────────────────────────┘
```

### 2.5 Subscription 套餐模板

#### 8 个预定义 Subscription Group

| Group 名称 | Plan | 档次 | 倍率 | 计费模式 | RPM | TPM | 月限额 |
|-----------|------|------|------|----------|-----|-----|--------|
| sub-token-39 | Token Plan | ¥39 | 1.2 | token | 60 | 1M | — |
| sub-token-99 | Token Plan | ¥99 | 1.2 | token | 120 | 3M | — |
| sub-token-399 | Token Plan | ¥399 | 1.2 | token | 300 | 10M | — |
| sub-token-799 | Token Plan | ¥799 | 1.2 | token | 600 | 30M | — |
| sub-code-39 | Code Plan | ¥39 | 1.2 | request | 60 | — | 18000/mo |
| sub-code-99 | Code Plan | ¥99 | 1.2 | request | 120 | — | 60000/mo |
| sub-code-399 | Code Plan | ¥399 | 1.2 | request | 300 | — | 180000/mo |
| sub-code-799 | Code Plan | ¥799 | 1.2 | request | 600 | — | 600000/mo |

**实现方式：** Admin 页面提供 "从模板创建" 按钮，选择 plan 类型和档次后自动填充所有参数。

---

## 3. 后端 API 变更

### 3.1 新增/修改的 API 端点

| 方法 | 端点 | 说明 | 状态 |
|------|------|------|------|
| `GET` | `/api/keys` | 已存在，Phase 1 已增强返回字段 | ✅ |
| `POST` | `/api/keys` | 已存在，Phase 1 已增强支持 key_type | ✅ |
| `PATCH` | `/api/keys/:id` | 已存在，Phase 1 已增强支持 current_model | ✅ |
| `GET` | `/api/keys/:id` | 已存在，Phase 1 已增强返回字段 | ✅ |
| `GET` | `/api/admin/groups` | 已存在，Phase 1 已修复查 user_groups | ✅ |
| `POST` | `/api/admin/groups` | 已存在，Phase 1 已增强字段 | ✅ |
| `PUT` | `/api/admin/groups/:id` | 已存在，Phase 1 已增强字段 | ✅ |
| `GET` | `/api/admin/groups/:id` | 已存在 | ✅ |
| `GET` | `/api/admin/users` | **新增/增强** — 返回 group 信息 | 🆕 |
| `PUT` | `/api/admin/users/:id` | **新增/增强** — 支持修改 group_id | 🆕 |
| `GET` | `/api/admin/models` | **新增** — 返回 service_models 列表（供模型选择器） | 🆕 |
| `POST` | `/api/admin/groups/from-template` | **新增** — 从 subscription 模板创建 group | 🆕 |

### 3.2 新增 API 详细定义

#### `GET /api/admin/users`

```json
{
  "users": [
    {
      "id": "uuid",
      "name": "张三",
      "email": "z@example.com",
      "role": "user",
      "group_id": "uuid",
      "group_name": "general-1",
      "user_type": "general",
      "balance": 12850,
      "created_at": "2026-05-01T10:00:00Z"
    }
  ],
  "total": 100,
  "page": 1,
  "pageSize": 20
}
```

#### `PUT /api/admin/users/:id`

Request:
```json
{
  "name": "张三",
  "role": "user",
  "group_id": "uuid",
  "user_type": "general",
  "balance": 12850
}
```

#### `GET /api/admin/models`

返回 `service_models` 表中 status='active' 的模型列表：
```json
{
  "models": [
    {
      "id": "uuid",
      "provider": "zhipu",
      "product_id": "glm5",
      "api_model_name": "glm5",
      "name": "GLM-5",
      "input_price": 0.001,
      "output_price": 0.002
    }
  ]
}
```

#### `POST /api/admin/groups/from-template`

Request:
```json
{
  "template": "subscription-token",  // or "subscription-code"
  "tier": 99,  // 39, 99, 399, 799
  "name": "sub-token-99"  // optional, auto-generated if empty
}
```

---

## 4. 前端页面清单

### 4.1 新增页面

| 页面文件 | 路由 | 说明 | 权限 |
|----------|------|------|------|
| `dashboard/keys.html` 或 Vue 组件 | `/dashboard/keys` | 我的 API Keys 管理 | 登录用户 |
| `dashboard/key-settings.html` | `/dashboard/keys/:id/settings` | Key 设置（模型选择器 + group 优先级） | Key 所有者 |
| `admin/groups.html` | `/admin/groups` | Group 列表 + CRUD | Admin |
| `admin/group-edit.html` | `/admin/groups/:id/edit` | Group 编辑表单 | Admin |
| `admin/users.html` | `/admin/users` | 用户列表（增强版） | Admin |

### 4.2 修改页面

| 页面文件 | 修改内容 |
|----------|----------|
| `admin.html` | 导航栏添加 "用户组" 入口 |
| `admin-users.html` | 增加 group 列、编辑时添加 group 选择器 |
| `apikeys.html` | 显示 group 信息、添加 group 筛选 |

### 4.3 可复用组件

| 组件 | 用途 |
|------|------|
| `ModelSelector.vue/.js` | 模型选择器（点击单选，数据源从 group.models） |
| `GroupPicker.vue/.js` | Group 选择器（下拉，按 group_type 过滤） |
| `KeyTypeBadge.vue/.js` | Key 类型标签（sk-wgit / sk-wgit-tp / sk-wgit-en） |
| `RateLimitInput.vue/.js` | 限流参数输入（RPM/TPM/RPH/TPH 等） |

---

## 5. 实施顺序

```
Phase 2 实施顺序
├── Step 1: 后端 API 补充
│   ├── GET /api/admin/users（增强：返回 group 信息）
│   ├── PUT /api/admin/users/:id（增强：支持修改 group_id）
│   ├── GET /api/admin/models（新增：返回可用模型列表）
│   └── POST /api/admin/groups/from-template（新增）
│   └── 预计: 2h
│
├── Step 2: Admin Group 管理页面
│   ├── admin-groups.html — Group 列表（表格 + 筛选）
│   ├── admin-group-edit.html — Group 创建/编辑表单
│   └── admin.html — 导航添加 "用户组" 入口
│   └── 预计: 3h
│
├── Step 3: Admin 用户管理增强
│   ├── admin-users.html — 增加 group 列
│   ├── admin-users.html — 编辑弹窗添加 group 选择器
│   └── 预计: 1.5h
│
├── Step 4: Dashboard "我的 API Keys"
│   ├── dashboard/keys.html — Key 列表页面
│   ├── Key 创建弹窗（选择 key_type + group）
│   ├── Key 设置弹窗（模型选择器 + group 优先级）
│   └── 预计: 4h
│
├── Step 5: 模型选择器组件
│   ├── ModelSelector 组件（可复用）
│   ├── 数据源: GET /api/admin/models 或 key group 的 models
│   ├── 选择后 PATCH /api/keys/:id
│   └── 预计: 2h
│
├── Step 6: Subscription 模板
│   ├── POST /api/admin/groups/from-template 后端
│   ├── Admin 页面 "从模板创建" 按钮
│   ├── 预定义 8 个模板配置
│   └── 预计: 2h
│
└── 总计预计: 14.5h（可拆分为 2-3 个迭代）
```

---

## 6. 数据流设计

### 6.1 Dashboard 创建 Key → 使用流程

```
用户登录 Dashboard
    ↓
点击 "我的 API Keys" → 查看已有 key 列表
    ↓
点击 "+ 新建"
    → 选择 key_type (general/economy/subscription/enterprise)
    → 选择 group（下拉，已按 key_type 过滤）
    → 输入名称
    → 点击确认
    ↓
[Portal] POST /api/keys
    → 生成对应前缀的 key
    → 从 group 继承 models/pricing_ratio/rate_limit
    → INSERT api_keys
    → 返回完整 key（仅展示一次）
    ↓
用户在 Dashboard 看到新 key
    → 点击 "设置" → 打开模型选择器
    → 点击选择模型（从 group.models 中）
    → 保存 → PATCH /api/keys/:id {current_model: "glm5"}
    ↓
[Portal] 更新 api_keys.current_model
    ↓
用户调用 API: Bearer sk-wgit-xxx, model: "wgit-maas-latest"
    ↓
[Proxy] wgit-maas-latest → current_model (glm5) → 路由到对应 provider
```

### 6.2 Admin 创建 Group 流程

```
Admin 登录 → 进入 "用户组管理"
    ↓
点击 "+ 新建用户组"
    → 填写名称、选择类型、设置倍率、选择模型
    → 或点击 "从模板创建" → 选择 subscription plan + 档次
    → 点击创建
    ↓
[Portal] POST /api/admin/groups
    → INSERT user_groups
    → 返回 group ID
    ↓
Admin 在列表中看到新 group
    → 可点击 "添加成员" 批量分配用户
    → 或用户注册时自动分配（Phase 1 已实现）
```

---

## 7. 关键决策点

| 决策 | 选项 A | 选项 B | 建议 |
|------|--------|--------|------|
| **前端技术栈** | 继续使用现有 HTML + vanilla JS | 引入 Vue/React 组件化 | **A** — 与现有 Portal 风格保持一致，降低复杂度 |
| **模型选择器数据源** | 从 `service_models` 全量获取 | 从 `user_groups.models` 获取 | **B** — 只显示 group 支持的模型，与 Phase 1 设计一致 |
| **group_priority 编辑** | 单 key 级别（每个 key 独立配置） | user 级别（所有 key 共享） | **A** — 与 Phase 1 "Group belongs to Key" 原则一致 |
| **subscription 模板** | 8 个固定模板（硬编码） | 可配置模板（数据库存储） | **A** — Phase 2 先硬编码，后续可扩展为可配置 |
| **admin-users group 迁移** | 即时生效（影响已有 key） | 只影响新 key | **B** — users.group_id 只作为默认值，已有 key 的 group 不变 |

---

## 8. 与 Phase 1 的衔接

| Phase 1 已完成 | Phase 2 在此基础上添加 |
|----------------|------------------------|
| `api_keys` 表已有 `group_id`/`key_type`/`current_model`/`billing_mode` | Dashboard 页面让用户可操作这些字段 |
| `user_groups` 表已有 `group_type`/`billing_mode`/`pricing_ratio`/`models` | Admin 页面让管理员可配置这些字段 |
| Proxy 已支持 `wgit-maas-latest` → `current_model` | Dashboard 模型选择器让用户设置 `current_model` |
| Proxy Claims 已扩展 group 字段 | Dashboard 展示这些字段，让用户理解当前配置 |
| 注册自动分配默认 general group | Admin 可将用户迁移到其他 group |

---

## 9. Open Questions

1. **Dashboard 框架**: 现有 Dashboard 是纯 HTML 还是已有 Vue/React？需要确认技术栈以决定实现方式。
2. **模型显示名称**: `service_models` 中的 `product_id`（如 `glm5`）vs 展示名（如 `GLM-5`），是否需要额外的显示名映射？
3. **Group 删除策略**: 有 key 关联的 group 是否可以删除？还是只能标记为 inactive？
4. **Subscription 购买流程**: Phase 2 中 subscription group 是 admin 创建后用户选择加入，还是用户直接购买（走支付流程）？
5. **Enterprise 邀请流程**: 企业 admin 发送邮件邀请的功能是否在 Phase 2 范围内？

---

## 10. 回滚策略

Phase 2 主要是**新增前端页面和 API**，不修改 Phase 1 的数据库 schema。回滚方式：

- 删除新增的前端页面文件
- 删除新增的后端路由处理函数
- 已有数据（`api_keys.current_model` 等）不受影响
