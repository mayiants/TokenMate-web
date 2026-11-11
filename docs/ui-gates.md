# UI 质量门禁（与 portal 同级要求）

> 适用于 `web/`（React + Semi UI）。目标是让"页面漂亮"和"页面可交付"同时被自动检查，
> 而不是靠人工看截图。所有检查都在 `web/e2e/*.cjs`，由 npm scripts 和 GitHub Actions 调用。

## 一、门禁清单

| 检查 | 脚本 | 通过标准 | 默认地址 |
|---|---|---|---|
| 国际化 | `e2e/check-i18n.cjs` | zh/en 键集完全一致、无"值等于键"、英文里无中文 | — |
| 可访问性 | `e2e/scan-a11y-axe.cjs --gate` | axe wcag2a/aa 的 serious + critical 为 0 | `CHECK_BASE`（默认 9090） |
| 控制台/网络错误 | `e2e/scan-console-errors.cjs --gate` | JS error / console.error / `/api` 4xx-5xx 为 0（管理员探测 403 白名单） | 同上 |
| 响应式 | `e2e/check-responsive.cjs --gate` | 1440 / 834 / 390 三个宽度下无横向溢出 | 同上 |
| Lighthouse | `e2e/scan-lighthouse.cjs --gate` | performance ≥ 0.45、accessibility ≥ 0.9、best-practices ≥ 0.9 | `CHECK_BASE`（默认 4174） |

页面清单集中在 `e2e/pages.cjs`（公开 / 用户控制台 / 平台后台三组），四个扫描脚本共用，
新增路由必须加到那里，否则不会进入任何门禁。当前覆盖 5 + 20 + 12 = 37 个路由
（含本轮新增的 `/console/admin/usage-explorer`、`/console/admin/rate-limits`）。

### 登录态与失败可见性

- 需要登录的页面用 `CHECK_EMAIL` / `CHECK_PASSWORD` 登录；`--role=admin` 切到平台后台页组。
- 登录失败时，脚本**直接判定门禁失败**（以前会静默地只扫登录页，看起来全绿）。
- 没有后端时用 `--public`（或 `CHECK_PUBLIC_ONLY=1`）只跑公开页，用于 GitHub CI。

## 二、本地怎么跑

```bash
cd web
npm ci
npm run build

# 1) 无后端：只跑公开页（CI 默认）
npx vite preview --outDir dist --host 127.0.0.1 --port 4174 &
CHECK_BASE=http://127.0.0.1:4174 npm run check:ui:public

# 2) 有后端：全量（含登录态页面）
API_TARGET=http://127.0.0.1:3001 npx vite preview --outDir dist --host 127.0.0.1 --port 4174 &
CHECK_BASE=http://127.0.0.1:4174 \
CHECK_EMAIL=you@example.com CHECK_PASSWORD='…' npm run check:ui
```

`API_TARGET` 决定预览服务器的 `/api` 代理目标（默认 `http://127.0.0.1:3001`）；
`HOST` / `PORT` / `PREVIEW_PORT` 可改监听地址与端口；`SHOTS_DIR` 指定响应式截图目录。

### 后端要求（实测踩过的坑）

1. 后端必须放行预览站点的来源：`ALLOWED_ORIGINS=http://127.0.0.1:4174`，否则登录返回
   `403 Origin not allowed`，门禁会报"login failed"。
2. MFA 相关操作依赖 Redis（限流/挑战存储）：`docker run -d --name tmp-redis -p 127.0.0.1:6379:6379 redis:7-alpine`。
3. Lighthouse 用的是完整 Chrome（不是 playwright 的 headless shell），需要
   `npx playwright install-deps chromium` 装 `libcups` 等系统库。

## 三、CI

`.github/workflows/ci-web.yml`：

- **build-and-public-gates**（push / PR 必跑）：`npm ci` → `npm run build`（含 `tsc --noEmit`）
  → `check:i18n` → 起 preview → `check:ui:public`（axe/console/responsive/lighthouse 公开页）。
- **authenticated-gates**（`workflow_dispatch`，需提供 `base_url` + secrets 账号）：
  跑 `check:a11y` / `check:console` / `check:responsive`，覆盖登录态页面，并上传响应式截图。

## 四、当前实测结果（2026-11-11，ECS 自建环境）

| 项目 | 结果 |
|---|---|
| `tsc --noEmit` + `vite build` | 通过（13s） |
| i18n | 1726 zh / 1726 en，0 problems |
| axe（admin 17 页） | 0 serious/critical |
| axe（enterprise_admin 25 页，含企业九页） | 0 serious/critical |
| console（admin / user） | js=0 console=0 network=0 |
| responsive（三档宽度 × 全部路由） | all clear |
| lighthouse（/, /models, /login） | perf 57/61/61、a11y 100/90/100、bp 96/96/100 |

## 五、本轮修复的现存问题

| 问题 | 原因 | 处理 |
|---|---|---|
| `/models` aria-required-parent（98 节点） | Semi `Avatar` 恒渲染 `role="listitem"`，被当独立图标用 | `ProviderIcon` 回落图标改成 `span[role=img]` |
| `/` 颜色对比度不达标 | 空状态用了 `--wg-text-3`（#8f959e） | 改用 `--wg-text-2`（#646a73，≥4.5:1） |
| 管理员/企业页移动端横向溢出 | Semi `Descriptions` 固定多列，390px 放不下 | 企业概览与账务改为自适应 label/value 网格 |
| `/models` 长模型名溢出 | 按钮内文本未截断 | 门禁放行 `text-overflow: ellipsis` 的刻意截断 |
| 限流页进度条无可访问名 | `<Progress>` 缺 aria-label | 补 `aria-label` |
| MFA 接口在旧后端 404 时刷错误 | 无兼容分支 | `MfaCard` 遇 404 静默隐藏 |
