# AI Portal Go - 前端架构方案 V2

> **ACTIVE** - Created: 2026-05-04 (综合 5 位 Agent 意见)

---

## 一、各方案对比分析

| 维度 | 方案 A: Go+HTMX | 方案 B: Preact | 方案 C: React+Bun | 方案 D: React+Vite |
|------|-----------------|----------------|-------------------|---------------------|
| 提出者 | Claude, Agent4 | Agent2 | Agent3 | Agent5 |
| 依赖 | 零 npm | Preact 3KB | React 40KB | React 40KB |
| 构建工具 | 无 | 无 | Bun | Vite |
| 首屏体积 | ~37KB | ~150KB | ~120KB | ~200KB |
| 交互能力 | HTMX (80%) | Preact SPA | React SPA | React SPA |
| Playground | 按需加载 Monaco | 自然支持 | 自然支持 | 自然支持 |
| 开发效率 | 中等 | 高 | 高 | 高 |
| 移动适配 | 手写 CSS | Tailwind | Tailwind | Tailwind |

### 共识点 (所有 Agent 一致同意)

| 决策 | 理由 |
|------|------|
| 移动优先响应式 | 管理员常在手机操作 |
| go:embed 部署 | 单二进制，零外部依赖 |
| CSRF + CSP + XSS | 充值场景安全要求 |
| 支付 SDK 按需加载 | Alipay/WeChat 仅需 QR 码，Stripe/PayPal 懒加载 |
| 服务端渲染首屏 | 境外服务器，减少白屏时间 |

### 分歧点

| 分歧 | 观点 |
|------|------|
| Go template vs React SPA | Go 原生 vs 交互能力 |
| HTMX vs Preact vs React | 轻量 vs 功能 |
| Pure CSS vs Tailwind | 零构建 vs 开发效率 |

---

## 二、最终决策：Go + HTMX + 渐进式 JS

### 2.1 决策理由

| 考量 | 决策 |
|------|------|
| 用户诉求 | "偏简约科技风"、"加载响应要快"、"少依赖" |
| 部署环境 | 境外 ECS，带宽按量计费 |
| 项目性质 | 管理后台，非 C 产品 |
| 扩展性 | Playground 等复杂功能按需引入 JS |

**结论：** Go template + HTMX 为核心，复杂页面按需引入轻量 JS 组件。

### 2.2 技术栈

```
┌─────────────────────────────────────────────────────────┐
│                    浏览器                                │
├─────────────────────────────────────────────────────────┤
│  Go html/template (服务端渲染)                           │
│  HTMX 2.x (AJAX 交互, 14KB)                            │
│  Alpine.js (轻量状态, 15KB, 按需)                        │
│  Tailwind CSS v4 (CDN JIT, 按需)                        │
│  Monaco Editor (仅 Playground, 按需)                     │
│  Stripe.js / PayPal SDK (仅支付页, 按需)                 │
└─────────────────────────────────────────────────────────┘
           │
           ▼
┌─────────────────────────────────────────────────────────┐
│           Go Server (Gin) + go:embed                    │
│  ├── html/template 渲染                                 │
│  ├── HTMX 局部刷新 handler                              │
│  ├── REST API (JSON)                                    │
│  └── SSE (Playground streaming)                         │
└─────────────────────────────────────────────────────────┘
```

### 2.3 体积预算

| 资源 | 大小 (gzip) | 加载方式 |
|------|-------------|----------|
| HTML (首屏) | ~15KB | 服务端渲染 |
| CSS (自定义) | ~8KB | go:embed |
| HTMX | ~14KB | go:embed |
| **首屏总计** | **~37KB** | **内嵌二进制** |
| Tailwind CDN | ~3KB | CDN (可选) |
| Alpine.js | ~15KB | 按需加载 |
| Monaco Editor | ~2MB | 仅 Playground |
| Stripe.js | ~40KB | 仅支付页 |

---

## 三、响应式设计方案

### 3.1 断点

```css
/* 移动优先 */
:root { --sidebar-width: 0; }

@media (min-width: 768px) {
  :root { --sidebar-width: 16rem; }
}

@media (min-width: 1280px) {
  :root { --sidebar-width: 18rem; }
}
```

### 3.2 布局策略

**手机 (<768px):**
- 底部 Tab 导航 (5 个图标)
- 单列卡片布局
- 表格转卡片列表
- 弹窗全屏

**平板 (768-1024px):**
- 可折叠侧边栏 (hamburger)
- 双列网格
- 表格保留

**桌面 (>1024px):**
- 固定侧边栏
- 多列布局
- 完整表格

### 3.3 设计风格

```
背景色:
  Light: #f8fafc (slate-50) → #f1f5f9 (slate-100) 微渐变
  Dark:  #0f172a (slate-900) → #1e293b (slate-800) 微渐变

强调色:
  Primary: #3b82f6 (blue-500) → #2563eb (blue-600) 渐变
  Success: #16a34a (green-500)
  Warning: #f59e0b (amber-500)
  Error:   #ef4444 (red-500)

卡片:
  背景: white / slate-800
  圆角: 12px
  阴影: shadow-sm (默认) → shadow-md (hover)
  边框: 1px solid slate-200 / slate-700

按钮:
  Primary: 渐变背景 + hover 变亮
  Secondary: 边框 + 背景透明
  圆角: 8px

字体:
  系统字体栈: -apple-system, system-ui, "Segoe UI", sans-serif
  代码: "SF Mono", Monaco, "Cascadia Code", monospace
```

---

## 四、安全强化方案

### 4.1 前端安全层

| 层级 | 措施 | 实现 |
|------|------|------|
| 传输 | HTTPS 强制 | HSTS header |
| 内容 | CSP | `default-src 'self'; script-src 'self' 'unsafe-inline'` |
| 内容 | XSS 防护 | Go template 自动转义 `{{.}}` |
| 内容 | CSRF | 表单隐藏字段 + Cookie 双重验证 |
| 内容 | 点击劫持 | `X-Frame-Options: DENY` |
| 会话 | Cookie 安全 | `HttpOnly; Secure; SameSite=Strict` |
| 输入 | 净化 | `SanitizeInput()` 中间件 (已实现) |

### 4.2 支付安全

| 措施 | 说明 |
|------|------|
| PCI DSS | Stripe Elements，卡号不经过我们服务器 |
| 金额校验 | 服务端计算，不信任前端 |
| 订单幂等 | 唯一订单号，防重复支付 |
| 二次确认 | 大额充值需输入密码 |

### 4.3 Go 中间件

```go
// 安全头中间件
func SecurityHeaders() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
        c.Header("X-Frame-Options", "DENY")
        c.Header("X-Content-Type-Options", "nosniff")
        c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
        c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

        // CSP (支付页特殊处理)
        if strings.HasPrefix(c.Request.URL.Path, "/billing/checkout") {
            c.Header("Content-Security-Policy",
                "default-src 'self'; "+
                "script-src 'self' https://js.stripe.com https://www.paypal.com; "+
                "frame-src https://js.stripe.com https://www.paypal.com")
        } else {
            c.Header("Content-Security-Policy",
                "default-src 'self'; "+
                "script-src 'self' 'unsafe-inline'; "+
                "style-src 'self' 'unsafe-inline'")
        }
        c.Next()
    }
}

// CSRF 中间件
func CSRFMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        if c.Request.Method == "GET" {
            token := generateCSRFToken()
            c.SetCookie("csrf_token", token, 3600, "/", "", true, false)
            c.Set("csrf_token", token)
            c.Next()
            return
        }
        // POST/PUT/DELETE 验证
        cookieToken, _ := c.Cookie("csrf_token")
        formToken := c.PostForm("csrf_token")
        headerToken := c.GetHeader("X-CSRF-Token")
        if cookieToken != formToken && cookieToken != headerToken {
            c.AbortWithStatusJSON(403, gin.H{"error": "CSRF token invalid"})
            return
        }
        c.Next()
    }
}
```

---

## 五、扩展性设计 (Playground 等)

### 5.1 渐进式 JS 策略

```
Level 0: 纯 HTML + HTMX (90% 页面)
  → 登录、注册、Dashboard、API Keys、设置、管理后台

Level 1: HTMX + Alpine.js (少量交互)
  → Dropdown、Modal、Tab、Toast 通知

Level 2: 独立 JS 组件 (复杂页面)
  → Monaco Editor (Playground)
  → Chart.js (图表)
  → Stripe.js (支付)
```

### 5.2 Playground 实现

```html
<!-- templates/playground.html -->
{{define "playground"}}
{{template "layout" .}}

<!-- 仅在 Playground 页面加载编辑器 -->
<link rel="stylesheet" href="/static/css/monaco.css">
<script src="/static/js/monaco-editor.min.js"></script>

<div class="playground-container">
  <div id="editor" style="height: 400px;"></div>
  <div id="output" hx-get="/api/playground/stream" hx-trigger="every 1s">
    <!-- SSE streaming output -->
  </div>
</div>

<script>
  require.config({ paths: { vs: '/static/js/monaco/vs' } });
  require(['vs/editor/editor.main'], function() {
    const editor = monaco.editor.create(document.getElementById('editor'), {
      value: '',
      language: 'markdown',
      theme: 'vs-dark'
    });

    // 发送到后端
    document.getElementById('send-btn').addEventListener('click', () => {
      fetch('/api/playground', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ prompt: editor.getValue() })
      });
    });
  });
</script>
{{end}}
```

### 5.3 架构扩展能力

| 未来功能 | 实现方式 | 影响范围 |
|----------|----------|----------|
| Playground | Monaco + SSE | 仅 /playground 页面 |
| 实时图表 | Chart.js + HTMX 轮询 | 仅 Dashboard |
| 实时通知 | HTMX SSE 扩展 | 全局，轻量 |
| 文件上传 | HTMX multipart | 原生支持 |
| 复杂表单 | 多步骤模板 + HTMX | 无额外依赖 |
| Markdown | 服务端渲染 (gomarkdown) | 零前端依赖 |

---

## 六、部署环境分析

### 6.1 AWS/Aliyun ECS

| 要素 | 对前端影响 | 应对 |
|------|-----------|------|
| ECS 单实例 | 无 | go:embed 内嵌 |
| Docker | 无 | 单二进制部署 |
| SSM | 无 | 后端读取，前端不感知 |
| ALB/SLB | 轻微 | HTTPS 终结在 LB |
| CDN | 正面 | 可选加速 /static/ |
| WAF | 正面 | 与 CSP 形成多层防护 |

### 6.2 部署架构

```
用户 → CloudFront/阿里云CDN (可选) → ALB/SLB (SSL终止) → ECS (Go二进制)
                                                          ↓
                                                    PostgreSQL + Redis
```

### 6.3 Dockerfile

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o portal ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/portal .
COPY --from=builder /app/migrations ./migrations/
EXPOSE 3001
ENTRYPOINT ["./portal"]
# 前端已通过 go:embed 内嵌到二进制
```

---

## 七、支付集成方案

### 7.1 各支付方式前端需求

| 支付方式 | 前端需求 | SDK | 加载方式 |
|----------|----------|-----|----------|
| Alipay | 展示 QR 码 | 无 | 服务端生成 |
| WeChat Pay | 展示 QR 码 | 无 | 服务端生成 |
| Stripe | Stripe Elements | ~40KB | 按需加载 |
| PayPal | PayPal Buttons | ~45KB | 按需加载 |

### 7.2 支付流程

```html
<!-- templates/billing/recharge.html -->
{{define "recharge"}}
{{template "layout" .}}

<div class="recharge-page">
  <!-- 金额选择 -->
  <div class="amount-grid">
    <button onclick="selectAmount(10)">¥10</button>
    <button onclick="selectAmount(50)">¥50</button>
    <button onclick="selectAmount(100)">¥100</button>
    <input type="number" id="custom-amount" placeholder="自定义">
  </div>

  <!-- 支付方式 -->
  <div class="payment-methods">
    <label><input type="radio" name="method" value="alipay"> 支付宝</label>
    <label><input type="radio" name="method" value="wechat"> 微信支付</label>
    <label><input type="radio" name="method" value="stripe"> 信用卡</label>
    <label><input type="radio" name="method" value="paypal"> PayPal</label>
  </div>

  <!-- 提交 -->
  <button hx-post="/api/orders/create"
          hx-include="[name='method'], #custom-amount"
          hx-target="#checkout-container">
    确认支付
  </button>

  <!-- 支付界面 (HTMX 动态加载) -->
  <div id="checkout-container"></div>
</div>

<!-- Stripe/PayPal 按需加载 -->
<script>
function loadStripe() {
  if (window.Stripe) return;
  const script = document.createElement('script');
  script.src = 'https://js.stripe.com/v3/';
  document.head.appendChild(script);
}

function loadPayPal() {
  if (window.paypal) return;
  const script = document.createElement('script');
  script.src = 'https://www.paypal.com/sdk/js?client-id={{.PayPalClientId}}';
  document.head.appendChild(script);
}
</script>
{{end}}
```

---

## 八、目录结构

```
ai-portal-go/
├── cmd/server/main.go
├── internal/
│   ├── routes/              # 现有 JSON API (不变)
│   └── web/                 # 新增: Web 页面处理
│       ├── handler.go       # Handler 结构体
│       ├── pages.go         # 页面路由处理
│       ├── data.go          # 模板数据获取
│       ├── helpers.go       # 模板函数
│       └── middleware.go    # CSRF, CSP
├── web/
│   ├── templates/
│   │   ├── layout.html      # 基础布局
│   │   ├── partials/
│   │   │   ├── nav.html     # 侧边栏
│   │   │   ├── mobile-nav.html
│   │   │   ├── flash.html   # 消息提示
│   │   │   └── pagination.html
│   │   ├── auth/
│   │   │   ├── login.html
│   │   │   └── register.html
│   │   ├── dashboard.html
│   │   ├── apikeys.html
│   │   ├── billing/
│   │   │   ├── balance.html
│   │   │   ├── usage.html
│   │   │   └── recharge.html
│   │   ├── playground.html  # 未来: 按需加载 Monaco
│   │   ├── admin/
│   │   │   ├── users.html
│   │   │   ├── models.html
│   │   │   ├── keypool.html
│   │   │   └── settings.html
│   │   └── errors/
│   │       ├── 404.html
│   │       └── 500.html
│   ├── static/
│   │   ├── css/
│   │   │   └── main.css     # 自定义样式
│   │   ├── js/
│   │   │   ├── htmx.min.js
│   │   │   └── app.js       # 全局逻辑 (主题切换等)
│   │   └── img/
│   │       └── logo.svg
│   └── embed.go             # go:embed 声明
├── migrations/
├── Dockerfile
└── ...
```

---

## 九、多 Agent 协作计划

### 9.1 Agent 分工

| Agent | 职责 | 产出 |
|-------|------|------|
| **Code Agent** | 前端代码编写 | web/ 目录所有文件 |
| **Test Agent** | 单元测试 + 集成测试 | *_test.go, test-web.ts |
| **Service Agent** | 服务测试 + 压力测试 | test-e2e.ts, wrk 报告 |
| **Review Agent** | 代码审查 + 安全审计 | review 报告 |

### 9.2 Code Agent 任务

| 阶段 | 任务 | 工时 |
|------|------|------|
| Phase 1 | 基础设施: embed.go, handler.go, layout.html, main.css | 4h |
| Phase 2 | 认证页面: login, register, forgot-password | 3h |
| Phase 3 | 用户页面: dashboard, apikeys, billing, settings | 5h |
| Phase 4 | 管理页面: users, models, keypool, providers | 5h |
| Phase 5 | 支付集成: recharge, checkout, order-status | 4h |

### 9.3 Test Agent 任务

| 测试类型 | 覆盖范围 | 工具 |
|----------|----------|------|
| 单元测试 | Go handler 函数 | testing + testify |
| 模板测试 | 模板渲染正确性 | httptest |
| 集成测试 | 页面 + API 联调 | test-web.ts |
| 安全测试 | CSRF, XSS, CSP | test-security.ts |

### 9.4 Service Agent 任务

| 测试类型 | 指标 | 工具 |
|----------|------|------|
| E2E 测试 | 用户流程 | Playwright / Puppeteer |
| 压力测试 | RPS, P99 延迟 | wrk / k6 |
| 移动测试 | 响应式布局 | Chrome DevTools |
| 支付测试 | 沙箱环境 | Stripe/PayPal sandbox |

### 9.5 Review Agent 任务

| 审查项 | 重点 |
|--------|------|
| 代码质量 | Go 模板最佳实践、错误处理 |
| 安全审计 | CSP、CSRF、XSS、Cookie 配置 |
| 性能审计 | 首屏加载、资源大小、缓存策略 |
| 可访问性 | ARIA 标签、键盘导航、对比度 |

---

## 十、开发计划

| 周 | Code Agent | Test Agent | Service Agent | Review Agent |
|----|------------|------------|---------------|--------------|
| 1 | Phase 1-2 | 单元测试 | — | — |
| 2 | Phase 3 | 集成测试 | E2E 测试 | 代码审查 |
| 3 | Phase 4-5 | 安全测试 | 压力测试 | 安全审计 |
| 4 | 修复 + 优化 | 回归测试 | 移动测试 | 最终审查 |

---

## 十一、体积与性能目标

| 指标 | 目标 | 说明 |
|------|------|------|
| 首屏 HTML | < 20KB (gzip) | 服务端渲染 |
| 首屏 CSS | < 10KB (gzip) | 自定义样式 |
| 首屏 JS | < 20KB (gzip) | HTMX + app.js |
| 首屏总计 | < 50KB (gzip) | 内嵌二进制 |
| TTFB | < 100ms | 境内 / < 300ms 境外 |
| 首屏渲染 | < 500ms | 境内 / < 1s 境外 |
| 页面切换 | < 200ms | HTMX 局部刷新 |

---

**文档版本:** 2.0
**更新时间:** 2026-05-04
**综合来源:** Claude, Kimi, Xiaomi, Minimax, Deepseek
