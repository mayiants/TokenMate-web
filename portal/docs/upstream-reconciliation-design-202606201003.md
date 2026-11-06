# Upstream Provider 对账页面设计

> **✅ IMPLEMENTED** — 页面 `/admin/reconciliation-ops` 已上线，含 summary cards / trend chart / detail table / CSV export
> 文档编号：`upstream-reconciliation-design-202606201003`  
> 创建时间：2026-06-20 10:03 UTC  
> 对应页面：`/admin/reconciliation-ops`

---

## 1. 背景与目标

### 1.1 现状痛点

`/admin/reconciliation` 已覆盖「客户侧对账与发票审核」，但缺少「上游 Provider / Key」维度的对账视图：

| 业务视角 | 数据表 | 当前 Admin 可见性 |
|----------|--------|------------------|
| 上游实际调用（Provider 真实消耗） | `upstream_key_usage` | 仅 Upstream Monitor 列表，无对账视角 |
| 向下游计费（Portal 账单） | `usage_records` | 仅 Usage Logs / Billing，无上游对齐 |
| 上游成本估算 | `service_models` 单价 | 未用于对账 |

### 1.2 目标

新增独立页面 **「上游 Provider 对账」**（`/admin/reconciliation-ops`），实现：

1. **时间范围灵活**：24h / 7d / 30d / 90d 一键切换 + 自定义日期。
2. **总览卡片**：上游请求数、上游 Token 数、下游计费金额、上游成本估算、差额、错误率。
3. **趋势图**：按天展示 Requests / Tokens / Cost / Error Rate 双轴对比（上游 vs 下游）。
4. **明细对账表**：按 Provider / Model / Upstream Key 分组，展示请求、Token、成本、差值。
5. **导出 CSV**：支持按当前筛选条件导出明细。

---

## 2. 数据源与口径

### 2.1 核心数据表

| 表 | 粒度 | 关键字段 | 用途 |
|----|------|---------|------|
| `upstream_key_usage` | 日级 / key / model | `provider`, `model`, `date`, `upstream_key_id`, `request_count`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `error_count` | 上游真实消耗 |
| `usage_records` | 每次调用 | `provider`, `model`, `upstream_key_id`, `request_count`, `input_tokens`, `output_tokens`, `cached_input_tokens`, `total_cost`, `created_at` | 下游计费依据 |
| `service_models` | model 定价 | `provider`, `api_model_name`, `input_price_cache_miss`, `output_price`, `cache_read_price`, `currency` | 上游成本估算 |
| `upstream_keys` | key 元数据 | `id`, `provider`, `key_preview` | key 可读展示 |

### 2.2 口径定义

- **上游请求数** = `SUM(upstream_key_usage.request_count)`
- **上游 Input Tokens** = `SUM(input_tokens + cache_read_tokens)`（含缓存命中读取）
- **上游 Output Tokens** = `SUM(output_tokens)`
- **上游错误数** = `SUM(error_count)`
- **上游错误率** = `error_count / request_count`
- **上游成本估算** = `input_tokens / 1e6 * input_price_cache_miss + output_tokens / 1e6 * output_price + cache_read_tokens / 1e6 * cache_read_price`
  - 若 `service_models` 缺少单价，则该行成本显示为 `-`（0 美元），避免误导。
- **下游计费金额** = `SUM(usage_records.total_cost)`（micro-USD quota → USD）
- **下游请求数** = `SUM(usage_records.request_count)`
- **差额（请求 / Token / 成本）** = `上游 - 下游`

> 说明：下游成本 Portal 计费通常包含 pricing ratio / 赠金抵扣 / 订阅抵扣，因此「上游成本估算」与「下游计费金额」天然存在差异。本页面的价值是**量化差异**，便于发现异常 key 或重复计费。

---

## 3. 时间范围设计

页面提供两种方式：

| 快捷选项 | 含义 | 明细聚合粒度 | 趋势聚合粒度 |
|----------|------|-------------|-------------|
| 24h | 当前时间前 24 小时 | 日（ today/yesterday 两天） | 日 |
| 7d | 今天 0 点起往前 7 天 | 日 | 日 |
| 30d | 今天 0 点起往前 30 天 | 日 | 日 |
| 90d | 今天 0 点起往前 90 天 | 日 | 日 |
| 自定义 | `date_from` ~ `date_to`（含起止） | 日 | 日 |

- 快捷选项对应的实际区间由后端统一计算，前端只传 `period`。
- 自定义模式由前端传 `date_from` + `date_to`。
- 所有时间均按 UTC 处理，界面上显示 `YYYY-MM-DD`。

---

## 4. API 设计

### 4.1 GET `/api/admin/reconciliation-ops/summary`

参数：`period`, `date_from`, `date_to`

返回：
```json
{
  "period": "7d",
  "date_from": "2026-06-13",
  "date_to": "2026-06-19",
  "upstream": {
    "requests": 1200000,
    "input_tokens": 800000000,
    "output_tokens": 300000000,
    "errors": 1200
  },
  "billed": {
    "requests": 1195000,
    "input_tokens": 795000000,
    "output_tokens": 298000000,
    "cost_usd": "1234.56"
  },
  "estimated_upstream_cost_usd": 1150.00,
  "cost_diff_usd": -84.56,
  "error_rate": 0.10
}
```

### 4.2 GET `/api/admin/reconciliation-ops/trend`

参数：`period`, `date_from`, `date_to`, `metric=requests|tokens|cost|error_rate`

返回：
```json
{
  "metric": "cost",
  "labels": ["06-13", "06-14", ...],
  "upstream": [110.0, 120.0, ...],
  "billed": [115.0, 118.0, ...]
}
```

### 4.3 GET `/api/admin/reconciliation-ops/detail`

参数：`period`, `date_from`, `date_to`, `provider`, `model`, `key_search`, `diff_threshold`, `page`, `limit`

返回：
```json
{
  "rows": [
    {
      "provider": "openai",
      "model": "gpt-4o",
      "upstream_key_id": "uuid",
      "key_preview": "sk-...abc",
      "upstream_requests": 5000,
      "billed_requests": 4980,
      "request_diff": 20,
      "upstream_input_tokens": 2000000,
      "billed_input_tokens": 1980000,
      "input_token_diff": 20000,
      "upstream_output_tokens": 800000,
      "billed_output_tokens": 795000,
      "output_token_diff": 5000,
      "upstream_cost_usd": 80.00,
      "billed_cost_usd": 95.00,
      "cost_diff_usd": -15.00,
      "error_rate": 0.02
    }
  ],
  "total": 42,
  "page": 1,
  "limit": 20,
  "total_pages": 3
}
```

### 4.4 GET `/api/admin/reconciliation-ops/export`

CSV 导出，字段与明细表一致。

---

## 5. 页面布局

```
┌─────────────────────────────────────────────────────────────┐
│  Upstream Provider Reconciliation                           │
├─────────────────────────────────────────────────────────────┤
│ [24h] [7d] [30d] [90d] [Custom 📅]        [Auto Refresh] [↻]│
├─────────────────────────────────────────────────────────────┤
│  Summary Cards                                              │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐       │
│  │ Upstream │ │ Billed   │ │ Est Cost │ │ Diff     │       │
│  │ Requests │ │ Requests │ │          │ │          │       │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘       │
├─────────────────────────────────────────────────────────────┤
│  Trend Chart (上游 vs 下游)                                  │
├─────────────────────────────────────────────────────────────┤
│  Provider ▼  Model ▼  Search key...  Diff > 0 □  [Export]   │
├─────────────────────────────────────────────────────────────┤
│  Detail Table                                               │
│  Provider | Model | Key | Upstream/Billed Requests | ...    │
└─────────────────────────────────────────────────────────────┘
```

### 5.1 明细表字段

| 字段 | 说明 |
|------|------|
| Provider | 上游 Provider |
| Model | 模型名 |
| Key Preview | `upstream_keys.key_preview` |
| Upstream Requests | 上游实际请求 |
| Billed Requests | 下游计费请求 |
| Request Diff | 差额（上游 - 下游） |
| Upstream Input Tokens | 上游 input（含 cache read） |
| Billed Input Tokens | 下游 input |
| Input Diff | 差额 |
| Upstream Cost (est.) | 基于 `service_models` 估算 |
| Billed Cost | `usage_records.total_cost` 转 USD |
| Cost Diff | 差额 |
| Error Rate | 上游错误率 |

---

## 6. 实现要点

### 6.1 后端

- 新增 `internal/query/reconciliation_ops.go`：
  - `ParseReconciliationPeriod(period, dateFrom, dateTo) (from, to time.Time, err error)`
  - `UpstreamReconciliationSummary(ctx, db, from, to)`
  - `UpstreamReconciliationTrend(ctx, db, from, to, metric)`
  - `UpstreamReconciliationDetail(ctx, db, from, to, filters)`
- 新增 `internal/routes/admin_reconciliation_ops.go`：
  - `getUpstreamReconciliationSummary`
  - `getUpstreamReconciliationTrend`
  - `getUpstreamReconciliationDetail`
  - `exportUpstreamReconciliation`
- 在 `internal/routes/admin_common.go` 注册上述接口。

### 6.2 前端

- 新增 `web/templates/admin-reconciliation-ops.html`。
- 风格与 `/admin/upstream-ops` 保持一致：Summary Cards、Controls、Trend Chart、Data Table。
- 趋势图复用简单 SVG 折线图（不引入新依赖）。
- 提供 Provider / Model 下拉（通过 `/api/admin/providers` 与 `/api/admin/models/grouped` 获取）。

### 6.3 路由与导航

- 页面路由：`/admin/reconciliation-ops`
- 接口前缀：`/api/admin/reconciliation-ops/*`
- Sidebar 新增入口：「Upstream Reconciliation」（位于「Upstream Operations」下方）。

---

## 7. 注意事项与后续优化

1. **成本估算精度**：当前使用 `service_models` 中的公开单价估算上游成本，未包含 Provider 折扣、缓存写入、batch 折扣等。该列仅作参考，不应作为财务结算依据。
2. **数据缺失**：若某 Provider/Model 在上游表存在但计费表缺失（或反之），仍展示，缺失侧显示 0。
3. **性能**：`upstream_key_usage` 已按 `date` 建立索引；`usage_records` 已按 `created_at` 分区。90d 查询在测试环境应可接受，若数据量增大可进一步按 Provider/Model 做物化视图。
4. **自定义日期**：前端使用原生 `<input type="date">`，提交时格式化为 `YYYY-MM-DD`。
