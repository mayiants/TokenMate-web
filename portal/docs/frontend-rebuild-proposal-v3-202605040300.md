# Go Portal 前端重建方案 V3

> **ACTIVE** — 2026-05-04
> V1: `frontend-rebuild-proposal-202605040000.md` (React+Bun)
> V2: `frontend-rebuild-proposal-v2-202605040100.md` (HTMX+Alpine+Go template)
> V3: 综合 V2 共识 + 业务需求细化 + billing/invoice 设计 + 四 Agent 协作

---

## 〇、V2→V3 关键变化

| 维度 | V2 | V3 |
|------|----|----|
| 技术栈 | HTMX+Alpine+Tailwind CDN | **HTMX+Alpine+Tailwind purged (proxy)** — Tailwind 不走 CDN, 生成后 go:embed |
| 多语言 | 未涉及 | **i18n: 中/英双语言**, Go template `{{t "key"}}` |
| API Key 策略 | 简单 key | **五密钥类型分层** — 见 §4 |
| 计费 | 基础 | **Token+Request 双重统计, Token Plan/Code Plan/单品/企业** |
| 敏感词 | 未讨论 | **建议暂缓** — 见 §10 |
| 电子邮件 | 单邮箱 | **邮箱注册 + 第二邮箱选项 + UUID 唯一标识** |
| 发票 | 未涉及 | **增值税发票抬头 + 多地址管理** |
| 工作流 | 单 agent 串行 | **四 Agent 并行: Code / Test / Stress / Review** |

---

## 一、技术栈 (V3 终版)

| 层 | 技术 | 体积 | 何以入选 |
|----|------|------|----------|
| 模板引擎 | Go `html/template` + `embed` | 零 | 标准库, 自动 XSS 转义, 单二进制 |
| 交互 | HTMX 2.x | 14KB | AJAX/表单/轮询/确认 — 共识之选 |
| 复杂 UI | Alpine.js 3.x | 15KB | dropdown/modal/tab (仅 10% 页面) |
| 样式 | Tailwind CSS v3 (purged, 不走 CDN) | ~8KB | 原子化 CSS, 响应式零成本 |
| 多语言 | Go template `{{t "key"}}` + JSON 字典 | ~5KB | 服务端渲染, 零 JS 开销 |
| 图标 | Lucide SVG 内联 | ~300B/个 | 树摇好, 按需引用 |
| 图表 | Chart.js | 60KB | 仅 Dashboard 按需加载 |
| 支付 | Stripe.js / PayPal SDK | 按需 | 仅 checkout 页面动态加载 |

**首屏体积:** ~40KB gzip (HTML+CSS+HTMX+Alpine), **零外部 CDN 请求**。

### 1.1 为何不用 React/Preact/Bun

| 方案 | 排除原因 |
|------|----------|
| React SPA (V1) | 构建链引入 Node.js, 与 CLAUDE.md "default Bun" 精神一致但增加了工具链复杂度 |
| Preact 混合 (frontend-arch-1600) | 仍需要 Bun build 步骤, 且 Go 模板 + Preact 双轨维护成本高 |
| 纯静态 HTML | 用户已排除 — Playground/支付交互无法支撑 |

V3 选择 **HTMX + Alpine**: 90% 交互无需 JS, 10% 复杂 UI 由 Alpine 处理, Playground/Chart/支付 独立 JS 按需加载。

---

## 二、多语言 i18n 方案

### 2.1 HTMX + Go template 下的 i18n

两种主流做法对比:

| 方案 | 实现 | 优点 | 缺点 |
|------|------|------|------|
| **服务端渲染** (Go template) | `{{t "login.title"}}` 翻译函数 | 零 JS, SEO 友好, 无闪烁 | 每个翻译对应一次模板调用 |
| 前端 JS i18n | i18next.js 客户端库 | 切换即时 | 额外 20KB+ JS, 有闪烁 |

**V3 选择: 服务端渲染 i18n** — 因为 HTMX 每次更新都从服务端获取 HTML, 翻译自然嵌入。

### 2.2 实现

```go
// internal/web/i18n.go
package web

import (
    "encoding/json"
    "os"
)

type I18n map[string]map[string]string // lang → key → value

func LoadTranslations() (I18n, error) {
    var i18n I18n
    data, _ := os.ReadFile("web/i18n/zh.json")
    json.Unmarshal(data, &i18n)
    return i18n, nil
}

// 模板函数: {{t "login.title"}}
func (h *Handler) t(c *gin.Context, key string) string {
    lang := c.GetString("lang") // 从 cookie/header 获取
    if msg, ok := h.i18n[lang][key]; ok {
        return msg
    }
    return key
}
```

```json
// web/i18n/zh.json
{
  "login.title": "登录",
  "login.email": "邮箱地址",
  "login.password": "密码",
  "login.submit": "登 录",
  "dashboard.balance": "账户余额",
  "dashboard.activeKeys": "活跃密钥"
}
```

```json
// web/i18n/en.json
{
  "login.title": "Sign In",
  "login.email": "Email",
  "login.password": "Password",
  "login.submit": "Login",
  "dashboard.balance": "Balance",
  "dashboard.activeKeys": "Active Keys"
}
```

```html
<!-- templates/auth/login.html -->
<h2>{{t "login.title"}}</h2>
<label>{{t "login.email"}}</label>
<input type="email" ...>
<button>{{t "login.submit"}}</button>
```

**语言切换**: Header 中 `中/EN` 按钮 → 设置 cookie `lang=zh|en` → 后端读取并渲染。HTMX 请求自动携带 cookie, 无缝切换。

### 2.3 短期范围

| 语言 | 覆盖 |
|------|------|
| 中文 (zh) | 全量 — 所有页面文本 |
| 英文 (en) | 全量 — 所有页面文本 |

> 后续扩展只需添加 `web/i18n/{lang}.json` + 在 handler 中注册新语言。

---

## 三、前端安全 V3

综合 test-and-security-plan (四层矩阵) + web-frontend-architecture (CSRF+CSP) + frontend-architecture (支付独立 CSP):

### 3.1 安全中间件 (最终版)

```go
// internal/web/middleware.go
func SecurityHeaders() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
        c.Header("X-Frame-Options", "DENY")
        c.Header("X-Content-Type-Options", "nosniff")
        c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
        c.Header("Permissions-Policy",
            "accelerometer=(), camera=(), geolocation=(), gyroscope=(), microphone=(), payment=()")

        // 动态 CSP
        path := c.Request.URL.Path
        csp := "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'"
        if strings.HasPrefix(path, "/billing/checkout") {
            csp += "; script-src 'self' https://js.stripe.com https://www.paypal.com; frame-src https://js.stripe.com https://hooks.stripe.com https://www.paypal.com; connect-src 'self' https://api.stripe.com https://www.paypal.com"
        } else if strings.HasPrefix(path, "/playground") {
            csp += "; script-src 'self' 'unsafe-eval'" // Monaco 需要
        } else {
            csp += "; script-src 'self'"
        }
        c.Header("Content-Security-Policy", csp)
        c.Next()
    }
}
```

### 3.2 CSRF Token

每个 `<form>` 携带 `{{.CSRFToken}}`, POST/PUT/DELETE 验证。

### 3.3 大额操作二次确认

> ¥1000 充值 / 删除 Key / 修改角色 / 降级 super_admin → 弹出密码验证。

---

## 四、用户体系与权限设计

### 4.1 用户类型 (计费维度)

| 类型 | access_mode | 计费方式 | Key 前缀 | Dashboard 特征 |
|------|-------------|----------|----------|---------------|
| **economy** (经济) | `economy` | 从 balance 扣费, 按 token 计费, 最低优先级 upstream pool | `sk-wgit-` | 基础用量 + 余额 |
| **general** (一般) | `general` | 从 balance 扣费, 按 token 计费, 普通 upstream pool | `sk-wgit-` | 基础用量 + 余额 + 模型可选 |
| **single-model** (单品) | `single_model` | 预付费, 针对某模型, 从购买额中扣 token | `sk-wgit-` | 单品余量 + 支持模型列表 |
| **subscription** (订阅) | `token_plan` 或 `code_plan` | 预付费套餐, token plan 扣 token / code plan 扣 request | `sk-wgit-tp-` 或 `sk-wgit-cp-` | 套餐额度 + 日限额 + 到期倒计时 |
| **enterprise** (企业) | `enterprise` | 后付费, 月度账单, 高优先级 upstream pool | `sk-wgit-ent-` | 企业用量总览 + 子成员管理 |

### 4.2 用户角色 (管理维度)

| 角色 | 人数限制 | 关键权限 | 区别 |
|------|----------|----------|------|
| **super_admin** | 1-3 | 管理 system_admin 增删禁 / 管理 upstream 供应商 / 管理支付系统 | **不可直接删除**, 需降级后删除 |
| **system_admin** | ≤10 | 日常管理维护 / 计费+账单+upstream key pool / 供应商下产品管理 | — |
| **enterprise_admin** | 不限 | 查看分配的企业 user key / 用量 / 计费 / 账单 | 由 super_admin 或 system_admin 创建 |
| **user** | 不限 | 基础 dashboard / API Key / 充值 | 默认角色 |

### 4.3 super_admin 特殊规则

```
1. 至少保留 1 位 super_admin
2. 不可直接删除 super_admin 角色账户
3. 降级流程: super_admin → 申请降级 → 另一位 super_admin 确认 → 降为 system_admin → 可删除
4. super_admin 修改密码: 直接自助
5. super_admin 关键权限:
   a) 添加/删除/禁用/启用 system_admin
   b) 管理 upstream 模型供应商 (增删禁启)
   c) 供应商激活后, 旗下产品管理可由 system_admin 处理
   d) 管理和修改支付系统/接口
```

---

## 五、API Key 分层策略 (核心讨论)

### 5.1 问题

如何在同一系统中区分:
- 单品购买用户 (只应访问特定模型)
- 经济/一般用户 (可从 pool 中自选模型)
- Token Plan 用户 (套餐内模型列表)
- Code Plan 用户 (按 request 计费, 非 token)
- 企业用户 (多成员共享 pool)

### 5.2 方案: 保留单一 Key, 通过 access_mode + metadata 区分

**不推荐**每种场景生成不同类型的 Key (前缀区分) — 会导致:
- 前端校验逻辑分散
- 数据库 foreign key 关系复杂
- 用户混淆 (我一个账户为何有多把不同类型的 Key?)

**推荐: 单 Key 多属性**

```sql
CREATE TABLE api_keys (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id),
    key_hash VARCHAR(128) NOT NULL UNIQUE,
    key_prefix VARCHAR(16) NOT NULL,       -- 统一 sk-wgit- 前缀
    name VARCHAR(100),
    access_mode VARCHAR(20) NOT NULL,       -- economy / general / single_model / token_plan / code_plan / enterprise
    allowed_models TEXT[],                  -- 允许的模型列表
    allowed_providers TEXT[],               -- 允许的供应商列表
    subscription_id UUID,                   -- 关联订阅 (token_plan / code_plan 时)
    rate_limit_rpm INTEGER DEFAULT 60,
    rate_limit_tpm INTEGER DEFAULT 100000,
    balance_override DECIMAL(18,6),         -- 单品购买时的可消费额度
    status VARCHAR(20) DEFAULT 'active',    -- active / disabled / revoked
    created_at TIMESTAMP DEFAULT NOW(),
    expires_at TIMESTAMP,                   -- 订阅/单品到期
    deleted_at TIMESTAMP
);
```

**access_mode 决定计费来源:**

| access_mode | 扣费来源 | 模型限制 |
|-------------|----------|----------|
| `economy` | `users.balance` (token 价) | 经济 pool 中的模型列表 |
| `general` | `users.balance` (token 价) | 普通 pool 中的模型列表 |
| `single_model` | `api_keys.balance_override` | 单一模型 |
| `token_plan` | `user_subscriptions.quota_remaining` (token) | 套餐内模型列表 |
| `code_plan` | `user_subscriptions.request_remaining` (request) | 套餐内模型列表 |
| `enterprise` | 企业账户额度 (后付费) | 企业 pool 中的模型列表 |

**一个用户可以有多个 Key** (例如同时有 token_plan key 和 economy key), 通过 Key 的 `name` 区分。

### 5.3 为什么前缀不区分

```
统一前缀: sk-wgit-xxxxxxxx
通过 api_keys.access_mode 字段区分类型
Dashboard 按 Key 展示不同类型的使用信息

企业 Key: 由 enterprise_admin 在 dashboard 中创建
          前缀仍是 sk-wgit-, 但 access_mode=enterprise
          系统为该企业分配独立的 rate_limit 和 upstream pool
```

---

## 六、计费模型 (核心盈利逻辑)

### 6.1 双重统计: Token + Request

**每一个 user key 的消耗都必须记录两种数据:**

```sql
-- usage_records 扩展字段
ALTER TABLE usage_records ADD COLUMN request_count INTEGER DEFAULT 1;
ALTER TABLE usage_records ADD COLUMN input_tokens INTEGER DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN output_tokens INTEGER DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN cached_tokens INTEGER DEFAULT 0;       -- cache hit
ALTER TABLE usage_records ADD COLUMN cache_miss_tokens INTEGER DEFAULT 0;   -- cache miss
ALTER TABLE usage_records ADD COLUMN reasoning_tokens INTEGER DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN effective_input_tokens INTEGER DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN upstream_key_id TEXT;                   -- 上游 Key 追踪
```

### 6.2 Token Plan 计费 (按 token)

```
Token 分类:
  input_cache_hit:  $0.025 / 1M tokens  (prompt caching hit)
  input_cache_miss: $0.125 / 1M tokens  (常规输入)
  output:           $0.750 / 1M tokens  (输出)
  reasoning:        $1.500 / 1M tokens  (推理, 如 o1 系列)

折扣: admin 可在 dashboard 中为不同产品或套餐设置价格档位 ($/M token)
```

| 套餐档位 | 月费 (￥) | Token 额度/月 | 日均限额 |
|----------|-----------|--------------|----------|
| Lite | ¥39 | 60M | 3M |
| Standard | ¥99 | 200M | 10M |
| Pro | ¥399 | 1B | 50M |
| Max | ¥699 | 2B | 100M |

### 6.3 Code Plan 计费 (按 request)

```
Request 计数单位:
  rpm:   requests per minute (每分钟)
  rph:   requests per hour (每小时)
  rp5h:  requests per 5 hours (每5小时)
  rpw:   requests per week (每周)
  rpmo:  requests per month (每月总计)
```

| 档位 | 月费 (￥) | rpm | rph | rpmo |
|------|-----------|-----|-----|------|
| S | ¥60 | 60 | 500 | 18,000 |
| M | ¥150 | 120 | 1,200 | 45,000 |
| L | ¥300 | 300 | 3,000 | 90,000 |
| XL | ¥600 | 600 | 6,000 | 180,000 |
| MAX | ¥900 | 900 | 9,000 | 300,000 |

### 6.4 上游 Key 统计 (对账用)

**每个 upstream key 也需要记录 token+request 数据**, 用于与上游模型商对账:

```sql
CREATE TABLE upstream_usage_summary (
    upstream_key_id TEXT,
    provider TEXT,
    model TEXT,
    total_input_tokens BIGINT,
    total_output_tokens BIGINT,
    total_requests INTEGER,
    total_cost_usd DECIMAL(18,6),
    period_start TIMESTAMP,
    period_end TIMESTAMP
);
```

---

## 七、邮件 + 用户管理

### 7.1 注册与验证

```
注册流程:
  1. 用户输入邮箱 → POST /api/auth/send-code → 6 位数字验证码 → Redis 10min
  2. 输入验证码 + 密码 → POST /api/auth/register → 创建 UUID 用户
  3. 用户 UUID 为唯一标识, 邮箱可后续在 dashboard 中修改

邮箱修改:
  - Dashboard → Settings → 修改邮箱
  - 输入新邮箱 → 发送验证码到新邮箱 → 验证通过后更新
  - 第二邮箱: 用户可添加备用邮箱 (可选), 仅在安全通知时使用

手机: 不强求, 不收集
```

### 7.2 用户表

```sql
ALTER TABLE users ADD COLUMN second_email VARCHAR(255);
ALTER TABLE users ADD COLUMN email_verified BOOLEAN DEFAULT false;
-- UUID 已是主键, 邮箱唯一但允许修改
```

---

## 八、发票 (增值税发票) 与地址管理

### 8.1 发票类型

| 类型 | 说明 |
|------|------|
| 增值税普通发票 | 个人/小规模纳税 |
| 增值税专用发票 | 一般纳税 (需税号) |

### 8.2 数据模型

```sql
CREATE TABLE invoice_profiles (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id),
    name VARCHAR(200) NOT NULL,             -- 发票抬头名称
    tax_number VARCHAR(50),                 -- 税号 (专用发票必填)
    address VARCHAR(500),
    phone VARCHAR(20),
    bank_name VARCHAR(200),
    bank_account VARCHAR(50),
    type VARCHAR(20) DEFAULT 'normal',      -- normal / special (增值税专用)
    is_default BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE addresses (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id),
    name VARCHAR(100) NOT NULL,
    phone VARCHAR(20),
    province VARCHAR(50),
    city VARCHAR(50),
    district VARCHAR(50),
    detail VARCHAR(500),
    is_default BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT NOW()
);
```

**前端**: 用户可创建多个发票抬头和地址, 开票或寄送时从列表中选择。兑换码消费不提供发票。

---

## 九、GDPR 数据保留

| 数据阶段 | 时长 | 存储策略 |
|----------|------|----------|
| 热数据 | 1-3 月 | PostgreSQL 主表, 直接查询 |
| 温数据 | 3-12 月 | usage_records 月度分区, 按需查询 |
| 冷数据 | 1-7 年 | 归档到 S3/OSS, JSON Lines 格式, 审计时恢复 |

```sql
-- 月度分区 (自动创建)
SELECT create_monthly_partition('usage_records', '2026-06-01');
```

前端无感知 — Dashboard 默认显示热数据, Admin 审计支持查询冷数据。

---

## 十、敏感词 — V3 建议暂缓

### 10.1 技术挑战

| 挑战 | 说明 |
|------|------|
| Proxy 端 | Proxy 是流式转发的, 无法对 streaming response 做完整 tokenization 后过滤 |
| 性能开销 | 实时敏感词匹配需要额外 CPU, 影响 throughput |
| 准确性 | 中文敏感词需要专业词库 + NLP, 简单正则匹配假阳性太高 |

### 10.2 V3 建议

**暂不在前端实现敏感词模块。** 理由:
1. 技术上, Proxy 端做实时过滤会影响核心转发性能
2. 市场上独立的内容安全 API 更专业 (如阿里云内容安全)
3. 用户提到 "在中国大陆部署的模型需要实名制, 我们暂时不需要" — 同样逻辑适用敏感词

**后续可选方案**: 调用第三方内容安全 API (阿里云/腾讯云) 在 content filter service 中集中处理。

---

## 十一、前端页面清单 V3

### 公开页面

| 路由 | 模板 | i18n keys |
|------|------|-----------|
| `/` | `landing.html` 或 redirect → `/login` | hero, features |
| `/login` | `auth/login.html` | `login.*` |
| `/register` | `auth/register.html` | `register.*` |
| `/forgot-password` | `auth/forgot-password.html` | `forgot.*` |

### 用户页面 (需 JWT)

| 路由 | 模板 | 核心交互 |
|------|------|----------|
| `/dashboard` | `dashboard.html` | 余额卡片 + 用量趋势 + 快捷入口, Chart.js 按需 |
| `/apikeys` | `apikeys.html` | Key 列表 + 创建/删除, HTMX 动态表格 |
| `/billing` | `billing/usage.html` | 用量明细 (token+request) |
| `/billing/recharge` | `billing/recharge.html` | 金额选择 + 支付方式 → checkout |
| `/billing/checkout` | `checkout.html` | **独立 CSP** — 动态加载支付 SDK |
| `/orders` | `orders.html` | 订单历史 |
| `/invoices` | `invoices.html` | 发票抬头管理 + 开票申请 |
| `/profile` | `profile.html` | 修改邮箱 + 第二邮箱 + 密码 |
| `/checkin` | `checkin.html` | 签到日历, HTMX 局部更新 |
| `/playground` | `playground.html` | Phase 2 — Monaco + SSE + Markdown |

### 管理员页面

| 路由 | 管理员角色 | 模板 |
|------|-----------|------|
| `/admin` | sys/super | `admin/dashboard.html` — Chart.js 系统概览 |
| `/admin/users` | sys/super | `admin/users.html` — 用户管理 + 角色编辑 |
| `/admin/keypool` | sys | `admin/keypool.html` — 上游 Key Pool CRUD |
| `/admin/models` | sys | `admin/models.html` — 模型定价 + 状态 |
| `/admin/providers` | **super only** | `admin/providers.html` — 供应商管理 |
| `/admin/payments` | **super only** | `admin/payments.html` — 支付系统配置 |
| `/admin/redemption` | sys | `admin/redemption.html` — 兑换码管理 |
| `/admin/subscriptions` | sys | `admin/subscriptions.html` — Token/Code Plan 管理 |
| `/admin/audit-logs` | sys | `admin/audit-logs.html` — 审计日志 (7 年) |
| `/admin/invoices` | sys | `admin/invoices.html` — 发票审批 |
| `/admin/enterprise` | sys/super | `admin/enterprise.html` — 企业管理 |
| `/admin/settings` | sys | `admin/settings.html` — 系统设置 |
| `/admin/channel-health` | sys | `admin/channel-health.html` — 通道健康 |

---

## 十二、目录结构 V3

```
ai-portal-go/
├── cmd/server/main.go                     # 修改: 初始化 i18n+模板+安全+web路由
├── internal/
│   ├── config/
│   ├── db/
│   ├── middleware/
│   │   ├── auth.go                        # 不变 (已在 role_hierarchy)
│   │   └── security.go                    # 新增: CSP+HSTS+CSRF
│   ├── routes/                            # 不变: JSON API
│   └── web/                               # 新增: HTML 渲染层
│       ├── handler.go                     # Handler 结构体 + 模板引擎
│       ├── pages.go                       # 所有 GET 页面 handler
│       ├── data.go                        # DB 查询辅助
│       ├── helpers.go                     # 模板函数 (formatAmount, formatDate, t)
│       ├── i18n.go                        # 多语言加载 + 翻译函数
│       └── middleware.go                  # CSRFToken 生成/验证
├── web/                                   # 新增: 前端资源 (go:embed)
│   ├── embed.go                           # //go:embed templates/* static/*
│   ├── i18n/
│   │   ├── zh.json                        # 中文翻译
│   │   └── en.json                        # 英文翻译
│   ├── templates/
│   │   ├── layout.html                    # 基础布局 + 主题切换
│   │   ├── partials/                      # sidebar, mobile-nav, flash, pagination
│   │   ├── auth/                          # login, register, forgot
│   │   ├── dashboard.html
│   │   ├── apikeys.html
│   │   ├── billing/                       # usage, recharge
│   │   ├── checkout.html                  # 支付收银台
│   │   ├── orders.html
│   │   ├── invoices.html
│   │   ├── profile.html
│   │   ├── checkin.html
│   │   ├── playground.html
│   │   ├── admin/                         # 所有管理页面
│   │   └── errors/                        # 404, 500
│   └── static/
│       ├── css/
│       │   └── app.css                    # Tailwind purged + 主题变量
│       └── js/
│           ├── htmx.min.js                # 14KB
│           └── alpine.min.js              # 15KB (可选)
├── migrations/                            # 新增 008_invoices.sql 等
├── Dockerfile
└── go.mod
```

---

## 十三、四 Agent 协作 V3

```
┌──────────────────────────────────────────────────────────────┐
│                     主 Agent (协调)                           │
│  检查进度 / 运行测试 / 合并结果                                │
├────────────────┬──────────────────┬───────────────┬───────────┤
│   Agent 1      │    Agent 2       │   Agent 3     │  Agent 4  │
│   前端代码开发  │  单元+集成测试    │ 业务+压力测试  │  安全审查  │
└────────────────┴──────────────────┴───────────────┴───────────┘
```

### Agent 1: Code — 前端代码

| Phase | 任务 | 预计 |
|-------|------|------|
| P1 基础设施 | embed.go / layout / 主题 / 安全中间件 / 路由注册 | 4h |
| P2 认证 | login / register / forgot + CSRF + 验证码发送 | 3h |
| P3 用户 | dashboard / apikeys / billing / profile / invoices / checkin | 8h |
| P4 支付 | recharge / checkout (4种支付) / 支付独立CSP | 5h |
| P5 Admin | 全部管理页面 (12个) + Chart.js | 8h |
| P6 i18n | zh.json / en.json / 翻译函数 / 语言切换 | 2h |
| **总计** | | **~30h** |

### Agent 2: Test — 单元 + 集成

| 模块 | 测试内容 | 框架 |
|------|----------|------|
| 模板渲染 | 所有模板 Execute 成功 + 数据注入正确 | Go `testing` + `httptest` |
| Handler | GET/POST 响应码 + 鉴权 | `httptest` |
| i18n | 中/英翻译覆盖 | 比对 JSON key |
| CSRF | Token 生成 + 验证 + 过期 | Go test |
| 集成 | 注册→登录→创建Key→Dashboard→充值 (完整流程) | `httptest` |
| API 兼容 | 100+ 端点对比 Node.js Portal | 复用 test-api.ts |

### Agent 3: Stress — 业务 + 压力

| 测试 | 工具 | 指标 |
|------|------|------|
| 首屏 TTFB | wrk / k6 | < 500ms (新加坡) |
| 页面切换 | HTMX AJAX | < 200ms |
| API 并发 | wrk -c100 -t4 | > 2000 RPS |
| 支付沙箱 | Playwright | Stripe test + PayPal Sandbox |
| 移动端 | Playwright 多 viewport | 手机/平板/桌面无断点 |
| 内存 | docker stats | < 50MB |

### Agent 4: Review — 安全审查

| 类别 | 测试项 | 工具 |
|------|--------|------|
| 认证 | JWT 破解/篡改/过期 | curl |
| 授权 | 越权/IDOR/角色提升 | 脚本 |
| 输入 | SQL 注入/XSS/路径遍历 | sqlmap + 手写 |
| CSP | 所有路由 CSP header 验证 | browser DevTools |
| 支付 | PCI-DSS checklist / 金额校验 | 手动审计 |
| 依赖 | govulncheck | Go 漏洞扫描 |

---

## 十四、实施排期

```
Week 1: P1 基础设施 + P2 认证 (Agent 1)
        → Agent 2 并行测试 P1+P2
        → Agent 4 代码审查

Week 2: P3 用户页面 (Agent 1)
        → Agent 2 测试 P3
        → Agent 3 E2E 测试
        → Agent 4 安全扫描

Week 3: P4 支付 + P6 i18n (Agent 1)
        → Agent 3 支付沙箱 + 压力测试
        → Agent 4 支付安全审查

Week 4: P5 Admin 页面 (Agent 1)
        → Agent 2 集成测试
        → Agent 3 全量压力测试
        → Agent 4 最终安全审计

Week 5: 修复 + 回归 + 部署验证
```

---

## 十五、对 Go 后端影响

| 模块 | 变动 | 说明 |
|------|------|------|
| `cmd/server/main.go` | 修改 | 模板引擎 + web 路由 + 安全中间件注册 |
| `internal/routes/*.go` | **不变** | JSON API 保持独立 |
| `internal/middleware/auth.go` | **不变** | 复用 role_hierarchy |
| `internal/web/*` | **新增** | handler, pages, data, helpers, i18n, middleware |
| `web/*` | **新增** | templates, static, i18n, embed.go |
| `migrations/` | **新增** | invoice_profiles, addresses 等 |
| `dist/` | **删除** | Phase 6 — 清理 Node.js 临时构建产物 |

---

## 十六、总结

| 问题 | V3 回答 |
|------|---------|
| API Key 策略 | 单 Key 多属性 (`access_mode` 区分类型), 统一 `sk-wgit-` 前缀, enterprise 由管理员批量创建 |
| Token+Request 双重统计 | **所有 key 均记录两种数据**, 计费时根据 access_mode 选择生效方式 |
| 上游对账 | 独立 `upstream_usage_summary` 表, 不与上游账单混淆 |
| 发票 | 增值税专用/普通两种, 多抬头多地址, 兑换码不开发票 |
| 多语言 | Go template `{{t}}` 服务端渲染, 中/英双语言 |
| 敏感词 | **暂缓** — 技术成本高, 建议后续对接第三方 API |
| Tailwind CDN | **不使用 CDN** — purged 后 go:embed, ~8KB |
| 不含 Node.js | **完全零 Node.js/npm/Bun 运行时依赖** |

---

*Document Version: 3.0*

*Changelog:*
- *V1→V2: 采纳四份方案共识, React+Bun→HTMX+Go template*
- *V2→V3: 业务需求细化 (五层用户模型+双计费+i18n+发票+GDPR)+四 Agent 协作*
