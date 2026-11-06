# Portal 老 Web 全量盘点与迁移审计（2026-11-06）

> 目的：UI 切换时"判断逻辑 + JS 行为"零遗漏；并给出信息架构重构意见。
> 范围：`ai-portal-go/web/templates/` 51 模板 + `internal/web` 35 个页面 handler + `web/static/js` 自定义脚本 1394 行。

## 一、必须移植的共享行为（JS 层）

来自 `app-common.js`(764行) / `app-layout.js`(534行) / `table-pagination.js`(96行)：

| 行为 | 函数 | React 落点 |
|---|---|---|
| 复制+回退+成功提示 | copyText/fallbackCopyText/showCopyOK | 已有 CopyButton，统一替换 |
| 数字/配额/token/延迟/百分比格式化 | fmtNum/fmtTokens/fmtLatency/fmtPercent/formatQuota | `src/utils/format.ts` 补齐并全站统一 |
| 导出下载（Blob/saveHandle） | downloadExport/saveBlobAs/pickSaveHandle | `utils/download.ts`（Keys/Logs 导出用） |
| 状态/类型徽章生成 | badge/statusBadge/typeBadge + chartColor | 统一 `components/StatusBadge.tsx` + 图表色板常量 |
| Toast/Modal/Confirm 全局 | showToast/showConfirm/showMessage | Semi Toast/Modal 已具备，定义统一封装 |
| 右侧滑出面板 | openRightPanel | 与 Drawer 体系合并 |
| 主题同步/时区初始化 | applyTheme/syncTheme/initTimezone | 已有主题；时区放 App 启动 effect |
| proper-nouns 名词本地化 | loadProperNouns/_t | 需评估：若老站对模型名做了中英映射表(proper-nouns.json)，React 侧要等价实现 |
| 表格客户端分页 | table-pagination.js | 全部改服务端分页（Semi Pagination） |

## 二、页面清单与迁移状态

图例：✅React 已完成 🔄P2.5 改造中 ⬜待做 ❓建议合并/重构掉

### 公开页（5）
| 路由 | 模板 | 状态 |
|---|---|---|
| / | landing.html | ✅ |
| /models | models-gallery.html | ✅ |
| /login /register /forgot-password | 对应 3 模板 | ✅/✅/⬜( forgot-password 未做，接 /api/auth/forgot+reset) |

### 用户控制台（17 → 建议收敛为 8）
| 路由 | 模板 | 内嵌逻辑要点 | 状态/建议 |
|---|---|---|---|
| /dashboard | dashboard.html (条件12) | 按角色显隐卡片、图表初始化 | ✅ |
| /api-keys | apikeys.html (条件41! script2) | 创建抽屉、复制、启用禁用、行内操作 | ✅ (P2-C) |
| /usage-logs | usage-logs.html | 筛选+导出 | ✅ (P2-D) |
| /billing + /billing/details + /invoices | 3 模板 (条件11/3/3) | 月度账单、明细、发票下载 | ⬜ ❓合并为一个「账单」页 Tabs（账单/明细/发票） |
| /voucher | voucher.html | 兑换+流水 | ⬜ ❓并入账单页 Tab |
| /subscriptions + /my-subscriptions | 2 模板 | 订阅计划列表 vs 我的订阅 | ⬜ ❓合并「订阅」页两个 Tab |
| /plan-and-group + /my-group | 2 模板 (条件21/20) | 计划购买、组限额/成员视角 | ⬜ ❓合并「分组与计划」 |
| /status | status.html | 上游状态展示 | ⬜ ❓可作 Dashboard 组件或独立轻页 |
| /notifications | notifications.html | 通知列表（刚被 upstream 修过样式） | ⬜ 独立页保留 |
| /profile | profile.html (条件8) | 资料/改密/时区偏好 | ⬜ 独立页保留 |
| /support | support.html | 工单提交 | ⬜ 保留（对接 tickets API） |
| /upstream-ops | user-upstream-ops.html | 用户视角上游操作 | ⬜ ❓冷门页，建议评估使用频率后并入日志页 |

### 企业管理台（enterprise/ 11 子模板）
| 模板 | 状态/建议 |
|---|---|
| dashboard/users/member-detail/groups | ⬜ 核心组，保留 |
| keys/key-detail | ⬜ 保留；key-detail 的明细与日志可用 Drawer 替代独立页 |
| usage/billing/audit/alerts/settings | ⬜ 保留；audit+alerts 可合成「安全与审计」 |

### 管理后台（17）—— ⚠️ 用户定调：**system admin 与 enterprise admin 分两套界面、两套数据，禁止共用页面**
- **Enterprise admin（客户侧，P3-E 优先）**：数据全部来自 `/api/enterprise*`（requireEnterpriseAccess 天然隔离本企业数据）。页面：企业概览(E1)/成员(E2，含邀请审批+配额+导入导出)/分组(E3 CRUD)/密钥(E4 归属成员视角)/用量(E5 五页签)/账务(E6 对账)/设置(E7+变更请求)。后端已齐备（见 enterprise_common.go 路由表）
- **System admin（平台运营侧，P3-A 后续）**：`/console/admin/*`，导航：概览 · 用户与企业 · 分组与计划 · 模型与定价(general+aws+pricing 合一) · 上游(providers+keypool+monitor) · 财务(subscriptions+vouchers+reconciliation) · 运营(tickets+announcements) · 安全与合规(audit+compliance) · 设置。数据 = 全平台
- 老模板映射：admin*.html(17) → P3-A；enterprise/*.html(11) → P3-E；旧 plan "按权限过滤菜单共用页面" 方案作废

### 页面合并讨论（用户指示押后一轮）
upstream-monitor / downstream-monitor / upstream-ops 等的整合方案暂不执行，待专项讨论。

## 三、重构意见（老布局不合理处）

1. **用户侧钱相关 6 页收敛为 1 页 4 Tab**：billing/billing-details/invoices/voucher/subscriptions/my-subscriptions 信息架构碎片化，新站「账单中心」一个入口。
2. **plan-and-group 与 my-group 合并**：同一对象两个视角，用 Tab 切换即可。
3. **模型管理 3 页合一**：admin-models-general/aws/pricing 都是运营模型，合并「模型与定价」用页签切换 general/aws 视图。
4. **上游 3 页靠拢**：providers/keypool/monitor-downstream 合并「上游管理」，monitor 作总览组件。
5. **key-detail 独立页 → Drawer**：键详情用右侧抽屉替代页面跳转（老站 20+ 个页面里跳转链最深的一处）。
6. **Enterprise 与 Admin 导航合并分组**：P3 统一 `/console/admin/*`，按权限过滤菜单，不再两套布局。

## 四、切换检查单（届时逐项打勾）

- [ ] proper-nouns.json 名词映射评估（React 等价实现或放弃）
- [ ] 每个 ⬜ 页面完成 + 对应老 API 联通验证
- [ ] 导出下载（saveBlobAs 的 File System API 回退）在新站可用
- [ ] 时区/主题偏好持久化逻辑移植
- [ ] SEO：landing/models 的 JSONLD/SSR meta 用 prerender 或保留轻量 SSR 注入
- [ ] 组新工程（portal+proxy+web+agent）后老仓库冻结

## 五、后续阶段事项（用户指示，2026-11-07）

- [ ] **Usage/账目细化**：企业用量与账务页按具体业务逐页比对老 portal 页面再细化（用户：等具体页面实施时一一比对）
- [ ] **Enterprise management token**：企业级管理 API token（类似 TR 的 mgmt key）
- [ ] **企业 SSO**：企业客户单点登录接入
- [ ] **企业分组免绑定自建**：当前后端 createEnterpriseGroup 强制 platform_group_id binding——企业完全自建分组（不继承系统组）需后端放开或加"自定义组"通道
- [ ] 企业自助货币充值（prepaid/credit 自助 topup）：当前仅 system admin 的 /api/admin/enterprise/:id/topup 可充三池（quota_total/credit_total/prepaid_balance）
