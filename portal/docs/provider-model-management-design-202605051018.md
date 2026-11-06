> **⚠️ PARTIALLY OUTDATED** — 本文档基于 2026-05-05 的架构，当时 `(provider, product_id)` 是 `service_models` 的唯一标识。2026-05-16/17 重构后，`api_model_name` 已成为跨 provider 的统一模型标识，同一 `api_model_name` 可对应多个 provider 的 `product_id`。详见 `../../ai-proxy-go/docs/model-name-translation-202605171430.md`。
>
> **仍有效的部分**: Provider 管理、供应商配置、模型添加流程。
> **已过时的部分**: 模型唯一性假设（§2.1-2.2）、模型与 provider 一对一的关系描述。

# Provider + Model 管理系统设计文档

## 概述

Provider (供应商) + Model (模型) 是平台业务的核心。本文档整理供应商和模型的选择、添加/删除、同步的完整设计思路。

---

## 一、供应商管理 (Provider)

### 1.1 数据表结构

```sql
CREATE TABLE providers (
  id          VARCHAR(50)  PRIMARY KEY,      -- 供应商ID: openai, anthropic, alibaba
  name        VARCHAR(100) NOT NULL,         -- 显示名称: OpenAI, Anthropic, 阿里巴巴
  type        VARCHAR(20),                   -- 类型: international, domestic
  base_url    VARCHAR(255),                  -- API基础地址
  enabled     BOOLEAN DEFAULT true,          -- 是否启用
  sort_order  INTEGER DEFAULT 0              -- 排序 (按首字母排序)
);
```

### 1.2 供应商列表 (18家)

| 序号 | ID | 名称 | 类型 | Base URL |
|------|-----|------|------|----------|
| 1 | `alibaba` | Alibaba | domestic | https://dashscope.aliyuncs.com/compatible-mode/v1 |
| 2 | `amazon` | Amazon | international | https://bedrock-runtime.us-east-1.amazonaws.com/v1 |
| 3 | `anthropic` | Anthropic | international | https://api.anthropic.com/v1 |
| 4 | `baidu` | Baidu | domestic | https://aip.baidubce.com/rpc/2.0/ai_custom/v1 |
| 5 | `bytedance` | ByteDance | domestic | https://ark.cn-beijing.volces.com/api/v3 |
| 6 | `cohere` | Cohere | international | https://api.cohere.ai/v1 |
| 7 | `deepseek` | DeepSeek | domestic | https://api.deepseek.com/v1 |
| 8 | `google` | Google | international | https://generativelanguage.googleapis.com/v1beta |
| 9 | `meta` | Meta | international | https://api.llama.meta.com/v1 |
| 10 | `microsoft` | Microsoft | international | https://api.openai.azure.com/v1 |
| 11 | `minimax` | MiniMax | domestic | https://api.minimax.chat/v1 |
| 12 | `mistral` | Mistral | international | https://api.mistral.ai/v1 |
| 13 | `moonshot` | Moonshot | domestic | https://api.moonshot.cn/v1 |
| 14 | `openai` | OpenAI | international | https://api.openai.com/v1 |
| 15 | `perplexity` | Perplexity | international | https://api.perplexity.ai |
| 16 | `tencent` | Tencent | domestic | https://api.hunyuan.cloud.tencent.com/v1 |
| 17 | `xai` | xAI | international | https://api.x.ai/v1 |
| 18 | `zhipu` | Zhipu | domestic | https://open.bigmodel.cn/api/paas/v4 |

### 1.3 供应商管理流程

```
┌─────────────────────────────────────────────────────────────┐
│                   供应商管理完整流程                          │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  步骤1: 初始化供应商                                        │
│  ┌─────────────────┐                                        │
│  │ 系统启动时      │───> providers 表初始化                 │
│  │ 自动初始化18家  │    (ON CONFLICT DO NOTHING)            │
│  └─────────────────┘                                        │
│                                                             │
│  步骤2: Admin 管理供应商                                    │
│  ┌─────────────────┐                                        │
│  │ 查看供应商列表  │                                        │
│  │ 编辑供应商信息  │───> 更新 providers 表                  │
│  │ 启用/禁用供应商 │                                        │
│  └─────────────────┘                                        │
│                                                             │
│  步骤3: Super Admin 高级管理                                │
│  ┌─────────────────┐                                        │
│  │ 添加新供应商    │───> INSERT INTO providers              │
│  │ 删除供应商      │───> DELETE FROM providers              │
│  │ (谨慎操作)      │    (需确认无关联密钥/模型)              │
│  └─────────────────┘                                        │
│                                                             │
│  步骤4: 供应商与模型关联                                    │
│  ┌─────────────────┐                                        │
│  │ OpenRouter同步  │───> openrouter_models.provider         │
│  │ 手动添加模型    │───> service_models.provider            │
│  │ API密钥配置     │───> api_keys.provider                  │
│  └─────────────────┘                                        │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 1.4 供应商初始化

供应商数据在服务启动时自动初始化 (main.go):

```go
func initProviders(database *db.DB, logger *zap.Logger) {
    providers := []struct {
        ID      string
        Name    string
        Type    string
        BaseURL string
    }{
        {"alibaba", "Alibaba", "domestic", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
        {"amazon", "Amazon", "international", "https://bedrock-runtime.us-east-1.amazonaws.com/v1"},
        {"anthropic", "Anthropic", "international", "https://api.anthropic.com/v1"},
        {"baidu", "Baidu", "domestic", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1"},
        {"bytedance", "ByteDance", "domestic", "https://ark.cn-beijing.volces.com/api/v3"},
        {"cohere", "Cohere", "international", "https://api.cohere.ai/v1"},
        {"deepseek", "DeepSeek", "domestic", "https://api.deepseek.com/v1"},
        {"google", "Google", "international", "https://generativelanguage.googleapis.com/v1beta"},
        {"meta", "Meta", "international", "https://api.llama.meta.com/v1"},
        {"microsoft", "Microsoft", "international", "https://api.openai.azure.com/v1"},
        {"minimax", "MiniMax", "domestic", "https://api.minimax.chat/v1"},
        {"mistral", "Mistral", "international", "https://api.mistral.ai/v1"},
        {"moonshot", "Moonshot", "domestic", "https://api.moonshot.cn/v1"},
        {"openai", "OpenAI", "international", "https://api.openai.com/v1"},
        {"perplexity", "Perplexity", "international", "https://api.perplexity.ai"},
        {"tencent", "Tencent", "domestic", "https://api.hunyuan.cloud.tencent.com/v1"},
        {"xai", "xAI", "international", "https://api.x.ai/v1"},
        {"zhipu", "Zhipu", "domestic", "https://open.bigmodel.cn/api/paas/v4"},
    }

    for _, p := range providers {
        database.Pool.Exec(ctx,
            `INSERT INTO providers (id, name, type, base_url) VALUES ($1, $2, $3, $4)
             ON CONFLICT (id) DO NOTHING`,
            p.ID, p.Name, p.Type, p.BaseURL,
        )
    }
}
```

**关键点:**
- 使用 `ON CONFLICT (id) DO NOTHING` 确保幂等性
- 不会覆盖已存在的供应商配置
- 启动时自动创建缺失的供应商

### 1.5 供应商状态定义

| 状态 | 说明 | 标签颜色 | 可添加模型 | 可配置密钥 | API调用 |
|------|------|----------|------------|------------|---------|
| `enabled` | 启用 | 绿色 | ✅ | ✅ | ✅ |
| `disabled` | 禁用 | 红色 | ❌ | ❌ | ❌ |

### 1.6 供应商操作权限

| 操作 | System Admin | Super Admin |
|------|--------------|-------------|
| 查看供应商列表 | ✅ | ✅ |
| 编辑供应商信息 | ✅ | ✅ |
| 启用/禁用供应商 | ✅ | ✅ |
| 添加新供应商 | ❌ | ✅ |
| 删除供应商 | ❌ | ✅ |

### 1.7 供应商删除约束

删除供应商前需检查：
1. 该供应商下是否有 API 密钥 (`api_keys.provider = ?`)
2. 该供应商下是否有模型 (`service_models.provider = ?`)
3. 该供应商下是否有 OpenRouter 缓存模型 (`openrouter_models.provider = ?`)

如有关联数据，需先清理或迁移，否则删除失败。

### 1.8 供应商前端页面

#### /admin/providers 页面 (System Admin)

**表格列:**
| ID | 名称 | 类型 | Base URL | 密钥数 | 模型数 | 状态 | 操作 |
|----|------|------|----------|--------|--------|------|------|

**操作按钮:**
- 编辑 - 修改名称、类型、Base URL
- 启用/禁用 - 切换 enabled 状态

#### /super/providers 页面 (Super Admin)

**表格列:**
| ID | 名称 | 类型 | Base URL | 状态 | 操作 |
|----|------|------|----------|------|------|

**操作按钮:**
- 编辑 - 修改名称、类型、Base URL
- 启用/禁用 - 切换 enabled 状态
- 删除 - 删除供应商 (需确认)

---

## 二、模型管理 (Model)

### 2.1 数据表结构

#### openrouter_models 表 (OpenRouter同步缓存)

```sql
CREATE TABLE openrouter_models (
  id             UUID PRIMARY KEY,
  openrouter_id  VARCHAR(200) UNIQUE,        -- OpenRouter模型ID: openai/gpt-4o
  provider       VARCHAR(50),                -- 映射后的供应商ID: openai
  name           VARCHAR(200),               -- 模型名称: GPT-4o
  input_price    NUMERIC(10,6),              -- 输入价格 ($/1K tokens)
  output_price   NUMERIC(10,6),              -- 输出价格 ($/1K tokens)
  context_window INTEGER,                    -- 上下文长度
  raw_data       JSONB,                      -- 原始JSON数据
  synced_at      TIMESTAMP                   -- 同步时间
);
```

#### service_models 表 (正式模型配置)

```sql
CREATE TABLE service_models (
  id                      UUID PRIMARY KEY,
  provider                VARCHAR(50) NOT NULL,       -- 供应商ID
  product_id              VARCHAR(100) NOT NULL,      -- 产品ID (模型ID)
  api_model_name          VARCHAR(100),               -- API调用时的模型名
  
  -- 状态管理
  status                  VARCHAR(20) DEFAULT 'pending',
  show_in_store           BOOLEAN DEFAULT true,
  sort_order              INTEGER DEFAULT 0,
  
  -- 定价 (四项定价标准)
  input_price_cache_hit   NUMERIC(10,6),              -- 缓存命中价格 ($/1M tokens)
  input_price_cache_miss  NUMERIC(10,6),              -- 缓存未命中价格 ($/1M tokens)
  output_price            NUMERIC(10,6),              -- 输出价格 ($/1M tokens)
  reasoning_price         NUMERIC(10,6),              -- 推理价格 ($/1M tokens)
  
  -- EOL 管理
  eol_announced_at        TIMESTAMP,                  -- 供应商宣布EOL时间
  eol_service_at          TIMESTAMP,                  -- 计划停止服务时间
  eol_purge_at            TIMESTAMP,                  -- 计划清除时间 (EOL后1个月)
  eol_confirmed_by        UUID,                       -- 确认EOL的管理员
  eol_confirmed_at        TIMESTAMP,                  -- 确认时间
  eol_notes               TEXT,                       -- EOL备注
  
  -- 暂停/停止管理
  paused_at               TIMESTAMP,
  paused_reason           TEXT,
  paused_notes            TEXT,
  discontinued_at         TIMESTAMP,
  discontinued_reason     TEXT,
  discontinued_notes      TEXT,
  
  -- 审批流程
  confirmed_by            UUID,
  confirmed_at            TIMESTAMP,
  
  -- 扩展数据
  aimodels_data           JSONB,
  provider_aliases        JSONB,                      -- 供应商别名映射
  
  created_at              TIMESTAMP DEFAULT NOW(),
  updated_at              TIMESTAMP DEFAULT NOW(),
  
  UNIQUE(provider, product_id)
);
```

### 2.2 模型状态定义

| 状态 | 说明 | 标签颜色 | 可购买 | 可服务 | 商店显示 |
|------|------|----------|--------|--------|----------|
| `pending` | 待审核 | 灰色 | ❌ | ❌ | ❌ |
| `active` | 正常服务 | 绿色 | ✅ | ✅ | ✅ |
| `paused` | 暂停服务 | 黄色 | ❌ | ❌ | ❌ |
| `eol_pending` | EOL预告 | 橙色 | ❌ | ✅ | ⚠️ (标记EOL) |
| `eol` | EOL服务中 | 红色 | ❌ | ✅ (限时) | ❌ |
| `discontinued` | 已下架 | 黑色 | ❌ | ❌ | ❌ |
| `ignored` | 已忽略 | 灰色 | ❌ | ❌ | ❌ |

### 2.3 模型状态转换图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           模型状态转换流程                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────┐                                                                │
│  │ pending │ ────── Admin确认 ──────> ┌─────────┐                           │
│  └─────────┘                          │  active │ <─── Admin恢复            │
│       │                               └─────────┘                           │
│       │                                    │                                │
│       │                                    │ Admin暂停                       │
│       │                                    ▼                                │
│       │                               ┌─────────┐                           │
│       │                               │ paused  │ ──── Admin恢复 ───> active │
│       │                               └─────────┘                           │
│       │                                    │                                │
│       └────────────────────────────────────┘                                │
│                                                                            │
│  ┌─────────────────────────────────────────────────────────────────────────┐│
│  │                         EOL 生命周期                                    ││
│  ├─────────────────────────────────────────────────────────────────────────┤│
│  │                                                                         ││
│  │  active ── 供应商通知EOL ──> eol_pending                                ││
│  │                                   │                                      ││
│  │                                   │ EOL日期到达                          ││
│  │                                   ▼                                      ││
│  │                              eol (仍服务,最多1个月)                       ││
│  │                                   │                                      ││
│  │                                   │ 1个月后 或 Admin确认                  ││
│  │                                   ▼                                      ││
│  │                            discontinued (完全下线)                       ││
│  │                                                                         ││
│  └─────────────────────────────────────────────────────────────────────────┘│
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2.4 EOL 详细说明

#### eol_pending (EOL预告)
- **触发条件**: 供应商宣布模型将在某日期EOL
- **业务影响**:
  - ❌ 不可新购买
  - ✅ 继续提供服务
  - ⚠️ 商店显示但标记"即将下线"
- **Admin操作**: 设置 `eol_announced_at` 和 `eol_service_at`

#### eol (EOL服务中)
- **触发条件**: EOL日期到达，从 `eol_pending` 转入
- **业务影响**:
  - ❌ 不可购买
  - ✅ 继续服务 (已有订阅用户)
  - ❌ 商店不显示
  - ⏰ 最多服务1个月后强制停止
- **自动操作**: 设置 `eol_purge_at = eol_service_at + 1 month`

#### discontinued (已下架)
- **触发条件**: 
  - 从 `eol` 自动转入 (1个月后)
  - Admin手动设置
- **业务影响**:
  - ❌ 不可购买
  - ❌ 不可服务
  - ❌ 不可见
- **操作权限**: 仅 Super Admin

---

## 三、定价体系

### 3.1 四项定价标准

| 价格项 | 字段名 | 说明 | 适用模型 |
|--------|--------|------|----------|
| 输入-缓存命中 | `input_price_cache_hit` | 从缓存读取的输入token价格 | 支持缓存的模型 (Claude, GPT-4等) |
| 输入-缓存未命中 | `input_price_cache_miss` | 首次处理的输入token价格 | 所有模型 |
| 输出 | `output_price` | 输出token价格 | 所有模型 |
| 推理 | `reasoning_price` | 推理token价格 | DeepSeek-R1, o1等推理模型 |

### 3.2 定价来源

```
┌─────────────────────────────────────────────────────────────┐
│                      定价数据来源                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  OpenRouter 同步                                            │
│  ├── input_price  ──> input_price_cache_miss (默认)        │
│  └── output_price ──> output_price                         │
│                                                             │
│  Admin 手动设置                                             │
│  ├── input_price_cache_hit   (缓存命中价格)                │
│  ├── input_price_cache_miss  (缓存未命中价格)              │
│  ├── output_price            (输出价格)                    │
│  └── reasoning_price         (推理价格)                    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 3.3 定价计算公式

```javascript
// 成本计算
function calculateCost(pricing, usage) {
  const inputTokens = usage.input - usage.cached;  // 未命中缓存的输入
  const cachedTokens = usage.cached;               // 命中缓存的输入
  const outputTokens = usage.output;
  const reasoningTokens = usage.reasoning || 0;
  
  const inputCost = (inputTokens / 1e6) * pricing.input_price_cache_miss;
  const cacheHitCost = (cachedTokens / 1e6) * pricing.input_price_cache_hit;
  const outputCost = (outputTokens / 1e6) * pricing.output_price;
  const reasoningCost = (reasoningTokens / 1e6) * pricing.reasoning_price;
  
  return {
    total: inputCost + cacheHitCost + outputCost + reasoningCost,
    breakdown: { inputCost, cacheHitCost, outputCost, reasoningCost }
  };
}
```

---

## 四、OpenRouter 同步

### 4.1 同步流程

```
┌─────────────────────────────────────────────────────────────┐
│                   OpenRouter 同步流程                        │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  1. Admin 点击 "同步 OpenRouter"                            │
│     │                                                       │
│     ▼                                                       │
│  2. 调用 OpenRouter API                                     │
│     GET https://openrouter.ai/api/v1/models                 │
│     │                                                       │
│     ▼                                                       │
│  3. 解析模型数据                                            │
│     model.ID = "openai/gpt-4o"                              │
│     │                                                       │
│     ▼                                                       │
│  4. 映射供应商名称                                          │
│     "openai" -> "openai"                                    │
│     "qwen" -> "alibaba"                                     │
│     │                                                       │
│     ▼                                                       │
│  5. 写入 openrouter_models 表 (UPSERT)                      │
│     │                                                       │
│     ▼                                                       │
│  6. 返回同步结果                                            │
│     { "synced": 262, "skipped": 50 }                        │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 4.2 供应商名称映射

```go
providerMapping := map[string]string{
    // OpenRouter名称 -> 标准供应商ID
    "openai":      "openai",
    "anthropic":   "anthropic",
    "google":      "google",
    "meta-llama":  "meta",
    "mistralai":   "mistral",
    "cohere":      "cohere",
    "x-ai":        "xai",
    "deepseek":    "deepseek",
    "perplexity":  "perplexity",
    "amazon":      "amazon",
    "microsoft":   "microsoft",
    "qwen":        "alibaba",
    "alibaba":     "alibaba",
    "baidu":       "baidu",
    "bytedance":   "bytedance",
    "moonshot":    "moonshot",
    "moonshotai":  "moonshot",
    "minimax":     "minimax",
    "zhipu":       "zhipu",
    "tencent":     "tencent",
}
```

---

## 五、模型管理流程

### 5.1 完整流程图

```
┌─────────────────────────────────────────────────────────────┐
│                   模型管理完整流程                           │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  步骤1: 同步 OpenRouter                                     │
│  ┌─────────────────┐                                        │
│  │ 点击 "同步"     │───> openrouter_models (缓存)           │
│  └─────────────────┘                                        │
│                                                             │
│  步骤2: 选择模型添加到正式表                                 │
│  ┌─────────────────┐                                        │
│  │ 浏览缓存模型    │                                        │
│  │ 选择需要的模型  │───> service_models (status=pending)    │
│  │ 点击 "添加"     │                                        │
│  └─────────────────┘                                        │
│                                                             │
│  步骤3: 确认模型配置                                        │
│  ┌─────────────────┐                                        │
│  │ 设置API模型名   │                                        │
│  │ 设置四项价格    │───> service_models (status=active)     │
│  │ 确认启用        │                                        │
│  └─────────────────┘                                        │
│                                                             │
│  步骤4: 日常管理                                            │
│  ┌─────────────────┐                                        │
│  │ 暂停/恢复服务   │                                        │
│  │ 设置EOL预告     │                                        │
│  │ 确认下架        │                                        │
│  └─────────────────┘                                        │
│                                                             │
│  步骤5: 手动添加国内模型                                    │
│  ┌─────────────────┐                                        │
│  │ 点击 "添加模型" │───> service_models                     │
│  │ 填写完整信息    │    (国内供应商模型)                    │
│  └─────────────────┘                                        │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 5.2 操作权限

| 操作 | System Admin | Super Admin |
|------|--------------|-------------|
| 同步 OpenRouter | ✅ | ✅ |
| 添加模型 | ✅ | ✅ |
| 确认模型 | ✅ | ✅ |
| 编辑价格 | ✅ | ✅ |
| 暂停/恢复 | ✅ | ✅ |
| 设置EOL预告 | ✅ | ✅ |
| 确认EOL | ❌ | ✅ |
| 下架模型 | ❌ | ✅ |
| 删除模型 | ❌ | ✅ |

---

## 六、API 端点

### 6.1 供应商 API

```
GET    /api/admin/providers           # 列出所有供应商
POST   /api/admin/providers           # 添加供应商
PATCH  /api/admin/providers/:id       # 更新供应商
DELETE /api/admin/providers/:id       # 删除供应商 (Super Admin)
POST   /api/admin/providers/:id/toggle # 启用/禁用供应商
```

### 6.2 模型 API

```
# OpenRouter 同步
POST   /api/admin/sync-openrouter-live   # 从 OpenRouter 同步模型
GET    /api/admin/openrouter-models      # 列出 OpenRouter 缓存模型

# 模型 CRUD
GET    /api/admin/service-models         # 列出正式模型
POST   /api/admin/models                 # 添加模型到正式表
GET    /api/admin/service-models/:id     # 获取单个模型
PATCH  /api/admin/service-models/:id     # 更新模型
DELETE /api/admin/service-models/:id     # 删除模型 (Super Admin)

# 模型状态管理
POST   /api/admin/service-models/:id/confirm      # 确认模型 (pending -> active)
POST   /api/admin/service-models/:id/pause        # 暂停服务 (active -> paused)
POST   /api/admin/service-models/:id/resume       # 恢复服务 (paused -> active)
POST   /api/admin/service-models/:id/ignore       # 忽略模型 (-> ignored)

# EOL 管理 (Super Admin)
POST   /api/admin/service-models/:id/set-eol-pending  # 设置EOL预告 (active -> eol_pending)
POST   /api/admin/service-models/:id/confirm-eol      # 确认EOL (eol_pending -> eol)
POST   /api/admin/service-models/:id/discontinue      # 下架模型 (-> discontinued)

# 定价管理
PATCH  /api/admin/service-models/:id/pricing    # 更新四项价格
```

---

## 七、前端页面设计

### 7.1 /admin/providers 页面 (System Admin)

**统计卡片:**
- 供应商总数 (providers 数量)
- 已启用 (enabled=true 数量)
- 国际供应商 (type=international 数量)
- 国内供应商 (type=domestic 数量)

**表格列:**
| ID | 名称 | 类型 | Base URL | 密钥数 | 模型数 | 状态 | 操作 |
|----|------|------|----------|--------|--------|------|------|

**操作按钮:**
- 编辑 - 修改名称、类型、Base URL
- 启用/禁用 - 切换 enabled 状态

### 7.2 /super/providers 页面 (Super Admin)

**统计卡片:**
- 供应商总数
- 已启用
- 已禁用

**表格列:**
| ID | 名称 | 类型 | Base URL | 状态 | 操作 |
|----|------|------|----------|------|------|

**操作按钮:**
- 编辑 - 修改名称、类型、Base URL
- 启用/禁用 - 切换 enabled 状态
- 删除 - 删除供应商 (需确认无关联数据)

**添加供应商弹窗:**
- ID (必填，唯一)
- 名称 (必填)
- 类型 (下拉选择: international/domestic)
- Base URL (必填)

### 7.3 /admin/models 页面

**统计卡片:**
- 已同步模型 (openrouter_models 数量)
- 正常服务 (status=active 数量)
- EOL预告 (status=eol_pending 数量)
- 已下架 (status=discontinued 数量)

**筛选器:**
- 供应商下拉框
- 状态下拉框 (全部/正常/暂停/EOL预告/EOL/已下架)

**操作按钮:**
- 同步 OpenRouter
- 添加模型

**表格列:**
| 模型ID | 供应商 | 名称 | 输入(缓存命中) | 输入(缓存未命中) | 输出 | 推理 | 状态 | 操作 |
|--------|--------|------|----------------|------------------|------|------|------|------|

**操作按钮 (根据状态显示):**
- `pending`: 确认、忽略
- `active`: 编辑、暂停、设置EOL预告
- `paused`: 恢复、设置EOL预告
- `eol_pending`: 确认EOL (Super Admin)
- `eol`: 下架 (Super Admin)
- `discontinued`: 删除 (Super Admin)

---

## 八、数据库迁移

需要添加以下字段到 `service_models` 表:

```sql
-- 添加四项定价字段
ALTER TABLE service_models 
  ADD COLUMN IF NOT EXISTS input_price_cache_hit NUMERIC(10,6),
  ADD COLUMN IF NOT EXISTS input_price_cache_miss NUMERIC(10,6),
  ADD COLUMN IF NOT EXISTS reasoning_price NUMERIC(10,6);

-- 重命名现有字段 (如果需要)
ALTER TABLE service_models 
  RENAME COLUMN input_price TO input_price_cache_miss;

-- 添加EOL管理字段
ALTER TABLE service_models
  ADD COLUMN IF NOT EXISTS eol_announced_at TIMESTAMP,
  ADD COLUMN IF NOT EXISTS eol_service_at TIMESTAMP,
  ADD COLUMN IF NOT EXISTS eol_purge_at TIMESTAMP,
  ADD COLUMN IF NOT EXISTS eol_confirmed_by UUID,
  ADD COLUMN IF NOT EXISTS eol_confirmed_at TIMESTAMP,
  ADD COLUMN IF NOT EXISTS eol_notes TEXT;

-- 添加暂停/停止管理字段
ALTER TABLE service_models
  ADD COLUMN IF NOT EXISTS paused_at TIMESTAMP,
  ADD COLUMN IF NOT EXISTS paused_reason TEXT,
  ADD COLUMN IF NOT EXISTS paused_notes TEXT,
  ADD COLUMN IF NOT EXISTS discontinued_at TIMESTAMP,
  ADD COLUMN IF NOT EXISTS discontinued_reason TEXT,
  ADD COLUMN IF NOT EXISTS discontinued_notes TEXT;

-- 添加审批字段
ALTER TABLE service_models
  ADD COLUMN IF NOT EXISTS confirmed_by UUID,
  ADD COLUMN IF NOT EXISTS confirmed_at TIMESTAMP;

-- 添加供应商别名
ALTER TABLE service_models
  ADD COLUMN IF NOT EXISTS provider_aliases JSONB;
```

---

## 九、待实现功能清单

### 9.1 供应商管理 (已完成 ✅)

- ✅ 列出供应商
- ✅ 添加供应商
- ✅ 编辑供应商
- ✅ 启用/禁用供应商
- ✅ 删除供应商 (Super Admin)
- ✅ 修复重复供应商问题

### 9.2 模型管理 (待实现)

- ✅ OpenRouter 同步到 `openrouter_models`
- ❌ 从 `openrouter_models` 选择添加到 `service_models`
- ✅ 模型确认功能 (pending -> active) — `POST /api/admin/service-models/:id/confirm` 与 `POST /api/admin/service-models/:id/auto-confirm` 已实现
- ❌ 四项定价编辑功能
- ❌ 暂停/恢复功能
- ❌ EOL预告设置
- ❌ EOL确认 (Super Admin)
- ❌ 下架功能 (Super Admin)
- ❌ 删除功能 (Super Admin)
- ❌ 手动添加模型功能

### 9.3 实现优先级

**P0 - 必须实现:**
1. 从 openrouter_models 选择添加到 service_models
2. 模型确认功能 (pending -> active)
3. 四项定价编辑功能

**P1 - 重要功能:**
4. 暂停/恢复功能
5. EOL管理功能
6. 下架功能

**P2 - 优化功能:**
7. 批量添加模型
8. 模型健康检查
9. 价格对比 (OpenRouter vs 官方)
