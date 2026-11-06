# AI Portal Go — Frontend Architecture v2

**Status:** ACTIVE
**Created:** 202605042000
**Supersedes:** frontend-architecture-202605041600.md

---

## 1. 方案对比总结

| 维度 | A: Go+HTMX | B: React SPA | C: Preact 轻量 | 共识 |
|------|------------|--------------|----------------|------|
| 体积 | ~50KB | ~150KB | ~80KB | ✅ Preact 折中 |
| SSR | ✅ 原生 | ❌ 需要 | ❌ 需要 | ✅ Server-render 页面 |
| 交互 | HTMX | React | Preact | ✅ HTMX + 少量 inline JS |
| 构建 | 无 | Bun/Vite | Bun | ✅ Bun 统一工具链 |
| Playground | ⚠️ 勉强 | ✅ 自然 | ✅ 可行 | ✅ 按需引入编辑器 |
| 支付 | 跳转/QRD | SDK | SDK | ✅ 各支付方式均支持 |

**核心共识：**
1. 境外部署需要轻量加载 → Preact 比 React 更合适
2. 使用 Bun 作为构建工具（符合 CLAUDE.md）
3. 不需要完整 SPA，页面级交互用 HTMX
4. Playground 作为独立模块按需加载
5. 支付集成：Alipay/WeChat 服务端生成，前端展示

---

## 2. 最终技术栈

### 2.1 分层架构

```
┌─────────────────────────────────────────────────────────────┐
│                      浏览器                                  │
├─────────────────────────────────────────────────────────────┤
│  HTML (Go SSR)  │  HTMX (页面交互)  │  Preact (复杂组件)     │
└────────┬───────┴────────┬──────────┴──────────┬─────────────┘
         │                │                    │
         ▼                ▼                    ▼
┌─────────────────────────────────────────────────────────────┐
│                  Go Server (Gin)                           │
│  ┌──────────┐  ┌──────────┐  ┌──────────────┐  ┌────────┐  │
│  │ Template │  │  HTMX    │  │  REST API    │  │ Preact │  │
│  │ (embed)  │  │ Handlers │  │  (JSON)      │  │ Bundle │  │
│  └──────────┘  └──────────┘  └──────────────┘  └────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 技术选型明细

| 层级 | 技术 | 版本 | 说明 |
|------|------|------|------|
| 后端框架 | Go + Gin | 1.21+ | 标准 |
| 模板引擎 | Go `html/template` + `embed` | 标准库 | SSR 首屏 |
| 轻量交互 | HTMX | 2.x | 14KB，替代 jQuery/AJAX |
| 复杂组件 | Preact | 10.x | 3KB，替代 React |
| 样式 | Tailwind CSS (CDN dev / purged prod) | 3.4 | 原子化 CSS |
| 构建工具 | Bun | 1.x | 统一工具链 (符合 CLAUDE.md) |
| 图表 | Chart.js (按需) | 4.x | 仅 Dashboard |
| 编辑器 | Monaco/CodeMirror (Playground) | 按需 | 仅 Playground |
| 支付 | Stripe.js / PayPal SDK | 按需 | 仅支付页面 |

### 2.3 资源体积预估

| 资源 | 大小 (gzip) | 加载方式 |
|------|-------------|----------|
| HTML (首屏) | ~15KB | Go SSR |
| Tailwind CSS | ~20KB | CDN dev / purged prod |
| HTMX | ~14KB | embed |
| Preact core | ~3KB | embed |
| Preact 复杂组件 | ~10KB | 按需加载 |
| 图标/字体 | ~5KB | 内联 SVG |

**首次加载: ~50KB** (不含 Playground/支付 SDK)

---

## 3. 目录结构

```
ai-portal-go/
├── cmd/server/main.go              # 入口
├── internal/
│   ├── config/                     # 配置
│   ├── db/                         # 数据库
│   ├── middleware/                 # JWT、CORS、安全头
│   ├── routes/                     # REST API
│   └── web/                       # Web 页面处理
│       ├── handler.go
│       ├── pages.go
│       └── middleware.go           # CSRF、CSP
├── web/
│   ├── templates/                  # Go templates (embed)
│   │   ├── layout/
│   │   │   ├── base.html          # 基础布局
│   │   │   ├── header.html
│   │   │   └── sidebar.html
│   │   ├── auth/
│   │   │   ├── login.html
│   │   │   └── register.html
│   │   ├── dashboard.html
│   │   ├── apikeys.html
│   │   ├── billing/
│   │   │   ├── balance.html
│   │   │   └── recharge.html
│   │   ├── playground.html         # Playground (空壳，Preact 接管)
│   │   ├── admin/
│   │   └── errors/
│   ├── static/
│   │   ├── css/
│   │   │   └── main.css           # Tailwind purged
│   │   └── js/
│   │       ├── htmx.min.js        # embed
│   │       └── app.js             # 少量 inline JS
│   └── src/                       # Preact 组件 (Playground 等)
│       ├── main.tsx
│       ├── App.tsx
│       ├── components/
│       │   ├── Editor.tsx          # Monaco wrapper
│       │   ├── Chat.tsx           # 消息列表
│       │   └── Payment.tsx        # Stripe/PayPal
│       └── hooks/
│           └── useSSE.ts
├── dist/                           # 构建产出 (Bun build)
│   ├── assets/
│   │   ├── index-[hash].js        # Preact bundle
│   │   └── index-[hash].css
│   └── index.html
├── migrations/
├── Dockerfile
└── go.mod
```

---

## 4. 响应式设计

### 4.1 断点策略

```css
/* 手机优先 */
@media (max-width: 640px)  { ... }  /* sm */
@media (max-width: 768px)  { ... }  /* md - 平板 */
@media (min-width: 1024px) { ... }  /* lg - 桌面 */
```

### 4.2 布局方案

**桌面 (>1024px):**
```
┌──────────┬────────────────────────────┐
│          │                            │
│ Sidebar  │       Main Content         │
│ (固定)   │       (可滚动)             │
│          │                            │
└──────────┴────────────────────────────┘
```

**手机 (<768px):**
```
┌──────────────────────────────────────┐
│ ☰  Logo                    💰 👤     │
├──────────────────────────────────────┤
│                                      │
│         Main Content                 │
│         (全宽)                       │
│                                      │
└──────────────────────────────────────┘
Bottom Tab: 首页 | 密钥 | 充值 | 我的
```

---

## 5. 安全设计

### 5.1 多层安全

| 层级 | 措施 | 实现 |
|------|------|------|
| 传输 | HTTPS | 强制重定向 |
| 内容 | CSP | `default-src 'self'; script-src 'self' 'unsafe-inline'` |
| 内容 | XSS | Go template 自动转义 |
| 会话 | CSRF Token | 表单 hidden field + X-CSRF-Token header |
| 会话 | Cookie | `HttpOnly; Secure; SameSite=Lax` |
| 会话 | JWT | httpOnly cookie，非 localStorage |
| 应用 | Rate Limit | 登录/支付接口限流 |
| 支付 | 订单幂等 | 服务端生成唯一订单号 |
| 支付 | 金额校验 | 不信任前端金额，后端核算 |

### 5.2 CSP 配置

```go
// 普通页面
c.Header("Content-Security-Policy",
    "default-src 'self'; "+
    "script-src 'self' 'unsafe-inline'; "+
    "style-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com; "+
    "img-src 'self' data: https:; "+
    "font-src 'self';")

// 支付页面 (Stripe/PayPal)
c.Header("Content-Security-Policy",
    "default-src 'self'; "+
    "script-src 'self' https://js.stripe.com https://www.paypal.com; "+
    "frame-src https://js.stripe.com https://hooks.stripe.com https://www.paypal.com; "+
    "connect-src 'self' https://api.stripe.com https://www.paypal.com")
```

---

## 6. 支付集成

### 6.1 各支付方式

| 支付方式 | 前端需求 | SDK | 实现 |
|----------|----------|-----|------|
| Alipay | 跳转/QR | 无 | 服务端返回链接，前端跳转 |
| WeChat Pay | QR 码 | 无 | 服务端返回 QR URL，前端展示 |
| Stripe | 信用卡 | Stripe.js | 支付页面动态加载 |
| PayPal | PayPal 按钮 | SDK | 支付页面动态加载 |

### 6.2 支付流程

```
1. 用户选择金额 + 支付方式
2. 前端 POST /api/orders/create
3. 后端创建订单，返回支付参数
4. 前端根据方式展示：
   - Alipay/WeChat: 跳转或展示二维码
   - Stripe: 调起卡片输入框
   - PayPal: 渲染 PayPal 按钮
5. 支付完成 → webhook 通知后端 → 前端轮询状态
```

### 6.3 关键原则

- **Stripe/PayPal 卡号不经我们服务器**，通过 SDK tokenization
- 国内支付 (Alipay/WeChat) 前端零依赖
- 支付页面独立，可加载外部 JS

---

## 7. Playground 扩展

### 7.1 功能需求

| 功能 | 实现 |
|------|------|
| 代码编辑器 | Monaco Editor (按需加载) |
| SSE streaming | `useSSE` hook |
| Markdown 渲染 | `react-markdown` (Preact) |
| 消息状态管理 | Preact state |
| 自动滚动 | 原生 scrollIntoView |

### 7.2 代码分割

```ts
// Bun build 自动代码分割
const result = await Bun.build({
  entrypoints: ["./src/main.tsx"],
  outdir: "../dist",
  codeSplitting: true,
  minify: true,
});
```

**产出预估:**
- 主包: ~45KB (含 Preact + HTMX 交互)
- Playground chunk: ~30KB (懒加载)

---

## 8. 部署架构

### 8.1 AWS/Aliyun ECS

| 组件 | 影响 | 对策 |
|------|------|------|
| ECS | 无 | Go 二进制运行 |
| Docker | 无 | 多阶段构建 |
| SSM | 无 | 后端读取密钥 |
| ALB/SLB | 轻微 | 配置 health check |
| CDN | 可选 | 加速 `/static/` |

### 8.2 Docker 多阶段构建

```dockerfile
# Stage 1: Build Preact frontend
FROM oven/bun:latest AS frontend
WORKDIR /app
COPY web/package.json bun.lock ./
RUN bun install
COPY web/ ./
RUN bun run build

# Stage 2: Build Go backend
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/dist ./dist/
RUN go build -o portal ./cmd/server

# Final
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/portal .
COPY --from=builder /app/migrations ./migrations/
EXPOSE 3001
ENTRYPOINT ["./portal"]
```

---

## 9. 开发计划

### Phase 1: 基础设施 (Week 1)

| 任务 | 产出 |
|------|------|
| 项目初始化 | Bun + Preact + Tailwind |
| Go embed 集成 | `web/embed.go` |
| 基础模板 | `layout.html` + `main.css` |
| 登录/注册 | 完整功能 |

### Phase 2: 核心页面 (Week 2-3)

| 任务 | 产出 |
|------|------|
| Dashboard | 统计卡片 + 趋势图 |
| API Keys | CRUD + 复制 |
| 响应式适配 | 手机/平板/桌面 |

### Phase 3: 支付集成 (Week 4)

| 任务 | 产出 |
|------|------|
| 充值页面 | 金额选择 + 支付方式 |
| Stripe/PayPal | SDK 集成 |
| Alipay/WeChat | QR 码展示 |

### Phase 4: Playground (Week 5, Optional)

| 任务 | 产出 |
|------|------|
| 编辑器 | Monaco 集成 |
| SSE | 流式响应 |
| Chat UI | 消息列表 |

### Phase 5: 安全 + 部署 (Week 6)

| 任务 | 产出 |
|------|------|
| 安全加固 | CSP + CSRF + SRI |
| Docker 部署 | 镜像 + 脚本 |
| 云部署验证 | AWS/Aliyun |

---

## 10. 团队分工参考

| 角色 | 任务 | 文档 |
|------|------|------|
| Agent 1 (Code) | 实现前端核心代码 | 本文档 v2 |
| Agent 2 (Unit Test) | 组件测试 + 集成测试 | test-plan.md |
| Agent 3 (Service Test) | 服务测试 + 压力测试 | stress-test.md |
| Agent 4 (Review) | 代码审查 + 安全审计 | review-report.md |

---

## 11. 关键决策回顾

| 问题 | 答案 |
|------|------|
| 为什么 Preact 不是 React？ | 体积 3KB vs 40KB，足够应付复杂交互 |
| 为什么用 HTMX？ | 80% 交互无需 JS，渐进增强 |
| 为什么 Bun？ | 统一工具链，符合 CLAUDE.md |
| 为什么 embed？ | 单二进制部署，零外部依赖 |
| Playground 怎么办？ | Preact 懒加载，不影响主包 |
| 支付安全吗？ | Stripe SDK tokenization，PCI 合规 |

---

**文档版本:** 2.0
**更新时间:** 2026-05-04

> **参考文档:**
> - `frontend-architecture-202605040443.md` (方案A - HTMX派)
> - `frontend-rebuild-proposal-202605040000.md` (方案B - React SPA派)
> - `web-frontend-architecture-202605040442.md` (方案C - Go原生派)
> - `frontend-plan-20260503.md` (方案D - 传统React派)
