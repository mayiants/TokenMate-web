# UI 强化方案：引入 daisyUI（试点两页）+ 视觉回归工具选型

> **Status:** COMPLETE（全站推广已完成 — 试点 + 批次 1/2/3 全部上线，见 §10；徽章全局描边 + checkbox light/dark 修复已合入）
> 创建时间（UTC）：2026-09-01T13:12Z
> 适用仓库：`ai-portal-go`（Portal 控制面）
> 基线版本：`v0.12.0`（HEAD `2fffc29`，CI green）—— 本方案实施前的稳定回退点
> 试点页面：用户 Dashboard（`web/templates/dashboard.html`）、后台用户管理（`web/templates/admin-users.html`）

## 1. 目标与非目标

**目标**
- 用成熟的 Tailwind 组件库统一并提升 UI 观感，先在两个代表性页面试点验证，再决定是否全站推广。
- 保持现有技术栈不变（服务端渲染 Go `html/template` + HTMX + Alpine.js + Tailwind CSS v3），**不引入前端框架、不引入客户端运行时**。
- 引入一个轻量视觉工具，支持"改版前后对比"，避免改版误伤其它页面。

**非目标**
- 不重写前端架构、不迁移到 React/Vue。
- 不在本阶段全站铺开（试点通过后再规划 rollout）。
- 不追求像素级设计稿还原，先达到"整洁、一致、现代"的后台观感。

## 2. 为什么选 daisyUI

| 候选 | 与本栈契合度 | 结论 |
|---|---|---|
| **daisyUI** | 极高 | Tailwind 官方插件，提供语义化 class（`btn`/`card`/`table`/`modal`/`badge`/`tabs`…），**零客户端 JS**，服务端模板直接渲染；被广泛认为是 HTMX 的最佳搭配；内置多套主题（含暗色）。**首选。** |
| Flowbite | 高 | 组件全，但部分交互组件依赖自带 JS，与现有 Alpine.js 可能职责重叠。 |
| Preline UI | 高 | 后台组件多，但同样带一层自有 JS。 |

daisyUI 只增加 CSS class、不改 DOM 交互模型，对"服务端模板 + HTMX + Alpine"改动最小、可渐进、可回退。

### 版本约束（重要）
- 当前项目使用 **Tailwind CSS v3**（`scripts/rebuild-tailwind.sh` 与 `Dockerfile` 均 `npm install tailwindcss@3`）。
- **daisyUI 5 需要 Tailwind 4**；因此本方案使用 **daisyUI 4.x**（兼容 Tailwind 3）。
- 未来若升级 Tailwind 4，再评估 daisyUI 5 迁移，单列。

## 3. 现状关键事实（实施前必须知道）

1. **Tailwind 在两处构建，必须同步改**：
   - `scripts/rebuild-tailwind.sh`（本地/CI fast-gate 用，`/tmp/tw-build`）
   - `Dockerfile` 内联的 `tailwind.config.js`（镜像构建时生成 `tailwind.min.css`）
   两处的 `tailwind.config.js` 必须**同时**加上 daisyUI 插件，否则本地与生产产物不一致。
2. **CI 有 Tailwind 体积门禁**：`Dockerfile` 校验 `tailwind.min.css` 必须 `< 500000` 字节（当前约 10KB）。daisyUI 经 purge 后通常只增加约 10–30KB，**预计仍远低于 500KB**，但实施后必须实测确认。
3. **缓存刷新**：`layout.html` 以 `app.css?v=37` 做 cache-busting。**任何样式改动都要 bump 这个版本号**（`?v=38`…），否则用户浏览器命中旧缓存。`tailwind.min.css` 无版本号但内容变化即变更文件，重建即可。
4. **暗色模式已支持**：Tailwind 配置 `darkMode: ['class','[data-theme="dark"]']`。daisyUI 主题机制也基于 `data-theme`，需保证与现有暗色切换不冲突（见风险 §7）。
5. **两个试点页面结构**：都是 `{{define "content"}}` 片段，挂在 `layout.html` 下（先加载 `tailwind.min.css`，再加载 `app.css?v=37`）。因此 daisyUI 组件类与现有 `app.css` 自定义类会共存，需注意样式优先级（见风险 §7）。
6. **无障碍门禁**：CI regression 跑 `e2e/scan-a11y-axe.js --gate`，对 serious/critical 违规（label 缺失、对比度不足）会失败。daisyUI 组件替换后必须重跑 axe。
7. **嵌入式资源**：模板/静态资源通过 `go:embed`（`web/embed.go`）打包，**任何前端改动都要 docker rebuild 才生效**。

## 4. daisyUI 接入方案（构建层）

在**两处** `tailwind.config.js` 做等价改动：

```js
// 依赖：在构建环境安装 daisyui@4（与 tailwindcss@3 并列）
//   rebuild-tailwind.sh: npm install tailwindcss@3 daisyui@4 ...
//   Dockerfile:          npm install tailwindcss@3 daisyui@4 ...
module.exports = {
  content: ["<templates>/**/*.html"],
  darkMode: ['class', '[data-theme="dark"]'],
  theme: { extend: { /* 现有 colors / backgroundImage 保留不变 */ } },
  plugins: [require('daisyui')],
  daisyui: {
    // 试点阶段只启用有限主题，减小体积、避免与现有主色冲突
    themes: [
      {
        woodgit: {
          "primary": "#2563eb",      // 贴近现有蓝色主色（--primary）
          "secondary": "#64748b",
          "accent": "#0891b2",
          "neutral": "#1f2937",
          "base-100": "#ffffff",
          "info": "#0ea5e9",
          "success": "#16a34a",
          "warning": "#d97706",       // 注意与 a11y 对比度（见 §7）
          "error": "#dc2626",
        },
      },
      "dark",                          // 复用 daisyUI 内置 dark，映射到 [data-theme=dark]
    ],
    base: false,   // 关键：不要让 daisyUI 重置全局 base 样式，避免打乱现有 app.css
    styled: true,
    utils: true,
    logs: false,
  },
}
```

> `base: false` 是降低风险的关键：只引入 daisyUI 的**组件类**，不接管全局排版/背景，最大限度减少对未试点页面的影响。

## 5. 试点范围与改造清单

### 5.1 用户 Dashboard（`web/templates/dashboard.html`）
- 统计卡片 → daisyUI `card` / `stats`。
- 主按钮/次按钮 → `btn btn-primary` / `btn btn-ghost`。
- 徽章/状态 → `badge badge-*`。
- 保持所有 `{{t "..."}}` i18n key 不变；只换 class，不改数据绑定与 HTMX/Alpine 行为。

### 5.2 后台用户管理（`web/templates/admin-users.html`）
- 用户表格 → `table table-zebra`。
- 顶部统计条 → `stats`。
- 抽屉/模态（含刚加的"重新分配企业"）→ daisyUI `modal` 或保留现有 drawer，仅统一按钮/输入样式（`input input-bordered` / `select select-bordered`）。
- **保留刚修复的 a11y 标注**（`for=`/`aria-label`），替换 class 时不得丢失。

### 5.3 不动的部分
- 后端 handler、路由、i18n key、HTMX 属性、Alpine 逻辑一律不变。
- 其它页面本阶段不改（`base:false` + 仅改这两页模板 class，天然隔离）。

## 6. 视觉回归工具选型：toHaveScreenshot vs playwright-ui-audit

| 维度 | Playwright `toHaveScreenshot()` | `playwright-ui-audit`（GitHub YogiAdv/playwright-ui-audit） |
|---|---|---|
| 定位 | 像素级基线对比，**测试断言** | 一条命令给所有路由出桌面+移动整页截图 + HTML 报告，**人工审阅** |
| 安装 | 已内置（项目已装 `@playwright/test`） | 需额外引入小工具 |
| 是否需维护基线 | 需要提交并维护 per-OS 基线图 | 不需要基线 |
| CI 稳定性 | 字体/子像素在 Mac↔Linux runner 上易 flaky，需在 Linux runner 固定基线 | 无基线即无 flaky（不做 pass/fail 判定） |
| 适合场景 | UI **稳定后**的回归门禁 | UI **正在改动**时的快速前后对比 |

**结论（分阶段）**
- **试点阶段 → 用 `playwright-ui-audit`**（性价比最高）：一条命令生成两页在桌面+移动的整页截图与 HTML 报告，改造前跑一次存"before"，改造后跑一次看"after"，人工对比即可，**零基线维护、零 flaky**。适合 UI 正在变动的当下。
- **稳定后 → 再加 `toHaveScreenshot()` 到 CI**：等 daisyUI 铺开、UI 定型后，为关键页面加像素级回归门禁；届时需在 CI 的 Linux runner 上生成/提交基线以规避字体差异。

即：**现在先上 playwright-ui-audit 做审阅工具，暂不入 CI 门禁；toHaveScreenshot 留到定型后作为回归 gate。**

> **实施记录（2026-09-01，更新）**：全站推广实际**未引入** `playwright-ui-audit` 这个第三方包，也**未启用** `toHaveScreenshot()`。改造过程中的 before/after 视觉对比，直接用项目已有的 `@playwright/test`（Playwright 自带 chromium）写一次性截图脚本（登录后 `page.screenshot()`，用完即删）完成，**零新增依赖**。视觉审阅已由 axe-core 无障碍门禁 + console 扫描 + 人工截图对比覆盖。
> **当前决定**：**暂不引入 UI audit 工具，也暂不启用 `toHaveScreenshot()` 视觉回归**。UI 已定型稳定，现有 axe/console/lighthouse 门禁足够；如未来需要像素级回归门禁再按本节"稳定后"计划评估。

## 7. 风险与缓解

| 风险 | 缓解 |
|---|---|
| daisyUI 全局 base 样式打乱现有 `app.css` | 配置 `base: false`，只引组件类；试点仅改两页。 |
| `tailwind.min.css` 超 500KB 门禁 | 实测体积；daisyUI purge 后增量小；只启用少量主题。 |
| 本地与生产产物不一致 | `rebuild-tailwind.sh` 与 `Dockerfile` **两处**配置同步修改 + 各自安装 `daisyui@4`。 |
| 浏览器命中旧 CSS | 改样式后 bump `app.css?v=` 版本号。 |
| a11y 回归（对比度/label） | 主题 `warning`/`error` 色需过 axe 对比度；改后重跑 `scan-a11y-axe.js --gate`。 |
| 暗色主题冲突 | daisyUI dark 主题映射到 `[data-theme=dark]`，与现有切换机制对齐后验证。 |
| 影响未试点页面 | `base:false` + 仅改两页模板；改造前后用 playwright-ui-audit 全站截图比对，确认其它页面无变化。 |

## 8. 分阶段实施清单

**阶段 0 — 准备**
- [ ] 打基线 tag（已完成：`v0.12.0`）。
- [ ] 引入 `playwright-ui-audit`，对全站（至少两试点页 + 若干邻近页）跑一次，存"before"截图/报告。

**阶段 1 — 构建接入**
- [ ] `rebuild-tailwind.sh` 与 `Dockerfile` 同步加 `daisyui@4` 依赖与插件配置（`base:false` + woodgit/dark 主题）。
- [ ] 本地重建，实测 `tailwind.min.css` 体积 < 500KB。

**阶段 2 — 两页改造**
- [ ] 改造 `dashboard.html`（card/stats/btn/badge）。
- [ ] 改造 `admin-users.html`（table/stats/input/select，保留 a11y 标注）。
- [ ] bump `app.css?v=`。

**阶段 3 — 验证**
- [ ] `scripts/rebuild-tailwind.sh` 无 diff；`i18n_full_audit.py --strict` 通过。
- [ ] docker rebuild + 启动 healthy。
- [ ] `scan-a11y-axe.js --gate` 两页通过；`scan-console-errors.js` 无新错误。
- [ ] playwright-ui-audit 出"after"，与"before"人工对比确认：两页变好、其它页无回归。
- [ ] 推送，确认 CI（fast-gate + regression）green。

**阶段 4 — 评审与决策**
- [ ] 团队评审两页效果，决定是否全站 rollout。
- [ ] 若 rollout：规划分批改造；UI 定型后再把 `toHaveScreenshot()` 作为 CI 回归门禁引入（在 Linux runner 固定基线）。

## 9. 回退方案
- 任一阶段出问题：`git revert` 相关提交或 `git checkout v0.12.0 -- <改动文件>`，重建 docker 即恢复。
- 由于试点仅改两页模板 class + 构建配置（`base:false` 隔离），回退范围小、影响可控。

## 10. 实施进展（更新于 2026-09-01）

> Status 更新：DRAFT → **COMPLETE（全站推广完成）**

### 10.1 关键决策修订：daisyUI 类名加 `du-` 前缀
试点落地时发现 `base:false` 只关闭 daisyUI 的 base/reset 层，**不关闭组件层**——daisyUI 仍会全局输出 `.modal`/`.btn`/`.card`/`.badge`/`.table` 等组件类，与 `app.css` 同名类**碰撞**。最严重的是 `.modal` 被 daisyUI 设成 `position:fixed;pointer-events:none`，导致**全站弹窗失效**（CI `ui-smoke` 点不到 `#members-modal .modal-close` 而失败）。

**修订方案**：给 daisyUI 配置加 `prefix: "du-"`（两处构建配置同步）。类名变为 `du-table`/`du-input`/`du-select`/`du-badge` 等，与 `app.css` **零碰撞**。模板统一用：
- `du-table du-table-zebra`（大表加 `du-table-sm`）
- `du-input du-input-bordered`
- `du-select du-select-bordered`（宽度受限的紧凑内联下拉**不加**，会裁切）
- `du-badge du-badge-outline`（状态徽章；scoped 深色 `#166534`/`#991b1b` 以过 axe 对比度）

副作用是好的：加前缀后 purge 只输出用到的 `du-` 类，`tailwind.min.css` 从 ~100KB 降到 **~24KB**。

### 10.2 缓存刷新修订
`tailwind.min.css` 原本**没有** `?v=` 版本号，导致 daisyUI 的新 CSS 命中旧缓存（斑马纹不显示）。已给它也加上 `?v=`，与 `app.css` 同步 bump。当前版本 **v=41**。

### 10.3 e2e 测试账号生命周期
新增 `e2e/e2e-accounts.sh enable|disable`，在 UI/a11y/console 扫描前 `enable`、扫描后 `disable`（CI 中 `if: always()`），避免特权测试账号（e2e-admin/user/enterprise）在测试间隙长期可用。本地补建了 e2e-enterprise 账号（enterprise_admin + 关联企业），使 `/enterprise/*` 权限页也能纳入扫描。

### 10.4 已完成
- **试点（2 页）**：`dashboard`、`admin-users` —— du-table/input/select + admin-users 描边状态徽章、企业列省略号截断修复按钮溢出。CI green。基线 tag `v0.12.0`。
- **批次 1（7 页，重表格页）**：`admin-models-general`、`admin-providers`、`admin-keypool`、`admin-pricing`、`admin-upstream-pricing`、`usage-logs`、`apikeys`。每页单独 commit + 本地 CI 校验；整批一次 push（`714078b..3d91a0d`）。GitHub CI **green**。全部 7 页 axe 0 违规、console 0 错误。
- **修复 + 徽章统一（`57b523e..d69be60`）**：
  - 修复可展开表格（`usage-logs` API Logs、`admin-upstream-pricing`）斑马纹失效——这类表在每个可见行后插入一个隐藏 `detail-row`，daisyUI `du-table-zebra`（`nth-child(2n)`）会给隐藏行上色。改为对可见行 scoped `nth-of-type(4n+3)` 上色。
  - **状态徽章改为全站描边**：直接改 `app.css` 的 `.badge-success/warning/error/info/danger`（透明底 + 彩色边框 + 无障碍 `--*-text` 文字色），**不走 `du-` 前缀**（`.badge` 与 daisyUI 冲突，直接改现有类更一致）。
- **批次 2（11 页）**：`enterprise/{dashboard,users,keys,groups,billing,usage,audit,alerts,settings}` + `admin-subscriptions` + `admin-vouchers`。均为简单表格（无可展开行），用 `du-table du-table-zebra`。每页单独 commit，整批一次 push（`d69be60..abb8f83`）。axe 0 违规、console 0 错误。
- **批次 3（31 页，收尾）**：`admin-announcements/audit-logs/compliance/enterprise-applications/enterprise/groups/keys/models-aws/monitor-downstream/monitor-upstream/provider-health/rate-limits/reconciliation-ops/reconciliation/upstream-ops/usage-explorer`、`admin`、`billing-details/billing`、`enterprise/key-detail`、`enterprise/member-detail`、`invoices`、`models-gallery`、`my-group`、`plan-and-group`、`profile`、`status`、`super/admin-management`、`super/payment`、`user-upstream-ops`、`voucher`。其中 `admin-reconciliation-ops` 的 `#detail-table` 为可展开表，用 scoped `nth-of-type(4n+3)` zebra；其余简单表用 `du-table du-table-zebra`。每页单独 commit，整批一次 push。axe 0 违规、console 0 错误。

**✅ 全站推广完成**（试点 2 + 批次 1 的 7 + 批次 2 的 11 + 批次 3 的 31 = 约 51 个页面）。表格斑马纹（含可展开表格的 scoped 修复）、daisyUI 描边输入/下拉、全站描边状态徽章均已统一。

### 10.5 收尾说明
- 全部含表格/表单的用户可见页面已 daisyUI 化。
- **不碰** `btn/card/stat`（需重构，另立项目）。徽章已全局描边。
- 后续新页面遵循 §8 清单 + §10.1 的 `du-` 前缀模式；可展开表格用 scoped zebra。
- **视觉回归工具**：本次**未引入** `playwright-ui-audit`、**未启用** `toHaveScreenshot()`（详见 §6 实施记录）。before/after 用 Playwright 原生截图脚本临时完成，零新增依赖。暂不进一步引入 UI audit / 视觉回归门禁。
