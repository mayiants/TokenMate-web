# AI Portal Go — Frontend Architecture V3

**Status:** DRAFT
**Created:** 202605042300
**Supersedes:** frontend-architecture-202605042000.md

---

## 0. 需求确认清单

| # | 需求 | V3 方案 | 状态 |
|---|------|---------|------|
| 0.1 | HTMX + Alpine.js，无 CDN 构建 | go:embed + Tailwind purged | ✅ 确认 |
| 0.2 | 支付宝/微信零 SDK，Stripe/PayPal 按需 | 服务端 QR + SDK 动态加载 | ✅ 确认 |
| 0.3 | 中英双语支持 | i18n 框架（轻量） | ✅ 确认 |
| 0.4 | Light/Dark/跟随系统 | CSS 变量 + Alpine.js | ✅ 确认 |
| 0.5 | 邮件注册 + 验证码 | 后端发送 + 前端验证 | ✅ 确认 |
| 0.6 | 用户类型 + 权限 | user/enterprise_admin/system_admin/super_admin | ✅ 确认 |
| 0.7 | 多种计费类型 | economy/general/single-model/subscription/enterprise | ✅ 确认 |
| 0.8 | API Key 前缀策略 | sk-wgit- / sk-wgit-tp- / 企业批量 | ✅ 讨论 |
| 0.9 | Token + Request 双重统计 | 数据库双字段 | ✅ 确认 |
| 0.10 | Token Plan / Code Plan | 阶梯计费 + RPM 计费 | ✅ 确认 |
| 0.11 | 发票 + 地址管理 | 多对多关联 | ✅ 确认 |
| 0.12 | GDPR 7 年数据保存 | 热/温/冷分层 | ✅ 确认 |
| 0.13 | 敏感词过滤 | **暂不支持**（proxy 无 tokenizer） | ✅ 确认 |
| 0.14 | 兑换码 | 优先消费，不提供发票 | ✅ 确认 |

---

## 1. 技术栈确认

### 1.1 最终技术选型

| 层级 | 技术 | 说明 |
|------|------|------|
| 模板引擎 | Go `html/template` + `embed` | SSR 首屏，零依赖 |
| 交互框架 | HTMX 2.x + Alpine.js 3.x | 14KB + 15KB，本地 embed |
| 样式 | Tailwind CSS (构建时 purge) | 生产不依赖 CDN |
| 构建工具 | Bun (CSS purge only) | 仅用于 Tailwind purge |
| i18n | go-i18n + 模板函数 | 中英双语，轻量 |
| 图标 | Lucide SVG 内联 | 按需，仅用到的图标 |

### 1.2 为什么不使用 CDN

| CDN 方案 | 问题 | V3 方案 |
|----------|------|---------|
| Tailwind CDN | 境外加载慢，调试困难 | 本地 `tailwindcss` CLI purge |
| HTMX CDN | 同上 | `htmx.min.js` 本地 embed |
| Alpine CDN | 同上 | `alpine.min.js` 本地 embed |

**构建流程：**
```bash
# 开发
bun run dev  # 热更新，CDN Tailwind 调试模式

# 生产
bun run build:css  # purgeTailwind，生成 app.min.css
go build -o portal ./cmd/server  # go:embed 打包
```

### 1.3 资源体积

| 资源 | 开发 | 生产 (purge) |
|------|------|--------------|
| HTML | ~15KB | ~15KB |
| Tailwind CSS | ~300KB CDN | ~20KB |
| HTMX | 14KB embed | 14KB embed |
| Alpine | 15KB embed | 15KB embed |
| 自定义 CSS | ~5KB | ~5KB |

**首屏总大小：~50KB**（生产）

---

## 2. i18n 国际化

### 2.1 方案选择

| 方案 | 体积 | 复杂度 | 适用性 |
|------|------|--------|--------|
| 全语言文件 | 大 | 简单 | ✅ 采纳 |
| JS 动态加载 | 中 | 中 | ❌ 增加复杂度 |
| Cookie/Header 判断 | - | - | 不影响体积 |

### 2.2 目录结构

```
web/
├── locales/
│   ├── en.json      # 英文
│   └── zh.json      # 中文
├── templates/
│   └── ...
└── embed.go
```

### 2.3 翻译函数

```go
// internal/web/helpers.go
func (h *Handler) i18n(key, locale string) string {
    // 读取 h.locales[locale][key]
    return h.locales[locale][key]
}

// 模板中使用
<span>{{i18n "dashboard.balance" .Locale}}</span>
```

### 2.4 语言切换

```html
<!-- header.html -->
<select onchange="window.location.href='/lang/' + this.value">
  <option value="en" {{if eq .Locale "en"}}selected{{end}}>English</option>
  <option value="zh" {{if eq .Locale "zh"}}selected{{end}}>中文</option>
</select>
```

---

## 3. 主题系统

### 3.1 CSS 变量

```css
/* main.css */
:root {
  --bg: #f8fafc;
  --bg-card: #ffffff;
  --text: #1e293b;
  --primary: #3b82f6;
  --border: #e2e8f0;
}

[data-theme="dark"] {
  --bg: #0f172a;
  --bg-card: #1e293b;
  --text: #f8fafc;
  --primary: #60a5fa;
  --border: #334155;
}
```

### 3.2 主题切换逻辑

```html
<!-- Alpine.js 主题切换 -->
<script>
function themeInit() {
  const saved = localStorage.getItem('theme');
  const system = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  return saved || system;
}
</script>

<div x-data="{ theme: themeInit() }" x-init="$watch('theme', v => {
  document.documentElement.setAttribute('data-theme', v);
  localStorage.setItem('theme', v);
})">
  <!-- 主题按钮 -->
</div>
```

---

## 4. 用户权限体系

### 4.1 角色定义

| 角色 | 说明 | 最大人数 | 核心权限 |
|------|------|----------|----------|
| super_admin | 超级管理员 | 1-3 位 | 添加/删除 system_admin，管理供应商，管理支付接口 |
| system_admin | 系统管理员 | ≤10 位 | 日常维护，计费账单管理，upstream pool |
| enterprise_admin | 企业管理员 | 不限 | 管理本企业 user key，查看用量和账单 |
| user | 普通用户 | 不限 | 基本 dashboard，API Keys，充值 |

### 4.2 super_admin 特殊规则

```
1. super_admin 不可删除自己
2. 删除 super_admin 需要先降级为 system_admin
3. 降级操作需要另一位 super_admin 确认
4. 密码修改不受限制
```

### 4.3 页面路由权限

| 页面 | user | enterprise_admin | system_admin | super_admin |
|------|------|-----------------|--------------|------------|
| /dashboard | ✅ | ✅ | ✅ | ✅ |
| /apikeys | ✅ | ✅ | ✅ | ✅ |
| /billing | ✅ | ✅ | ✅ | ✅ |
| /admin/users | ❌ | ❌ | ✅ | ✅ |
| /admin/providers | ❌ | ❌ | ❌ | ✅ |
| /admin/settings | ❌ | ❌ | ✅ | ✅ |

---

## 5. 用户类型与 API Key 策略

### 5.1 用户类型

| 类型 | 说明 | 计费方式 | API Key 前缀 |
|------|------|----------|--------------|
| economy | 经济客户 | Token 池，按量计费 | `sk-wgit-ec-` |
| general | 一般用户 | Token 池，按量计费 | `sk-wgit-` |
| single-model | 单品购买 | 指定模型包 | `sk-wgit-sm-` |
| subscription | 订阅用户 | Token/Code Plan | `sk-wgit-tp-` |
| enterprise | 企业用户 | 企业额度池 | `sk-wgit-ent-` |

### 5.2 API Key 前缀策略

**问题讨论：** 如何区分不同用户的 key？

**V3 方案：**

| 用户类型 | Key 前缀 | 示例 | 访问控制 |
|----------|----------|------|----------|
| general | `sk-wgit-` | sk-wgit-a1b2c3d4 | 普通池，全部模型 |
| economy | `sk-wgit-ec-` | sk-wgit-ec-xxx | 经济池，限价模型 |
| single-model | `sk-wgit-sm-` | sk-wgit-sm-xxx | 单一模型包 |
| subscription (Token) | `sk-wgit-tp-` | sk-wgit-tp-xxx | Token Plan 池 |
| subscription (Code) | `sk-wgit-tc-` | sk-wgit-tc-xxx | Code Plan 池 |
| enterprise | `sk-wgit-ent-` | sk-wgit-ent-xxx | 企业高优池 |

**访问控制实现：**

```go
// internal/middleware/auth.go
func (a *Auth) ValidateAPIKey(apiKey string) (*KeyUsage, error) {
    prefix := strings.Split(apiKey, "-")[0]
    switch prefix {
    case "sk-wgit-ec":
        return a.checkEconomyPool(apiKey)
    case "sk-wgit-sm":
        return a.checkSingleModelPool(apiKey, modelID)
    case "sk-wgit-tp":
        return a.checkTokenPlan(apiKey, tokensRemaining)
    case "sk-wgit-tc":
        return a.checkCodePlan(apiKey, requestsRemaining)
    case "sk-wgit-ent":
        return a.checkEnterprisePool(apiKey, quotaRemaining)
    default:
        return a.checkGeneralPool(apiKey)
    }
}
```

### 5.3 企业管理员批量创建

```html
<!-- enterprise_admin 创建 user key 批量模式 -->
<form hx-post="/enterprise/keys/batch">
  <input type="number" name="count" min="1" max="100" value="10">
  <input type="text" name="prefix" placeholder="team-a-">
  <button type="submit">生成 10 把 Key</button>
</form>
```

---

## 6. 计费模型

### 6.1 Token + Request 双重统计

所有 usage 记录必须包含：

```sql
ALTER TABLE usage_records ADD COLUMN request_count INTEGER DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN prompt_tokens INTEGER DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN completion_tokens INTEGER DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN cache_hit_tokens INTEGER DEFAULT 0;
```

**统计展示：**
- 计费时选择 Token 或 Request 生效
- 两种数据都记录，用于分析和审计
- 上游对账时使用，不能完全相信上游账单

### 6.2 Token Plan 计费

| 费用类型 | 计费公式 | 说明 |
|----------|----------|------|
| input (cache miss) | prompt_tokens × ratio / 1M | 有效输入 |
| input (cache hit) | cache_hit_tokens × ratio × 0.1 / 1M | 缓存命中，10% 计价 |
| output | completion_tokens × ratio / 1M | 输出 |
| reasoning | 单独字段 | 推理模型专属 |

**Admin 配置界面：**
```html
<!-- admin/models.html -->
<div class="price-tier">
  <label>输入(cache miss):</label>
  <input type="number" name="input_cache_miss_price" step="0.0001"> $/1M

  <label>输入(cache hit):</label>
  <input type="number" name="input_cache_hit_price" step="0.0001"> $/1M

  <label>输出:</label>
  <input type="number" name="output_price" step="0.0001"> $/1M
</div>
```

### 6.3 Code Plan 计费

| 档位 | 月请求量 | 价格 |
|------|----------|------|
| lite | 60 rpm | $39/月 |
| standard | 500 rpm | $99/月 |
| pro | 1200 rpm | $399/月 |
| max | 9000 rpm | $699/月 |
| enterprise | 18000+ | 定制 |

**Code Plan 统计：**
- rpm (requests per minute)
- rph (requests per hour)
- rp5h (requests per 5 hours)
- rpw (requests per week)
- rpmonth (requests per month)

### 6.4 计费页面设计

```html
<!-- billing.html -->
<div class="billing-summary">
  <h2>{{i18n "billing.tokenStats" .Locale}}</h2>
  <table>
    <tr>
      <td>{{i18n "billing.inputCacheMiss" .Locale}}</td>
      <td>{{formatTokens .Usage.PromptTokens}}</td>
      <td>{{formatCurrency .Usage.PromptCost "USD"}}</td>
    </tr>
    <tr>
      <td>{{i18n "billing.inputCacheHit" .Locale}}</td>
      <td>{{formatTokens .Usage.CacheHitTokens}}</td>
      <td>{{formatCurrency .Usage.CacheHitCost "USD"}}</td>
    </tr>
    <tr>
      <td>{{i18n "billing.output" .Locale}}</td>
      <td>{{formatTokens .Usage.CompletionTokens}}</td>
      <td>{{formatCurrency .Usage.CompletionCost "USD"}}</td>
    </tr>
  </table>

  <h2>{{i18n "billing.requestStats" .Locale}}</h2>
  <table>
    <tr>
      <td>{{i18n "billing.totalRequests" .Locale}}</td>
      <td>{{.Usage.RequestCount}}</td>
    </tr>
    <tr>
      <td>{{i18n "billing.rpm" .Locale}}</td>
      <td>{{.Usage.RPM}}</td>
    </tr>
  </table>

  <h2>{{i18n "billing.chargeable" .Locale}}</h2>
  <p>{{i18n "billing.chargeBy" .Locale}}: {{.Usage.ChargeBy}}</p>
  <p>{{i18n "billing.totalDue" .Locale}}: {{formatCurrency .Usage.TotalDue "USD"}}</p>
</div>
```

---

## 7. 发票与地址管理

### 7.1 数据库设计

```sql
-- 发票抬头表
CREATE TABLE invoice_titles (
  id UUID PRIMARY KEY,
  user_id UUID NOT NULL,
  title_type VARCHAR(20), -- personal, enterprise
  company_name VARCHAR(200),
  tax_id VARCHAR(50),     -- 税号
  address TEXT,
  created_at TIMESTAMP DEFAULT NOW()
);

-- 收货地址表
CREATE TABLE delivery_addresses (
  id UUID PRIMARY KEY,
  user_id UUID NOT NULL,
  recipient VARCHAR(100),
  phone VARCHAR(20),
  address TEXT,
  is_default BOOLEAN DEFAULT false,
  created_at TIMESTAMP DEFAULT NOW()
);

-- 用户发票关联
CREATE TABLE user_invoices (
  id UUID PRIMARY KEY,
  user_id UUID NOT NULL,
  invoice_title_id UUID,
  delivery_address_id UUID,
  amount DECIMAL(10,2),
  status VARCHAR(20), -- pending, issued, sent
  created_at TIMESTAMP DEFAULT NOW()
);
```

### 7.2 页面设计

```html
<!-- settings.html -->
<section class="invoice-management">
  <h2>{{i18n "settings.invoices" .Locale}}</h2>
  <button hx-get="/settings/invoice/new" hx-target="#invoice-modal">
    {{i18n "settings.addInvoiceTitle" .Locale}}
  </button>

  <div class="invoice-list">
    {{range .InvoiceTitles}}
    <div class="invoice-card">
      <p>{{.CompanyName}}</p>
      <p>{{i18n "settings.taxId" .Locale}}: {{.TaxId}}</p>
      <button>编辑</button>
    </div>
    {{end}}
  </div>
</section>
```

---

## 8. 数据保存策略 (GDPR)

### 8.1 分层存储

| 层级 | 保留时间 | 存储介质 | 说明 |
|------|----------|----------|------|
| 热数据 | 1-3 个月 | PostgreSQL SSD | 常用查询 |
| 温数据 | 3-12 个月 | PostgreSQL HDD / S3 | 归档，压缩 |
| 冷数据 | 1-7 年 | S3 Glacier / Deep Archive | 加密存储 |

### 8.2 迁移策略

```go
// pkg/audit/archival.go
func (a *Audit) ArchiveOldRecords() error {
    // 迁移 3 个月前的记录到温存储
    rows, err := a.db.Query(`
        SELECT * FROM usage_records
        WHERE created_at < NOW() - INTERVAL '90 days'
        AND created_at >= NOW() - INTERVAL '1 year'
    `)
    // 压缩 + 移动到 S3

    // 迁移 1 年前的记录到冷存储
    rows, err = a.db.Query(`
        SELECT * FROM usage_records
        WHERE created_at < NOW() - INTERVAL '1 year'
    `)
    // 加密 + 移动到 Glacier
}
```

### 8.3 审计日志保留

```
审计日志: 7 年
- 7 年内: 随时可查询
- 7 年后: 删除（GDPR 合规）
```

---

## 9. 敏感词过滤

### 9.1 结论：暂不支持

| 理由 | 说明 |
|------|------|
| Proxy 无 tokenizer | 当前 ai-proxy-go 没有 token 计数能力 |
| 准确率问题 | 纯关键词匹配误报率高 |
| 性能开销 | 大规模扫描影响响应速度 |

### 9.2 未来方案预留

如果后续需要支持：

1. **Proxy 层集成**：在 proxy 层添加 tokenizer（如 tiktoken）
2. **敏感词服务**：独立的 Moderation API 调用
3. **后置检测**：先通过再人工审核

---

## 10. 兑换码

### 10.1 三种兑换码类型

| 类型 | 用途 | 例子 |
|------|------|------|
| 余额型 | 直接增加余额 | `CODE-100-xxxx` |
| 套餐型 | 赠送特定套餐 | `PLAN-PRO-xxxx` |
| 折扣型 | 折扣码 | `DISCOUNT-20-xxxx` |

### 10.2 兑换流程

```
1. 用户输入兑换码
2. HTMX POST /redeem
3. 后端验证：
   - 存在
   - 未使用
   - 未过期
   - 适用用户类型匹配
4. 余额优先消费兑换码
5. 返回成功 + 新余额
```

### 10.3 发票规则

**兑换码消费不提供发票** — 系统提示：

```html
<p class="text-warning">
  {{i18n "redeem.noInvoice" .Locale}}
</p>
```

---

## 11. 支付集成

### 11.1 四种支付方式

| 方式 | 前端要求 | SDK | 特殊页面 |
|------|----------|-----|----------|
| Alipay | 跳转/QR 码 | 无 | `/billing/checkout/alipay` |
| WeChat Pay | 二维码 | 无 | `/billing/checkout/wechat` |
| Stripe | 信用卡 | Stripe.js | `/billing/checkout/stripe` |
| PayPal | 按钮 | PayPal SDK | `/billing/checkout/paypal` |

### 11.2 支付页面 CSP

```go
func paymentCSP(path string) string {
    if strings.HasPrefix(path, "/billing/checkout/stripe") {
        return "script-src 'self' https://js.stripe.com; frame-src https://js.stripe.com"
    }
    if strings.HasPrefix(path, "/billing/checkout/paypal") {
        return "script-src 'self' https://www.paypal.com; frame-src https://www.paypal.com"
    }
    return "script-src 'self'"  // Alipay/WeChat 无特殊 JS
}
```

---

## 12. 实施计划

### Phase 1: 基础设施 (Week 1)

| 任务 | 产出 |
|------|------|
| go:embed 目录结构 | `web/embed.go` |
| Tailwind purge 构建 | `tailwind.config.js` + `bun run build:css` |
| 基础模板 | `layout.html` + CSS 变量 |
| i18n 框架 | `locales/*.json` + 模板函数 |
| 主题切换 | Alpine.js 主题逻辑 |

### Phase 2: 认证 + 用户 (Week 2)

| 任务 | 产出 |
|------|------|
| 登录/注册/验证码 | 完整 auth 流程 |
| 用户权限中间件 | role-based access |
| 用户类型字段 | 数据库扩展 |

### Phase 3: Dashboard + API Keys (Week 3)

| 任务 | 产出 |
|------|------|
| Dashboard 页面 | 统计卡片 + 用量图表 |
| API Keys CRUD | 按类型前缀生成 |
| enterprise key 批量 | 批量创建接口 |

### Phase 4: 计费 + 支付 (Week 4)

| 任务 | 产出 |
|------|------|
| 计费页面 | Token + Request 双统计展示 |
| Token Plan 配置 | Admin 价格设置 |
| Code Plan 配置 | RPM 档位设置 |
| 支付流程 | 4 种支付方式 |

### Phase 5: Admin + 发票 (Week 5)

| 任务 | 产出 |
|------|------|
| Admin 权限细化 | super_admin 特殊权限 |
| 发票管理 | 抬头 + 地址 |
| 数据归档 | GDPR 分层 |

### Phase 6: 安全 + 部署 (Week 6)

| 任务 | 产出 |
|------|------|
| CSP + CSRF | 安全中间件 |
| Docker 构建 | 多阶段构建 |
| 云部署验证 | AWS/Aliyun |

---

## 13. 页面清单

### 13.1 公开页面

| 路由 | 模板 | 说明 |
|------|------|------|
| `/` | `landing.html` | 首页 |
| `/login` | `auth/login.html` | 登录 |
| `/register` | `auth/register.html` | 注册 + 验证码 |
| `/forgot-password` | `auth/forgot-password.html` | 忘记密码 |

### 13.2 用户页面

| 路由 | 模板 | 说明 |
|------|------|------|
| `/dashboard` | `dashboard.html` | 仪表盘 |
| `/apikeys` | `apikeys.html` | API Keys |
| `/billing` | `billing/index.html` | 计费概览 |
| `/billing/recharge` | `billing/recharge.html` | 充值 |
| `/billing/checkout/*` | `billing/checkout/*.html` | 各支付方式 |
| `/invoices` | `invoices.html` | 发票记录 |
| `/settings` | `settings.html` | 设置 |
| `/redeem` | `redeem.html` | 兑换码 |

### 13.3 Admin 页面

| 路由 | 权限 | 说明 |
|------|------|------|
| `/admin` | system_admin+ | 管理仪表盘 |
| `/admin/users` | system_admin+ | 用户管理 |
| `/admin/providers` | super_admin | 供应商管理 |
| `/admin/models` | system_admin+ | 模型管理 |
| `/admin/keypool` | system_admin+ | Key Pool |
| `/admin/subscriptions` | system_admin+ | 订阅计划 |
| `/admin/redemption` | system_admin+ | 兑换码管理 |
| `/admin/audit-logs` | system_admin+ | 审计日志 |
| `/admin/settings` | system_admin+ | 系统设置 |

---

## 14. 数据库变更清单

```sql
-- 用户表扩展
ALTER TABLE users ADD COLUMN user_type VARCHAR(20) DEFAULT 'general';
ALTER TABLE users ADD COLUMN second_email VARCHAR(100);
ALTER TABLE users ADD COLUMN created_by UUID REFERENCES users(id);

-- API Keys 表扩展
ALTER TABLE api_keys ADD COLUMN key_prefix VARCHAR(20);
ALTER TABLE api_keys ADD COLUMN model_restriction VARCHAR(100); -- 单品用户限制

-- 用量记录扩展
ALTER TABLE usage_records ADD COLUMN request_count INTEGER DEFAULT 0;

-- 订阅表
CREATE TABLE subscriptions (
  id UUID PRIMARY KEY,
  user_id UUID NOT NULL,
  plan_type VARCHAR(20), -- lite, standard, pro, max, enterprise
  request_limit INTEGER,
  token_limit INTEGER,
  status VARCHAR(20),
  expires_at TIMESTAMP,
  created_at TIMESTAMP DEFAULT NOW()
);

-- 发票表
CREATE TABLE invoice_titles (...);
CREATE TABLE delivery_addresses (...);
CREATE TABLE user_invoices (...);
```

---

**文档版本:** 3.0
**更新时间:** 2026-05-04

> **待确认事项:**
> 1. API Key 前缀策略是否满足需求？
> 2. super_admin 降级删除流程是否合理？
> 3. Token Plan / Code Plan 默认档位是否合适？
