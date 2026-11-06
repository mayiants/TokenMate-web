# 任务：B2 —— 企业管理台补齐审计/告警两页（woodgit-web @ woodgit-platform）

你是资深 React 前端工程师。项目 `/opt/workspace/woodgit-platform/web/`（main）。企业管理台 `/console/enterprise/*` 已有七页（概览/成员/分组/密钥/用量/账务/设置），本任务补最后两页。规范与 B1 任务书相同（design-system.md §1-§3.1：PageHead、三段式、Drawer CRUD、dangerConfirm 居中确认、Toast 反馈、双 i18n、`body`+`body[theme-mode='dark']` 成对覆盖）。

**内存红线：不要 `npm run dev`；只用 `NODE_OPTIONS=--max-old-space-size=1024 npm run build` + `npm run check:i18n`。**

## E8 审计 `/console/enterprise/audit`
- 后端：读 `/opt/workspace/woodgit-platform/portal/internal/routes/enterprise_*.go` 找企业审计查询端点（enterprise_audit_query_test.go 暗示存在审计查询接口；logEnterpriseAudit 写入的 enterprise_auditEntry 是数据源）。若仅有平台级 /api/admin/audit-logs 而企业自查端点缺失，则用企业侧可用端点（如 reconciliation/member-logs）组合，并在报告中声明缺口——**不得伪造数据**。
- 页面：筛选卡（时间范围/操作人/类别/变更类型）+ 表格（时间/操作人/类别 badge/变更字段/旧值→新值/原因）+ 分页 + 导出（有 export 端点则接）。

## E9 告警 `/console/enterprise/alerts`
- 后端：enterprise_alerts.go（读源码确认端点：列表/规则/订阅）。
- 页面：上半部分=告警规则卡片列表（规则名/条件摘要/启用 Switch/编辑 Drawer）；下半部分=近期告警事件表格（级别药丸 info/warning/critical/时间/内容/已读）。创建/编辑规则走 Drawer；删除规则 dangerConfirm。

## 导航与流程
- ConsoleLayout 的 ENT_ITEMS 加两项（audit/alerts），图标 IconShieldStroked/IconBellStroked（以 semi-icons 实际导出为准），选中态最长路径优先已兼容
- 每页一个 commit：`feat(web): B2-...`，身份 WorkBuddy Agent
- 完成后 build 零错误 + check:i18n；输出 API 对接清单+遗留
