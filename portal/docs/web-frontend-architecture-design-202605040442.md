# AI Portal Go — Web Frontend Architecture (Go-Native)

> **ACTIVE** — Created: 2026-05-04

---

## 一、方案概述

本文档确认 AI Portal Go 的 Web 前端架构方案。采用 **Go `html/template` + `embed` + HTMX + Pure CSS**，零 Node.js 依赖，所有资源内嵌于单一 Go 二进制文件，适用于境外服务器部署。

---

## 二、用户诉求逐条回应

### 2.1 诉求一：境外部署，加载快，依赖少

**回应：全力支持。**

| 决策 | 理由 |
|------|------|
| 零外部 CDN | 境外 CDN（jsdelivr/unpkg）在部分区域加载不稳定，全部资源本地托管 |
| 单一 HTMX 文件 | `htmx.min.js` 仅 ~14KB，可内嵌于二进制，无其他 JS 框架 |
| 纯 CSS | 单文件 `main.css` ~500 行，无 Tailwind/SCSS 构建链 |
| `go:embed` | 模板 + CSS + JS 全部嵌入二进制，部署只需一个可执行文件 |
| 首屏直出 | Server-Side Rendering，浏览器收到即渲染，无白屏等待 |

**首屏加载预估**：
- HTML: ~15KB (gzip)
- CSS: ~8KB (gzip)
- HTMX: ~14KB (gzip)
- **总计: ~37KB**，无额外请求，< 100ms TTFB 即可渲染。

### 2.2 诉求二：简约科技风 + 背景色 + 渐变，适配电脑与手机

**回应：响应式设计，移动端优先。**

**视觉风格**：
- 背景：柔和渐变（`linear-gradient(135deg, #f5f7fa 0%, #e4e8ec 100%)` 浅色模式）
- 卡片：白色/半透明背景 + 细微阴影 + 圆角 `8px`
- 强调色：科技蓝 `#2563eb` + 成功绿 `#16a34a` + 警告橙 `#f59e0b`
- 字体：系统字体栈，无外部字体加载

**响应式断点**：
```css
/* 手机 */
@media (max-width: 640px) { ... }
/* 平板 */
@media (min-width: 641px) and (max-width: 1024px) { ... }
/* 电脑 */
@media (min-width: 1025px) { ... }
```

**移动端适配**：
- 侧边栏 → 底部 Tab 导航（手机）/ 汉堡菜单（小平板）
- 数据表格 → 卡片列表（手机）
- 表单一行 → 垂直堆叠（手机）
- 字体大小：相对单位 `rem`，根字体随屏幕缩放

### 2.3 诉求三：安全措施强化（客户充值场景）

**回应：多层安全设计。**

| 层级 | 措施 | 实现方式 |
|------|------|----------|
| 传输层 | HTTPS 强制 | `Strict-Transport-Security: max-age=31536000; includeSubDomains` |
| 传输层 | TLS 1.2+ | 服务端配置 |
| 内容层 | CSP | `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'` |
| 内容层 | CSRF Token | 每个表单内置 `<input type="hidden" name="csrf_token">`，服务端验证 |
| 内容层 | X-Frame-Options | `DENY` — 防止点击劫持 |
| 内容层 | X-Content-Type-Options | `nosniff` |
| 会话层 | Secure Cookie | `HttpOnly; Secure; SameSite=Strict` |
| 会话层 | JWT 过期 | 短时效 access token（1h）+ refresh token（7d）|
| 应用层 | 输入净化 | 已有 `middleware.SanitizeInput()`，去除 null bytes |
| 应用层 | 速率限制 | 登录/注册/支付接口限流：5 次/分钟/IP |
| 应用层 | 审计日志 | 所有敏感操作记录用户、IP、时间、操作类型 |
| 支付层 | 订单幂等 | 支付前生成唯一订单号，防止重复支付 |
| 支付层 | 金额校验 | 服务端重新计算金额，不信任前端传入 |

### 2.4 诉求四：纯粹静态页面扩展性？是否加入少量 inline JS？

**回应：少量 inline JS 不仅可行，而且是最佳实践。**

**HTMX 覆盖范围（~80% 交互）**：
- 页面切换：`hx-boost="true"`（AJAX 化所有链接）
- 表单提交：`hx-post` + `hx-target`
- 数据加载：`hx-get` + `hx-trigger`
- 删除确认：`hx-delete` + `hx-confirm`
- 搜索过滤：`hx-trigger="keyup changed delay:300ms"`

**少量 inline JS 场景（~20%）**：
```html
<!-- 复制 API Key 到剪贴板 -->
<button onclick="navigator.clipboard.writeText('{{.Key}}'); this.textContent='已复制';">
  复制
</button>

<!-- 简单开关切换 -->
<script>
  function toggleMenu() {
    document.getElementById('sidebar').classList.toggle('open');
  }
</script>
```

**为什么不是回归 jQuery**：
- jQuery 时代的问题是 DOM 操作繁琐 → HTMX 解决了
- inline JS 只处理极简单的局部交互，逻辑不超过 5 行
- 无全局状态管理，无复杂组件生命周期
- 渐进增强（Progressive Enhancement），页面无 JS 也能基本使用

### 2.5 诉求五：后续 Playground 等复杂交互，架构是否满足扩展？

**回应：完全满足，局部增强不影响整体架构。**

**当前架构的扩展能力**：

| 未来功能 | 实现方式 | 对现有架构的影响 |
|----------|----------|-----------------|
| Playground（代码编辑器）| 在 `/playground` 页面按需加载 Monaco Editor 或 CodeMirror 6 | 仅该页面引入编辑器 JS，其他页面零影响 |
| 实时图表 | Chart.js（CDN 或本地）+ HTMX 轮询 | 局部引入 |
| 实时通知 | HTMX `SSE` 扩展（Server-Sent Events）| 局部引入 |
| 文件上传 | HTMX `hx-encoding="multipart/form-data"` | 原生支持 |
| 复杂表单（Wizard）| 多步骤模板 + HTMX 局部替换 | 无额外依赖 |
| Markdown 渲染 | `github.com/gomarkdown/markdown` 服务端渲染 | 零前端依赖 |

**Playground 具体设计**：
```html
<!-- templates/playground.html -->
{{if .EnablePlayground}}
<script src="/static/js/monaco-editor.min.js"></script>
<script>
  require.config({ paths: { vs: '/static/js/monaco/vs' } });
  require(['vs/editor/editor.main'], function() {
    monaco.editor.create(document.getElementById('editor'), {
      value: "{{.DefaultPrompt}}",
      language: 'markdown',
      theme: 'vs-dark'
    });
  });
</script>
{{end}}
```

**原则**：整体保持轻量，复杂功能作为"插件"在特定页面按需加载，不影响全局架构。

### 2.6 诉求六：AWS / 阿里云东南亚 ECS 部署，对前端的影响

**回应：ECS 部署不影响前端架构，但有一些最佳实践。**

| 部署要素 | 影响 | 前端应对 |
|----------|------|----------|
| ECS 实例 | 无直接影响 | 前端作为静态资源嵌入二进制 |
| SSM Parameter Store | 用于管理 `JWT_SECRET`、`DB_PASSWORD` 等 | 前端不感知，纯后端配置 |
| Docker | 单容器部署 Go 二进制 | `Dockerfile` 只需 `FROM alpine` + 复制二进制 |
| 负载均衡（ALB/SLB）| 需要配置 health check | `/api/health` 已存在 |
| SSL 证书（ACM/阿里云SSL）| ALB/SLB 终止 TLS | 前端无需改动，确保 HTTPS 链接 |
| CDN（CloudFront/阿里云CDN）| 加速静态资源 | 可选：将 `/static/` 路径走 CDN |
| WAF | 防护 XSS/SQL 注入 | 与 CSP/输入净化形成多层防护 |

**推荐部署架构**：
```
User → CloudFront/阿里云CDN (静态加速) → ALB/SLB (SSL终止) → ECS (Go二进制)
                                    ↓
                              PostgreSQL + Redis
```

**注意**：即使使用 CDN，由于所有资源已 `go:embed` 嵌入二进制，CDN 只是锦上添花。最小部署只需一个 ECS 实例即可运行完整服务。

### 2.7 诉求七：支付宝、微信支付、Stripe、PayPal 集成，对前端的要求

**回应：各支付渠道前端要求不同，统一封装为"支付网关选择页"。**

| 支付渠道 | 前端要求 | 实现方式 |
|----------|----------|----------|
| **支付宝** | 跳转到支付宝收银台（PC）或支付宝 H5 页面 | 服务端生成支付 URL，前端 `window.location.href = url` |
| **微信支付** | PC：展示二维码（用户微信扫码）；H5：跳转到微信唤起页 | 服务端生成二维码 URL 或调起参数，前端渲染 QR 码或跳转 |
| **Stripe** | 必须加载 `stripe.js`（PCI 合规要求）| 支付页面按需加载 Stripe Elements，收集卡信息后提交给 Stripe |
| **PayPal** | 必须加载 PayPal SDK | 支付页面按需加载 PayPal Buttons SDK |

**统一支付流程**：
```
1. 用户点击"充值" → 进入 /billing/recharge
2. 选择金额 + 支付方式（支付宝/微信/Stripe/PayPal）
3. 前端提交 → 服务端创建订单，返回支付参数
4. 前端根据支付方式渲染对应界面：
   - 支付宝/微信：跳转或展示二维码
   - Stripe：加载 stripe.js，调起卡片输入框
   - PayPal：加载 PayPal SDK，渲染支付按钮
5. 支付完成后，渠道回调服务端 webhook
6. 前端轮询（HTMX）或 SSE 获取支付结果
```

**关键设计**：
- 支付页面是独立的 `/billing/checkout`，可以安全加载外部 JS
- Stripe/PayPal 的 JS 通过 `<script>` 标签加载（必须，PCI 合规）
- 加载前检查 `Content-Security-Policy: script-src 'self' https://js.stripe.com https://www.paypal.com`
- 支付完成后跳转回 `/billing/result?order_id=xxx`，由服务端渲染结果页

---

## 三、技术架构

### 3.1 技术栈

| 层级 | 技术 | 说明 |
|------|------|------|
| 模板引擎 | Go `html/template` | 原生，类型安全，自动 XSS 转义 |
| 资源嵌入 | `//go:embed` | 模板 + CSS + JS 全部嵌入二进制 |
| 交互 | HTMX 2.x | 14KB，AJAX/表单/轮询/确认对话框 |
| 样式 | Pure CSS | 单文件，~500 行，CSS 变量主题 |
| 图标 | Lucide (SVG 内联) | 仅引用需要的图标，每个 ~300B |
| 图表 | Chart.js (按需) | 仅 Dashboard/Analytics 页面加载 |
| 编辑器 | Monaco (按需) | 仅 Playground 页面加载 |

### 3.2 目录结构

```
ai-portal-go/
├── cmd/server/main.go              # 初始化模板引擎，注册 web 路由
├── internal/
│   ├── routes/                     # 现有 JSON API（不变）
│   └── web/                        # NEW: HTML 页面处理
│       ├── handler.go              # Handler 结构体 + 构造函数
│       ├── pages.go                # 页面路由处理函数
│       ├── data.go                 # 模板数据获取（复用现有查询）
│       ├── helpers.go              # 模板函数（格式化、判断等）
│       └── middleware.go           # Web 专属中间件（CSRF、CSP）
├── web/                            # NEW: 可嵌入的模板和静态资源
│   ├── templates/
│   │   ├── layout.html             # 基础布局
│   │   ├── partials/
│   │   │   ├── nav.html            # 侧边栏导航
│   │   │   ├── mobile-nav.html     # 移动端底部导航
│   │   │   ├── flash.html          # 消息提示
│   │   │   ├── pagination.html     # 分页组件
│   │   │   └── footer.html         # 页脚
│   │   ├── auth/
│   │   │   ├── login.html
│   │   │   └── register.html
│   │   ├── dashboard.html
│   │   ├── apikeys.html
│   │   ├── billing/
│   │   │   ├── balance.html
│   │   │   ├── usage.html
│   │   │   └── recharge.html
│   │   ├── admin/
│   │   │   ├── users.html
│   │   │   ├── analytics.html
│   │   │   ├── keypool.html
│   │   │   └── providers.html
│   │   └── errors/
│   │       ├── 404.html
│   │       └── 500.html
│   └── static/
│       ├── css/
│       │   └── main.css
│       └── js/
│           └── htmx.min.js
│   └── embed.go                    # go:embed 声明
├── dist/                           # 保留：Node.js 前端参考（不修改）
```

### 3.3 核心代码示例

**`web/embed.go`**：
```go
package web

import "embed"

//go:embed templates/*
//go:embed templates/partials/*
//go:embed templates/auth/*
//go:embed templates/billing/*
//go:embed templates/admin/*
//go:embed templates/errors/*
var TemplatesFS embed.FS

//go:embed static/css/*
//go:embed static/js/*
var StaticFS embed.FS
```

**`internal/web/handler.go`**：
```go
package web

import (
    "html/template"
    "net/http"
    
    "github.com/gin-gonic/gin"
)

type Handler struct {
    tmpl   *template.Template
    db     *db.DB
    cfg    *config.Config
    auth   *middleware.Auth
}

func NewHandler(db *db.DB, cfg *config.Config, auth *middleware.Auth) (*Handler, error) {
    tmpl, err := template.New("").Funcs(templateFuncs).ParseFS(web.TemplatesFS, "templates/**/*.html")
    if err != nil {
        return nil, err
    }
    return &Handler{tmpl: tmpl, db: db, cfg: cfg, auth: auth}, nil
}

func (h *Handler) render(c *gin.Context, name string, data map[string]any) {
    buf := new(bytes.Buffer)
    if err := h.tmpl.ExecuteTemplate(buf, name, data); err != nil {
        c.String(http.StatusInternalServerError, "Render error")
        return
    }
    c.Data(http.StatusOK, "text/html; charset=utf-8", buf.Bytes())
}
```

**`cmd/server/main.go` 修改**（添加 web 路由）：
```go
// 在 API 路由注册之后，dist 静态文件服务之前

// Web page handlers
webHandler, err := web.NewHandler(database, cfg, auth)
if err != nil {
    logger.Fatal("Failed to load templates", zap.Error(err))
}

// Static assets (from embed)
staticServer := http.FileServer(http.FS(web.StaticFS))
router.GET("/static/*filepath", func(c *gin.Context) {
    c.Request.URL.Path = c.Param("filepath")
    staticServer.ServeHTTP(c.Writer, c.Request)
})

// HTML page routes
router.GET("/", webHandler.Home)
router.GET("/login", webHandler.LoginPage)
router.GET("/register", webHandler.RegisterPage)
router.GET("/dashboard", auth.RequireAuth(), webHandler.DashboardPage)
router.GET("/apikeys", auth.RequireAuth(), webHandler.APIKeysPage)
router.GET("/billing", auth.RequireAuth(), webHandler.BillingPage)
router.GET("/admin/users", auth.RequireAuth(), auth.RequireAdmin(), webHandler.AdminUsersPage)
// ... etc

// 保留 dist/ 作为 fallback（仅当 go:embed 资源不存在时）
```

---

## 四、安全设计详述

### 4.1 CSRF 防护

```go
// internal/web/middleware.go
func CSRFMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        if c.Request.Method == "GET" {
            // 生成 token 存入 cookie
            token := generateCSRFToken()
            c.SetCookie("csrf_token", token, 3600, "/", "", true, false)
            c.Set("csrf_token", token)
            c.Next()
            return
        }
        
        // POST/PUT/DELETE 验证 token
        if c.Request.Header.Get("X-CSRF-Token") != c.MustGet("csrf_token") {
            c.AbortWithStatusJSON(403, gin.H{"error": "CSRF token invalid"})
            return
        }
        c.Next()
    }
}
```

```html
<!-- 每个表单 -->
<form hx-post="/api/apikeys" hx-target="#result">
  <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
  ...
</form>
```

### 4.2 Content Security Policy

```go
c.Header("Content-Security-Policy",
    "default-src 'self'; "+
    "script-src 'self' 'unsafe-inline'; "+  // inline JS for simple interactions
    "style-src 'self' 'unsafe-inline'; "+
    "img-src 'self' data: https:; "+
    "font-src 'self'; "+
    "connect-src 'self'; "+
    "frame-ancestors 'none'; "+
    "base-uri 'self'")
```

**支付页面例外**：
```go
if strings.HasPrefix(c.Request.URL.Path, "/billing/checkout") {
    // 允许 Stripe / PayPal 脚本
    c.Header("Content-Security-Policy",
        "default-src 'self'; "+
        "script-src 'self' https://js.stripe.com https://www.paypal.com https://www.sandbox.paypal.com; "+
        "frame-src https://js.stripe.com https://hooks.stripe.com https://www.paypal.com; "+
        "connect-src 'self' https://api.stripe.com https://www.paypal.com")
}
```

### 4.3 其他安全头

```go
// 全局中间件
func SecurityHeaders() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
        c.Header("X-Frame-Options", "DENY")
        c.Header("X-Content-Type-Options", "nosniff")
        c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
        c.Header("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
        c.Next()
    }
}
```

---

## 五、支付集成前端设计

### 5.1 充值页面 (`/billing/recharge`)

```html
<!-- 选择金额 + 支付方式 -->
<div class="recharge-page">
  <h1>账户充值</h1>
  
  <div class="amount-grid">
    <button class="amount-btn" onclick="selectAmount(10)">¥10</button>
    <button class="amount-btn" onclick="selectAmount(50)">¥50</button>
    <button class="amount-btn" onclick="selectAmount(100)">¥100</button>
    <input type="number" id="custom-amount" placeholder="自定义金额">
  </div>
  
  <div class="payment-methods">
    <label><input type="radio" name="method" value="alipay"> 支付宝</label>
    <label><input type="radio" name="method" value="wechat"> 微信支付</label>
    <label><input type="radio" name="method" value="stripe"> 信用卡 (Stripe)</label>
    <label><input type="radio" name="method" value="paypal"> PayPal</label>
  </div>
  
  <button hx-post="/billing/create-order"
          hx-include="[name='method'], #custom-amount"
          hx-target="#checkout-container"
          class="btn-primary">
    确认支付
  </button>
  
  <div id="checkout-container">
    <!-- HTMX 加载对应支付界面 -->
  </div>
</div>
```

### 5.2 支付回调处理

```
支付宝/微信：服务端收到异步通知 → 更新余额 → 前端轮询 /billing/order-status?id=xxx
Stripe：Stripe.js 前端回调 → 服务端确认 PaymentIntent → 更新余额
PayPal：PayPal SDK 前端回调 → 服务端确认订单 → 更新余额
```

```html
<!-- 支付结果页模板片段 -->
<div hx-get="/billing/order-status?id={{.OrderID}}" 
     hx-trigger="every 3s"
     hx-target="this"
     hx-swap="outerHTML">
  <p>正在查询支付结果...</p>
</div>
```

---

## 六、实施计划

### Phase 1: 基础设施（3-4 小时）
- [ ] 创建 `web/` 目录结构
- [ ] 创建 `web/embed.go` 文件
- [ ] 创建 `internal/web/` 包（handler, helpers, middleware）
- [ ] 修改 `cmd/server/main.go` 注册 web 路由和静态资源
- [ ] 创建 `templates/layout.html`（含响应式布局、导航、主题变量）
- [ ] 创建 `static/css/main.css`（科技风、渐变、响应式）
- [ ] 验证模板渲染正确、静态资源可访问

### Phase 2: 认证页面（2-3 小时）
- [ ] Login 页面 + 表单
- [ ] Register 页面 + 表单
- [ ] Logout 功能
- [ ] Flash 消息系统（成功/错误提示）

### Phase 3: 核心用户页面（4-5 小时）
- [ ] Dashboard（余额、用量统计、快捷入口）
- [ ] API Keys 列表/创建/删除
- [ ] Usage 记录查询
- [ ] Profile 设置

### Phase 4: 账单与支付（4-5 小时）
- [ ] 余额页面
- [ ] 充值页面（金额选择 + 支付方式）
- [ ] 订单记录
- [ ] 发票申请

### Phase 5: 管理后台（5-6 小时）
- [ ] 用户管理
- [ ] 用量分析 / 图表
- [ ] Key Pool 管理
- [ ] Provider 配置

### Phase 6: 安全加固与优化（3-4 小时）
- [ ] CSRF Token 系统
- [ ] CSP / Security Headers
- [ ] 速率限制（web 路由）
- [ ] 移动端适配细节优化
- [ ] 清理 `dist/` 相关代码（最终阶段，待用户确认后执行）

**总计：约 20-25 小时**

---

## 七、与现有代码的关系

| 现有代码 | 变动 |
|----------|------|
| `cmd/server/main.go` | 添加 web 路由注册、模板初始化、安全中间件 |
| `internal/routes/*.go` | **不变**，JSON API 保持独立 |
| `internal/middleware/auth.go` | **不变**，复用于 web 页面鉴权 |
| `dist/` | **保留**，作为参考，最终 Phase 6 清理 |
| 数据库 / 配置 | **不变** |

---

## 八、交付物清单

实施完成后，以下文件将新增或修改：

**新增文件：**
- `web/embed.go`
- `web/templates/layout.html`
- `web/templates/partials/*.html`
- `web/templates/auth/*.html`
- `web/templates/dashboard.html`
- `web/templates/apikeys.html`
- `web/templates/billing/*.html`
- `web/templates/admin/*.html`
- `web/templates/errors/*.html`
- `web/static/css/main.css`
- `web/static/js/htmx.min.js`
- `internal/web/handler.go`
- `internal/web/pages.go`
- `internal/web/data.go`
- `internal/web/helpers.go`
- `internal/web/middleware.go`

**修改文件：**
- `cmd/server/main.go`

---

> **确认**：以上方案是否满足全部诉求？确认后我将从 **Phase 1** 开始实施。
