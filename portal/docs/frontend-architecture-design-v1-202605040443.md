# AI Portal Go - 前端架构方案

> **ACTIVE** - Created: 2026-05-04

---

## 一、核心诉求分析

### 1.1 性能要求

| 诉求 | 分析 | 方案 |
|------|------|------|
| 境外服务器部署 | 东南亚 ECS，网络延迟敏感 | 服务端渲染 + CDN 静态资源 |
| 少依赖 | npm 生态风险，bundle 过大 | 零 npm，纯 Go 模板 |
| 加载快 | 首屏 < 1s，交互 < 100ms | HTMX 局部刷新，无 SPA 白屏 |

### 1.2 设计风格

| 元素 | 实现 |
|------|------|
| 简约科技风 | 大留白、清晰层次、无多余装饰 |
| 简单背景色 | 纯色/微渐变，不用复杂背景图 |
| 渐变效果 | 按钮、卡片 hover、标题点缀 |
| 电脑适配 | 标准 grid/flex 布局 |
| 手机适配 | 移动优先，sidebar 折叠为 hamburger |

### 1.3 安全要求

| 风险 | 措施 |
|------|------|
| XSS | Go template 自动转义，CSP header |
| CSRF | 双重提交 Cookie + Token |
| 注入 | 参数化 SQL（已实现） |
| 会话劫持 | HttpOnly + Secure + SameSite Cookie |
| 暴力破解 | Rate limit (已实现) |
| 数据泄露 | 敏感信息脱敏，日志不记录密码 |

---

## 二、技术选型

### 2.1 最终方案：Go Template + HTMX + Tailwind CDN

```
┌─────────────────────────────────────────────────┐
│                  浏览器                          │
├─────────────────────────────────────────────────┤
│  HTML (Go 渲染)  │  HTMX (交互)  │  少量 JS    │
└────────┬──────────┴───────┬───────┴──────┬──────┘
         │                  │              │
         ▼                  ▼              ▼
┌─────────────────────────────────────────────────┐
│           Go Server (Gin)                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────────┐  │
│  │ Template  │  │  HTMX    │  │  REST API    │  │
│  │ Rendering │  │ Handlers │  │  (JSON)      │  │
│  └──────────┘  └──────────┘  └──────────────┘  │
└─────────────────────────────────────────────────┘
```

### 2.2 依赖清单

| 依赖 | 方式 | 说明 |
|------|------|------|
| Tailwind CSS | CDN `<script>` | 无需构建，自动 JIT |
| HTMX | CDN `<script>` 或 go:embed | 14KB gzip |
| Lucide Icons | 内联 SVG | 零请求 |
| Alpine.js | 可选 CDN | 轻量交互 (dropdown, modal) |
| Go html/template | 标准库 | 服务端渲染 |

**总前端资源大小：** ~50KB (gzip)

### 2.3 不使用的技术

| 技术 | 原因 |
|------|------|
| React/Vue/Angular | 需要构建工具，依赖重 |
| Webpack/Vite | 增加构建复杂度 |
| npm | 安全风险，依赖地狱 |
| TypeScript | 需要编译步骤 |

---

## 三、响应式设计

### 3.1 断点策略

```css
/* Tailwind 默认断点 */
sm: 640px    /* 大手机 */
md: 768px    /* 平板 */
lg: 1024px   /* 笔记本 */
xl: 1280px   /* 桌面 */
```

### 3.2 布局方案

**桌面 (>1024px):**
```
┌──────────┬────────────────────────────┐
│          │                            │
│ Sidebar  │       Main Content         │
│ (固定)    │       (可滚动)             │
│          │                            │
└──────────┴────────────────────────────┘
```

**手机 (<768px):**
```
┌──────────────────────────────────────┐
│ ☰  Logo                    👤       │
├──────────────────────────────────────┤
│                                      │
│         Main Content                 │
│         (全宽)                       │
│                                      │
└──────────────────────────────────────┘

Sidebar: 点击 ☰ 弹出 overlay
```

### 3.3 手机优先实现

```html
<!-- Sidebar: 默认隐藏，lg 显示 -->
<aside class="hidden lg:block lg:w-64 lg:fixed ...">

<!-- Mobile menu button -->
<button class="lg:hidden" hx-get="/partials/sidebar" 
        hx-target="#mobile-sidebar" hx-trigger="click">
  ☰
</button>
```

---

## 四、安全强化方案

### 4.1 前端安全措施

| 措施 | 实现 |
|------|------|
| CSP Header | `Content-Security-Policy: default-src 'self'; script-src 'self' cdn.tailwindcss.com cdn.jsdelivr.net` |
| XSS 防护 | Go template `{{.}}` 自动 HTML 转义 |
| CSRF | 每个表单 `<input type="hidden" name="_csrf" value="{{.CSRFToken}}">` |
| Cookie 安全 | `HttpOnly; Secure; SameSite=Lax; Path=/` |
| 点击劫持 | `X-Frame-Options: DENY` |
| MIME 嗅探 | `X-Content-Type-Options: nosniff` |

### 4.2 支付页面安全

```go
// 支付页面额外安全头
func paymentSecurityHeaders() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Header("Content-Security-Policy", "default-src 'self'; frame-src pay.google.com js.stripe.com")
        c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
        c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
    }
}
```

### 4.3 敏感操作确认

```
┌─────────────────────────────────────────┐
│  ⚠️ 确认充值                             │
│                                         │
│  金额: ¥100.00                          │
│  方式: Alipay                           │
│                                         │
│  请输入密码确认:                         │
│  [________________]                     │
│                                         │
│  [取消]              [确认支付]          │
└─────────────────────────────────────────┘
```

---

## 五、架构扩展性分析

### 5.1 当前架构 vs 未来需求

| 特性 | 当前 | 未来 (Playground) | 架构支持 |
|------|------|-------------------|----------|
| 页面渲染 | Go template | Go template | ✅ 同一模式 |
| 动态交互 | HTMX | HTMX + 少量 JS | ✅ 渐进增强 |
| API 调用 | HTMX → Go → API | JS → API (直接) | ✅ REST API 兼容 |
| 实时更新 | 轮询 | WebSocket | ✅ Go 原生支持 |
| 复杂表单 | HTML form | JS 增强 | ✅ Alpine.js 补充 |

### 5.2 Playground 场景分析

Playground 需要：
- 代码编辑器 (Monaco/CodeMirror)
- 实时 API 调用
- 流式响应 (SSE/WebSocket)
- 结果格式化显示

**方案：** Playground 页面单独用 JS 组件，其他页面保持 HTMX

```html
<!-- 普通页面: HTMX -->
<div hx-get="/api/dashboard" hx-trigger="load">

<!-- Playground 页面: JS 组件 -->
<script src="/static/js/monaco-editor.min.js"></script>
<div id="playground-editor"></div>
```

### 5.3 渐进式 JS 策略

```
Level 0: 纯 HTML + HTMX (90% 页面)
Level 1: HTMX + Alpine.js (dropdown, modal, tab)
Level 2: 独立 JS 组件 (editor, chart, payment)
```

---

## 六、部署环境分析

### 6.1 AWS/Aliyun ECS 前端影响

| 组件 | 对前端影响 | 说明 |
|------|-----------|------|
| ECS | 无 | Go 编译为单二进制 |
| Docker | 无 | 多阶段构建，前端内嵌 |
| SSM (Secrets Manager) | 无 | 后端读取，前端不感知 |
| ALB/Nginx | 轻微 | 需配置静态文件缓存 |
| CDN | 正面 | 静态资源 CDN 加速 |

### 6.2 Docker 部署

```dockerfile
# 多阶段构建
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o portal ./cmd/server

FROM alpine:3.19
COPY --from=builder /app/portal .
COPY --from=builder /app/migrations ./migrations/
# 前端模板已通过 go:embed 内嵌到二进制
EXPOSE 3001
ENTRYPOINT ["./portal"]
```

### 6.3 Nginx 配置 (如使用反向代理)

```nginx
server {
    listen 443 ssl http2;
    server_name portal.example.com;

    # 静态资源缓存
    location /static/ {
        expires 30d;
        add_header Cache-Control "public, immutable";
    }

    # API 和页面
    location / {
        proxy_pass http://127.0.0.1:3001;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

---

## 七、支付集成前端要求

### 7.1 各支付方式前端需求

| 支付方式 | 前端需求 | SDK 大小 | 说明 |
|----------|----------|----------|------|
| Alipay | 跳转/二维码 | 0 | 服务端生成链接 |
| WeChat Pay | 二维码/JSAPI | 0 | 服务端生成链接 |
| Stripe | Stripe.js | ~60KB | 前端收集卡号 (PCI 合规) |
| PayPal | PayPal SDK | ~40KB | 按钮渲染 |

### 7.2 支付流程设计

**Alipay/WeChat Pay (无需前端 SDK):**
```
用户选择金额 → 后端创建订单 → 返回支付链接/二维码 → 用户支付 → Webhook 通知 → 更新余额
```

**Stripe (需要前端 SDK):**
```html
<!-- 仅在支付页面加载 Stripe.js -->
{{if .IsPaymentPage}}
<script src="https://js.stripe.com/v3/"></script>
<script>
const stripe = Stripe('{{.StripePublicKey}}');
// 收集卡号，PCI 合规
</script>
{{end}}
```

**PayPal (需要前端 SDK):**
```html
<!-- 仅在支付页面加载 PayPal SDK -->
<script src="https://www.paypal.com/sdk/js?client-id={{.PayPalClientId}}"></script>
```

### 7.3 支付安全

| 措施 | 说明 |
|------|------|
| PCI DSS | 使用 Stripe Elements，卡号不经过我们服务器 |
| HTTPS | 支付页面强制 HTTPS |
| 金额校验 | 前端显示金额，后端校验实际金额 |
| 防重复提交 | 支付按钮 disable + 幂等订单号 |

---

## 八、最终方案

### 8.1 技术栈

| 层 | 技术 | 版本 |
|----|------|------|
| 模板引擎 | Go html/template | 标准库 |
| 交互 | HTMX | 2.0 |
| 样式 | Tailwind CSS | 3.4 CDN |
| 轻量交互 | Alpine.js | 3.x CDN (可选) |
| 图标 | Lucide SVG | 内联 |
| 编辑器 | Monaco/CodeMirror | 仅 Playground 页面 |
| 支付 | Stripe.js / PayPal SDK | 仅支付页面 |

### 8.2 目录结构

```
ai-portal-go/
├── cmd/server/main.go
├── internal/
│   ├── config/
│   ├── db/
│   ├── middleware/
│   ├── routes/
│   └── web/                    # 前端核心
│       ├── handler.go          # 路由 + 渲染
│       ├── middleware.go        # CSRF, 安全头
│       └── template.go         # 模板引擎初始化
├── web/
│   ├── templates/
│   │   ├── layout/
│   │   │   ├── base.html       # 基础布局
│   │   │   ├── sidebar.html    # 侧边栏
│   │   │   └── header.html     # 顶栏
│   │   ├── pages/
│   │   │   ├── index.html      # 首页/登录
│   │   │   ├── dashboard.html  # 仪表盘
│   │   │   ├── apikeys.html    # API Keys
│   │   │   ├── billing.html    # 计费
│   │   │   ├── orders.html     # 订单
│   │   │   ├── settings.html   # 设置
│   │   │   └── admin/
│   │   │       ├── dashboard.html
│   │   │       ├── users.html
│   │   │       ├── models.html
│   │   │       └── settings.html
│   │   └── partials/           # HTMX 局部刷新
│   │       ├── apikeys-table.html
│   │       ├── usage-chart.html
│   │       └── notifications.html
│   ├── static/
│   │   ├── css/
│   │   │   └── app.css         # 自定义样式 (少量)
│   │   ├── js/
│   │   │   ├── app.js          # 全局逻辑 (主题切换等)
│   │   │   └── htmx-config.js  # HTMX 配置
│   │   └── img/
│   │       └── logo.svg
│   └── embed.go                # go:embed 嵌入
├── migrations/
├── Dockerfile
└── ...
```

### 8.3 性能指标

| 指标 | 目标 |
|------|------|
| 首屏加载 | < 500ms (国内) / < 1s (东南亚) |
| 页面切换 | < 200ms (HTMX 局部刷新) |
| JS 大小 | < 20KB (gzip，不含支付 SDK) |
| CSS 大小 | < 10KB (自定义，Tailwind CDN 另算) |
| 总资源 | < 100KB (首屏，gzip) |

---

## 九、开发计划

| 周 | 任务 | 产出 |
|----|------|------|
| 1 | 基础布局、登录/注册 | 可登录系统 |
| 2 | 仪表盘、API Keys、设置 | 用户功能 |
| 3 | 计费、订单、支付集成 | 充值功能 |
| 4 | 管理后台 | 管理功能 |
| 5 | 响应式优化、安全测试、部署 | 上线 |

---

**文档版本:** 1.0
**更新时间:** 2026-05-04
