# 任务：B1 —— 用户控制台补齐七页（woodgit-web @ woodgit-platform）

你是资深 React 前端工程师。项目 `/opt/workspace/woodgit-platform/web/`（React 18 + Semi UI + Vite + TS，main 分支）。现有：公开页 + 认证 + 用户四页（仪表盘/API 密钥/用量日志/余额）+ 企业管理台七页，全部走真实 API、通过 i18n(585键)/console/axe/responsive 门禁。

**内存红线：不要 `npm run dev`；验证只用 `NODE_OPTIONS=--max-old-space-size=1024 npm run build` + `npm run check:i18n`。**

## 设计基线（必须遵守，见 web/docs/design-system.md §1-§3.1）
PageHead 置顶（20px/600，仅仪表盘有副标题）；Semi Pro 三段式；条件控件左/CRUD 右（创建蓝、删除红）；**一切 CRUD 用 Drawer**；危险确认用 `dangerConfirm()`（居中红钮，src/components/dangerConfirm.tsx）**禁止 Popconfirm/原生弹窗**；轻反馈 Toast；图标仅 semi-icons（禁 emoji 图标）；所有 `--semi-*` 覆盖成对写 `body` 和 `body[theme-mode='dark']`；文案全 t() 双份。

## 侧栏
ConsoleLayout 在用户四项后加第二组扁平节（与企业管理台同模式：不可点标题项 + 顶层项，无嵌套组）：
- 账户（acc-header）：资料 `/console/profile`、通知 `/console/notifications`、工单 `/console/support`、账单中心 `/console/billing`、订阅 `/console/subscriptions`、分组与计划 `/console/groups`、服务状态 `/console/status`
（selectedKey 最长路径优先匹配已就位，仿 ENT_ITEMS 加 ACC_ITEMS）

## 七页（后端源码 `/opt/workspace/woodgit-platform/portal/internal/routes/`，响应结构以源码为准）

### P1 资料 `/console/profile`
- PageHead「资料」；卡片分区（DrawerSections 风格但页内直接分区）：
  - 基本资料：姓名修改（找 profile.go 的 GET/PUT 端点，通常是 /api/profile 或 /api/user/profile——先读源码确认路径与字段）
  - 安全：修改密码（POST /api/auth/change-password {current_password? 或 old_password?, password}——字段以源码为准），成功后强制重新登录（清 token 跳 /login）
  - 只读展示：邮箱、注册时间、角色

### P2 通知 `/console/notifications`
- 列表页：筛选（类型/已读状态）+ 表格（标题/内容摘要/时间/已读状态药丸）+ 行操作「标记已读」（若源码有 mark-read 端点；没有再就只读列表）+ 空态。参考 notifications.go。

### P3 工单 `/console/support`
- 页签：我的工单 / 新建工单
- 列表：编号/标题/状态药丸(open/pending/resolved)/更新时间/操作「查看」
- 查看 = Drawer：消息流（滚动区）+ 底部回复框（POST /api/tickets/:id/messages）；新建 = Drawer 表单（标题/内容/附件可选——有 staging 接口就加 Upload，没有则纯文本）。参考 tickets.go + tickets_staging.go。

### P4 账单中心 `/console/billing`（合并老 billing-details + invoices + voucher 三页）
- 顶部统计小卡组（现金/券/累计充值，复用现有 api/console 的 balance 接口）
- Tabs：①余额流水（/api/balance-transactions）②充值记录（/api/recharge-records）③发票（invoices.go 的用户侧列表端点）④券（voucher：兑换码输入+Redeem 按钮 POST /api/voucher/redeem + 券流水列表）
- 页签内统一表格卡，导出按钮走对应 export 接口（有则用）

### P5 订阅 `/console/subscriptions`（合并 subscriptions + my-subscriptions）
- Tabs：平台计划（可订阅列表卡片：名称/价格/额度/模型数/「订阅」主按钮——订阅动作若有端点则接上，没有则按钮 disabled+Tooltip 后续接入）/ 我的订阅（列表：计划/生效时间/到期/状态药丸）

### P6 分组与计划 `/console/groups`（合并 plan-and-group + my-group）
- 我的分组卡片（当前用户所在组：名称/限额 RPM·TPM/模型数，来源 /api/...groups 相关用户侧端点——读源码）
- 平台公开分组列表（/api/...groups/public 或 models/grouped/public，只读展示+「加入/购买」占位 disabled）

### P7 服务状态 `/console/status`
- 卡片：各上游/模型状态表（/api/models/status 或源码里 status 相关端点：模型名/状态药丸(正常/降级/故障)/延迟/更新时间）+ 整体状态横幅（全部正常=绿色横幅）。参考老模板 status.html 的数据源。

## 流程
- 依赖已装好（npm install 由协调者完成）；边做边 `npx tsc --noEmit` 自查
- 每完成 2-3 页 commit 一次：`feat(web): B1-X ...`，身份 WorkBuddy Agent，精确 add
- 七页完成后：`NODE_OPTIONS=--max-old-space-size=1024 npm run build` 零错误 + `npm run check:i18n` 通过
- 最终输出：七页 API 对接清单（端点→字段映射）+ 遗留问题
- 只改 web/src 内文件；不 push
