> **ACTIVE** — 2026-05-15

# AccessAPI / New-api 对比分析

基于对 https://provider.example.com (new-api 部署) 的实际 API 调用和代码分析，与我们的 ai-portal-go + ai-proxy-go 项目进行对比，评估借鉴可行性和修改难度。

---

## 1. 倍率计费体系

### AccessAPI (new-api) 的做法

计费公式：`total_cost = (effective_input × model_ratio + output × model_ratio × completion_ratio + cached × model_ratio × cache_ratio + cache_creation × model_ratio × create_cache_ratio + reasoning × model_ratio) / 1M × group_ratio`

核心概念：
- **model_ratio**：模型输入倍率（GPT-3.5 = 0.5, Claude Sonnet = 3.0, GPT-4o = 5.0, Claude Opus = 15.0）
- **completion_ratio**：输出倍率（通常是 3~5x，即输出 token 比输入贵）
- **group_ratio**：组倍率（不同组有不同的价格系数）
- **cache_ratio**：缓存读取折扣（如 0.1 = 10% 价格）
- **create_cache_ratio**：缓存写入加价（如 1.25 = 写入多收 25%）
- **quota_type**：0=按 token 计费，1=按次计费（固定 model_price）

所有价格用倍率表示，最终乘以 `group_ratio` 得到实际价格。1 quota ≈ $0.001。

### 我们的做法

Proxy 端 (`pkg/billing/billing.go`) **已经实现了完全相同的倍率计费**：

```
inputCost = effectiveInput / 1e6 × ModelRatio
outputCost = output / 1e6 × ModelRatio × CompletionRatio
cacheReadCost = cached / 1e6 × ModelRatio × CacheRatio
cacheWriteCost = cacheCreation / 1e6 × ModelRatio × CacheCreationRatio
reasoningCost = reasoning / 1e6 × ModelRatio
```

Portal 端的 `user_groups` 表已有 `pricing_ratio` 字段（默认 1.0000），注释写明：
- `0.85 = economy（折扣组）`
- `1.0 = general（标准组）`
- `1.2 = subscription premium（订阅加价组）`

### 差异

| 项目 | AccessAPI | 我们 |
|------|-----------|------|
| 基础计费 | 倍率制 ✅ | 倍率制 ✅ 已实现 |
| 组倍率 | group_ratio 乘到最终价格 | pricing_ratio 已有字段，但 **未在计费公式中使用** |
| 缓存计费 | cache_ratio + create_cache_ratio | Proxy 已实现 ✅，Portal 有 cache_cost 字段 ✅ |
| 按次计费 | quota_type=1 + model_price | 有 billing_mode='request' 字段，但 **未实现按次计费逻辑** |
| 缓存写入区分 5min/1h | CacheCreation5mRatio / 1hRatio | Proxy 已实现 ✅ |

### 修改难度：低

**pricing_ratio 未接入计费链路** 是最大的缺口。修改点：
1. Portal 的 `/api/admin/pricing/sync` 接口需将 `pricing_ratio` 传给 Proxy（目前 `PortalPricing` 已有 `GroupRatio` 字段 ✅）
2. Proxy 的 `CalculateCostWithCache` 需乘以 `GroupRatio`（目前 `PriceData` 已有 `GroupRatio` 字段 ✅，但 `CalculateCostWithCache` **没有乘 GroupRatio**）
3. Portal 端需根据 Key 的 group_id 查询 pricing_ratio 并传给 Proxy

**只需在 `CalculateCostWithCache` 的 `totalCost` 计算中乘以 `pricing.GroupRatio` 即可。** 这是约 1 行代码的修改。

---

## 2. Group = 定价层级

### AccessAPI 的做法

- 组名即定价层级：`default`、`seed`、`gpt-o-1`~`gpt-o-8`、`claude-3`~`claude-4`、`gemini-1`~`gemini-10` 等
- 每个模型有 `enable_groups` 列表，只有属于这些组的用户才能看到和使用
- 不同组 = 不同模型范围 + 不同价格
- 组倍率在 `/api/pricing` 返回的 `group_ratio` 中体现
- 用户组通过 `user.group` 字段绑定，Token 也继承 `group`

### 我们的做法

`user_groups` 表结构：
- `group_type`: economy / general / subscription / enterprise
- `pricing_ratio`: 0.85 / 1.0 / 1.2（折扣/标准/加价）
- `models`: JSON 数组，允许的模型列表
- `rate_limit_rpm` + 各种 RPM/TPM 限流字段
- `billing_mode`: token / request

### 差异

| 项目 | AccessAPI | 我们 |
|------|-----------|------|
| 组定位 | 组=定价层级+模型范围 | 组=限速+折扣+模型范围 |
| 组数量 | 26+ 组，按供应商/价格细分 | 1 组（default-general） |
| 模型范围 | enable_groups 列表 | models JSON 数组 ✅ |
| 组倍率 | group_ratio 乘到计费 | pricing_ratio 字段已有但未接入计费 |
| 跨组重试 | cross_group_retry | group_priority JSON ✅ 已有字段 |
| 组切换 | 管理员手动分配 | 管理员手动分配 ✅ |

### 关键差异分析

AccessAPI 的组是**商业定价层级**：不同付费等级的用户看到不同模型、享受不同价格。这是典型的 SaaS 分层策略。

我们的组是**功能分组**：economy=折扣组、general=标准组、subscription=订阅组、enterprise=企业组。概念上已经覆盖了，但实际只创建了 1 个 general 组。

**核心差距不在代码架构，而在运营策略**：需要创建多个组（如 `economy-0.85`、`premium-1.2`、`claude-exclusive` 等），配置不同的 pricing_ratio 和 models 范围。

### 修改难度：低

代码架构已完备，只需：
1. 接入 pricing_ratio 到计费链路（上面已分析，1 行代码）
2. 运营上创建多个组并配置不同 pricing_ratio + models
3. 前端展示组信息（my-group 页面已有 ✅）

---

## 3. 缓存计费

### AccessAPI 的做法

- `cache_ratio`：缓存读取折扣（Claude = 0.1 即 10% 价格，OpenAI o3-mini = 0.5 即 50%）
- `create_cache_ratio`：缓存写入加价（Claude = 1.25 即多收 25%）
- 区分 5min 和 1h 缓存写入（不同 ratio）
- 计费公式中明确区分：effective_input（非缓存部分） + cached × cache_ratio + cache_creation × create_cache_ratio

### 我们的做法

**Proxy 端已完整实现缓存计费** ✅：

`pkg/billing/billing.go` 中：
- `CalculateCostWithCache()` 完整实现了 effective_input / cache_read / cache_write / reasoning 的分离计费
- 支持 Claude 的 5min/1h 缓存区分
- `PriceData` 有 `CacheRatio`、`CacheCreationRatio`、`CacheCreation5mRatio`、`CacheCreation1hRatio`
- 默认值：CacheRatio=0.1, CacheCreationRatio=0.25

**Portal 端也已记录** ✅：
- `usage_records` 表有 `cached_input_tokens`、`effective_input_tokens`、`cache_cost`、`cache_hit` 字段
- `input_price_cache_hit` 和 `input_price_cache_miss` 字段在 service_models 表中

### 结论：缓存计费已完整实现，不仅仅是统计

Proxy 端的 `CalculateCostWithCache` 已经在计费公式中区分了缓存读取和写入，并使用了不同的倍率。Portal 端也记录了 cache_cost。

**唯一的缺口**：Portal 端的 `service_models` 表有 `input_price_cache_hit` 和 `input_price_cache_miss`，但这些是固定价格字段，不是倍率。Proxy 端用的是倍率制（CacheRatio），两者是不同计费体系。目前 Proxy 的倍率制是实际生效的计费逻辑，Portal 的固定价格字段主要用于展示。

---

## 4. sidebar_modules 可配置

### AccessAPI 的做法

用户 `setting` 中有 `sidebar_modules` JSON，控制显示哪些模块：
```json
{
  "chat": {"chat": true, "enabled": true, "playground": true},
  "console": {"detail": true, "enabled": true, "log": true, "midjourney": true, "task": true, "token": true},
  "personal": {"enabled": true, "personal": true, "topup": true}
}
```
每个子模块都可单独开关。

### 我们的做法

侧边栏是固定菜单，没有用户可配置的模块开关。

### 修改难度：中

需要：
1. 在 users 表或 user_settings 中添加 sidebar_modules JSON 字段
2. 前端读取配置动态渲染侧边栏
3. 提供设置页面让用户开关模块

这不是核心功能，优先级较低。

---

## 5. cross_group_retry（跨组重试）

### AccessAPI 的做法

Token 有 `cross_group_retry` 字段，当主组渠道失败时，尝试其他组的渠道。

### 我们的做法

`api_keys` 表已有 `group_priority` JSON 字段（组优先级列表），设计意图相同。

### 修改难度：中

`group_priority` 字段已存在，但 Proxy 端的路由逻辑尚未实现跨组 fallback。需要在 Proxy 的渠道选择逻辑中，当主组所有渠道失败时，按 group_priority 顺序尝试其他组的渠道。

---

## 总结：修改难度评估

| 借鉴点 | 代码架构完备度 | 修改难度 | 优先级 |
|--------|---------------|---------|--------|
| pricing_ratio 接入计费 | 字段已有，计费公式未乘 | **低**（1行代码） | P0 |
| 创建多个定价组 | 表结构支持 | **低**（运营配置） | P0 |
| 按次计费(quota_type=1) | billing_mode 字段已有 | **中**（需新增计费逻辑） | P1 |
| 跨组重试 | group_priority 字段已有 | **中**（Proxy 路由逻辑） | P1 |
| sidebar 可配置 | 无 | **中**（新增字段+前端） | P2 |
| Playground/Chat | 无 | **高**（全新功能） | P3 |

**最关键的修改是让 pricing_ratio 生效**：在 Proxy 的 `CalculateCostWithCache` 中乘以 `GroupRatio`，并在 Portal 的 pricing sync 中传递 Key 所属组的 pricing_ratio。这是 1 行核心代码 + 少量数据传递的修改，完成后整个倍率+组折扣计费体系就完整闭环了。

---

## 7. 我们的优势（对比 AccessAPI）

### 7.1 通用模型名路由（wgit-maas-latest）

AccessAPI 用户每次请求必须指定真实模型名（如 `gpt-4o`），模型下线后必须手动修改。

我们支持 `wgit-maas-latest` 通用模型名，用户无需修改代码即可自动切换底层模型。真实模型名仍可使用，提供稳定性保障。这是 AccessAPI 没有的差异化功能。

### 7.2 有序跨组 Fallback

AccessAPI 的 `cross_group_retry` 是简单布尔开关。我们的 `group_priority` 是有序的组优先级列表（最多3组），按优先级逐级 fallback，更精细可控。

### 7.3 用户主动选择模型

AccessAPI 组内所有模型平等可用，当平台有几十个模型时用户体验差。我们要求用户从组模型中主动选择3-5个业务需要的模型，路由范围可控、体验可预期。

### 7.4 Fallback 按实际组计费

AccessAPI 未明确区分跨组计费。我们按实际使用的组计费（pricing_ratio 动态切换），更公平透明，且记录 is_fallback 标记供用户查看。

### 7.5 缓存计费更细致

Proxy 已完整实现缓存计费（包括 5min/1h 区分），比 AccessAPI 更细致。

---

## 附录：关键代码位置

### Proxy 端 (ai-proxy-go)
- 计费公式：`pkg/billing/billing.go:89-149`（CalculateCostWithCache）
- PriceData 结构：`types/relay.go:84-87`（CacheRatio, GroupRatio 等字段）
- 缓存 token 解析：`internal/server/handlers.go:145-165`
- Portal 同步：`pkg/billing/billing.go:237-280`（SyncPricingFromPortal）

### Portal 端 (ai-portal-go)
- 组模型：`migrations/202605061500_user_group_phase1.sql`（pricing_ratio, models, group_type）
- 组 API：`internal/routes/groups.go`（CRUD + pricing_ratio 更新）
- 使用记录：`usage_records` 表（cached_input_tokens, cache_cost, effective_input_tokens）
- 模型定价：`service_models` 表（input_price_cache_hit, input_price_cache_miss）

### New-api 参考代码 (new-api/)
- 计费公式：`model/pricing.go`
- 组模型：`model/group.go`
- Token 模型：`model/token.go`（cross_group_retry, model_limits）
- 前端：`web/src/`（React SPA，Semi UI 组件库）