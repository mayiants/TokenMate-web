# User / User Group / API Key / Upstream Key 属性规范重构方案

> 版本：2026-06-15  
> 涉及仓库：`ai-portal-go`（Portal）、`ai-proxy-go`（Proxy）  
> 目标：消除 `users.user_type` 与 `api_keys.access_mode` 的语义混乱，让“用户组类型”成为统一的能力/路由/计费分层依据。

---

## 1. 现状与问题

### 1.1 `users.user_type` 过度干预 Group 分配

- `users.user_type` 当前有 `general / economy / subscription / enterprise` 四个值，默认值 `general`。
- 它的唯一实际作用是：在 `admin/users` 编辑页限制该用户可选的 `user_groups.group_type`。
- 问题：
  - 用户量大时，admin 无法逐个维护用户类型。
  - 一个用户本应可以临时加入 `economy` 组做降级，也可以加入 `subscription` 组使用套餐，但被 `user_type` 锁死。
  - 与 Proxy 实际路由毫无关系，造成误解。

### 1.2 `api_keys.access_mode` 默认全为 `single_model`

- 当前创建 key 时前端不再传 `access_mode`，数据库默认值是 `single_model`。
- Proxy 里 `single_model` 被映射为 `high` 优先级。
- 结果：普通个人 key 全部走到 `high` 优先级， economy 路由分支几乎不可用。

### 1.3 `model_routing_mode` 与 key tier 被混为一谈

- `model_routing_mode`（single / round_robin / priority）决定的是“多模型时如何选模型”。
- 但在部分讨论中它曾被误用来决定 key 的经济型/高级型，这是两个正交概念。

### 1.4 Upstream Key 的 tier 与 priority

- `upstream_keys.tier` 是 Portal 数据库里唯一的分类字段，当前 UI 已支持四类：`default / economy / premium / enterprise`。
- Portal 在 `/api/sync` 时根据 `tier` 计算 `priority` 推给 Proxy：
  - `default` → `normal`
  - `economy` → `economy`
  - `premium / enterprise` → `high`
- Proxy 内部同时存在 `Tier` 和 `Priority`，实际选 key 时以 `Priority` 为主。

---

## 2. 重构目标

1. **取消 `users.user_type` 对 Group 选择的限制**：用户加入什么 Group 由 admin 直接分配，Group 本身决定能力。
2. **统一 Group Type 为四类**：`general / economy / subscription / enterprise`。
3. **API Key 的 `access_mode` 由所属 Group Type 决定**：不再由 model routing mode 决定。
4. **Upstream Key Tier 与 Group Type 对齐**：`default / economy / premium / enterprise` 分别服务四类 Group 的请求。
5. **`routing_track` 保持独立**：只决定请求先走 `standard / bedrock / mantle` 哪条轨道，不影响 key 优先级。

---

## 3. 规范后的属性定义

### 3.1 User

| 字段 | 取值 | 说明 |
|---|---|---|
| `users.role` | `admin / enterprise_admin / user` | 平台权限 |
| `users.enterprise_id` | UUID / NULL | 企业归属 |
| `users.group_id` | UUID / NULL | 默认个人 Group |
| `users.routing_track` | `standard / bedrock / mantle` | 个人级路由轨道偏好，覆盖 Group 级 |
| `users.user_type` | **废弃使用** | 保留列，不再参与业务逻辑 |

### 3.2 User Group

| 字段 | 取值 | 说明 |
|---|---|---|
| `user_groups.group_type` | `general / economy / subscription / enterprise` | 决定组内 key 的 access_mode 与可用 upstream tier |
| `user_groups.routing_track` | `standard / bedrock / mantle` | 组级默认路由轨道 |
| `user_groups.pricing_ratio` | float | 计费倍率 |
| `user_groups.models` | []string | 可用模型白名单 |

### 3.3 API Key

| 字段 | 取值 | 说明 |
|---|---|---|
| `api_keys.key_type` | `general / subscription / enterprise` | 账单/所有权类型，保留 |
| `api_keys.access_mode` | `general / economy / subscription / enterprise` | **由所属 Group Type 决定**，用于 Proxy 路由优先级 |
| `api_keys.model_routing_mode` | `single / round_robin / priority` | 仅控制多模型选择策略 |
| `api_keys.group_id` | UUID | 所属 User Group |

### 3.4 Upstream Key

| 字段 | 取值 | 说明 |
|---|---|---|
| `upstream_keys.tier` | `default / economy / premium / enterprise` | 上游 key 类型，同步时映射为 Proxy priority |
| `upstream_keys.priority` | （计算字段，不存库） | `default→normal`, `economy→economy`, `premium/enterprise→high` |

---

## 4. 映射关系

### 4.1 Group Type → API Key access_mode → Proxy userPriority

| User Group Type | API Key `access_mode` | Proxy `userPriority` | 可使用的 Upstream Tier |
|---|---|---|---|
| `general` | `general` | `normal` | `default` |
| `economy` | `economy` | `economy` | `economy` |
| `subscription` | `subscription` | `high` | `premium` |
| `enterprise` | `enterprise` | `high` | `enterprise` / `premium` |

### 4.2 Upstream Tier → Proxy Priority

| Upstream Tier | Proxy Priority | 说明 |
|---|---|---|
| `default` | `normal` | 通用默认 key |
| `economy` | `economy` | 限速/低价 key |
| `premium` | `high` | 高质量/高配额 key |
| `enterprise` | `high` | 企业级 key，可与 premium 同池，也可单独隔离 |

---

## 5. Proxy 选 key 逻辑

### 5.1 路由优先级

Proxy 根据请求 key 的 `access_mode` 得到 `userPriority`：

```go
func getUserPriority(accessMode string) string {
	switch accessMode {
	case "subscription", "enterprise":
		return "high"
	case "economy":
		return "economy"
	default:
		return "normal"
	}
}
```

然后在对应 provider 的 key pool 中按 `Priority` 过滤：

- `high`：优先 `high`，回退 `normal`
- `normal`：只选 `normal` / 无 priority
- `economy`：优先 `economy`，回退 `normal`

### 5.2 路由轨道（Routing Track）

`routing_track` 决定请求先尝试哪条模型供应轨道：

- `standard`：标准 Provider keypool 路由
- `bedrock`：AWS Bedrock 轨道
- `mantle`：Mantle 轨道

如果目标轨道不可用，再 fall back 到 `standard`。

**关键点**：`routing_track` 与 `userPriority` 是**正交**的。

- `routing_track` 选“哪条路”。
- `userPriority` 选“这条路上哪个等级的 key”。

例如：
- 一个 `enterprise` group 的用户可以设置 `routing_track = bedrock`，Proxy 会先在 Bedrock 轨道里找 `high` priority 的 key，没有则回退到 standard 轨道里找 `high` priority 的 key。
- 一个 `economy` group 的用户即使 `routing_track = bedrock`，也只会用 `economy` priority 的 key。

---

## 6. Admin `/users` 页面 User Group 字段语义

### 6.1 创建用户

`/admin/users` 的“创建用户”表单目前**没有 User Group 字段**，后端 `createUser` 也不会写入 `group_id`。因此新建用户的 `users.group_id = NULL`，即**没有默认用户组**。

### 6.2 编辑用户

编辑用户时，User Group 下拉框第一项为 **“无”**：

- 选“无”时，前端**不会提交 `group_id` 字段**。
- 后端 `updateUser` 仅在 `req.GroupID != nil` 时才更新 `users.group_id`。

因此“无”的实际含义是：**不修改当前用户的默认组，保持原值**，而不是把 group 清空。

### 6.3 默认组 ≠ 可见/可用范围

把用户的默认组设为 `subscription` group，**不会**限制他只能看到或使用 subscription group：

- `/api/groups` 返回的是当前角色能看到的**所有平台 group**（普通用户可见 general / economy / subscription）。
- 创建 API key 时，group 下拉框会列出 key type 允许的所有兼容 group（general key 仍可手动选择 general/economy group）。
- 默认组仅在**创建 key 未指定 group** 时作为 fallback 使用。

> 例如：默认组为 subscription 的用户，直接创建 general key 会失败（`general keys can only use general or economy groups, got subscription`），因为他未手动选择 group，fallback 到了 subscription group。只要显式选一个 general/economy group，就能正常创建。

### 6.4 默认组与订阅、企业的关系

- **默认组是 subscription group ≠ 已订阅**：创建 `subscription` key 仍会检查 `user_subscriptions` 是否有 `status='active'` 的记录。未订阅时返回 `您尚未订阅套餐，请订阅后再创建套餐Key使用`。
- **默认组与企业归属无关**：用户能否加入企业取决于 `users.enterprise_id` / `enterprise_members`，与 `group_id` 无关。默认组是 subscription 的用户仍可被邀请/分配进 enterprise。

### 6.5 建议

- 普通个人用户建议默认组设为 **general**，避免创建 general key 时因 fallback 到 subscription/enterprise group 而报错。
- 仅当用户明确以 subscription 套餐为主时，才将默认组设为 subscription group。

---

## 11. 本轮修复记录

### 11.1 Group Type 下拉文案对齐

- `web/i18n/en.json` / `web/i18n/zh.json` 中 group/key type 相关翻译统一为：
  - `general` → `General` / `通用`
  - `economy` → `Economy` / `经济`
  - `subscription` → `Subscription` / `订阅`
  - `enterprise` → `Enterprise` / `企业`
- `web/templates/admin-users.html` 中用户编辑页 Group 下拉分组标签改为走翻译键，避免写死旧文案。

### 11.2 系统设置默认值兜底

- `internal/db/seed.go` 的 `SeedDefaultData` 新增默认 `system_settings` 初始化。
- 覆盖 `quota_for_new_user`、`checkin_enabled`、`routing_strategy`、`routing_max_retries` 等全部配置项，使用 `ON CONFLICT (key) DO NOTHING` 保证幂等。
- 解决数据库清空后 `/admin/settings` 读取接口 404 的问题。

### 11.3 Admin 创建用户 Email 规范化

- `internal/routes/admin_users.go` 的 `createUser` 在入库前执行 `strings.ToLower(strings.TrimSpace(req.Email))`。
- 修复 admin 输入大写邮箱后，用户按小写邮箱无法登录的问题。

### 11.4 Enterprise Admin 无 Enterprise 时的访问控制

- `internal/routes/enterprise_common.go` 新增两个中间件：
  - `requireEnterpriseAccess()`：允许企业成员 **或** `enterprise_admin`/`system_admin`/`super_admin` 查看企业页面。
  - `requireEnterpriseContext()`：在执行创建/修改/删除等变更操作前检查 `enterprise_id` 是否存在。
- 路由调整：
  - GET 类页面（`/enterprise`、`/enterprise/usage`、`/enterprise/members`、`/enterprise/keys`、`/enterprise/groups` 等）对企业管理员开放，数据为空时返回空列表/空对象。
  - POST/PUT/DELETE 类操作（添加成员、创建 key、创建企业 group、审批邀请、修改企业资料等）增加 `requireEnterpriseContext()`，无 enterprise 时返回 `400 Enterprise does not exist`。
  - `/enterprise/quit` 仍要求已加入企业。
- 新增 i18n 文案：
  - `error.not_enterprise_member`
  - `error.enterprise_not_found`

### 11.5 Enterprise 成员邀请支持预置姓名

- `internal/db/schema.go`：`enterprise_invitations` 表新增 `name VARCHAR(255)` 列。
- `internal/routes/enterprise_members.go`：添加企业成员接口接收可选 `name` 字段。
  - 若邮箱对应的用户不存在：姓名写入邀请函，用户通过邀请链接注册时自动回填到表单。
  - 若邮箱对应的用户已存在：后端立即更新 `users.name`（仅在传入非空姓名时）。
- `web/templates/enterprise/users.html`：添加成员表单新增“姓名”输入框，JS 仅在填写时随请求发送。
- `web/templates/enterprise-accept.html`：邀请接受页预填管理员设置的姓名，用户仍可在提交前修改。

### 11.6 回滚 Enterprise Group 平台分组模型预览

- `internal/routes/enterprise_groups.go` 的 `getPlatformGroups` 移除 `models` 数组返回，仅保留 `model_count`。
- `web/templates/enterprise/groups.html` 还原以下改动：
  - 平台分组下拉选项不再显示模型 sample 与 tooltip，仅显示 `model_count` 与 RPM。
  - 创建抽屉不再渲染平台分组详情面板。
  - 选择平台分组后不再默认全选模型，需要管理员手动勾选。
- 该功能回滚是因为在当前数据量下模型列表过长，下拉/详情面板体验不佳；后续如需要可在弹窗或独立页面中展示完整模型列表。

### 11.7 Enterprise 成员管理增强（Pending 邀请、姓名编辑、SMTP 邮件）

#### Pending 邀请可视化与配额管控

- `internal/query/enterprise.go` 新增 `EnterpriseInvitation` 与 `GetEnterpriseInvitationsData`。
- `internal/web/data.go` / `internal/web/pages.go`：`handleEnterpriseUsers` 同时查询已确认成员和待处理邀请，渲染到 `enterprise/users`。
- `web/templates/enterprise/users.html`：
  - 顶部统计卡新增 **Pending** 数量。
  - 成员表格下方新增 **待处理邀请** 列表，展示邮箱、姓名、角色、状态、过期时间。
  - 支持复制邀请链接（SMTP 未配置时可手动发送）和取消邀请。
- `max_members` 配额统一口径：
  - `addEnterpriseMember` 与 `inviteMember` 均按 `effectiveMembers = activeMembers + pendingInvitations` 计算。
  - pending 邀请只统计那些还没有对应 active member 的记录，避免重复计算。
  - 接近上限时返回 `(X active + Y pending = Z/N)` 明确提示。

#### Pending 邀请按 email 去重与脏数据清理

- 创建邀请时检查该邮箱是否已有 `pending/email_confirmed` 邀请，有则返回 409，避免重复堆积。
- 用户通过邀请链接加入企业、或管理员审批通过后，按 **email** 把该企业下所有同邮箱的 `pending/email_confirmed` 邀请全部置为已处理状态，防止“一个 email 同时是 member 又是 pending”。
- `GetEnterpriseInvitationsData` 查询时排除那些 email 已存在 active member 的邀请，即使数据库有旧脏数据也不会显示在页面。

#### 成员姓名可编辑

- `internal/routes/enterprise_members.go` 新增 `updateMemberName`。
- `internal/routes/enterprise_common.go` 注册 `PUT /api/enterprise/members/:userId/name`。
- `web/templates/enterprise/users.html` 编辑抽屉增加姓名输入框，保存时并行提交 name / role / quota_limit。

#### SMTP 邀请邮件

- `addEnterpriseMember`（enterprise admin 自服务添加成员）在用户不存在时原本只写 `enterprise_invitations` 表，**不发送邮件**。
- 修复后：创建邀请记录后，若 SMTP 已配置，异步调用 `SendEnterpriseInvitation` 发送邀请邮件；同时返回 `accept_url` 供前端展示/复制。
- 添加已存在用户时也会异步发送 `SendEnterpriseAddedNotification` 通知邮件。

### 11.8 API Key 类型与 Group 选择约束

移除 `users.user_type` 后，个人密钥创建页 `/apikeys` 的 key type / group 选择出现两处偏差，已修复：

#### Key Type 可见性

- `CanCreateSubscriptionKey`：只要系统里存在 `subscription` group 即对所有用户显示。普通用户即使暂无 active subscription，也能看到 subscription key 类型；点击创建时若未订阅，后端返回 `error.subscribe_first_for_package_key` 提示。避免用户因看不到 subscription 类型而误用 general key 访问套餐模型。
- `CanCreateEnterpriseKey`：仅当用户是 admin、或 `users.enterprise_id` 不为空时才显示。未加入企业的用户不应看到 enterprise key 类型。

#### Group 下拉按 Key Type 过滤

`web/templates/apikeys.html` 的 `populateGroupSelects` 调整兼容类型：

| Key Type | 可选 Group Type |
|---|---|
| general | general, economy |
| subscription | subscription |
| enterprise | enterprise |

#### 后端创建校验

`internal/routes/apikeys_crud.go` 的 `createKey` 在拿到 primary group 的 `group_type` 后增加校验：

- `general` key → group_type 必须是 `general` 或 `economy`
- `subscription` key → group_type 必须是 `subscription`，且用户必须有 active subscription
- `enterprise` key → group_type 必须是 `enterprise`，且用户必须有 `enterprise_id`

---

## 12. Enterprise Admin 但无 Enterprise 时的行为建议

### 12.1 现状

- 数据库里 `users.role = 'enterprise_admin'` 是真实角色，权限层级高于 `user`、低于 `system_admin`/`super_admin`。
- 如果该用户没有 `users.enterprise_id`：
  - 可以登录、使用普通用户功能。
  - 可以看到 Enterprise 菜单和入口。
  - 进入企业页面后，列表类数据为空。
  - 尝试创建成员/key/group 等变更操作时会被后端拦截，提示“企业不存在”。

### 12.2 为什么这样设计

- **可发现性**：用户不会因为“还没创建企业”而找不到 Enterprise 控制台入口。
- **安全性**：所有会写入数据的动作都强制要求 `enterprise_id` 上下文，防止在无企业的情况下产生游离数据。
- **一致性**：`RequireAdmin` 仍只认 `system_admin`/`super_admin`，`enterprise_admin` 不能进 `/admin/*`。

### 12.3 推荐用法

| 场景 | 推荐操作 |
|---|---|
| 只是普通用户 | role 设为 `user` |
| 要当企业管理员 | 先创建 enterprise，再将该用户设为 `enterprise_admin` 并写入 `users.enterprise_id` |
| 已有 `enterprise_admin` 但暂无企业 | 可保留角色，进入企业页面查看空状态；创建数据时会收到明确提示 |

### 12.4 “You are not a member of any enterprise” 来源

该提示来自 `internal/routes/enterprise_common.go` 原 `requireEnterpriseMember()` 中间件，仅用于普通企业成员路由；调整后企业管理员无企业时不会再看到此 403，而是看到空页面；普通用户（非 admin）访问企业页面时仍会看到该提示。


## 6. 对现有功能的影响

### 6.1 用户管理

- `admin/users` 编辑页**移除 User Type 字段**。
- 任何用户都可以被分配到任意 Group，不再受 `user_type` 限制。
- 历史用户的 `user_type` 列保留但不再读取。

### 6.2 Group 管理

- Group 创建/编辑时 `group_type` 固定为四类之一。
- 需要检查历史 group_type（如 `vip` 等旧值），统一迁移或映射到四类。

### 6.3 API Key 创建

- 创建 key 时，前端不传 `access_mode`。
- 后端根据所选 `group_id` 查询 `group_type`，自动写入 `access_mode`。
- `model_routing_mode` 继续由用户选择，仅影响多模型策略。

### 6.4 Upstream Key

- UI 已支持四类 tier，无需改动。
- 需要确保 `premium` 和 `enterprise` 两类 key 在 Proxy 中按预期隔离或共享。

---

## 7. 预期表现

1. **admin 无需维护用户类型**：创建用户后，直接分配 Group 即可。
2. **Group 即套餐/等级**：`economy` 组的用户只能用 economy tier key，`subscription` 组用户能用 premium key。
3. **key 创建简化**：用户只需要选 Group 和模型路由模式，access_mode 自动正确。
4. **路由可预测**：
   - 个人标准用户 → normal priority → default key
   - 经济型用户 → economy priority → economy key
   - 订阅/企业用户 → high priority → premium/enterprise key
5. **routing_track 灵活**：同一 Group Type 可以绑定不同路由轨道，而不影响 key 等级选择。

---

## 8. 实施范围（Portal + Proxy）

### Portal

- `internal/db/schema.go`：保留 `user_type` 列，移除默认值依赖（可选）。
- `internal/routes/auth.go`：新用户注册不再写 `user_type`。
- `internal/web/admin_handler_common.go`：用户详情页移除 `user_type` 相关逻辑。
- `web/templates/admin-users.html`：移除 User Type 下拉。
- `internal/routes/apikeys_crud.go`：创建/更新 key 时根据 `group_id` 自动设置 `access_mode`。
- `web/templates/apikeys.html`：移除 `access_mode` 相关表单字段（如果还有）。
- `internal/routes/groups.go` 及模板：限制 Group Type 为四类。

### Proxy

- `ai-proxy-go/internal/server/handlers.go`：更新 `getUserPriority` 映射，支持 `general / economy / subscription / enterprise`。
- （可选）`ai-proxy-go/pkg/keypool/manager.go`：如需严格隔离 enterprise key，可在 `high` 内部按 tier 细分。

---

## 9. 兼容性说明

- 旧 key 的 `access_mode` 可能为 `single_model` / `multi_model` / 空。重构后需做一次数据迁移：根据 key 所属 Group Type 重写 `access_mode`。
- 旧 `user_type` 数据保留，不删除列，避免回滚困难。
- 如果某 key 没有 Group，则 fallback 到 `general` / `normal`。

---

## 10. 关于 `routing_track` 的补充说明

**`routing_track` 不影响 Proxy 的 key 优先级。**

- 它只决定请求先走 `standard / bedrock / mantle` 中的哪条轨道。
- 进入某条轨道后，Proxy 仍然根据 `access_mode` → `userPriority` 来选择该轨道内的高/中/经济型 key。
- 因此 `routing_track` 是“横向轨道选择”，`access_mode` 是“纵向 key 等级选择”，两者独立。
