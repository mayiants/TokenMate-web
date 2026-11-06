# AI Portal Go — 前端架构与业务方案 V3

> **ACTIVE / IMPLEMENTED** — Created: 2026-05-04; status updated 2026-06-11
> 整合: 5-Agent 意见稿交叉评审 + 用户业务需求逐项确认
>
> 当前状态：本文档的 8 阶段前端实施计划已在 `enterprise-cardification` 分支基本落地（模板、`go:embed`、Tailwind/HTMX/Alpine、认证、用户/企业/管理后台、支付、超级管理员、安全中间件均已在代码中实现）。敏感词过滤与 OAuth 按设计在 Phase 1 不实现；CSP/CSRF 渗透测试与 i18n 完整性作为持续项继续迭代。

---

## 目录

1. [技术栈最终确认](#一技术栈最终确认)
2. [业务需求逐项回应](#二业务需求逐项回应)
3. [数据库 Schema 关键设计](#三数据库-schema-关键设计)
4. [User Key 策略建议](#四user-key-策略建议)
5. [计费模型详细设计](#五计费模型详细设计)
6. [多语言实现方案](#六多语言实现方案)
7. [主题与响应式](#七主题与响应式)
8. [发票与地址管理](#八发票与地址管理)
9. [GDPR 数据保存策略](#九gdpr-数据保存策略)
10. [敏感词评估](#十敏感词评估)
11. [兑换码设计](#十一兑换码设计)
12. [前端页面清单（按用户类型）](#十二前端页面清单按用户类型)
13. [实施计划](#十三实施计划)
14. [四-Agent 协作计划](#十四四-agent-协作计划)

---

## 一、技术栈最终确认

### 1.1 最终决策（经第三次讨论确认）

| 层级 | 技术 | 体积 | 说明 |
|------|------|------|------|
| 模板引擎 | Go `html/template` | 0 | 标准库，自动 XSS 转义 |
| 资源嵌入 | `//go:embed` | 0 | 单二进制部署 |
| 交互 | HTMX 2.x | ~14KB | 页面导航、表单、轮询、SSE |
| 复杂交互 | Alpine.js 3.x | ~15KB | dropdown、tab、modal、主题切换 |
| 样式 | **Tailwind CSS** | ~10KB (purged) | **不用 CDN**，本地 purged 后嵌入 |
| 图标 | Lucide SVG 内联 | ~0.3KB/个 | 仅用需要的图标 |
| 图表 | Chart.js (按需) | ~60KB | 仅 Dashboard 页加载 |
| 编辑器 | Monaco (按需) | ~1MB | 仅 Playground 页加载 |
| 支付 | Stripe.js / PayPal SDK (按需) | ~40-60KB | 仅支付页加载 |
| 多语言 | Go i18n 模板函数 | ~5KB | 中/英两套翻译文件 |

**首屏加载（不含按需组件）**：~45KB gzip（HTML 15KB + CSS 10KB + HTMX 14KB + Alpine 15KB），零外部请求。

### 1.2 Tailwind 不用 CDN 的方案

```bash
# 开发时：使用 Tailwind CLI standalone（无需 Node.js）
curl -sLO https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-linux-x64
chmod +x tailwindcss-linux-x64
./tailwindcss-linux-x64 -i web/static/css/input.css -o web/static/css/app.css --minify

# 生产时：purged 后的 CSS 通过 go:embed 嵌入
```

```css
/* web/static/css/input.css */
@tailwind base;
@tailwind components;
@tailwind utilities;

@layer base {
  :root {
    --bg: #f8fafc;
    --bg-card: #ffffff;
    --text: #1e293b;
    --primary: #3b82f6;
  }
  [data-theme="dark"] {
    --bg: #0f172a;
    --bg-card: #1e293b;
    --text: #f8fafc;
    --primary: #60a5fa;
  }
}
```

---

## 二、业务需求逐项回应

### 2.1 多语言支持（中/英）

**方案：Go 模板函数 i18n**

```go
// internal/web/i18n.go
package web

var translations = map[string]map[string]string{
    "zh": {
        "dashboard.title":       "仪表盘",
        "dashboard.balance":     "余额",
        "dashboard.apikeys":     "API 密钥",
        "billing.recharge":      "充值",
        "auth.login":            "登录",
        "auth.register":         "注册",
        "auth.email":            "邮箱",
        "auth.password":         "密码",
        "auth.verify_code":      "验证码",
        "auth.send_code":        "发送验证码",
        "common.save":           "保存",
        "common.cancel":         "取消",
        "common.confirm":        "确认",
        "common.loading":        "加载中...",
    },
    "en": {
        "dashboard.title":       "Dashboard",
        "dashboard.balance":     "Balance",
        "dashboard.apikeys":     "API Keys",
        "billing.recharge":      "Recharge",
        "auth.login":            "Login",
        "auth.register":         "Register",
        "auth.email":            "Email",
        "auth.password":         "Password",
        "auth.verify_code":      "Verification Code",
        "auth.send_code":        "Send Code",
        "common.save":           "Save",
        "common.cancel":         "Cancel",
        "common.confirm":        "Confirm",
        "common.loading":        "Loading...",
    },
}

func (h *Handler) t(c *gin.Context, key string) string {
    lang := c.GetString("lang")
    if lang == "" {
        lang = "zh" // 默认中文
        // 从 Accept-Language header 检测
        accept := c.GetHeader("Accept-Language")
        if strings.Contains(accept, "en") {
            lang = "en"
        }
    }
    if tr, ok := translations[lang][key]; ok {
        return tr
    }
    return key // fallback
}
```

```html
<!-- 模板中使用 -->
<h1>{{t "dashboard.title"}}</h1>
<button class="btn-primary">{{t "common.save"}}</button>
```

**语言切换**：用户可在 Settings 中切换，保存到 `users.preferred_language` 字段，JWT claims 中携带。

### 2.2 主题系统（light / dark / 跟随系统）

```html
<!-- layout.html -->
<script>
  // 初始化主题
  (function() {
    const saved = localStorage.getItem('theme');
    const systemDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const theme = saved || (systemDark ? 'dark' : 'light');
    document.documentElement.setAttribute('data-theme', theme);
  })();
</script>

<!-- Alpine.js 主题切换 -->
<div x-data="{ theme: localStorage.getItem('theme') || 'system' }"
     x-init="$watch('theme', v => { localStorage.setItem('theme', v); document.documentElement.setAttribute('data-theme', v === 'system' ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : v) })">
  <select x-model="theme" class="form-select">
    <option value="light">{{t "theme.light"}}</option>
    <option value="dark">{{t "theme.dark"}}</option>
    <option value="system">{{t "theme.system"}}</option>
  </select>
</div>
```

### 2.3 邮箱注册与账户管理

**需求确认**：
- ✅ 仅邮件注册，邮件验证码
- ✅ Dashboard 提供第二邮箱选项
- ✅ 不强求手机
- ✅ 数据库 UUID 唯一化
- ✅ 用户可在后台更改邮箱

**数据库扩展**：

```sql
-- users 表扩展
ALTER TABLE users ADD COLUMN IF NOT EXISTS secondary_email VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMP;
ALTER TABLE users ADD COLUMN IF NOT EXISTS secondary_email_verified_at TIMESTAMP;
ALTER TABLE users ADD COLUMN IF NOT EXISTS preferred_language VARCHAR(10) DEFAULT 'zh';
ALTER TABLE users ADD COLUMN IF NOT EXISTS theme_preference VARCHAR(10) DEFAULT 'system';

-- 邮箱更改记录（安全审计）
CREATE TABLE IF NOT EXISTS email_change_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    old_email VARCHAR(255) NOT NULL,
    new_email VARCHAR(255) NOT NULL,
    changed_at TIMESTAMP DEFAULT NOW(),
    ip_address INET,
    user_agent TEXT
);
```

**流程**：
1. 注册：输入邮箱 → 发送验证码 → 验证 → 设置密码 → 完成
2. 改邮箱：输入新邮箱 → 发送验证码 → 验证 → 更新 → 记录日志
3. 第二邮箱：在 Settings 中添加 → 发送验证 → 验证通过后可设为通知邮箱

### 2.4 用户权限设计（4 级）

**权限矩阵**：

| 功能 | user | enterprise_admin | system_admin | super_admin |
|------|------|-----------------|--------------|-------------|
| 查看自己的 Dashboard | ✅ | ✅ | ✅ | ✅ |
| 管理自己的 API Keys | ✅ | ✅ | ✅ | ✅ |
| 查看企业用户的 Key/用量 | ❌ | ✅(本企业) | ✅(全部) | ✅(全部) |
| 管理企业用户 | ❌ | ✅(本企业) | ✅ | ✅ |
| 查看 Billing/充值 | ✅ | ✅ | ✅ | ✅ |
| 管理 upstream providers | ❌ | ❌ | ✅ | ✅ |
| 添加/删除 system_admin | ❌ | ❌ | ❌ | ✅ |
| 管理支付系统/接口 | ❌ | ❌ | ❌ | ✅ |
| 审计日志 | ❌ | ❌ | ✅ | ✅ |
| 系统设置 | ❌ | ❌ | ✅ | ✅ |
| 兑换码管理 | ❌ | ❌ | ✅ | ✅ |
| 订阅计划管理 | ❌ | ❌ | ✅ | ✅ |

**super_admin 特殊规则**：
- 至少 1 位，最多 3 位
- super_admin 不可被删除（需先降级为 system_admin）
- 降级需另一位 super_admin 确认
- super_admin 可修改自己的密码

```go
// internal/middleware/auth.go 扩展
func (a *Auth) RequireSuperAdmin() gin.HandlerFunc {
    return a.requireRole("super_admin")
}

func (a *Auth) CanModifySuperAdmin(currentRole string) bool {
    return currentRole == "super_admin"
}

func (a *Auth) CanDemoteSuperAdmin(requesterRole, targetRole string) bool {
    return requesterRole == "super_admin" && targetRole == "super_admin"
}
```

### 2.5 用户类型与购买选择（5 种）

**用户类型定义**：

| 类型 | 标识 | 计费方式 | 模型选择 | 付费模式 | 目标用户 |
|------|------|----------|----------|----------|----------|
| **一般型** | `general` | 按 Token | general key pool | 预存余额，按量扣 | 普通用户 |
| **经济型** | `economy` | 按 Token | economy 用户组 | 预存余额，按组折扣 | 价格敏感用户 |
| **单品型** | `single_model` | 按 Token | 单一绑定模型 | 预付费，按次购买 | 特定模型需求 |
| **订阅型** | `subscription` | Token Plan / Code Plan | 套餐限定模型 | 月付订阅 | 高频用户 |
| **企业型** | `enterprise` | 按 Token | 企业 key pool（高优先级） | 后付费，月度结算 | 企业客户 |

**用户类型与 role 的关系**：
- `user` role 可以是 economy / general / single_model / subscription
- `enterprise_admin` role 对应 enterprise 类型
- `system_admin` / `super_admin` 是平台管理员

**Economy 用户组设计**（重要）：
- Economy 不是单一类型，而是**"用户组"**概念
- Admin 可创建多个 economy 组（eco-1, eco-2...），每组包含不同模型
- 每组独立设置：折扣率、可用模型、RPM 限制（如 30rpm vs general 的 60rpm）
- 用户可在 Dashboard 自由切换 economy 组或回到 general 状态
- **编码默认折扣率：85 折（0.85）**

```sql
-- users 表增加 user_type 和 economy_group_id
ALTER TABLE users ADD COLUMN IF NOT EXISTS user_type VARCHAR(20) DEFAULT 'general'
    CHECK (user_type IN ('economy', 'general', 'single_model', 'subscription', 'enterprise'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS economy_group_id UUID; -- 关联 economy 组
```

---

## 三、数据库 Schema 关键设计

### 3.1 新增/扩展表

```sql
-- ========== 用户表扩展 ==========
ALTER TABLE users ADD COLUMN IF NOT EXISTS secondary_email VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMP;
ALTER TABLE users ADD COLUMN IF NOT EXISTS secondary_email_verified_at TIMESTAMP;
ALTER TABLE users ADD COLUMN IF NOT EXISTS preferred_language VARCHAR(10) DEFAULT 'zh';
ALTER TABLE users ADD COLUMN IF NOT EXISTS theme_preference VARCHAR(10) DEFAULT 'system';
ALTER TABLE users ADD COLUMN IF NOT EXISTS user_type VARCHAR(20) DEFAULT 'general';
ALTER TABLE users ADD COLUMN IF NOT EXISTS enterprise_id UUID; -- 关联企业

-- ========== 邮箱更改日志 ==========
CREATE TABLE IF NOT EXISTS email_change_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    old_email VARCHAR(255) NOT NULL,
    new_email VARCHAR(255) NOT NULL,
    changed_at TIMESTAMP DEFAULT NOW(),
    ip_address INET,
    user_agent TEXT
);

-- ========== 订阅计划表 ==========
CREATE TABLE IF NOT EXISTS subscription_plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(50) NOT NULL,           -- lite/standard/pro/max
    plan_type VARCHAR(20) NOT NULL       CHECK (plan_type IN ('token', 'code')),
    display_name VARCHAR(100),
    description TEXT,
    price_cents INTEGER NOT NULL,        -- 月费（分）
    currency VARCHAR(3) DEFAULT 'CNY',
    -- Token Plan 配置
    quota_total BIGINT,                  -- 月 Token 额度
    daily_limit BIGINT,                  -- 每日上限
    -- Code Plan 配置
    rpm INTEGER,                         -- 每分钟请求数
    rph INTEGER,                         -- 每小时
    rp5h INTEGER,                        -- 每5小时
    rpw INTEGER,                         -- 每周
    rpm_monthly INTEGER,                 -- 每月总请求数
    -- 通用
    allowed_models TEXT[],               -- 限定模型列表
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- 默认计划
INSERT INTO subscription_plans (name, plan_type, display_name, price_cents, quota_total, daily_limit)
VALUES
    ('lite', 'token', 'Lite', 3900, 60000000, 2000000),
    ('standard', 'token', 'Standard', 9900, 2000000000, 66666667),
    ('pro', 'token', 'Pro', 39900, 10000000000, 333333333),
    ('max', 'token', 'Max', 69900, 20000000000, 666666667)
ON CONFLICT DO NOTHING;

-- ========== 用户订阅表 ==========
CREATE TABLE IF NOT EXISTS user_subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    plan_id UUID NOT NULL REFERENCES subscription_plans(id),
    status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'paused', 'expired', 'cancelled')),
    started_at TIMESTAMP DEFAULT NOW(),
    expires_at TIMESTAMP NOT NULL,
    auto_renew BOOLEAN DEFAULT false,
    -- Token Plan 用量
    quota_total BIGINT,
    quota_used BIGINT DEFAULT 0,
    quota_remaining BIGINT,
    daily_quota_used BIGINT DEFAULT 0,
    daily_quota_reset TIMESTAMP,
    -- Code Plan 用量
    requests_used INTEGER DEFAULT 0,
    requests_remaining INTEGER,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- ========== 单品购买记录 ==========
CREATE TABLE IF NOT EXISTS single_model_purchases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    model VARCHAR(100) NOT NULL,         -- 绑定的模型
    provider VARCHAR(50) NOT NULL,
    amount_cents INTEGER NOT NULL,       -- 购买金额
    balance_cents INTEGER NOT NULL,      -- 剩余金额
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMP DEFAULT NOW(),
    expires_at TIMESTAMP                 -- 可选过期时间
);

-- ========== Economy 用户组配置 ==========
CREATE TABLE IF NOT EXISTS user_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,          -- 组名（如 eco-1, eco-2）
    group_type VARCHAR(20) DEFAULT 'economy' CHECK (group_type IN ('economy', 'general', 'enterprise')),
    discount_rate DECIMAL(3,2) DEFAULT 0.85, -- 折扣率
    rpm_limit INTEGER DEFAULT 30,        -- RPM 限制
    allowed_models TEXT[],               -- 允许的模型列表
    description TEXT,
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id), -- admin 创建者
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- 默认 economy 组
INSERT INTO user_groups (name, group_type, discount_rate, rpm_limit, allowed_models, description)
VALUES
    ('eco-default', 'economy', 0.85, 30, 
     ARRAY['gpt-4o', 'claude-3-sonnet', 'gemini-pro'], 
     '默认经济组：85折，30rpm')
ON CONFLICT DO NOTHING;

-- ========== 发票表 ==========
CREATE TABLE IF NOT EXISTS invoices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    order_id UUID REFERENCES orders(id),
    amount_cents INTEGER NOT NULL,
    title VARCHAR(255) NOT NULL,         -- 发票抬头
    tax_number VARCHAR(50),              -- 税号
    address_id UUID,                     -- 关联地址
    status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'rejected')),
    invoice_type VARCHAR(20) DEFAULT 'vat' CHECK (invoice_type IN ('vat', 'receipt')),
    created_at TIMESTAMP DEFAULT NOW(),
    processed_at TIMESTAMP
);

-- ========== 地址表 ==========
CREATE TABLE IF NOT EXISTS addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    name VARCHAR(100),                   -- 收件人
    phone VARCHAR(20),
    country VARCHAR(50),
    province VARCHAR(50),
    city VARCHAR(50),
    district VARCHAR(50),
    address_line TEXT,
    zip_code VARCHAR(20),
    is_default BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT NOW()
);

-- ========== 兑换码表 ==========
CREATE TABLE IF NOT EXISTS redemption_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(24) UNIQUE NOT NULL,    -- WG-xxxx 格式，16-20 字符
    name VARCHAR(100),
    description TEXT,
    code_type VARCHAR(20) DEFAULT 'balance' CHECK (code_type IN ('balance', 'token_plan', 'code_plan')),
    -- balance 类型
    amount_cents INTEGER,
    -- plan 类型
    plan_id UUID REFERENCES subscription_plans(id),
    duration_days INTEGER,
    -- 通用
    max_uses INTEGER DEFAULT 1,
    used_count INTEGER DEFAULT 0,
    status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'expired')),
    expires_at TIMESTAMP,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW()
);

-- ========== 兑换记录表 ==========
CREATE TABLE IF NOT EXISTS redemption_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code_id UUID NOT NULL REFERENCES redemption_codes(id),
    user_id UUID NOT NULL REFERENCES users(id),
    amount_cents INTEGER,
    balance_before INTEGER,
    balance_after INTEGER,
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMP DEFAULT NOW()
);

-- ========== usage_records 扩展（Token + Request 双重统计）==========
ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS request_count INTEGER DEFAULT 1;
ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS cached_input_tokens INTEGER DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS cache_cost DECIMAL(18,10) DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS effective_input_tokens INTEGER DEFAULT 0;
ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS reasoning_tokens INTEGER DEFAULT 0;

-- ========== 数据分区（GDPR 7年策略）==========
-- 热数据：1-3 个月（主表）
-- 温数据：3-12 个月（按月度分区）
-- 冷数据：1-7 年（按年度归档表）
CREATE TABLE IF NOT EXISTS usage_records_archive (
    LIKE usage_records INCLUDING ALL
) PARTITION BY RANGE (created_at);

-- 年度分区示例
CREATE TABLE IF NOT EXISTS usage_records_2026 
    PARTITION OF usage_records_archive
    FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');

-- 冷数据申请查询表
CREATE TABLE IF NOT EXISTS cold_data_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'rejected')),
    download_url TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    completed_at TIMESTAMP
);
```

---

## 四、User Key 策略建议

### 4.1 问题分析

用户提出的困惑：
- 企业用户：企业管理员批量创建 user key
- Token/Code Plan：sk-wgit-tp-xxxx / sk-wgit-cp-xxxx
- 通用：sk-wgit-xxxx
- 单品用户：如何区分？

### 4.2 建议方案：**统一前缀 + 元数据标识**

**不推荐为每种类型生成分离前缀**（太复杂，proxy 端需要维护大量验证逻辑）。

**最终方案（3 种 Key 格式）**：

```
Key 格式（仅 3 种）:

1. sk-wgit-{random}
   → 普通用户（general）和 economy 用户共用
   → 从 balance 扣费（economy 用户在套餐额度内享受折扣）

2. sk-wgit-tp-{random}
   → Token Plan / Code Plan 订阅用户
   → 从套餐额度扣费，用完停止服务（不 fallback）

3. sk-wgit-en-{random}
   → 企业用户
   → 后付费，仅统计用量

示例:
  sk-wgit-a1b2c3d4e5f6       (普通/economy 用户)
  sk-wgit-tp-x9y8z7w6v5u4    (Token Plan / Code Plan)
  sk-wgit-en-corp01-xyz789   (企业用户)
```

**proxy 端验证逻辑**：

```go
// proxy 验证 key 时，解析前缀确定 key 类型（仅 3 种格式）
func ParseKeyType(key string) (string, error) {
    if !strings.HasPrefix(key, "sk-wgit-") {
        return "", errors.New("invalid key format")
    }
    
    remainder := strings.TrimPrefix(key, "sk-wgit-")
    
    if strings.HasPrefix(remainder, "en-") {
        return "enterprise", nil
    }
    if strings.HasPrefix(remainder, "tp-") {
        return "subscription", nil
    }
    // sk-wgit-xxxx → 普通用户（general / economy / single_model）
    return "general", nil
}

// 根据 key 类型决定可用模型和计费方式
func (v *KeyValidator) Validate(ctx context.Context, key string) (*KeyInfo, error) {
    keyType, _ := ParseKeyType(key)
    
    switch keyType {
    case "general":
        // 查询用户类型和 model 绑定情况
        userType := v.getUserTypeByKey(ctx, key)
        requestedModel := v.getRequestedModel(ctx)
        
        switch userType {
        case "economy":
            return v.validateEconomy(ctx, key)
        case "single_model":
            // single_model 用户：检查是否请求了绑定的模型
            purchase := v.getActiveSingleModelPurchase(ctx, key, requestedModel)
            if purchase != nil && purchase.BalanceCents > 0 {
                // 使用单品余额（优先）
                return v.validateSingleModel(ctx, key, purchase)
            }
            // 单品余额用完 或 未请求绑定模型 → fallback 到 general balance
            return v.validateStandard(ctx, key)
        default:
            // 标准用户：查询 users.balance
            return v.validateStandard(ctx, key)
        }
    case "subscription":
        // 查询 user_subscriptions 额度
        return v.validateSubscription(ctx, key)
    case "enterprise":
        // 查询 enterprise 配额（后付费）
        return v.validateEnterprise(ctx, key)
    default:
        return nil, errors.New("unknown key type")
    }
}
```

**portal 端创建 key 时**：

```go
// 根据用户类型生成 key（仅 3 种格式）
func GenerateAPIKey(userType string) string {
    random := generateRandomString(24)
    
    switch userType {
    case "enterprise":
        return fmt.Sprintf("sk-wgit-en-%s", random)
    case "subscription":
        return fmt.Sprintf("sk-wgit-tp-%s", random)
    default:
        // general 和 economy 共用 sk-wgit-xxxx 格式
        return fmt.Sprintf("sk-wgit-%s", random)
    }
}
```

### 4.3 Key 与模型的关系

| Key 类型 | 模型限制 | 计费来源 |
|----------|----------|----------|
| `sk-wgit-xxxx` (general) | 全部模型（标准价） | `users.balance` |
| `sk-wgit-xxxx` (economy) | economy key pool（折扣价） | economy 套餐额度 → 用完恢复 standard |
| `sk-wgit-tp-xxxx` | 套餐限定模型列表 | `user_subscriptions.quota_remaining` / `requests_remaining` |
| `sk-wgit-en-xxxx` | 企业 key pool（高优先级） | 企业后付费账单 |

**注意**：economy 不是独立 key 类型，而是普通 key 用户的一种"折扣套餐"状态。

---

## 五、计费模型详细设计

### 5.1 核心原则

**所有 user key 消耗必须同时记录 token + request 两种数据**，计费时选择其中一种生效。

### 5.2 计费公式（Go 实现）

```go
// pkg/billing/calculator.go
package billing

// CalculateCost 计算单次请求费用
func (b *Billing) CalculateCost(usage *dto.Usage, model string, provider string, userType string) (*CostBreakdown, error) {
    pricing := b.getModelPricing(model, provider)
    if pricing == nil {
        return nil, fmt.Errorf("pricing not found for %s/%s", model, provider)
    }
    
    // 经济型用户享受折扣
    conversionRate := 1.0
    if userType == "economy" {
        conversionRate = 0.85 // 85折
    }
    
    // 有效输入 = 总输入 - 缓存命中
    effectiveInput := usage.PromptTokens - usage.PromptCacheHitTokens
    
    // 基础费用（按 token）
    baseCost := conversionRate * pricing.ModelRatio * pricing.PromptRatio *
        float64(effectiveInput + usage.CompletionTokens*int(pricing.CompletionRatio)) / 1_000_000
    
    // 缓存费用（按 token，折扣率）
    cacheCost := conversionRate * pricing.ModelRatio * pricing.PromptRatio *
        float64(usage.PromptCacheHitTokens) * b.cacheRatios.ReadRatio / 1_000_000
    
    // Reasoning 费用（如有）
    reasoningCost := 0.0
    if usage.ReasoningTokens > 0 {
        reasoningCost = conversionRate * pricing.ModelRatio * pricing.PromptRatio *
            float64(usage.ReasoningTokens) / 1_000_000
    }
    
    totalCost := baseCost + cacheCost + reasoningCost
    
    return &CostBreakdown{
        BaseCost:      baseCost,
        CacheCost:     cacheCost,
        ReasoningCost: reasoningCost,
        TotalCost:     totalCost,
        InputTokens:   usage.PromptTokens,
        OutputTokens:  usage.CompletionTokens,
        CachedTokens:  usage.PromptCacheHitTokens,
        ReasoningTokens: usage.ReasoningTokens,
        RequestCount:  1,
    }, nil
}
```

### 5.3 不同用户类型的扣费逻辑（最终确认）

**核心原则：**
- **subscription（tp key）**：用完停止，**不 fallback**
- **single_model / economy**：单品/套餐余额用完 → **fallback 到 users.balance**
- **general**：直接从 balance 扣费

```go
// internal/routes/webhook.go —— 修正后扣费逻辑

func (r *WebhookRoutes) processUsageRecord(record *UsageRecord) error {
    ctx := context.Background()
    
    // 根据 api_key_hash 查询 key 关联的用户类型和 model
    var userType, requestedModel string
    err := r.db.QueryRow(ctx, `
        SELECT u.user_type, ak.allowed_models->>0 as model
        FROM api_keys ak JOIN users u ON ak.user_id = u.id
        WHERE ak.key_hash = $1`,
        record.APIKeyHash,
    ).Scan(&userType, &requestedModel)
    
    switch userType {
    case "general":
        // 标准用户：从 users.balance 按标准价扣费
        cost := r.billing.CalculateStandardCost(record)
        return r.deductFromBalance(ctx, record.UserID, cost)
        
    case "economy":
        // Economy 用户：查询用户所属的 economy 组配置
        group := r.getUserEconomyGroup(ctx, record.UserID)
        if group != nil && group.IsActive {
            // 按 economy 组折扣率从 balance 扣费
            cost := r.billing.CalculateEconomyCost(record, group.DiscountRate)
            return r.deductFromBalance(ctx, record.UserID, cost)
        }
        // economy 组无效 → 按标准价从 balance 扣费
        cost := r.billing.CalculateStandardCost(record)
        return r.deductFromBalance(ctx, record.UserID, cost)
        
    case "single_model":
        // Single Model 用户：优先从单品余额扣费
        purchase, err := r.getActiveSingleModelPurchase(ctx, record.UserID, record.Model)
        if err == nil && purchase != nil && purchase.BalanceCents > 0 {
            // 按单品价从 single_model_purchases 扣费
            cost := r.billing.CalculateStandardCost(record)
            return r.deductFromSingleModel(ctx, purchase.ID, cost)
        }
        // 单品余额用完 或 请求了未绑定模型 → fallback 到 balance
        cost := r.billing.CalculateStandardCost(record)
        return r.deductFromBalance(ctx, record.UserID, cost)
        
    case "subscription":
        // Token Plan：从 quota_remaining 扣 token 额度
        if record.SubscriptionType == "token" {
            tokens := record.InputTokens + record.OutputTokens
            return r.deductFromTokenPlan(ctx, record.UserID, record.SubscriptionID, tokens)
        }
        // Code Plan：从 requests_remaining 扣请求额度
        if record.SubscriptionType == "code" {
            return r.deductFromCodePlan(ctx, record.UserID, record.SubscriptionID, 1)
        }
        
    case "enterprise":
        // 企业用户：仅记录用量，不实时扣费
        return r.recordEnterpriseUsage(ctx, record.EnterpriseID, record)
    }
    
    return nil
}
```

### 5.4 自动续费逻辑

**Subscription（tp key）自动续费**：

```go
// internal/jobs/subscription.go
func AutoRenewSubscriptions(ctx context.Context, db *db.DB, mail *mail.Sender) error {
    // 查询 3 天内到期的活跃订阅
    rows, _ := db.Pool.Query(ctx, `
        SELECT id, user_id, plan_id, expires_at, auto_renew
        FROM user_subscriptions
        WHERE status = 'active'
          AND expires_at <= NOW() + INTERVAL '3 days'
          AND expires_at > NOW()
    `)
    
    for rows.Next() {
        var sub UserSubscription
        rows.Scan(&sub.ID, &sub.UserID, &sub.PlanID, &sub.ExpiresAt, &sub.AutoRenew)
        
        user := getUser(ctx, db, sub.UserID)
        plan := getPlan(ctx, db, sub.PlanID)
        
        daysUntilExpiry := int(sub.ExpiresAt.Sub(time.Now()).Hours() / 24)
        
        if daysUntilExpiry == 3 {
            // 提前 3 天发送提醒（邮件 + 短信，如用户已添加手机）
            if sub.AutoRenew {
                // 检查余额是否充足
                if user.Balance >= plan.PriceCents {
                    // 自动扣费续订
                    deductBalance(ctx, db, sub.UserID, plan.PriceCents)
                    extendSubscription(ctx, db, sub.ID, 30)
                    mail.Send(ctx, user.Email, "subscription_renewed", plan)
                    if user.Phone != "" {
                        sms.Send(user.Phone, fmt.Sprintf("【WOODGIT】您的%s套餐已自动续订，扣费¥%.2f。", plan.Name, float64(plan.PriceCents)/100))
                    }
                } else {
                    // 余额不足，发送提醒
                    mail.Send(ctx, user.Email, "subscription_renew_insufficient", plan)
                    if user.Phone != "" {
                        sms.Send(user.Phone, "【WOODGIT】您的订阅即将到期，余额不足，请充值。")
                    }
                }
            } else {
                // 未开启自动续费，发送到期提醒
                mail.Send(ctx, user.Email, "subscription_expiring", plan)
                if user.Phone != "" {
                    sms.Send(user.Phone, "【WOODGIT】您的订阅将在 3 天后到期，如需继续使用请续订。")
                }
            }
        }
    }
    
    return nil
}
```

**Economy 套餐提醒**：

```go
func CheckEconomyPackages(ctx context.Context, db *db.DB, mail *mail.Sender) error {
    // economy 套餐到期前/额度将满时邮件提醒
    // 不自动续费，不自动扣款
    rows, _ := db.Pool.Query(ctx, `
        SELECT id, user_id, remaining_cents, expires_at
        FROM economy_packages
        WHERE status = 'active'
          AND (remaining_cents < total_cents * 0.2 OR expires_at <= NOW() + INTERVAL '3 days')
    `)
    
    for rows.Next() {
        var pkg EconomyPackage
        rows.Scan(&pkg.ID, &pkg.UserID, &pkg.RemainingCents, &pkg.ExpiresAt)
        
        user := getUser(ctx, db, pkg.UserID)
        
        // 发送提醒邮件
        mail.Send(ctx, user.Email, "economy_package_expiring", pkg)
        
        // economy 用完自动解除
        if pkg.RemainingCents <= 0 {
            db.Pool.Exec(ctx, `
                UPDATE economy_packages SET status = 'expired', updated_at = NOW() WHERE id = $1
            `, pkg.ID)
            // 恢复用户为 general 类型
            db.Pool.Exec(ctx, `
                UPDATE users SET user_type = 'general' WHERE id = $1 AND user_type = 'economy'
            `, pkg.UserID)
        }
    }
    
    return nil
}
```

### 5.5 企业用户额度告警与月结

**企业用户配置**（admin 创建时设置）：
- 企业名称、联系人
- **Quota 额度**（约定额度）
- **服务中止额度**（通常为约定额度的 110%）
- 允许的模型列表
- 强制使用同一**企业域名邮箱**

**额度告警**：80% / 95% / 99%

```go
func CheckEnterpriseQuota(ctx context.Context, db *db.DB, mail *mail.Sender) error {
    thresholds := []struct{ percent float64; label string }{
        {0.80, "warning"},
        {0.95, "critical"},
        {0.99, "urgent"},
    }
    
    for _, t := range thresholds {
        rows, _ := db.Pool.Query(ctx, `
            SELECT e.id, e.user_id, e.quota_total, e.quota_used, e.shutdown_quota,
                   u.email, u.phone, u.name
            FROM enterprise_quotas e
            JOIN users u ON e.user_id = u.id
            WHERE e.quota_used >= e.quota_total * $1
              AND e.quota_used < e.quota_total * ($1 + 0.01)
              AND e.status = 'active'
        `, t.percent)
        
        for rows.Next() {
            var q EnterpriseQuota
            rows.Scan(&q.ID, &q.UserID, &q.QuotaTotal, &q.QuotaUsed, &q.ShutdownQuota, &q.Email, &q.Phone, &q.Name)
            
            percent := int(t.percent * 100)
            
            // 邮件通知企业管理员
            mail.Send(ctx, q.Email, "enterprise_quota_alert", map[string]any{
                "percent": percent,
                "used": q.QuotaUsed,
                "total": q.QuotaTotal,
                "shutdown": q.ShutdownQuota,
            })
            
            // 短信通知（如有手机号）
            if q.Phone != "" {
                sms.Send(q.Phone, fmt.Sprintf("【WOODGIT】您的企业账户额度已使用 %d%%，请及时处理。", percent))
            }
        }
    }
    
    return nil
}
```

**服务中止检查**：
```go
func CheckEnterpriseShutdown(ctx context.Context, db *db.DB) error {
    // 达到 shutdown_quota 后停止服务
    _, err := db.Pool.Exec(ctx, `
        UPDATE enterprise_quotas
        SET status = 'shutdown',
            shutdown_at = NOW()
        WHERE status = 'active'
          AND quota_used >= shutdown_quota
    `)
    return err
}
```

**月度结算**：
- 自然月月结
- 不满一个月按实际天数计费
- 每月 1 日生成上月账单，发送给企业管理员

### 5.6 扣费优先级（单 Key 内）

对于 `sk-wgit-xxxx` key（普通/economy 用户）：

```
扣费优先级:
  1. economy 套餐额度（如用户购买了 economy 套餐且额度充足）→ 按折扣价扣
  2. 兑换码余额（如存在）→ 先扣兑换码
  3. 用户余额（users.balance）→ 按标准价扣
  4. 余额不足 → 拒绝请求
```

**注意**：`tp` key 和 `gn` key 是独立的，不存在跨 key fallback。

---

## 六、多语言实现方案

### 6.1 翻译文件组织

```
web/translations/
├── zh.json          # 中文
├── en.json          # 英文
└── i18n.go          # 加载逻辑
```

```json
// web/translations/zh.json
{
  "auth": {
    "login_title": "登录",
    "register_title": "注册",
    "email_placeholder": "请输入邮箱",
    "password_placeholder": "请输入密码",
    "verify_code_placeholder": "请输入验证码",
    "send_code": "发送验证码",
    "resend_code": "重新发送",
    "forgot_password": "忘记密码？",
    "no_account": "还没有账号？",
    "has_account": "已有账号？"
  },
  "dashboard": {
    "title": "仪表盘",
    "balance": "余额",
    "api_keys": "API 密钥",
    "usage_today": "今日用量",
    "usage_month": "本月用量",
    "recent_activity": "最近活动"
  },
  "billing": {
    "recharge": "充值",
    "orders": "订单记录",
    "invoices": "发票管理",
    "amount": "金额",
    "payment_method": "支付方式",
    "alipay": "支付宝",
    "wechat": "微信支付",
    "stripe": "信用卡",
    "paypal": "PayPal"
  },
  "admin": {
    "users": "用户管理",
    "keypool": "密钥池",
    "providers": "供应商",
    "models": "模型管理",
    "subscriptions": "订阅管理",
    "redemption": "兑换码",
    "audit_logs": "审计日志",
    "settings": "系统设置"
  },
  "common": {
    "save": "保存",
    "cancel": "取消",
    "confirm": "确认",
    "delete": "删除",
    "edit": "编辑",
    "create": "创建",
    "search": "搜索",
    "loading": "加载中...",
    "no_data": "暂无数据",
    "success": "操作成功",
    "error": "操作失败"
  }
}
```

### 6.2 模板中使用

```html
<!-- 通过模板函数访问 -->
<h1>{{i18n "dashboard.title" .Lang}}</h1>
<p>{{i18n "dashboard.balance" .Lang}}: ¥{{.Balance}}</p>
<button class="btn-primary">{{i18n "common.save" .Lang}}</button>

<!-- Alpine.js 中通过 data 属性 -->
<div x-data="{ lang: '{{.Lang}}' }"
     x-text="translations[lang]['common.save']">
</div>
```

### 6.3 语言切换 UI

```html
<!-- Settings 页面 -->
<div class="form-group">
  <label>{{i18n "settings.language" .Lang}}</label>
  <select name="language" class="form-select"
          hx-post="/api/profile/language"
          hx-target="#lang-result"
          x-data
          @change="document.documentElement.lang = $event.target.value">
    <option value="zh" {{if eq .Lang "zh"}}selected{{end}}>简体中文</option>
    <option value="en" {{if eq .Lang "en"}}selected{{end}}>English</option>
  </select>
  <span id="lang-result" class="text-sm text-green-600"></span>
</div>
```

---

## 七、主题与响应式

### 7.1 主题系统（light / dark / system）

已在前述 V2 中详细设计，V3 确认不变：
- CSS 变量定义两套颜色
- Alpine.js 管理切换逻辑
- localStorage 持久化
- 监听 `prefers-color-scheme`（system 模式）

### 7.2 响应式断点

```css
/* Tailwind 默认断点 */
sm: 640px   /* 大手机 */
md: 768px   /* 平板 */
lg: 1024px  /* 笔记本 */
xl: 1280px  /* 桌面 */
```

### 7.3 移动端底部导航

```html
<!-- mobile-nav.html -->
<nav class="lg:hidden fixed bottom-0 left-0 right-0 bg-white dark:bg-slate-900 border-t border-gray-200 dark:border-slate-700 z-50">
  <div class="flex justify-around py-2">
    <a href="/dashboard" class="flex flex-col items-center p-2 text-gray-600 dark:text-gray-400 hover:text-primary">
      <svg class="w-6 h-6" data-lucide="home"></svg>
      <span class="text-xs mt-1">{{i18n "nav.dashboard" .Lang}}</span>
    </a>
    <a href="/apikeys" class="flex flex-col items-center p-2 text-gray-600 dark:text-gray-400 hover:text-primary">
      <svg class="w-6 h-6" data-lucide="key"></svg>
      <span class="text-xs mt-1">{{i18n "nav.apikeys" .Lang}}</span>
    </a>
    <a href="/billing" class="flex flex-col items-center p-2 text-gray-600 dark:text-gray-400 hover:text-primary">
      <svg class="w-6 h-6" data-lucide="credit-card"></svg>
      <span class="text-xs mt-1">{{i18n "nav.billing" .Lang}}</span>
    </a>
    <a href="/settings" class="flex flex-col items-center p-2 text-gray-600 dark:text-gray-400 hover:text-primary">
      <svg class="w-6 h-6" data-lucide="settings"></svg>
      <span class="text-xs mt-1">{{i18n "nav.settings" .Lang}}</span>
    </a>
  </div>
</nav>
```

---

## 八、发票与地址管理

### 8.1 发票管理

**功能**：
- 用户可创建多个发票抬头
- 开票时选择抬头和寄送地址
- 支持增值税发票和普通收据

**流程**：
```
1. 用户在 Settings → 发票管理 中添加抬头
2. 提交开票申请（选择订单 + 抬头 + 地址）
3. Admin 在后台审核
4. 审核通过后，电子发票发送到用户邮箱
5. 如需纸质发票，使用选择的地址寄送
```

### 8.2 地址管理

```html
<!-- addresses.html -->
<div class="space-y-4">
  {{range .Addresses}}
  <div class="card p-4 {{if .IsDefault}}border-primary{{end}}">
    <div class="flex justify-between">
      <div>
        <p class="font-medium">{{.Name}} {{if .IsDefault}}<span class="badge badge-primary">默认</span>{{end}}</p>
        <p class="text-sm text-gray-500">{{.Phone}}</p>
        <p class="text-sm">{{.Province}} {{.City}} {{.District}} {{.AddressLine}}</p>
        <p class="text-sm text-gray-500">{{.ZipCode}}</p>
      </div>
      <div class="space-x-2">
        <button hx-get="/settings/addresses/{{.ID}}/edit"
                hx-target="#address-form"
                class="btn-sm btn-ghost">{{i18n "common.edit" $.Lang}}</button>
        <button hx-delete="/api/settings/addresses/{{.ID}}"
                hx-confirm="{{i18n "confirm.delete_address" $.Lang}}"
                class="btn-sm btn-danger">{{i18n "common.delete" $.Lang}}</button>
      </div>
    </div>
  </div>
  {{end}}
  
  <button hx-get="/settings/addresses/new"
          hx-target="#address-form"
          class="btn-outline w-full py-3">
    + {{i18n "address.add_new" .Lang}}
  </button>
</div>
```

---

## 九、GDPR 数据保存策略

### 9.1 数据分级保存

| 阶段 | 时间范围 | 存储位置 | 访问频率 | 策略 |
|------|----------|----------|----------|------|
| **热数据** | 0-3 个月 | 主表 (usage_records) | 高频 | 正常查询 |
| **温数据** | 3-12 个月 | 月度分区表 | 中频 | 归档分区，仍可查询 |
| **冷数据** | 1-7 年 | 年度归档表 | 低频 | 压缩存储，按需恢复 |
| **过期数据** | > 7 年 | 删除 | — | GDPR 合规删除 |

### 9.2 自动归档策略

```go
// internal/jobs/archive.go
func ArchiveOldUsageRecords(ctx context.Context, db *db.DB) error {
    // 每月 1 日执行
    // 1. 将 3 个月前的数据从主表迁移到归档表
    // 2. 删除 7 年前的数据
    
    threeMonthsAgo := time.Now().AddDate(0, -3, 0)
    sevenYearsAgo := time.Now().AddDate(-7, 0, 0)
    
    // 归档
    _, err := db.Pool.Exec(ctx, `
        INSERT INTO usage_records_archive
        SELECT * FROM usage_records
        WHERE created_at < $1
        ON CONFLICT DO NOTHING
    `, threeMonthsAgo)
    
    // 删除主表已归档数据
    _, err = db.Pool.Exec(ctx, `
        DELETE FROM usage_records
        WHERE created_at < $1
    `, threeMonthsAgo)
    
    // 删除 7 年以上数据（GDPR）
    _, err = db.Pool.Exec(ctx, `
        DELETE FROM usage_records_archive
        WHERE created_at < $1
    `, sevenYearsAgo)
    
    return err
}
```

---

## 十、敏感词评估

### 10.1 评估结论：**Phase 1 暂不实现**

| 维度 | 评估 |
|------|------|
| 必要性 | 中 — 主要是内容合规需求 |
| 技术难度 | 高 — 需要 tokenizer 支持中文分词 |
| proxy 支持 | ❌ 当前 proxy 无 tokenizer 特性 |
| 方案 A：服务端检测 | 需要引入中文 NLP 库（如 jieba-go），增加复杂度 |
| 方案 B：上游过滤 | 依赖 OpenAI/Claude 等上游提供商的内容过滤 |
| 方案 C：轻量正则 | 仅匹配关键词列表，误杀率高 |

**建议**：
1. **Phase 1 不实现敏感词功能**
2. 依赖上游提供商（OpenAI, Claude, 通义千问等）的内容安全策略
3. 在 admin → settings 中预留敏感词配置页面（空壳），后续如需实现再接入
4. 如需实现，推荐方案：在 proxy 端增加轻量关键词列表匹配（正则），不涉及分词

```go
// 预留接口（暂不实现）
// pkg/content/filter.go
package content

var sensitiveWords = []string{
    // 空列表，后续由 admin 配置
}

func ContainsSensitive(text string) (bool, []string) {
    // Phase 2 实现
    return false, nil
}
```

---

## 十一、兑换码设计

### 11.1 三种兑换码类型

| 类型 | 标识 | 效果 | 使用场景 |
|------|------|------|----------|
| **余额兑换码** | `balance` | 充值指定金额到 users.balance | 营销活动 |
| **Token Plan 兑换码** | `token_plan` | 激活指定 Token Plan 订阅 | 赠送套餐 |
| **Code Plan 兑换码** | `code_plan` | 激活指定 Code Plan 订阅 | 赠送套餐 |

### 11.2 使用规则

- 有兑换码余额时，**优先消费兑换码**
- 兑换码消费**不提供发票**
- 每个兑换码有**最大使用次数**限制
- 兑换码可设置**过期时间**
- 兑换记录需**审计日志**
- Admin 可**批量生成**兑换码（支持导入/导出）
- 格式：**WG-xxxx**，长度 16-20 字符

### 11.3 首次登录赠送兑换码

```sql
-- 用户首次登录时自动发放
INSERT INTO redemption_codes (code, name, code_type, amount_cents, max_uses, created_by)
VALUES ('WG-WELCOME-XXXX', '新用户欢迎礼', 'balance', 1500, 1, system_admin_id);
```

用户注册后首次登录，系统自动发放 **¥15** 余额兑换码。

### 11.3 兑换流程

```html
<!-- redemption.html -->
<div class="max-w-md mx-auto">
  <h2 class="text-xl font-bold mb-4">{{i18n "redemption.title" .Lang}}</h2>
  <form hx-post="/api/redemption/redeem" hx-target="#redemption-result">
    <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
    <div class="form-group">
      <label>{{i18n "redemption.code" .Lang}}</label>
      <input type="text" name="code" class="form-input uppercase"
             placeholder="XXXX-XXXX-XXXX"
             maxlength="20" required>
    </div>
    <button type="submit" class="btn-primary w-full">
      {{i18n "redemption.redeem" .Lang}}
    </button>
  </form>
  <div id="redemption-result" class="mt-4"></div>
</div>
```

```go
// internal/routes/redemption.go
func (r *RedemptionRoutes) redeem(c *gin.Context) {
    var req struct {
        Code string `json:"code" binding:"required"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": gin.H{"message": "invalid code"}})
        return
    }
    
    userID := c.GetString("user_id")
    code := strings.ToUpper(strings.TrimSpace(req.Code))
    
    // 事务 + 行锁
    tx, _ := r.db.Pool.Begin(c.Request.Context())
    defer tx.Rollback(c.Request.Context())
    
    var rc RedemptionCode
    err := tx.QueryRow(c.Request.Context(), `
        SELECT id, code, code_type, amount_cents, plan_id, duration_days, 
               max_uses, used_count, status, expires_at
        FROM redemption_codes
        WHERE code = $1 FOR UPDATE
    `, code).Scan(&rc.ID, &rc.Code, &rc.CodeType, &rc.AmountCents, 
        &rc.PlanID, &rc.DurationDays, &rc.MaxUses, &rc.UsedCount, 
        &rc.Status, &rc.ExpiresAt)
    
    // 验证
    if rc.Status != "active" {
        c.JSON(400, gin.H{"error": gin.H{"message": "code inactive"}})
        return
    }
    if rc.ExpiresAt != nil && rc.ExpiresAt.Before(time.Now()) {
        c.JSON(400, gin.H{"error": gin.H{"message": "code expired"}})
        return
    }
    if rc.UsedCount >= rc.MaxUses {
        c.JSON(400, gin.H{"error": gin.H{"message": "code max uses reached"}})
        return
    }
    
    // 根据类型处理
    switch rc.CodeType {
    case "balance":
        // 增加余额
        tx.Exec(c.Request.Context(), `
            UPDATE users SET balance = balance + $1 WHERE id = $2
        `, rc.AmountCents, userID)
    case "token_plan", "code_plan":
        // 创建订阅
        tx.Exec(c.Request.Context(), `
            INSERT INTO user_subscriptions 
            (user_id, plan_id, status, started_at, expires_at, 
             quota_total, quota_remaining, requests_remaining)
            VALUES ($1, $2, 'active', NOW(), NOW() + INTERVAL '$3 days',
                    (SELECT quota_total FROM subscription_plans WHERE id = $2),
                    (SELECT quota_total FROM subscription_plans WHERE id = $2),
                    (SELECT rpm_monthly FROM subscription_plans WHERE id = $2))
        `, userID, rc.PlanID, rc.DurationDays)
    }
    
    // 更新兑换码使用次数
    tx.Exec(c.Request.Context(), `
        UPDATE redemption_codes 
        SET used_count = used_count + 1,
            status = CASE WHEN used_count + 1 >= max_uses THEN 'inactive' ELSE status END
        WHERE id = $1
    `, rc.ID)
    
    // 记录兑换日志
    tx.Exec(c.Request.Context(), `
        INSERT INTO redemption_records 
        (code_id, user_id, amount_cents, ip_address, user_agent)
        VALUES ($1, $2, $3, $4, $5)
    `, rc.ID, userID, rc.AmountCents, c.ClientIP(), c.Request.UserAgent())
    
    tx.Commit(c.Request.Context())
    
    c.JSON(200, gin.H{"message": "redeemed successfully", "type": rc.CodeType})
}
```

---

## 十二、前端页面清单（按用户类型）

### 12.1 公开页面（所有用户）

| 路径 | 模板 | 说明 |
|------|------|------|
| `/` | `landing.html` | 产品首页（或跳转 /dashboard） |
| `/login` | `auth/login.html` | 邮箱 + 密码登录 |
| `/register` | `auth/register.html` | 邮箱验证码 + 密码注册 |
| `/forgot-password` | `auth/forgot-password.html` | 验证码重置密码 |

### 12.2 普通用户页面（user role）

| 路径 | 模板 | 用户类型可见 |
|------|------|-------------|
| `/dashboard` | `dashboard.html` | all |
| `/apikeys` | `apikeys.html` | all |
| `/billing` | `billing/balance.html` | all |
| `/billing/usage` | `billing/usage.html` | all |
| `/billing/recharge` | `billing/recharge.html` | all |
| `/billing/checkout` | `checkout.html` | all |
| `/orders` | `orders.html` | all |
| `/invoices` | `invoices.html` | all |
| `/invoices/apply` | `invoices/apply.html` | all |
| `/settings` | `settings/profile.html` | all |
| `/settings/password` | `settings/password.html` | all |
| `/settings/email` | `settings/email.html` | all |
| `/settings/addresses` | `settings/addresses.html` | all |
| `/settings/invoices` | `settings/invoice_titles.html` | all |
| `/redemption` | `redemption.html` | all |
| `/checkin` | `checkin.html` | all |

### 12.3 企业管理员页面（enterprise_admin role）

| 路径 | 模板 | 说明 |
|------|------|------|
| `/enterprise/dashboard` | `enterprise/dashboard.html` | 企业用量概览 |
| `/enterprise/users` | `enterprise/users.html` | 企业用户管理 |
| `/enterprise/keys` | `enterprise/keys.html` | 企业 Key 管理 |
| `/enterprise/billing` | `enterprise/billing.html` | 企业账单 |
| `/enterprise/usage` | `enterprise/usage.html` | 企业用量统计 |

### 12.4 系统管理员页面（system_admin role）

| 路径 | 模板 | 说明 |
|------|------|------|
| `/admin/dashboard` | `admin/dashboard.html` | 系统概览 + Chart.js |
| `/admin/users` | `admin/users.html` | 用户管理 |
| `/admin/users/:id` | `admin/user_detail.html` | 用户详情 |
| `/admin/keypool` | `admin/keypool.html` | upstream key 管理 |
| `/admin/providers` | `admin/providers.html` | 供应商配置 |
| `/admin/models` | `admin/models.html` | 模型管理 |
| `/admin/subscriptions` | `admin/subscriptions.html` | 订阅计划管理 |
| `/admin/redemption` | `admin/redemption.html` | 兑换码管理 |
| `/admin/orders` | `admin/orders.html` | 订单管理 |
| `/admin/invoices` | `admin/invoices.html` | 发票审核 |
| `/admin/audit-logs` | `admin/audit-logs.html` | 审计日志 |
| `/admin/settings` | `admin/settings.html` | 系统设置 |
| `/admin/channel-health` | `admin/channel-health.html` | 通道健康 |

### 12.5 超级管理员页面（super_admin role）

| 路径 | 模板 | 说明 |
|------|------|------|
| `/super/admin-management` | `super/admin_management.html` | 添加/删除/禁用 system_admin |
| `/super/providers` | `super/providers.html` | 管理 upstream 供应商 |
| `/super/payment` | `super/payment.html` | 支付系统配置 |
| `/super/super-admins` | `super/super_admins.html` | super_admin 管理（降级等） |

### 12.6 未来页面（Phase 2）

| 路径 | 说明 |
|------|------|
| `/playground` | AI Playground（Monaco + SSE） |
| `/docs` | API 文档 |

---

## 十三、实施计划

### 13.1 数据库迁移（Phase 0，1-2 天）

- [x] users 表扩展字段
- [x] 新建 subscription_plans / user_subscriptions
- [x] 新建 single_model_purchases
- [x] 新建 invoices / addresses
- [x] 新建 redemption_codes / redemption_records
- [x] 新建 email_change_logs
- [x] usage_records 扩展字段
- [x] 数据分区配置

### 13.2 基础设施（Phase 1，2-3 天）

- [x] web/ 目录 + embed.go
- [x] internal/web/ 包（handler, pages, data, helpers, i18n）
- [x] Tailwind CSS 配置 + purged CSS
- [x] HTMX + Alpine.js 嵌入
- [x] 多语言框架（zh/en 翻译文件）
- [x] 主题系统（light/dark/system）
- [x] 安全中间件（CSP, CSRF, SecurityHeaders）
- [x] cmd/server/main.go 修改

### 13.3 认证页面（Phase 2，1-2 天）

- [x] Login 页面
- [x] Register 页面（邮箱验证码）
- [x] Forgot Password 页面
- [x] Flash 消息系统
- [x] CSRF 全流程验证

### 13.4 用户核心页面（Phase 3，3-4 天）

- [x] Dashboard（按用户类型显示不同内容）
- [x] API Keys（按用户类型创建不同前缀 key）
- [x] Billing / Balance / Usage
- [x] Orders / Invoices
- [x] Settings（Profile, Password, Email, Addresses, Invoice Titles, Language, Theme）
- [x] Redemption / Checkin

### 13.5 支付集成（Phase 4，2-3 天）

- [x] Recharge 页面（金额 + 支付方式）
- [x] Alipay / WeChat Pay（QR 码 + 轮询）
- [x] Stripe（动态加载 SDK）
- [x] PayPal（动态加载 SDK）
- [x] 支付结果回调 + 余额更新

### 13.6 企业管理员页面（Phase 5，2 天）

- [x] Enterprise Dashboard
- [x] Enterprise Users / Keys / Billing / Usage

### 13.7 系统管理员页面（Phase 6，3-4 天）

- [x] Admin Dashboard（Chart.js）
- [x] Users / Key Pool / Providers / Models
- [x] Subscriptions / Redemption / Orders / Invoices
- [x] Audit Logs / Settings / Channel Health

### 13.8 超级管理员页面（Phase 7，1-2 天）

- [x] Admin Management
- [x] Provider Management
- [x] Payment System Config
- [x] Super Admin Management

### 13.9 安全加固与清理（Phase 8，2 天）

- [x] CSP 全路径测试
- [x] CSRF 渗透测试
- [x] 速率限制验证
- [x] 移动端全面测试
- [x] 多语言完整性检查
- [x] 清理 dist/ Node.js 代码

**总计：约 18-25 天**

---

## 十四、四-Agent 协作计划

### 14.1 分工

```
┌─────────────────────────────────────────────────────────────┐
│                       协调 Agent（本Agent）                  │
│  进度检查 / 方案确认 / 跨 Agent 问题协调                     │
├────────────┬─────────────────┬────────────────┬──────────────┤
│  Agent 1   │    Agent 2      │   Agent 3      │   Agent 4    │
│  Code      │  Test           │  Service+Stress │  Review      │
│  编码实现  │  单元+集成测试   │  业务+压力测试   │  安全+代码审查 │
└────────────┴─────────────────┴────────────────┴──────────────┘
```

### 14.2 Agent 1: Code（编码）

**职责**：所有前端模板 + Go handler + 数据库迁移

| Phase | 任务 | 预估时间 |
|-------|------|----------|
| P0 | 数据库迁移（所有新表） | 1-2 天 |
| P1 | 基础设施（embed, layout, CSS, i18n, theme） | 2-3 天 |
| P2 | 认证页面（login, register, forgot） | 1-2 天 |
| P3 | 用户核心页面（dashboard, apikeys, billing, settings） | 3-4 天 |
| P4 | 支付集成（4种支付方式） | 2-3 天 |
| P5 | 企业管理员页面 | 2 天 |
| P6 | 系统管理员页面 | 3-4 天 |
| P7 | 超级管理员页面 | 1-2 天 |
| P8 | 安全中间件 + 清理 | 2 天 |

### 14.3 Agent 2: Unit Test + Integration Test

**职责**：测试所有新增代码

| 任务 | 内容 |
|------|------|
| 模板测试 | 每个 .html 模板能正确渲染（无 panic） |
| Handler 测试 | 所有页面路由返回正确状态码 |
| 数据库测试 | 迁移脚本可重复执行，不报错 |
| 计费测试 | CalculateCost 公式验证（对照 Node.js 实现） |
| 扣费测试 | Token Plan / Code Plan 扣减逻辑验证 |
| CSRF 测试 | Token 生成/验证/过期 |
| i18n 测试 | zh/en 翻译完整性 |
| 集成测试 | 注册→登录→创建 Key→充值→使用→查看用量 完整流程 |

### 14.4 Agent 3: Service Test + Stress Test

**职责**：端到端 + 性能 + 安全渗透

| 任务 | 工具 | 目标 |
|------|------|------|
| E2E 测试 | Playwright / curl 脚本 | 完整业务流程 |
| 响应式测试 | 浏览器 DevTools | 手机/平板/桌面 |
| 性能测试 | k6 / hey | RPS > 2000, P99 < 100ms |
| 并发测试 | k6 | 1000 并发，成功率 99.9% |
| 支付沙箱 | Stripe Test Mode + PayPal Sandbox | 4 种支付全通过 |
| 安全渗透 | 手动 + 脚本 | XSS, CSRF, SQL 注入, IDOR |
| 计费验证 | 实际调用 proxy | token/request 统计准确性 |

### 14.5 Agent 4: Review

**职责**：代码审查 + 架构审查 + 安全审查

| 任务 | 内容 |
|------|------|
| 代码审查 | Go idioms, 错误处理, SQL 注入风险, N+1 查询 |
| 架构审查 | 模板继承是否合理, 路由设计, 是否过度工程 |
| 安全审查 | CSP 完整性, CSRF 覆盖, 敏感数据泄露, 密码策略 |
| 性能审查 | 数据库查询优化, 模板缓存, CSS 体积 |
| 计费审查 | 公式与 Node.js 实现一致性, 精度问题 |
| 文档审查 | 代码注释, API 文档同步 |

### 14.6 协作流程

```
A1 编码 → 提交 PR
  ↓
A2 测试 → 发现 Bug → A1 修复 → A2 复测通过
  ↓
A3 E2E + 压测 → 发现问题 → A1/A2 修复 → A3 复测通过
  ↓
A4 审查 → 批准 或 要求修改 → A1 修改 → A4 复审通过
  ↓
合并 → 进入下一 Phase
```

---

## 十五、总结

### 15.1 关键决策

| 问题 | V3 决策 |
|------|---------|
| 技术栈 | HTMX + Alpine.js + Tailwind (purged) + go:embed |
| 多语言 | Go 模板函数 i18n，短期中/英 |
| 主题 | light/dark/system，CSS 变量 + Alpine.js |
| 注册 | 仅邮箱 + 验证码，不强求手机 |
| 权限 | 4 级（user/enterprise_admin/system_admin/super_admin） |
| 用户类型 | 5 种（economy/general/single_model/subscription/enterprise） |
| User Key | 3 种格式：`sk-wgit-xxxx` / `sk-wgit-en-xxxx` / `sk-wgit-tp-xxxx` |
| 计费 | Token + Request 双重统计，按用户类型选择计费方式 |
| 发票 | 多抬头 + 多地址，admin 审核后发送 |
| GDPR | 7 年保存，1-3月热/3-12月温/1-7年冷，每月第一个周末归档 |
| 敏感词 | Phase 1 暂不实现，仅 admin 空壳菜单 |
| 兑换码 | WG-xxxx 格式，16-20字符，支持批量生成/导入/导出，优先消费 |
| 首次登录 | 自动发放 ¥15 余额兑换码 |
| OAuth | Phase 1 不实现 |
| 签到 | 不实现 |
| Playground | 不实现 |

### 15.2 预估工作量

| 阶段 | 内容 | 预估时间 |
|------|------|----------|
| P0 | 数据库迁移 | 1-2 天 |
| P1 | 基础设施 | 2-3 天 |
| P2 | 认证 | 1-2 天 |
| P3 | 用户核心页面 | 3-4 天 |
| P4 | 支付 | 2-3 天 |
| P5 | 企业管理员 | 2 天 |
| P6 | 系统管理员 | 3-4 天 |
| P7 | 超级管理员 | 1-2 天 |
| P8 | 安全加固 | 2 天 |
| **总计** | | **18-25 天** |

---

*Document Version: 3.0*
*Changelog:*
- *V2 → V3: 增加业务需求逐项回应（多语言、权限、用户类型、User Key、计费、发票、GDPR、敏感词、兑换码）*
- *V3 新增: 数据库 Schema 设计、计费模型详细设计、按用户类型的前端页面清单*
