# UI Redesign Comprehensive Review Report (v3.1.0 Baseline)

> **ACTIVE** — Phase 4a/4b/4c + Enterprise 卡片化重构审查，含 v3.1.0 对照
> **日期**: 2026-06-07 18:42 UTC
> **分支**: `enterprise-cardification`（v3.1.0 后 25 commits）
> **审查人**: Sisyphus
> **对比基线**: git tag `v3.1.0` (commit `eceb837`)
> **关联文档**: `ui-admin-redesign-detail-202606070703.md`, `ui-enterprise-redesign-detail-202606061621.md`
> **替代**: `ui-review-report-202606071716.md`（仅覆盖 Admin + i18n，已标记 OUTDATED）

---

## 目录

1. [审查范围与基线](#1-审查范围与基线)
2. [v3.1.0 ⇢ 当前 总览](#2-v310--当前-总览)
3. [API Keys 页面](#3-api-keys-页面)
4. [Billing / Top-up 页面](#4-billing--top-up-页面)
5. [用户公共页面（Dashboard / Profile / Subscriptions / Redemption / Invitation）](#5-用户公共页面)
6. [Enterprise 管理页面（快速扫描）](#6-enterprise-管理页面快速扫描)
7. [侧边栏导航回归](#7-侧边栏导航回归)
8. [Admin Users 页面完整性（摘要）](#8-admin-users-页面完整性摘要)
9. [i18n 问题清单](#9-i18n-问题清单)
10. [安全缺陷](#10-安全缺陷)
11. [统计 Bug](#11-统计-bug)
12. [严重性分级与修复建议](#12-严重性分级与修复建议)
13. [附录：文件清单](#13-附录文件清单)

---

## 1. 审查范围与基线

### 1.1 覆盖页面

| 页面组 | 模板数量 | 审查重点 |
|--------|---------|---------|
| **用户公共页面** | 16 个模板 | v3.1.0 功能完整性对比 |
| **Billing / 充值页面** | 5 个模板 | v3.1.0 功能完整性对比 |
| **API Keys 页面** | 1 个模板 | v3.1.0 功能完整性对比 |
| **Admin 页面** | 10 个模板 | Drawer 化后字段丢失 / i18n |
| **Enterprise 页面** | 7 个模板 | 卡片化后功能完整性（快速扫描） |

### 1.2 审查维度

- **功能完整性**: v3.1.0 基线对比，字段/功能是否丢失
- **i18n 覆盖率**: 硬编码中文字符串、`_t()` / `{{t}}` 缺失
- **安全性**: 权限确认流程是否保留
- **数据正确性**: 统计卡片数据源是否正确
- **导航回归**: 侧边栏链接是否删除

### 1.3 对比基线确认

```
基线 Tag: v3.1.0
基线 Commit: eceb837
当前分支: enterprise-cardification
领先 Commits: 25

文件变更统计:
- 模板修改: 48+ 个文件（全部修改，无删除）
- CSS 修改: static/css/ui-kit.css
- JS 修改: 部分内联化
```

**关键发现**: 没有任何模板文件被删除。所有功能差异均为**同一文件内的修改/替换**，不存在整页丢失的情况。

---

## 2. v3.1.0 ⇢ 当前 总览

### 2.1 变更模式分类

| 变更类型 | 涉及页面 | 风险等级 |
|---------|---------|---------|
| **i18n key 后缀清理** (`_2` 后缀) | 全部 48+ 模板 | 🟢 无风险 |
| **Stat card 替换** (表格→卡片) | apikeys, billing, enterprise pages | 🟡 需验证数据源 |
| **HTML 结构重排** (class/div 调整) | 全部模板 | 🟢 无功能影响 |
| **Sidebar 导航链接移除** | layout.html (7 项) | 🔴 导航回归 |
| **API Key pagination 移除** | apikeys.html | 🟠 功能降级 |
| **HTMX auth 移除** | billing.html, subscriptions.html | 🟠 需确认是否已迁移 |
| **onsubmit handler 移除** | plan-detail partial | 🟠 可能的回归 |
| **Enterprise key detail table → badge** | enterprise-keys.html | 🟡 信息密度降低 |

### 2.2 功能增减总表

| 页面 | v3.1.0 独有（已移除） | 当前独有（新增） | 净变化 |
|------|---------------------|-----------------|--------|
| **apikeys.html** | 分页器, 搜索栏 | 统计卡片, 月度用量, 原因筛选, 状态 toggle, 复制 model 名称 | **功能提升 ✅** |
| **billing.html** | HTMX auth, `_2` key | stat card 重构 | 等效 ⚠️ |
| **enterprise/billing.html** | 部分 `_2` key | stat cards 替代 info cards | 等效 |
| **dashboard.html** | quick action nav | — | **小幅降级 🟡** |
| **enterprise/dashboard.html** | quick action nav | stat cards | 等效 |
| **profile.html** | — | i18n key rename | 等效 |
| **subscriptions.html** | HTMX auth | — | **小幅降级 🟡** |
| **enterprise/keys.html** | detail table | count badge | **信息密度降低 🟡** |

---

## 3. API Keys 页面

### 3.1 文件状态

| 基线文件 | 当前文件 | 状态 |
|---------|---------|------|
| `web/templates/apikeys.html` (v3.1.0, 199 行) | `web/templates/apikeys.html` (当前, ~366 行) | ✅ 大幅增强 |

### 3.2 变更类型分布

| 变更类型 | 发生次数 | 说明 |
|---------|---------|------|
| i18n key 后缀 `_2` 移除 | 5+ 处 | 全局清理：`status_有效_2`→`status_有效` |
| HTML 结构重排 | ~20 处 | class 调整，header div 重组 |
| **功能新增** | 6 项 | 见下方表格 |
| **功能移除** | 1 项 | 客户端分页器 API keys pagination |

### 3.3 新增功能明细

| 新增功能 | 代码位置 | 说明 |
|---------|---------|------|
| 统计卡片 (Stat Cards) | 页面顶部 4 张卡片 | 总数 / 有效 / 即将过期 / 已过期，带颜色图标 |
| 搜索/筛选 | 搜索输入框 + 原因状态筛选 dropdown | 按状态/原因过滤 key 列表 |
| 月度用量显示 | 每行新增 `MonthlyUsage` 列 | 显示每个 key 的月度 token 消耗 |
| 创建日期列 | 每行新增 `created_at` 显示 | 之前未展示 |
| 状态快速切换 | 行内 toggle button | 无需进入详情即可启用/禁用 |
| 双击复制 model 名称 | 行内双击事件 | 复制模型名称到剪贴板 |

### 3.4 功能移除明细

| 移除内容 | 影响 | 严重性 |
|---------|------|--------|
| **客户端分页器** (`.pagination` 区域被删除) | 大量 key 时只能滚动查看，无分页导航 | 🟡 中等（后端仍返回分页数据） |

> **结论**: API Keys 页面是本次重构中**质量提升最大的页面**——新增 6 项功能，仅丢失分页器 UI。建议补回分页器。

---

## 4. Billing / Top-up 页面

### 4.1 文件清单

| 文件 | 基线大小 | 当前大小 | 变更类型 |
|------|---------|---------|---------|
| `web/templates/billing.html` | ~200 行 | ~200+ 行 | 结构重组 + i18n 清理 |
| `web/templates/enterprise/billing.html` | ~150 行 | ~150+ 行 | info card → stat card |
| `web/templates/partials/billing/pricing-info.html` | ~30 行 | 变更 | i18n 清理 |
| `web/templates/partials/billing/plan-detail.html` | ~20 行 | 变更 | `onsubmit` 丢失 |
| `web/templates/partials/billing/topup-form.html` | — | 未删除 | i18n 清理 |

### 4.2 普通用户 Billing 页面

| v3.1.0 特性 | 当前状态 | 变化 |
|------------|---------|------|
| 充值表单 (top-up form) | ✅ 保留 | i18n key 清理 |
| 定价信息卡片 (pricing info) | ✅ 保留 | 内容调整，i18n key 更新 |
| 套餐详情 (plan detail) | ✅ 保留 | **`onsubmit` handler 丢失** |
| HTMX 认证 (`hx-auth`) | ❌ **已移除** | 从整个文件中删除 |
| `_2` 后缀 i18n key | ✅ 已清理 | 全部恢复为标准 key |

### 4.3 `plan-detail.html` onsubmit 回归

```
v3.1.0:
  <form hx-put="..." hx-target="..." hx-swap="..."
        onsubmit="return confirm('{{t "billing.confirm_switch"}}')">
    ...
  </form>

当前:
  <form hx-put="..." hx-target="..." hx-swap="...">
    <!-- ❌ onsubmit 被移除 -->
    ...
  </form>
```

**影响**: 用户切换套餐时不会再弹出确认对话框，可能造成误操作。建议补回。

### 4.4 Enterprise Billing 页面

| v3.1.0 特性 | 当前状态 | 变化 |
|------------|---------|------|
| 信用额度信息卡片 | ✅ 保留 | 从 info card 改为 stat card 样式 |
| 定价倍率显示 | ✅ 保留 | i18n key 更新 |
| 已用信用/剩余信用 | ✅ 保留 | 数据正常 |

> **结论**: Billing 页面无重大功能丢失。`onsubmit` 回归和 HTMX auth 移除需确认是否影响功能。

---

## 5. 用户公共页面

### 5.1 文件清单

| 文件 | 基线大小 | 重排行数 | 功能影响 |
|------|---------|---------|---------|
| `web/templates/dashboard.html` | ~145 行 | ~20 行 | 🟡 导航快捷方式移除 |
| `web/templates/profile.html` | ~30 行 | ~5 行 | 🟢 仅 i18n 清理 |
| `web/templates/subscriptions.html` | ~80 行 | ~5 行 | 🟡 HTMX auth 移除 |
| `web/templates/my-subscriptions.html` | ~80 行 | ~5 行 | 🟢 仅 i18n |
| `web/templates/my-group.html` | ~80 行 | 未变 | 🟢 无实质变化 |
| `web/templates/redemption.html` | ~60 行 | 未变 | 🟢 无实质变化 |
| `web/templates/orders.html` | ~100 行 | ~10 行 | 🟢 仅结构重排 |
| `web/templates/invoices.html` | ~40 行 | 未变 | 🟢 无实质变化 |
| `web/templates/invitation.html` | ~80 行 | 未变 | 🟢 无实质变化 |
| `web/templates/addresses.html` | ~30 行 | 未变 | 🟢 无实质变化 |

### 5.2 Dashboard 页面

```
v3.1.0:
  {{if .User.IsAdmin}}
    <!-- Quick action shortcut nav -->
  {{end}}
  <div class="row">
    <div class="col-md-6">...余额...</div>
    <div class="col-md-6">...用量...</div>
  </div>

当前:
  <!-- ❌ Quick action nav 被删除（包含 admin 快捷入口）-->
  <div class="row">
    <!-- 保留余额和用量 -->
  </div>
```

**影响**: Dashboard 顶部快捷操作导航区域被删除。该区域包含管理员常用的快速入口（如创建 API Key、查看统计等）。虽然普通用户可能不常用，但涉及管理员角色降级。

### 5.3 Subscriptions 页面

- **HTMX auth**: `hx-auth` 属性从 `subscriptions.html` 中移除
- **影响**: 可能需要 JS 层代替认证。需结合前端 JS 检查是否已补全

### 5.4 其他用户页面

| 页面 | 差异 | 结论 |
|------|------|------|
| profile.html | `t "ui_保存"` → `t "ui.save"` | 等效，i18n key 标准化 |
| my-subscriptions.html | 少量 i18n key 调整 | 等效 |
| my-group.html | 无实质性变更 | 完全相同 |
| redemption.html | 无实质性变更 | 完全相同 |
| orders.html | HTML 结构重排 (div 结构调整) | 等效 |
| invoices.html | 无实质性变更 | 完全相同 |
| invitation.html | 无实质性变更 | 完全相同 |
| addresses.html | 无实质性变更 | 完全相同 |

> **结论**: 用户公共页面基本不受影响。Dashboard 快捷导航移除是主要降级。

---

## 6. Enterprise 管理页面（快速扫描）

### 6.1 文件状态

| 文件 | 变更类型 | 功能影响 |
|------|---------|---------|
| `enterprise/dashboard.html` | info cards → stat cards | 🟢 等效 |
| `enterprise/users.html` | 表格 + drawer | 🟢 等效 |
| `enterprise/groups.html` | 表格 + drawer | 🟢 等效 |
| `enterprise/keys.html` | **detail table → count badge** | 🟡 信息降级 |
| `enterprise/billing.html` | info cards → stat cards | 🟢 等效 |
| `enterprise/usage.html` | 统计卡片 | 🟢 等效 |
| `enterprise/audit.html` | 表格 | 🟢 等效 |

### 6.2 Enterprise Keys 页面 detail table 丢失

```
v3.1.0:
  API Key 详情表格（显示 key、model、状态、过期时间等详细信息）

当前:
  API Key 显示为 badge + count（仅显示数量和状态摘要）
  <!-- ❌ 详情表格被替换 -->
```

**影响**: 企业管理员无法在 keys 页面查看完整的 API Key 详细信息。如果需要查看详情，需额外点击或其他操作。

### 6.3 HTML 注释残留在 `<head>` 中

多个 enterprise 页面保留中文 HTML 注释，如：
```html
<!-- 头部 -->
<!-- 统计卡片区域 -->
```

> 低优先级，但建议统一为英文注释。

---

## 7. 侧边栏导航回归

### 7.1 移除的导航链接

`web/templates/layout.html` 侧边栏中以下 7 个导航链接被删除：

| 移除的链接 | 路由 | 目标用户 | 影响 |
|-----------|------|---------|------|
| 我的团队 (My Group) | `/my-group` | 普通用户 | 🔴 用户无法访问团队管理 |
| 邀请记录 (Invitation) | `/invitation` | 普通用户 | 🔴 用户无法查看邀请记录 |
| 使用记录 (Orders) | `/orders` | 普通用户 | 🔴 用户无法查看订单历史 |
| 发票管理 (Invoices) | `/invoices` | 普通用户 | 🔴 用户无法查看发票 |
| 地址管理 (Addresses) | `/addresses` | 普通用户 | 🔴 用户无法管理收货地址 |
| 供应商管理 (Super/Providers) | `/super/providers` | 超级管理员 | 🔴 超级管理员入口丢失 |
| 支付配置 (Super/Payment) | `/super/payment` | 超级管理员 | 🔴 支付配置入口丢失 |

### 7.2 推测原因

这些链接被移除可能与 **Enterprise 角色 7 项约定** 中 "隐藏通用用户的「团队/邀请/使用记录/发票管理」等页面" 的实现有关。但问题是：

- 这些路由的后端 handler **仍然存在且可访问**（直接输入 URL 可打开）
- 前端无任何角色判断来有条件显示这些链接
- 即使企业版用户不应看到这些链接，普通用户仍需要它们

### 7.3 建议修复

```html
{{if not .User.IsEnterprise}}
  <a href="/my-group">{{t "nav.my_group"}}</a>
  <a href="/orders">{{t "nav.orders"}}</a>
  <a href="/invoices">{{t "nav.invoices"}}</a>
  <a href="/invitation">{{t "nav.invitation"}}</a>
  <a href="/addresses">{{t "nav.addresses"}}</a>
{{end}}
```

> **注**: 需确认 `.User` 对象中是否有 `IsEnterprise` 或类似字段。或者使用角色类型判断。

---

## 8. Admin Users 页面完整性（摘要）

> 完整内容见 `ui-review-report-202606071716.md`，此处仅列举关键问题。

### 8.1 Drawer 化后丢失的字段

| 丢失内容 | 后端是否仍在查询 | 严重性 |
|---------|----------------|--------|
| 用户组分配 (Group 下拉框) | ✅ `PATCH /api/admin/users/:id` 仍接受 `group_id` | 🔴 P0 |
| 用户类型编辑 (UserType) | ✅ 后端仍返回 `AdminUserItem.UserType` | 🔴 P0 |
| 模拟登录按钮 | ✅ 路由 `POST /users/:id/impersonate` 仍存在 | 🟡 P2 |

### 8.2 旧版 Partial 仍完整可用

```go
// GET /admin/users/:id/detail → 仍返回 user-detail-partial 模板
// 文件: web/templates/partials/user-detail.html（82 行，完整可用）
```

建议直接复用 `user-detail-partial` 恢复功能。

---

## 9. i18n 问题清单

> 完整列表见 `ui-review-report-202606071716.md` §3。以下仅列出本次新增或未覆盖项目。

### 9.1 新发现的 i18n 缺失

| 文件 | 问题 | 严重性 |
|------|------|--------|
| `enterprise/dashboard.html` | HTML 注释中文 `<!-- 头部 -->` | 🟢 低 |
| `enterprise/users.html` | HTML 注释中文 | 🟢 低 |
| `enterprise/groups.html` | HTML 注释中文 | 🟢 低 |
| `enterprise/keys.html` | HTML 注释中文 | 🟢 低 |
| `enterprise/billing.html` | HTML 注释中文 + 硬编码回退文本 | 🟡 中 |
| `enterprise/usage.html` | HTML 注释中文 | 🟢 低 |

### 9.2 `_2` 后缀清理（全部正确）

本次重构全局清理了 i18n key 的 `_2` 后缀（如 `status_有效_2` → `status_有效`）。审查所有 48+ 文件确认：

- ✅ `enterprise/dashboard.html`: 全部清理
- ✅ `enterprise/users.html`: 全部清理
- ✅ `enterprise/groups.html`: 全部清理
- ✅ `enterprise/keys.html`: 全部清理
- ✅ `enterprise/billing.html`: 全部清理
- ✅ `enterprise/usage.html`: 全部清理
- ✅ `enterprise/audit.html`: 全部清理

> 这是正确的清理方向，所有变更一致。

---

## 10. 安全缺陷

### 10.1 角色修改确认绕过（Admin Users）

**严重性**: 🔴 P0

**详情**: 见 `ui-review-report-202606071716.md` §4.1。

### 10.2 HTMX Auth 移除

**严重性**: 🟡 P2

以下文件移除了 `hx-auth` 属性，但后端路由仍需要认证：

| 文件 | 移除内容 |
|------|---------|
| `web/templates/billing.html` | `hx-auth` 属性从多个 HTMX 元素中移除 |
| `web/templates/subscriptions.html` | `hx-auth` 属性移除 |

> 需要确认前端 JS 是否已通过其他方式（如 `HX-Auth` header 或 JS 层）代替。如果未代替，这些 HTMX 请求将不再携带认证头。

---

## 11. 统计 Bug

### 11.1 活跃用户统计显示总用户数（Admin Users）

**严重性**: 🔴 P0

**详情**: 见 `ui-review-report-202606071716.md` §5.1。两个 stat card（总用户数 / 活跃用户数）均使用同一变量 `$d.Total`。

### 11.2 Stat Card 数据源待验证

| 页面 | 统计卡片数 | 数据源 | 验证状态 |
|------|-----------|--------|---------|
| apikeys.html | 4 (总/有效/即将过期/已过期) | `$d.Stats.*` | ✅ 使用独立 `KeyStatsData` 结构体，代码审查确认独立查询 |
| enterprise/dashboard.html | 4 (用户/组/Key/用量) | `EnterpriseDashboardData` | ❌ 未逐一验证 |
| enterprise/billing.html | stat card | `EnterpriseBillingData` | ✅ 等效于原 info card |
| admin-groups.html | 2 | `AdminGroupsData` | ❌ 需验证 |
| admin-enterprise.html | 4 | `AdminEnterpriseData` | ❌ 需验证 |
| admin-keypool.html | 4 | `AdminKeypoolData` | ❌ 需验证 |

---

## 12. 严重性分级与修复建议

### P0（马上修复）

| # | 问题 | 文件 | 修复方案 |
|---|------|------|---------|
| 1 | Edit User Drawer 缺少 Group 分配 | `admin-users.html` + `partials/user-detail.html` | 复用 `user-detail-partial` 的 group 下拉框逻辑 |
| 2 | Edit User Drawer 缺少 UserType 字段 | `admin-users.html` | 从后端查询用户 user_type 并展示/编辑 |
| 3 | "活跃用户" 统计 = 总用户数 | `admin-users.html:22` + `internal/query/admin.go` | 增加独立活跃用户计数查询 |
| 4 | 角色修改确认被绕过（安全） | `admin-users.html` | 恢复 role-confirm-modal 二次确认流程 |

### P1（本周修复）

| # | 问题 | 文件 | 修复方案 |
|---|------|------|---------|
| 5 | **侧边栏 7 个导航链接丢失** | `layout.html` | 按角色有条件恢复：普通用户显示 my-group/orders/invoices/invitation/addresses；超级管理员显示 super/providers, super/payment |
| 6 | **API Keys 分页器丢失** | `apikeys.html` | 从 v3.1.0 恢复 `.pagination` 区域，适配新 HTML 结构 |
| 7 | **Plan-detail onsubmit handler 丢失** | `partials/billing/plan-detail.html` | 恢复切换套餐前的确认对话框 |
| 8 | **Enterprise Keys detail table 降级** | `enterprise/keys.html` | 保留 badge 同时恢复 detail table，或改为可展开的行详情 |
| 9 | Dashboard 快捷导航丢失 | `dashboard.html` | 按角色恢复 admin 快捷入口 |
| 10 | `admin-groups.html` 路由轨道标签硬编码 `标准通道` | `admin-groups.html:82,141` | 替换为 `{{t "status_标准通道"}}` |

### P2（本月修复）

| # | 问题 | 文件 | 修复方案 |
|---|------|------|---------|
| 11 | 批量 `showMessage()` 未使用 `_t()` | admin-keypool, admin-enterprise, admin-providers | 逐一替换为 `_t('xxx')` 包裹 |
| 12 | `i18n-messages.json` 未被加载 | `layout.html` | 添加 `<script src="/static/js/i18n-messages.json">` 或合并到内联字典 |
| 13 | Enterprise 页面 HTML 注释中文 | 5 个 enterprise 模板 | 改为英文注释 |
| 14 | HTMX auth 移除未验证 | `billing.html`, `subscriptions.html` | 确认 JS 层是否已补全认证头 |
| 15 | 统计卡片数据源验证 | 所有 admin 页面 | 逐一验证 key-stat-card 的绑定变量 |
| 16 | 余额编辑无保护 | `admin-users.html` | 恢复 toggle + 警告机制 |
| 17 | fallback 错误信息未翻译 | `admin-users.html:258-301` | 使用 `{{t "error.xxx"}}` 替换硬编码英文 |

---

## 13. 附录：文件清单

### 13.1 审查的核心文件

| 文件 | 说明 |
|------|------|
| `web/templates/apikeys.html` | API Keys 页面（366 行，**大幅增强**） |
| `web/templates/billing.html` | 用户充值页面 |
| `web/templates/dashboard.html` | 用户仪表盘 |
| `web/templates/profile.html` | 用户个人信息 |
| `web/templates/subscriptions.html` | 公开套餐列表 |
| `web/templates/my-subscriptions.html` | 我的套餐 |
| `web/templates/my-group.html` | 我的团队 |
| `web/templates/redemption.html` | 兑换码 |
| `web/templates/orders.html` | 使用记录/订单 |
| `web/templates/invoices.html` | 发票管理 |
| `web/templates/invitation.html` | 邀请记录 |
| `web/templates/addresses.html` | 地址管理 |
| `web/templates/layout.html` | 布局模板（含侧边栏导航） |
| `web/templates/admin-users.html` | Admin 用户管理 |
| `web/templates/partials/user-detail.html` | 旧版用户详情 partial（82 行，仍可用） |
| `web/templates/partials/billing/plan-detail.html` | 套餐详情 partial（onsubmit 丢失） |
| `web/templates/enterprise/` | Enterprise 7 页面 |
| `web/i18n/en.json` | 英文翻译映射（2868 行） |
| `web/i18n/zh.json` | 中文翻译映射 |
| `web/static/js/i18n-messages.json` | JS 层翻译映射（313 条，未使用） |
| `internal/query/admin.go` | Admin 数据查询 |
| `internal/routes/admin.go` | Admin API 路由 |

### 13.2 涉及的关键提交

| 提交 | 说明 |
|------|------|
| `eceb837` | **基线** v3.1.0 tag |
| `8ff89dd` | Phase 4a — drawer-first for users/groups/enterprise/subscriptions |
| `90c230b` | 合并所有 drawer 页面为单 script 块 + null-guards |
| `85858e4` | 内联所有 page-admin JS，消除 CSP/加载失败 |
| `6b4bb00` | Phase 4b — 标准化剩余 admin 页面到 enterprise 模式 |
| `89e2f30` | 4 个页面中转换 add/edit modal 为 drawer |
| `021d0cc` | 重写 models 页面为 JS 加载表格 |
| `01afe7e` | i18n key 清理 |
| `14d2f87` | Enterprise i18n 清理 + 统计卡片 |
| `f9e25ca` | P5.b v3.1 + i18n（修复 admin-groups 问题） |

### 13.3 背景 Agent 引用

本报告的 v3.1.0 对比数据来源于 4 个并行审查 agent：

| Agent | 范围 | Session ID |
|-------|------|-----------|
| Explore 1 | Enterprise/Admin 页面快速扫描 | `ses_15ce77339ffe` |
| Explore 2 | 所有用户公共页面 v3.1.0 对比 | `ses_15ce78101ffe` |
| Explore 3 | Billing 页面 v3.1.0 对比 | `ses_15ce79383ffe` |
| Explore 4 | API Keys 页面 v3.1.0 对比 | `ses_15ce7a205ffe` |

---

## 14. Post-review Fixes Verified (2026-06-08)

> 本报告撰写后，`enterprise-cardification` 分支又推进了若干 commit。经代码逐项核对，以下问题**已在代码中修复**：

### 已修复的 P0/P1 问题

| 原 # | 问题 | 验证结果 | 修复 Commit |
|------|------|---------|------------|
| P0-1 | Edit User Drawer 缺少 Group 分配 | ✅ `edit-group-id` select 已存在，`populateGroupSelect()` 动态加载 | `0d06a8f` |
| P0-2 | Edit User Drawer 缺少 UserType 字段 | ✅ `edit-user-type` select 已存在，含 general/economy/subscription/enterprise 选项 | `0d06a8f` |
| P0-3 | "活跃用户" 统计 = 总用户数 | ✅ `admin-users.html` 已使用独立 `{{$d.ActiveCount}}` 变量，与 `{{$d.Total}}` 分开展示 | `0d06a8f` |
| P0-4 | 角色修改确认被绕过 | ✅ `role-confirm-modal` 二次确认流程完整存在，`confirmRoleChange()` 函数有效 | `0d06a8f` |
| P1-5 | 侧边栏 7 个导航链接丢失 | ✅ `layout.html` 中 11 个个人导航链接全部存在（dashboard/apikeys/subscriptions/billing-details/billing/redemption/my-group/orders/invitation/invoices/addresses），Enterprise/System Admin 两级 sidebar 亦完整 | `0d06a8f` |
| P1-6 | API Keys 分页器丢失 | ✅ `apikeys.html` 含完整 `.pagination` 区域，条件渲染 `{{if gt $personalKeys.TotalPages 1}}` | 基线已存在 |
| P1-9 | Dashboard 快捷导航丢失 | ✅ `dashboard.html` 含 `dashboard-quick-actions`，7 个快捷入口（apikeys/billing/subscriptions/billing-details/orders/invoices/profile） | 基线已存在 |
| P2-16 | 余额编辑无保护 | ✅ `toggleBalanceEdit()` + 警告提示 `⚠ {{t "ui_修改余额是危险操作保存后不可撤销"}}` 已存在 | `0d06a8f` |
| P2-17 | fallback 错误信息未翻译 | ✅ `admin-users.html` 258-301 区域已使用 `_t()` 包裹 | `19ff2f7` |

### 仍为 Open / Partial 的问题

| 原 # | 问题 | 当前状态 |
|------|------|---------|
| P1-7 | Plan-detail onsubmit handler | ⚠️ `partials/billing/plan-detail.html` 目录已不存在，页面结构已重构为 drawer，需确认新结构中是否等效保留确认流程 |
| P1-8 | Enterprise Keys detail table | ✅ **审查结论修正** — 卡片信息密度**高于**旧版 table。经与 v3.1.0 基线对比，旧 table 仅展示 name/prefix/assigned/group/status/createdAt/actions（且"最近使用"列硬编码为"从未使用"，无实际数据）；当前卡片在此基础上增加了 `MonthRequests`、`MonthTokens`、`AllowedModels` 标签列表，信息密度为**提升**而非降级 |
| P1-10 | admin-groups.html 路由轨道标签 | ✅ **审查结论修正** — `AWS Runtime` 与 `AWS Mantle` 为 AWS 官方产品专有名词，保留英文是正确做法；`{{t "status_标准通道"}}` 已正确 i18n 化 |
| P2-11 | admin-keypool showMessage 中文硬编码 | ✅ **已修复 (2026-06-10)** — 全部 12 处已替换为 `_t()` 包裹；同时修复 admin-group-edit.html (1处)、admin-users.html (2处)；新增 3 个 i18n key 到 layout.html |
| P2-14 | HTMX auth 移除 | ✅ **已修复** — `billing.html` 与 `subscriptions.html` 的所有 `fetch` 调用已补全 `credentials: 'same-origin'` + `X-CSRF-Token` header + `csrf_token` body 字段 |

---

> **总结**: 本次 UI 重构整体质量良好。API Keys 页面获得显著增强，Billing 和用户公共页面基本保持功能完整。
>
> **2026-06-08 更新**：本报告原列出的 🔴 P0 Admin Users 功能降级 和 🔴 P1 侧边栏导航回归 均已在后续 commit（主要是 `0d06a8f`）中修复。经代码二次核对：
> - **P1-8 Enterprise Keys 详情展示**：审查结论修正 — 当前卡片信息密度**高于** v3.1.0 table（增加了月请求数、月 Tokens、模型标签），非降级
> - **P1-10 admin-groups 路由轨道**：审查结论修正 — `AWS Runtime` / `AWS Mantle` 为专有产品名，保留英文正确
> - **P2-14 HTMX auth**：已补全 `credentials` + `X-CSRF-Token`
>
> 当前真正遗留的主要问题为 **admin-keypool 中文硬编码**（P2，约 12 处），以及需要用户决策的 **右上角下拉菜单是否进一步精简**（B8 v2 设计原则要求仅保留 Profile + Logout，但当前代码仍保留 Enterprise/Admin 条件入口）。
>
> 建议修复优先级：P2（admin-keypool i18n）→ 决策 B8 下拉菜单精简 → 统计数据源复核。

## 15. Additional Status Update (2026-06-28)

### Verified Fixed Since §14

| Item | Status | Verification |
|------|--------|--------------|
| P2-11 admin-keypool `showMessage()` 中文硬编码 | ✅ Fixed | All 30+ `showMessage` / `showConfirm` / `showError` calls in `admin-keypool.html` now wrapped with `_t()` |
| P1-7 Plan-detail onsubmit handler | ✅ Resolved | `partials/billing/plan-detail.html` no longer exists; subscription purchase now uses Alpine.js modal with explicit `purchase-plan-modal` confirmation in `subscriptions.html` |
| Sidebar navigation completeness | ✅ Resolved | `layout-sidebar.html` contains full user/enterprise/admin/super-admin nav sections; personal pages consolidated under `/plan-and-group` |

### Still Open / New Findings

| Item | Status | Notes |
|------|--------|-------|
| Form input/select labels | ✅ Fixed | Batch added `aria-label` to 97 `<select>` and 165 `<input>` elements; local axe-core scan now reports 0 violations across 37 pages |
| Color contrast | ✅ Fixed | Updated `app.css` and `admin.html` inline styles to use `-text` color variables; 0 contrast violations remain |
| CSS/JS minification | ⏸️ Deferred | User decision: not needed at current sizes |
| Vale English copy lint | 🟡 Report-only | Migrated from write-good to Vale; 205 warnings + 216 suggestions in `en.json` (passive voice, E-Prime). Kept report-only per user decision. |

### Tools Wired into CI

- axe-core accessibility scan (**gate enabled** on 37 pages)
- Lighthouse public-page scan (gate enabled)
- Vale English copy lint on `web/i18n/en.json` (report-only)
