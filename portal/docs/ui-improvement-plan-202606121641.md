# ai-portal-go UI Review & Improvement Plan

**Design Read:** 这是一个面向开发者和运维人员的 B2B AI API 中转后台门户，视觉语言偏向功能/清爽的 SaaS dashboard，同时包含一个对外的 marketing landing 页。整体应以**保留现有品牌、逐步进化**为主，而不是推倒重做。

**Dial Values（由 brief 推导）:**
- `DESIGN_VARIANCE: 4` — 后台以秩序、对齐、可预测为主；landing 页允许适度不对称和视觉层次。
- `MOTION_INTENSITY: 3` — 以功能性微交互为主（hover、active、focus、toast、页面淡入），避免滚动劫持和过度动画。
- `VISUAL_DENSITY: 6-7` — 后台数据密集；landing 页密度降至 4。

**Mode:** Redesign - Preserve（保留信息架构、URL、品牌色，局部视觉现代化）。

---

## 1. Scope（范围）

基于用户选择：**Landing 页面 + 后台设计系统规范化**。

不包含：
- 不更换前端框架（继续使用 Go template + HTMX + Alpine + 自定义 CSS）。
- 不改动后端路由、API 契约、URL slug、表单字段名。
- 不引入 React/Next/Fluent/Carbon 等完整设计系统（项目当前栈不支持，且成本过高）。

包含：
1. **Landing 页面（`web/templates/landing.html`）** — 消除 AI design tells，统一营销视觉语言。
2. **设计系统 token（`web/static/css/app.css`）** — 补全缺失语义变量、统一颜色/圆角/阴影、修复硬编码色值。
3. **后台页面视觉规范化** — 把 `dashboard.html`、`admin.html`、各 `enterprise/*.html`、`apikeys.html` 等页面里的硬编码颜色、 inline style、卡片/表格/空状态/加载态统一到设计系统。
4. **可访问性与移动端** — 修复 focus、对比度、表格横向滚动、按钮换行、hero 适配等。

---

## 1.1 当前完成状态（截至 2026-06-14）

| Phase | 主题 | 状态 | 说明 |
|---|---|---|---|
| Phase 1 | 设计系统 Token 补全 | ✅ 已完成 | `app.css` 已定义 `--color-primary/success/warning/error/info/violet/cyan/orange` 及对应 light 变体、utility classes（`.text-*` / `.bg-*`）。 |
| Phase 2 | Landing 页 Redesign | ✅ 基本完成 | 原 audit 中的 `gradient` 文字、`pulse-dot`、`&mdash;`、`Step 1/2/3`、假精确数字等已清理；仅剩一处 feature card 背景渐变使用 `--primary-light`。 |
| Phase 3 | 后台页面视觉规范化 | ✅ 基本完成 | 模板中 `style="...#hex"` 硬编码颜色已清零；仅 `layout.html` 的 announcement banner 脚本保留少量固定 banner 色。 |
| Phase 4 | 可访问性与移动端收尾 | 🚧 移动端已完成，可访问性仍需验证 | mobile 适配已收尾：后台页面工具栏/卡片/表单的响应式规则已补全，sidebar 折叠修复，dashboard 空链接移除；Playwright mobile 38/38 passed，console 36/36 clean。focus-visible、WCAG 对比度、reduced-motion 仍需系统性验证。 |
| Phase 5 | Layout 模板拆分与 JS 治理 | ✅ 已完成 | `layout.html` 拆分为 `partials/layout-sidebar.html`、`partials/layout-modals.html` 与 `static/js/app-layout.js`；修复执行时序导致的 `_t` / `executeGlobalConfirm` 未定义及 `document.body.dataset` 空指针问题；`node e2e/scan-console-errors.js` 36/36 页面通过。 |

> **验证快照**
> - `grep -R -E 'style="(color|background|background-color): *#' web/templates` → **0 处**
> - `grep -R -E 'style="[^"]*#[0-9a-fA-F]' web/templates` → **0 处**
> - `grep -R -E '#3b82f6|#8b5cf6|#f59e0b|#ef4444|#0ea5e9|#6366f1' web/templates` → **仅 layout.html banner 脚本 2 处**
> - `node e2e/scan-console-errors.js` → **36/36 pages clean**

---

## 2. Audit Findings（关键问题）

### 2.1 Landing 页（AI design tells 集中）
- **Hero 居中 + 渐变文字**：`hero-title .gradient` 使用蓝紫渐变，属于典型的 AI-purple 默认。
- **3 列等宽 feature cards**：`feature-grid` 是通用三卡布局，无视觉变化。
- **装饰性脉冲点**：`pulse-dot` 没有实际语义状态。
- **假精确数字**：`99.9%`、`<100ms`、`50+`、`10M+` 未标注为示例数据。
- **步骤编号 eyebrow**：`Step 1/2/3` 属于 banned generic step labels。
- **em-dash 字符**：footer 和 privacy/terms modal 里出现 `&mdash;`（`—`），违反 em-dash ban。
- **手画 SVG icons**：feature 图标全部为 inline SVG path，应使用统一图标库。
- **CTA 重复**：`免费注册`/`登录` 在 hero 和 CTA section 重复出现，意图重复。
- **无真实图片**：全页为文字/渐变/code block，缺少真实视觉资产。

### 2.2 后台设计系统
- **硬编码颜色泛滥**：`dashboard.html`、`enterprise/dashboard.html`、`apikeys.html`、`plan-and-group.html` 等存在大量 `#8b5cf6`、`#0ea5e9`、`#f59e0b`、`#ef4444` 等 hex。
- **缺失语义变量**：`--color-error`、`--color-warning`、`--color-success` 等被模板使用但 CSS 未定义，导致 fallback 到固定 hex，暗色模式可能不协调。
- **颜色一致性**：同一页出现蓝、紫、青、橙、绿多种强调色，无单一 accent lock。
- **Shape 不一致**：按钮/卡片圆角已在 CSS 变量中，但部分 inline 元素（step-number、feature-item）硬编码 10px/12px。
- **表格/卡片重复**：后台大量页面使用相同卡片+表格模式，但间距、hover、空状态不统一。
- **Inline style 过多**： especially enterprise dashboard / apikeys，应迁移到 class。

### 2.3 可访问性
- `focus-visible` 已存在，但部分自定义按钮（`.btn`、`.tab-btn`）未明确验证对比度。
- Landing hero 在暗色模式下渐变文字可读性需验证。
- 部分 placeholder 颜色依赖浏览器默认，可能对比度不足。

---

## 3. Implementation Plan

### Phase 1: 设计系统 Token 补全（基础设施）

**目标：** 让 `app.css` 拥有一套完整、语义化的 token，所有模板都能引用，而不是写死 hex。

**具体改动：**
1. 在 `:root` 和 `[data-theme="dark"]` 中新增/统一：
   - `--color-primary`, `--color-primary-hover`, `--color-primary-light`
   - `--color-success`, `--color-warning`, `--color-error`, `--color-info`
   - `--color-violet`（用于 token/quota 等已有紫色语义）、`--color-cyan`、`--color-orange`
   - 将现有 `--success`/`-warning`/`-error` 映射到上述新 token，保持兼容性。
2. 新增 utility classes：
   - `.text-violet`, `.bg-violet-light`, `.text-cyan`, `.bg-cyan-light`, `.text-orange`, `.bg-orange-light`
   - `.stat-icon-violet`, `.stat-icon-cyan`, `.stat-icon-orange` 等，替换 dashboard/enterprise 中的硬编码色。
3. 统一圆角/阴影：
   - 确保所有卡片、按钮、输入框使用 `--radius` / `--radius-lg` / `--shadow`。
   - 清理模板中硬编码的 `border-radius: 10px/12px/16px`（landing 特殊结构除外）。
4. 修复 `--color-error`/`--color-warning` 缺失：
   - 将 `enterprise/dashboard.html` 中的用法改为已定义变量，或新增这些别名。

**验证：**
- `grep -R "style=\"color: #\|style=\"background: #\|style=\"background-color: #" web/templates` 数量显著下降。
-  light/dark 切换后颜色过渡自然。

### Phase 2: Landing 页 Redesign

**目标：** 让 landing 页更像一个严肃、可信的 B2B SaaS 首页，而不是模板化 AI 落地页。

**具体改动：**
1. **Hero 重构**
   - 去掉蓝紫渐变文字；改为单一强调色（primary）+ 深色/浅色文字。
   - 移除装饰性 `pulse-dot`；如要表现"在线/可用"，改为语义化 badge（如绿色小点 + 状态文案）。
   - 保持居中 hero（因为 landing 信息本身就是主角），但 headline 控制在 2 行以内，subtext ≤ 20 词。
   - 添加一张真实/生成的 hero 视觉（如 API 网络/抽象数据流图），避免纯文字+渐变。
2. **CTA 精简**
   - 保留一组主/次 CTA，删除 CTA section 的重复按钮。
   - 主按钮文案固定为 "免费注册"，次按钮固定为 "登录"。
3. **Stats 区域**
   - 将假精确数字标注为示例/占位（注释或前缀 "示例数据"）。
   - 或改为更可信的定性描述（"多地节点"、"24/7 监控"）。
4. **Features 区域**
   - 将 3 列等宽卡片改为**非对称 bento 风格**或 2+1 布局，至少 2-3 个 cell 有真实图像/渐变/图案变化。
   - 统一使用 Phosphor/HugeIcons/Radix/Tabler 图标（优先 Tabler，因为当前 inline SVG  stroke 风格接近 Tabler），不再手画 path。
5. **Quick Start Steps**
   - 删除 "Step 1/2/3" 编号标签，改用动词直接作为标题（"注册账号"、"创建 API Key"、"发起请求"）。
   - 减少每个 step 内的密集文字，突出 code block。
6. **Footer / Modals**
   - 将所有 `&mdash;` 替换为 `-` 或重构句子。
   - 删除 modal 中的装饰性高亮段落，改为普通正文。
7. **Copy Self-Audit**
   - 重新阅读所有可见字符串，修正 AI 感文案。
   - 例如 "统一接口按量计费企业级安全用标准 openai 格式调用 claudegptgemini" 需要断句和可读性优化。

**验证：**
- Pre-flight check：0 em-dashes、无重复 CTA 意图、hero ≤ 4 个文本元素、feature grid 有视觉变化。
- 中英文切换后布局不崩。

### Phase 3: 后台页面视觉规范化

**目标：** 把硬编码颜色和 inline style 迁回设计系统，统一卡片、表格、空状态、按钮、badge。

**具体改动：**
1. **Dashboard（`dashboard.html`）**
   - 将 8 个 stat card 的硬编码 icon 背景/文字色改为 Phase 1 的 utility classes。
   - 统一 stat card 结构，避免部分 card 用 `style="color: #..."`。
2. **Admin（`admin.html`）**
   - quick-action 按钮颜色统一。
   - chart 颜色使用 `--color-primary` 而非 `#3b82f6`。
3. **Enterprise Dashboard / Billing / Groups**
   - 替换所有 `#8b5cf6`、`#6366f1`、`#f59e0b` 硬编码。
   - 图表（Chart.js）颜色从 token 读取，或提供与主题一致的调色板。
   - 修复 `var(--color-error, #ef4444)` 等未定义变量。
4. **API Keys（`apikeys.html`）**
   - radio accent color、EOL warning background/border 改用 token。
   - 将 JS 中硬编码 `borderColor = '#3b82f6'` 改为 token。
5. **Admin Models General / other admin pages**
   - 将 `.badge-orange`、`.text-orange` 等转为 `--color-warning` 语义类。
6. **空状态/加载态**
   - 统一使用 `.empty-state`、`.empty-state-icon`、`.empty-state-title`。
   - 为 HTMX 加载区域添加 skeleton shimmer（使用已有 shimmer 或新加 CSS）。
7. **表格**
   - 统一 `.data-table` 样式，移除页面级覆盖。
   - 确保 `.data-table-wrapper` 用于所有横向滚动场景。

**验证：**
- i18n 检查通过（`python3 scripts/check_i18n.py`）。
- E2E 测试通过（`playwright test e2e/ui-all-pages.spec.js`）。
- 暗色模式浏览主要页面无突兀色块。

### Phase 4: 可访问性与移动端收尾

**状态：** 移动端已完成，可访问性仍有具体缺口。

**已完成 ✅**
- mobile 响应式收尾：后台工具栏/卡片/表单换行规则已补全，sidebar 折叠修复，dashboard 空链接移除。
- Playwright mobile 38/38 passed，`node e2e/scan-console-errors.js` 36/36 clean。
- landing feature grid 在移动端 1 列。
- topbar 在 1024px 以下正确折叠为 mobile menu。
- reduced-motion 降级已存在（`app.css` 全局 + `landing.html` hero/CTA）。

**仍待完成 ⏳**
1. WCAG AA 对比度：
   - dark-mode `.btn-primary`（`#60a5fa` on white）约 2.54:1，未达 AA。
   - placeholder 颜色（`var(--text-muted)` 80% opacity on `#ffffff`）约 3.24:1，未达 AA。
2. `:focus-visible`：
   - `.btn`/链接/tab 已覆盖全局 `*:focus-visible`。
   - `.form-input`/`.form-select`/`.form-textarea` 在 focus 时设 `outline: none`，仅靠 border-color/box-shadow 指示，需显式补 focus-visible 语义。
3. mobile 表格折叠：
   - 绝大多数 `<table>` 已包 `.data-table-wrapper`。
   - `admin-reconciliation.html` 中两个 `.ledger-table` 未包 wrapper，可能溢出。

---

## 4. Deliverables

1. 修改后的 `web/static/css/app.css`（token 补全 + utility classes + 清理）。
2. 重写后的 `web/templates/landing.html`。
3. 清理后的 `web/templates/dashboard.html`、`web/templates/admin.html`、各 `enterprise/*.html`、`apikeys.html`、`admin-models-general.html` 等。
4. 新增/更新的 i18n key（如 landing copy 调整需要）。
5. 测试报告：i18n + E2E + Docker 镜像验证。

---

## 5. Test Plan

- `python3 scripts/check_i18n.py` — 确保新增/修改文案都有翻译。
- `npm run test:all` 或 `playwright test e2e/ui-all-pages.spec.js` — 确保页面功能未回归。
- Docker rebuild: `docker compose -f docker-compose.portal.yml up --build -d portal`。
- 手动检查：light/dark 切换、中英文切换、移动端 375px/768px、主要 admin 页面滚动/表格。

---

## 6. Risk & Notes

- **范围控制**：后台页面数量多，Phase 3 会按"逐个文件硬编码颜色清零"的方式推进，避免一次性改动过大。
- **不引入新依赖**：图标仍用 inline SVG，但统一从 Tabler/Phosphor 复制标准 path，不再手绘；如需大量图标再考虑引入图标库。
- **品牌色保留**：当前 primary 为 `#3b82f6`，保持蓝色作为 accent，不换成紫色/霓虹渐变。
- **向后兼容**：所有改动只影响视觉，不动 DOM 结构中的 name/id/HTMX trigger。

---

## 7. Appendix — `layout.html` 保守拆分方案

### 背景

`layout.html` 当前约 **1,970 行 / 111 KB**，是整个模板目录最大的文件。它既包含页面骨架（header + nav + sidebar + footer），又包含大量 inline JS（theme、i18n、modal、announcement、sidebar collapse 等）。直接把它拆成多个 partial 会增加未来整体改版时的维护面，因此推荐**渐进式、保留骨架**的拆分策略。

### 拆分原则

1. **骨架不动**：`layout.html` 继续作为唯一入口，负责 `<html>`、`<body>`、全局 grid、`<script>`/`<link>` 引用、`{{block "content"}}` 组合。整体布局范式改变时，只改骨架即可。
2. **按“稳定边界”抽 partial**：把可能独立演变的局部块抽到 `partials/`，例如 sidebar nav、global modals、onboarding wizard。
3. **脚本外移**：把底部大型 inline `<script>` 迁移到 `web/static/js/app-layout.js`，让 HTML 只负责引入，改 JS 逻辑不用打开 2,000 行模板。

### 推荐文件结构

```
web/templates/
├── layout.html                        # 骨架（目标降到 ~600 行）
├── partials/
│   ├── layout-header.html             # 顶部栏（可折叠菜单、用户下拉）
│   ├── layout-sidebar.html            # 侧边栏导航列表
│   ├── layout-footer.html             # 页脚（如有）
│   ├── layout-modals.html             # 全局 message/confirm/announcement/wizard modal
│   └── layout-toast.html              # toast 容器
└── ...

web/static/js/
├── app-layout.js                      # 原 layout.html 底部 inline script
├── app-common.js                      # 已有：通用交互
└── ...
```

### 具体步骤

1. **提取底部 inline script**
   - 将 `layout.html` 中从 `var _pendingConfirmCallback = null;` 到 sidebar collapse 逻辑的大段 `<script>` 移到 `web/static/js/app-layout.js`。
   - `layout.html` 中改为 `<script src="/static/js/app-layout.js"></script>`。
   - 预期收益：`layout.html` 减少约 800~900 行；后续修改 modal/announcement/sidebar 逻辑不再翻动 HTML。

2. **提取全局 Modal**
   - 把 `global-message-modal`、`global-confirm-modal`、`announcement-popup-modal`、`onboarding-wizard-modal` 四个 modal 的 HTML 抽到 `partials/layout-modals.html`。
   - `layout.html` 中 `{{template "layout-modals" .}}` 引入。
   - 预期收益：减少约 100 行；改弹窗结构只改一处。

3. **提取侧边栏导航**
   - 把 `<aside id="sidebar">` 及其内部所有 `<a>`、权限判断、tooltip 抽到 `partials/layout-sidebar.html`。
   - `layout.html` 中 `{{template "layout-sidebar" .}}` 引入。
   - 预期收益：减少约 250~300 行；新增/调整菜单项只改 sidebar partial。

4. **保留顶部栏在骨架中（可选）**
   - 顶部栏相对较小（~150 行）且与骨架耦合度高，可以保留在 `layout.html`，或视情况抽到 `partials/layout-header.html`。

### 风险控制

- **DOM 结构不变**：所有 partial 只移动 HTML，不改动 `id`、`class`、HTMX trigger、Alpine data；JS 和 CSS 选择器无需修改。
- **向后兼容**：内容页继续 `{{define "content"}}{{end}}`，完全无感。
- **未来改版成本**：若以后从 sidebar 改为 top-nav，只需替换 `layout-sidebar.html` 并调整 `layout.html` 的 grid；内容页 0 改动。

### 验收标准

- `layout.html` ≤ 700 行。
- `grep -n "<script>" web/templates/layout.html` 不再包含大型 inline script（仅保留 tiny theme 初始化脚本）。
- 任意页面 `/admin/*`、`/dashboard`、`/enterprise/*` 加载后无 console error。
- Docker rebuild 后 E2E 页面扫描通过。
