# 任务：P3-A1 —— 系统管理后台第一批：骨架 + 总览 + 用户与企业（woodgit-web @ woodgit-platform）

你是资深 React 前端工程师。项目 `/opt/workspace/woodgit-platform/web/`（main）。**本平台主卖企业客户，system admin 与 enterprise admin 是完全分离的两套界面**（用户已定调）。现建系统管理后台 `/console/admin/*`。

规范同 B1 任务书（design-system.md）：PageHead、三段式、条件左 CRUD 右、Drawer CRUD、dangerConfirm、Toast、双 i18n、暗色成对覆盖、图标仅 semi-icons。
**内存红线：不要 `npm run dev`；只用 build + check:i18n 验证。**

## 准入与骨架
- 入口：`GET /api/admin/overview`（或任一 /api/admin/* 200 且 role=system_admin/super_admin）探测；**仅 system_admin/super_admin 可见**（enterprise_admin 和普通用户看不到导航，前端隐藏+后端 403 双重）
- ConsoleLayout 在企业管理台组之后加「平台管理」组（不可点标题+扁平项，同既有模式）：总览/用户/企业/分组与计划/模型与定价/上游管理/财务/运营/安全合规/设置 —— 本批先挂前 3 项，其余项由后续批次加（先把导航项放全但 disable 或只加本批的，建议只加本批三项，后续批次追加）
- AdminLayout：`/console/admin` 包裹 AdminLayout（探测权限，非管理员 Empty+引导）；面包屑可省（PageHead 承担）

## A1 总览 `/console/admin`
- PageHead「平台总览」
- KPI 卡组（8 横排或 2×4）：总用户/企业数/今日请求/今日消费/活跃密钥/上游数/待审工单/待审企业申请（端点：/api/admin/overview 或组合 /api/admin/* 现有统计端点——读 portal 源码 admin*.go 确认）
- 图表卡：近 30 天消费趋势 + 请求趋势（复用 usage-trend 类端点的全局版本，源码找 admin dashboard 数据源）
- 待办卡：待审变更请求/待审工单/将过期上游 key 三个小列表（有端点就接，无则跳过该块）

## A2 用户 `/console/admin/users`
- 筛选卡（邮箱/姓名/角色/状态/企业）+ 表格（ID/姓名/邮箱/角色药丸/状态药丸/企业/余额/创建时间/操作）
- 行操作：编辑 Drawer（姓名/角色 Select/状态 Toggle）、重置密码（dangerConfirm→POST 源码里 send-password-reset 端点）、删除（dangerConfirm）
- 工具条右：「创建用户」Drawer（邮箱/姓名/密码或邀请）
- 后端：admin_users.go + admin_users_matrix（字段矩阵）——以源码为准

## A3 企业 `/console/admin/enterprises`
- 筛选卡（名称/状态/认证）+ 表格（名称/联系邮箱/成员数/额度池(已用/总)/状态药丸/已认证 Tag/创建时间/操作）
- 行操作：编辑 Drawer（名称/联系人/最大成员数）、充值（Drawer：quota/credit/prepaid 三池+金额+备注+issue_invoice 开关——对应 POST /api/admin/enterprise/:id/topup，**字段以源码为准**）、暂停/恢复/认证（dangerConfirm 或主按钮）、删除（dangerConfirm）
- 头部主按钮「创建企业」Drawer
- 页签：企业列表 / 变更请求（change-requests 列表+批准/拒绝）/ 邀请（invitations 列表+撤销）
- 后端：enterprise_common.go 的 admin 组 + enterprise_application*.go

## 流程
- 每页 commit：`feat(web): P3-A1-...`，WorkBuddy Agent 身份
- 完成后 build 零错误 + check:i18n；输出 API 清单+遗留
