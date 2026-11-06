# Go Portal 前端重建方案

> **ACTIVE** — 2026-05-04
> 背景: `dist/` 内为 Node.js 临时构建产物, Go 前端需从零重写

---

## 一、现状分析

### 1.1 当前架构

```
浏览器 ──▶ Go Portal (:3001) ──▶ PostgreSQL / Redis
                │
                └── router.Static("/assets", "./dist/assets")   ← 服务任何静态文件
                    router.NoRoute → dist/index.html            ← SPA fallback
```

Go 后端已经完成了静态文件服务能力 (`main.go:214-226`)，替换 `dist/` 目录内容即可切换前端。

### 1.2 关键约束

| 约束 | 说明 |
|------|------|
| 境外部署 | AWS (新加坡/东京) 或 Aliyun (东南亚) ECS，带宽按量计费，需控制静态资源体积 |
| 移动优先 | 管理员常在手机端操作，响应式布局关系可用性 |
| 安全敏感 | 涉及充值/余额，CSRF/XSS/CSP 必须严谨 |
| 未来扩展 | 需预留 Playground (SSE streaming)、支付集成、实时通知等复杂交互 |

---

## 二、方案对比

### 2.1 候选方案

| 维度 | A: 纯 HTML + inline JS | B: React SPA (Vite) | C: React SPA (Bun build) ★ |
|------|------------------------|---------------------|---------------------------|
| 静态资源体积 | ~20KB | ~150KB (gzip ~45KB) | ~120KB (gzip ~35KB) |
| 开发效率 | 低 — 手写 DOM | 高 — 组件 + HMR | 高 — 组件 + HMR |
| 交互复杂度 | 低 — 页面级切换 | 高 — 富交互 | 高 — 富交互 |
| Playground 支持 | 困难 | 自然 | 自然 |
| 支付 SDK 集成 | 手动加载 | 组件化封装 | 组件化封装 |
| 移动端适配 | 手写 CSS | Tailwind 响应式 | Tailwind 响应式 |
| 构建工具 | 无 | Node.js / Vite | Bun (单一工具) |
| 与现有 Go 后端 | OK | OK | OK — `bun build` → `dist/` |

> **★ 推荐方案 C** — 理由见下。

### 2.2 为何不选其他方案

| 方案 | 排除原因 |
|------|----------|
| 纯静态 HTML | 无法支撑 Playground (SSE streaming、markdown 渲染、消息状态管理)、支付 SDK 动态加载、复杂表单验证 |
| Go `html/template` SSR | 每次导航刷新页面，无法做 SPA 路由、HMR 开发体验差、第三方 JS SDK 集成困难 |
| Vite (方案 B) | 需要 Node.js 运行时构建，与项目 CLAUDE.md 主张的 "Default to Bun" 不一致 |

---

## 三、推荐方案: React SPA + Bun Build

### 3.1 技术选型

| 层 | 选择 | 理由 |
|----|------|------|
| 框架 | React 18 | 团队已有经验，生态成熟 |
| 构建 | Bun (`bun build`) | 统一工具链，构建速度快于 Vite，产出体积更小 |
| 样式 | Tailwind CSS v4 | 原子化 CSS，gzip 后约 3KB，响应式适配零成本 |
| 图标 | Lucide React | 开源图标库，tree-shaking 好 |
| 图表 | Recharts | 与当前 Node.js 前端一致，Dashboard 直接用 |
| HTTP | 原生 `fetch` | Bun/浏览器均已内置，零依赖 |
| 路由 | React Router v7 | 文件级路由，SPA + 懒加载 |
| 状态 | React Context + useReducer | 轻量，无需 Redux/Zustand |
| 支付 SDK | 按需动态加载 | Stripe.js / PayPal SDK 仅支付页加载 |

### 3.2 目录结构

```
ai-portal-go/
├── web/                              ← 前端源码 (新建)
│   ├── index.html                    ← SPA 入口
│   ├── src/
│   │   ├── main.tsx                  ← React root
│   │   ├── App.tsx                   ← 路由 + 布局
│   │   ├── index.css                 ← Tailwind + 主题变量
│   │   │
│   │   ├── context/
│   │   │   ├── AuthContext.tsx        ← 认证状态 (JWT token, user, role)
│   │   │   └── ThemeContext.tsx       ← light/dark/system 主题
│   │   │
│   │   ├── hooks/
│   │   │   ├── useAuth.ts            ← 登录态检查 + 路由守卫
│   │   │   ├── useApi.ts             ← fetch 封装 + JWT 注入 + 错误处理
│   │   │   └── useSSE.ts             ← SSE streaming (Playground 预留)
│   │   │
│   │   ├── components/
│   │   │   ├── Layout.tsx            ← 响应式布局 (侧边栏 + 顶栏 + 内容区)
│   │   │   ├── Sidebar.tsx           ← 可折叠导航
│   │   │   ├── Header.tsx            ← Logo + 主题切换 + 用户菜单
│   │   │   ├── MobileNav.tsx         ← 移动端汉堡菜单
│   │   │   ├── ThemeToggle.tsx       ← 明/暗/跟随系统
│   │   │   ├── BalanceBadge.tsx      ← 余额显示 (实时刷新)
│   │   │   ├── LoadingSpinner.tsx
│   │   │   └── ErrorBoundary.tsx
│   │   │
│   │   └── pages/
│   │       ├── public/
│   │       │   ├── LoginPage.tsx
│   │       │   ├── RegisterPage.tsx
│   │       │   └── ForgotPasswordPage.tsx
│   │       ├── user/
│   │       │   ├── DashboardPage.tsx
│   │       │   ├── ApiKeysPage.tsx
│   │       │   ├── BillingPage.tsx
│   │       │   ├── RechargePage.tsx    ← 支付集成入口
│   │       │   ├── OrdersPage.tsx
│   │       │   ├── InvoicesPage.tsx
│   │       │   ├── CheckinPage.tsx
│   │       │   ├── ProfilePage.tsx
│   │       │   └── PlaygroundPage.tsx  ← SSE streaming (Phase 2)
│   │       └── admin/
│   │           ├── AdminDashboardPage.tsx
│   │           ├── UsersPage.tsx
│   │           ├── KeyPoolPage.tsx
│   │           ├── ServiceModelsPage.tsx
│   │           ├── ProvidersPage.tsx
│   │           ├── RedemptionPage.tsx
│   │           ├── SubscriptionsPage.tsx
│   │           ├── AuditLogsPage.tsx
│   │           ├── SettingsPage.tsx
│   │           └── ChannelHealthPage.tsx
│   │
│   ├── public/
│   │   └── favicon.svg
│   └── build.ts                       ← Bun build script (生产构建)
│
├── dist/                              ← 构建产出 (Go 服务此目录)
│   ├── index.html
│   ├── assets/
│   └── ...
│
└── cmd/server/main.go                 ← 无需修改 (已支持 Static)
```

### 3.3 构建脚本

```ts
// web/build.ts
const result = await Bun.build({
  entrypoints: ["./src/main.tsx"],
  outdir: "../dist",
  target: "browser",
  minify: true,
  splitting: true,
  sourcemap: "external",
  define: {
    "process.env.NODE_ENV": JSON.stringify("production"),
  },
});

// Copy index.html to dist/
await Bun.write("../dist/index.html", Bun.file("./index.html"));
```

构建命令:
```bash
cd web && bun run build.ts    # → dist/
go build -o portal ./cmd/server  # 含前端产出
./portal                        # 单二进制部署
```

### 3.4 为什么 Bun build 优于 Vite

| 对比 | Vite | Bun |
|------|------|-----|
| 依赖 | Node.js + npm + rollup/esbuild | 仅 Bun (~100MB 单文件) |
| 构建速度 | 中等 (需安装 node_modules) | 快 (~3x Vite, 无安装步骤) |
| 产出体积 | ~150KB (含 runtime) | ~120KB |
| CSS 处理 | PostCSS 插件链 | 原生 CSS 解析 |
| JSX 转换 | @vitejs/plugin-react | 原生支持 |
| devDependencies | ~50 个包 | 0 |

---

## 四、视觉设计规范

### 4.1 设计基调: "简约科技风"

```
配色:
  Light: bg=#f8fafc, text=#1e293b, accent=#6366f1 (indigo-500)
  Dark:  bg=#0f172a, text=#e2e8f0, accent=#818cf8 (indigo-400)

渐变 (仅用于关键 CTA):
  Hero 区域:  bg-gradient-to-br from-indigo-500 via-purple-500 to-pink-500
  按钮悬停:  bg-gradient-to-r from-indigo-500 to-indigo-600

圆角: 12px (卡片), 8px (按钮), 6px (输入框)
阴影: shadow-sm (默认), shadow-md (hover)
间距: gap-4 / p-4 为基本单位
```

### 4.2 响应式断点

| 断点 | 宽度 | 布局 |
|------|------|------|
| Mobile | < 768px | 汉堡菜单, 单列, 卡片全宽 |
| Tablet | 768-1024px | 折叠侧边栏, 双列 |
| Desktop | > 1024px | 固定侧边栏, 多列/表格式 |

### 4.3 关键页面示例 (ASCII 线框)

```
┌─ Mobile ───────────────────────┐  ┌─ Desktop ──────────────────────────────┐
│ ☰ WOODGIT        🌙 👤        │  │  🏠 WOODGIT          🌙 💰 ¥123.45 👤 │
├────────────────────────────────┤  ├──────────┬──────────────────────────────┤
│                                │  │ 📊 仪表盘 │  欢迎回来, Admin           │
│  ┌────────────────────────┐   │  │ 🔑 密钥   │                              │
│  │  💰 余额: ¥123.45      │   │  │ 💵 计费   │  ┌──────┐ ┌──────┐ ┌─────┐ │
│  │  🔑 活跃: 3 把密钥     │   │  │ 📦 订单   │  │用户  │ │密钥  │ │请求 │ │
│  │  📊 今日: 1,234 tokens │   │  │ 🧾 发票   │  │ 128  │ │ 456  │ │8.2K │ │
│  └────────────────────────┘   │  │ ⚙️ 管理   │  └──────┘ └──────┘ └─────┘ │
│                                │  │  用户     │                              │
│  ┌────────────────────────┐   │  │  密钥池   │  📈 用量趋势 (7天)          │
│  │  📈 7 天用量趋势        │   │  │  模型     │  ┌──────────────────────────┐│
│  │  [迷你图表]             │   │  │  供应商   │  │████████▌                ││
│  └────────────────────────┘   │  │  审计     │  │██████                   ││
│                                │  │  设置     │  └──────────────────────────┘│
│  ┌────────────────────────┐   │  │           │                              │
│  │  🔑 我的 API 密钥       │   │  │           │                              │
│  │  sk-a1b2...  [复制]     │   │  │           │                              │
│  │  sk-c3d4...  [复制]     │   │  │           │                              │
│  └────────────────────────┘   │  └──────────┴──────────────────────────────┘
└────────────────────────────────┘
```

---

## 五、安全设计

### 5.1 前端安全措施

| 措施 | 实现 |
|------|------|
| **CSRF Protection** | 所有写请求携带 `X-CSRF-Token` header (从 cookie 读取, 服务端 Set-Cookie) |
| **Content Security Policy** | Go 中间件设置 CSP header: `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' https://api.stripe.com` |
| **XSS 防护** | React 默认 JSX 转义 + DOMPurify (用于 markdown 渲染) |
| **JWT 存储** | httpOnly cookie (非 localStorage, 防 XSS 窃取) |
| **SRI (子资源完整性)** | 构建时生成 hash, HTML 中嵌入 integrity 属性 |
| **HTTPS Only** | 生产环境强制 HTTPS, HSTS header |
| **敏感操作二次确认** | 删除 Key / 修改角色 / 充值 > ¥1000 弹出验证码 |

### 5.2 Go 后端配套安全

```go
// middleware/security.go — 建议新增
func SecurityHeaders() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Header("Content-Security-Policy",
            "default-src 'self'; connect-src 'self' https://api.stripe.com; frame-src https://js.stripe.com https://www.paypal.com")
        c.Header("X-Content-Type-Options", "nosniff")
        c.Header("X-Frame-Options", "DENY")
        c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
        c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
        c.Next()
    }
}
```

---

## 六、支付集成方案

### 6.1 支付方式与前端要求

| 支付方式 | 前端集成方式 | SDK 大小 | HTTS 要求 |
|----------|-------------|----------|-----------|
| **Stripe** | Stripe.js + Elements (动态加载) | ~40KB (async) | ✅ 强制 |
| **PayPal** | PayPal JS SDK (动态加载) | ~45KB (async) | ✅ 强制 |
| **Alipay** | 服务端生成支付链接 → 展示 QR 码 (无 JS SDK) | 0 | ✅ |
| **WeChat Pay** | JSAPI (需微信内置浏览器) / Native QR 码 | 0 (Native) | ✅ |

### 6.2 实现策略

```
支付页面 (RechargePage.tsx)
  ├── 选择金额 / 自定义金额
  ├── 选择支付方式
  │   ├── Alipay  →  POST /api/orders/create → 返回 QR 码 URL → 展示 + 轮询
  │   ├── WeChat  →  POST /api/orders/create → 返回 QR 码 URL → 展示 + 轮询
  │   ├── Stripe  →  动态加载 Stripe.js → Stripe Elements → 收集卡信息 → 确认
  │   └── PayPal  →  动态加载 PayPal SDK → PayPal Buttons → 确认
  └── 支付结果回调 → Webhook (后端处理) → 前端轮询 / SSE 通知
```

**关键: 国内支付 (Alipay/WeChat) 对前端几乎零要求** — 服务端返回 QR 码 URL, 前端只需展示图片 + 轮询订单状态。**不需要任何第三方 JS SDK。**

```tsx
// RechargePage.tsx — Alipay 支付示例
function AlipayPayment({ orderId, qrCodeUrl }: { orderId: string; qrCodeUrl: string }) {
  const { data: status } = usePolling(`/api/orders/${orderId}/status`, 2000);

  if (status === "paid") return <SuccessMessage />;
  return (
    <div className="flex flex-col items-center gap-4">
      <img src={qrCodeUrl} alt="Alipay QR" className="w-48 h-48" />
      <p>请用支付宝扫描二维码</p>
    </div>
  );
}
```

---

## 七、部署适配

### 7.1 AWS / Aliyun ECS 对前端的影响

| 环境因素 | 影响 | 对策 |
|----------|------|------|
| ECS 单实例 | 无影响 — 静态文件由 Go 进程服务 | 无需 CDN, 直接服务 |
| ALB/NLB 前置 | HTTPS 终结在 LB, 后端 HTTP | CSP 需允许 LB 转发头 |
| IAM Role | 不影响前端 | — |
| SSM Parameter Store | 不影响前端 | — |
| Docker 部署 | 前端已在 Go 二进制中 (或挂载 dist/) | Dockerfile 一行 `COPY dist/ /app/dist/` |
| 跨境访问 (中国大陆 → 新加坡) | 静态资源加载可能慢 2-5s | 可选: CloudFront CDN 加速 `/assets/*` |

**结论: 前端部署无特殊要求，与 Go 后端一起打在 Docker 镜像中即可。**

### 7.2 Dockerfile

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o portal ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/portal .
COPY dist/ ./dist/
COPY config/ ./config/
COPY migrations/ ./migrations/
EXPOSE 3001
CMD ["./portal"]
```

---

## 八、Playground 扩展能力评估

### 8.1 Playground 的技术需求

```
┌─────────────────────────────────────┐
│  Playground                         │
│  ┌─────────────────────────────────┐│
│  │  System: 你是一个...             ││
│  │                                 ││
│  │  User: Hello!                   ││
│  │                                 ││
│  │  AI: Hello! How can I help...   ││  ← Markdown 渲染
│  │  ████████░░░░  streaming...     ││  ← SSE streaming
│  └─────────────────────────────────┘│
│  ┌─────────────────────────────────┐│
│  │  Type a message...        [Send]││
│  └─────────────────────────────────┘│
│  Model: [gpt-4o ▼]  Temp: [0.7]    │
└─────────────────────────────────────┘
```

需要的技术能力:
- SSE streaming 读取 (EventSource 或 fetch + ReadableStream)
- Markdown 渲染 (代码高亮 + 数学公式)
- 消息状态管理 (loading / streaming / done / error)
- 自动滚动 + 虚拟滚动 (长对话)
- 模型/参数选择器

### 8.2 当前架构能否支持

| 能力 | 方案 B/C (React SPA) | 方案 A (静态 HTML) |
|------|---------------------|-------------------|
| SSE streaming | ✅ `useSSE` hook | ❌ 手写状态管理困难 |
| Markdown 渲染 | ✅ react-markdown + rehype | ⚠️ 需引入 marked.js |
| 代码高亮 | ✅ Prism.js 懒加载 | ⚠️ |
| 数学公式 | ✅ KaTeX 懒加载 | ⚠️ |
| 虚拟滚动 | ✅ react-virtuoso | ❌ |
| 消息状态管理 | ✅ React state | ❌ 全局变量污染 |

**结论: React SPA 完全可以支撑 Playground 需求，且 Bun build 支持动态 import 做代码分割。**

```tsx
// Playground 页面懒加载 (不影响主包体积)
const PlaygroundPage = lazy(() => import("./pages/user/PlaygroundPage"));
// → 只有访问 /playground 时才加载 ~30KB
```

### 8.3 代码分割策略

```
主包 (首次加载):          ~45KB gzip
  ├── React + ReactDOM    ~30KB
  ├── React Router        ~5KB
  ├── Tailwind CSS        ~3KB
  └── Auth + Layout       ~7KB

懒加载 chunk:
  ├── Dashboard           ~5KB
  ├── ApiKeys             ~4KB
  ├── Playground          ~30KB (含 react-markdown + Prism.js)
  ├── Admin/*             ~8KB
  └── Recharge/Stripe     ~6KB (含支付 SDK 动态加载)
```

---

## 九、实施计划

### Phase 1: 基础框架 + 核心页面 (Week 1-2)

| # | 任务 | 工时 |
|---|------|------|
| 1 | 初始化 web/ 目录, 配置 Bun build + Tailwind | 0.5d |
| 2 | 主题系统 (light/dark/system) + CSS 变量 | 0.5d |
| 3 | Layout 组件 (Sidebar + Header + 响应式) | 2d |
| 4 | Auth 页面 (Login / Register / ForgotPassword) | 2d |
| 5 | `useAuth` hook + AuthContext + 路由守卫 | 1d |
| 6 | Dashboard 页 (统计卡片 + 趋势图) | 2d |
| 7 | API Keys 页 (CRUD + 复制 + 用量) | 2d |

### Phase 2: 用户功能 + 支付 (Week 3-4)

| # | 任务 | 工时 |
|---|------|------|
| 8 | Billing + Usage 记录页 | 1.5d |
| 9 | Recharge 页 (金额选择 + 支付方式) | 1d |
| 10 | Alipay / WeChat Pay QR 码支付流程 | 1d |
| 11 | Stripe / PayPal 支付集成 | 1.5d |
| 12 | Orders + Invoices 页 | 1.5d |
| 13 | Checkin 签到页 | 0.5d |
| 14 | Profile + Settings 页 | 1.5d |

### Phase 3: Admin 后台 (Week 5)

| # | 任务 | 工时 |
|---|------|------|
| 15 | Admin Dashboard (图表 + 统计) | 1.5d |
| 16 | Users 管理 (列表 + 编辑角色/余额) | 1d |
| 17 | Key Pool 管理 (CRUD + import/export) | 1.5d |
| 18 | Service Models / Providers 管理 | 1.5d |
| 19 | Redemption / Subscriptions 管理 | 1.5d |
| 20 | Audit Logs / Settings | 1d |

### Phase 4: 安全加固 + 部署 (Week 6)

| # | 任务 | 工时 |
|---|------|------|
| 21 | CSP / CSRF / SRI 安全头 + 实现 | 1d |
| 22 | 支付流程端到端测试 (沙箱环境) | 1.5d |
| 23 | 移动端响应式全面测试 | 1d |
| 24 | Docker 镜像构建 + 部署脚本 | 1d |
| 25 | AWS/Aliyun ECS 部署验证 | 1d |

### Phase 5: Playground (后续, 独立排期)

| # | 任务 | 工时 |
|---|------|------|
| 26 | SSE streaming hook | 1d |
| 27 | Markdown 渲染 (代码高亮 + 数学公式) | 1d |
| 28 | 聊天 UI (消息列表 + 输入 + 自动滚动) | 1.5d |
| 29 | 模型/参数选择器 | 1d |
| 30 | 会话管理 (历史记录) | 1d |

---

## 十、依赖清单 (零 Node.js 依赖)

```json
// web/package.json — 仅 Bun 构建依赖, 运行时零依赖
{
  "devDependencies": {
    "@types/react": "^19.0.0",
    "@types/react-dom": "^19.0.0"
  },
  "dependencies": {
    "react": "^19.0.0",
    "react-dom": "^19.0.0",
    "react-router-dom": "^7.0.0",
    "recharts": "^2.15.0",
    "lucide-react": "^0.460.0"
  }
}
```

> **不需要** tailwindcss/autoprefixer/postcss — Tailwind v4 支持原生 CSS 导入, Bun 直接解析。

安装方式:
```bash
bun install    # 3 秒内完成 (vs npm install 30s+)
```

---

## 十一、与 Go 后端的 API 契约

前端完全通过以下格式与 Go 后端通信：

**请求:**
```
Authorization: Bearer <jwt_token>
Content-Type: application/json
```

**成功响应:**
```json
{ "key": "value" }
```

**错误响应:**
```json
{ "error": { "message": "描述信息" } }
```

响应字段统一使用 **camelCase** (与前端 JS 习惯一致, Go 后端已通过双 JSON tag 支持)。

---

## 十二、总结

| 问题 | 回答 |
|------|------|
| 纯静态页面是否够用？ | **不够** — Playground/支付/复杂交互需要 React SPA |
| 当前架构是否满足扩展？ | **满足** — Go `router.Static()` 已就绪, React 代码分割支持渐进增强 |
| 境外服务器对前端有影响吗？ | **无** — 静态文件可走 CDN, Docker 化部署与 Go 一体 |
| 支付集成需要什么？ | 国内支付 (Alipay/WeChat) 仅需 QR 码展示, 国际支付 (Stripe/PayPal) 懒加载 SDK |
| 安全性够吗？ | React SPA + CSP + httpOnly JWT + CSRF token — 满足充值场景 |
| 代码体积多大？ | 首次加载 ~45KB gzip, 后续页面懒加载 3-8KB 每个 |

---

*Document Version: 1.0*
