# UI 设计原则与实施指导

> **状态**: ACTIVE — 实施指导
> **日期**: 2026-06-06
> **关联**:
> - `docs/ui-improvement-proposal-plan-202606061645.md` — 主实施文档（视觉打磨 + 交互重构）
> - `docs/frontend-page-redesign-plan-P6-20260605.md` — ARCHIVED，技术参考
> - `docs/ui-improvement-discussion-202606061600.md` — 讨论记录，决策参考

---

## 一、总体原则

### 1.1 三层界面标准

| 层级 | 目标用户 | 信息密度 | 设计要求 |
|------|---------|---------|---------|
| **用户页** | 消费者 | 低 | 大量留白，卡片化，简洁美观，低认知负荷 |
| **Enterprise Admin** | 专业用户 | 中 | 类似 Google Workspace，功能丰富但信息架构清晰 |
| **System/Super Admin** | 管理员 | 中高 | 功能优先，尽量美观，减少疲劳和误操作 |

### 1.2 技术栈

- **前端**: Go 模板 + CSS 变量 + HTMX + Alpine.js
- **不引入**: Semi UI、React、Tailwind CSS（避免和 new-api 等大众模板雷同）

---

## 二、布局原则

### 2.1 三栏按需出现

- 参考 TokenRouter 三栏布局，但**右栏仅在必要页面出现**
- 通常保持两栏（sidebar + main content），内容区更宽敞
- 右栏用于：Dashboard 概览、API Key 详情、Enterprise 统计等上下文信息
- 右栏默认隐藏，点击某行或按钮后滑出（类似 TokenRouter Drawer）

### 2.2 左侧导航栏可折叠

- **展开状态**：240px，显示 SVG 图标 + 文字
- **折叠状态**：64px，仅显示 SVG 图标居中
- SVG 图标与导航文字**分离处理**，折叠时文字隐藏，图标不受影响
- `localStorage` 记住用户折叠偏好
- CSS `transition` 动画过渡

---

## 三、导航结构

### 3.1 导航层级总则

**核心原则（TokenRouter 实证）**：用户不应通过下拉菜单"切换上下文"来访问不同层级的页面。相反，sidebar 应**同时展示**用户有权限访问的所有层级，通过区域分组清晰区分。

| 用户角色 | Sidebar 层级 | 说明 |
|---------|-------------|------|
| 普通用户 | Personal 单层 | 只有个人导航，扁平 5-6 项 |
| Enterprise Admin | Admin Console + Personal 双层 | 企业区展开 + 个人区可折叠 |
| System/Super Admin | System Admin + Personal 双层 | 系统管理区展开 + 个人区可折叠 |

### 3.2 左侧导航（两层结构）

#### 3.2.1 普通用户 — 单层（5~6 项）

| 栏目 | 包含内容 | 说明 |
|------|---------|------|
| Dashboard | — | 用户仪表盘 |
| API Keys | — | Key 管理 |
| Subscriptions | 订阅管理 + 我的订阅 | 合并订阅相关入口，常用放前面 |
| Usage | 用量查询 + 使用记录 + 计费详情 | 独立页面，信息密度可较高 |
| Billing | 充值 + 订单历史 + 发票 + 地址管理 | 合并计费相关入口 |
| Redemption | 兑换码 + 欢迎码 | 合并兑换相关入口 |

#### 3.2.2 Enterprise Admin — 双层（参考 TokenRouter）

```
┌─ Sidebar ────────────────────────────┐
│                                       │
│  ▼ Admin Console         ← 区域标题   │
│    ├─ Overview                        │
│    ├─ Members                         │
│    ├─ Groups                          │
│    ├─ Keys                            │
│    ├─ Billing                         │
│    ├─ Usage                           │
│    └─ Audit                           │
│                                       │
│  ▷ Personal              ← 可折叠     │
│    ├─ Dashboard                       │
│    ├─ API Keys                        │
│    ├─ Subscriptions                   │
│    ├─ Billing Details                 │
│    ├─ Billing                         │
│    └─ Redemption                      │
│                                       │
│  Collapse Sidebar                     │
└───────────────────────────────────────┘
```

**设计要点**：
- 区域标题（`sidebar-group-label`）是一个 `<button>`，点击展开/折叠下方列表
- **Admin Console 默认展开**（高频使用），**Personal 默认折叠**（低频使用时收起，减少视觉噪音）
- 折叠状态通过 localStorage 记住
- 普通用户不受影响，仍为扁平单层

#### 3.2.3 System/Super Admin — 双层

```
┌─ Sidebar ────────────────────────────┐
│                                       │
│  ▼ System Admin         ← 区域标题    │
│    ├─ Overview                        │
│    ├─ Users                           │
│    ├─ Groups                          │
│    ├─ Enterprise                      │
│    ├─ Key Pool                        │
│    ├─ Models                          │
│    ├─ Providers                       │
│    ├─ Subscriptions                   │
│    ├─ ...（核心管理项，不超过 12 项）  │
│    └─ Settings                        │
│                                       │
│  ▷ Personal              ← 可折叠     │
│    ├─ Dashboard                       │
│    ├─ API Keys                        │
│    └─ ...                             │
│                                       │
│  Collapse Sidebar                     │
└───────────────────────────────────────┘
```

（具体栏目以 i18n key 为准，实施时调整）

### 3.3 右上角下拉菜单

> **原则**：侧栏本身已展示用户有权限访问的所有层级，因此下拉菜单**不需要承担导航功能**，只放个人操作入口。

```
┌─────────────────────┐
│ 👤 Profile          │  ← 所有用户
│ ─────────────────── │
│ 🔒 Super Admin      │  ← 仅 super_admin（低频操作）
│ ─────────────────── │
│ 🚪 Logout           │  ← 所有用户
└─────────────────────┘
```

**规则**：
- **Profile** 不在左侧导航，统一放在下拉菜单中
- **Super Admin** 入口放在下拉菜单中（超级管理员设置使用频率极低，不需要在 sidebar 占用空间）
- Enterprise Admin 和 System Admin 的所有入口**都在 sidebar 中**，不下放到 dropdown
- 普通用户下拉菜单仅两项：Profile / Logout

### 3.4 Topbar 右侧图标

```
[Logo]  [页面标题]                    [🌐 语言] [🌓 主题] [头像 ▼]
```

- **语言选择器**：图标按钮，点击切换 zh/en
- **主题切换器**：图标按钮，dark / light / system 三态循环切换

---

## 四、Drawer 优先与 Modal 例外策略

> **TokenRouter 实证修正**（v6/v7 实测）：TokenRouter 的右侧 Drawer 同时承载 read（模型详情）和 write（创建 API Key），**无 backdrop**，点击外部关闭。因此我们的策略从"Panel=只读 / Modal=写操作"修正为**"Drawer=默认容器 / Modal=仅例外"**。

### 4.1 核心原则

**Drawer 优先**：列表页的创建、编辑、详情查看**优先使用右侧 Drawer**（保持列表上下文，不阻断操作流程）。

**Modal 例外**：仅以下 4 类**必须阻断**的场景使用 Modal：
1. **删除确认**（防止误删）
2. **支付/充值**（涉及金钱，行业惯例）
3. **阻断式提示**（余额不足、权限不足等必须处理才能继续）
4. **合规章程**（首次使用必须接受）

### 4.2 使用 Drawer 的场景（默认）

| 用户层级 | 使用 Drawer 的操作 | 理由 |
|---------|------------------|------|
| **普通用户** | API Key 创建/编辑 | TokenRouter 实证 = Drawer，保持列表上下文 |
| | 模型详情查看 | TokenRouter 实证 = 卡片点击打开 Drawer |
| **Enterprise Admin** | Enterprise key 创建/编辑 | 表单复杂但不需要阻断 |
| | Group 创建/编辑 | 企业级操作但可在 Drawer 内完成 |
| **System/Super Admin** | Group/Enterprise 创建/编辑 | 系统级操作，Drawer 空间足够 |
| | Model/Key Pool/Provider 编辑 | Inline edit row 或 Drawer |

### 4.3 使用 Modal 的场景（例外）

| 场景 | 使用 Modal | 理由 |
|------|-----------|------|
| 删除确认（所有层级） | ✅ Modal | 必须阻断，防止误删 |
| 充值/支付 | ✅ Modal | 涉及金钱，必须专注 |
| 余额不足提示 | ✅ Modal | 阻断式，必须处理 |
| 首次合规章程 | ✅ Modal | 一次性，必须接受 |

### 4.4 实施原则

- **Drawer 决策优先**：新增页面/功能时，先考虑是否能放 Drawer，不能才用 Modal
- 每个使用 Modal 的场景在代码中以注释标注 `"MODAL_EXCEPTION: [原因]"`
- 维护一份 **Drawer/Modal 使用清单**，随代码同步更新
- 具体实施时按页面逐一沟通确认

---

## 五、视觉风格

### 5.1 用户页

- 大量留白，卡片化
- 信息密度低，低认知负荷
- 参考 TokenRouter、Vercel 风格

### 5.2 无缝布局（参考 TokenRouter）

- **Sidebar 与主内容区无分隔线**：sidebar 背景与主内容区背景同色（`var(--bg)`），去掉 `border-right`
- **无间隙**：sidebar 和 main content 紧密相邻，无 margin/gap
- **滚动条融合**：自定义 scrollbar 颜色与背景同色，几乎不可见
- 仅靠导航项的 hover/active 状态区分 sidebar 区域

### 5.2 Enterprise Admin

- 类似 Google Workspace / Gmail 设置 / Chrome 设置
- 内容可较多，但信息架构必须清晰有序

### 5.3 System/Super Admin

- 后台管理，复杂可接受
- 尽量美观，操作便捷
- 减少 admin 视觉疲劳和误操作

---

## 六、实施优先级

```
Phase 1 — 视觉基础（3-4 天）
  ├── 左侧导航精简 + 可折叠
  ├── Topbar 右侧图标（语言/主题）
  ├── 右上角下拉菜单（Profile/Admin/Logout）
  ├── 三栏布局框架（CSS + layout.html）
  └── 主题/动效润色

Phase 2 — API Keys 重构（4-5 天）
  ├── 卡片列表替代表格
  ├── 右侧面板显示详情
  └── 保留 create/edit/delete modal

Phase 3 — Enterprise 重构（5-6 天）
  ├── 6 个页面信息架构重组
  ├── 卡片化 + 视图切换
  └── 保留关键操作 modal

Phase 4 — Admin 后台优化（4-5 天，v3.3）
  ├── 部分页面改为页面内表单
  ├── 保留 Group/Enterprise modal
  └── 表格优化（行高、padding、hover）
```

---

## 七、文档版本历史

| 版本 | 日期 | 变更 |
|------|------|------|
| 1.0 | 2026-06-06 | 初始版本，汇总 10 条设计原则及补充确认 |
| 1.1 | 2026-06-06 | §三 重构：新增 3.1 导航层级总则 + 3.2 三层两侧栏结构 + 3.3 Profile Switcher（TokenRouter 实证：sidebar 两级展示，dropdown 改为上下文切换器） |
