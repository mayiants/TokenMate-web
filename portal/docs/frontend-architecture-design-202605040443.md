# AI Portal Go - 前端架构方案 V3 (综合讨论版)

> **ACTIVE** - Created: 2026-05-04
> 综合来源: Claude V1/V2, Kimi V2, Xiaomi V2, Minimax V2, Deepseek V2
> 本版重点: 用户类型、计费模型、权限体系、User Key 策略

---

## 一、技术栈确认

### 1.1 最终选型 (基于 V2 共识)

| 层 | 技术 | 说明 |
|----|------|------|
| 模板 | Go `html/template` + `go:embed` | 零依赖，单二进制 |
| 交互 | HTMX 2.x (14KB) | 90% 场景 |
| 轻量状态 | Alpine.js 3.x (15KB) | dropdown/tab/modal |
| 样式 | Tailwind CSS v4 (本地构建) | 不用 CDN，生产 purgeCSS |
| 图标 | Lucide SVG 内联 | 每个 ~300B |
| 图表 | Chart.js (按需) | 仅 Dashboard/Analytics |
| 编辑器 | Monaco (按需) | 仅 Playground |
| 支付 | Stripe.js / PayPal SDK (按需) | 仅支付页 |
| i18n | Go template + JSON 翻译文件 | 中/英双语 |

### 1.2 多语言方案

```go
// internal/web/i18n.go
type I18n struct {
    Lang  string
    Trans map[string]string
}

func loadTranslations(lang string) map[string]string {
    // 从 go:embed 加载
    data, _ := translationsFS.ReadFile("translations/" + lang + ".json")
    var trans map[string]string
    json.Unmarshal(data, &trans)
    return trans
}
```

```html
<!-- 模板中使用 -->
<h1>{{index .Trans "dashboard.title"}}</h1>
<p>{{index .Trans "dashboard.welcome" .User.Name}}</p>
```

```json
// translations/zh.json
{
  "dashboard.title": "仪表盘",
  "dashboard.welcome": "欢迎回来，%s",
  "nav.home": "首页",
  "nav.products": "产品中心"
}

// translations/en.json
{
  "dashboard.title": "Dashboard",
  "dashboard.welcome": "Welcome back, %s",
  "nav.home": "Home",
  "nav.products": "Products"
}
```

**切换方式:**
- URL 参数: `?lang=en`
- Cookie: `lang=zh`
- 用户设置中保存偏好

---

## 二、用户体系设计

### 2.1 角色权限矩阵

| 角色 | 英文 | 最大数量 | 权限范围 |
|------|------|----------|----------|
| 超级管理员 | super_admin | 3 | 全部 + 管理 system_admin + 供应商 + 支付系统 |
| 系统管理员 | system_admin | 10 | 日常管理 + 计费 + 账单 + keypool |
| 企业管理员 | enterprise_admin | 不限 | 企业内部 user key 管理 + 用量查看 + 账单 |
| 普通用户 | user | 不限 | 个人 dashboard + key + 充值 |

### 2.2 权限详情

#### super_admin (超级管理员)

| 权限 | 说明 |
|------|------|
| 管理 system_admin | 添加/删除/禁用/启用 |
| 管理供应商 | 添加/删除/禁用/启用 upstream provider |
| 管理支付系统 | 配置支付接口 (Stripe/Alipay/WeChat/PayPal) |
| 管理模型市场 | 供应商激活后的产品管理可委托给 system_admin |
| 审计日志 | 查看所有操作日志 |
| 应急操作 | 禁用用户、冻结系统 |

**特殊规则:**
- super_admin 不可删除自己，需降级到 system_admin 后由另一位 super_admin 删除
- 降级需另一位 super_admin 确认
- 可自行修改密码

#### system_admin (系统管理员)

| 权限 | 说明 |
|------|------|
| 用户管理 | 查看/编辑用户信息、角色、余额 |
| 计费管理 | 设置定价、查看账单 |
| Key Pool 管理 | upstream key CRUD |
| 模型管理 | 已激活供应商的产品管理 |
| 订阅计划 | 创建/编辑套餐 |
| 兑换码 | 生成/管理兑换码 |
| 审计日志 | 查看操作日志 |

#### enterprise_admin (企业管理员)

| 权限 | 说明 |
|------|------|
| User Key 管理 | 批量创建/分配/查看企业内部 key |
| 用量查看 | 企业内部所有 key 的使用情况 |
| 账单查看 | 企业账单、发票 |
| 用户管理 | 管理企业内部 user |

#### user (普通用户)

| 权限 | 说明 |
|------|------|
| Dashboard | 个人用量概览 |
| API Keys | 创建/查看自己的 key |
| 充值 | 选择套餐/充值 |
| 订单 | 查看订单历史 |
| 设置 | 个人资料、改密码、语言 |

### 2.3 用户类型 (服务等级)

| 类型 | 英文 | 计费方式 | Key 类型 | 说明 |
|------|------|----------|----------|------|
| 经济用户 | economy | 按 token | 通用 key | 最便宜，特定 keypool，按需充值 |
| 一般用户 | general | 按 token | 通用 key | 预存/充值，普通 keypool |
| 单品用户 | single-model | 按 token | 专用 key | 针对具体模型充值，预付费 |
| 订阅用户 | subscription | Token Plan / Code Plan | 套餐 key | 订阅套餐，预付费 |
| 企业用户 | enterprise | 按 token + 后付费 | 企业 key | 高优先级 pool，整体额度控制 |

---

## 三、User Key 策略 (核心设计)

### 3.1 问题分析

用户提问：
> a) 企业用户由企业管理员自行批量创建 user key
> b) Token/Code plan 客户基于套餐生成 key，如 sk-wgit-tp-xxxx
> c) 其他用户使用通用 key，如 sk-wgit-xxxx
> 但如何区分单一产品和随意模型使用？

### 3.2 方案：Key 绑定访问范围

**核心思路：** Key 本身不区分类型，通过 **Key 的 `access_scope` 字段** 控制可访问模型。

```sql
CREATE TABLE api_keys (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    key_hash VARCHAR(255) NOT NULL,
    key_prefix VARCHAR(20) NOT NULL,     -- sk-wgit-xxxx / sk-wgit-tp-xxxx / sk-wgit-ent-xxxx
    name VARCHAR(255),
    
    -- 访问控制
    access_scope VARCHAR(50) DEFAULT 'general',
    -- 'general'       : 可访问所有普通模型 (经济/一般用户)
    -- 'single:model_id' : 仅可访问指定模型 (单品用户)
    -- 'plan:plan_id'   : 可访问套餐内模型 (订阅用户)
    -- 'enterprise'     : 可访问企业专属 pool (企业用户)
    
    allowed_models JSONB,                -- 允许的模型列表 (根据 scope 自动填充)
    
    -- 计费关联
    billing_mode VARCHAR(20) DEFAULT 'token',
    -- 'token'  : 按 token 计费
    -- 'code'   : 按 request 计费 (Code Plan)
    
    subscription_id UUID,                -- 关联的订阅 ID (subscription 用户)
    product_id VARCHAR(100),             -- 关联的产品 ID (single-model 用户)
    
    -- 统计
    total_requests BIGINT DEFAULT 0,
    total_tokens BIGINT DEFAULT 0,
    
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
```

### 3.3 Key 前缀规范

| 前缀 | 格式 | 用途 | 示例 |
|------|------|------|------|
| `sk-wgit-` | 通用 key | 经济/一般用户 | `sk-wgit-a1b2c3d4` |
| `sk-wgit-tp-` | Token Plan key | Token 订阅用户 | `sk-wgit-tp-x5y6z7` |
| `sk-wgit-cp-` | Code Plan key | Code 订阅用户 | `sk-wgit-cp-m8n9o0` |
| `sk-wgit-sm-` | Single Model key | 单品用户 | `sk-wgit-sm-p1q2r3` |
| `sk-wgit-ent-` | Enterprise key | 企业用户 | `sk-wgit-ent-s4t5u6` |

### 3.4 Key 创建流程

```
用户注册/购买
    ↓
根据用户类型创建 Key
    ├── economy/general → 自动创建通用 key (sk-wgit-xxxx)
    ├── single-model → 创建专用 key (sk-wgit-sm-xxxx) + 绑定 product_id
    ├── subscription → 创建套餐 key (sk-wgit-tp/cp-xxxx) + 绑定 subscription_id
    └── enterprise → 企业管理员批量创建 (sk-wgit-ent-xxxx)
    ↓
Key 的 access_scope 自动设置
    ↓
Proxy 验证 key 时检查 access_scope 是否匹配请求的 model
```

### 3.5 Proxy 验证逻辑

```go
// Proxy 验证 key 时
func (p *Proxy) validateKey(keyHash, model string) error {
    key := p.getKeyByHash(keyHash)
    
    switch key.AccessScope {
    case "general":
        // 可访问所有普通模型
        return nil
        
    case "single:model_id":
        // 仅可访问指定模型
        if key.ProductID != model {
            return errors.New("this key is bound to a specific model")
        }
        return nil
        
    case "plan:plan_id":
        // 检查套餐是否包含该模型
        if !p.planIncludesModel(key.SubscriptionID, model) {
            return errors.New("model not included in your plan")
        }
        return nil
        
    case "enterprise":
        // 可访问企业专属 pool
        return nil
    }
}
```

### 3.6 简化方案 (推荐)

**避免过度复杂：** 统一使用 `sk-wgit-` 前缀，通过数据库字段区分类型。

| 方面 | 决策 |
|------|------|
| Key 格式 | 统一 `sk-wgit-` 前缀 + 随机字符串 |
| 区分方式 | 数据库 `access_scope` + `billing_mode` 字段 |
| 单品 vs 通用 | 通过 `allowed_models` JSONB 字段控制 |
| 企业 key | 通过 `user_type = 'enterprise'` 标识 |

**理由：**
- Key 前缀细分增加管理复杂度
- 数据库字段更灵活，可随时调整
- 用户无需关心 key 类型，只关心可用模型

---

## 四、计费模型设计

### 4.1 双重统计

**所有 user key 消耗必须同时记录:**

| 统计维度 | 字段 | 说明 |
|----------|------|------|
| Token 统计 | `input_tokens`, `output_tokens`, `cached_tokens`, `reasoning_tokens` | 精细 token 分类 |
| Request 统计 | `request_count` | 请求数量 |

**计费时选择其中一种:**

| 计费方式 | 适用场景 | 计费公式 |
|----------|----------|----------|
| Token Plan | 按量消耗 | `费用 = (input + output × 倍率) × 单价 / 1M` |
| Code Plan | 按次消耗 | `费用 = request_count × 单价` |

### 4.2 Token Plan 细分

```sql
CREATE TABLE token_plans (
    id UUID PRIMARY KEY,
    name VARCHAR(100),
    
    -- Token 分类定价 (per million tokens)
    input_price DECIMAL(10,4),           -- 输入 (cache-miss)
    input_cached_price DECIMAL(10,4),    -- 输入 (cache-hit)
    output_price DECIMAL(10,4),          -- 输出
    reasoning_price DECIMAL(10,4),       -- 推理
    
    -- 折扣
    discount_rate DECIMAL(5,4) DEFAULT 1.0,
    
    currency VARCHAR(10) DEFAULT 'USD',
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMP DEFAULT NOW()
);
```

**示例定价:**

| 模型类型 | Input | Input (Cached) | Output | Reasoning |
|----------|-------|----------------|--------|-----------|
| GPT-4o | $2.50/1M | $1.25/1M | $10.00/1M | — |
| Claude Sonnet | $3.00/1M | $1.50/1M | $15.00/1M | — |
| DeepSeek | $0.14/1M | $0.07/1M | $0.28/1M | $0.56/1M |

### 4.3 Code Plan 细分

```sql
CREATE TABLE code_plans (
    id UUID PRIMARY KEY,
    name VARCHAR(100),
    
    -- Request 配额
    rpm_limit INTEGER,                   -- 每分钟请求数
    rph_limit INTEGER,                   -- 每小时请求数
    rp5h_limit INTEGER,                  -- 每5小时请求数
    rpd_limit INTEGER,                   -- 每天请求数
    rpw_limit INTEGER,                   -- 每周请求数
    rpm_month_limit INTEGER,             -- 每月请求数
    
    price DECIMAL(10,2),                 -- 月费
    currency VARCHAR(10) DEFAULT 'USD',
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMP DEFAULT NOW()
);
```

**默认 5 档:**

| 档位 | RPM | 月费 | 适用场景 |
|------|-----|------|----------|
| Lite | 60 | $39 | 个人开发 |
| Standard | 500 | $99 | 小团队 |
| Pro | 1,200 | $399 | 中型团队 |
| Max | 9,000 | $699 | 大型团队 |
| Enterprise | 18,000 | 定制 | 企业 |

### 4.4 用户类型与计费对应

| 用户类型 | 计费方式 | 余额来源 | 扣费逻辑 |
|----------|----------|----------|----------|
| economy | Token Plan | 充值 | 按 token 从余额扣除，单价最低 |
| general | Token Plan | 充值 | 按 token 从余额扣除，标准单价 |
| single-model | Token Plan | 购买特定模型额度 | 从该次购买额度扣除 |
| subscription | Token/Code Plan | 订阅套餐额度 | 从套餐额度扣除 |
| enterprise | Token Plan (后付费) | 整体额度 | 月末结算 |

### 4.5 Upstream Key 统计

**Upstream key 也需要记录，用于对账:**

```sql
CREATE TABLE upstream_usage_records (
    id BIGSERIAL PRIMARY KEY,
    upstream_key_id VARCHAR(50),
    provider VARCHAR(50),
    model VARCHAR(100),
    
    -- 上游报告的用量
    upstream_input_tokens BIGINT,
    upstream_output_tokens BIGINT,
    upstream_cached_tokens BIGINT,
    upstream_request_count INTEGER,
    
    -- 上游报告的费用
    upstream_cost DECIMAL(18,10),
    
    -- 我们计算的费用
    our_cost DECIMAL(18,10),
    
    -- 差异
    cost_diff DECIMAL(18,10),
    
    created_at TIMESTAMP DEFAULT NOW()
);
```

**对账逻辑:**
```
每月对比 upstream_usage_records 的 upstream_cost vs our_cost
差异 = upstream_cost - our_cost
如果差异 > 5%，告警
```

---

## 五、订阅套餐设计

### 5.1 默认套餐

| 套餐 | 价格 | Token 额度 | 有效期 | 自动续费 |
|------|------|------------|--------|----------|
| Lite | ¥39/月 | 60M tokens | 30天 | 可选 |
| Standard | ¥99/月 | 200M tokens | 30天 | 可选 |
| Pro | ¥399/月 | 1B tokens | 30天 | 可选 |
| Max | ¥699/月 | 2B tokens | 30天 | 可选 |

### 5.2 订阅状态机

```
创建订阅 → active → 过期 → expired
                ↓
            取消 → cancelled
                ↓
            自动续费 → active (续期)
```

### 5.3 额度消耗优先级

```
1. 兑换码额度 (优先消耗，不提供发票)
2. 套餐额度 (subscription)
3. 充值余额 (general/economy)
4. 单品购买额度 (single-model)
```

---

## 六、兑换码设计

### 6.1 兑换码类型

| 类型 | 说明 | 有效期 | 用途 |
|------|------|--------|------|
| welcome | 新用户欢迎码 | 注册后7天 | 自动发放 |
| promo | 促销码 | 自定义 | 营销活动 |
| gift | 礼品码 | 自定义 | 用户赠送 |

### 6.2 兑换码消费规则

```
有"兑换码 + 余额"时:
1. 优先消费兑换码额度
2. 兑换码用完后再用余额
3. 兑换码消费不提供发票
```

### 6.3 简化 UI (相比 Node.js 版)

```
┌─────────────────────────────────────────┐
│  兑换码                                  │
│                                         │
│  [请输入兑换码]  [兑换]                  │
│                                         │
│  我的兑换记录:                           │
│  ┌─────────────────────────────────────┐│
│  │ WELCOME-2026  ¥50  已用 ¥30  剩余 ¥20││
│  │ PROMO-ABC123  ¥100  已用 ¥100  已用完 ││
│  └─────────────────────────────────────┘│
└─────────────────────────────────────────┘
```

---

## 七、发票与地址管理

### 7.1 发票信息

```sql
CREATE TABLE invoice_info (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    
    -- 发票抬头
    title_type VARCHAR(20),              -- 'personal' / 'company'
    title VARCHAR(255),                  -- 抬头名称
    tax_id VARCHAR(50),                  -- 税号
    
    -- 企业信息 (company 类型)
    company_address TEXT,
    company_phone VARCHAR(20),
    bank_name VARCHAR(100),
    bank_account VARCHAR(50),
    
    -- 联系方式
    email VARCHAR(255),
    phone VARCHAR(20),
    
    is_default BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT NOW()
);
```

### 7.2 地址信息

```sql
CREATE TABLE addresses (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    
    name VARCHAR(100),                   -- 收件人
    phone VARCHAR(20),
    province VARCHAR(50),
    city VARCHAR(50),
    district VARCHAR(50),
    address TEXT,
    postal_code VARCHAR(20),
    
    is_default BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT NOW()
);
```

### 7.3 发票申请流程

```
用户选择订单 → 选择发票抬头 → 选择地址 → 提交申请
    ↓
Admin 审核 → 开票 → 更新状态
    ↓
用户查看发票记录
```

---

## 八、敏感词评估

### 8.1 问题分析

| 问题 | 分析 |
|------|------|
| Proxy 无 tokenizer | 无法精确分词，只能做简单字符串匹配 |
| 中文分词 | 需要额外库 (jieba 等)，增加复杂度 |
| 误判率高 | 简单匹配会导致大量误判 |
| 维护成本 | 需要持续更新词库 |

### 8.2 建议：暂不实现

| 理由 | 说明 |
|------|------|
| 技术限制 | Proxy 无 tokenizer，无法精确识别 |
| 误判风险 | 简单字符串匹配误判率高 |
| 合规成本 | 需要专业词库和持续维护 |
| 替代方案 | 依赖上游供应商的内容过滤 |

**保留接口:** 前端预留敏感词设置页面，后端预留 `sensitive_words` 表，但标记为 **Phase 2** 功能。

---

## 九、GDPR 合规

### 9.1 数据保留策略

| 数据类型 | 热数据 | 温数据 | 冷数据 | 总保留 |
|----------|--------|--------|--------|--------|
| usage_records | 1-3月 | 3-12月 | 1-7年 | 7年 |
| audit_logs | 1-3月 | 3-12月 | 1-7年 | 7年 |
| 用户数据 | 活跃期 | — | 删除后匿名化 | 7年 |

### 9.2 分区策略

```sql
-- usage_records 按月分区
CREATE TABLE usage_records (
    ...
) PARTITION BY RANGE (created_at);

-- 自动创建月度分区
CREATE TABLE usage_records_2026_05 PARTITION OF usage_records
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
```

### 9.3 数据归档

```
热数据 (1-3月): PostgreSQL 主表
温数据 (3-12月): PostgreSQL 分区表
冷数据 (1-7年): 压缩归档 (pg_dump + gzip)
```

---

## 十、部署架构

### 10.1 推荐架构

```
用户 → CloudFront/CDN (可选) → ALB/SLB (SSL终止) → ECS (Go二进制)
                                                          ↓
                                                    PostgreSQL (RDS)
                                                    Redis (ElastiCache)
```

### 10.2 Dockerfile

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
```

---

## 十一、四 Agent 协作计划

| Agent | 职责 | 产出 |
|-------|------|------|
| **Code** | 前端代码 + Go handler | web/ 目录 + internal/web/ |
| **Test** | 单元测试 + 集成测试 | *_test.go, test-web.ts |
| **Service** | E2E + 压力测试 | Playwright, wrk |
| **Review** | 代码审查 + 安全审计 | 审查报告 |

### 开发排期

| 阶段 | Code Agent | Test Agent | Service Agent | Review Agent |
|------|------------|------------|---------------|--------------|
| Week 1 | 基础设施 + 认证 | 单元测试 | — | — |
| Week 2 | 用户页面 + 计费 | 集成测试 | E2E 测试 | 代码审查 |
| Week 3 | 支付 + 订阅 | 安全测试 | 压力测试 | 安全审计 |
| Week 4 | Admin 后台 | 回归测试 | 移动测试 | 最终审查 |

---

## 十二、待确认事项

| # | 事项 | 建议 | 需确认 |
|---|------|------|--------|
| 1 | User Key 前缀 | 统一 `sk-wgit-`，数据库区分 | ✅ |
| 2 | 敏感词 | 暂不实现，保留接口 | ✅ |
| 3 | 兑换码优先级 | 先于余额消耗 | ✅ |
| 4 | 企业后付费 | 月末结算，整体额度控制 | ✅ |
| 5 | 自动续费 | 订阅套餐支持 | ✅ |
| 6 | Tailwind CDN vs 本地 | 本地构建 (purgeCSS) | ✅ |
| 7 | Playground | Phase 2，按需加载 Monaco | ✅ |

---

**文档版本:** 3.0
**更新时间:** 2026-05-04
