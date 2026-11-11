# TokenMate-web 对齐老 Portal 的缺口清单与补齐记录（2026-11-11）

> 对照基线：`ai-portal-go/web/templates`（51 个 SSR 模板）+ `internal/web/pages.go` 路由表
> 目标工程：本仓库 `web/`（React SPA，TokenMate 默认仍走 portal SSR，切换前需功能对齐）

## 一、本轮已补齐（可构建通过）

| 老 Portal 能力 | 老入口 / API | 新实现 | 说明 |
|---|---|---|---|
| 登录二次验证（TOTP 挑战） | `/login/mfa` · `POST /api/auth/login/mfa` | `src/pages/auth/Login.tsx` 两步式登录 + `src/api/auth.ts` | 密码通过后 `mfa_required` 返回 `mfa_token`，页面切到动态码/恢复码校验；不再跳 `/login/mfa` 独立页 |
| 个人两步验证管理 | `profile.html` · `/api/profile/mfa/{status,setup,enable,disable,recovery-codes/regenerate}` | `src/pages/console/MfaCard.tsx` + `src/api/profile.ts` | 启用（密钥 + otpauth URI + 校验码）、恢复码展示/复制、重新生成、停用；token_version 轮换后自动替换本地 token |
| 管理端用量探索 | `/admin/usage-explorer` · `/api/admin/usage-explorer/{aggregate,logs,export/*}` | `/console/admin/usage-explorer` · `src/pages/admin/UsageExplorer.tsx` · `src/api/adminExplorer.ts` | 6 维度聚合（user/enterprise/model/key/endpoint/subscription_plan）+ 时间窗/自定义区间 + KPI + 下钻明细分页 + CSV 导出 |
| 管理端限流与容量 | `/admin/rate-limits` · `/api/admin/rate-limits/*` | `/console/admin/rate-limits` · `src/pages/admin/RateLimits.tsx` · `src/api/adminRateLimits.ts` | 用户配额 / 分组 / Key 生效限额 / 上游 Key / 容量 5 个页签，窗口 1h·24h·7d，按维度导出 CSV |

配套改动：`src/App.tsx` 路由、`src/layouts/ConsoleLayout.tsx` 导航（平台管理组新增两项）、`src/i18n/locales/{zh,en}.json`（1712 键，`node e2e/check-i18n.cjs` 通过）。

## 二、仍未对齐（按优先级）

### P1 — 运营/财务强相关

1. **上游定价（upstream pricing）**
   老页：`admin-upstream-pricing.html`；API：`GET/PUT /api/admin/upstream-pricing`、`POST /api/admin/upstream-pricing/:id/generate-downstream`、`GET export/csv`、`POST import/csv`。
   现状：新站只有「下游定价规则」（`/console/admin/models` 的 pricing 页签），缺上游目录价与生成下游价的链路。
2. **对账（用户/企业账单核对 + 发票请求）**
   老页：`admin-reconciliation.html`；API：`/api/admin/reconciliation/{user,enterprise,top-users,top-enterprises,ledger}`、`/api/admin/invoice-requests`（含 external 发票）。
   现状：新站 `finance` 页签只做了 `reconciliation-ops`（上游成本差），缺用户/企业侧账单核对与发票处理。
3. **企业入驻申请审批**
   老页：`admin-enterprise-applications.html`；API：`GET /api/admin/enterprise-applications`、`GET :id`、`POST :id/{approve,reject,request-info}`、`GET :id/documents`、`GET :id/documents/:docId/download`。
   现状：`api/admin.ts` 只有 `listEnterpriseApplications`（总览页待办用），无审批页与附件下载。

### P2 — 少量页面/入口

4. **下游密钥总览**：`admin-keys.html` · `GET /api/admin/downstream-keys?page=`（老站独立页，新站仅在用户页签内看密钥）。
5. **支付网关配置（super_admin）**：`super/payment.html` · `GET/PUT /api/admin/payment/gateways/*`、`POST /api/admin/settings/payment_simulation_mode`。
6. **超管账号管理页**：`super/admin-management.html` · `/api/admin/users*` + SMTP 配置（新站已把 SMTP 放进设置页，账号部分仅有用户管理页，super 专属动作未单列）。
7. **企业自助申请入驻**：`enterprise-apply.html` · `/api/enterprise-application`（提交/查询申请、附件上传）。
8. **企业邀请接受页**：`enterprise-accept.html` · `GET/POST /api/enterprise/accept?token=`（未登录可访问的接受落地页；新站仅登录后在成员页审批）。
9. **通知渠道与偏好**：老 `profile.html` 的 `/api/notifications/channels`、`/api/notifications/preferences`（邮件/站内开关），新站通知页只有列表与已读。

### P3 — 已判定可收敛（无需补页）

- `user-upstream-ops.html` → 由 `/console/status` 覆盖同一 `GET /api/status/upstream?period=`。
- `admin-monitor-upstream/downstream` → 已并入 `/console/admin/upstream` 监控页签（仅缺下游导出按钮）。
- `my-subscriptions / my-group / voucher / invoices / billing-details` → 已按信息架构合并到订阅、账单两个页面。

## 三、下一步建议顺序

1. 上游定价（数据链路上下游一体，缺它时定价只能手工同步）
2. 企业入驻申请审批（有真实申请就会卡流程）
3. 对账 + 发票请求（财务月度必用）
4. 企业自助申请 / 邀请接受落地页（对外可用性）
5. 支付网关、下游密钥、通知偏好（低频，随版本补齐）
