# AI Portal Go — Web Frontend Architecture V2 (Go-Native)

> **ACTIVE** — Created: 2026-05-04
> 整合来源: 5-Agent 意见稿交叉评审后修订

---

## 一、五份意见稿交叉评审摘要

### 1.1 意见稿来源

| 文档 | 核心主张 | 关键差异 |
|------|----------|----------|
| `web-frontend-architecture-202605040442.md` (本Agent V1) | Go `html/template` + `embed` + HTMX + Pure CSS | 零 Node.js，零 CDN |
| `frontend-architecture-202605040443.md` (Agent A) | Go Template + HTMX + Tailwind CDN + 可选 Alpine.js | 使用 Tailwind CDN，保留 Alpine.js 选项 |
| `frontend-architecture-202605041600.md` (Agent B) | Preact (3KB) 混合方案 | 大部分静态 + 少量 Preact 组件，分级增强 |
| `frontend-rebuild-proposal-202605040000.md` (Agent C) | **React SPA + Bun Build** | 认为纯静态无法支撑 Playground，主张完整 SPA |
| `frontend-plan-20260503.md` (Agent D) | React 18 + TypeScript + Vite | 传统 React SPA 方案，shadcn/ui |

### 1.2 共识（5/5 Agent 一致）

- ✅ 使用 `go:embed` 嵌入静态资源，单二进制部署
- ✅ 响应式设计，移动端优先（Sidebar → Hamburger / Bottom Nav）
- ✅ 强化安全：CSP + CSRF + HTTPS + HSTS + Secure Cookie
- ✅ 支付宝/微信支付：服务端生成链接/二维码，前端零 SDK
- ✅ Stripe/PayPal：仅支付页面按需加载 SDK
- ✅ 境外部署（AWS/阿里云东南亚），静态资源体积越小越好

### 1.3 分歧焦点

| 议题 | 方案A (本Agent) | 方案B (Preact) | 方案C/D (React SPA) |
|------|-----------------|----------------|---------------------|
| 前端框架 | 无（HTMX only） | Preact（3KB，局部） | React（30-40KB） |
| Playground 支持 | 特定页面按需加载 Monaco | 特定页面加载 Preact | 自然支持（已有生态） |
| 构建工具 | 无 | Bun build | Bun/Vite |
| 首屏体积 | ~37KB | ~40KB | ~45KB+ |
| Node.js 依赖 | **零** | **零**（Bun 单文件） | 需要 Bun/Node |

### 1.4 评审结论

**坚持 V1 方向**（Go `html/template` + `embed` + HTMX + Pure CSS），但 **吸收其他方案的关键补充**：
- 从 Preact 方案吸收「渐进增强分级策略」和「部署架构图」
- 从 React SPA 方案吸收「Playground 需求清单」「SRI 安全」「敏感操作二次确认」
- 从 Tailwind 方案吸收「CSP 细化设计」

---

## 二、V2 修订要点

### 2.1 新增：渐进增强三级策略

借鉴 Agent B 的分级思想，但去除 Alpine.js，改为纯 HTMX + 原生 JS 分级：

```
┌─────────────────────────────────────────────────────────────┐
│ Level 0: 纯 HTML + HTMX (90% 页面)                          │
│   - 页面导航: hx-boost="true"                               │
│   - 表单提交: hx-post / hx-target                           │
│   - 数据加载: hx-get / hx-trigger                           │
│   - 删除确认: hx-delete / hx-confirm                        │
├─────────────────────────────────────────────────────────────┤
│ Level 1: HTMX + 少量 inline JS (8% 页面)                    │
│   - 复制到剪贴板: navigator.clipboard                       │
│   - 主题切换: document.documentElement.setAttribute         │
│   - 侧边栏开关: classList.toggle                            │
│   - 表单验证: 原生 HTML5 validation + JS 增强               │
├─────────────────────────────────────────────────────────────┤
│ Level 2: 独立 JS 组件 (2% 页面)                             │
│   - Playground: Monaco Editor + SSE 处理                    │
│   - Dashboard Charts: Chart.js (仅 admin 页面加载)          │
│   - 支付页面: Stripe.js / PayPal SDK (按需加载)             │
│   → 这些页面单独引入 JS，不影响全局架构                     │
└─────────────────────────────────────────────────────────────┘
```

**关键原则**：Level 2 页面是「孤岛」——它们引入独立 JS，但路由、布局、鉴权仍由 Go 后端控制。Playground 页面可以是一个 `<iframe>` 或独立路由，由 Go 渲染容器模板，内部加载 Monaco + 自定义 JS。

### 2.2 修订：Playground 扩展方案

Agent C 详细列举了 Playground 的技术需求：
- SSE streaming 读取（EventSource）
- Markdown 渲染（代码高亮 + 数学公式）
- 消息状态管理（loading / streaming / done / error）
- 自动滚动 + 虚拟滚动（长对话）
- 模型/参数选择器

**V2 应对方案**：

```html
<!-- templates/playground.html —— Go 渲染外壳，内部加载独立 JS -->
{{template "layout" .}}
{{define "title"}}Playground{{end}}
{{define "content"}}
  <div id="playground-container"
       data-api-base="/api"
       data-jwt-token="{{.JWTToken}}"
       data-models='{{.ModelsJSON}}'>
    <!-- 以下由独立 JS 接管 -->
    <div id="chat-messages"></div>
    <div id="chat-input"></div>
  </div>

  <!-- Level 2: 仅此页面加载 -->
  <script src="/static/js/monaco-editor.min.js"></script>
  <script src="/static/js/marked.min.js"></script>
  <script src="/static/js/playground.js"></script>
  <script>
    // playground.js 读取 data-* 属性初始化
    initPlayground({
      container: document.getElementById('playground-container'),
      apiBase: document.getElementById('playground-container').dataset.apiBase,
      token: document.getElementById('playground-container').dataset.jwtToken,
      models: JSON.parse(document.getElementById('playground-container').dataset.models)
    });
  </script>
{{end}}
```

**体积控制**：
- Monaco Editor: ~1MB（按需加载，首次访问 Playground 才下载）
- marked.js: ~20KB
- playground.js（自定义）: ~10KB
- **首屏不受影响**，Playground 是独立页面

### 2.3 新增：SRI（Subresource Integrity）

借鉴 Agent C 的安全建议：

```go
// internal/web/helpers.go
var sriHashes = map[string]string{
    "htmx.min.js":    "sha384-...",
    "main.css":       "sha384-...",
}

func (h *Handler) sriHash(file string) string {
    return sriHashes[file]
}
```

```html
<!-- layout.html -->
<script src="/static/js/htmx.min.js"
        integrity="sha384-{{sriHash "htmx.min.js"}}"
        crossorigin="anonymous"></script>
```

> 注：由于所有资源通过 `go:embed` 内嵌，SRI 更多是防御中间人攻击的纵深安全措施。

### 2.4 修订：敏感操作二次确认机制

Agent C 提出「删除 Key / 修改角色 / 充值 > ¥1000 弹出验证码」，V2 采纳：

```html
<!-- 充值页面 —— 金额 > 1000 时要求密码确认 -->
<form hx-post="/billing/recharge" hx-target="#result">
  <input type="hidden" name="amount" value="{{.Amount}}">
  <input type="hidden" name="method" value="{{.Method}}">
  
  {{if gt .Amount 1000}}
  <div class="confirm-box">
    <label>请输登录入密码确认充值 ¥{{.Amount}}：</label>
    <input type="password" name="confirm_password" required
           minlength="8" autocomplete="current-password">
  </div>
  {{end}}
  
  <button type="submit" class="btn-primary">确认支付</button>
</form>
```

后端校验：
```go
if amount > 1000 {
    // 验证 confirm_password 是否与用户当前密码匹配
    if err := bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(req.ConfirmPassword)); err != nil {
        c.JSON(403, gin.H{"error": gin.H{"message": "密码错误，充值取消"}})
        return
    }
}
```

### 2.5 修订：CSP 细化设计

借鉴 Agent A 的 CSP 设计，V2 细化如下：

**通用页面 CSP**：
```
default-src 'self';
script-src 'self';
style-src 'self' 'unsafe-inline';
img-src 'self' data: https:;
font-src 'self';
connect-src 'self';
frame-ancestors 'none';
base-uri 'self';
form-action 'self'
```

**支付页面 CSP（放宽 Stripe/PayPal）**：
```
default-src 'self';
script-src 'self' https://js.stripe.com https://www.paypal.com https://www.sandbox.paypal.com;
style-src 'self' 'unsafe-inline';
frame-src https://js.stripe.com https://hooks.stripe.com https://www.paypal.com;
connect-src 'self' https://api.stripe.com https://www.paypal.com;
img-src 'self' data: https:;
```

**Playground 页面 CSP（放宽 Monaco CDN）**：
```
default-src 'self';
script-src 'self' 'unsafe-eval';  // Monaco 需要 eval
style-src 'self' 'unsafe-inline';
connect-src 'self';
```

### 2.6 新增：移动端设计线框

借鉴 Agent C 的线框设计，V2 明确移动端布局：

```
┌─ Mobile (< 768px) ──────────────────────┐
│ ☰ WOODGIT              🌙 👤          │
├─────────────────────────────────────────┤
│                                         │
│  ┌───────────────────────────────────┐  │
│  │  💰 余额: ¥123.45                 │  │
│  │  🔑 活跃: 3 把密钥                │  │
│  │  📊 今日: 1,234 tokens            │  │
│  └───────────────────────────────────┘  │
│                                         │
│  ┌───────────────────────────────────┐  │
│  │  📈 7 天用量趋势                   │  │
│  │  [迷你图表]                       │  │
│  └───────────────────────────────────┘  │
│                                         │
│  ┌───────────────────────────────────┐  │
│  │  🔑 我的 API 密钥                  │  │
│  │  sk-a1b2...  [复制]               │  │
│  │  sk-c3d4...  [复制]               │  │
│  └───────────────────────────────────┘  │
│                                         │
│  ┌──┬──┬──┬──┐                         │
│  │🏠│🔑│💰│⚙️│  ← 底部 Tab 导航        │
│  └──┴──┴──┴──┘                         │
└─────────────────────────────────────────┘

┌─ Desktop (> 1024px) ──────────────────────────────────────┐
│  🏠 WOODGIT                    🌙 💰 ¥123.45 👤          │
├──────────┬────────────────────────────────────────────────┤
│ 📊 仪表盘 │  欢迎回来, Admin                               │
│ 🔑 密钥   │                                                │
│ 💵 计费   │  ┌──────┐ ┌──────┐ ┌─────┐                    │
│ 📦 订单   │  │ 用户 │ │ 密钥 │ │ 请求│                    │
│ 🧾 发票   │  │ 128  │ │ 456  │ │ 8.2K│                    │
│ ⚙️ 管理   │  └──────┘ └──────┘ └─────┘                    │
│  用户     │                                                │
│  密钥池   │  📈 用量趋势 (7天)                              │
│  模型     │  ┌──────────────────────────────────────────┐  │
│  供应商   │  │████████▌                                 │  │
│  审计     │  │██████                                    │  │
│  设置     │  └──────────────────────────────────────────┘  │
│          │                                                │
└──────────┴────────────────────────────────────────────────┘
```

### 2.7 修订：支付集成设计

综合 Agent A/C/D 的支付方案，V2 明确以下设计：

**支付页面统一流程**：
```
1. 用户访问 /billing/recharge
   → Go 渲染充值页（金额选择 + 支付方式）

2. 用户选择金额 + 支付方式 → 点击确认
   → HTMX POST /billing/create-order
   → 服务端创建订单，返回 {orderId, paymentType, ...}

3. 前端根据 paymentType 渲染对应界面：
   
   Alipay:    <img src="data:image/png;base64,{{.QRBase64}}"> + HTMX 轮询状态
   WeChat:    同上
   Stripe:    加载 Stripe.js → 渲染卡片输入 → Stripe.confirmCardPayment()
   PayPal:    加载 PayPal SDK → 渲染按钮 → 回调确认

4. 支付完成 → 渠道回调服务端 Webhook → 更新余额
   → 前端轮询（HTMX every 3s）检测到 paid → 跳转成功页
```

**关键安全点**：
- 金额由服务端计算，前端仅展示
- 订单号 UUID 幂等，防止重复支付
- 支付按钮点击后 `disabled` + loading 状态
- 大额（>¥1000）要求密码二次确认

### 2.8 新增：部署架构图

借鉴 Agent B/C 的部署设计：

```
                        ┌─────────────────┐
   User ──────►  CloudFront / CDN  ──────► ALB/SLB (SSL终止)
                                              │
                                              ▼
                                         ECS (Docker)
                                         │ Go 二进制
                                         │ go:embed 模板+静态资源
                                         │
                                         ▼
                                   RDS PostgreSQL
                                   ElastiCache Redis
```

**静态资源缓存策略**：
```nginx
# Nginx / ALB 配置
location /static/ {
    expires 30d;
    add_header Cache-Control "public, immutable";
}

location / {
    proxy_pass http://127.0.0.1:3001;
    proxy_set_header X-Real-IP $remote_addr;
}
```

由于 `go:embed` 内嵌了所有静态资源，**不需要 CDN 也能运行**。CDN 只是锦上添花。

### 2.9 修订：主题系统

借鉴 Agent D 的 CSS 变量设计，V2 采用更完整的主题系统：

```css
/* web/static/css/main.css */
:root {
  /* Light Theme (Default) */
  --bg: #f8fafc;
  --bg-card: #ffffff;
  --bg-sidebar: #0f172a;
  --text: #1e293b;
  --text-secondary: #64748b;
  --text-inverse: #f8fafc;
  --border: #e2e8f0;
  --primary: #3b82f6;
  --primary-hover: #2563eb;
  --success: #16a34a;
  --warning: #f59e0b;
  --danger: #ef4444;
  --shadow: 0 1px 3px rgba(0,0,0,0.1);
  --radius: 8px;
  --radius-lg: 12px;
  --font-sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
}

[data-theme="dark"] {
  --bg: #0f172a;
  --bg-card: #1e293b;
  --bg-sidebar: #020617;
  --text: #f8fafc;
  --text-secondary: #94a3b8;
  --text-inverse: #1e293b;
  --border: #334155;
  --primary: #60a5fa;
  --primary-hover: #3b82f6;
}
```

主题切换由少量 inline JS 处理（Level 1），无框架依赖：
```html
<script>
function toggleTheme() {
    const html = document.documentElement;
    const current = html.getAttribute('data-theme');
    const next = current === 'dark' ? 'light' : 'dark';
    html.setAttribute('data-theme', next);
    localStorage.setItem('theme', next);
}
// 初始化
const saved = localStorage.getItem('theme');
if (saved) document.documentElement.setAttribute('data-theme', saved);
</script>
```

---

## 三、V2 最终技术栈

| 层级 | 技术 | 体积 | 说明 |
|------|------|------|------|
| 模板引擎 | Go `html/template` | 0 | 标准库，自动 XSS 转义 |
| 资源嵌入 | `//go:embed` | 0 | 单二进制部署 |
| 交互 | HTMX 2.x | ~14KB | 所有 AJAX/表单/轮询/确认 |
| 样式 | Pure CSS | ~8KB | 单文件，CSS 变量主题 |
| 图标 | Lucide SVG 内联 | ~0.3KB/个 | 仅用需要的图标 |
| 图表 | Chart.js (按需) | ~40KB | 仅 Dashboard/Analytics 加载 |
| 编辑器 | Monaco (按需) | ~1MB | 仅 Playground 加载 |
| 支付 | Stripe.js / PayPal SDK (按需) | ~40-60KB | 仅支付页面加载 |
| 安全 | CSP + CSRF + HSTS + SRI | 0 | 全服务端控制 |

**首屏加载（不含按需组件）**：~37KB HTML+CSS+HTMX，零外部请求。

---

## 四、安全设计 V2

### 4.1 安全头中间件（完整版）

```go
// internal/web/middleware.go
func SecurityHeaders() gin.HandlerFunc {
    return func(c *gin.Context) {
        // HSTS
        c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
        
        // Frame 保护
        c.Header("X-Frame-Options", "DENY")
        
        // MIME 嗅探保护
        c.Header("X-Content-Type-Options", "nosniff")
        
        // Referrer 策略
        c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
        
        // 权限策略
        c.Header("Permissions-Policy", 
            "accelerometer=(), camera=(), geolocation=(), gyroscope=(), "+
            "magnetometer=(), microphone=(), payment=(), usb=()")
        
        // CSP（根据路径动态调整）
        csp := buildCSP(c.Request.URL.Path)
        c.Header("Content-Security-Policy", csp)
        
        c.Next()
    }
}

func buildCSP(path string) string {
    base := "default-src 'self'; style-src 'self' 'unsafe-inline'; " +
            "img-src 'self' data: https:; font-src 'self'; " +
            "connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
    
    if strings.HasPrefix(path, "/billing/checkout") {
        return base + "; script-src 'self' https://js.stripe.com https://www.paypal.com; " +
               "frame-src https://js.stripe.com https://hooks.stripe.com https://www.paypal.com; " +
               "connect-src 'self' https://api.stripe.com https://www.paypal.com"
    }
    if strings.HasPrefix(path, "/playground") {
        return base + "; script-src 'self' 'unsafe-eval'"
    }
    return base + "; script-src 'self'"
}
```

### 4.2 CSRF Token 机制

```go
// 每个 GET 请求生成 CSRF token
csrfToken := generateCSRFToken()
c.SetCookie("csrf_token", csrfToken, 3600, "/", "", true, false)
c.Set("csrf_token", csrfToken)
```

```html
<!-- 每个表单 -->
<form hx-post="/api/apikeys" hx-target="#result" hx-headers='{"X-CSRF-Token": "{{.CSRFToken}}"}'>
  <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
  ...
</form>
```

### 4.3 速率限制（Web 路由）

```go
// 对 web 登录/注册/支付页面同样限流
router.POST("/login", rateLimit(5, time.Minute), webHandler.Login)
router.POST("/register", rateLimit(3, time.Minute), webHandler.Register)
router.POST("/billing/recharge", rateLimit(3, time.Minute), auth.RequireAuth(), webHandler.Recharge)
```

---

## 五、实施计划（6 Phase）

### Phase 1: 基础设施（3-4h）
- [ ] 创建 `web/` 目录结构 + `embed.go`
- [ ] 创建 `internal/web/` 包（handler, helpers, middleware, data）
- [ ] 修改 `cmd/server/main.go`（模板引擎初始化、web 路由注册、安全中间件）
- [ ] 创建 `templates/layout.html`（响应式布局、侧边栏、主题系统）
- [ ] 创建 `static/css/main.css`（科技风、渐变、响应式、CSS 变量主题）
- [ ] 集成 HTMX + 验证模板渲染

### Phase 2: 认证页面（2-3h）
- [ ] Login 页面 + 表单（HTMX POST）
- [ ] Register 页面 + 表单
- [ ] Flash 消息系统
- [ ] CSRF Token 全流程验证

### Phase 3: 核心用户页面（4-5h）
- [ ] Dashboard（余额卡片、用量概览、快捷入口）
- [ ] API Keys 列表/创建/删除（HTMX 局部刷新表格）
- [ ] Usage 记录查询（分页 + 日期过滤）
- [ ] Profile / Settings

### Phase 4: 账单与支付（4-5h）
- [ ] 余额页面
- [ ] 充值页面（金额选择 + 支付方式选择）
- [ ] 支付宝/微信支付流程（二维码 + 轮询）
- [ ] Stripe/PayPal 支付流程（按需加载 SDK）
- [ ] 订单记录 + 发票申请
- [ ] 大额支付密码二次确认

### Phase 5: 管理后台（5-6h）
- [ ] 用户管理（列表、编辑角色/余额）
- [ ] 用量分析 / Chart.js 图表
- [ ] Key Pool 管理
- [ ] Provider 配置
- [ ] 审计日志

### Phase 6: 安全加固与清理（3-4h）
- [ ] CSP 全路径测试
- [ ] CSRF 渗透测试
- [ ] 移动端全面测试
- [ ] SRI hash 计算与验证
- [ ] 速率限制验证
- [ ] **清理 `dist/` Node.js 相关代码**（用户确认后执行）

**总计：约 22-28 小时**

---

## 六、四-Agent 协作计划

基于 V2 方案，建议按以下分工协作：

### Agent 1: Code（编码 Agent）

**职责**：所有前端代码实现

| 任务 | 产出 |
|------|------|
| Phase 1-2 | `web/` 目录、`internal/web/` 包、Layout、Login、Register |
| Phase 3 | Dashboard、API Keys、Usage、Profile |
| Phase 4 | Billing、Recharge、Payment（4种支付方式）|
| Phase 5 | Admin 后台所有页面 |
| Phase 6 | 安全中间件、CSP、CSRF、清理 dist/ |

**技能要求**：Go、html/template、HTMX、CSS

### Agent 2: Unit Test + Integration Test（测试 Agent）

**职责**：模板渲染测试 + Handler 测试 + 集成测试

| 任务 | 产出 |
|------|------|
| 模板测试 | `internal/web/handler_test.go` — 验证每个模板能正确渲染 |
| Handler 测试 | `internal/web/pages_test.go` — 模拟 HTTP 请求，验证响应 |
| 数据层测试 | `internal/web/data_test.go` — 验证数据查询正确性 |
| 集成测试 | `tests/web_integration_test.go` — 端到端：请求 → 数据处理 → 模板渲染 → HTML 输出 |
| 安全测试 | CSRF token 生成/验证测试、CSP 头测试 |

**技能要求**：Go testing、`httptest`、模板断言

### Agent 3: Service Test + Stress Test（服务测试 Agent）

**职责**：端到端测试 + 性能测试 + 安全渗透

| 任务 | 产出 |
|------|------|
| E2E 测试 | Playwright 脚本：登录 → Dashboard → API Keys → Billing → 支付 → 登出 |
| 响应式测试 | 手机/平板/桌面 viewport 测试 |
| 性能测试 | `hey` / `k6`：首屏 TTFB、并发用户、内存占用 |
| 安全渗透 | XSS 注入测试、CSRF 绕过测试、Session 劫持测试 |
| 支付沙箱 | Stripe 测试模式 + PayPal Sandbox 完整流程验证 |

**技能要求**：Playwright、k6、安全测试

### Agent 4: Review（审查 Agent）

**职责**：代码审查 + 架构审查 + 安全审查 + 文档审查

| 任务 | 产出 |
|------|------|
| 代码审查 | PR Review：Go idioms、错误处理、并发安全、SQL 注入风险 |
| 架构审查 | 模板继承结构是否合理、路由设计是否 RESTful、是否过度工程 |
| 安全审查 | CSP 配置是否完善、CSRF 是否全覆盖、敏感数据是否泄露到模板 |
| 性能审查 | CSS 是否高效、模板是否缓存、数据库查询是否 N+1 |
| 文档审查 | 代码注释、API 文档、部署文档是否同步更新 |

**技能要求**：Go 高级、安全审计、性能优化

### 协作流程

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  Code (A1)  │────▶│  Test (A2)  │────▶│ Stress (A3) │────▶│ Review (A4) │
└─────────────┘     └─────────────┘     └─────────────┘     └─────────────┘
       │                   │                   │                   │
       │                   │                   │                   │
       ▼                   ▼                   ▼                   ▼
   实现代码            单元/集成测试        E2E/性能/安全        最终审查
   提交 PR             发现 bug → 反馈     发现瓶颈 → 反馈      批准/要求修改
```

**每 Phase 完成后**：
1. A1 提交代码 → A2 跑测试 → 发现 Bug → A1 修复
2. A2 测试通过 → A3 跑 E2E + 压测 → 发现问题 → A1/A2 修复
3. A3 通过 → A4 审查 → 批准或要求修改
4. A4 批准 → 合并 → 进入下一 Phase

---

## 七、与现有代码的关系

| 现有代码 | 变动 |
|----------|------|
| `cmd/server/main.go` | 新增模板引擎初始化、web 路由注册、安全中间件 |
| `internal/routes/*.go` | **不变**，JSON API 保持独立 |
| `internal/middleware/auth.go` | **不变**，复用于 web 页面鉴权 |
| `dist/` | **Phase 6 清理**，当前保留作为参考 |
| `web/` | **新增**，模板 + 静态资源 + embed |
| `internal/web/` | **新增**，HTML 页面处理逻辑 |
| 数据库 / 配置 | **不变** |

---

## 八、交付物清单

实施完成后，以下文件将新增或修改：

**新增文件：**
- `web/embed.go`
- `web/templates/layout.html`
- `web/templates/partials/*.html` (nav, mobile-nav, flash, pagination, footer)
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
- `internal/web/*_test.go` (A2 产出)
- `tests/web_e2e_test.go` (A3 产出)
- `tests/web_load_test.js` (A3 产出)

**修改文件：**
- `cmd/server/main.go`

---

> **确认**：以上 V2 方案整合了 5 份意见稿的全部精华，坚持 Go 原生模板方向，吸收 Preact/React 方案中的安全、部署、Playground 扩展等补充设计。
>
> **请确认后，我将启动 Phase 1 实施，并协调 4-Agent 按计划推进。**
