# AI Portal Go Frontend Architecture Decision

**Status:** ACTIVE
**Created:** 202605041600

---

## 1. Performance & Loading Speed

**问题:** 境外服务器 + 轻量加载需求

**分析:**
- 距离远 = 高延迟，静态资源加载受影响
- ECS 通常带宽有限，需要控制传输体积
- 用户体验取决于首屏加载速度

**建议方案:**
| 方案 | 体积 | 首屏 | SEO | 维护 |
|------|------|------|-----|------|
| 纯静态 HTML | 50-100KB | 最快 | 好 | 简单 |
| Preact + inline | 150KB | 快 | 差 | 中等 |
| React (代码分割) | 200KB | 快 | 差 | 复杂 |

**推荐:** 使用 **Preact** (3KB) 替代 React，配合少量 inline JS 做交互。

---

## 2. UI/UX Design

**问题:** 简约科技风 + 渐变 + 响应式

**设计方向:**
- 主色调: 深色背景 (#0f172a) + 蓝紫渐变 (#3b82f6 → #8b5cf6)
- 卡片式布局，圆角 8-12px
- 渐变用于: 按钮、hover 效果、强调区域
- 字体: Inter / system-ui
- 移动端优先: flex + grid，BP 768px/1024px/1280px

**技术选型:**
- Tailwind CSS CDN (仅在调试时)
- 生产用: 构建时 purgeCSS，体积 < 50KB
- 图标: Lucide (可选，仅在需要时加载)

---

## 3. Security Measures

**问题:** 涉及充值，必须强化安全

**后端安全 (Go):**
- HTTPS only (强制重定向)
- CSRF Token
- Rate Limiting (登录/支付)
- 输入验证 (所有 user input)
- SQL 注入防护 (parameterized queries)
- JWT 短期 + Refresh Token

**前端安全:**
- 敏感数据不存 localStorage (用 httpOnly cookie)
- CSP (Content Security Policy) headers
- XSS 防护 (React 默认防 XSS)
- PCI-DSS 兼容 (支付相关，不在前端存储卡号)

**建议:**
```
// Go middleware 添加
- CORS 严格配置
- Security headers (HSTS, CSP, X-Frame-Options)
- 请求签名验证
```

---

## 4. Pure Static vs Inline JS

**问题:** 纯粹静态页面扩展性差？

**分析:**

| 方案 | 优点 | 缺点 | 适用场景 |
|------|------|------|----------|
| 纯静态 | 快、安全、无依赖 | 交互差 | 官网 |
| 少量 inline JS | 轻量、有交互 | 复杂逻辑复杂 | 管理面板(简单) |
| Preact/React SPA | 交互强、状态管理 | 体积大 | 管理面板(复杂) |

**推荐:** **混合方案**
- 页面: 纯 HTML + Server-rendered
- 交互: 少量 vanilla JS inline ( < 5KB per page)
- 特定页面 (Key 管理、充值): Preact component

**示例结构:**
```
dist/
├── index.html              # 主入口
├── assets/
│   └── app.js              # 少量 inline JS (gzipped < 10KB)
├── css/
│   └── styles.css          # Tailwind purged
└── images/
```

---

## 5. Playground & Future扩展

**问题:** 未来可能加入 Playground，前端复杂度？

**Playground 需求:**
- 多模型切换
- 流式输出 (Server-Sent Events / WebSocket)
- Markdown 渲染 (marked.js 1KB)
- 代码高亮 (highlight.js 10KB)
- 文件上传

**架构评估:**

| 组件 | 当前架构 | Playground 支持 |
|------|----------|------------------|
| 路由 | Gin server | ✅ 可行 |
| 静态文件 | `embed.FS` | ✅ 可行 |
| SSE/WS | Gin support | ✅ 可行 |
| 前端状态 | 无 SPA | ⚠️ 需引入 Preact |
| 实时更新 | 轮询 | ⚠️ 改用 SSE |

**结论:** 当前架构可扩展，但 Playground 需要引入 **Preact** 作为轻量 SPA。

**建议:**
1. 保持当前 Go + embed 架构
2. 特定页面 (Playground、Chat) 使用 Preact
3. 其他页面保持静态 HTML + inline JS

---

## 6. Cloud Deployment & Security

**问题:** AWS / Aliyun ECS 对鉴权、SSM、Docker 的要求？

**AWS / Aliyun 东南亚:**

| 项目 | 影响 | 应对 |
|------|------|------|
| HTTPS | 需配置证书 | ACM (AWS) / Ali SSL |
| 防火墙 | 端口限制 | 仅开放 443, 22 (SSH) |
| SSM | ECS 需要 | 使用 bastion host |
| Docker | 完全支持 | 无特殊要求 |
| 负载均衡 | ALB/CLB | 建议添加 |

**部署架构建议:**
```
                    ┌─────────────────┐
   User ──────►  CloudFront / CDN  ──────► S3 (静态) / ALB
                                              │
                                              ▼
                                         ECS (Docker)
                                              │
                                              ▼
                                        RDS / ElastiCache
```

**Dockerfile 建议:**
```dockerfile
FROM scratch
COPY portal /portal
COPY dist /dist
EXPOSE 3001
ENTRYPOINT ["/portal"]
# 或使用 alpine 镜像
```

---

## 7. Payment Integration

**问题:** Alipay / WeChat Pay / Stripe / PayPal 对前端要求

**各支付方式对比:**

| 支付方式 | 前端集成 | 后端需求 | PCI-DSS |
|----------|----------|----------|---------|
| Alipay | 跳转支付页 | 需商户账号 | 不需要 |
| WeChat Pay | 跳转支付页 | 需商户账号 | 不需要 |
| Stripe | JS SDK (5KB) | Stripe API | 必须 |
| PayPal | JS SDK | PayPal API | 必须 |

**前端要求:**

| 支付方式 | SDK | 前端工作 |
|----------|-----|----------|
| Alipay | 无 | 跳转链接 |
| WeChat Pay | 无 | 跳转链接 |
| Stripe | `@stripe/stripe-js` | 信用卡组件 (安全) |
| PayPal | `@paypal/react-paypal-js` | PayPal 按钮 |

**安全注意事项:**
- Stripe/PayPal: 卡号不进我们服务器，通过 SDK tokenization
- 支付回调必须验证签名
- 前端仅存储 `payment_intent_id`，不存储敏感信息

**前端代码示例 (Stripe):**
```html
<!-- Stripe.js 仅在支付页面加载 -->
<script src="https://js.stripe.com/v3/"></script>
<script>
  // 实际卡号从不经过我们的服务器
  const stripe = Stripe('pk_test_xxx');
</script>
```

---

## Final Recommendations

### 架构决策

```
┌─────────────────────────────────────────────────────────────┐
│                    ai-portal-go                              │
├─────────────────────────────────────────────────────────────┤
│  Go Backend (Gin)                                            │
│  ├── embed.FS (静态文件)                                     │
│  ├── JWT Auth + Security Middleware                          │
│  ├── API Routes (REST)                                       │
│  └── SSE / WebSocket (未来 Playground)                       │
├─────────────────────────────────────────────────────────────┤
│  Frontend                                                    │
│  ├── HTML (静态, Server-rendered)                            │
│  ├── Tailwind CSS (purgeCSS, < 50KB)                        │
│  ├── Preact (少量 SPA组件, < 5KB)                            │
│  └── Inline JS (少量交互, < 10KB)                            │
└─────────────────────────────────────────────────────────────┘
```

### 技术栈最终选择

| 层级 | 技术 | 原因 |
|------|------|------|
| 框架 | Go + Gin | 高性能、简单部署 |
| 静态服务 | `embed.FS` | 单 binary，零依赖 |
| CSS | Tailwind CDN → purged | 开发快，生产小 |
| 轻量 SPA | Preact | 3KB vs React 40KB |
| 支付 | Stripe + PayPal SDK | 标准化、安全 |
| 鉴权 | JWT + httpOnly Cookie | 安全 |

### 下一步行动

1. ~~确定前端技术选型~~ ✅
2. 设计 UI 组件库 (3-5 核心组件)
3. 实现登录/注册/Key 管理页面
4. 集成支付 (Stripe / PayPal)
5. 添加 Playground (可选，后续)

---

## Appendix: Related Files

- `../ai-proxy-main/` - Node.js reference
- `../ai-proxy-go/` - Go proxy reference
- `../new-api/` - Open source reference (React)
