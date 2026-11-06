# Pricing 架构设计：Usage × Pricing 完全分离

**时间**: 2026-07-02 18:00  
**状态**: 已落地 / 开发完成  
**相关系统**: ai-portal-go, ai-proxy-go

---

## 1. 背景与问题

当前定价信息散落在 `service_models` 表中（`input_price`、`output_price`、`cache_*`、`reasoning_price`、`fixed_price`、`tiered_pricing` 等），同时 ai-proxy-go 里还有一份硬编码的 `DefaultPricing` 作为 fallback。这种混合设计带来几个问题：

1. **模型目录与价格耦合**：修改价格必须进模型编辑页，运营效率低。
2. **多 provider / 多 region 同模型定价困难**：`service_models` 按 `(provider, product_id)` 唯一，价格跟着模型走，难以灵活维护同一模型在不同 provider、不同 region 的价格。
3. **硬编码 fallback 难以维护**：ai-proxy-go `pkg/billing/billing.go` 的 `DefaultPricing` 以及 portal `BillingService` 的 fallback 都是写死的，新增模型需要改代码发版。
4. **阶梯 / 峰谷计价扩展受限**：把 tiered / time-based 规则塞进 `service_models` 的 JSONB 列，未来做组合计价、促销价、地区价会越来越重。
5. **Proxy 预扣费依赖本地价格**：Proxy 的 pre-consume 需要单价做估算，但 `/api/admin/pricing/sync` 目前不存在，Proxy 只能使用硬编码 fallback。

本文提出把 **Pricing 完全独立成一张表**，让 billing 公式变为：

> **Cost = Usage Record × Pricing Rules**

---

## 2. 设计目标

- Pricing 与模型目录解耦，有独立管理页。
- 同一模型在不同 provider、不同 region 可独立定价。
- 支持 flat、tiered、time-based（峰谷）；阶梯/时间窗口内按 component（input/output/cache/reasoning）分别定价。
- 消除代码中的硬编码 fallback，fallback 也来自 DB。
- 提供 CSV 导入导出，方便运营批量维护。
- Proxy 仍能做 pre-consume 估算，但无需掌握完整定价规则。
- 模型删除不强制级联删除价格规则，保留历史定价可追溯。

---

## 3. 非目标

- 不改造 Usage 采集流程：Proxy 继续按现有格式上报 usage。
- 不改造 Quota / Balance 账户体系：Pricing 只决定“单价”，扣费逻辑保持不变。
- 不做实时价格引擎： pricing 规则以分钟级生效为准，满足现有业务即可。

---

## 4. 核心数据模型

### 4.1 `model_pricing_rules`

一条规则代表一个 **(provider, region, model) 的价格方案**，方案内部同时定义 input、output、cache、reasoning、fixed_request 等所有 component 的价格。

```sql
CREATE TABLE model_pricing_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- 匹配维度
    provider VARCHAR(50) NOT NULL,          -- e.g. 'amazon', 'openai', '*' 表示通配
    region VARCHAR(50) DEFAULT '*',         -- e.g. 'us-east-1', 'ap-southeast-1', '*' 表示通配
    product_id VARCHAR(100),                -- 精确匹配 provider 侧的 model id
    api_model_name VARCHAR(100),            -- 精确匹配对外暴露的 model name

    -- 价格方案类型
    billing_mode VARCHAR(30) NOT NULL,      -- flat | tiered | time_based

    -- flat 价格：所有 component 的统一单价（$/1M tokens 或 $/request）
    prices JSONB,                           -- {input, output, cache_hit, cache_miss, reasoning, fixed_request}

    -- tiered 价格：每个 tier 内包含所有 component 的单价
    tiers JSONB,                            -- [{max_tokens, prices:{input, output, cache_hit, ...}}, ...]

    -- time_based 价格：每个时间窗口内包含所有 component 的单价
    time_rules JSONB,                       -- [{days:[1,2,3,4,5], start:"09:00", end:"23:00", prices:{input, output, ...}}, ...]
    timezone VARCHAR(50) DEFAULT 'UTC',

    currency VARCHAR(10) DEFAULT 'USD',

    -- 生命周期
    effective_from TIMESTAMP DEFAULT NOW(),
    effective_to TIMESTAMP,                 -- NULL 表示永久有效
    priority INT DEFAULT 0,                 -- 同维度冲突时高优先生效
    status VARCHAR(20) DEFAULT 'active',    -- active | disabled

    -- 元数据
    rule_name VARCHAR(255),
    note TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_model_pricing_rules_match ON model_pricing_rules(provider, region, product_id, api_model_name, status);
CREATE INDEX idx_model_pricing_rules_time ON model_pricing_rules(effective_from, effective_to);
```

### 4.2 匹配规则

计费时按以下顺序匹配：

1. `provider = 实际 provider` 且 `region = 实际 region` 且 (`product_id = ...` 或 `api_model_name = ...`)
2. `provider = 实际 provider` 且 `region = '*'` 且 (`product_id = ...` 或 `api_model_name = ...`)
3. `provider = '*'` 且 `region = '*'` 且 (`product_id = ...` 或 `api_model_name = ...`)
4. 仍未命中，使用 DB 中的全局 fallback 规则

同一维度存在多条规则时，按 `priority DESC, effective_from DESC` 取第一条。

### 4.3 计价语义

- **flat**: 直接使用 `prices` 中各 component 的单价。
- **tiered**: 按 `tiers` 数组分段累计计费；每个 tier 的 `prices` 对象同时定义 input、output、cache 等价格。
- **time_based**: 根据请求时间匹配 `time_rules` 中的窗口，使用该窗口内的 `prices`。
- **fixed_request**: `prices.fixed_request` 表示每次请求的固定价格，与 token 数无关。

计费公式：

```
total = input_cost + output_cost + cache_hit_cost + cache_miss_cost + reasoning_cost + fixed_cost
```

每个 component 独立在 tier 或 time window 内计算，最后再求和。

---

## 5. BillingService 改造

Portal `internal/services/billing.go` 改为：

```go
func (s *BillingService) CalculateCost(ctx context.Context, record UsageRecord) int64 {
    rule := s.matchPricingRule(ctx, record.Provider, record.Region, record.Model, record.Timestamp)
    if rule == nil {
        rule = s.fallbackRule(ctx) // 来自 DB，不再硬编码
    }

    prices := s.resolvePrices(rule, record.Timestamp)

    inputCost  := calculateComponentCost(record.EffectiveInputTokens, prices.input)
    outputCost := calculateComponentCost(record.OutputTokens,        prices.output)
    cacheHit   := calculateComponentCost(record.CachedTokens,        prices.cache_hit)
    cacheMiss  := calculateComponentCost(record.CacheCreationTokens, prices.cache_miss)
    reasoning  := calculateComponentCost(record.ReasoningTokens,      prices.reasoning)
    fixedCost  := prices.fixed_request

    return USDtoQuota(inputCost + outputCost + cacheHit + cacheMiss + reasoning + fixedCost)
}
```

- `matchPricingRule` 按 `(provider, region, model)` 匹配，支持 `*` 通配。
- `resolvePrices` 根据 `billing_mode` 解析出最终每个 component 的单价：
  - `flat`: 直接返回 `prices`。
  - `tiered`: 对 `tiers` 中每个 component 分别做分段累计。
  - `time_based`: 根据 `record.Timestamp` 和 `timezone` 匹配时间窗口，返回窗口内的 `prices`。
- 规则变更后缓存 1-5 分钟 TTL。
- 删除 Portal `BillingService` 中写死的 fallback。

---

## 6. Proxy 价格同步策略

### 6.1 Proxy 为什么需要价格？

Proxy 的 pre-consume 需要在请求上游前估算输入 token 费用，向 Portal 申请冻结余额。如果 Proxy 完全不做本地估算，每次 pre-consume 都变成一次 RPC 让 Portal 算，会增加 Portal 压力和延迟。

### 6.2 推荐方案：同步“简化价格视图”

Portal 暴露现有接口（但补齐实现）：

```http
GET /api/admin/pricing/sync
Header: x-proxy-secret: <secret>
```

返回一个轻量数组，每个 `(provider, region, model)` 只保留预扣费所需的**保守单价**（按 component 分别给出）：

```json
{
  "pricing": [
    {
      "provider": "amazon",
      "region": "us-east-1",
      "model": "claude-3-opus",
      "inputPricePer1M": 15.00,
      "outputPricePer1M": 75.00,
      "cacheHitPricePer1M": 1.50,
      "cacheMissPricePer1M": 15.00,
      "reasoningPricePer1M": 15.00,
      "fixedPrice": 0.00,
      "billingType": "flat"
    }
  ],
  "syncedAt": "2026-07-02T18:00:00Z"
}
```

- Proxy 按 `(provider, region, model)` 查找；找不到 region 精确规则时使用 `region='*'` 的通配规则。
- 对于 tiered / time-based 规则，Portal 在生成 sync 视图时计算每个 component 的 **worst-case（最高档/最贵窗口）单价**给 Proxy 做保守冻结；最终精确计费仍由 Portal 在 usage 上报后按完整规则重算。

### 6.3 Proxy 改造

1. 删除 `DefaultPricing` 中的硬编码模型列表，改为：
   - 启动时从 Portal sync 一次。
   - 每 60s 定时 sync。
   - sync 失败时使用上次缓存；如果连缓存都没有，则**fail-open**（允许请求，estimatedCost=0），避免新模型无法使用。
2. `GetPricing` 不再返回硬编码 fallback，仅返回缓存中的 Portal 数据。
3. `estimateInputCost` 使用 sync 视图中的 `inputPricePer1M * margin`。

这样 Proxy 不需要理解 tier、time、component 的复杂规则，pricing 表再大也不会影响 Proxy 内存或同步复杂度。

---

## 7. Fallback 处理

完全消除代码硬编码 fallback：

1. 在 `model_pricing_rules` 中插入一条全局 fallback：

   ```sql
   INSERT INTO model_pricing_rules (provider, region, billing_mode, prices, rule_name, priority)
   VALUES ('*', '*', 'flat',
           '{"input": 1.0, "output": 3.0, "cache_hit": 0.1, "cache_miss": 1.0, "reasoning": 1.0, "fixed_request": 0}',
           'Global fallback', -100);
   ```

2. Portal `BillingService` 和 Proxy sync 接口在找不到具体规则时，返回这条 fallback。
3. 管理员可在定价页随时调整 fallback，无需发版。

---

## 8. 模型删除与定价规则生命周期

由于 pricing 已独立，**不建议对 `service_models` 删除做级联硬删除定价规则**。

推荐策略：

- `service_models` 删除后，相关 `model_pricing_rules` 因匹配不到模型而自然失效（inert）。
- 保留历史 pricing 可用于：账单对账、审计、恢复模型后重新启用。
- 提供后台任务或管理按钮：**清理已失效 pricing 规则**（30/90 天未匹配到任何模型时软删除或归档）。
- 若需要强一致性，可在删除模型时弹窗询问：“是否同步删除关联的 X 条定价规则？”

---

## 9. 管理页面与 CSV

### 9.1 独立页面 `/admin/pricing`

- 列表：按 provider / region / model / billing_mode / status 筛选
- 新建/编辑：
  1. 选择 `provider`（支持 `*`）、`region`（支持 `*`）、`product_id` / `api_model_name`
  2. 选择 `billing_mode`
  3. 动态表单：
     - **flat**: 直接填写 input / output / cache_hit / cache_miss / reasoning / fixed_request 单价
     - **tiered**: 增删 tier 行，每行填 `max_tokens` 和该 tier 内所有 component 的单价
     - **time_based**: 增删时间窗口，每行填 days / start / end / 该窗口内所有 component 的单价
- 规则启用/禁用
- 删除（软删或硬删，可配置）
- 显示“关联模型”链接

### 9.2 CSV 导入导出

导出列示例：

```csv
provider,region,product_id,api_model_name,billing_mode,prices,tiers,time_rules,timezone,currency,effective_from,effective_to,priority,status,rule_name
*,*,*,*,flat,"{input:1.0,output:3.0,cache_hit:0.1,cache_miss:1.0,reasoning:1.0,fixed_request:0}",,,UTC,USD,2026-01-01,,-100,active,Global fallback
amazon,us-east-1,us.anthropic.claude-3-opus-20240229-v1:0,claude-3-opus,flat,"{input:15.0,output:75.0,cache_hit:1.5,cache_miss:15.0,reasoning:15.0,fixed_request:0}",,,UTC,USD,2026-01-01,,100,active,AWS Opus US-East-1 Flat
amazon,us-east-1,us.anthropic.claude-3-opus-20240229-v1:0,claude-3-opus,tiered,,"[{max_tokens:100000000,prices:{input:15.0,output:75.0,cache_hit:1.5,cache_miss:15.0}},{max_tokens:null,prices:{input:12.0,output:60.0,cache_hit:1.2,cache_miss:12.0}}]",,UTC,USD,2026-01-01,,100,active,AWS Opus US-East-1 Tiered
deepseek,*,,deepseek-v4,time_based,,"","[{days:[1,2,3,4,5],start:"09:00",end:"23:00",prices:{input:1.0,output:3.0}},{days:[1,2,3,4,5,6,0],start:"23:00",end:"09:00",prices:{input:0.5,output:1.5}}]",Asia/Shanghai,USD,2026-06-01,,100,active,DeepSeek V4 Peak/Off-peak
```

导入支持：

- insert or update（以 `provider, region, product_id, api_model_name, rule_name` 为联合键）
- 预览模式：先显示将要新增/更新/忽略的行数
- 错误行单独返回，不影响其他行

---

## 10. 实施阶段（建议）

因为系统尚未上线，**无需迁移旧数据**，可以直接替换。

### Phase 1: 数据层 + BillingService

1. 创建 `model_pricing_rules` 表（含 `region`、`prices`、`tiers`、`time_rules`）。
2. 插入全局 fallback 规则。
3. 重写 Portal `BillingService`：从 `model_pricing_rules` 读取，移除硬编码 fallback。
4. 移除 `service_models` 中后续不再直接用于计费的字段（保留 `tiered_pricing` 过渡，或一次性移除）。
5. 单元测试覆盖 flat / tiered / time-based / fallback / region 匹配。

### Phase 2: Proxy 同步

1. Portal 实现 `/api/admin/pricing/sync`，返回 `(provider, region, model)` 维度的保守单价。
2. Proxy 删除 `DefaultPricing` 硬编码，改为 sync 缓存 + fail-open。
3. Proxy pre-consume 使用 sync 视图中的保守单价。

### Phase 3: 管理页面 + CSV

1. 新建 `/admin/pricing` 页面，支持 CRUD。
2. CSV 导入导出。
3. 在 `admin/models-aws` 和 `admin/models-general` 中移除价格编辑，改为“定价规则”入口。

### Phase 4: 清理

1. 删除 `service_models` 中已迁移到 pricing 表的冗余字段。
2. 删除 Portal 中之前为 inline pricing 写的过渡代码。

> **实施状态**: Phase 1 ~ Phase 3 已完成并 push 到 main 分支；Phase 4 中 UI 层的价格字段已移除，`service_models` 表的物理列保留以备审计，后续可再清理。

---

## 11. 开发状态与已确认细节

### 11.1 已完成的实现

| 模块 | 状态 | 关键提交 / 文件 |
|---|---|---|
| `model_pricing_rules` 表 + 全局 fallback | ✅ | `internal/db/schema.go`、`internal/db/seed.go` |
| `PricingService`（flat/tiered/time-based） | ✅ | `internal/services/pricing.go` |
| `BillingService` 委托 PricingService 计费 | ✅ | `internal/services/billing.go` |
| `/api/admin/pricing` CRUD + sync 端点 | ✅ | `internal/routes/admin_pricing.go` |
| `/admin/pricing` 管理页面 | ✅ | `web/templates/admin-pricing.html`、`internal/web/handler.go` |
| CSV 导入导出 | ✅ | `internal/services/pricing.go`、`internal/routes/admin_pricing.go` |
| Proxy 同步绝对单价 | ✅ | `ai-proxy-go/pkg/billing/billing.go`、`cmd/server/main.go` |
| 移除模型页价格输入 | ✅ | `web/templates/admin-models-general.html`、`admin-models-aws.html` |
| 回归测试脚本 | ✅ | `scripts/run-pricing-ci-tests.sh` |

### 11.2 Priority 规则（已确认并上线）

- **生效范围**: `-1000 ~ 1000`，超出范围保存会被拒绝。
- **比较方向**: **数字越大越优先**；全局 fallback 使用 `-100`。
- **作用**: 同一 `(provider, region, model)` 维度存在多条规则时，高 Priority 规则胜出；与 `admin/models` 页面 **Model Routing Config** 的 Priority（数字越小越优先，决定请求走哪个 provider）完全独立。
- **匹配打分**: 精确匹配 provider/region/model 各加 100 分，Priority 每 1 分加 10 分，最终取最高分规则。

### 11.3 Status 控制（已调整）

- 编辑抽屉中**不再包含 Status 下拉框**。
- 列表每行操作列增加 **Activate / Deactivate** 按钮，调用 `POST /api/admin/pricing/:id/toggle`。

### 11.4 Rule Name（已明确）

- 仅作为人类可读标签，不影响计费。
- 留空时自动生成：`provider/region/model`。

### 11.5 Time-based 编辑格式（已确认）

- UI 布局改为两行：
  - 第一行：`Days` + `Start` + `End`
  - 第二行：`Input` / `Output` / `Cache Hit` / `Cache Miss` / `Reasoning`
- `Days` 格式：逗号分隔的 `0-6` 数字，`0=周日`，`6=周六`。
- 已加客户端和服务端校验。

### 11.6 模型管理页价格字段（已移除）

- `admin/models` 和 `admin/models-aws` 的新增/编辑/确认抽屉中**不再显示价格输入框**。
- 表格中不再展示价格列。
- 抽屉内显示提示：*“Pricing is managed separately in Pricing Rules”*。
- 后端确认接口 (`POST /api/admin/service-models/:id/confirm`) 的价格字段改为可选，使用 `COALESCE` 保留已有值，避免误清空。

---

## 12. 需要决策的点

1. **通配符语法**：`provider = '*'` 和 `region = '*'` 都允许。同一模型在不同 provider 可各自定价，也可以设一条 `provider='*'` 的默认价。
2. **阶梯计价粒度**：每个 tier 内同时包含 input / output / cache / reasoning 价格，而不是只针对单一 component。
3. **峰谷计价粒度**：每个时间窗口内也同时包含所有 component 价格，与阶梯计价保持一致。
4. **时间规则精度**：到小时足够，还是到分钟？建议到分钟。
5. **Proxy fail-open 还是 fail-closed**：建议 pre-consume fail-open（不阻断请求），但记录告警；billing 阶段在 Portal 按 fallback 计价。
6. **多规则冲突**：同一 (provider, region, model) 维度只取一条规则（priority 最高），规则内部覆盖所有 component，不叠加。
7. **Currency**：现在是否只需要 USD？建议表结构支持 currency，默认 USD。

---

## 13. 结论

把 Pricing 提升为独立实体是解决当前模型目录与价格耦合、硬编码 fallback、阶梯/峰谷计价的根本方案。实施上可分为数据层 → Proxy 同步 → 管理页 → 清理四步走；由于系统未上线，不需要历史数据迁移，改造风险可控。
