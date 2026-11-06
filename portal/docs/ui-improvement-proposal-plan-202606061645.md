# UI 风格改进方案评估

> **状态**: ACTIVE — 已与 `ui-design-principles-202606051751.md` 对齐  
> **日期**: 2026-06-06（更新）  
> **用途**: 参照 TokenRouter (tokenrouter.com) 设计风格，评估 ai-portal-go UI 改进工作量  
> **关联**: `docs/ui-design-principles-202606051751.md` — 设计原则与实施指导（10 条原则、Drawer 优先 / Modal 例外、三层界面标准）  
> **注意**: 本方案按"按需保留 modal"策略实施（非全删 modal），详见 §四及附录 A.4

---

## 一、当前 UI 现状盘点

### 1.1 已实现的基础设施

| 基础设施 | 文件 | 说明 |
|---------|------|------|
| CSS 变量体系 | `app.css` :root / [data-theme="dark"] | 完整的 light/dark 双主题 CSS 变量 |
| Inter 字体 | `app.css` @font-face (本地 woff2) | 与 TokenRouter 一致的字体选择 |
| Card 组件 | `app.css` .card / .card-header / .card-title | 圆角 12px + shadow + border |
| Modal 弹窗 | `app.css` .modal-overlay / .modal / .modal-header/body/footer | 完整弹窗体系 |
| Toast 通知 | `app.css` .toast / .toast-success/error/warning | 滑入动画通知 |
| Badge 标签 | `app.css` .badge / .badge-success/warning/error/info | 状态标签 |
| 分页 | `app.css` .pagination | 表格分页 |
| 骨架屏/空状态 | `app.css` .empty-state | 空数据占位 |
| 面包屑 | `app.css` .breadcrumb | 页面导航 |
| 进度条/环 | `app.css` .progress-bar-bg/fill / .progress-ring | 可视化指标 |
| 趋势指示器 | `app.css` .trend-up/down/neutral | 趋势箭头 |
| 活动流 | `app.css` .activity-feed / .activity-item | 动态列表 |
| 下拉菜单 | `app.css` .dropdown / .dropdown-menu / .dropdown-item | 右键/操作菜单 |
| Sidebar | `app.css` .sidebar (240px fixed) | 左侧固定导航 |
| 暗色模式切换 | `layout.html` theme toggle + localStorage | 完整主题持久化 |
| 响应式适配 | `app.css` @media (max-width: 1024px) | 移动端 sidebar 折叠 |
| SVG 图标 | `layout.html` 内联 svg (sidebar + topbar) | 全站统一图标 |
| i18n | `layout.html` 1300+ 行中英映射 | 完整国际化 |

### 1.2 页面清单（60 个模板）

| 类别 | 页面 | 数量 |
|------|------|------|
| **公共** | login, register, landing, forgot-password | 4 |
| **用户** | dashboard, profile, apikeys, my-group | 4 |
| **计费** | billing, billing-details, subscriptions, my-subscriptions, invoices, orders, redemption, addresses | 8 |
| **Admin** | dashboard, users, groups, enterprise, group-edit, keypool, models-general, models-aws, providers, subscriptions, redemption, invitations, announcements, audit-logs, settings, monitor-upstream, monitor-downstream, upstream-usage, provider-health | 19 |
| **Enterprise** | dashboard, users, groups, keys, billing, usage, audit | 7 |
| **Super Admin** | admin-management, providers, payment | 3 |
| **其他** | enterprise-accept, invitation, checkout | 3 |
| **占位** | errors/placeholder, billing/placeholder, auth/placeholder, admin/placeholder, partials/* | ~12 |

---

## 二、TokenRouter 设计分析

基于之前研究 (`docs/reports/tokenrouter-research-202606031600.md`):

### 2.1 视觉特征

```
色板:   深色 #141821  / 浅蓝强调 #e8f2ff  / 白底
字体:   PPNeueMontreal (Inter fallback)
圆角:   6px / 8px / 12px
布局:   三栏 (左侧导航 240px + 内容区 + 右侧面板 220px)
间距:   大面积留白，低信息密度
卡片:   圆角 + 轻 shadow + 白底
暗色:   支持
风格:   Vercel / Linear 风格，比传统管理后台更少表格感
```

### 2.2 与我们的视觉差异

| 维度 | TokenRouter | 我们 | 差异度 |
|------|-------------|------|--------|
| 字族 | PPNeueMontreal | Inter | 接近（Inter 是标准替代） |
| 色值 | `#141821`/`#e8f2ff` | `#1e293b`/`#3b82f6` | 微调 |
| 圆角 | 6/8/12px | 8/12px | 一致 |
| 布局 | 三栏 | 两栏 | **中等** |
| 信息密度 | 低（大面积留白） | 中（表格密集） | **中等** |
| 表格感 | 弱（卡片为主） | 强（表格为主） | **大** |
| 阴影 | 极轻 | 中等 | 微调 |
| 图标 | Semi UI 图标库 | 内联 SVG | 微调 |
| 动效 | 平滑过渡 | 基本无 | **中等** |
| 组件库 | Semi UI (React) | 纯 CSS | 架构差异 |

---

## 三、方案评估

### 方案 A：CSS 微调（1-2 天）

仅改 `app.css` + `layout.html`，不动模板结构。

| 改动项 | 文件 | 预估时间 |
|--------|------|---------|
| 色值对齐 `#141821` / `#1e293b` → 更柔和 | `app.css` :root | 30min |
| 调大留白（padding/margin 全局） | `app.css` .page-content / .card | 30min |
| Shadow 减淡 | `app.css` --shadow 变量 | 10min |
| 暗色模式色值微调 | `app.css` [data-theme="dark"] | 20min |
| 字体微调（调整 weight 映射） | `app.css` @font-face | 10min |
| 调大表格行高（更透气） | `app.css` .data-table th/td | 10min |
| 按钮圆角统一为 8px | `app.css` .btn | 5min |

**结果**: 颜色和间距接近，但布局结构不变，表格感仍然重。  
**适合**: 快速改善，零风险。

### 方案 B：UI 优化 + 关键页面改造（18-22 天）

在 A 基础上改造布局结构和核心页面。

> ⚠️ **注意**：本表为早期评估（B1-B6 共 6 项）。完整实施范围已扩展至 **§四 B0-B13（14 项）**，涵盖导航重构、Topbar 工具栏、Modal 策略、a11y 规范等。总工作量 **18-22 天**（详见 §六.3 与 §七）。

| 阶段 | 改动 | 文件 | 预估 |
|------|------|------|------|
| **B1** | 布局改为三栏（+右侧 contextual 面板） | `layout.html` + `app.css` | 1.5 天 |
| **B2** | 仪表盘改卡片组合布局 | `dashboard.html` + `app.css` | 1 天 |
| **B3** | API Keys 页信息架构重组 | `apikeys.html` | 1 天 |
| **B4** | Admin 管理页表格→卡片过渡 | 核心 5-6 个 admin 模板 | 2 天 |
| **B5** | 动效润色 + 过渡动画 | `app.css` + 全局 | 0.5 天 |
| **B6** | 图标统一 + 空状态插画 | 全局 | 0.5 天 |

**结果**: 视觉上接近 TokenRouter 的精致度，Go 模板架构不变。  
**适合**: 性价比最高，推荐方案。

### 方案 C：前端独立为 React SPA（6-10 周）

完全重写前端，React + Semi UI / shadcn/ui。

| 阶段 | 改动 | 文件 | 预估 |
|------|------|------|------|
| **C1** | 项目初始化 + 组件库选型 | 新 repo | 1 周 |
| **C2** | 路由 + 布局 + 认证 + API 层 | 新 repo | 1 周 |
| **C3** | 用户端页面迁移（~16 页） | 新 repo | 2-3 周 |
| **C4** | Admin 页面迁移（~19 页） | 新 repo | 2 周 |
| **C5** | Enterprise + Super 页面（~10 页） | 新 repo | 1 周 |
| **C6** | 暗色模式 + 响应式 + i18n | 新 repo | 1 周 |
| **C7** | 联调 + 集成测试 | 新 repo + Portal | 1 周 |

**结果**: 架构和 TokenRouter 同级，但工作量最大。  
**适合**: 团队有专职前端 + 有长期维护计划。

---

## 四、推荐方案 B — 完整 UI 重设计实施计划

本方案不是仅对现有 UI 进行视觉打磨，而是覆盖**导航结构、Topbar 工具栏、视觉风格、Drawer / Modal 策略**的完整重设计。实施范围包括：左侧 sidebar 精简与折叠、右上角用户菜单重构、语言/主题切换入口统一、三栏按需布局、核心页面卡片化、动效体系建立、图标标准化、Drawer 优先 / Modal 例外映射落地、页面内表单策略落地，以及可访问性（a11y）规范。

以下按模块拆分 14 项实施任务（B0–B13），每项包含设计来源（引用 `ui-design-principles-202606051751.md`）、具体改动点、代码示例及工作量估算。

### B0：三层用户分级标准（0.5 天）

> 设计来源：设计原则 §一.1 — 三层界面标准

所有后续改造按用户层级区分信息密度和交互策略：

| 层级 | 目标用户 | 信息密度 | 设计要求 |
|------|---------|---------|---------|
| **用户页** | 消费者 | 低 | 大量留白，卡片化，简洁美观，低认知负荷 |
| **Enterprise Admin** | 专业用户 | 中 | 类似 Google Workspace，功能丰富但信息架构清晰 |
| **System/Super Admin** | 管理员 | 中高 | 功能优先，尽量美观，减少疲劳和误操作 |

实施时以此表为基准：用户页优先卡片化与低信息密度；Enterprise Admin 采用清晰的卡片+列表混合；System/Super Admin 以表格优化+页面内表单为主，不强制卡片化。

### B1：三栏按需出现 + 无缝布局（1.5 天）

> 设计来源：设计原则 §二.1 — 三栏按需出现；§五.1 — 无缝布局

**原则：右栏按需出现，默认两栏。**

#### 4.1.1 `app.css` — 新增右侧面板样式

```css
/* 默认两栏 */
.app-layout {
  display: grid;
  grid-template-columns: var(--sidebar-width) 1fr;
  min-height: 100vh;
}

/* 三栏：页面通过 .has-right-panel 开启 */
.app-layout.has-right-panel {
  grid-template-columns: var(--sidebar-width) 1fr 260px;
}

/* Sidebar 与主内容区无缝衔接（参考 TokenRouter） */
.sidebar {
  background: var(--bg);          /* 与页面背景同色，不用 bg-card */
  border-right: none;             /* 去掉分隔边框 */
}

.main-content {
  background: var(--bg);          /* 与 sidebar 同色 */
}

.right-panel {
  background: var(--bg-card);
  border-left: 1px solid var(--border);
  padding: 24px 16px;
  position: sticky;
  top: 0;
  height: 100vh;
  overflow-y: auto;
  display: none; /* 默认隐藏 */
}

.app-layout.has-right-panel .right-panel {
  display: block;
}

/* 滚动条与背景融合 */
::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}
::-webkit-scrollbar-track {
  background: transparent;
}
::-webkit-scrollbar-thumb {
  background: var(--border);
  border-radius: 3px;
}
::-webkit-scrollbar-thumb:hover {
  background: var(--text-muted);
}
* {
  scrollbar-width: thin;
  scrollbar-color: var(--border) transparent;
}

/* 右侧面板组件 */
.context-card {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 14px 16px;
  margin-bottom: 12px;
}

.context-card-title {
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-muted);
  margin-bottom: 8px;
}
```

#### 4.1.2 `layout.html` — 布局结构

```html
<div class="app-layout {{block "layoutClass" .}}{{end}}">
  <aside class="sidebar">...</aside>
  <div class="main-content">
    <header class="topbar">...</header>
    <div class="page-content">
      {{block "content" .}}{{end}}
    </div>
  </div>
  <aside class="right-panel">
    {{block "rightPanel" .}}{{end}}
  </aside>
</div>
```

#### 4.1.3 右侧面板内容（按需填充，仅读操作）

- **仪表盘**: 今日概览、余额、用量速览
- **API Keys**: 当前 key 详情（只读）
- **Admin**: 选中行的详情展示（只读），编辑仍走 modal
- **通用**: 帮助链接、文档快捷入口

**设计约束**：右侧面板仅承载**读操作**（查看详情、上下文信息）。**写操作**（新建/编辑/删除）仍使用 modal。

### B2：仪表盘改造（1 天）

> 设计来源：设计原则 §一.1 — 用户页信息密度标准（低）

#### 当前
```
┌───┐ ┌───┐ ┌───┐ ┌───┐
│余额│ │Key │ │用量│ │月  │   ← stat-card 网格
└───┘ └───┘ └───┘ └───┘
┌──────────────────────┐
│ 快捷操作行            │
└──────────────────────┘
┌──────────────────────┐
│ 最近使用卡片          │
└──────────────────────┘
```

#### 改为
```
┌─────────────────────────────────────┐
│ 欢迎回来，xxx                        │
│ 上次登录：3小时前                     │   ← 用户卡片
└─────────────────────────────────────┘

┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐
│余额   │ │用量   │ │Key   │ │订阅   │   ← mini-stat 水平卡片
│$12.50│ │1.2M  │ │8     │ │1     │
│+12% ↑│ │+5% ↑ │ │—     │ │—     │
└──────┘ └──────┘ └──────┘ └──────┘

┌──────────────────────┐ ┌──────────┐
│ 用量趋势图 (chart.js) │ │ 快速入口  │
│                      │ │ · API Key│
│  ▁▃▆▇▆▅▄▃            │ │ · 充值   │
│                      │ │ · 订阅   │
└──────────────────────┘ └──────────┘

┌─────────────────────────────────────┐
│ 最近活动                             │
│ · Key "production" 已创建  2h ago    │
│ · 余额自动充值 $50        5h ago    │   ← activity-feed
│ · 模型 claude-4 用量超限   1d ago    │
└─────────────────────────────────────┘
```

### B3：API Keys 页改造（1 天）

> 设计来源：设计原则 §一.1 — 用户页信息密度标准（低）；§四 — Drawer 优先 / Modal 例外

#### 当前
表格为主，每行一个 key，操作按钮在每行末尾。

#### 改为
- 上方统计卡片（总 key 数、活跃 key、本月用量）
- 搜索 + 过滤栏（名称/状态/类型）+ **Bulk Copy 按钮（始终可见，未选时 disabled）**
- 卡片列表（每个 key 一个卡片）
  - **卡片 body 点击** → 打开右侧 Drawer（详情/编辑）
  - **卡片右上角图标** → 快捷操作（📋 复制单 key 名、☐ 加入多选）
- 右侧 Drawer：承载 Key 详情查看 + 编辑表单（TokenRouter 实证：同一 Drawer 既读又写）
- **新建 Key** → 右侧 Drawer（不是 Modal）
- **删除 Key** → 确认 Modal（唯一保留 Modal 的场景）

```html
{{define "content"}}
<!-- Summary stats -->
<div class="stats-grid">
  <div class="stat-card mini">
    <div class="stat-icon stat-icon-blue">🔑</div>
    <div>
      <div class="stat-label">总 Key 数</div>
      <div class="stat-value">{{.TotalKeys}}</div>
    </div>
  </div>
  <!-- ... -->
</div>

<!-- Filter bar -->
<div class="filter-bar">
  <input class="form-input" placeholder="搜索 Key..." />
  <select class="form-select">...</select>
  <!-- Bulk Copy: 始终可见，未选时 disabled -->
  <button class="btn btn-secondary" id="bulk-copy-btn" disabled>📋 Copy Selected</button>
  <button class="btn btn-primary">+ 新建 Key</button>
</div>

<!-- Key cards -->
<div class="key-grid">
  {{range .Keys}}
  <div class="key-card" data-key-id="{{.ID}}">
    <!-- 右上角快捷操作图标（hover 显示） -->
    <div class="key-card-actions">
      <button class="icon-btn" title="复制 Key 名" onclick="copyKeyName('{{.Name}}')">📋</button>
      <button class="icon-btn" title="选中" onclick="toggleSelect('{{.ID}}')">☐</button>
    </div>
    <!-- 卡片 body：点击打开 Drawer -->
    <div class="key-card-body" onclick="openKeyDrawer('{{.ID}}')">
      <div class="key-header">
        <span class="badge badge-{{.Status}}">{{.Status}}</span>
        <span class="key-name">{{.Name}}</span>
      </div>
      <div class="key-meta">{{.ModelRoutingMode}} · {{.SelectedModels}} models</div>
      <div class="key-usage">
        <div class="progress-bar-bg">
          <div class="progress-bar-fill low" style="width: {{.UsagePercent}}%"></div>
        </div>
      </div>
    </div>
  </div>
  {{end}}
</div>
{{end}}
```

**交互规范（参考 TokenRouter）**：
1. **卡片 body 点击** = 打开/关闭 Drawer（toggle），**不进入多选状态**
2. **卡片右上角 ☐ 图标** = 多选模式，勾选后卡片加蓝色边框高亮
3. **至少选中 1 个后**，顶部 Bulk Copy 按钮 `disabled → enabled`
4. **新建/编辑** = Drawer 内表单；**删除** = 确认 Modal

### B4：Admin 页面改造（2 天）

> 设计来源：设计原则 §一.1 — Enterprise Admin（中）与 System/Super Admin（中高）分层标准；§四.2 — Drawer 场景 / §四.3 — Modal 例外

按用户层级区分策略：

#### Enterprise Admin 页面（卡片化）

| 页面 | 文件 | 改造方式 | 时间 |
|------|------|---------|------|
| Enterprise 仪表盘 | `enterprise-dashboard.html` | 卡片组合 + activity feed | 0.5 天 |
| Enterprise Keys | `enterprise-keys.html` | 卡片列表 | 0.5 天 |
| Enterprise Users | `enterprise-users.html` | 卡片网格 | 0.5 天 |
| Enterprise Groups | `enterprise-groups.html` | 卡片列表 | 0.5 天 |

**保留 modal**：Enterprise key / group 的 create/edit/delete（设计原则 §4.2）

#### System/Super Admin 页面（表格优化 + 部分页面内表单）

| 页面 | 文件 | 改造方式 | Modal 策略 |
|------|------|---------|-----------|
| 模型管理 | `admin-models-general.html` | 表格优化（行高、padding、hover）+ 状态标签增强 | **页面内表单**（add/edit/delete） |
| Key Pool | `admin-keypool.html` | 表格优化 | **页面内表单**（add/edit/delete） |
| Provider 管理 | `admin-providers.html` | 状态卡片 + 健康指示器 | **页面内表单**（add/edit/delete） |
| 分组管理 | `admin-groups.html` | 表格优化 | **保留 modal**（create/edit/delete） |
| Enterprise 管理 | `admin-enterprise.html` | 表格优化 | **保留 modal**（create/edit/delete） |

改造原则：
- 不改变后端数据接口（Go handler 不变）
- 只改模板 HTML + CSS class
- 保持向后兼容（i18n key 不变）
- 具体 modal 保留清单见设计原则 §4.2

### B5：动效润色（0.5 天）

> 设计来源：设计原则 §五 — 视觉风格整体要求

| 效果 | 实现 | 文件 |
|------|------|------|
| 页面切换 fade in | `.page-content { animation: fadeIn 0.2s }` | `app.css` |
| Card hover lift | `.card:hover { transform: translateY(-2px) }` | `app.css` |
| Sidebar 子项展开 | CSS transition on max-height | `app.css` |
| 表格行 hover 增强 | 更明显的背景色变化 | `app.css` |
| Toast 滑入动画 | 已有，优化 timing function | `app.css` |
| 按钮点击反馈 | active scale(0.97) | `app.css` |

### B6：图标统一 + 空状态（0.5 天）

> 设计来源：设计原则 §五 — 视觉风格整体要求

- Sidebar SVG 图标标准化（已有 48 个内联 svg 保持一致风格）
- 空状态插画（.empty-state 增加 svg 插画）
- 加载骨架屏（skeleton loading placeholder）

### B7：Topbar 右侧图标（语言 + 主题）（0.5 天）

> 设计来源：设计原则 §三.3 — Topbar 右侧图标

将语言切换和主题切换从现有位置统一收拢到 topbar 右侧、头像左侧：

```
[Logo]  [页面标题]                    [🌐 语言] [🌓 主题] [头像 ▼]
```

- **语言选择器**：图标按钮，点击切换 zh/en（或下拉选择多语言）
- **主题切换器**：图标按钮，dark / light / system 三态循环切换
- 使用 `localStorage` 持久化用户选择
- ARIA label：`aria-label="Switch language"` / `aria-label="Toggle theme (dark/light/system)"`

### B8：右上角下拉菜单 — Profile + Super Admin + Logout（0.5 天）

> **设计定位**：侧栏两级结构已展示所有导航层级，因此下拉菜单**不承担导航功能**，只放个人操作入口。
>
> 设计来源：设计原则 §三.3 — 右上角下拉菜单

```
┌─────────────────────┐
│ 👤 Profile          │  ← 所有用户
│ ─────────────────── │
│ 🔒 Super Admin      │  ← 仅 super_admin（低频，不放 sidebar）
│ ─────────────────── │
│ 🚪 Logout           │  ← 所有用户
└─────────────────────┘
```

- **Profile** 从 sidebar 移除，统一放入下拉菜单
- **Super Admin** 入口仅在下拉菜单中（超级管理员设置使用频率极低）
- Enterprise Admin 和 System Admin 的所有入口均在 sidebar 中（见 B10），不下放到 dropdown
- 普通用户下拉菜单仅两项：Profile / Logout
- 键盘支持：Enter/Space 打开关闭，↑↓ 导航，ESC 关闭

### B9：Sidebar 折叠（0.5 天）

> 设计来源：设计原则 §二.2 — 左侧导航栏可折叠

- 展开状态：240px，显示 SVG 图标 + 文字
- 折叠状态：64px，仅显示 SVG 图标居中
- **SVG 图标与导航文字分离处理**：折叠时文字通过 `visibility: hidden` 或 `opacity: 0` 隐藏，图标不受影响，避免重新渲染
- `localStorage` 记住用户折叠偏好，刷新后保持状态
- CSS `transition` 动画过渡，时长 `0.25s ease`

```css
.sidebar {
  width: var(--sidebar-width); /* 240px */
  transition: width 0.25s ease;
}

.sidebar.collapsed {
  width: 64px;
}

.nav-text {
  transition: opacity 0.2s ease;
}

.sidebar.collapsed .nav-text {
  opacity: 0;
  visibility: hidden;
  white-space: nowrap;
}
```

### B10：左侧导航两级结构（1 天）

> **TokenRouter 实证修正**：Admin/Enterprise 入口**不移除到下拉菜单**，而是作为 sidebar 的第二层级展示。用户在同一侧栏中同时看到管理区和个人区，无需来回切换上下文。
>
> 设计来源：设计原则 §三.2 — 左侧导航（两层结构）

#### 普通用户 — 单层扁平（不变）

保持现有 5~6 个核心入口：

| 栏目 | 包含内容 | 说明 |
|------|---------|------|
| Dashboard | — | 用户仪表盘 |
| API Keys | — | Key 管理 |
| Subscriptions | 订阅管理 + 我的订阅 | 合并订阅相关入口 |
| Usage | 用量查询 + 使用记录 + 计费详情 | 独立页面 |
| Billing | 充值 + 订单历史 + 发票 + 地址管理 | 合并计费相关入口 |
| Redemption | 兑换码 + 欢迎码 | 合并兑换相关入口 |

- Profile 从 sidebar 移除，统一放入右上角下拉菜单（见 B8）

#### Enterprise Admin — 双层（参考 TokenRouter）

```
▼ Admin Console          ← 区域标题（默认展开）
  ├─ Overview
  ├─ Members
  ├─ Groups
  ├─ Keys
  ├─ Billing
  ├─ Usage
  └─ Audit

▷ Personal               ← 可折叠区域
  ├─ Dashboard
  ├─ API Keys
  ├─ Subscriptions
  ├─ Billing Details
  ├─ Billing
  └─ Redemption
```

- 区域标题是 `<button>`，点击展开/折叠下方列表
- Admin Console 默认展开，Personal 默认折叠
- 折叠状态通过 localStorage 记住
- 新增 CSS class：`.sidebar-group` / `.sidebar-group-label` / `.sidebar-group-items`，沿用当前设计语言

#### System/Super Admin — 双层

```
▼ System Admin           ← 区域标题（默认展开）
  ├─ Overview
  ├─ Users
  ├─ Groups
  ├─ Enterprise
  ├─ Key Pool
  ├─ Models
  ├─ Models AWS
  ├─ Providers
  ├─ Subscriptions
  ├─ Redemption
  ├─ Invitations
  ├─ Announcements
  ├─ Audit Logs
  ├─ Settings
  ├─ Monitor Upstream
  ├─ Monitor Downstream
  ├─ Upstream Usage
  └─ Provider Health

▷ Personal               ← 可折叠区域
  └─ (同上)
```

- 栏目名称以 i18n key 为准，保持向后兼容

### B11：Drawer 优先 / Modal 例外（0.5 天）

> 设计来源：设计原则 §四 — Drawer 优先与 Modal 例外策略
> **TokenRouter 实证**（v6/v7）：TokenRouter 的右侧 Drawer 同时承载 read（模型详情）和 write（创建 API Key），**无 backdrop**。因此我们的策略从"Panel=只读 / Modal=写操作"修正为"Drawer=默认容器 / Modal=仅例外"。

#### 决策原则

**默认使用 Drawer**：列表页的创建、编辑、详情查看优先使用右侧 Drawer（保持列表上下文）。

**例外使用 Modal**：仅以下 4 类必须阻断的场景使用 Modal：

| 场景 | 使用 Modal | 理由 |
|------|-----------|------|
| 删除确认（所有层级） | ✅ | 必须阻断，防止误删 |
| 充值/支付 | ✅ | 涉及金钱，行业惯例 |
| 余额不足提示 | ✅ | 阻断式，必须处理 |
| 首次合规章程 | ✅ | 一次性，必须接受 |

#### 各层级具体映射

| 用户层级 | 操作 | 容器 | 备注 |
|---------|------|------|------|
| **普通用户** | API Key 创建/编辑 | **Drawer** | TokenRouter 实证 |
| | API Key 删除 | **Modal** | 确认阻断 |
| | 模型详情查看 | **Drawer** | 卡片点击打开 |
| | 充值支付 | **Modal** | 必须阻断 |
| **Enterprise Admin** | Enterprise key 创建/编辑 | **Drawer** | 表单复杂但不需要阻断 |
| | Group 创建/编辑 | **Drawer** | 企业级操作 |
| | 删除确认 | **Modal** | 必须阻断 |
| **System/Super Admin** | Model/Key Pool/Provider 编辑 | **Inline edit row / Drawer** | 配置项少 |
| | Group/Enterprise 创建/编辑 | **Drawer** | 系统级操作 |
| | 删除确认 | **Modal** | 必须阻断 |

实施原则：
- **Drawer 决策优先**：新增功能先考虑是否能放 Drawer
- 每个使用 Modal 的场景在代码中标注 `"MODAL_EXCEPTION: [原因]"`
- 维护 Drawer/Modal 使用清单，随代码同步更新

### B12：页面内表单策略（1 天）

> 设计来源：设计原则 §四.3 + 附录 C — Inline edit row

按操作类型区分实现方式：

| 操作 | 实现方式 | 适用页面 |
|------|---------|---------|
| **创建（Create）** | 页面内表单视图（列表上方展开大表单） | admin-keypool、admin-models-general、admin-providers |
| **编辑（Edit）** | **Inline edit row**（行下方展开内联表单） | 同上 |
| **删除（Delete）** | 确认 Modal（阻断，防止误操作） | 所有页面 |

System/Super Admin 专属调整：
- Model / Key Pool / Provider 的添加/编辑/删除改为页面内表单（配置项少，操作不频繁）
- Group / Enterprise 仍保留 modal（系统级操作，字段多、影响大）

Inline edit row 实现要点：
- 点击编辑按钮后，表格行替换为内联表单
- 使用 HTMX `hx-get` / `hx-post` 异步提交
- 3-8 个简单字段适合 inline edit；10+ 字段或复杂联动仍用 modal
- 取消操作后恢复原行视图

详见附录 C 完整代码示例。

### B13：a11y / 键盘导航（1-1.5 天）

> 设计来源：附录 D — 可访问性与键盘导航规范

确保 UI 改造满足键盘操作和辅助技术需求：

1. **Modal focus trap**：Modal 打开时键盘焦点限制在 Modal 内部，Tab 循环不逃出。关闭后焦点返回触发元素。
2. **ESC 全局关闭**：优先级 Modal → Dropdown → Right Panel，逐级关闭。
3. **Sidebar 键盘导航**：Tab 顺序为 Logo → 语言 → 主题 → 头像 → 导航项 → 主内容区。当前页面项标记 `aria-current="page"`。
4. **Right panel 焦点返回**：面板关闭后焦点回到触发它的列表行。
5. **Dropdown 键盘**：Enter/Space 打开关闭，↑↓ 导航菜单项，ESC 关闭并返回头像按钮。
6. **ARIA labels**：Sidebar (`aria-label="Main navigation"`)、Modal (`role="dialog"` `aria-modal="true"`)、右侧面板 (`role="complementary"`)、折叠按钮 (`aria-expanded` `aria-controls`) 等。
7. **Focus styles + color contrast**：所有可交互元素使用 `:focus-visible` 高亮（`outline: 2px solid var(--primary)`）。常规文字对比度 ≥ 4.5:1，大文字 ≥ 3:1。

详见附录 D 完整 JavaScript 与 CSS 代码示例。

---

## 五、CSS 变量映射表

TokenRouter 色值 → 我们的新色值建议：

| TokenRouter | 我们当前 | 建议值 | 位置 |
|------------|---------|--------|------|
| `#141821` (body text) | `#1e293b` | `#141821` | `--text` |
| `#e8f2ff` (blue accent) | `#dbeafe` | `#e8f2ff` | `--primary-light` |
| `#3b82f6` (primary blue) | `#3b82f6` | 不变 | `--primary` |
| `#ffffff` (card bg) | `#ffffff` | 不变 | `--bg-card` |
| `#f8fafc` (page bg) | `#f8fafc` | `#f5f7fa` | `--bg` |
| shadow: 极轻 | `0 1px 3px rgba(0,0,0,0.1)` | `0 1px 2px rgba(0,0,0,0.06)` | `--shadow` |
| border: `#e5e7eb` | `#e2e8f0` | `#e5e7eb` | `--border` |

---

## 六、风险与注意事项

### 6.1 Go 模板的限制

- 没有前端路由，每次页面切换是完整 HTTP 请求 → fadeIn 动画需要额外处理
- 没有组件化，卡片样式的重复需要 maintain CSS class 的一致性
- 部分页面（如 `admin-models-general.html` 1412 行）非常大，改造成卡片需谨慎

### 6.2 兼容性

- 所有 CSS 变量改动在暗色模式有对应映射
- 不修改后端 handler 和路由
- i18n key 不变
- 所有改动通过 CSS class rename 或新增 class 实现，不删除旧 class

### 6.3 逐步实施建议（与设计原则 §六 对齐）

**核心思路**：按用户层级（User → Enterprise Admin → System/Super Admin）渐进式重构，先视觉基础，后按用户层级深化。每个 Phase 内**具体 B 项的内容与时间估算见 §四**（本节不重复，避免双重维护）。

| Phase | 主题 | 工作量 | 涉及 B 项（详见 §四） | 用户层级 |
|-------|------|--------|----------------------|----------|
| **Phase 1** | 视觉基础与导航重构 | 5-6 天 | B0, B1, B5, B6, B7, B9, B13 | 全部层级 |
| **Phase 2** | API Keys 重构 | 4-5 天 | B3 + B11（user 层）+ 右侧面板 | 普通用户 |
| **Phase 3** | Enterprise 重构 | 5-6 天 | B10（企业层）+ B8（企业层）+ B2 + B4（企业层）+ B11（enterprise 层） | Enterprise Admin |
| **Phase 4** | Admin 后台优化（v3.3） | 4-5 天 | B10（系统层）+ B4（系统层）+ B11（system 层）+ B12 + B8 | System/Super Admin |
| **合计** | — | **18-22 天** | — | — |

**实施顺序原则**：
1. **Phase 1 优先**：所有用户层都受益，且为后续 Phase 提供视觉基础
2. **Phase 2 → 3 → 4 按用户层级递进**：先解决普通用户最高频场景（API Keys），后处理企业级，最后是系统后台
3. **右侧面板按需启用**：Phase 1 框架就绪，Phase 2/3 在用户实际操作中验证其价值
4. **可并行点**：Phase 1 后期 B3 可与 Phase 2 早期并行；Phase 3 与 Phase 4 也可小规模并行
5. **日历时间**：2-3 人并行开发，预计 **10-14 天**（约 2 周）完成 Phase 1-3

**工作量来源**：
- §四 B0-B13 单项时间累加 = 12-12.5 天（纯实施，不含联调、回归、Code Review）
- 加上 Phase 切换间的集成测试、跨页验证、Code Review、UI 走查约 6-9.5 天
- 总计 **18-22 天**

---

## 七、结论

| 方案 | 工作量 | 效果 | 风险 | 推荐 |
|------|--------|------|------|------|
| A: CSS 微调 | 1-2 天 | 改善 20% | 极低 | 快速 wins |
| **B: UI 优化** | **18-22 天** | **改善 70%** | **低** | **✅ 首选** |
| C: React SPA | 6-10 周 | 改善 95% | 高 | 远期考虑 |

**推荐方案 B**。核心原因是 Go 模板 + CSS 变量已能实现接近 TokenRouter 的视觉效果，主要差距在布局结构（三栏）、信息密度（表格→卡片）、导航体系（sidebar 精简折叠 + 右上角菜单）、Modal 策略及可访问性规范，这些都不需要重构后端。**18-22 天**（按 §六.3 的 4 个 Phase 累加）可将 UI 精致度提升到接近竞品水平。

---

## 八、文档版本历史

| 版本 | 日期 | 状态 | 主要变更 |
|------|------|------|----------|
| v1.0 | 2026-06-03 | DRAFT | 原始 5-7 天方案，全删 modal 立场 |
| v1.1 | 2026-06-04 | DRAFT | 附录 A 加入 TokenRouter = new-api 分析，提出"页面+弹窗混合"修正 |
| v2.0 | 2026-06-05 | ACTIVE | 与 `ui-design-principles-202606051751.md` 对齐，确立 3 用户层级 + Modal 保留清单 |
| **v2.1** | **2026-06-06** | **ACTIVE** | **§四 重构为 B0-B13 共 14 子项 + 附录 B (Panel vs Modal) / C (Inline edit row) / D (a11y) 补充；§六.3 改索引形式；§七 数字统一为 18-22 天；加 §八 版本历史 + §九 实施检查清单** |
| **v2.2** | **2026-06-06** | **ACTIVE** | **TokenRouter 实证修正：B8 导航菜单 → Profile Switcher，B10 扁平单层 → 两级结构（Admin Console + Personal），Phase 3/4 加入 B10/B8 修订项** |

---

## 九、实施检查清单（按 Phase）

> 本清单与 §六.3 的 Phase 划分一一对应，作为实施时的工作追踪项。

### Phase 1 — 视觉基础与导航重构（5-6 天）
> **状态**: 🟢 已完成（2026-06-05）— 全部 9 项 P0 已落地
>
> **Commits**: `92b52a3` Phase 1 主提交 + `b705fa7` / `3fc6847` / `43dfe0e` / `510bbbf` / `45e3d5f` / `4ea73bc` 后续修复
>
> **Doc**: 三层用户标准见 `ui-design-principles-202606051751.md` §1.1（commit `f93bc1f`）

- [x] **B0**: 文档化 3 用户层级分级标准 (文档/标准先行) — `ui-design-principles-202606051751.md` §1.1
- [x] **B1**: 三栏 CSS 框架（默认两栏 + `.has-right-panel` opt-in） — `app.css` 增加 313 行，含 rightPanel block
- [x] **B5**: 动效润色（fadeIn、card hover lift、btn:active scale、toast timing） — `app.css` 动画块
- [x] **B6**: 图标统一 + 空状态插画 — SVG 全部内联 + 统一尺寸（layout 18 处、apikeys 8 处）+ `.empty-state` 体系（app.css:1332+）
- [x] **B7**: Topbar 右侧图标（语言/主题） — `layout.html` topbar icon buttons
- [x] **B8**: 右上角下拉菜单（v1 — 基本版） — Google-style dropdown，role-based admin links
  - ⚠️ **v2 精简**（见 Phase 3/4 B8 修订）：只保留 Profile / (Super Admin) / Logout，Enterprise/System Admin 入口在 sidebar 中
  - ⚠️ **实际状态（2026-06-08 代码核对）**：v1 已实现（含条件 Enterprise/Admin 链接），v2 精简（移除 Enterprise/Admin 入口、仅保留 Profile+Logout）**尚未实施**，但 sidebar 两级结构已完整承载这些入口
- [x] **B9**: 左侧导航可折叠（240px ↔ 64px） — localStorage 记忆，SVG+text 分离
- [x] **B10**: 左侧导航精简（v1 — 扁平个人版） — 6 栏目：Dashboard/API Keys/Subscriptions/Usage/Billing/Redemption
  - ⚠️ **v2 将改为两级结构**（见 Phase 3/4 B10 修订）：TokenRouter 实证 admin 用户需要同时看到管理区+个人区
- [x] **B13**: a11y / 键盘导航基础规范（详见附录 D） — focus-visible、ARIA labels、aria-current

### Phase 2 — API Keys 重构（4-5 天）
> **状态**: 🟢 已完成（2026-06-04）— 全部 P0/P1 项已落地，代码已 commit
>
> **Commit**: `a602fcf` — Phase 2 API Keys page card layout overhaul

- [x] **B3**: 卡片列表 + 统计卡片 + 模型标签展示
  - 表格 → 2 列卡片网格 (`grid-template-columns: repeat(2, 1fr)`)
  - 每张卡片展示：名称、前缀、类型 badge、创建日期、月请求数、月 tokens、路由模式、模型标签列表
  - 模型标签以内联 tag 展示，支持 `flex-wrap: wrap`
  - 统计卡片（总 key 数 / 活跃 key / 本月用量 / 禁用 key）实时刷新
- [x] **B11**: 普通用户层 Drawer/Modal 映射确认并落地
  - **创建 Key** → 右侧 Drawer (`#create-key-drawer`)，含完整表单（名称、类型、分组、过期时间、模型选择、备注）
  - **删除 Key** → 确认 Modal（保留，阻断式）
  - **复制 Key** → 复用 key-created Modal（显示 "Save carefully and Keep in confidential"）
  - **设置 Key** → 保留 Modal（编辑表单，字段多）
- [x] 状态切换：卡片 footer 内新增 `status-toggle` 胶囊按钮
  - 点击调用 `PATCH /api/keys/:id`
  - 实时更新卡片 `data-key-status` + 全局统计计数器
  - i18n 确认对话框（禁用/启用确认提示）
- [x] 个人 Key 创建限制：后端 `apikeys.go` 增加 count 检查，单用户最多 10 个 personal key
- [ ] ~~搜索过滤栏~~ — **Deferred**：当前卡片数量少，搜索过滤优先级降低，后续按需补充

### Phase 3 — Enterprise 重构（5-6 天）
- [x] **B10**: 实现 Enterprise Admin 两层 sidebar（Admin Console + Personal）
- [x] **B2**: Enterprise 仪表盘卡片化
- [x] **B4**: Enterprise 页面卡片化（keys / users / groups / billing / usage / audit / dashboard）
- [x] **B11**: Enterprise Admin 层 Drawer/Modal 映射
- [x] 右侧面板：Enterprise keys/users 详情面板（点击列表项显示右侧详情）
- [x] **B8**: 右上角下拉菜单精简为 Profile + Logout，管理员可见 Enterprise/System Admin 入口（带 Management 分组标签）

### Phase 4 — Admin 后台优化（4-5 天，v3.3）
- [x] **B10**: 实现 System/Super Admin 两层 sidebar（System Admin + Personal）
- [x] **B4**: System/Super admin 表格优化（行高 / padding / hover）
- [x] **B11**: System/Super Admin 层 Drawer/Modal 映射
- [x] **B12**: Model / Key Pool / Provider 页面内表单（详见附录 C Inline edit row）
- [x] **B8**: 右上角下拉菜单加入 Super Admin 入口 ✅ 已完成 — 管理/系统管理员可见入口，普通用户仅 Profile + Logout

---

## 附录 A: TokenRouter 技术深入分析（2026-06-04）

> **⚠️ 立场演进注**: 本附录代表探索期的"页面+弹窗混合"思路早期版本。其核心结论（**查看用面板、写操作用弹窗**）已被 §四 B11/B12 和附录 B/C 吸收为正式设计原则；本附录 §A.4 中关于"全删 modal"的极端立场**已被 §四 B11 Drawer 优先 / Modal 例外 + 附录 C Inline edit row 取代**。阅读时请以 §四 与附录 B/C 为准；本附录作为**历史调研记录**保留，供技术细节参考。

> **背景**: 尝试登录 tokenrouter.com 进一步观察其内部界面，但因技术限制未能成功。以下发现基于公开源码分析（该项目基于开源项目 [new-api](https://github.com/QuantumNous/new-api) 构建）。

### A.1 无法直接登录的技术原因

| 限制项 | 说明 |
|--------|------|
| **架构** | 纯 React SPA，所有路由在前端处理 |
| **认证** | 使用 `pbdAuthBridge.js` 自定义认证桥（非标准 OAuth/JWT） |
| **API 端点** | 所有 `/api/v1/*` POST 请求在登录前均返回 `405 Method Not Allowed` |
| **源码保护** | JS 代码经过混淆和压缩，接口签名不公开 |

**结论**: 无法通过常规方式抓取内部 UI。但该项目基于开源 new-api，其技术栈和设计理念完全透明。

### A.2 TokenRouter = new-api（开源项目）

TokenRouter 的前端直接 fork 自 [QuantumNous/new-api](https://github.com/QuantumNous/new-api)（维护者：Calcium-Ion），核心特征如下：

#### 技术栈
```
Frontend: React 18 + TypeScript + Semi UI (字节跳动设计系统)
Routing:  React Router v6 (前端路由)
State:    自定义 Hook + Context
Styling:  CSS-in-JS (Semi UI 自带)
DarkMode: semiDark (Semi UI 暗色主题，非 CSS 变量)
Icons:    Semi Icons (@douyinfe/semi-icons)
```

#### 与我们的技术差异

| 维度 | TokenRouter (new-api) | 我们 (ai-portal-go) | 可借鉴度 |
|------|----------------------|---------------------|----------|
| **渲染方式** | React SPA（CSR） | Go 模板（SSR） | 低（架构不同） |
| **组件库** | Semi UI（企业级React组件库） | 纯手写 CSS | 中（视觉效果可复刻） |
| **路由** | 前端路由（无刷新切换） | 服务端路由（整页刷新） | 低（可用 HTMX 模拟） |
| **弹窗策略** | 大量使用 Modal + Drawer | 当前大量使用 Modal | **高（不必完全抛弃）** |
| **三栏布局** | ✅ 侧边导航 + 内容 + 右侧面板 | ❌ 当前两栏 | **高（已在方案 B1 中规划）** |
| **卡片密度** | 低（卡片为主，表格极少） | 中（表格密集） | **高（方案 B3/B4 目标）** |
| **暗色模式** | `semiDark`（JS 切换） | CSS 变量切换 | 中（效果等价） |
| **动效** | Semi UI 内置过渡动画 | 手动 CSS animation | 中（可逐一手写） |

### A.3 关键发现：TokenRouter 也使用弹窗，只是用得更克制

new-api 的源码显示，它的弹窗策略是**"查看用面板，操作用弹窗"**：

| 场景 | TokenRouter 方式 | 我们的当前方式 | 建议 |
|------|-----------------|----------------|------|
| **查看详情** | 右侧面板（Drawer）或卡片展开 | 弹窗（Modal） | ✅ 改为右侧面板 |
| **新建/编辑** | 弹窗（Modal）或表单页 | 弹窗（Modal） | ✅ 保留，但简化 |
| **确认删除** | 确认弹窗 | 确认弹窗 | ✅ 保留 |
| **批量操作** | 顶部工具栏 + 批量确认弹窗 | 无批量 | 可引入 |

**核心洞察**: TokenRouter 不是"不用弹窗"，而是**弹窗只用于"写操作"**，而"读操作"全部用右侧面板或卡片展开。这与我们之前 P6 redesign plan 中"全部改为页面内视图切换"的思路不完全一致——P6 plan 过于激进地提议删除所有弹窗。

### A.4 修正后的设计建议

基于对 new-api 源码的理解，建议将 P6 redesign plan 修正为**"页面 + 弹窗混合"**策略：

```
┌─────────────────────────────────────────────┐
│  三栏布局（侧边 240px + 内容 + 右侧 260px）   │
│                                              │
│  ┌──────┐  ┌──────────────────┐  ┌────────┐  │
│  │导航   │  │ 内容区            │  │右侧面板│  │
│  │      │  │                   │  │（详情） │  │
│  │      │  │ 卡片列表           │  │        │  │
│  │      │  │ · 点击→右侧显示详情 │  │ · 只读  │  │
│  │      │  │ · 操作按钮→弹窗    │  │ · 快捷操作│ │
│  └──────┘  └──────────────────┘  └────────┘  │
│                                              │
│  弹窗仅用于：新建、编辑、确认删除               │
│  右侧面板用于：查看详情、上下文信息             │
└─────────────────────────────────────────────┘
```

#### 与 P6 redesign plan（原方案）的差异

| 场景 | P6 原方案（全页面切换） | 修正方案（面板+弹窗混合） | 理由 |
|------|------------------------|--------------------------|------|
| 新建 Key | 跳转到 `/apikeys/new` | 弹窗（保留当前方式） | 表单简单，弹窗不中断上下文 |
| 编辑 Key | 跳转到 `/apikeys/{id}/edit` | 弹窗（保留当前方式） | 同上 |
| 查看 Key 详情 | 页面内展开（当前方案） | **右侧面板**（新） | 不遮挡内容，可对比多个 |
| 模型参数查看 | 弹窗 | **右侧面板**（新） | 参数多，面板空间更大 |
| 删除确认 | 弹窗 | 弹窗（不变） | 标准交互 |
| 创建分组 | 弹窗 | 弹窗（不变） | 表单简单 |

#### 实施修正

1. **保持 P6 plan 的"视图切换"架构**，但范围缩小：
   - 仅用于"查看/详情"场景 → 右侧面板
   - "写操作"（新建/编辑/删除）仍用弹窗

2. **右侧面板使用 HTMX 加载**：
   ```html
   <aside class="right-panel" id="right-panel">
     <!-- HTMX 动态加载详情内容 -->
   </aside>
   ```
   ```html
   <!-- 列表中点击某行 -->
   <tr hx-get="/apikeys/{{.ID}}/details" hx-target="#right-panel">
   ```

3. **弹窗简化**：
   - 去掉表单中的 tab 切换（如 models-aws 的 Parameters/Settings/Advanced）
   - 改为单列表单，减少弹窗高度
   - 使用 `hx-post` 提交后关闭弹窗 + 刷新列表

### A.5 new-api 的视觉细节可复刻项

| 元素 | new-api 实现 | 我们的复刻方式 |
|------|-------------|---------------|
| **卡片圆角** | `border-radius: 8px` | 已有 `--radius: 12px`，可调为 `8px` |
| **卡片阴影** | `box-shadow: 0 2px 8px rgba(0,0,0,0.04)` | 调 `--shadow` 变量 |
| **表格行高** | `height: 56px`（较宽松） | 调 `.data-table td { padding: 14px 16px }` |
| **按钮圆角** | `border-radius: 6px` | 已有 `8px`，接近 |
| **标签页样式** | 底部边框 + 无背景 | `.tabs { border-bottom: 1px solid var(--border) }` |
| **空状态** | 居中插画 + 文字 + 按钮 | 已有 `.empty-state`，可加强插画 |
| **加载状态** | Skeleton 骨架屏 | 已有，可扩展为全页骨架 |
| **Badge 样式** | 圆点 + 文字，无背景色块 | 已有 `.badge`，可更轻量 |

### A.6 结论

TokenRouter（new-api）给我们的最大启示：

1. **Drawer 是通用侧栏容器**（v6/v7 实测）：同时承载 read（模型详情）和 write（创建 API Key），**无 backdrop**，点击外部关闭。不是"只读面板"。
2. **Modal 仅用于必须阻断的场景**：删除确认、支付、阻断式提示。
3. **页面切换（Page）**: 承载"独立流程"——如结算、订阅购买、设置向导。

修正后的策略：
- **右侧 Drawer**: 默认容器——详情查看、创建表单、编辑表单（保持列表上下文）
- **弹窗（Modal）**: 仅例外——删除确认、充值支付、阻断提示、合规章程
- **页面切换（Page）**: 独立流程——结算、订阅购买

这个策略比 P6 原方案（全删除弹窗）更务实，也比"Panel=只读 / Modal=写操作"的二分法更灵活。详见附录 B 对照表。

---

## 附录 B：右侧 Drawer 与 弹窗（Modal）使用场景对照

> **TokenRouter 实证修正**（v6/v7 实测）：TokenRouter 的右侧 Drawer 同时承载 read（模型详情）和 write（创建 API Key），**无 backdrop**，点击外部关闭。因此 Drawer 是**通用侧栏容器**，不限于只读。
>
> 本附录明确 Drawer 与 Modal 的使用边界，避免实施时混淆。

### B.1 核心差异

| 维度 | 右侧 Drawer | 弹窗（Modal） |
|------|-------------|--------------|
| **位置** | 从右侧滑出，宽度 640-700px | 居中弹出 |
| **遮罩层（Backdrop）** | ❌ **无**（TokenRouter 实证） | ✅ **有半透明遮罩** |
| **阻断用户操作** | ❌ **不阻断**，可继续浏览列表 | ✅ **阻断**，必须处理完才能继续 |
| **主内容区可见** | ✅ 列表和 Drawer **同时可见** | ❌ 被遮罩层**完全遮挡** |
| **ESC / 点击外部关闭** | 点击外部区域或 × 按钮关闭 | 点击遮罩层或 ESC 关闭 |
| **内容类型** | **通用容器**（只读详情、编辑表单、创建表单） | **阻断式容器**（确认、支付、提示） |

### B.2 使用场景矩阵

| 场景 | Drawer | Modal | 理由 |
|------|:--:|:--:|------|
| 查看 API Key 详情 | ✅ | ❌ | 保持列表上下文，可对比多个 Key |
| 查看模型参数详情 | ✅ | ❌ | 参数多，Drawer 空间更宽 |
| **创建 API Key** | ✅ | ❌ | TokenRouter 实证 = Drawer |
| **编辑 API Key** | ✅ | ❌ | TokenRouter 实证 = Drawer |
| **创建 Group / Enterprise** | ✅ | ❌ | 表单复杂但不需要阻断 |
| 查看企业概览 | ✅ | ❌ | 上下文信息 |
| 编辑 Model（3-5 字段） | ✅ / Inline | ❌ | Inline edit row 或 Drawer |
| **删除确认** | ❌ | ✅ | 必须阻断，防止误删 |
| **充值支付** | ❌ | ✅ | 涉及金钱，必须阻断 |
| **余额不足提示** | ❌ | ✅ | 阻断式，必须处理 |
| **首次合规章程** | ❌ | ✅ | 一次性，必须接受 |

### B.3 Drawer 的典型内容

Drawer 是**通用容器**，可包含任意内容（只读信息或表单）：

**只读详情示例**（模型价格）：
```
┌─ [logo] qwen/qwen3.7-plus ──── × ┐
│ [Text]                             │
│                                    │
│ 🔗 API Endpoints                   │
│   OpenAI-compatible POST /v1/...   │
│                                    │
│ 💲 Price               [Billing ▾] │
│   Input  $0.400 / ≤256K Tokens     │
│   Input  $1.200 / >256K Tokens     │
│   Output $1.600 / ≤256K Tokens     │
│   ...                              │
└────────────────────────────────────┘
```

**编辑表单示例**（创建 API Key）：
```
┌─ Create New Key ───────────────── × ┐
│                                     │
│ 📋 Basic Information                │
│   Name * [________________]         │
│   Expiration [Never▾]               │
│   Quantity [^ 1 v]                  │
│                                     │
│ 💲 Quota Settings                   │
│   Unlimited Quota [●○]              │
│                                     │
│ 🧊 Models                             │
│   Allowed Models [Select... ▾]      │
│                                     │
│        [Cancel]   [Submit]          │
└─────────────────────────────────────┘
```

---

## 附录 C：页面内表单的具体实现 — Inline edit row

> 设计原则 §4.3 规定 System/Super admin 的 Key/Model/Provider 改为"页面内表单"。本附录明确具体实现方式。

### C.1 策略：按操作类型区分

| 操作 | 实现方式 | 适用页面 |
|------|---------|---------|
| **创建（Create）** | 页面内表单视图（列表上方展开大表单） | admin-keypool、admin-models-general、admin-providers |
| **编辑（Edit）** | **Inline edit row**（行下方展开内联表单） | 同上 |
| **删除（Delete）** | 确认 Modal（阻断，防止误操作） | 所有页面 |

### C.2 Inline edit row 实现

```html
<!-- 表格行 -->
<tr id="row-001">
  <td>gpt-4</td>
  <td>Active</td>
  <td>0.03</td>
  <td>
    <button hx-get="/admin/models/001/edit-row"
            hx-target="#row-001"
            hx-swap="outerHTML">
      ✏️
    </button>
  </td>
</tr>

<!-- 点击编辑后替换为 -->
<tr id="row-001">
  <td colspan="4">
    <form class="inline-edit-form" hx-post="/admin/models/001" hx-target="#row-001">
      <div class="inline-edit-fields">
        <label>Name <input name="name" value="gpt-4"/></label>
        <label>Status
          <select name="status">
            <option value="active" selected>Active</option>
            <option value="disabled">Disabled</option>
          </select>
        </label>
        <label>Price <input name="price" value="0.03"/></label>
      </div>
      <div class="inline-edit-actions">
        <button type="submit" class="btn btn-sm btn-primary">Save</button>
        <button type="button" class="btn btn-sm btn-ghost"
                hx-get="/admin/models/001/row" hx-target="#row-001">Cancel</button>
      </div>
    </form>
  </td>
</tr>
```

```css
.inline-edit-form {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 16px;
  margin: 4px 0;
}

.inline-edit-fields {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
}

.inline-edit-fields label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 13px;
}

.inline-edit-actions {
  margin-top: 12px;
  display: flex;
  gap: 8px;
}
```

### C.3 Inline edit row 的适用边界

| 条件 | 适合 Inline edit row | 不适合 Inline edit row |
|------|----------------------|----------------------|
| 字段数量 | 3-8 个 | 10+ 个 |
| 字段复杂度 | 简单 input/select | 有联动（如 key_type → 显示不同选项） |
| 验证复杂度 | 简单必填/格式 | 复杂交叉验证 |
| 操作频率 | 频繁修改多条记录 | 偶尔操作 |
| 错误处理 | 单行内显示错误 | 需要大面积错误展示 |

**结论**：Inline edit row 适合 System/Super admin 的表格页面（Model、Key Pool、Provider 的**编辑**操作）。**创建**操作仍使用页面内表单视图（列表上方展开），**删除**使用确认 Modal。

### C.4 不适用的页面

以下页面**不使用** Inline edit row，按设计原则保留 Modal：

- **API Key 创建/编辑**：字段多、有联动、需复制 key 值 → **Modal**
- **Enterprise key/group 编辑**：企业级操作 → **Modal**
- **Group/Enterprise 创建/编辑**：系统级操作 → **Modal**
- **充值支付**：涉及金钱 → **Modal**

---

## 附录 D：可访问性（a11y）与键盘导航规范

> 本附录确保 UI 改造不仅视觉美观，也满足键盘操作和辅助技术需求。

### D.1 Modal 焦点陷阱（Focus Trap）

Modal 打开时，键盘焦点必须限制在 Modal 内部，不能 Tab 到背景内容。

```javascript
// 全局焦点陷阱（添加到 page-common.js 或独立 a11y.js）
document.addEventListener('keydown', (e) => {
    if (e.key !== 'Tab') return;
    
    const modal = document.querySelector('.modal.active');
    if (!modal) return;
    
    const focusable = Array.from(modal.querySelectorAll(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    )).filter(el => !el.disabled && el.offsetParent !== null);
    
    if (focusable.length === 0) return;
    
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    
    if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
    }
});
```

**Modal 打开/关闭的焦点管理**：

```javascript
function openModal(id) {
    const modal = document.getElementById(id);
    modal.classList.add('active');
    modal.setAttribute('aria-hidden', 'false');
    
    // 保存触发元素，关闭后焦点返回
    modal._triggerElement = document.activeElement;
    
    // 焦点移到 Modal 内第一个可聚焦元素
    const focusable = modal.querySelector('button, input, select, textarea, [href]');
    if (focusable) focusable.focus();
}

function closeModal(id) {
    const modal = document.getElementById(id);
    modal.classList.remove('active');
    modal.setAttribute('aria-hidden', 'true');
    
    // 焦点返回触发元素
    if (modal._triggerElement) modal._triggerElement.focus();
}
```

### D.2 ESC 全局关闭

```javascript
document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    
    // 优先级：Modal > Dropdown > Right Panel
    const modal = document.querySelector('.modal.active');
    if (modal) { closeModal(modal.id); return; }
    
    const dropdown = document.querySelector('.dropdown-menu.active');
    if (dropdown) { dropdown.classList.remove('active'); return; }
    
    const panel = document.querySelector('.right-panel.active');
    if (panel) { closePanel(); return; }
});
```

### D.3 Sidebar 键盘导航

**Tab 顺序**：
```
Logo → 语言切换 → 主题切换 → 头像下拉 → 左侧导航第1项 → ... → 主内容区
```

**实现要求**：
- 导航项使用 `<a>` 或 `<button>`，天然可聚焦
- 当前页面导航项添加 `aria-current="page"`
- 折叠状态：Tab 顺序不变，`.nav-text` 通过 `visibility: hidden`（非 `display: none`）保持可访问性

```html
<nav class="sidebar" aria-label="Main navigation">
    <a href="/dashboard" class="nav-item" aria-current="page">
        <svg class="nav-icon" aria-hidden="true">...</svg>
        <span class="nav-text">Dashboard</span>
    </a>
    <!-- ... -->
</nav>
```

### D.4 Right Panel 键盘操作

- 面板打开时：内部元素参与 Tab 顺序
- 面板关闭时：`display: none` 使内部元素脱离 Tab 顺序
- ESC 关闭面板，焦点返回到触发元素（点击的行）

```javascript
function openPanel(url, triggerElement) {
    const panel = document.getElementById('right-panel');
    panel._triggerElement = triggerElement;
    panel.classList.add('active');
    // HTMX 加载内容...
}

function closePanel() {
    const panel = document.getElementById('right-panel');
    panel.classList.remove('active');
    if (panel._triggerElement) panel._triggerElement.focus();
}
```

### D.5 Dropdown 键盘操作

右上角用户下拉菜单：

```html
<div class="user-dropdown">
    <button class="avatar-btn"
            aria-haspopup="true"
            aria-expanded="false"
            aria-label="User menu">
        <img src="{{.Avatar}}" alt="" />
    </button>
    <ul class="dropdown-menu" role="menu" aria-label="User menu">
        <li role="none"><a href="/enterprise" role="menuitem">Enterprise Console</a></li>
        <li role="none"><a href="/admin" role="menuitem">System Admin</a></li>
        <li role="separator" aria-hidden="true"></li>
        <li role="none"><a href="/profile" role="menuitem">Profile</a></li>
        <li role="none"><a href="/logout" role="menuitem">Logout</a></li>
    </ul>
</div>
```

**键盘行为**：
- Enter/Space：打开/关闭下拉菜单
- Tab：在菜单项之间移动
- Enter/Space：激活菜单项
- ESC：关闭菜单，焦点返回头像按钮
- 点击外部：关闭菜单

### D.6 ARIA Labels 清单

| 元素 | ARIA 属性 | 说明 |
|------|----------|------|
| Sidebar | `aria-label="Main navigation"` | 主导航区域 |
| 当前导航项 | `aria-current="page"` | 标识当前页面 |
| Modal overlay | `role="dialog"` `aria-modal="true"` | 对话框 |
| Modal 标题 | `aria-labelledby="modal-title"` | 标题关联 |
| 右侧面板 | `role="complementary"` `aria-label="Details panel"` | 辅助内容 |
| 折叠按钮 | `aria-expanded="true/false"` `aria-controls="sidebar"` | 侧边栏折叠 |
| 主题切换 | `aria-label="Toggle theme (dark/light/system)"` | 主题按钮 |
| 语言切换 | `aria-label="Switch language"` | 语言按钮 |
| 空状态 | `role="status"` | 状态提示 |
| Toast | `role="alert"` `aria-live="polite"` | 通知提示 |
| 骨架屏 | `aria-busy="true"` | 加载中 |
| Inline edit | `aria-label="Edit row"` | 行内编辑 |

### D.7 焦点样式与色彩对比度

**焦点样式**：所有可交互元素必须有可见焦点指示器

```css
/* 全局焦点样式 */
*:focus-visible {
    outline: 2px solid var(--primary);
    outline-offset: 2px;
}

/* 避免 outline: none 无替代方案 */
button:focus-visible,
a:focus-visible,
input:focus-visible,
select:focus-visible {
    outline: 2px solid var(--primary);
    outline-offset: 2px;
}
```

**色彩对比度（WCAG AA）**：
- 常规文字：对比度 ≥ 4.5:1
- 大文字（18px+ bold / 24px+ normal）：对比度 ≥ 3:1
- 暗色模式下检查 `--text-muted` 和 `--text` 的对比度
