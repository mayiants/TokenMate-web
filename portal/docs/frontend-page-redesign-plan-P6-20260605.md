# 前端页面交互改造方案：从弹窗到视图切换

> **HISTORICAL** — Interactive refactoring approach merged into `ui-improvement-proposal-plan-202606061645.md`
>
> 日期: 2026-06-05
> 关联: C2 P6、P1~P5 已完成
> 范围: 管理后台复杂操作页面（admin-enterprise、apikeys、admin-models、admin-subscriptions 等）
> **具体实施方案参见**: `docs/ui-improvement-proposal-plan-202606061645.md` §四（方向 B：交互重构）

---

## 一、改造出发点

### 1.1 当前问题：弹窗（Modal）已成为瓶颈

P1~P5 完成后，前端 JS 已提取到独立文件，权限判断已下沉到 Go，简单操作已转为 form POST。但 **11 处复杂操作**仍保留在弹窗中，弹窗本身成为了最大的交互障碍：

| 问题 | 现象 | 影响 |
|------|------|------|
| 空间拥挤 | `max-width: 500-600px`，8+ 字段的表单密集堆叠 | 用户填错率高，视觉疲劳 |
| 层级叠加 | 弹窗内再打开确认弹窗（如 apikeys 创建成功后显示 key 值） | z-index 管理复杂，ESC/点击 backdrop 行为不一致 |
| 移动端不可用 | 小屏上弹窗几乎占满全屏，关闭按钮难点击 | 手机端管理后台体验极差 |
| 模板臃肿 | 每个页面嵌入 2-6 个 modal HTML 结构 | admin-enterprise 304 行中 ~160 行是 modal；admin-models-general 575 行中 ~250 行是 modal |
| JS 控制复杂 | `openModal()`/`closeModal()`/`z-index`/`backdrop`/`focus trap` | 大量与业务无关的 UI 控制代码 |
| 上下文丢失 | 弹窗关闭后回到列表，用户不知道刚才编辑的是哪一行 | 需要额外的滚动定位或高亮逻辑 |

### 1.2 为什么现在改

P1~P5 已经清除了以下障碍：
- ✅ JS 已提取到独立文件，模板结构清晰
- ✅ 权限判断在 Go 层，模板只负责渲染
- ✅ C1 统一了数据层，表单提交后 redirect 即可拿到最新数据
- ✅ 简单 fetch() 已转为 form POST，验证了"表单提交 + redirect + toast"模式可行

此时改造弹窗，只需动 UI 层，不动数据层，风险可控。

---

## 二、设计思路：参考 Chrome/Edge 设置页面

### 2.1 核心原则

> **取消弹窗。点击"创建/编辑"后，在同一页面的主内容区切换到表单视图；保存/取消后回到列表视图。**

### 2.2 Chrome 设置的交互模式

Chrome 设置页没有弹窗：
- 左侧是导航，右侧是内容区
- 点击某一项设置 → 右侧内容切换为对应表单
- 表单有"保存"/"取消"或自动保存
- 用户始终知道自己在哪，点击"返回"即可回去

### 2.3 我们的适配

```
┌─────────────────────────────────────────────┐
│  Sidebar  │  Main Content Area (#main-content)│
│           │                                   │
│           │  ┌─────────────────────────────┐  │
│           │  │  Toolbar: [创建] [筛选]      │  │
│           │  └─────────────────────────────┘  │
│           │  ┌─────────────────────────────┐  │
│           │  │  List Table                 │  │  ← 列表视图（默认）
│           │  │  ...                        │  │
│           │  └─────────────────────────────┘  │
│           │                                   │
│           │  点击"创建" → hx-get="/admin/enterprise/new"
│           │                                   │
│           │  ┌─────────────────────────────┐  │
│           │  │  [← 返回列表]               │  │
│           │  │  创建企业                   │  │  ← 表单视图（替换 main-content）
│           │  │  [表单字段...]              │  │
│           │  │  [保存] [取消]              │  │
│           │  └─────────────────────────────┘  │
│           │                                   │
│           │  表单 POST → 302 redirect → 列表视图
└─────────────────────────────────────────────┘
```

**关键交互细节**：
1. 列表视图和表单视图**共享同一个 URL 前缀**（如 `/admin/enterprise`）
2. 点击编辑时，地址栏变为 `/admin/enterprise?id=xxx&view=edit`（或 push-state）
3. 刷新页面后 still works —— Go handler 根据 `view` 参数渲染对应视图
4. 保存后 redirect 回列表视图，显示 toast
5. 取消直接 `history.back()` 或 redirect

---

## 三、技术方案：HTMX 视图切换

### 3.1 为什么选 HTMX

| 方案 | 工作量 | 与现有架构兼容性 | 评估 |
|------|--------|-----------------|------|
| 保留 Modal + HTMX 化 fetch | 2-3 天 | 高 | modal 问题仍在 |
| **HTMX 视图切换（推荐）** | **5-7 天** | **极高** | **删除全部 modal，体验最佳** |
| 独立编辑页面（`/enterprise/:id/edit`） | 4-6 天 | 高 | 有页面跳转闪烁 |
| React SPA 重写 | 6-8 周 | 低 | 完全不匹配团队现状 |

HTMX 视图切换的优势：
- 不需要新增路由路径（query string 区分视图）
- 不需要写一行 fetch/axios
- 后端仍是 Go template 渲染
- 与 P3 的 "form POST + redirect" 模式无缝衔接

### 3.2 后端架构

每个需要改造的页面，新增两个 handler：

```go
// 1. 列表视图（已有，只需加视图切换参数处理）
func (h *Handler) HandleAdminEnterprise(c *gin.Context) {
    view := c.Query("view")  // "", "new", "edit"
    id := c.Query("id")

    data := query.GetAdminEnterprisesData(...)

    switch view {
    case "new":
        c.HTML(200, "admin-enterprise.html", gin.H{
            "View": "form",
            "Mode": "create",
            "Data": data,  // 保留列表数据，用于"返回列表"
        })
    case "edit":
        enterprise := query.GetEnterpriseByID(...)
        c.HTML(200, "admin-enterprise.html", gin.H{
            "View": "form",
            "Mode": "edit",
            "Enterprise": enterprise,
            "Data": data,
        })
    default:
        c.HTML(200, "admin-enterprise.html", gin.H{
            "View": "list",
            "Data": data,
        })
    }
}

// 2. 表单提交（已有，P3 模式）
func (h *Handler) HandleCreateEnterprise(c *gin.Context) {
    // ... 解析表单、保存数据库
    c.Redirect(302, "/admin/enterprise?toast=success&message=Created")
}
```

**设计决策**：**不拆分为独立模板文件**，而是在同一个模板内用 `{{if eq .View "form"}}` 控制渲染表单或列表。原因：
- 减少模板文件数量
- "返回列表"时不需要重新加载整个页面（HTMX `hx-swap`）
- 表单和列表共享 layout 和 CSS

### 3.3 前端交互

```html
<!-- 列表视图中的"创建"按钮 -->
<button hx-get="/admin/enterprise?view=new"
        hx-target="#main-content"
        hx-push-url="true"
        class="btn btn-primary">
    创建企业
</button>

<!-- 列表视图中的"编辑"按钮 -->
<button hx-get="/admin/enterprise?id={{.ID}}&view=edit"
        hx-target="#main-content"
        hx-push-url="true"
        class="btn btn-sm btn-ghost">
    编辑
</button>
```

```html
<!-- 表单视图（模板内） -->
{{if eq .View "form"}}
<div class="page-form-view">
    <a href="/admin/enterprise" class="back-link">← 返回列表</a>
    <h2>{{if eq .Mode "create"}}创建企业{{else}}编辑企业{{end}}</h2>

    <form method="POST" action="/admin/enterprise">
        <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        {{if eq .Mode "edit"}}<input type="hidden" name="id" value="{{.Enterprise.ID}}">{{end}}

        <!-- 表单字段 -->
        <div class="form-group">...

        <div class="form-actions">
            <button type="submit" class="btn btn-primary">保存</button>
            <a href="/admin/enterprise" class="btn btn-secondary">取消</a>
        </div>
    </form>
</div>
{{else}}
<!-- 列表视图（原有内容） -->
<div class="page-list-view">...
{{end}}
```

### 3.4 需要保留 JS 的复杂交互

不是所有交互都能纯化为 HTML form。以下场景**保留 JS**，但**不再装在 modal 里**：

| 场景 | 当前实现 | 新方案 |
|------|---------|--------|
| apikeys create：选择 key_type 后显示不同选项 | JS `onchange` 切换 section | 保留 JS，在表单视图内切换 |
| apikeys create：选择 group 后异步加载模型列表 | JS `fetch()` 加载 | 保留 JS，改为 `htmx:get` 触发局部刷新 |
| apikeys create：模型拖拽排序 | JS drag-and-drop | 保留 JS，在表单视图内实现 |
| enterprise members：invite/remove/approve | JS 动态刷新 members 列表 | 保留为独立子页面 `/enterprise/:id/members`，或表单视图内嵌 HTMX 局部刷新 |
| models routing：添加/删除路由规则 | JS 动态表格 | 表单视图内嵌 HTMX 局部刷新 |

**原则**：只有"打开/关闭一个黑色半透明遮罩层并在中间显示内容"这个行为被删除。所有业务逻辑 JS 保留，只是容器从 modal 变为页面内的 div。

---

## 四、各页面适配策略

### 4.1 简单表单页面（低复杂度）

| 页面 | 操作 | 当前 Modal 字段 | 新方案 | 工作量 |
|------|------|----------------|--------|--------|
| admin-enterprise | create | 7 字段 | 表单视图 | 0.5 天 |
| admin-enterprise | edit | 8 字段 | 表单视图 | 0.5 天 |
| admin-enterprise | topup | 2 字段 | 表单视图 | 0.5 天 |
| admin-subscriptions | edit plan | 6 字段 | 表单视图 | 0.5 天 |
| admin-models | add model | 7 字段 | 表单视图 | 0.5 天 |
| admin-models | edit model | 7 字段 | 表单视图 | 0.5 天 |
| admin-models | confirm model | 1 字段 + 说明 | 表单视图 | 0.5 天 |
| admin-models | eol pending/edit | 2-3 字段 | 表单视图 | 0.5 天 |

### 4.2 复杂交互页面（中/高复杂度）

| 页面 | 操作 | 复杂度 | 新方案 | 工作量 |
|------|------|--------|--------|--------|
| apikeys | create key | **高** | 表单视图 + 保留全部 JS 交互（key_type 选择、group→模型联动、拖拽排序） | 1.5-2 天 |
| apikeys | settings | 中 | 表单视图 | 1 天 |
| admin-models | add routing | 中 | 表单视图内嵌 HTMX 局部刷新 | 1 天 |
| admin-models-aws | sync-from-key | 中 | 表单视图 + 模型 checkbox 列表 | 1 天 |
| enterprise | members | 中 | **独立子页面** `/enterprise/:id/members` | 1 天 |

**members 为什么用独立子页面**：
- members 管理本身就是一个完整的 CRUD 页面（列表 + 邀请 + 角色切换 + 删除）
- 放在 enterprise 表单视图里会过于拥挤
- Chrome 设置中，"用户管理"也是独立的子页面，不是内嵌表单

---

## 五、实施计划

### 5.1 分批策略

不建议一次性全部改造。按"风险从低到高、依赖从少到多"分批：

**Batch 1：验证模式（2 天）**
- `admin-enterprise`：create + edit + topup
- `admin-subscriptions`：edit plan
- 目标：验证"表单视图"模式在真实场景下可行，用户接受度如何

**Batch 2：纯表单页面（2 天）**
- `admin-models-general`：add + edit + confirm + eol
- `admin-models-aws`：sync-from-key + price-edit
- 目标：覆盖模型管理全链路

**Batch 3：复杂交互页面（2-3 天）**
- `apikeys`：create + settings
- 目标：验证"保留 JS 交互但移除 modal 容器"的方案

**Batch 4：动态列表页面（1 天）**
- `enterprise/members`
- `models/routing`
- 目标：收尾，可延后到下一版本

### 5.2 每批的验收标准

1. `go build ./...` 零错误
2. 该批所有页面 HTTP 200，无 console error
3. 创建 → 保存 → 列表刷新 → Toast 显示，全流程手动验证
4. 取消 → 回到列表，数据未变
5. 刷新页面（F5）后 still works（直接显示表单视图或列表视图）

---

## 六、样式规范

### 6.1 表单视图 CSS

```css
.page-form-view {
    max-width: 720px;           /* 比 modal 的 550px 更宽 */
    margin: 0 auto;
    padding: 24px;
}

.page-form-view .back-link {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--primary);
    font-size: 14px;
    margin-bottom: 16px;
}

.page-form-view h2 {
    font-size: 20px;
    font-weight: 600;
    margin-bottom: 24px;
}

.page-form-view .form-actions {
    display: flex;
    gap: 12px;
    margin-top: 32px;
    padding-top: 24px;
    border-top: 1px solid var(--border);
}
```

### 6.2 视图切换动画（可选）

```css
/* HTMX 添加的 class */
.htmx-added {
    animation: fadeIn 0.2s ease;
}

@keyframes fadeIn {
    from { opacity: 0; transform: translateY(8px); }
    to   { opacity: 1; transform: translateY(0); }
}
```

---

## 七、风险与缓解

| 风险 | 可能性 | 影响 | 缓解 |
|------|--------|------|------|
| 用户不习惯无弹窗 | 中 | 中 | Batch 1 先小范围验证，收集反馈后再推进 |
| 长列表编辑后回到原位 | 中 | 低 | 用 `sessionStorage` 保存滚动位置，返回时恢复；或列表项加 `id="row-xxx"` + URL hash |
| 表单视图内嵌 JS 交互出错 | 低 | 中 | 保留原有 JS，只改容器；每个 Batch 后完整手动测试 |
| 移动端布局问题 | 低 | 低 | 表单视图本身就是为移动端优化的，风险低于 modal |
| 工作量超出 | 中 | 中 | Batch 1 做完后重新评估 Batch 2-4；可跳过 Batch 4 延后 |

---

## 八、与 P1~P5 的关系

| 已完成（P1~P5） | P6 在此基础上做什么 |
|----------------|-------------------|
| JS 提取到独立文件 | 删除 `page-admin-*.js` 中的 `openModal()`/`closeModal()` 函数 |
| 权限判断下沉 Go | 表单视图直接使用 `{{if .CanEdit}}` 控制字段显示 |
| fetch() → form POST | 表单视图内的 `<form>` 直接沿用 P3 的后端 handler |
| snake_case 统一 | 表单视图的 `name` 属性统一用 snake_case |
| query/ 包 | 表单提交后 redirect 回列表，列表数据由 query/ 层统一提供 |

**P6 不修改**：
- `internal/query/` 任何函数
- `internal/routes/` 任何 API endpoint
- 数据库 schema
- 业务逻辑

**P6 只修改**：
- `web/templates/*.html` 的 UI 结构（删除 modal，添加视图切换）
- `internal/web/admin_handlers.go` 少数 handler 增加 `view` 参数支持
- `web/static/css/main.css` 新增表单视图样式

---

## 九、参考

- `frontend-refactoring-evaluation_202606041900.md` — C1/C2/C3 总体评估
- `c2-frontend-cleanup-progress-report-202606042100.md` — P1~P5 完成报告
- `c1-refactor-review-report-202606050500.md` — C1/C2 review
- Chrome Settings UI: `chrome://settings`
- HTMX docs: https://htmx.org/attributes/hx-target/

---

---

## 十、文档状态变更说明

> **2026-06-06**: 本文档的交互重构方案（HTMX 视图切换、从弹窗到页面内视图）已作为**方向 B（交互重构）**整合至 `docs/ui-improvement-proposal-plan-202606061645.md` §四。
>
> 该文档同时包含：
> - **方向 A（视觉打磨）**：三栏布局、卡片化、CSS 变量、动效 — 来自 `ui-improvement-proposal-202606031645.md`
> - **方向 B（交互重构）**：HTMX 视图切换、modal → 页面内表单 — 来自本文档
>
> **后续实施以 `ui-improvement-proposal-plan-202606061645.md` 为准**，本文档保留作为技术参考草稿。

**文档版本**: 1.0 ARCHIVED
**作者**: AI Assistant
**待审阅项**: 已合并至 `ui-improvement-proposal-plan-202606061645.md`
