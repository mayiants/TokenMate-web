# 前端重构评估：精简 HTMX vs React 重写

> **ACTIVE** — 评估前端架构方向，基于团队规模、技术栈和当前痛点
>
> 日期: 2026-06-04（更新于 2026-06-05：C1 100% 完成 + C2 P1~P5 全部完成）
> 团队: 1-2 人，后端为主，前端经验有限
> 已有方案: `frontend-rebuild-proposal-v3-202605040300.md`（HTMX+Alpine 共识）

---

## 〇、与已有文档的关系

2026-05-04 期间已有 **11 份前端文档** 讨论了技术选型，结论是 V3：

| 文档 | 结论 |
|------|------|
| `frontend-rebuild-proposal-v3-202605040300.md` | ✅ HTMX+Alpine+Tailwind (purged) + Go template |
| `frontend-architecture-design-v2-202605040443.md` | ✅ 同上，五层用户模型、双计费 |
| `web-frontend-architecture-design-v3-202605040636.md` | ✅ 同上，i18n、发票、GDPR 细化 |

**V3 方案已定，不需要重新讨论框架选择。本报告聚焦于 2026-06-04 新增发现：后端 SQL 重复导致的维护问题，以及如何在前端不加重的前提下解决。**

---

## 一、新增痛点（2026-06-04 发现）

### 1.1 同一个过滤条件需要改 10+ 处

添加 `deleted_at` 软删除时：

| 层级 | 改动次数 | 文件 |
|------|---------|------|
| schema | 1 | `schema.go`（14 张表加列） |
| routes API | 4 | `groups.go`, `models.go`, `redemption.go`, `subscriptions.go`... |
| routes DELETE | 7 | 7 个文件硬 DELETE → 软删除 |
| web/data.go | 6+ | 页面渲染查询漏了过滤 → **页面仍显示已删数据** |
| CHECK 约束 | 3 | `invoice_profiles`, `api_keys`, `enterprises` |

**一个字段改了 20+ 处**，测试只覆盖 API 层，页面渲染层的遗漏是用户发现的。

### 1.2 根因：data.go 重复了 routes 的 SQL

```
routes/groups.go:listGroups()   → SELECT ... WHERE deleted_at IS NULL  ← 修了
web/data.go:GetAdminGroups()    → SELECT ... (没 WHERE)                ← 漏了！
```

用户访问 `/admin/groups` 页面 → 走 data.go → 删掉的 group 还在列表里。但 API `/api/admin/groups` 返回正确。排查了 30 分钟才发现是两条不同的 SQL。

### 1.3 前端的 JS 逻辑加重了这个问题

模板里嵌着从 Node.js 继承的 JS 判断（状态显示、权限检查、字段格式化），这些逻辑 Go 后端也有但写得不完全一致。结果是：一个 bug 可能出在三层中任何一层。


---

## 二、重构目标

**核心原则：逻辑归于后端，前端只管展示。**

```diff
- 前端 JS 做业务判断 + data.go 写 SQL + routes 写 SQL
+ routes 是唯一数据访问层，前端只调 API 或服务端渲染
```

目标架构：

```
┌──────────────────────────────────────────┐
│ 前端 (轻量模板)                            │
│  ├─ HTMX：页面导航、表单提交               │
│  ├─ Alpine.js：toggle、modal、dropdown    │
│  └─ 不做：业务判断、数据转换、复杂状态管理   │
├──────────────────────────────────────────┤
│ 后端 (统一数据层)                          │
│  ├─ routes/ → 唯一的数据访问入口           │
│  ├─ JSON API → 调 routes                  │
│  └─ 页面渲染 → 也调 routes                 │
├──────────────────────────────────────────┤
│ 数据库                                     │
│  ├─ InitSchema 完整覆盖所有字段            │
│  ├─ 集成测试自动验证 schema + 约束          │
│  └─ 字段变更只需在 schema.go 改一次         │
└──────────────────────────────────────────┘
```

---

## 三、方案对比

| 维度 | A: React 全量重写 | **B: 精简当前 HTMX** | C: 渐进式 (推荐) |
|------|------|------|------|
| 前端框架 | React + Vite | HTMX + Alpine.js | HTMX + Alpine.js |
| 工作量 | 6-8 周 | 1-2 周 | **3-4 周** |
| 学习成本 | 高 | 低 | 低 |
| 维护成本 | 中（需 JS 技能） | 低 | 低 |
| UX 体验 | 优秀 | 够用 | 够用 |
| 风险 | 高 | 低 | 低 |
| 适合 | 有前端专人 | 后端为主 | **后端为主** |

### 为什么不做 React 重写

1. **人员不匹配** — 1-2 人团队，前端经验有限。React 学习曲线陡峭（hooks、状态管理、路由、构建配置），投入产出比低
2. **产品性质** — B2B 管理后台，用户是管理员，不是消费者。HTMX 的页面级交互完全满足需求
3. **真正的瓶颈在后端** — SQL 重复、类型不一致、CHECK 约束不匹配才是高频 bug 来源。换成 React 只会增加一层需要同步的接口
4. **React 引入新问题** — 需要维护 API 类型定义、前端状态与后端同步、SSR/CSR 选择、构建流水线

### 为什么精简 HTMX 是正确方向

1. **Go 开发者可直接维护** — 模板语法简单，后端工程师改 bug 不用学 React
2. **无构建步骤** — 模板嵌入 Go 二进制，部署简单
3. **天然一致** — 服务端渲染直接拿到后端数据，不存在前后端数据不同步
4. **HTMX 够用** — 表单提交、列表分页、弹窗编辑都是 HTMX 擅长的场景
5. **Alpine.js 补位** — dropdown、modal、toggle 等轻交互，不需要 React 状态管理

---

## 四、推荐方案：渐进式

分三步走，每一步都有独立价值，可以随时停下来。

### C1: 统一数据层（3-5 天）

**目标**：删除 `web/data.go` 的独立 SQL，页面渲染调用 `routes/` 层函数

```
Before:
  GET /admin/groups → web/pages.go → web/data.go → 自己写 SQL
  GET /api/admin/groups → routes/groups.go → 自己写 SQL（重复！）

After:
  GET /admin/groups → web/pages.go → routes/groups.GetGroups()
  GET /api/admin/groups → routes/groups.GetGroups()
  只有一份 SQL
```

具体工作：

1. 将 `routes/` 中的列表/详情查询函数导出为可复用的函数
2. `web/pages.go` 的 handler 调用这些函数而非 `data.go`
3. 删除 `data.go` 中已被替代的函数
4. 运行已有测试确保无回归

### C2: 清理模板（3-5 天）

**目标**：移除模板中内嵌的 JS 业务逻辑，统一 JSON 字段命名

具体工作：

1. 扫描 55 个 HTML 模板，找出 JS 中的业务判断（如状态判断、权限检查）
2. 移到 Go handler 层处理，模板只做 `{{if .Condition}}` 简单渲染
3. 统一 camelCase → snake_case（Go 端统一加 `json` tag 双格式支持）
4. 删掉不再需要的 JS 函数和变量

### C3: 补全测试（2-3 天）

**目标**：确保改完后不再出现 schema/API 不一致

具体工作：

1. `TestInitSchema` 扩展：覆盖全部表 + 全部 CHECK 约束 + 全部 FK 引用
2. API 冒烟测试：对每个 CRUD 端点自动验证创建→读取→更新→删除
3. CI 集成：每次 PR 跑 schema 测试和 API 冒烟

### 工作量汇总

| 阶段 | 工作 | 估时 |
|------|------|------|
| C1 | data.go → routes/，删除重复 SQL | 3-5 天 |
| C2 | 清理模板 JS 逻辑，统一字段命名 | 3-5 天 |
| C3 | 扩展测试，CI 集成 | 2-3 天 |
| **合计** | | **8-13 天** |

---

## 五、风险与取舍

| 风险 | 缓解 |
|------|------|
| C1 重构 data.go 时引入回归 | 已有 schema_test.go 和业务测试保护 |
| HTMX 某些交互不够流畅 | Alpine.js + 少量自定义 JS 补位（如表格排序、筛选） |
| 未来可能需要 SPA 体验 | HTMX 不是排斥 React，而是先修好后端架构；真需要时再加 API 层即可 |
| 模板越来越多，组织困难 | Go embed 已支持，55 个模板在管理后台项目中可接受 |

---

## 六、与 V3 方案的关系

已有 `frontend-rebuild-proposal-v3-202605040300.md` 的 V3 方案完全正确（HTMX+Alpine+Tailwind+Go template），不需要改框架。本报告的 C1+C2+C3 是对 V3 的**落地优化**，核心补充：

1. **V3 的 `internal/web/*` 新增** — 但当前 data.go 的 SQL 重复问题说明，新增 web handler 必须调 routes 层，不能再自己写 SQL
2. **V3 提到"前端 JS 清理"** — 但没量化：当前 55 个模板有 2475 处中文硬编码（见 `frontend-i18n-audit.md`），大量 JS 判断分散在模板中
3. **C1（统一数据层）是 V3 落地的前提** — data.go 的 SQL 不清理干净，前端做再好也会出现 API 正确、页面错误的问题

## 七、结论

**技术栈沿用 V3 方案（HTMX+Alpine+Go template），先花 8-13 天做三件事：**

| 优先级 | 阶段 | 效果 |
|--------|------|------|
| P0 | C1: 统一数据层 — data.go 删重复 SQL，调 routes | 再也不会出现"API 对、页面错" |
| P1 | C2: 清理模板 — 移除 JS 业务逻辑，统一命名 | 模板只做渲染，bug 范围缩小到 Go |
| P2 | C3: 补全测试 | 自动发现 schema 不一致 |

之后如果业务需要 React 级交互，后端 API 层已经成熟，前端只管调接口。

> **当前状态（2026-06-05）：C1 ✅ 完成（34/34）+ C2 P1~P5 ✅ 完成。剩余 P6（复杂 fetch HTMX 化）和 C3（补全测试）。**

---

## 八、C1 实施进度 (2026-06-04 ~ 2026-06-05)

> **当前状态：✅ 34/34 函数全部迁移（100%），C1 已完成。**
> 2 个 BLOCKER 已修复（详见下方）。

### 架构升级：`internal/query/` 纯数据层

基于评估报告 `c1-c2-assessment-and-recommendations-202606050330.md` 的建议，已将 C1 提取的函数从 `routes/` 进一步下沉到新建的 `internal/query/` 包，使 `routes/` 只负责 HTTP 协议处理，`query/` 只负责纯数据查询。

```
After (当前):
├─ query/admin.go         ── GetAdminUsersData()                 ← 纯 SQL，无 HTTP
├─ routes/admin.go        ── listUsers() → query.GetAdminUsersData()  ← 只处理 HTTP
└─ web/data.go            ── query.GetAdminUsersData()           ← 统一调 query/
```

### 已完成（22 函数）

| 函数 | 目标文件 | 说明 |
|------|----------|------|
| `GetAdminUsersData` | `query/admin.go` | 统一 `deleted_at IS NULL` + 分页 + `key_count` |
| `GetAdminProvidersData` | `query/admin.go` | 统一 `ORDER BY sort_order, name`；补充 `sync_source` |
| `GetAdminPlansData` | `query/plans.go` | 统一 `ORDER BY sort_order, created_at`；补充 rate limit 字段 |
| `GetAdminModelsData` | `query/admin.go` | 统一 providerFilter + 分页 + 9 个统计数字 |
| `GetAdminUpstreamKeysData` | `query/admin.go` | 统一 providers 下拉框 + key 分页 + 统计 |
| `GetDashboardData` | `query/dashboard.go` | 统一 balance/usage/subscription/invoice 聚合 |
| `GetAdminDashboardData` | `query/dashboard.go` | 统一 stats/RecentUsers/RecentAuditLogs 聚合 |
| `GetAdminGroupsData` | `query/groups.go` | 统一 group 列表查询 |
| `GetAdminEnterprisesData` | `query/enterprise.go` | 企业列表 + 统计 |
| `GetEnterpriseDashboardData` | `query/enterprise.go` | 企业 dashboard 聚合 |
| `GetEnterpriseUsersData` | `query/enterprise.go` | 企业用户列表 |
| `GetEnterpriseKeysData` | `query/enterprise.go` | 企业密钥列表 |
| `GetEnterpriseGroupsData` | `query/enterprise.go` | 企业群组列表 |
| `GetEnterpriseBillingData` | `query/enterprise.go` | 企业计费聚合 |
| `GetAPIKeysData` | `query/apikeys.go` | API Keys 列表 + 统计 |
| `GetAssignedEnterpriseKeysData` | `query/apikeys.go` | 企业分配密钥 |
| `GetBillingData` | `query/billing.go` | 用户计费聚合 |
| `GetOrdersData` | `query/billing.go` | 订单列表 |
| `GetInvoiceData` | `query/billing.go` | 发票列表 |
| `GetAdminRedemptionCodesData` | `query/admin.go` | 兑换码列表 |
| `GetAdminAuditLogsData` | `query/admin.go` | 审计日志列表 |
| `GetAdminSettingsData` | `query/admin.go` | 设置数据 |

**代码量变化**：
- `internal/web/data.go` — 2107 行 → **~595 行**（移除 1500+ 行 SQL）
- `internal/routes/*.go` — 剥离 ~1800 行 SQL
- `internal/query/*.go` — **12 个文件**，纯数据查询层，零 HTTP 依赖

**编译状态**：✅ `go build ./...` 通过  
**测试状态**：✅ `internal/web/` 全部通过；`internal/db/` 全部通过；auth bug 修复测试通过  
**e2e 验证**：✅ 通过 `./start.sh portal` 启动服务，curl 验证以下页面 HTTP 200 且数据非空：
- `/dashboard`, `/profile`, `/admin`, `/admin/users`, `/admin/providers`, `/admin/models`, `/admin/models-aws`
- `/admin/keypool`, `/admin/subscriptions`, `/admin/invitations`, `/admin/redemption`, `/admin/audit-logs`, `/admin/settings`
- `/enterprise`, `/enterprise/users`, `/enterprise/keys`, `/enterprise/groups`, `/enterprise/billing`, `/enterprise/audit`

**e2e 中发现并修复的问题**：
1. `upstream_keys` 的 `rpm_limit`/`tpm_limit`/`quota_limit` 列可能为 NULL，Scan 到 `int`/`int64` 会报错 → 改为 Scan 到 `*int`/`*int64`（commit `1251948`）
2. `subscription_plans` 的 `plan_type`/`duration_days` 列可能为 NULL → 补回 `COALESCE(plan_type, 'token')` 和 `COALESCE(duration_days, 30)`（commit `a37d5f7`）
3. **Auth 验证顺序 bug**：`register`/`reset-password` handler 先校验密码复杂度再校验验证码 → 改为先校验验证码（commit 在 query 迁移批次中）
4. **Webhook 测试硬编码密码**：`webhook_billing_test.go` / `webhook_db_test.go` 修复

### 2026-06-05 BLOCKER 修复

| BLOCKER | 问题 | 修复 | 验证 |
|---|---|---|---|
| **BLOCKER-1** | `GetProfileData` Scan 类型错配：SQL 12 列 vs Scan 13 目标，`balance` (numeric) 被扫入 `&p.Timezone` (string) | `web/data.go`：SQL 补充 `COALESCE(timezone, 'UTC')`，Scan 顺序调整使 `&p.Timezone` 对齐第 9 列 | `/profile` HTTP 200 ✅ |
| **BLOCKER-2** | `/api/admin/usage/chart` 404：模板 `admin.html:341` 调用该端点，但无路由注册 | `routes/usage.go`：`RegisterAdmin` 新增 `GET("/chart", ...)`，`getUsageChart`  handler 返回 `{labels, values}` 格式 | `/api/admin/usage/chart?period=7d` HTTP 200 ✅ |

**已 push 的 commit**：
- `ea8b40b` — C1 第一批：users, providers, plans
- `a37d5f7` — 修复 plans COALESCE
- `1251948` — C1 第二批：models, upstream keys（含 NULL Scan 修复）
- `03e1157` — C1 第三批：dashboard, admin dashboard
- `query 迁移批次` — 8 个 routes/ 函数迁移到 query/ + auth bug 修复 + webhook 测试修复
- `b2357e9` — BLOCKER-1 + BLOCKER-2 修复
- `e7b29f5` — C1 Batch A+B：9 个函数迁移到 query/（profile, settings, checkout, group-edit, usage, provider-health）
- `3b358c3` — C1 Batch C：最后 3 个复杂函数迁移到 query/（my-group, monitor-upstream, monitor-downstream）

### 已完成 Batch A+B（2026-06-05）

| 函数 | 目标文件 | 说明 |
|------|----------|------|
| `GetProfileData` | `query/profile.go` | 用户资料查询 |
| `GetRecentKeysData` | `query/apikeys.go` | Dashboard 最近使用的 5 个 API Key |
| `GetSuperAdmins` | `query/settings.go` | 设置页超级管理员列表 |
| `GetSuperProviders` | `query/settings.go` | 设置页 Provider 列表 |
| `GetPaymentConfig` | `query/settings.go` | 支付网关配置 |
| `GetCheckoutData` | `query/billing.go` | 结账页订单数据 |
| `GetAdminGroupEditData` | `query/groups.go` | 群组编辑页（group + models） |
| `GetAdminUpstreamUsageData` | `query/usage.go` | 上行用量统计 |
| `GetAdminProviderHealthData` | `query/providers.go` | Provider 健康监控 |

### 已完成 Batch C（2026-06-05）

| 函数 | 目标文件 | 说明 |
|------|----------|------|
| `GetMyGroupData` | `query/groups.go` | 用户组 + 企业组 + 速率限制 |
| `GetUpstreamKeyMonitorData` | `query/monitor.go` | 上行 Key 监控 |
| `GetDownstreamKeyMonitorData` | `query/monitor.go` | 下行 Key 监控 |

### C1 完成度：✅ 34/34 函数全部迁移

> `internal/web/data.go` 中所有 `Get*` 函数均已委托到 `query/` 层，不再包含直接 SQL。

**不再存在的函数（原评估表中列出但实际无 SQL）：**
- `GetAdminAnnouncementsData` / `GetAdminInvitationsData` — 页面无服务端数据渲染，纯 JS fetch
- `GetAdminKeypoolData` — 页面已使用已迁移的 `GetAdminUpstreamKeys`

**代码量最终变化**：
- `internal/web/data.go` — 2107 行 → **~595 行**（移除 1500+ 行 SQL）
- `internal/query/*.go` — **12 个文件**，纯数据查询层

---

## 九、C2 实施进度 (2026-06-04 ~ 2026-06-05)

### 已完成

| 批次 | 文件 | 说明 | 行数变化 |
|------|------|------|---------|
| 表单 fetch → HTML form | `admin-subscriptions.html` | 移除 JS submitPlanForm，改为 `<form method="POST">` | — |
| 补充 rpm_limit/tpm_limit | `admin-subscriptions.html` + `admin_handlers.go` | 表单和 handler 缺这两个字段 | — |
| 删除多余 `}` | `admin-subscriptions.html` | 第 343 行多余的 `}` 导致整个 `<script>` 块语法错误 | — |
| 全局 unhandledrejection | `layout.html` | 所有页面 165 个 fetch() 有兜底报错 | — |
| **Batch 1** | `app-common.js` + `apikeys.html` | 新建通用 JS 文件，提取 `copyText`/`esc`/`formatDateTime`/`formatDate`/`api`/`closeModal`；清理 apikeys.html 重复定义的 `showMessage`/`showConfirm`/`copyText` 和局部 modal HTML | 873 → 806 |
| **Batch 2** | `admin-groups.html` | 删除重复的 `closeModal()` 和 `executeConfirm` 别名 | 321 → 314 |
| **Batch 3** | `page-admin-models-aws.js` + `admin-models-aws.html` | 提取 130 行 AWS 模型管理 JS 到独立文件；删除死代码 `csrfToken` | 345 → 216 |
| **Batch 4** | `page-admin-models.js` + `admin-models-general.html` | 提取 840 行通用模型管理 JS 到独立文件；替换模板内 `{{.CSRFToken}}` 为 meta 标签读取 | 1414 → 575 |
| **Batch 5** | `admin-announcements.html` | 删除 `formatDateTime`/`closeModal` 重复定义；改用带参数的 `closeModal('announcement-modal')` | 208 → 192 |
| **Batch 5** | `admin-enterprise.html` | 删除 `formatDateTime`/`executeConfirm` 重复定义 | — |
| **Batch 5** | `invitation.html` | 删除 `formatDateTime`/`formatDate`/`copyText`/`fallbackCopyText`/`showCopyOK` 重复定义 | 303 → 262 |
| **Batch 5** | `admin-invitations.html` | 删除 `formatDateTime` 重复定义 | — |
| **Batch 5** | `billing-details.html` | 删除 `formatDateTime` 重复定义 | — |

**累计效果**：9 个模板共减少 **~1099 行**内联 JS；新增 3 个独立 JS 文件。

### C2 P1~P5 全部完成（commits 9136d01 + 859bee6）

P1（JS 提取与统一）、P2（权限判断下沉 Go）、P3（fetch → form POST）、P4（剩余页面提取 JS）、P4.a（权限扫描）、P5（snake_case 统一）已全部完成。

### 待处理

| 优先级 | 内容 | 影响 | 预估工作量 |
|--------|------|------|-----------|
| P6 | 11 处复杂 fetch() 逐步 HTMX 化（create/edit/topup/invite 等） | 模板交互 | 2-3 天 |
| C3 | 补全测试（schema 扩展、API 冒烟、CI 集成） | 质量保障 | 2-3 天 |

---

## 九、逐层比对发现：data.go 与 routes 存在 6 类不一致（2026-06-04 补充）

> 本节记录对 `internal/web/data.go`（2438 行，35 个 `Get*` 函数，111 处 `db.Query/QueryRow/Exec`）与 `internal/routes/` 层对应 API 的逐行比对结果。
> **结论：`deleted_at` 只是冰山一角，真正的风险是"同一个业务概念在后端有两个独立实现"。**

### 9.1 分页 vs 不分页（页面和 API 返回数据量不同）

| data.go 函数 | 有分页？ | routes 对应函数 | 有分页？ |
|---|---|---|---|
| `GetAdminUsers` | ✅ LIMIT/OFFSET | `admin.go:listUsers` | ❌ 全量 |
| `GetAdminModels` | ✅ | 无 admin 模型列表 API | — |
| `GetAdminUpstreamKeys` | ✅ | `admin.go:listUpstreamKeys` | ❌ |
| `GetAdminRedemptionCodes` | ✅ | 无对应 API | — |
| `GetAdminAuditLogs` | ✅ | 无对应 API | — |

**后果**：页面显示 50 条，API 返回全部；前端 JS 如果按 API 做判断会错；大数据量时 API 性能差。

### 9.2 过滤条件不同（不只是 deleted_at）

- **`GetAdminProviders` vs `admin.go:listProviders`**
  - data.go: `ORDER BY name`
  - routes: `ORDER BY sort_order, name`，还返回 `sync_source`
- **`GetAdminUpstreamKeys` vs `admin.go:listUpstreamKeys`**
  - data.go: 查两遍（providers 下拉列表 + upstream_keys 分页）
  - routes: 只查 upstream_keys，无分页，无 providers 列表
- **`GetAPIKeys` vs `apikeys.go:listKeys`**
  - data.go: 过滤 `NOT (key_type = 'enterprise' AND enterprise_id IS NOT NULL)`
  - routes: **不过滤 enterprise key**，还多了 LATERAL JOIN 统计（`month_requests`, `month_tokens`, `today_requests`, `today_tokens`）

### 9.3 排序不一致

| 函数 | data.go 排序 | routes 排序 |
|---|---|---|
| `GetAdminPlans` | `ORDER BY sort_order, created_at` | `ORDER BY created_at ASC` |
| `GetAdminProviders` | `ORDER BY name` | `ORDER BY sort_order, name` |
| `GetAdminUsers` | `ORDER BY created_at DESC` | `ORDER BY created_at DESC` ✅（唯一一致） |

**`GetAdminPlans` 最明显**：admin 页面按 `sort_order` 排列，API 按 `created_at` 升序，看到的套餐顺序完全不同。

### 9.4 返回字段不一致（最隐蔽的 bug 来源）

| 函数 | data.go 字段数 | routes 字段数 | 差异 |
|---|---|---|---|
| `GetAdminUsers` | 9 | 10 | routes 多 `key_count` |
| `GetAdminPlans` | 8 | 13 | routes 多 `rpm_limit, rph_limit, tpm_limit, tph_limit, concurrent_limit` |
| `GetAPIKeys` | 10 | 18+ | routes 多 8 个统计字段（LATERAL JOIN） |

此外，routes 层为了兼容前端 JS，大量返回**双字段命名**：
```go
"user_type": userType, "userType": userType,
"group_id": groupID, "groupId": groupID,
```
而 data.go 返回的是 Go struct（camelCase），模板里的 JS 混用 `data.user_type` 和 `data.userType`，统一时会有一边崩。

### 9.5 统计/聚合逻辑差异（dashboard 类最严重）

- **`GetAdminDashboardData`**（data.go 第 755 行）：12 个独立 `QueryRow` 自己算（总用户数、本周新增、今日请求、活跃模型、上行 key 状态、企业数、24h 告警、平均延迟、provider 健康、请求趋势）。routes 层无对应 admin dashboard API，完全孤立。
- **`GetDashboardData`**（data.go 第 36 行）：8 个独立 `QueryRow`（balance、active_keys、today/month tokens/requests、subscription_quota、pending_invoices）。
- **`GetAdminModels`**：自己算 9 个统计数字（total、active、pending、paused、eol_pending、eol、discontinued、vendorCount、syncedCount）。

### 9.6 字段命名不一致（camelCase vs snake_case）

- data.go 用 Go struct 字段（`UserType`, `GroupID`）
- routes 返回 JSON 用 snake_case（`user_type`, `group_id`），甚至双写兼容
- 模板 JS 中混用两种风格，导致一旦统一字段命名就会报错

---

## 十、C1 实现方法的具体思路

> 目标：**不是让浏览器前端直接调 REST API，而是让 `web/data.go` 调用 `routes/` 层已经封装好的导出函数。**

### 10.1 架构变化

```
现在（有重复）：
├─ GET /api/admin/users  → routes/admin.go:listUsers()      → db.Query(...)   ← SQL A
└─ GET /admin/users      → pages.go → data.go:GetAdminUsers() → db.Query(...)   ← SQL A'（相似但不一致）

目标（唯一数据源）：
├─ GET /api/admin/users  → routes/admin.go:listUsers()      → GetAdminUsersData(db)   ← 唯一 SQL
└─ GET /admin/users      → pages.go → data.go:GetAdminUsers() → routes.GetAdminUsersData(db)   ← 同一套
```

**关键区别**：浏览器/模板不需要改，页面仍然是服务端渲染。改的是 **Go 后端内部** 的数据流向——`data.go` 不再直连数据库，而是调用 `routes` 包暴露的导出函数。

### 10.2 Routes 层需要做的改造

当前 routes 里的查询逻辑嵌在 gin handler 内部，例如：
```go
func (r *AdminRoutes) listUsers(c *gin.Context) {
    rows, err := r.db.Query(...)
    // ... Scan + gin.H 组装
}
```

改造为两层：
```go
// 1. 导出函数：纯数据查询，返回强类型 struct，不依赖 gin
func GetAdminUsersData(ctx context.Context, db *db.DB, page, pageSize int) (*AdminUsersData, error) { ... }

// 2. gin handler：只负责 HTTP 协议层（参数解析、认证、响应序列化）
func (r *AdminRoutes) listUsers(c *gin.Context) {
    page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
    data, err := GetAdminUsersData(c.Request.Context(), r.db, page, 50)
    // ... JSON 响应
}
```

### 10.3 data.go 需要做的改造

改造前（自己写 SQL）：
```go
func (h *Handler) GetAdminUsers(ctx context.Context, page, pageSize int) (*PaginatedData, error) {
    _ = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`).Scan(&total)
    rows, err := h.db.Query(ctx, `SELECT ... FROM users ...`)
    // ... 80 行 SQL + Scan
}
```

改造后（一行调用）：
```go
func (h *Handler) GetAdminUsers(ctx context.Context, page, pageSize int) (*PaginatedData, error) {
    return routes.GetAdminUsersData(ctx, h.db, page, pageSize)
}
```

### 10.4 为什么先修 C1 再碰 C2

`data.go` 的 SQL 不清理干净，前端做再好也会出现 **"API 正确、页面错误"**。C1 完成后：
- `deleted_at IS NULL` 只在 `GetAdminUsersData` 里写一次
- 分页、排序、字段过滤逻辑只有一份
- 模板层感知不到变化，但永远和 API 返回的数据一致
- 新增字段时，改 `routes/` 层的导出函数一处即可

### 10.5 按优先级推进的建议顺序

按 **"不一致严重程度 × 使用频率"** 排序：

| 优先级 | 函数 | 核心问题 |
|---|---|---|
| P0 | `GetAdminUsers` | deleted_at + 分页 + key_count + 双字段命名 |
| P0 | `GetAdminPlans` | 排序不一致 + rate limit 字段缺失 |
| P0 | `GetAPIKeys` | 字段完全不同（10 vs 18+），企业 key 过滤不一致 |
| P1 | `GetAdminProviders` | 排序不一致 + sync_source 缺失 |
| P1 | `GetAdminUpstreamKeys` | 分页 vs 不分页 + provider 列表重复查询 |
| P1 | `GetDashboardData` / `GetAdminDashboardData` | 孤立聚合逻辑，无 API 对应 |
| P2 | `GetAdminModels` | 复杂统计 + providerFilter，routes 无对应 API |
| P2 | `GetBillingData` / `GetOrders` / `GetInvoiceData` | 用户侧计费聚合 |
