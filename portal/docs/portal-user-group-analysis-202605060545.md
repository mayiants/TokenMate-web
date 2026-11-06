# Portal User + Group 实现现状分析与建议方案

**Date:** 2026-05-06 05:45 UTC  
**Scope:** `ai-portal-go` — user registration, group management, API key creation, and group inheritance  
**Status:** DRAFT — pending discussion

---

## 1. 现状梳理

### 1.1 数据库 Schema

```
users
├── id, name, email, phone, password_hash, company, role
├── balance (bigint, default 0)
├── group_id (uuid) → FK to user_groups(id)   ← 外键已存在，但所有用户为 NULL
├── user_type (varchar, default 'general')     ← economy/general/single_model/subscription/enterprise
├── enterprise_config (jsonb)
└── managed_by (uuid) → self-referential FK for enterprise admin hierarchy

user_groups (新表，当前空)
├── id, name, description
├── pricing_ratio (numeric 5,4, default 1.0000)   ← 计费折扣倍率
├── model_permissions (jsonb)                     ← 可用模型白名单
├── rate_limit_rpm (int, default 60)
└── status (active/inactive)

groups (旧表，2条数据：默认组、VIP组)
├── id, name, description, quota_limit
└── 已被 user_groups 取代，但代码中 listGroups 仍在查此表

api_keys
├── id, user_id, key_hash, key_prefix, name, status
├── access_mode, allowed_models, rate_limit_rpm, rate_limit_tpm
├── total_requests, total_tokens, balance, model_binding
├── subscription_id, key_type, expires_at
└── ❌ 没有 group_id 字段
```

### 1.2 代码实现状态

| 模块 | 文件 | 状态 | 问题 |
|------|------|------|------|
| Group API | `internal/routes/groups.go` | ✅ 完整 CRUD + 成员管理 | `listGroups` 查旧表 `groups`，其余操作查新表 `user_groups` |
| Group 路由 | `cmd/server/main.go:220` | ✅ 已注册 `/api/admin/groups` | — |
| Group 页面 | `web/templates/admin-groups.html` | ❌ 不存在 | admin.html 导航中也没有 groups 入口 |
| User 注册 | `internal/routes/auth.go` | ✅ 有 register/login/getMe | 注册时不分配 group，不设置 user_type |
| API Key 创建 | `internal/routes/apikeys.go:93` | ✅ 基本功能 | 不读取 user.group 配置，默认 rate_limit_rpm=60 |
| Admin Users 页 | `web/templates/admin-users.html` | ✅ 存在 | 没有 group 列，编辑时没有 group 选择器 |

### 1.3 当前数据状态

```sql
-- users: 所有 group_id 为 NULL
SELECT group_id, COUNT(*) FROM users GROUP BY group_id;
-- => NULL: 5 rows

-- user_groups: 完全为空
SELECT COUNT(*) FROM user_groups;
-- => 0

-- groups: 2 条旧数据
SELECT name FROM groups;
-- => 默认组, VIP组

-- api_keys: 创建时无 group 关联
SELECT group_id FROM api_keys LIMIT 1;
-- => ERROR: column "group_id" does not exist
```

---

## 2. 核心问题

### 问题 1：双表并存（groups vs user_groups）

`groups` 是旧表，`user_groups` 是新表。`users.group_id` 外键指向 `user_groups`，但 `groups.go` 的 `listGroups` 查的是旧表。

**影响：**
- Admin 看到的 group 列表和 group 详情可能来自不同表
- `默认组/VIP组` 存在于旧表但不在新表，无法给用户分配

### 问题 2：用户注册不分配 group

`auth.go` 的 `register()` 只插入 `name, email, password_hash`，不设置 `group_id` 和 `user_type`。

**影响：**
- 所有用户都是 "散户"，无法按 group 统一管理
- `user_type` 字段形同虚设

### 问题 3：API Key 创建不继承 group 配置

`apikeys.go` 的 `createKey()` 默认 `rate_limit_rpm=60, rate_limit_tpm=100000`，不查用户的 group 配置。

**影响：**
- VIP 组用户和普通用户创建的 key 限流相同
- group 的 `pricing_ratio` 和 `model_permissions` 对 key 无影响

### 问题 4：api_keys 表缺少 group 关联

当前 `api_keys` 只有 `user_id`，没有 `group_id`。这意味着：
- 按 group 统计 usage 需要 JOIN users 表
- key 迁移 user 时，group 配置追溯困难

### 问题 5：缺少前端页面

- 没有 `admin-groups.html`
- `admin-users.html` 没有 group 列
- `apikeys.html` 没有 group 信息

---

## 3. 建议方案

### 方案 A：最小改动（推荐作为 Phase 1）

**目标：** 让 group 系统先跑起来，不破坏现有数据。

#### Step 1：统一使用 `user_groups` 表

```sql
-- 将旧表数据迁移到新表
INSERT INTO user_groups (name, description, pricing_ratio, rate_limit_rpm, status)
SELECT name, description, 1.0, 60, 'active' FROM groups;

-- 修改 groups.go：listGroups 查 user_groups
```

#### Step 2：注册时自动分配默认 group

```go
// auth.go register()
// 1. 查询默认 group ID
var defaultGroupID *string
_ = tx.QueryRow(ctx, `SELECT id::text FROM user_groups WHERE status = 'active' ORDER BY created_at LIMIT 1`).Scan(&defaultGroupID)

// 2. 插入用户时设置 group_id
INSERT INTO users (name, email, password_hash, group_id, user_type, ...)
VALUES ($1, $2, $3, $4, 'general', ...)
```

#### Step 3：API Key 创建时继承 group 配置

```go
// apikeys.go createKey()
// 1. 查用户的 group 配置
var groupPricingRatio float64
var groupRateLimitRPM, groupRateLimitTPM int
var groupModels interface{}
_ = r.db.QueryRow(ctx,
    `SELECT g.pricing_ratio, g.rate_limit_rpm, g.rate_limit_tpm, g.model_permissions
     FROM user_groups g JOIN users u ON u.group_id = g.id
     WHERE u.id = $1::uuid`, userID,
).Scan(&groupPricingRatio, &groupRateLimitRPM, &groupRateLimitTPM, &groupModels)

// 2. 用 group 配置作为默认值（用户可覆盖）
if req.RateLimitRPM == 0 && groupRateLimitRPM > 0 {
    req.RateLimitRPM = groupRateLimitRPM
}
if req.RateLimitTPM == 0 && groupRateLimitTPM > 0 {
    req.RateLimitTPM = groupRateLimitTPM
}
```

#### Step 4：创建基础前端页面

- `admin-groups.html`：表格 + 添加/编辑表单（字段：name, pricing_ratio, rate_limit_rpm, model_permissions, status）
- `admin-users.html`：列表加 group 列，编辑加 group 下拉选择
- `admin.html`：快捷入口加 "用户组"

**工作量：** 约 2-3 小时

---

### 方案 B：完整方案（Phase 2）

在方案 A 基础上增加：

#### Step 5：api_keys 表添加 group_id

```sql
ALTER TABLE api_keys ADD COLUMN group_id UUID REFERENCES user_groups(id);
CREATE INDEX idx_api_keys_group_id ON api_keys(group_id);
```

**理由：**
- 按 group 统计 usage 时不需要 JOIN users 表
- key 可以独立设置 group（不强制跟随 user）
- 支持 "key 级别 override group 配置"

#### Step 6：group 配置 override 机制

```
Key 实际配置 = Key 自身配置（如果有）
            ?? User.group 配置（继承）
            ?? 系统默认
```

实现：
- `api_keys` 新增 `pricing_ratio_override`, `model_permissions_override` 字段
- 为空则继承 group，非空则覆盖

#### Step 7：user_type 与 group 联动

```
user_type 决定可加入的 group 类型：
- 'general'     → 普通组（pricing_ratio = 1.0）
- 'economy'     → 经济组（pricing_ratio < 1.0，限速更低）
- 'subscription'→ 订阅组（有 quota_limit）
- 'enterprise'  → 企业组（managed_by 企业管理员）
```

**工作量：** 约 4-6 小时（含迁移）

---

## 4. 数据流设计

### 注册 → 创建 Key 的完整链路（方案 A）

```
User 注册
    ↓
[Portal] auth.register()
    → 自动分配默认 user_group
    → user.group_id = default_group_id
    → user.user_type = 'general'
    ↓
User 创建 API Key
    ↓
[Portal] apikeys.createKey()
    → 读取 user.group（pricing_ratio, rate_limit_rpm, model_permissions）
    → 用 group 默认值填充 key 配置
    → INSERT api_keys (user_id, rate_limit_rpm, rate_limit_tpm, allowed_models, ...)
    ↓
Proxy 使用 Key
    ↓
[Proxy] auth 验证 key → 返回 Claims（含 rate_limit_rpm, rate_limit_tpm）
    ↓
[Proxy] 请求完成 → audit.Log() → Portal /api/webhook/usage-batch
    ↓
[Portal] webhook.processRecord()
    → 扣减 user subscription / balance
    → 应用 group.pricing_ratio 计算实际费用
    → INSERT usage_records
```

---

## 5. 关键决策点

| 决策 | 选项 A | 选项 B | 建议 |
|------|--------|--------|------|
| **旧 groups 表** | 保留，双表并存 | 迁移后删除 | **B** — 统一用 user_groups |
| **api_keys.group_id** | 不添加，通过 user JOIN | 添加，支持 key 级 override | **先 A 后 B** — Phase 1 不加，Phase 2 加 |
| **注册默认 group** | 所有用户进 "默认组" | 根据 user_type 分配不同组 | **A** — 先统一默认组，后续细分 |
| **group 配置继承** | key 只继承一次（静态快照） | key 实时查 group（动态） | **A** — 静态快照，group 变更不影响历史 key |
| **pricing_ratio 应用时机** | 创建 usage_records 时计算 | 展示时实时计算 | **A** — 写入时计算，避免追溯混乱 |

---

## 6. Open Questions

1. **user_groups.model_permissions 格式**：是 `{"models": ["gpt-4o", "claude-sonnet"]}` 还是 `{ "gpt-4o": true }`？需要与 Proxy validate endpoint 对齐。
2. **pricing_ratio 范围**：>1.0 表示加价，<1.0 表示折扣。是否允许 >1.0？
3. **group 删除策略**：有成员的 group 不能删除（当前已实现）。成员如何迁移？
4. **enterprise 用户的 group**：`enterprise_config` 和 `user_groups` 是否冲突？`managed_by` 层级和 group 层级哪个优先？

---

## 7. 实施优先级

```
P0（阻塞集成测试）
├── 统一 user_groups 表（修复 listGroups）
├── 注册自动分配默认 group
├── API Key 继承 group 配置
└── admin-groups.html 基础页面

P1（提升体验）
├── api_keys 添加 group_id
├── admin-users.html 加 group 列/选择器
├── apikeys.html 显示 group 信息
└── group 配置 override 机制

P2（高级功能）
├── user_type 与 group 类型联动
├── group 间用户批量迁移
└── group 级 usage 统计报表
```


<A> 我陈述对用户 + 用户组 + 模型 + 套餐的定义和使用：

1）User：通用，注册时使用邮箱验证，user本身没有高低等级之分。

2）Group: 通用，由admin创建及管理维护，分三种：
economy：计费 *0.85，即85折；使用general(sk-wgit-xxxx) 的用户key
general：计费 *1，即标准计费；使用general(sk-wgit-xxxx) 的用户key
subscription：套餐产品，分token plan（按token计费） 和 code plan（按request计费） 两种，每种分39、99、399、799四个档次，一共8个套餐，实际是8个group；使用subscription(sk-wgit-tp-xxxx)的用户key
enterpise: 企业客户，由企业admin管理，需要由企业admin发送邮件邀请并验证登录看到enterprise账户；企业admin创建维护token plan和code plan两种，分配给企业用户使用；使用enterprise(sk-wgit-en-xxxx)的用户key

user登录后，可以选择创建3类不同的api-key（如果不是企业用户，只能创建前2类）：
portal是依赖user key来对用户进行优先级进行划分，而不是对user本身！！！所以需要在user key的属性中加入group信息及相关的服务属性和能力。

注意：一个user可以同时创建多个key，例如新建一个 sk-wgit 的key用户一般性产品使用；两个 sk-wgit-tp 使用相关的套餐；一个 sk-wgit-en 用户企业业务。

至于group的业务范围，例如：
Group-1：economoy-1 命名，包括 qwen3.6，kimi k2.5，minimax m2.5，Zhipu glm5，deepseek v3.2 5个产品，消费倍率0.85，按token计费，tpd=20M，预付费（从钱包扣除）
Group-2：genernal-1 命名，包括 qwen3.6，kimi k2.5，minimax m2.5，Zhipu glm5，deepseek v3.2 5个产品，消费倍率1，按token计费，tpd=30M，预付费（从钱包扣除）
Group-3：subscription-1 命名，包括 kimi k2.6，minimax m2.7，Zhipu glm5.1，deepseek v4pro 4个产品，消费倍率1.2，按request计费，rpm/rph/rp5h/rpw/rpmonth=60/600/1200/9000/18000，预付费
Group-4：subscription-2 命名，包括 kimi k2.6，minimax m2.7，Zhipu glm5.1，deepseek v4pro 4个产品，消费倍率0.9，按token计费，tpm/tph/tpd/tpw/tpmonth=1M/10M/30M/80M/1600M，预付费

创建时user key时允许使用一个通用的modelnme，例如 wgit-maas-latest, 这样可以覆盖group中的所有模型，使用时“baseurl + sk-wgit-xxx + wgit-maas-latest”。

该user可以在dashbaord页面选择sk-wgit 的优先级：group-1，group-2；当前选择的model是glm5；Proxy优先在group-1的支持的模型中为他路由；
如果他对效果不满足需要调整，登录dashboard调整配置：group优先级：group-2，group-1；当前选择的model是kimi k2.6; 那proxy在2~3分钟同步portal信息后将路由调整为group-2优先，且模型产品指向kimi k2.6

3）订阅客户，subscription，本质是一种特殊的group，只是优先级更高，使用了单独的 sk-wgit-tp 方便路由；
4）企业客户，enterprise，同理，是优先级最高的情况，使用 sk-wgit-en 进行路由；







---

## 8. 用户澄清后的补充（2026-05-06 14:00 UTC）

### 8.1 设计确认

| 确认项 | 结论 |
|--------|------|
| `users.group_id` | **保留**，作为默认 group（general 类别），也是 fallback group |
| `user_subscriptions` | **仅用于展示**，套餐购买信息直观查看；实际服务属性（模型、倍率、限流）由 `user_groups` 决定 |
| subscription 本质 | subscription 是 group 的一种 `group_type`，不是独立于 group 的概念 |
| 企业客户 enterprise | 同理，enterprise 也是 `group_type` 的一种 |

### 8.2 两种 API 使用模式

#### 模式 A：`wgit-maas-latest` 通用名（推荐）

```
用户请求：POST /v1/chat/completions
Authorization: Bearer sk-wgit-xxx
Body: { "model": "wgit-maas-latest", "messages": [...] }
```

**Proxy 处理流程：**
1. Auth：查一次表，获取 key 的 `group_priority` + `allowed_models`
2. 路由：在优先级 group 的 `allowed_models` 中 **round-robin 或 balance** 选择实际模型
3. 无需关心用户 dashboard 配置的 `current_model`

**优势：**
- Proxy auth 只查一次表（O(1)）
- 模型能力趋同后，可在 group 内自动负载均衡
- 降低 Proxy 压力，降低用户消费
- 用户不需要关心具体用哪个 provider 的哪个模型

#### 模式 B：真实模型名（明确指定）

```
用户请求：POST /v1/chat/completions
Authorization: Bearer sk-wgit-xxx
Body: { "model": "glm5", "messages": [...] }
```

**Proxy 处理流程：**
1. Auth：查一次表，获取 key 的 `group_priority` + `current_model` + `allowed_models`
2. 验证：请求中的 `model` 必须 == `current_model`，否则 400 拒绝
3. 路由：在优先级 group 的 `allowed_models` 中查找 `glm5`，依次 fallback

**注意：**
- 由于 Dashboard 是**点击选择模型**（不是输入），Portal 已做合法性检查
- 理论上不会出现 "模型不在 group 中" 的情况（除非用户绕过 Dashboard 直接调 API）
- 这种设计是为了确保用户明确知道自己用的是什么模型

### 8.3 Portal 合法性检查（Dashboard 端）

```
Dashboard 模型选择器：
┌─────────────────────────────────────┐
│ 选择当前模型：                       │
│  ○ glm5        (Group-2 支持)        │
│  ● kimi k2.6   (Group-2 支持)  ← 已选│
│  ○ deepseek v3.2 (Group-2 支持)      │
│                                     │
│ [Group-1 不支持 kimi k2.6，不显示]   │
└─────────────────────────────────────┘
```

- 只显示当前 key 所属 group 的 `allowed_models` 交集
- 用户只能选 group 支持的模型
- 选择后更新 `api_keys.current_model`

### 8.4 Proxy 路由逻辑伪代码

```go
func handleChatCompletions(c *gin.Context) {
    reqModel := req.Model
    claims := auth.Validate(c) // 获取 key 的 group 配置

    if reqModel == "wgit-maas-latest" {
        // 模式 A：通用名，在 group 内 round-robin
        upstreamModel := selectModelRoundRobin(claims.GroupPriority, claims.AllowedModels)
        routeToProvider(upstreamModel)
    } else {
        // 模式 B：真实模型名，验证一致性
        if reqModel != claims.CurrentModel {
            c.JSON(400, gin.H{"error": "Model mismatch. Dashboard config: " + claims.CurrentModel})
            return
        }
        // 在优先级 group 中查找模型
        upstreamModel := findModelInPriorityGroups(reqModel, claims.GroupPriority)
        if upstreamModel == "" {
            c.JSON(400, gin.H{"error": "Model not available in current groups"})
            return
        }
        routeToProvider(upstreamModel)
    }
}
```

### 8.5 Key 前缀路由规则

| Key 前缀 | group_type | 优先级 | 说明 |
|----------|-----------|--------|------|
| `sk-wgit-` | economy / general | 低 | 普通用户，预付费 |
| `sk-wgit-tp-` | subscription | 中 | 套餐用户，预付费 |
| `sk-wgit-en-` | enterprise | 高 | 企业用户，企业管理员管理 |

**Proxy 路由优先级：**
1. 先看 key 前缀确定 `group_type`
2. 在相同 `group_type` 的 upstream keys 中按 group_priority 查找
3. 如果没有匹配的，fallback 到 `users.group_id`（默认 general group）

### 8.6 修订后的数据模型（最终版）

#### `api_keys` 表

```sql
ALTER TABLE api_keys ADD COLUMN group_id UUID REFERENCES user_groups(id);
ALTER TABLE api_keys ADD COLUMN group_priority JSONB DEFAULT '[]';   -- [group_id_1, group_id_2, ...]
ALTER TABLE api_keys ADD COLUMN current_model VARCHAR(100);          -- dashboard 选择的模型
ALTER TABLE api_keys ADD COLUMN billing_mode VARCHAR(20) DEFAULT 'token'; -- token | request
ALTER TABLE api_keys ADD COLUMN key_type VARCHAR(20) DEFAULT 'general';   -- economy | general | subscription | enterprise
-- key_prefix 根据 key_type 生成：sk-wgit- / sk-wgit-tp- / sk-wgit-en-
```

#### `user_groups` 表

```sql
ALTER TABLE user_groups ADD COLUMN group_type VARCHAR(20) NOT NULL DEFAULT 'general'; -- economy | general | subscription | enterprise
ALTER TABLE user_groups ADD COLUMN billing_mode VARCHAR(20) DEFAULT 'token';          -- token | request
ALTER TABLE user_groups ADD COLUMN pricing_ratio NUMERIC(5,4) DEFAULT 1.0;            -- 0.85, 1.0, 1.2, 0.9 等
ALTER TABLE user_groups ADD COLUMN models JSONB DEFAULT '[]';                         -- ["qwen3.6", "kimi-k2.5", ...]
-- 限流参数（subscription 用）
ALTER TABLE user_groups ADD COLUMN rpm INT DEFAULT 0;
ALTER TABLE user_groups ADD COLUMN rph INT DEFAULT 0;
ALTER TABLE user_groups ADD COLUMN rp5h INT DEFAULT 0;
ALTER TABLE user_groups ADD COLUMN rpw INT DEFAULT 0;
ALTER TABLE user_groups ADD COLUMN rpmonth INT DEFAULT 0;
ALTER TABLE user_groups ADD COLUMN tpm INT DEFAULT 0;
ALTER TABLE user_groups ADD COLUMN tph INT DEFAULT 0;
ALTER TABLE user_groups ADD COLUMN tpd INT DEFAULT 0;
ALTER TABLE user_groups ADD COLUMN tpw INT DEFAULT 0;
ALTER TABLE user_groups ADD COLUMN tpmonth INT DEFAULT 0;
```

#### `users` 表（不变，仅保留 group_id 作为默认值）

```sql
-- users.group_id 保留，作为用户默认 group
-- 创建 key 时，如果没指定 group，使用 users.group_id
```

### 8.7 实施顺序（修订版）

```
Phase 1：数据模型调整（Portal + Proxy 并行）
├── Portal：扩展 api_keys / user_groups 表
├── Portal：修改 apikeys.go 创建 key（支持选择 group_type，生成对应前缀）
├── Portal：修改 auth.go 注册（分配默认 general group）
├── Proxy：扩展 Claims（group_id, group_priority, current_model, billing_mode, pricing_ratio）
└── Proxy：扩展 validate 响应（返回 group 配置）

Phase 2：Dashboard 配置页面
├── Portal：Dashboard 添加 "我的 API Keys" 管理（显示每个 key 的 group、current_model）
├── Portal：Dashboard 添加模型选择器（点击选择，合法性检查）
├── Portal：Dashboard 添加 group 优先级配置
├── Portal：Admin 创建 group 页面（配置模型列表、倍率、限流）
└── Portal：Admin group 列表页面

Phase 3：Proxy 路由适配
├── Proxy：key 前缀识别（sk-wgit / sk-wgit-tp / sk-wgit-en）
├── Proxy：wgit-maas-latest 通用名 → round-robin / balance
├── Proxy：真实模型名 → 验证 current_model 一致性
├── Proxy：group_priority fallback 路由
└── Proxy：按 request 计费（subscription code plan）
```

### 8.8 用户补充：Phase 1 的 wgit-maas-latest 行为（2026-05-06 14:15 UTC）

**当前阶段（Phase 1）**：
- `wgit-maas-latest` **不是** round-robin / balance
- `wgit-maas-latest` = `current_model` 的别名
- Proxy 收到 `wgit-maas-latest` 时，直接替换为 `claims.CurrentModel`

**下一阶段（Phase 2+）**：
- 才会在 group 的 `allowed_models` 中做 round-robin / balance
- 那时 `wgit-maas-latest` 才是真正的"通用名"

**Phase 1 的 Proxy 路由逻辑（简化版）**：

```go
func resolveModel(reqModel string, claims *Claims) (string, error) {
    if reqModel == "wgit-maas-latest" {
        // 当前阶段：通用名 = current_model 别名
        return claims.CurrentModel, nil
    }
    // 真实模型名：验证与 dashboard 配置一致
    if reqModel != claims.CurrentModel {
        return "", fmt.Errorf("model mismatch: request=%s, config=%s", reqModel, claims.CurrentModel)
    }
    return reqModel, nil
}
```

**结论**：Phase 1 中两种模式行为一致，只是入口参数不同。
