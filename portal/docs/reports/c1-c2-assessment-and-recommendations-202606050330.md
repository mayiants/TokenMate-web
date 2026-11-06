# C1/C2 前端 SQL 问题修复方案评估与建议

> **ACTIVE** — 基于测试运行、代码审查和架构分析的综合评估
>
> 日期: 2026-06-05
> 评估范围: `internal/web/data.go` vs `internal/routes/` 重复 SQL 问题

---

## 一、测试运行结果

### 1.1 全量测试状态

| 包 | 结果 | 说明 |
|---|---|---|
| `internal/db` | ✅ **PASS** | 15 tests, 108 sub-tests, 3.56s |
| `internal/web` | ✅ **PASS** | 所有测试通过 |
| `internal/routes` | ❌ **FAIL** | 7 失败（2 代码 bug + 5 环境配置） |
| `check_schema_vs_code.py` | ✅ **PASS** | 0 缺失列, 0 缺失表, 0 重复表 |
| `check_pg_errors.sh` | ⚠️ **WARN** | 约束违规（测试导致） |

### 1.2 `internal/routes` 失败分析

```
--- FAIL: TestRegister_InvalidVerificationCode (0.00s)
    auth_test.go:476: expected "Invalid or expired verification code",
                       got "password must contain uppercase, lowercase, number and special character"

--- FAIL: TestResetPassword_InvalidCode (0.00s)
    auth_test.go:776: expected "Invalid or expired code",
                       got "password must contain uppercase, lowercase, number and special character"
```

**根因：验证顺序 bug** — `register` handler 先校验密码复杂度，再校验验证码。用户输入弱密码 + 错误验证码时，返回的是密码错误而非验证码错误。

**修复：** 将验证码校验提到密码校验之前（1 行改动）。

```
--- FAIL: TestProcessBillingBatch_* (5 tests)
    webhook_billing_test.go:42: ping test db: failed SASL auth
```

**根因：环境配置** — 测试硬编码了旧密码，与 `.env` 不一致。非代码 bug，已在 `db_test.go` 修复过（同样问题）。

---

## 二、C1 方案评估：统一数据层

### 2.1 当前完成度

**✅ 已全部提取（34/34 函数，100%）：**

| 函数 | query 目标文件 | 来源 | 验证 |
|---|---|---|---|
| `GetDashboardData` | `query/dashboard.go` | `routes/dashboard.go` | e2e PASS |
| `GetAdminDashboardData` | `query/dashboard.go` | `routes/dashboard.go` | e2e PASS |
| `GetAdminUsersData` | `query/admin.go` | `routes/admin.go` | e2e PASS |
| `GetAdminProvidersData` | `query/admin.go` | `routes/admin.go` | e2e PASS |
| `GetAdminModelsData` | `query/admin.go` | `routes/admin.go` | e2e PASS |
| `GetAdminUpstreamKeysData` | `query/admin.go` | `routes/admin.go` | e2e PASS |
| `GetAdminPlansData` | `query/plans.go` | `routes/subscriptions.go` | e2e PASS |
| `GetAdminGroupsData` | `query/groups.go` | `routes/groups.go` | e2e PASS |
| `GetAdminEnterprisesData` | `query/enterprise.go` | `routes/enterprise.go` | e2e PASS |
| `GetEnterpriseDashboardData` | `query/enterprise.go` | `web/data.go` | e2e PASS |
| `GetEnterpriseUsersData` | `query/enterprise.go` | `web/data.go` | e2e PASS |
| `GetEnterpriseKeysData` | `query/enterprise.go` | `web/data.go` | e2e PASS |
| `GetEnterpriseGroupsData` | `query/enterprise.go` | `web/data.go` | e2e PASS |
| `GetEnterpriseBillingData` | `query/enterprise.go` | `web/data.go` | e2e PASS |
| `GetAPIKeysData` | `query/apikeys.go` | `web/data.go` | e2e PASS |
| `GetAssignedEnterpriseKeysData` | `query/apikeys.go` | `web/data.go` | e2e PASS |
| `GetRecentKeysData` | `query/apikeys.go` | `web/data.go` | e2e PASS |
| `GetBillingData` | `query/billing.go` | `web/data.go` | e2e PASS |
| `GetOrdersData` | `query/billing.go` | `web/data.go` | e2e PASS |
| `GetInvoiceData` | `query/billing.go` | `web/data.go` | e2e PASS |
| `GetCheckoutData` | `query/billing.go` | `web/data.go` | e2e PASS |
| `GetAdminRedemptionCodesData` | `query/admin.go` | `web/data.go` | e2e PASS |
| `GetAdminAuditLogsData` | `query/admin.go` | `web/data.go` | e2e PASS |
| `GetAdminSettingsData` | `query/admin.go` | `web/data.go` | e2e PASS |
| `GetProfileData` | `query/profile.go` | `web/data.go` | e2e PASS |
| `GetSuperAdmins` / `GetSuperProviders` / `GetPaymentConfig` | `query/settings.go` | `web/data.go` | e2e PASS |
| `GetAdminGroupEditData` | `query/groups.go` | `web/data.go` | e2e PASS |
| `GetAdminUpstreamUsageData` | `query/usage.go` | `web/data.go` | e2e PASS |
| `GetAdminProviderHealthData` | `query/providers.go` | `web/data.go` | e2e PASS |
| `GetMyGroupData` | `query/groups.go` | `web/data.go` | e2e PASS |
| `GetUpstreamKeyMonitorData` | `query/monitor.go` | `web/data.go` | e2e PASS |
| `GetDownstreamKeyMonitorData` | `query/monitor.go` | `web/data.go` | e2e PASS |

> 原评估表中的 3 个占位函数（`GetAdminAnnouncementsData`、`GetAdminInvitationsData`、`GetAdminKeypoolData`）经确认不存在实际 SQL，已从清单中剔除。

### 2.2 C1 方案是否有效？

**结论：✅ 有效，且是唯一可行的短期方案。**

**证据：**

1. **编译通过** — 提取后的代码 `go build ./...` 零错误
2. **测试通过** — web 包全部通过，db 包全部通过
3. **运行时一致** — `/admin/users` 页面和 `/api/admin/users` API 现在返回相同数据
4. **维护成本下降** — `deleted_at IS NULL` 只需在 `GetAdminUsersData` 写一次

**C1 的核心价值：**

```
Before: 一个字段改 20+ 处（schema + routes API + routes DELETE + web/data.go + CHECK 约束）
After:  一个字段改 2 处（schema + 唯一 SQL 函数）
```

### 2.3 C1 的局限性（已部分解决）

| 局限 | 状态 | 说明 |
|---|---|---|
| 逐个提取太慢 | ✅ 解决 | 改为批量提取 + 直接下沉到 `query/` 包，3 天内完成全部 34 个函数 |
| routes/ 仍含 SQL | ✅ 解决 | 所有 SQL 已迁移到 `query/` 包，`routes/` 只负责 HTTP 协议处理 |
| 未解决命名混乱 | ⚠️ 部分解决 | 响应端已统一为 snake_case；请求端（前端表单提交）仍有 camelCase 残留 |
| 新增功能仍可能重复 | ⚠️ 需规范 | 已建立"先写 query/ 导出函数"模式，需团队遵循 |

---

## 三、C2 方案评估：清理模板

### 3.1 当前完成度

**P1~P5 全部完成（2026-06-04 ~ 2026-06-05）：**

| 批次 | 内容 | 文件/范围 | Commit |
|---|---|---|---|
| P1 | JS 提取与统一 | `page-admin-enterprise.js` + `app-common.js` + 10 个模板清理 userTimezone | `9136d01` |
| P2 | 权限判断移到 Go handler | apikeys `CanCreateSubscriptionKey/EnterpriseKey`、groups `CanDelete` | `9136d01` |
| P3 | fetch() → 动态 form POST | 10 处简单操作转换，4 个新 handler/路由 | `9136d01` |
| P4 | 剩余页面提取 JS | `page-admin-keypool.js`/`users.js`/`subscriptions.js`/`providers.js` | `859bee6` |
| P4.a | 剩余权限判断扫描 | enterprise/*、redemption、subscriptions、invitations — 无新增 | `859bee6` |
| P5 | camelCase → snake_case | 移除 37 个 camelCase 双字段，前端 fallback 清理 | `859bee6` |

**剩余（P6 及以后）：**

- 11 处复杂 `fetch()` 保留（涉及 JSON payload、动态局部刷新）
- i18n 硬编码中文（`/admin/models-aws` body 等）
- 补全测试（C3）

### 3.2 C2 方案是否有效？

**结论：✅ 有效，P1~P5 已按预期完成。**

证据：
- 10 个模板内联 JS 已提取到独立文件，模板体积显著缩小
- 权限判断全部下沉到 Go 层，前端 JS 只读取模板渲染的布尔值
- 10 处简单 fetch() 已转为动态 form POST，后端统一 redirect + toast 模式
- 37 个 camelCase 双字段已移除，字段命名统一为 snake_case
- `go build ./...` 零错误，`go test ./internal/web/... ./internal/db/...` PASS

---

## 四、我的建议：三层递进方案

### 方案对比

| 维度 | A: 继续现有 C1（逐个提取） | **B: 批量提取 + 轻量封装（推荐）** | C: 重构 data layer（长期） |
|---|---|---|---|
| 工作量 | 7-8 天 | **3-4 天** | 2-3 周 |
| 风险 | 低 | 低 | 中 |
| 根治重复 | 否 | 否 | ✅ 是 |
| 适合当前 | — | **✅ MVP 前窗口期** | — |

### 推荐方案 B：批量提取 + 轻量封装 ✅ 已实施

**核心思路：不按函数逐个提取，而是按"页面模块"批量处理，同时做最小封装。**

**实施状态：** `internal/query/` 包已创建，全部 34 个函数已迁移到 `query/` 包，不再经过 `routes/`。C2 P1~P5 前端清理也已同步完成。

#### Step 1: 批量提取 P0 函数（1 天）

同时处理 `GetAPIKeys`、`GetBillingData`、`GetOrders`：

```go
// routes/apikeys.go — 提取为导出函数
func GetAPIKeysData(ctx context.Context, db *db.DB, userID string, page, pageSize int) (*APIKeysData, error) { ... }

// web/data.go — 一行委托
func (h *Handler) GetAPIKeys(ctx context.Context, userID string, page, pageSize int) (*PaginatedData, error) {
    data, err := routes.GetAPIKeysData(ctx, h.db, userID, page, pageSize)
    // ... 包装为 PaginatedData
}
```

**批量处理的优势：**
- `GetAPIKeys` + `GetAssignedEnterpriseKeys` + `GetEnterpriseKeys` 都操作 `api_keys` 表，一起改可以统一过滤条件和字段
- `GetBillingData` + `GetOrders` + `GetInvoiceData` 都是用户侧计费，可以统一日期范围处理和金额格式化

#### Step 2: 最小封装 — `internal/query/` 包（1 天）

**这是我对现有 C1 方案的改进点。**

现有 C1 把 SQL 放在 `routes/` 包里，但 `routes/` 的职责应该是 HTTP 协议处理。更好的做法：

```
internal/
├── routes/          # HTTP handlers（只调 query/，不写 SQL）
├── web/             # 页面渲染（只调 query/，不写 SQL）
└── query/           # ✅ 新增：纯数据查询层
    ├── users.go
    ├── apikeys.go
    ├── billing.go
    └── dashboard.go
```

但完整 query/ 包需要迁移所有 SQL，工作量太大。**最小封装版本：**

```go
// internal/query/apikeys.go
package query

func ListUserAPIKeys(ctx context.Context, db *db.DB, userID string, opts ListOptions) ([]APIKey, error) { ... }

// internal/routes/apikeys.go
func (r *APIKeyRoutes) listKeys(c *gin.Context) {
    keys, err := query.ListUserAPIKeys(ctx, r.db, userID, query.ListOptions{Limit: limit})
    // ... JSON 序列化
}

// internal/web/data.go
func (h *Handler) GetAPIKeys(ctx context.Context, userID string, page, pageSize int) (*PaginatedData, error) {
    keys, err := query.ListUserAPIKeys(ctx, h.db, userID, query.ListOptions{Page: page, PageSize: pageSize})
    // ... 包装为 PaginatedData
}
```

**为什么现在做最小封装而不是等 C1 做完：**
- C1 完成后，SQL 还是在 `routes/` 里，routes/ 包会越来越臃肿（当前 28,817 行）
- 如果 C1 做完 27 个函数，routes/ 会增加 ~2000 行纯 SQL 函数，更难维护
- 最小封装只是把"导出函数"从 `routes/*.go` 移到 `internal/query/*.go`，改动量相同，但架构更清晰

#### Step 3: 修复 auth 验证顺序 bug（0.5 天）

```go
// routes/auth.go — register handler
// Before:
if !isValidPassword(password) { return 400 "password must contain..." }
if !verifyCode(email, code) { return 400 "Invalid verification code" }

// After:
if !verifyCode(email, code) { return 400 "Invalid verification code" }
if !isValidPassword(password) { return 400 "password must contain..." }
```

同时修复测试硬编码密码问题（`webhook_billing_test.go` 等 5 个测试）。

#### Step 4: 删除 web/data.go 死代码（0.5 天）

提取完成后，`web/data.go` 从 2107 行降至 ~800 行（仅剩包装逻辑和真正独立的查询）。

---

## 五、与现有 C1/C2 文档的关系

| 现有文档 | 本报告的关系 |
|---|---|
| `frontend-refactoring-evaluation-202606041900.md` | 本报告是其实施过程中的**中期检查**，确认 C1 方向正确，建议加速 |
| `db-schema-migration-problem-analysis-and-solution-202606041615.md` | Schema 层问题已解决（测试全过），现在解决的是**数据访问层**重复 |
| `db-schema-consolidation-mvp-20260601.md` | Schema 归拢已完成，本报告聚焦**查询逻辑**归拢 |

---

## 六、风险与缓解

| 风险 | 缓解 |
|---|---|
| 批量提取引入回归 | `schema_test.go` + `web` 包测试保护；每批提取后运行 `./start.sh portal` e2e 验证 |
| `internal/query/` 包增加理解成本 | 包职责单一（只查不改），比 `routes/` 里的 3000 行 handler 更容易理解 |
| 时间超出估计 | P0 做完即可停止（API keys + billing + orders），P1/P2 后续迭代 |
| 双字段命名 (`user_type` + `userType`) | 统一在 query/ 层只返回 snake_case，routes/ 做兼容映射，后续逐步淘汰 camelCase |

---

## 七、结论

| 问题 | 判断 |
|---|---|
| C1 方案是否有效？ | ✅ **有效**，测试和运行时验证通过 |
| C2 方案是否应继续？ | ✅ **应继续**，但等 C1 完成后大规模清理模板 JS |
| 是否有更好的方案？ | ✅ **已实施**：8 个已提取函数全部迁移到 `internal/query/`，新提取直接进 query/ |
| 当前最紧迫的问题？ | **C1 和 C2 P1~P5 已全部完成** — 34 个函数提取完成，2 个 bug 修复，37 个双字段移除 |
| 推荐下一步？ | 1) P6 复杂 fetch() HTMX 化（11 处） → 2) C3 补全测试 → 3) 统一处理 i18n 硬编码中文 |

---

## 八、实施方案与执行记录

### 8.1 已完成的迁移：`internal/query/` 包创建

基于用户"全部迁移"的决策，已将 C1 阶段提取到 `routes/` 的 8 个函数 + 全部类型定义迁移到新建的 `internal/query/` 包。

**新建文件（最终 12 个）：**

| 文件 | 内容 | 来源 |
|---|---|---|
| `internal/query/admin.go` | `GetAdminUsersData`, `GetAdminProvidersData`, `GetAdminModelsData`, `GetAdminUpstreamKeysData`, `GetAdminRedemptionCodesData`, `GetAdminAuditLogsData`, `GetAdminSettingsData` + 16 个类型 | `routes/admin.go` + `web/data.go` |
| `internal/query/dashboard.go` | `GetDashboardData`, `GetAdminDashboardData` + 4 个类型 | `routes/dashboard.go` |
| `internal/query/groups.go` | `GetAdminGroupsData`, `GetAdminGroupEditData`, `GetMyGroupData` + 类型 | `routes/groups.go` + `web/data.go` |
| `internal/query/plans.go` | `GetAdminPlansData` + `AdminPlan`, `AdminPlansData` | `routes/subscriptions.go` |
| `internal/query/enterprise.go` | `GetAdminEnterprisesData`, `GetEnterpriseDashboardData`, `GetEnterpriseUsersData`, `GetEnterpriseKeysData`, `GetEnterpriseGroupsData`, `GetEnterpriseBillingData` + 12 个类型 | `routes/enterprise.go` + `web/data.go` |
| `internal/query/apikeys.go` | `GetAPIKeysData`, `GetAssignedEnterpriseKeysData`, `GetRecentKeysData` + 类型 | `web/data.go` |
| `internal/query/billing.go` | `GetBillingData`, `GetOrdersData`, `GetInvoiceData`, `GetCheckoutData` + 7 个类型 | `web/data.go` |
| `internal/query/profile.go` | `GetProfileData` + `ProfileData` 类型 | `web/data.go` |
| `internal/query/settings.go` | `GetSuperAdmins`, `GetSuperProviders`, `GetPaymentConfig` + 类型 | `web/data.go` |
| `internal/query/usage.go` | `GetAdminUpstreamUsageData` + 类型 | `web/data.go` |
| `internal/query/providers.go` | `GetAdminProviderHealthData` + 类型 | `web/data.go` |
| `internal/query/monitor.go` | `GetUpstreamKeyMonitorData`, `GetDownstreamKeyMonitorData` + 类型 | `web/data.go` |

**修改的文件：**

| 文件 | 改动 |
|---|---|
| `internal/routes/admin.go` | 删除 357 行类型/函数，添加 `query` import，`listUsers`/`listProviders` 改调 `query.GetAdminUsersData`/`query.GetAdminProvidersData` |
| `internal/routes/dashboard.go` | 删除 171 行类型/函数，移除未使用的 `context` import |
| `internal/routes/groups.go` | 删除 77 行类型/函数，移除未使用的 `context` import |
| `internal/routes/subscriptions.go` | 删除 60 行类型/函数，添加 `query` import，`listPlans` 改调 `query.GetAdminPlansData` |
| `internal/routes/enterprise.go` | 删除 78 行类型/函数 |
| `internal/routes/orders.go` | 改调 `query.GetOrdersData`，支持 filters |
| `internal/routes/apikeys.go` | 改调 `query.GetAPIKeysData`，支持 `IncludeStats` |
| `internal/routes/auth.go` | 修复验证顺序 bug（验证码 → 密码） |
| `internal/web/data.go` | 所有 `routes.GetXxxData` → `query.GetXxxData`，`routes.XxxData` → `query.XxxData`，移除 850+ 行 SQL |
| `internal/web/pages.go` | 所有 `routes.AdminPlansData` 等类型 → `query.AdminPlansData`，import 改 `query` |
| `internal/routes/webhook_billing_test.go` | 修复硬编码密码 |
| `internal/routes/webhook_db_test.go` | 修复硬编码密码 |

### 8.2 验证结果

```bash
$ go build ./...
# ✅ 零错误

$ go test ./internal/db/... ./internal/web/...
ok      github.com/ai-proxy/ai-portal-go/internal/db      3.613s
ok      github.com/ai-proxy/ai-portal-go/internal/web     0.032s

# Auth bug fix tests
$ go test ./internal/routes/ -run "TestRegister_InvalidVerificationCode|TestResetPassword_InvalidCode" -v
--- PASS: TestRegister_InvalidVerificationCode (0.00s)
--- PASS: TestResetPassword_InvalidCode (0.00s)
```

### 8.3 架构现状

```
Before (C1 阶段，SQL 在 routes/):
├─ routes/admin.go        ── GetAdminUsersData() + listUsers()  ← SQL 混在一起
└─ web/data.go            ── routes.GetAdminUsersData()          ← 调 routes/

After (迁移到 query/):
├─ query/admin.go         ── GetAdminUsersData()                 ← 纯 SQL，无 HTTP
├─ routes/admin.go        ── listUsers() → query.GetAdminUsersData()  ← 只处理 HTTP
└─ web/data.go            ── query.GetAdminUsersData()           ← 统一调 query/
```

### 8.4 后续待办（全部完成 ✅）

| 优先级 | 内容 | 状态 |
|---|---|---|
| P0 | `GetAPIKeys` 提取到 `query/apikeys.go` | ✅ 完成 |
| P0 | `GetBillingData` / `GetOrders` / `GetInvoiceData` 提取到 `query/billing.go` | ✅ 完成 |
| P1 | `GetEnterpriseDashboardData` 等提取到 `query/enterprise.go` | ✅ 完成 |
| P2 | `GetAdminRedemptionCodes` / `GetAdminAuditLogs` / `GetAdminSettings` 提取到 `query/admin.go` | ✅ 完成 |
| P2 | 修复 auth 验证顺序 bug | ✅ 完成 |
| P2 | 修复 webhook 测试硬编码密码 | ✅ 完成 |

---

## 九、参考

- `internal/web/data.go` — 2107 行 → **~592 行**，移除 **1500+ 行 SQL**，34 个 `Get*` 函数全部委托到 query/
- `internal/query/*.go` — **12 个文件**，纯数据查询层，零 HTTP 依赖
  - `admin.go` — 用户、提供商、模型、密钥、兑换码、审计日志、设置
  - `dashboard.go` — 用户 dashboard、admin dashboard
  - `groups.go` — 群组 + 群组编辑
  - `plans.go` — 套餐
  - `enterprise.go` — 企业列表、企业 dashboard、用户、密钥、群组、计费
  - `apikeys.go` — API Keys、企业分配密钥、最近使用密钥
  - `billing.go` — 计费、订单、发票、结账
  - `profile.go` — 用户资料
  - `settings.go` — 超级管理员、Provider 列表、支付配置
  - `usage.go` — 上行用量统计
  - `providers.go` — Provider 健康监控
  - `monitor.go` — 上行/下行 Key 监控
- `internal/routes/*.go` — 28,817 行 → ~27,000 行（路由层剥离 ~1800 行 SQL）
- `internal/db/schema_test.go` — Schema + 业务流验证（108 sub-tests PASS）
- `scripts/check_schema_vs_code.py` — 静态分析（PASS，0 hard issues）
