# 大型文件拆分重构路线图

> 生成时间：2026-05-29  
> 关联 Issue：D1（main.go 拆分）扩展 — 全局代码库瘦身计划

---

## 一、现状概览

通过对 `ai-portal-go`（Portal）和 `ai-proxy-go`（Proxy）两个项目的 Go 源文件进行行数统计，发现大量文件远超合理规模（业界通常建议单文件不超过 400-600 行或 10-15 个函数）。以下是超标文件的完整清单：

### 1.1 Portal（ai-portal-go）超标文件

| 文件 | 行数 | 函数数 | 严重度 | 说明 |
|------|------|--------|--------|------|
| `internal/routes/admin.go` | **2,898** | 56 | 🔴 P0 | 用户/Provider/Model/Key Pool/验证/导出/审计全混在一起 |
| `internal/routes/enterprise.go` | **2,575** | 51 | 🔴 P0 | 企业CRUD、成员管理、配额、分组、账单、邀请全混在一起 |
| `internal/web/data.go` | **2,432** | 38 | 🔴 P0 | Dashboard/API Keys/Billing/Orders/Profile/Admin panels 数据查询全在一个文件 |
| `internal/web/admin_handlers.go` | **2,320** | 59 | 🔴 P0 | HTMX admin handlers：Model/Provider/Key/Plan/User 操作混杂 |
| `internal/routes/models.go` | **2,331** | 54 | 🔴 P0 | Model CRUD、EOL生命周期、Provider路由、审批流混杂 |
| `internal/routes/webhook.go` | **1,770** | 25 | 🟡 P1 | `processRecord` 单个函数 ~435 行，`processBillingBatch` ~256 行，计费逻辑过度集中 |
| `internal/routes/apikeys.go` | 1,151 | — | 🟡 P2 | 较大，但功能相对聚焦（API Key CRUD），可延后 |
| `internal/routes/subscriptions.go` | 995 | — | 🟡 P2 | 接近 1000 行，订阅计划/购买/管理逻辑 |
| `cmd/server/main.go` | 647 | 6 | 🟡 P1 | D1 原始目标：路由注册、中间件、后台 goroutine、CORS、限流器混杂 |

### 1.2 Proxy（ai-proxy-go）超标文件

| 文件 | 行数 | 函数数 | 严重度 | 说明 |
|------|------|--------|--------|------|
| `pkg/keypool/manager.go` | **2,259** | 65 | 🔴 P0 | 密钥池管理、路由解析、同步、健康检查、RPM/TPM配额、统计全在一个文件 |
| `internal/server/handlers.go` | **2,015** | 39 | 🔴 P0 | Relay handler、Usage记录、Provider Health、Heartbeat 混杂 |
| `internal/auth/auth.go` | 931 | — | 🟡 P2 | 认证/授权/Nonce/BruteForce/RBAC 逻辑，接近拆分阈值 |
| `internal/ratelimit/ratelimit.go` | 848 | — | 🟡 P2 | 限流器核心 + 多种算法实现，较大但功能聚焦 |
| `pkg/relay/channel/aws/aws.go` | 843 | — | 🟡 P2 | AWS Bedrock 适配器，渠道特定实现 |
| `pkg/billing/billing.go` | 639 | — | 🟢 P3 | 中等规模，暂不处理 |

---

## 二、Portal 拆分方案

### 2.1 `internal/routes/admin.go` (2,898 行 → 目标：每文件 < 600 行)

**当前问题：** 包含用户管理、Provider 管理、Model 管理、Key Pool 管理、异步验证、密钥导出、审计日志、系统设置等 8+ 个独立领域。

**拆分目标文件：**

```
internal/routes/
├── admin.go                  # 保留：NewAdminRoutes + Register + 共享辅助函数
├── admin_users.go            # 用户管理 (listUsers, updateUser, deleteUser, getAnalytics)
├── admin_providers.go        # Provider 管理 (list/create/update/toggle/deleteProvider)
├── admin_models.go           # Service Model 管理 (list/updateServiceModel, batchValidate)
├── admin_keys.go             # Key Pool 管理 (listUpstreamKeys, createUpstreamKey,
│                           #   getKeyPool, getKeyHistory, deleteProvider)
├── admin_validation.go       # 异步验证 (validateKeyAsync, validateKeyPoolKey,
│                           #   validateKeyPoolKeysBatch, buildModelsURL)
└── admin_export.go           # 导出功能 (exportKeyPoolKeys, checkExportLimit)
```

**注意事项：**
- `admin.go` 保留 `AdminRoutes` struct 定义和 `Register()` 方法
- 共享的 `buildModelsURL()` 移至 `admin_validation.go` 或 `pkg/utils/`
- `checkExportLimit` 限流器可保留在 `admin_export.go`

---

### 2.2 `internal/routes/enterprise.go` (2,575 行 → 目标：每文件 < 600 行)

**当前问题：** 包含"我的企业"、成员管理、配额管理、企业Admin后台、分组管理、账单详情、邀请处理等 7+ 个领域。

**拆分目标文件：**

```
internal/routes/
├── enterprise.go             # 保留：NewEnterpriseRoutes + Register + 共享中间件
├── enterprise_my.go          # "我的企业"相关 (getMyEnterprise, getMyEnterpriseMembers,
│                           #   getMyEnterpriseUsage)
├── enterprise_members.go     # 成员管理 (removeMyEnterpriseMember, topupMemberQuota,
│                           #   setMemberQuotaLimit, toggleMemberStatus)
├── enterprise_admin.go       # 企业 Admin 后台 (listEnterprises, getEnterpriseBillingDetails,
│                           #   getEnterpriseStats, handleEnterpriseAlert)
├── enterprise_groups.go      # 企业分组 (list/create/update/delete EnterpriseGroup)
└── enterprise_invitations.go # 邀请处理 (list/create/respond EnterpriseInvitation)
```

**注意事项：**
- `requireEnterpriseMember()` 和 `requireEnterpriseAdmin()` 中间件保留在 `enterprise.go`
- `checkEnterpriseLimits()` 和 `sendEnterpriseAlert()` 等辅助函数随主调用方迁移

---

### 2.3 `internal/web/data.go` (2,432 行 → 目标：每文件 < 600 行)

**当前问题：** 纯数据查询层，包含用户 Dashboard、API Keys、Billing、Orders、Profile、Group、Admin Dashboard、Admin Enterprises、Admin Users、Admin Models 等 15+ 个数据查询函数。

**拆分目标文件：**

```
internal/web/
├── data.go                   # 保留：共享结构体定义 + newPaginatedData 等通用辅助
├── data_user.go              # 用户端数据 (GetDashboardData, GetRecentKeys, GetAPIKeys,
│                           #   GetAssignedEnterpriseKeys, GetBillingData, GetOrders,
│                           #   GetProfileData, GetMyGroupData)
├── data_admin.go             # Admin 数据 (GetAdminEnterprises, GetAdminDashboardData,
│                           #   GetAdminUsers, GetAdminModels, GetAdminProviders,
│                           #   GetAdminUpstreamKeys, GetAdminPlans, GetAdminRedemptionCodes,
│                           #   GetAdminAuditLogs, ExportAdminAuditLogs)
└── data_analytics.go         # 统计/分析数据 (各种 COUNT/SUM 聚合查询)
```

**注意事项：**
- `data.go` 保留所有共享的 DTO struct 定义
- `csvEscape()` 等通用辅助函数保留在 `data.go`

---

### 2.4 `internal/web/admin_handlers.go` (2,320 行 → 目标：每文件 < 600 行)

**当前问题：** HTMX Admin 页面 handlers，涉及 Model/Provider/Key/Plan/User 等各种操作，与 `routes/admin.go` 有大量功能重叠但面向不同客户端（HTMX vs REST API）。

**拆分目标文件：**

```
internal/web/
├── admin_handlers.go         # 保留：通用辅助 + Handler 注册
├── admin_handlers_users.go   # 用户相关 (HandleAdminUserDetail, HandleImpersonateUser)
├── admin_handlers_models.go  # Model 管理 (HandleCreateModel, HandleToggleModel,
│                           #   HandleApproveModel)
├── admin_handlers_keys.go    # Key 管理 (HandleAddUpstreamKey, HandleDeleteUpstreamKey,
│                           #   validateUpstreamKeyAsync)
├── admin_handlers_providers.go # Provider 管理 (HandleAddProvider, HandleEditProvider,
│                             #   HandleUpdateProviderPriority)
└── admin_handlers_plans.go   # 订阅计划 (HandleCreatePlan, HandleTogglePlan, HandleGetPlanDetail)
```

**注意事项：**
- `validateUpstreamKeyAsync()` 与 `routes/admin.go` 中的 `validateKeyAsync()` 有重复逻辑，考虑提取到共享服务
- `getProviderDefaultURL()` 可移至 `pkg/utils/providers.go`

---

### 2.5 `internal/routes/models.go` (2,331 行 → 目标：每文件 < 600 行)

**当前问题：** 包含 Service Model CRUD、EOL 生命周期管理、Provider 路由、Model 审批、OpenRouter 同步等。

**拆分目标文件：**

```
internal/routes/
├── models.go                 # 保留：NewModelRoutes + Register + 列表查询
├── models_crud.go            # CRUD 操作 (create/update/delete ServiceModel)
├── models_lifecycle.go       # EOL 生命周期 (setEOLPending, updateEOL, cancelEOL,
│                           #   confirmEOL, discontinue, toggleHidden, pause/resume)
├── models_routing.go         # Provider 路由 (modelProviderRouting, updateRouting)
└── models_sync.go            # 外部同步 (syncOpenRouterModels, importModelsFromCSV)
```

**注意事项：**
- `listPublicModels()` 和 `listServiceModels()` 等列表查询保留在 `models.go`
- OpenRouter 同步逻辑较重，独立文件便于维护

---

### 2.6 `internal/routes/webhook.go` (1,770 行 → 目标：每文件 < 600 行)

**当前问题：** 虽然只有 25 个函数，但 `processRecord` (~435 行) 和 `processBillingBatch` (~256 行) 两个核心计费函数占据了近 40% 的代码量。

**拆分目标文件：**

```
internal/routes/
├── webhook.go                # 保留：NewWebhookRoutes + Register + 共享辅助
├── webhook_handlers.go       # HTTP Handlers (handleUsage, handleUsageBatch,
│                           #   handleUpstreamUsageBatch, handleKeyPoolStatus,
│                           #   handleHeartbeat, handleProviderHealthBatch)
├── webhook_billing.go        # 计费核心 (processRecord, processBillingBatch,
│                           #   calculateCostFromPricing, getModelPricing,
│                           #   getPricingRatio, logBalanceDeductions,
│                           #   checkEnterpriseLimits, sendEnterpriseAlert,
│                           #   sendEnterpriseAlertEmail)
└── webhook_queue.go          # 队列处理器 (StartQueueProcessor, processStatsQueue,
                            #   processBillingQueue, upsertUpstreamKeyUsage)
```

**注意事项：**
- `processRecord` 和 `processBillingBatch` 是 D3 对账的核心，拆分后便于后续统一计费路径
- `logBalanceDeductions()` 和 `checkEnterpriseLimits()` 随计费逻辑迁移

---

### 2.7 `cmd/server/main.go` (647 行 → 目标：每文件 < 300 行)

**当前问题：** D1 原始目标。`main()` 约 440 行，包含路由注册、中间件挂载、后台 goroutine 启动、CORS 配置、限流器初始化、默认管理员创建等。

**拆分目标文件：**

```
cmd/server/
├── main.go                   # 保留：main() 入口 + config 加载 + DB 初始化
├── routes.go                 # 路由注册 (setupRoutes, 各模块路由组的注册逻辑)
├── middleware.go             # 中间件 (corsMiddleware, authMiddleware, rateLimitMiddleware,
│                           #   csrfMiddleware)
├── background.go             # 后台任务 (startBackgroundJobs, enterpriseReminder,
│                           #   modelLifecycleWatcher, keyValidationWorker, autoRenewalWorker)
└── init.go                   # 初始化逻辑 (initCoreTables, initDefaultAdmin, initRateLimiter)
```

**注意事项：**
- `safeGo()` 辅助函数移至 `background.go`
- `newIPRateLimitMiddleware()` 移至 `middleware.go`

---

## 三、Proxy 拆分方案

### 3.1 `pkg/keypool/manager.go` (2,259 行 → 目标：每文件 < 600 行)

**当前问题：** 65 个函数，涵盖密钥池生命周期管理、模型路由解析、密钥选择策略、健康检查、配额统计、Proxy 信息管理等 6+ 个独立领域。

**拆分目标文件：**

```
pkg/keypool/
├── manager.go                # 保留：NewManager, SyncFromPortal, 生命周期管理
├── routing.go                # 路由解析 (ResolveUpstreamModelName, GetRoutingOrder,
│                           #   GetSmartRouting, updateModelRouting, updateModelNameMap)
├── selection.go              # 密钥选择 (GetKey, GetKeyWithContext, GetKeyByPriority,
│                           #   KeyHasRPMBudget, GetProviderConfig)
├── health.go                 # 健康检查 (checkUpstreamAvailable, checkUpstreamTPMOverflow,
│                           #   RecordUpstreamRequest, RecordUpstreamTokens)
├── stats.go                  # 统计信息 (GetAllStats, GetProviderHealth, GetKeyHealth)
└── proxy.go                  # Proxy 元数据 (getProxyID, getProxyRegion, SetProxyInfo)
```

**注意事项：**
- `Manager` struct 保留在 `manager.go`，其他文件通过方法接收器扩展
- `providerLock` 等内部类型保留在 `manager.go`

---

### 3.2 `internal/server/handlers.go` (2,015 行 → 目标：每文件 < 600 行)

**当前问题：** 39 个函数，涵盖 OpenAI relay、Token usage 记录、Upstream usage 记录、Provider health 记录、Heartbeat 处理等。

**拆分目标文件：**

```
internal/server/
├── handlers.go               # 保留：HandlerDependencies, Set*Dependencies 辅助函数
├── handlers_relay.go         # OpenAI Relay (ChatCompletion, Streaming, Error handling)
├── handlers_usage.go         # Usage 记录 (recordUsageAsync, recordTokenUsageAsync,
│                           #   recordUpstreamUsageAsync)
├── handlers_health.go        # Health 记录 (recordProviderHealthAsync, checkAllowedModels)
└── handlers_middleware.go    # Handler 辅助 (getUserPriority, model validation helpers)
```

**注意事项：**
- `UsageRecordContext` 等共享类型保留在 `handlers.go`
- `SetUsageBuffer` / `SetUpstreamUsageBuffer` / `SetProviderHealthBuffer` 等依赖注入辅助函数保留在 `handlers.go`

---

## 四、实施优先级与风险评估

### 4.1 实施顺序建议

| 优先级 | 文件 | 理由 |
|--------|------|------|
| **P0-1** | `cmd/server/main.go` | D1 已承诺，功能边界最清晰，零业务风险 |
| **P0-2** | `pkg/keypool/manager.go` (Proxy) | 纯函数拆分，无路由/API 变更，测试覆盖好即可 |
| **P0-3** | `internal/routes/webhook.go` | D3 对账的前置条件，计费逻辑集中便于后续统一 |
| **P1-1** | `internal/routes/admin.go` | 最大文件，拆分后各模块可独立演进 |
| **P1-2** | `internal/routes/enterprise.go` | 企业功能持续迭代中，拆分可降低冲突 |
| **P1-3** | `internal/web/data.go` | 纯数据层，拆分风险低 |
| **P2-1** | `internal/web/admin_handlers.go` | HTMX 层，可与 `admin.go` REST 层对应拆分 |
| **P2-2** | `internal/routes/models.go` | Model 生命周期功能趋于稳定 |
| **P2-3** | `internal/server/handlers.go` (Proxy) | Relay 逻辑稳定后可拆 |

### 4.2 通用拆分原则

1. **保持接口不变**：拆分仅移动代码，不修改函数签名或路由路径
2. **先新增后删除**：新文件创建并验证编译通过后，再从原文件删除
3. **逐个文件拆分**：避免一次性修改过多文件导致 review 困难
4. **测试先行**：确保被拆分文件有充分测试覆盖后再动手
5. **保留原文件作为入口**：原文件保留 struct 定义和 `Register()` 方法，新文件作为功能子模块

### 4.3 风险提示

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 循环导入 | 🔴 高 | 拆分前梳理 import 图，共享类型提取到 `pkg/types/` 或 `internal/common/` |
| 方法接收器跨文件 | 🟡 中 | Go 允许同一包内跨文件定义方法，只需确保在同一 package |
| 测试覆盖不足 | 🟡 中 | 拆分前补充测试，拆分时保持测试同步移动 |
| Git 历史断裂 | 🟢 低 | 使用 `git mv` + `git commit` 分步操作，保持 blame 可追溯 |

---

## 五、与 Code Review 关联

本文档是 Code Review 中 **D1（main.go 拆分）** 问题的扩展。原始 D1 仅关注 `cmd/server/main.go`，但分析发现代码库中存在**数倍于 main.go 规模**的超大文件，因此将 D1 升级为**全局代码库瘦身计划**。

关联文档：
- `docs/code-review-report-202605261540.md` — 原始 Code Review 报告
- `docs/code-review-verification-response-202605291500.md` — 修复验证响应（含 D1 状态更新）
