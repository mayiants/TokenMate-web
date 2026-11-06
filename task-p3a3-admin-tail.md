# 任务：P3-A3 —— 系统管理后台第三批：财务 + 运营 + 安全合规 + 设置（woodgit-platform/web）

规范与准入同 P3-A1 任务书。导航追加本批四项（财务/运营/安全合规/设置），「平台管理」九项至此齐全。

## A7 财务 `/console/admin/finance`（合并 subscriptions 管理/vouchers/reconciliation/对账）
- 页签：①订阅管理（全平台订阅记录表格+退款/调整操作，有端点才做操作）②卡券（vouchers：批次列表/生成 Drawer（面额/数量/过期）/停用）③对账（reconciliation：汇总 KPI+差异表+导出 CSV）
## A8 运营 `/console/admin/ops`（tickets + announcements）
- 页签：①工单（全部工单表格+级别筛选；查看 Drawer 消息流+回复+状态流转 close/resolve）②公告（announcements 列表+创建 Drawer（标题/内容/生效期/置顶）+编辑/下架）
## A9 安全合规 `/console/admin/security`（audit-logs + compliance）
- 页签：①审计日志（全平台 audit_logs 表格：时间/操作人/类别/动作/对象/结果+筛选）②合规（compliance 数据：数据保留策略展示/导出记录/GDPR 删除请求列表+执行操作）
## A10 设置 `/console/admin/settings`
- 分区页（DrawerSections 平铺）：平台参数（名称/客服邮箱/注册开关等——admin-settings 源码）/ 费率与计价（pricing_ratio 默认值、汇率）/ 邮件与通知（SMTP 状态展示+测试发送按钮，有端点才接）/ 危险区（缓存清理等维护操作 dangerConfirm）
- 后端：admin-settings.go / admin-compliance.go / admin-audit-logs.go

## 流程
- 每页 commit：`feat(web): P3-A3-...`，WorkBuddy Agent
- 完成 build 零错误 + check:i18n；输出 API 清单+遗留；此批完成后九项导航齐全，输出整个 admin 的 API 对接总表
