# 老 Portal 页面 → 新 UI 覆盖对照（2026-11-07 切换评估）

> 老 portal 51 个 SSR 模板 vs 新 React UI（woodgit-platform/web）。
> 结论：**功能全覆盖，可切换**；遗留增强项见文末。

## 公开/认证（5/5）

| 老页面 | 新页面 | 说明 |
|---|---|---|
| landing / login / register / forgot-password / models-gallery | `/` `/login` `/register` `/forgot-password` `/models` | 全部重建，TR 对标风格 |

## 用户控制台（老 17 页 → 新 11 页）

| 老页面 | 新页面 | 处理方式 |
|---|---|---|
| dashboard | `/console` | 重建（KPI+图表+问候） |
| apikeys | `/console/keys` | 重建（领域模型创建流） |
| usage-logs | `/console/logs` | 重建 |
| billing + billing-details + invoices + voucher | `/console/billing` 账单中心 | **4 页合并**：余额流水/充值记录/发票/券 4 页签 |
| subscriptions + my-subscriptions | `/console/subscriptions` | **2 页合并**：平台计划/我的订阅 |
| plan-and-group + my-group | `/console/groups` | **2 页合并**：我的分组+公开分组 |
| status | `/console/status` | 重建（模型状态表） |
| profile | `/console/profile` | 重建（资料+改密强制重登） |
| notifications | `/console/notifications` | 重建（标记已读/全部已读） |
| support | `/console/support` | 重建（附件 staging 支持） |
| user-upstream-ops | （并入 logs/keys 详情） | 收敛掉，入口合并 |

## 企业管理台（老 11 页 → 新 9 页）

| 老页面 | 新页面 |
|---|---|
| enterprise/dashboard·users·groups·keys·usage·billing·settings | `/console/enterprise/*` 七页全部重建 |
| enterprise/audit | `/console/enterprise/audit` 重建 |
| enterprise/alerts | `/console/enterprise/alerts` 重建 |
| key-detail / member-detail 独立页 | 并入 Drawer（不再跳页） |

## 系统管理后台（老 17 页 → 新 9 页，八组导航）

| 老页面 | 新页面 | 处理 |
|---|---|---|
| admin（总览） | `/console/admin` | 重建 |
| admin-users | `/console/admin/users` | 重建 |
| admin-enterprise | `/console/admin/enterprises` | 重建（含充值三池/变更请求页签） |
| admin-groups / admin-subscriptions / admin-rate-limits | `/console/admin/groups` | **合并**为用户组+计划页签 |
| admin-models-general / -aws / -pricing | `/console/admin/models` | **合并**三页签 |
| admin-providers / admin-keypool / admin-monitor-downstream | `/console/admin/upstream` | **合并**三页签 |
| admin-subscriptions管理 / admin-vouchers / admin-reconciliation | `/console/admin/finance` | **合并**三页签 |
| admin-tickets / announcements | `/console/admin/ops` | **合并**两页签 |
| admin-audit-logs / admin-compliance | `/console/admin/security` | **合并**两页签 |
| admin-settings | `/console/admin/settings` | 重建（分区页+危险区） |
| admin-keys / admin-invitations | 并入对应页页签/操作 | 收敛 |
| admin-monitor-upstream / upstream-ops | 并入 `/console/admin/upstream` 监控页签 | 合并讨论时用户确认 |

## 切换与回退

- portal `webui` 嵌入 SPA；`LEGACY_WEB_UI=true` 一键回退旧 SSR
- 切换操作：老容器停服保留 → platform `portal/start.sh rebuild`（自动快照）→ nginx 443 不变

## 遗留增强项（不阻塞切换）

1. 充值支付集成（Top-up 按钮占位，等支付通道）
2. 订阅「立即订阅」动作（计划订阅端点确认后接）
3. admin 仪表盘部分待办卡片依赖 change-requests/工单统计（有数据即显示）
4. user-upstream-ops 如确认高频使用可单独补页
