# Go Portal 前端重建方案 V2

> **ACTIVE** — 2026-05-04
> V1: `frontend-rebuild-proposal-202605040000.md` (React+Bun)
> V2: 综合 `web-frontend-architecture`, `frontend-architecture`(×2), `frontend-plan` 四份方案

---

## 零、多方案横评与 V2 决策

### 0.1 四份方案的异同

| 维度 | web-frontend | frontend-arch | frontend-plan | 本文 V1 | **V2 采纳** |
|------|:-----------:|:------------:|:------------:|:------:|:----------:|
| 渲染方式 | SSR 模板 | SSR 模板 | React SPA | React SPA | **SSR 模板 + HTMX** |
| JS 框架 | HTMX (14KB) | HTMX+Alpine | React+Vite | React+Bun | **HTMX+Alpine** |
| CSS 方案 | Pure CSS | Tailwind CDN | Tailwind Vite | Tailwind Bun | **Tailwind CDN → purged** |
| 构建工具 | 无 | 无 | Vite (Node) | Bun | **无 (CDN) → 可选 purge** |
| go:embed | ✅ | ✅ | ✅ | ✅ | ✅ |
| 支付 QR | ✅ | ✅ | — | ✅ | ✅ |
| 支付 SDK | 按需加载 | 按需加载 | — | 按需加载 | **支付页独立 CSP** |
| 首屏大小 | 37KB | ~50KB | ~150KB | ~45KB | **~40KB** |
| 工时估算 | 25h | 5周 | 5周 | 6周 | **~30h 纯前端 + 安全/支付** |

**结论**: 五份方案在三个核心决策上**高度一致** — `go:embed`嵌入、QR码支付>SDK支付、Playground按需加载；分歧仅在 JS 框架选择。

### 0.2 V2 决策逻辑

```
                无 Node.js 依赖 ──▶ 排除 Vite/React/Bun build
                          │
        ┌─────────────────┼──────────────────┐
        ▼                 ▼                  ▼
   Pure HTML         HTMX+Alpine        Preact SPA
   (交互太弱)          (平衡之选)          (仍有 JS 构建)
                          │
                    共识: HTMX + Go 模板
                    10% 动态: Alpine.js
                    支付页: Stripe/PayPal SDK
                    未来: Playground 独立加载 Monaco
```

**V2 最终技术栈**:

| 层 | 选择 | 体积 | 说明 |
|----|------|------|------|
| 模板 | Go `html/template` | 零依赖 | 自动 XSS 转义, 类型安全 |
| 资源 | `//go:embed` | 零文件 | 嵌入二进制, 单文件部署 |
| 交互 | HTMX 2.x | 14KB | AJAX/表单/轮询/确认/SSE |
| 复杂交互 | Alpine.js 3.x | 15KB | dropdown/tab/modal (可选) |
| 样式 | Tailwind CDN + 自定义 CSS | ~10KB | CDN 开发, 生产 purge 或直接用 |
| 图表 | Chart.js (按需) | 60KB | 仅 Dashboard 页加载 |
| 图标 | Lucide SVG 内联 | ~300B/个 | 零请求 |
| 支付 | Stripe.js / PayPal SDK | 按需 | 仅支付 checkout 页加载 |

---

## 一、用户诉求逐条回应 (综合四方案)

### 1.1 境外部署 + 加载快 + 少依赖

四个方案的共识与强化:

| 多方案建议 | 决策 |
|-----------|------|
| web-frontend: 零 CDN, 全资源本地 | ✅ 采纳 — 生产不依赖 jsdelivr/unpkg |
| frontend-arch: HTMX 14KB gzip | ✅ 采纳 |
| frontend-plan: Vite 构建 → 排除 | ✅ 排除 — 不引入 Node.js |
| V1: Bun build → 仍需要 JS 运行时 | ✅ 排除 — 零构建更纯粹 |

**最终首屏**: HTML 15KB + CSS 8KB + HTMX 14KB = **~37KB gzip**, < 500ms TTFB.

### 1.2 简约科技风 + 背景渐变 + 响应式

综合 web-frontend (Pure CSS) 和 frontend-arch (Tailwind CDN) 的精华:

```
配色 (CSS 变量, 明暗两套):
  Light: --bg: #f8fafc   --card: #ffffff   --text: #1e293b   --accent: #6366f1
  Dark:  --bg: #0f172a   --card: #1e293b   --text: #e2e8f0   --accent: #818cf8

渐变 (仅 3 处):
  Hero / 登录背景:  bg-gradient-to-br from-indigo-500/10 via-purple-500/5 to-transparent
  关键 CTA 按钮:   bg-gradient-to-r from-indigo-500 to-indigo-600
  卡片 hover:      border-indigo-500/30 shadow-lg shadow-indigo-500/10

圆角: card=12px, button=8px, input=6px
阴影: shadow-sm 默认, hover 升 shadow-md + translateY(-2px)
字体: system-ui stack (Inter / Noto Sans SC — 已在 dist/fonts/)
```

**响应式策略** (三方案共识):
```html
<!-- 桌面: 固定侧边栏 -->
<aside class="hidden lg:block lg:w-64 lg:fixed lg:h-full">...</aside>

<!-- 手机: 汉堡菜单 → overlay -->
<button class="lg:hidden" onclick="toggleMenu()">☰</button>
```

### 1.3 安全措施强化 (充值场景)

综合 test-and-security-plan 的四层安全矩阵 + web-frontend 的 CSRF 实现:

```go
// 新增 middleware/security.go — 五方案共识
func SecurityHeaders() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Header("Content-Security-Policy",
            "default-src 'self'; "+
            "script-src 'self'; "+          // 无 unsafe-inline 除非支付页
            "style-src 'self' 'unsafe-inline'; "+
            "img-src 'self' data: https:; "+
            "frame-ancestors 'none'; "+
            "base-uri 'self'")
        c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
        c.Header("X-Frame-Options", "DENY")
        c.Header("X-Content-Type-Options", "nosniff")
        c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
        c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
        c.Next()
    }
}

// 支付页面宽松 CSP (仅在 /billing/checkout)
func PaymentSecurityHeaders() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Header("Content-Security-Policy",
            "default-src 'self'; "+
            "script-src 'self' https://js.stripe.com https://www.paypal.com; "+
            "frame-src https://js.stripe.com https://www.paypal.com; "+
            "connect-src 'self' https://api.stripe.com")
        c.Next()
    }
}
```

| 层 | 措施 | V2 实现 |
|----|------|---------|
| 传输 | HTTPS + HSTS | SecurityHeaders 中间件 |
| 内容 | CSP + XSS 防护 | Go template `{{.}}` 自动转义 |
| 会话 | JWT httpOnly + SameSite=Strict | 已有 `middleware/auth.go` |
| 表单 | CSRF Token 双重验证 | 每个 `<form>` 嵌入 `{{.CSRFToken}}` |
| 接口 | Rate Limiting | 登录 5次/min, 支付 3次/min |
| 审计 | 所有敏感操作记录 | 已有 `pkg/audit/` |

### 1.4 静态页面 vs inline JS → HTMX + Alpine (四方案共识)

| 交互类型 | 实现 | 示例 |
|----------|------|------|
| 页面导航 | HTMX `hx-boost` | 所有链接 AJAX 化 |
| 表单提交 | HTMX `hx-post` | 登录/注册/创建 Key |
| 删除确认 | HTMX `hx-delete` + `hx-confirm` | 删除 Key"确认？" |
| 实时轮询 | HTMX `hx-get` + `hx-trigger="every 5s"` | 支付结果查询 |
| 搜索过滤 | HTMX `hx-trigger="keyup delay:300ms"` | 用户/模型搜索 |
| 下拉/弹窗/标签 | Alpine.js `x-data` `x-show` | 用户菜单、主题切换 |
| 复制到剪贴板 | Inline `onclick` (3行) | API Key 复制 |
| 图表 | Chart.js (按需) | Dashboard 页 |

### 1.5 Playground 扩展性

五方案完全一致: Playground 作为**独立功能模块**在特定页面按需加载，不增加全局负担。

```
当前架构                     Playground 扩展
  ┌─────────────────┐         ┌──────────────────────┐
  │ 90% 页面: HTMX  │         │ /playground 独立页面  │
  │ 10% 动态: Alpine│         │ ├── Monaco Editor    │ ← ~2MB (gzip ~500KB)
  │ 首屏 ~40KB      │         │ ├── SSE streaming    │ ← Go 原生支持
  └─────────────────┘         │ ├── Markdown 渲染     │ ← marked.js ~20KB
                              │ └── 代码高亮          │ ← highlight.js ~30KB
                              └──────────────────────┘
                              仅访问该页才加载
```

### 1.6 AWS/Aliyun ECS 部署 (四方案共识)

| 部署要素 | 对前端影响 | 决策 |
|----------|-----------|------|
| ECS | 无 — 单 Go 二进制 | `go:embed` 嵌入所有资源 |
| SSM/IAM | 无 — 仅后端感知 | 密钥由 Go 从环境变量/SSM 读取 |
| Docker | 无 — 多阶段构建 | `FROM alpine` + 复制二进制 |
| ALB/SLB | HTTPS 终结在 LB | 后端 `router.Run(":3001")` |
| CDN | 可选 — 加速 /static/ | CloudFront 缓存 `cache-control: 1y` |

**Dockerfile** (四方案一致):
```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 模板/静态文件已通过 go:embed 嵌入 — 无需 COPY dist/
RUN CGO_ENABLED=0 go build -o portal ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /app/portal .
COPY --from=builder /app/migrations ./migrations/
EXPOSE 3001
CMD ["./portal"]
```

### 1.7 支付集成 (五个方案共识)

| 支付方式 | SDK | 前端工作 | PCI DSS |
|----------|-----|----------|---------|
| Alipay | 无 | 展示 QR 码 + 轮询状态 | 无 |
| WeChat Pay | 无 | 展示 QR 码 (NATIVE) | 无 |
| Stripe | Stripe.js | Elements 收集卡号 (不进我们服务器) | Level 1 |
| PayPal | PayPal SDK | Buttons 渲染 | Level 1 |

**统一支付页流程** (综合 web-frontend + frontend-arch):
```
[选择金额+方式] → POST /api/orders/create → 返回 payment_url / client_secret
                                            │
                    ┌───────────────────────┼──────────────────────┐
                    ▼                       ▼                      ▼
              Alipay/WeChat           Stripe                  PayPal
              展示 QR 码            动态加载 stripe.js        动态加载 SDK
              <img src="qr">         Stripe Elements         PayPal Buttons
              轮询 /order/status     前端收集→createToken     前端确认→onApprove
                    │                       │                      │
                    └───────────────────────┴──────────────────────┘
                                            │
                                    后端 Webhook 回调
                                    前端 HTMX 轮询展示结果
```

---

## 二、技术架构 V2

### 2.1 整体架构

```
浏览器 ────────────HTMX (AJAX)──────────▶ Go Portal (:3001)
  │                                              │
  │  HTML (模板渲染)                               ├── JSON API (不变)
  │  CSS (Tailwind + 自定义)                      ├── HTML Page Handler (新增)
  │  Alpine.js (dropdown/tab)                     ├── CSRF/Security 中间件 (新增)
  │  Chart.js (Dashboard, 按需)                   ├── go:embed 静态资源 (新增)
  │  Stripe/PayPal (支付页, 按需)                  │
  │                                              └── PostgreSQL / Redis
  └──────────────────────────────────────────────┘
```

### 2.2 目录结构

```
ai-portal-go/
├── cmd/server/main.go                  # 修改: 初始化模板 + 注册 web 路由
├── internal/
│   ├── config/
│   ├── db/
│   ├── middleware/
│   │   ├── auth.go                     # 不变
│   │   └── security.go                 # 新增: CSP/HSTS/CSRF
│   ├── routes/                         # 不变: JSON API
│   └── web/                            # 新增: HTML 页面
│       ├── handler.go                  # Handler 结构体
│       ├── pages.go                    # 页面路由 (GET)
│       ├── data.go                     # 模板数据获取
│       └── helpers.go                  # 模板函数 (formatAmount等)
├── web/                                # 新增: 模板 + 静态资源
│   ├── embed.go                        # go:embed 声明
│   ├── templates/
│   │   ├── layout.html                 # 基础布局
│   │   ├── partials/
│   │   │   ├── sidebar.html            # 桌面侧边栏
│   │   │   ├── mobile-nav.html         # 移动端底部导航
│   │   │   ├── flash.html              # 消息提示
│   │   │   └── pagination.html         # 分页
│   │   ├── auth/
│   │   │   ├── login.html
│   │   │   ├── register.html
│   │   │   └── forgot-password.html
│   │   ├── dashboard.html
│   │   ├── apikeys.html
│   │   ├── billing/
│   │   │   ├── balance.html
│   │   │   ├── usage.html
│   │   │   └── recharge.html           # 支付入口
│   │   ├── checkout.html               # 支付收银台 (独立 CSP)
│   │   ├── admin/
│   │   │   ├── dashboard.html
│   │   │   ├── users.html
│   │   │   ├── keypool.html
│   │   │   ├── models.html
│   │   │   ├── providers.html
│   │   │   ├── redemption.html
│   │   │   ├── subscriptions.html
│   │   │   ├── audit-logs.html
│   │   │   └── settings.html
│   │   └── errors/
│   │       ├── 404.html
│   │       └── 500.html
│   └── static/
│       ├── css/
│       │   └── app.css                 # 自定义样式 (~300行)
│       └── js/
│           ├── htmx.min.js             # HTMX 2.x (14KB)
│           └── alpine.min.js           # Alpine 3.x (15KB, 可选)
├── migrations/
├── Dockerfile
└── go.mod
```

### 2.3 核心代码示例

**`web/embed.go`:**
```go
package web

import "embed"

//go:embed templates/*
var TemplatesFS embed.FS

//go:embed static/*
var StaticFS embed.FS
```

**`internal/web/handler.go`:**
```go
package web

import (
    "bytes"
    "html/template"
    "net/http"

    "github.com/ai-proxy/ai-portal-go/internal/db"
    "github.com/ai-proxy/ai-portal-go/internal/middleware"
    "github.com/gin-gonic/gin"
)

type Handler struct {
    tmpl *template.Template
    db   *db.DB
    auth *middleware.Auth
}

func NewHandler(db *db.DB, auth *middleware.Auth) (*Handler, error) {
    tmpl, err := template.New("").Funcs(template.FuncMap{
        "formatAmount": func(cents int64) string {
            return fmt.Sprintf("%.2f", float64(cents)/100)
        },
        "formatDate": func(t time.Time) string {
            return t.Format("2006-01-02 15:04")
        },
        "safeHTML": func(s string) template.HTML {
            return template.HTML(s)
        },
    }).ParseFS(web.TemplatesFS, "templates/**/*.html")
    if err != nil {
        return nil, err
    }
    return &Handler{tmpl: tmpl, db: db, auth: auth}, nil
}

func (h *Handler) render(c *gin.Context, name string, data gin.H) {
    if data == nil {
        data = gin.H{}
    }
    data["CSRFToken"] = c.GetString("csrf_token")
    data["User"] = gin.H{
        "ID":    c.GetString("user_id"),
        "Email": c.GetString("email"),
        "Role":  c.GetString("role"),
    }
    buf := &bytes.Buffer{}
    if err := h.tmpl.ExecuteTemplate(buf, name, data); err != nil {
        c.String(500, "Render error")
        return
    }
    c.Data(200, "text/html; charset=utf-8", buf.Bytes())
}
```

**`cmd/server/main.go` 修改:**

```go
// 在现有 router 初始化之后, API 路由之前:

// 初始化 web handler
webHandler, err := web.NewHandler(database, auth)
if err != nil {
    logger.Fatal("Failed to load web templates", zap.Error(err))
}

// 安全中间件
router.Use(middleware.SecurityHeaders())
router.Use(middleware.CSRFMiddleware(cfg.Auth.JWTSecret))

// 静态资源 (CSS, JS, 图标)
staticSub, _ := fs.Sub(web.StaticFS, "static")
router.StaticFS("/static", http.FS(staticSub))

// HTML 页面路由
router.GET("/", webHandler.HomePage)
router.GET("/login", webHandler.LoginPage)
router.GET("/register", webHandler.RegisterPage)

// 需要登录
authPages := router.Group("", auth.RequireAuth())
authPages.GET("/dashboard", webHandler.DashboardPage)
authPages.GET("/apikeys", webHandler.APIKeysPage)
authPages.GET("/billing", webHandler.BillingPage)
// ... 更多页面

// 管理员页面
adminPages := router.Group("/admin", auth.RequireAuth(), auth.RequireAdmin())
adminPages.GET("/dashboard", webHandler.AdminDashboardPage)
adminPages.GET("/users", webHandler.AdminUsersPage)
// ... 更多页面

// API 路由保持不变
api := router.Group("/api")
// ... 现有路由 ...
```

---

## 三、四 Agent 协作计划

```
┌─────────────────────────────────────────────────────────────┐
│                       主 Agent (协调)                        │
│  检查 Progress / Run Tests / Merge Results                   │
├────────────┬─────────────────┬────────────────┬──────────────┤
│  Agent 1   │    Agent 2      │   Agent 3     │   Agent 4    │
│  前端开发  │  单元+集成测试   │  业务+压力测试  │   安全审查    │
└────────────┴─────────────────┴────────────────┴──────────────┘
```

### Agent 1: 前端代码开发 (Code)

**产出物**: 所有 `web/` 目录文件 + `internal/web/` Go 代码

| Phase | 任务 | 文件 |
|-------|------|------|
| **P1: 基础设施** | embed.go, layout.html, app.css, 安全中间件, handler | ~10 文件 |
| **P2: 认证页面** | login, register, forgot-password HTML + 对应 Go handler | ~6 文件 |
| **P3: 用户页面** | dashboard, apikeys, billing, balance, usage | ~10 文件 |
| **P4: 支付** | recharge.html, checkout.html + 支付中间件 | ~5 文件 |
| **P5: Admin** | admin/* 全部页面 + Chart.js 集成 | ~12 文件 |

**开发顺序**: `基础设施 → 认证 → 仪表盘 → API Keys → 计费 → 支付 → Admin`

**构建/运行命令**:
```bash
go build -o portal ./cmd/server   # 模板已 go:embed, 零额外构建
./portal                          # 直接运行
```

### Agent 2: 单元测试 + 集成测试 (Test)

**测试框架**: Go `testing` + `httptest` + `miniredis`

| 层 | 测试内容 | 预计文件 |
|----|----------|----------|
| 模板渲染 | 每个 `.html` 模板能否成功 Execute | `web/template_test.go` |
| 页面 Handler | GET 请求返回 200 / 401 / 403 | `web/pages_test.go` |
| CSRF | 表单提交 token 验证 | `middleware/security_test.go` |
| 集成 | 完整用户流程: 注册→登录→创建 Key→dashboard | `integration/flow_test.go` |
| API 兼容 | 100+ 端点测试 (复用 test-api.ts 逻辑) | `integration/api_test.go` |

**运行**:
```bash
go test ./... -v -count=1
go test ./internal/web/ -coverprofile=coverage.out
```

### Agent 3: 业务测试 + 压力测试 (Service & Stress Test)

**工具**: `wrk` / `k6` + curl 脚本

| 测试 | 指标 | 目标 |
|------|------|------|
| 首屏加载 | TTFB + FCP + LCP | < 500ms (首尔→新加坡) |
| 页面切换 | HTMX AJAX 时间 | < 200ms |
| API 吞吐 | RPS | > 2000 |
| P99 延迟 | — | < 100ms |
| 内存占用 | docker stats | < 50MB |
| 并发 1000 连接 | 成功率 | 99.9% |
| 支付流程 | 创建订单→查询→标记支付→余额更新 | 全通过 |

**运行**:
```bash
wrk -t4 -c100 -d30s http://localhost:3001/api/health
wrk -t4 -c100 -d30s http://localhost:3001/dashboard
```

### Agent 4: 安全审查 (Review)

**对照 test-and-security-plan 的安全矩阵:**

| 类别 | 测试项 | 工具 |
|------|--------|------|
| 认证 | JWT 破解/过期/篡改 | curl + jwt.io |
| 授权 | 水平越权/垂直越权/IDOR | curl + 脚本 |
| 输入 | SQL注入/XSS/路径遍历/超长输入 | sqlmap + 手写 |
| 数据 | 密码hash检查/日志泄露/错误信息 | grep + curl |
| 配置 | CORS/HTTPS/RateLimit/端口 | nmap + 手写 |
| 前端 | CSP头/XSS沙箱/CSRF token | 浏览器 DevTools |
| 支付 | PCI-DSS checklist/金额校验 | 手动审计 |
| 依赖 | Go依赖漏洞扫描 | govulncheck |

**产出**: `docs/reports/security-audit-*.md`

---

## 四、前端页面清单

### 公开页面 (无登录)

| 路由 | 模板 | 说明 |
|------|------|------|
| `/` | `landing.html` | 产品首页 (可选, 也可直接跳转到 /login) |
| `/login` | `auth/login.html` | 邮箱 + 密码登录 |
| `/register` | `auth/register.html` | 邮箱验证码 + 密码注册 |
| `/forgot-password` | `auth/forgot-password.html` | 验证码重置密码 |

### 用户页面 (需 JWT)

| 路由 | 模板 | HTMX 动态区 |
|------|------|------------|
| `/dashboard` | `dashboard.html` | 统计卡片 (hx-get="/api/dashboard/stats") |
| `/apikeys` | `apikeys.html` | 列表 + 创建表单 + 删除确认 |
| `/billing` | `billing/balance.html` | 余额 + 用量表格 |
| `/billing/recharge` | `billing/recharge.html` | 金额选择 → checkout |
| `/billing/checkout` | `checkout.html` | **独立 CSP**, 动态加载支付 SDK |
| `/orders` | `orders.html` | 订单列表 |
| `/invoices` | `invoices.html` | 发票记录 |
| `/settings` | `settings.html` | 改密码 + 资料 |
| `/checkin` | `checkin.html` | 签到日历 |

### 管理员页面 (需 admin role)

| 路由 | 模板 | HTMX 动态区 |
|------|------|------------|
| `/admin` | `admin/dashboard.html` | Chart.js 图表 |
| `/admin/users` | `admin/users.html` | 用户表格 + 编辑弹窗 |
| `/admin/keypool` | `admin/keypool.html` | Key 池 CRUD |
| `/admin/models` | `admin/models.html` | 模型列表 + 状态切换 |
| `/admin/providers` | `admin/providers.html` | 供应商配置 |
| `/admin/redemption` | `admin/redemption.html` | 兑换码生成/管理 |
| `/admin/subscriptions` | `admin/subscriptions.html` | 订阅计划 |
| `/admin/audit-logs` | `admin/audit-logs.html` | 审计日志查询 |
| `/admin/settings` | `admin/settings.html` | 系统设置 |

### 未来 (Phase 2+)

| 路由 | 说明 |
|------|------|
| `/playground` | 独立页面: Monaco + SSE + Markdown — 不影响主包 |

---

## 五、实施计划

### Agent 1 开发排期: ~30h 纯前端

```
Day 1-2: Phase 1 基础设施 (6h)
  ├── web/embed.go + 目录结构
  ├── internal/web/handler.go + helpers.go + data.go
  ├── internal/middleware/security.go (CSP + CSRF)
  ├── templates/layout.html + sidebar + mobile-nav
  ├── static/css/app.css (主题变量 + 响应式 + 渐变)
  └── cmd/server/main.go 修改

Day 3: Phase 2 认证页面 (4h)
  ├── login.html + register.html + forgot-password.html
  └── 对应 Go handler

Day 4-5: Phase 3 用户页面 (8h)
  ├── dashboard + apikeys + billing + orders + settings + checkin
  └── 所有 HTMX 交互 + 表单

Day 6: Phase 4 支付 (4h)
  ├── recharge.html (金额选择 + 支付方式)
  ├── checkout.html (独立 CSP + Stripe/PayPal 动态加载)
  └── QR 码展示 (Alipay/WeChat)

Day 7-8: Phase 5 Admin (8h)
  ├── admin/dashboard (Chart.js 图表)
  ├── admin/users + keypool + models + providers
  └── admin/redemption + subscriptions + audit + settings

Day 9-10: 集成 + 安全 + 响应式优化 (4h buffer)
```

### 并行工作流

```
Agent 1: ───P1───P2───P3───P4───P5─── 优化 ──▶
Agent 2:       ──P1测试──P2测试──P3测试──P4-5测试──▶
Agent 3:            ──API压测──页面压测──支付流程──▶
Agent 4:  ──代码审查──安全扫描──CSP验证──支付审计──▶
```

---

## 六、对 Go 后端的影响范围

### 新增文件 (14 个)

```
internal/middleware/security.go        # SecurityHeaders + CSRF
internal/web/handler.go                # Handler 结构体
internal/web/pages.go                  # 所有页面 GET handler
internal/web/data.go                   # DB 查询辅助
internal/web/helpers.go                # 模板函数
web/embed.go                           # go:embed
web/templates/layout.html
web/templates/partials/*.html          # sidebar, mobile-nav, flash, pagination
web/templates/auth/*.html              # login, register, forgot
web/static/css/app.css
web/static/js/htmx.min.js
```

### 修改文件 (1 个)

`cmd/server/main.go` — 添加 web handler 初始化 + 页面路由注册 + 安全中间件 + 静态资源服务

### 不影响文件

- `internal/routes/*.go` — 全部 JSON API **不变**
- `internal/middleware/auth.go` — JWT 认证**复用**
- `internal/db/*` — 数据库**不变**
- `pkg/*` — 工具包**不变**

---

## 七、总结

| 问题 | V2 回答 |
|------|---------|
| 依赖多吗？ | **零 npm, 零 Node.js, 零构建**, 仅 HTMX(14KB)+Alpine(15KB) 可选 JS |
| 加载快吗？ | 首屏 **~37KB gzip**, SSR 直出无白屏 |
| 风格？ | 简约科技风, CSS 变量明暗主题, 渐变仅 3 处 |
| 响应式？ | 移动端优先, desktop=sidebar, mobile=汉堡+底部导航 |
| 安全？ | CSP+CSRF+HSTS+httpOnly Cookie+RateLimit+支付独立 CSP |
| 够不够扩展？ | HTMX 覆盖 90% 交互, Alpine 覆盖复杂 UI, Playground 独立按需加载 |
| ECS 部署？ | `go:embed` 单二进制, Dockerfile 三行 |
| 支付？ | Alipay/WeChat 零 SDK (QR), Stripe/PayPal 支付页动态加载 |
| 团队？ | Agent1 coding + Agent2 testing + Agent3 stress + Agent4 review, 并行推进 |

---

*Document Version: 2.0*

*Changelog:*
- *V1 → V2: 采纳四份方案共识, 技术栈从 React+Bun 改为 HTMX+Go template+Alpine*
- *V2 新增: 多方案横评表, 四 Agent 协作计划, 支付独立 CSP 设计, 安全中间件代码*
