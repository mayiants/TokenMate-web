# Admin UI Redesign Detail

> **ACTIVE** — System/Super Admin 后台页面标准化改造。统一为 enterprise page-header + key-stats 模式，核心页面 Drawer 化。

- **Created**: 2026-06-07
- **Updated**: 2026-06-07 (Phase 4a/4b/4c 完成 + 全部 22 页覆盖 + Bugfix 批次)
- **Branch**: `enterprise-cardification`
- **Related**: `ui-enterprise-redesign-detail-202606061621.md`, `ui-improvement-proposal-plan-202606061645.md`, `ui-design-principles-202606051751.md`

---

## 1. 页面全景

### 1.1 完整清单（22 个模板）

| # | 路由 | 模板 | 角色 | Sidebar | 备注 |
|---|------|------|------|---------|------|
| 1 | `/admin` | admin.html | system/super | ✅ Overview | Dashboard 概览 |
| 2 | `/admin/users` | admin-users.html | system/super | ✅ Users | 用户管理 |
| 3 | `/admin/groups` | admin-groups.html | system/super | ✅ Groups | 用户组管理 |
| 4 | `/admin/groups/:id/edit` | admin-group-edit.html | system/super | — | 组编辑子页面 |
| 5 | `/admin/enterprise` | admin-enterprise.html | system/super | ✅ Enterprise | 企业管理 |
| 6 | `/admin/keypool` | admin-keypool.html | system/super | ✅ Key Pool | API 密钥池 |
| 7 | `/admin/models` | admin-models-general.html | system/super | ✅ Models | 通用模型管理 |
| 8 | `/admin/models-aws` | admin-models-aws.html | system/super | ✅ AWS Models | AWS Bedrock 模型 |
| 9 | `/admin/providers` | admin-providers.html | system/super | ✅ Providers | 供应商管理 |
| 10 | `/admin/subscriptions` | admin-subscriptions.html | system/super | ✅ Subscriptions | 订阅计划 |
| 11 | `/admin/redemption` | admin-redemption.html | system/super | ✅ Redemption | 兑换码管理 |
| 12 | `/admin/invitations` | admin-invitations.html | system/super | ✅ Invitations | 邀请码管理 |
| 13 | `/admin/announcements` | admin-announcements.html | system/super | ✅ Announcements | 公告管理 |
| 14 | `/admin/audit-logs` | admin-audit-logs.html | system/super | ✅ Audit Logs | 审计日志 |
| 15 | `/admin/settings` | admin-settings.html | system/super | ✅ Settings | 系统设置 |
| 16 | `/admin/monitor-upstream` | admin-monitor-upstream.html | system/super | ✅ Monitor Upstream | 上行 Key 监控 |
| 17 | `/admin/monitor-downstream` | admin-monitor-downstream.html | system/super | ✅ Monitor Downstream | 下行 Key 监控 |
| 18 | `/admin/upstream-usage` | admin-upstream-usage.html | system/super | ✅ Upstream Usage | 上行使用统计 |
| 19 | `/admin/provider-health` | admin-provider-health.html | system/super | ✅ Provider Health | 供应商健康状态 |
| 20 | `/super/admin-management` | super/admin-management.html | super only | 下拉菜单 | 管理员配置 |
| 21 | `/super/payment` | super/payment.html | super only | 下拉菜单 | 支付配置 |
| 22 | `/super/providers` | super/providers.html | super only | 下拉菜单 | 供应商配置 |

### 1.2 角色权限矩阵

| 角色 | Admin 页面 | Super 页面 | 说明 |
|------|-----------|-----------|------|
| system_admin | ✅ 全部 19 页 | ❌ | 日常管理 |
| super_admin | ✅ 全部 19 页 | ✅ 3 页 | 超级管理员 |
| enterprise_admin | ❌ | ❌ | 仅企业页面 |

---

## 2. 当前页面模式分类

### 2.1 模式 A — `page-header` + `key-stats`（已完成，3 页）

| 模板 | Phase | 说明 |
|------|-------|------|
| admin-keypool.html | Phase 4 ✅ | 4 key-stat-card + drawer create/edit |
| admin-models-general.html | Phase 4 ✅ | 5 key-stat-card + drawer create/edit + bridge JS |
| admin-providers.html | Phase 4 ✅ | 4 key-stat-card + drawer create/edit + priority/delete |

> 结构: `page-header(h1.page-title)` → `key-stats(key-stat-card × N)` → `key-search-bar` → `card(table)`

### 2.2 模式 B — `admin-header` + `stats-grid`（接近，5 页）

| 模板 | 状态 | 说明 |
|------|------|------|
| admin.html | 需对齐 | Dashboard 概览，有 quick-action 按钮 |
| admin-monitor-upstream.html | 需对齐 | 上行 Key 监控，无 modals |
| admin-monitor-downstream.html | 需对齐 | 下行 Key 监控，无 modals |
| admin-upstream-usage.html | 需对齐 | 上行使用统计，无 modals |
| admin-provider-health.html | 需对齐 | 供应商健康，有 detail-modal |

> 结构: `admin-header(admin-header-left+right)` → `stats-grid(stat-card + stat-icon + stat-content)` → 内容区
> 需对齐: `admin-header` → `page-header`, `h1` → `h1.page-title`, `stats-grid` → `key-stats`, `stat-card` → `key-stat-card`, `stat-icon` → `key-stat-icon`

### 2.3 模式 C — `card` + `card-header`（待改造，9 页）

| 模板 | 状态 | Stats? | Modals | Drawer 改造 |
|------|------|--------|--------|------------|
| admin-users.html | 🔄 改造中 | ❌ | user-modal | ✅ add/edit → drawer |
| admin-groups.html | 🔄 改造中 | ✅ (2 stat-card) | create-modal | ✅ add/edit → drawer |
| admin-enterprise.html | 🔄 改造中 | ✅ (4 stat-card) | create/edit/members/topup/detail | ✅ add/edit → drawer |
| admin-subscriptions.html | 🔄 改造中 | ✅ (2 stat-card) | add-plan/edit-plan | ✅ add/edit → drawer |
| admin-redemption.html | ✅ | ✅ (4, API-loaded) | create-code/batch-code/batch-result | — (复杂批量操作) |
| admin-invitations.html | ✅ | ✅ | welcome/settings | — |
| admin-announcements.html | ✅ | ✅ | announcement-modal | — |
| admin-audit-logs.html | ✅ | ✅ (3 stat-card) | log-detail | — (只读) |
| admin-settings.html | ✅ | ❌ | — | — (内联编辑) |
| admin-group-edit.html | 保持 | ❌ (表单页) | — | — (全页表单) |

---

## 3. 改造进度（2026-06-07 最终状态）

### 3.1 全部 22 页完成

| # | 页面 | page-header | key-stats | Add | Edit | Delete |
|---|------|-------------|-----------|-----|------|--------|
| 1 | admin (概览) | ✅ | ✅ | — | — | — |
| 2 | admin-users | ✅ | ✅ | Drawer | Drawer | Modal |
| 3 | admin-groups | ✅ | ✅ | Drawer | Drawer | Modal |
| 4 | admin-group-edit | — | — | — | (合并到 drawer) | — |
| 5 | admin-enterprise | ✅ | ✅ | Drawer | Drawer | Modal |
| 6 | admin-keypool | ✅ | ✅ | Drawer | Drawer | Modal |
| 7 | admin-models | ✅ | ✅ JS | Drawer | Drawer | Modal |
| 8 | admin-models-aws | ✅ | ✅ JS | Drawer | Drawer | Modal |
| 9 | admin-providers | ✅ | ✅ | Drawer | Drawer | Modal |
| 10 | admin-subscriptions | ✅ | ✅ | Drawer | Drawer | Modal |
| 11 | admin-redemption | ✅ | ✅ JS | Drawer | Drawer(批量) | Modal |
| 12 | admin-invitations | ✅ | ✅ | Drawer | — | Modal |
| 13 | admin-announcements | ✅ | ✅ | Drawer | Drawer | Modal |
| 14 | admin-audit-logs | ✅ | ✅ | — | — | — |
| 15 | admin-settings | ✅ | — | — | — | — |
| 16-22 | monitor-* / super | ✅ | ✅ | — | — | — |

### 3.2 关键技术决策

| 决策 | 说明 |
|------|------|
| **models JS 加载** | `PaginatedData.Items` 为 `interface{}`，Go template 无法通过它访问 slice 字段。改用 JS `/api/admin/service-models` 加载表格 + gin.H map 传 stats |
| **单 script 块** | 全部 admin 页面统一单 `<script>` 块，drawer 函数在最前，`null-guard` 保护，消除外部 JS 依赖和函数冲突 |
| **Drawer 互斥** | `openCreateX` 先关 `editX`，`openEditX` 先关 `createX`，避免叠加 |
| **840px 宽 drawer** | groups/enterprise/subscriptions/models 等复杂表单使用 `style="width:840px"` |

### 3.3 已修复 Bug（2026-06-07）

| # | Bug | 文件 | 修复 |
|---|-----|------|------|
| 1 | Template render crash: `.Description` 字段不存在 | admin-providers | 删除 `data-description="{{.Description}}"` |
| 2 | Template render crash: `.TotalPages` 字段不存在 | admin-subscriptions | 删除不存在的 pagination 块 |
| 3 | 多个 `form.submit()` 缺 CSRF token | groups/enterprise/keypool/providers/models-aws | 补 `meta[name="csrf-token"]` |
| 4 | `toggleGroup` 缺 CSRF | admin-groups | 补 CSRF input |
| 5 | `confirmActivate`/`confirmDelete` 缺 CSRF | admin-enterprise | 补 CSRF input |
| 6 | 时间戳显示 `06072026133142` | admin-announcements | `fmtTs()` 替代 `formatDateTime()` |
| 7 | subscriptions drawings 叠加 | admin-subscriptions | 重写 drawer 逻辑，互斥开关 |
| 8 | subscriptions 外部 JS 函数冲突 | — | 删除 `page-admin-subscriptions.js` |
| 9 | `POST /api/admin/users` 404 | admin.go | 添加 `createUser` handler |
| 10 | Models 表格空白 (interface{} bug) | admin-models-general | JS `/api/admin/service-models` 加载 |
| 11 | Models AWS 表格空白 | admin-models-aws | 同样 JS 加载 + handler gin.H |
| 12 | Sidebar 缺 models-aws | layout.html | 添加 `nav.admin.models_aws` |
| 13 | 全部页面双 script 块冲突 | 6 文件 | 合并为单 script 块 + null-guard |
| 14 | Drawer 字段不全 (rate limits) | admin-groups | 10 限流字段 + 模型选择器 |

---

## 4. CSS 类名映射

| 旧模式 | 新模式 | 说明 |
|--------|--------|------|
| `div.card > div.card-header > h1.card-title` | `div.page-header > h1.page-title` | 页面标题 |
| `div.stats-grid` | `div.key-stats` | 统计卡片容器 |
| `div.stat-card` | `div.key-stat-card` | 单张统计卡片 |
| `div.stat-icon` | `div.key-stat-icon primary/success/warning/danger` | 统计图标 |
| `div.stat-content` | 不需要（key-stat-card 直接包含 value+label） | — |
| `div.stat-label` | `div.key-stat-label` | 统计标签 |
| `div.stat-value` | `div.key-stat-value` | 统计数值 |
| `.modal-overlay > .modal` | `.drawer + .drawer-backdrop` | 创建/编辑改用 Drawer |

### 图标色彩约定

| 场景 | CSS 类 | 颜色 |
|------|--------|------|
| 总数/用户类 | `key-stat-icon primary` | 蓝色 |
| 活跃/健康类 | `key-stat-icon success` | 绿色 |
| 禁用/错误类 | `key-stat-icon danger` | 红色 |
| 警告/待处理 | `key-stat-icon warning` | 橙色 |

---

## 5. Drawer/Modal 策略（Admin 层）

| 场景 | 容器 | 理由 |
|------|------|------|
| 创建 (add) | **Drawer** | 保持列表上下文，不阻断操作流程 |
| 编辑 (edit) | **Drawer** | 同上 |
| 删除 (delete) | **Modal** | 必须阻断，防止误操作 |
| 禁用/启用 (toggle) | **Modal** 或 inline | 确认阻断 |
| 批量操作 | **Modal** | 操作影响范围大 |
| 详情查看 | **Drawer** 或 inline | 不阻断浏览 |

---

## 6. 编译与部署

```bash
go build -o portal ./cmd/server
docker compose -f docker-compose.portal.yml down portal
docker compose -f docker-compose.portal.yml up -d portal --build
curl http://localhost:3001/api/health
```

---

## 7. Git 分支

- **分支**: `enterprise-cardification`
- **基准**: `main` @ `9a8fdee` (2026-06-06)
- **Tag**: `v3.1.0` @ `eceb837` (2026-06-05)
- **最新**: `155a52f` — 27+ commits ahead of main
- **状态**: 全部 22 页完成，14 个 bugfix + P0/P1 回归修复（`0d06a8f` 修复 admin-users drawer 字段 + sidebar 导航 + active count），待合入 main
- **目标**: 完成全部改造后合并回 `main`
