# Portal 统计与计费页面 UI/UX 优化方案

> 分析对象：`/opt/ai-platform/workspace/apiweb.png`  
> 日期：2026-07-16

---

## 1. 图片来源判断

### 结论
这张图**不是 CC Switch**，也**不是 CCDash**，而是一张 **LLM API 网关的管理后台（数据看板 / 使用日志）截图**。

最可能属于以下产品之一：

- **New API / VoAPI**（开源/二开 API 网关）
- **Fenno / AccessAPI** 等商业 API 聚合平台
- 某内部或私有化部署的 API 网关控制台

### 判断依据

| 特征 | 说明 |
|------|------|
| 页面结构 | 顶部 KPI / 中部图表 / 底部明细表，典型网关运营后台 |
| 中文术语 | “模型分布”“端点分布”“分组使用分布”“Token 使用趋势”“按 Token / 按实际消费”“计费类型”“计费模式” |
| 字段维度 | API 密钥前缀、模型、端点、IP、分组、流式/非流式、按量计费、延迟（首字/总耗时） |
| 数据粒度 | 以请求日志为原子，支持按多维度聚合，符合 API 网关特征 |

CC Switch 本质是**配置切换器**（管理 Claude Code / Codex 的 provider 配置），CCDash 是**个人 Claude Code 会话分析器**，两者都不会出现“API 密钥”“端点”“分组”“计费模式”这类面向多租户网关的运营字段。

> 因此：这张图更适合作为 **System Admin / 运营后台** 的参考，而不是直接照搬给普通用户。

---

## 2. 信息密度评估

### 优点

- **一屏信息完整**：模型、端点、分组、趋势、明细全部在一个页面可见，定位问题快。
- **对比意识强**：实际费用 vs 标准费用、首字延迟 vs 总耗时，便于发现异常。
- **可视化丰富**：Donut 图展示占比，折线图展示趋势，表格展示明细，三者互补。
- **工具感强**：筛选、刷新、重置、列设置、导出 CSV 一应俱全。

### 缺点

- **信息密度过高**：深色背景 + 大量数字 + 多个图表并排，长时间查看容易疲劳（“看花眼”）。
- **颜色对比强**：绿色费用、蓝色徽章、紫色折线同时出现，视觉焦点分散。
- **术语专业**：普通用户可能不理解“按量”“流式”“首字延迟”“Cache Creation”等概念。
- **缺乏层级**：所有模块看起来同等重要，没有明显的“先看什么、后看什么”的引导。

### 适用对象判断

| 用户角色 | 适用度 | 原因 |
|----------|--------|------|
| **System Admin / 平台运营** | ⭐⭐⭐⭐⭐ | 需要全局可观测、多维度下钻、导出对账 |
| **Enterprise Admin / 团队管理员** | ⭐⭐⭐ | 需要分组/成员成本，但不需要端点/IP 等过细字段 |
| **普通用户 / 开发者** | ⭐⭐ | 更关注余额、今日消费、最近调用，信息过载反而干扰决策 |

### 对 Portal 的启示

**不要照搬这张图**。应该把它拆成三层：

1. **User 层**：精简，只看“我花了多少、什么模型最贵、最近调用”。
2. **Enterprise Admin 层**：增加团队/分组/成员维度，关注预算和异常。
3. **System Admin 层**：保留全部维度，支持对账、导出、深度分析。

---

## 3. 按角色页面的优化建议

### 3.1 普通用户（User）— Dashboard / Usage / Billing

#### 目标
- 3 秒内知道余额和今日消费。
- 快速定位“哪个 Key / 哪个模型”在烧钱。
- 充值入口清晰。

#### 从 apiweb.png 可借鉴

| 模块 | 当前 Portal | 建议优化 |
|------|-------------|----------|
| **KPI 卡片** | 已有 stats-bar，但风格不统一 | 统一卡片样式，数字用 K/M/B 缩写，费用用绿色，趋势用红/绿 badge |
| **趋势图** | Enterprise Analytics 分三个独立 bar chart | 合并为单张多系列折线图：Requests / Tokens / Spend，默认 7 天 |
| **模型分布** | usage-logs 有 model 分布表格 | 增加 donut + 排行榜卡片，只看 Top 5 模型 |
| **最近调用** | usage-logs 表格较简单 | 增加最近 5 条摘要卡片：模型、费用、延迟、时间 |
| **充值页** | 支付方式按钮 | 增加金额快捷选择卡片、余额进度条、最近充值记录 |

#### 应避免
- 不要把端点分布、IP 归属地、Cache Creation 等字段暴露给普通用户。
- 不要在一个页面放超过 3 个图表。

---

### 3.2 企业管理员（Enterprise Admin）— Enterprise Dashboard / Usage & Reconciliation

#### 目标
- 掌握团队整体用量和预算消耗。
- 识别高消费成员 / 分组 / 模型。
- 发现异常调用（如某个 Key 费用突增）。

#### 从 apiweb.png 可借鉴

| 模块 | 当前 Portal | 建议优化 |
|------|-------------|----------|
| **分组使用分布** | Enterprise Dashboard 只有整体趋势 | 增加“按分组”Donut + 表格，显示各分组的请求/Token/费用 |
| **成员排行** | 当前 Top Users 只有本月累计 | 增加时间范围切换（1d/7d/30d/quarter），支持按费用/请求/Token 排序 |
| **模型分布** | 无 | 增加 Top Models 卡片，帮助企业识别哪个模型最贵 |
| **预算进度** | Credit Limit 进度条已有 | 增加多阈值告警色（<60% 绿 / <80% 黄 / ≥80% 红） |
| **费用趋势** | Analytics Spend tab | 叠加“预算上限”参考线，直观显示是否会超支 |
| **明细筛选** | Enterprise Usage 页面筛选较弱 | 增加模型、成员、分组、Key、计费模式筛选 + 导出 CSV |

#### 应避免
- 不要把“端点”“IP”“上游渠道”等运营字段放给企业管理员。
- 不要把缓存命中率、推理强度等技术人员字段作为默认列。

---

### 3.3 系统管理员（System Admin）— Admin Dashboard / Usage Logs / Reconciliation

#### 目标
- 全局可观测：请求、Token、费用、延迟、错误、缓存。
- 成本对账：上游实际成本 vs 下游用户扣费。
- 异常定位：慢请求、高成本请求、模型掉包、渠道故障。

#### 从 apiweb.png 可借鉴

| 模块 | 当前 Portal | 建议优化 |
|------|-------------|----------|
| **模型分布** | admin dashboard 有 Top Models bar chart | 改为 donut + 表格，显示请求/Token/实际费用/标准费用/毛利率 |
| **端点分布** | 无 | 新增：按 `/v1/chat/completions`、`/v1/responses` 等端点聚合 |
| **Token 使用趋势** | 单一折线或 bar chart | 多系列折线：Input / Output / Cache Read / Cache Hit Rate（双 Y 轴） |
| **延迟分布** | monitor-upstream 有健康状态 | 增加 P50/P90/P99 延迟趋势 + 慢请求排行 |
| **使用日志表** | usage-logs 表格字段较少 | 增加端点、IP+地区、分组、类型、计费模式、TTFT/总耗时、状态 |
| **筛选栏** | 只有日期范围 | 增加模型、Key、分组、成员、端点、计费模式、状态、IP 筛选 |
| **导出 CSV** | 无 | 支持按当前筛选条件导出明细 |
| **对账** | admin/reconciliation 已有 Upstream vs Billed | 增加 cache hit/write/reasoning 各组件成本拆分、模型毛利率表 |

#### 需要 proxy 配合的新字段

| 字段 | 用途 | 改造难度 |
|------|------|----------|
| `endpoint` | 端点分布 | 中（proxy 写入 usage_records） |
| `client_ip` / geo | IP 归属地 | 中（proxy 写入 + GeoIP 解析） |
| `ttft_ms` | 首字延迟 | 中（流式响应统计） |
| `reasoning_intensity` | 推理强度标识 | 中（模型/请求元数据） |
| `cache_creation_tokens` | Cache Creation | 低-中（部分上游已返回） |

---

## 4. 具体页面改造方案

### 4.1 Dashboard（所有角色）

#### 布局建议

```
┌─────────────────────────────────────────────┐
│  KPI 卡片 × 4（余额 / 今日请求 / 今日 Token / 今日消费）  │
├────────────────────────┬────────────────────┤
│  趋势图（折线，7 天）    │  模型分布 Donut    │
├────────────────────────┴────────────────────┤
│  最近调用 / Top 用户 / Top Keys（表格或卡片）  │
└─────────────────────────────────────────────┘
```

#### 视觉建议

- KPI 卡片统一圆角、轻微阴影、图标左置。
- 数字统一格式化：1,234 → 1.2K，1,234,567 → 1.2M。
- 费用/收益用绿色，异常/警告用黄色/红色。
- 趋势 badge 用 ↑↓ 箭头 + 百分比。

---

### 4.2 Usage Logs（User / Enterprise / Admin）

#### 字段逐项评估

针对 user 自己看的 API Logs，**不是所有字段都适合直接展示**，需要区分“有用且可理解”与“过于运营或敏感”。

| 字段 | 普通用户 | Enterprise Admin | System Admin | 说明 |
|------|----------|------------------|--------------|------|
| **端点（Endpoint）** | ✅ | ✅ | ✅ | 用户调用不同接口时有助于理解费用构成，技术用户熟悉。 |
| **流式 / 非流式** | ✅ | ✅ | ✅ | 直接影响延迟与计费体验，能解释 latency 差异。 |
| **分组（Group）** | ✅（如有多分组） | ✅ | ✅ | 帮助用户/企业把成本归因到项目或团队。 |
| **计费模式** | ✅ | ✅ | ✅ | 让用户知道是扣余额、订阅额度还是其他。 |
| **API Key 前缀** | ✅ | ✅ | ✅ | 只显示前缀，安全且便于识别哪个 Key 在调用。 |
| **成员（Member）** | ❌ | ✅ | ✅ | Enterprise Admin 需要知道哪个成员在消费。 |
| **IP / IP 归属地** | ⚠️ 不建议默认展示 | ⚠️ 仅展示地区 | ✅ | 个人用户 IP 过于敏感；Enterprise 可用于安全审计；System Admin 需要完整 IP。 |
| **推理强度** | ⚠️ 视模型而定 | ⚠️ 视模型而定 | ✅ | 如果模型有 reasoning 档位且影响价格则展示，否则是噪音。 |
| **上游渠道 / Provider** | ❌ | ❌ | ✅ | 平台运营信息，用户不需要知道后端走了哪个上游。 |
| **TTFT / 总耗时** | ⚠️ 放详情 | ⚠️ 放详情 | ✅ | 普通用户默认表格不必展示，放进详情面板即可。 |

#### 表格字段分层

基于以上评估，不同角色默认表格推荐列如下：

| 角色 | 默认列 |
|------|--------|
| 普通用户 | 时间、模型、端点、类型(流式/非流式)、分组、计费模式、Token（In/Out/Cached）、费用、状态 |
| 企业管理员 | + Key 前缀、成员、IP 归属地（地区） |
| 系统管理员 | + 完整 IP、上游渠道、TTFT/总耗时、缓存命中率、推理强度 |

#### 密度控制：默认精简 + 详情面板

为了避免默认表格信息过载，建议采用 **“默认精简列 + 展开/侧滑详情面板”** 的方式：

- **默认表格**：只展示最关键的 7-8 列，保证一屏可读。
- **点击行展开 / 侧滑详情**：展示完整字段，包括端点、流式状态、分组、计费模式、延迟明细（TTFT/总耗时）、成本明细、IP、上游渠道等。

这样既能保证信息透明，又避免界面“花眼”。

#### 实现前提

当前 `usage_records` 表中没有 `endpoint`、`client_ip`、`is_streaming` 等字段，**需要 proxy 在写入时携带这些信息**。建议实施顺序：

1. Proxy 改造：写入 `endpoint`、`is_streaming`、`client_ip`。
2. 前端 User Logs 增加：端点、流式/非流式、分组、计费模式。
3. Enterprise Admin 增加：成员、Key 前缀、IP 归属地。
4. System Admin 保持完整字段。

#### 筛选栏建议

```
[日期范围] [模型] [API Key] [分组] [成员] [类型] [计费模式] [状态] [搜索] [重置] [导出 CSV]
```

#### 可视化增强

- **Token**：用 ↓ 表示 input，↑ 表示 output，⟲ 表示 cache。
- **费用**：统一绿色，hover 显示成本明细（input/output/cache/reasoning）。
- **延迟**：颜色条，<500ms 绿、<2s 黄、≥2s 红。
- **状态/类型/分组**：圆角 pill badge。

---

### 4.3 Billing / Recharge（User / Enterprise）

#### 充值页

- 顶部：余额卡片 + 最近 7 天消费趋势 mini chart。
- 中部：金额快捷选择（$10 / $50 / $100 / $200 / 自定义）。
- 底部：支付方式卡片（WeChat / Alipay / Stripe / PayPal），disabled 状态清晰。
- 增加：充值记录表格、发票入口。

#### 账单明细

- 按天汇总：日期、请求数、Token、费用。
- 按模型汇总：模型、请求数、Token、费用、占比。
- 按 Key/成员汇总：Enterprise 场景。
- 支持导出 CSV。

---

### 4.4 Reconciliation（System Admin）

#### 已有基础

`/admin/reconciliation-ops` 已有 Upstream vs Billed Trend，这是非常好的起点。

#### 建议增强

| 模块 | 说明 |
|------|------|
| **Upstream vs Billed Trend** | 保留并优化，支持按模型/分组/端点下钻 |
| **成本拆分表** | cache hit / cache write / reasoning / input / output 各组件成本 |
| **模型毛利率** | 下游收入 - 上游成本，按模型/供应商聚合 |
| **异常对账告警** | 某日毛利率突降、上游费用为 0 但下游扣费 >0 等 |
| **导出对账单** | 支持按月份导出 PDF/CSV |

---

## 5. 视觉风格建议

### 5.1 是否切换深色主题？

**不建议全局切换为深色**。

- Portal 当前是浅色主题，全局换肤成本高。
- 深色主题虽然“看起来酷”，但对非技术用户并不友好。
- 可以保留浅色主题，但吸收 apiweb.png 的以下元素：
  - 卡片化布局 + 大圆角
  - 数据可视化（donut、多系列折线）
  - 颜色语义统一
  - 信息分层（重要数字放大，次要信息弱化）

### 5.2 颜色与字体

| 用途 | 建议色值 | 说明 |
|------|----------|------|
| 页面背景 | `#F8F9FC` 或当前变量 | 保持浅灰 |
| 卡片背景 | `#FFFFFF` | 纯白，带极淡阴影 |
| 主文字 | `#111827` | 近黑 |
| 次文字 | `#6B7280` | 中灰 |
| 弱化文字 | `#9CA3AF` | 浅灰 |
| 费用/成功 | `#10B981` | 绿色 |
| 警告 | `#F59E0B` | 橙色 |
| 错误/高延迟 | `#EF4444` | 红色 |
| 信息/链接 | `#3B82F6` | 蓝色 |

### 5.3 图表规范

- 趋势图默认用折线，避免过多柱状图并排。
- 多系列图表使用清晰图例，hover 显示具体数值。
- 双 Y 轴仅用于不同量纲（如 Token 量 vs 百分比）。
- 分布图使用 donut 或水平 bar，避免 3D 效果。

### 5.4 数字格式化

- Token：≥1M 用 `1.2M`，≥1K 用 `1.2K`，否则原值。
- 费用：统一 `$0.473415` 或 `$0.47`，根据角色决定精度。
- 延迟：`1.54s`、`29.27s`，避免纯毫秒长数字。
- 百分比：保留 1 位小数。

---

## 6. 实施优先级

### P0（1-2 周，立竿见影）

1. **Usage Logs 筛选栏**：增加模型、Key、分组、时间范围筛选 + 导出 CSV。
2. **Usage Logs 表格增强**：增加延迟颜色条、分组 badge、Token 箭头图标。
3. **Dashboard KPI 卡片统一**：统一数字格式化、颜色、趋势 badge。
4. **Enterprise Dashboard 模型/分组分布卡片**：Donut + 排行榜表格。

### P1（2-4 周，体验升级）

1. **趋势图升级**：从单一 bar chart 改为多系列折线图（Input / Output / Cache / Hit Rate）。
2. **Billing 充值页优化**：金额快捷选择、余额趋势 mini chart、充值记录。
3. **Enterprise Admin 成员/分组排行**：支持时间范围和排序维度切换。
4. **Admin Dashboard 端点分布**：前提是 proxy 写入 endpoint。

### P2（1-2 月，深度可观测）

1. **延迟可视化**：P50/P90/P99 趋势 + TTFT 字段（需 proxy 支持）。
2. **IP 归属地**：proxy 写入 client_ip + GeoIP 解析。
3. **Reconciliation 成本拆分**：cache hit/write/reasoning 各组件对账。
4. **模型毛利率表**：Upstream Cost vs Billed Revenue。

### P3（长期）

1. 全局深色主题（可选）。
2. 高级列设置（用户自定义显示哪些列）。
3. 实时 Live Stream 调用 feed。

---

## 7. 实施计划更新（2026-07-17）

结合最新反馈与页面现状，将上述建议整理为可执行计划。已完成的改造（dashboard/usage-logs 自然日边界、Requests 趋势线、Avg Latency/TPS 列）不再列入。

### 7.1 已确认问题

#### A. `admin/reconciliation-ops` 对账明细 "估算成本" 永远为 0
- **根因**：`internal/query/reconciliation_ops.go` 的 `loadModelPrices` 从 `service_models` 表读取 `input_price/output_price/cache_read_price`，但这三张字段当前全部为 0；平台实际价格在 `model_pricing_rules.prices` JSONB 列。
- **影响**：
  - "估算成本" 列恒为 $0.00，无参考价值。
  - "成本差额" = 估算成本 - 实际扣费，会显示为绝对值很大的负数，误导为“巨额亏损”。
- **处理建议**：
  - **短期（推荐）**：直接删除对账明细表的 **Est Cost** 和 **Cost Diff** 两列，仅保留 request/token diff。上游对账的核心价值是“上游统计数量 vs 下游计费数量”是否一致，cost diff 可等 pricing 来源修复后再加回。
  - **中期**：改造 `loadModelPrices` 改为从 `model_pricing_rules` 解析 `input/output/cache_hit/cache_write/reasoning/fixed_request` 价格；或复用 `services.PricingService` 的 `MatchRule` 逻辑。修复后恢复 cost diff，并增加 cache/write/reasoning 组件拆分。

### 7.2 改造清单与优先级

| 优先级 | 页面 | 改造项 | 说明 | 状态 |
|--------|------|--------|------|------|
| **P0** | `admin/reconciliation` | 用户/企业详情增加 **Usage Trend / Model Distribution / Key Distribution / API Logs** 标签页，支持日期选择器 + `1day/7days/15days/30days/quarter` 快捷范围 + 导出 CSV | 补齐 system admin 对用户/企业调用明细的最大短板 | ✅ 已完成（`admin-reconciliation.html`：Overview / Usage Trend / Model Distribution / Key Distribution / API Logs / Ledger 标签页 + 自定义日期范围 + Export CSV） |
| **P0** | `admin/reconciliation-ops` | 删除对账明细表 **Est Cost**、**Cost Diff** 列（ pricing 来源修复后可加回） | 避免显示无意义/误导性数据 | ✅ 已完成（当前表头：Provider / Model / Key / Requests / Input Tokens / Output Tokens / Billed Cost / Error Rate） |
| **P1** | `admin.html` | "用量趋势" 从当前按月 bar chart 改为和 dashboard 一致的多系列 **折线图**（Requests/Tokens/Spend），高度加大（建议 ≥320px） | 全局信息汇总需要更醒目的趋势展示 | ✅ 已完成（Chart.js line，height=320px，三 Y 轴：Requests / Tokens / Spend） |
| **P1** | `admin.html` | 增加 **Top Users (7d/30d)** 和 **Top Enterprises (7d/30d)** 卡片，支持下钻到 reconciliation 详情 | 首页即可看到资源消耗主体 | ✅ 已完成（带 7days / 30days / This Month 下拉，点击跳转 `/admin/reconciliation?user=...` 或 `?enterprise=...`） |
| **P1** | `enterprise/usage` | 增加时间范围快捷下拉 `1day/7days/15days/30days/quarter`；明细表增加 **Avg Latency / Avg TPS**；Member Detail 增加最近 API 调用日志入口 | 与企业管理员对成员调用的精细化需求对齐 | ✅ 已完成（时间下拉 + By Member/Model/Key 维度 + Avg Latency / Avg TPS 列 + Member Detail API Logs） |
| **P2** | `admin/monitor-downstream` | 增加 **Group by User / Group by Enterprise** 视图，点击用户下钻到 API Logs / Usage 详情 | 从 key 视角切换到用户视角 | ✅ 已完成（`group_by` 下拉：Key / User / Enterprise，User/Enterprise 行有 View Details 链接到 reconciliation） |
| **P2** | 全站 | 统一时间范围选择器为 `1day/7days/15days/30days/quarter`（admin monitor 现有 `24h/7d/30d/90d` 可保留或映射为相同语义） | 减少用户认知成本 | ✅ 已完成（`admin/upstream-ops` 前端下拉改为 `1day/7days/30days`；后端 `ParsePeriod` / `NormalizePeriod` 映射为 canonical `24h/7d/30d`） |
| **P2** | `admin/provider-health` / `admin/upstream-ops` | 完善 i18n（Healthy/Degraded/Unhealthy/Unknown 等英文未翻译）；异常事件增加“影响用户/企业”列 | 国际化和运营关联性 | ✅ 已完成（upstream-ops 异常事件表新增 `Impacted Users` / `Impacted Enterprises` 列及 CSV 导出；i18n 全站审计 TOTAL ISSUES: 0） |
| **P2** | `enterprise/usage` | 时间下拉框字体截断 / 框体高度修复 | 与 enterprise admin 成员调用明细需求对齐 | ✅ 已完成（select 与 date-range input 统一 `height:38px`、`padding`、`line-height`，避免字体被裁剪） |
| **P2** | `admin/reconciliation-ops` | 对账明细表原始行与展开行内容重新分配，减少凌乱 | 提高对账可读性 | ✅ 已完成（原始行保留 Provider/Model/Key + 对比数字 + 成本差额；展开行分 Token Breakdown / Upstream Cost Breakdown / Billed Cost Breakdown 三块明细） |
| **P3** | `admin/reconciliation-ops` | 新增 `/admin/upstream-pricing` 上游定价配置页；`reconciliation-ops` 恢复 cost diff，增加 upstream/billed 双侧 **input/output/cache hit/cache write/fixed** 组件成本拆分及毛利率 | 深度成本对账 | ✅ 已完成（上游价格从 `service_models` 读取，支持 CSV 导入/导出；summary 卡片和 detail 表均展示成本差额与毛利率） |
| **P3** | 新增页面 | **Platform Usage Explorer**：任意维度（user/enterprise/model/key/endpoint）+ 时间范围 + 下钻到 API Logs + 导出 CSV | 系统 admin 专用高级分析页面 | ✅ 已完成（`/admin/usage-explorer` 页面 + `/api/admin/usage-explorer/*` 聚合/日志/导出 API） |

### 7.3 实施状态更新（2026-07-18）

**第一阶段（已完成）**：
1. ✅ `admin/reconciliation-ops` 删除 Est Cost / Cost Diff 列（改前端即可，零风险）。
2. ✅ `admin/reconciliation` 后端 API 扩展：为 user/enterprise 返回 usage trend、model/key distribution、api logs。
3. ✅ `admin/reconciliation.html` 增加标签页和日期选择器/导出。

**第二阶段（已完成）**：
4. ✅ `admin.html` 用量趋势改为折线图 + 加高。
5. ✅ `admin.html` 增加 Top Users / Top Enterprises 卡片。
6. ✅ `enterprise/usage` 增加时间下拉和 Avg Latency/TPS 列。

**第三阶段（已完成）**：
7. ✅ `admin/monitor-downstream` 增加 Group by User/Enterprise。
8. ✅ 全站时间选择器统一：`admin/upstream-ops` 的 `24h/7d/30d` 已对齐为 `1day/7days/30days`（后端映射为语义一致的 canonical 值）。
9. ✅ `admin/provider-health` / `admin/upstream-ops`：upstream-ops 异常事件表已补充“影响用户/企业”列。

**第四阶段（已完成）**：
10. ✅ 新增 `/admin/upstream-pricing` 上游定价配置页；`reconciliation-ops` 从 `service_models` 读取上游价格，恢复 cost diff，并拆分 input/output/cache hit/cache write/fixed 组件成本 + 毛利率。

**第五阶段（已完成）**：
11. ✅ 新增 **Platform Usage Explorer** 页面（`/admin/usage-explorer`），支持 user/enterprise/model/key/endpoint 五维聚合、时间范围快捷选择、Top N 排行、点击下钻到 API Logs、聚合与日志双 CSV 导出。

### 7.4 修正与补充（2026-07-18）

1. **Usage Explorer enterprise 维度归属原则修正**
   - 原实现曾改为按 `users.enterprise_id` 聚合，将企业成员使用个人 key 的调用也归入企业。
   - 已回滚为按 `api_keys.enterprise_id` 聚合：企业计费只与 **enterprise key** 绑定，成员用 general/subscription key 的调用属于个人。
   - 影响：`aggregateByEnterprise` 与 `queryExplorerLogs` 的 JOIN / WHERE 条件恢复为 `api_keys.enterprise_id`。

2. **Upstream Pricing 页增加状态卡片**
   - 在 `/admin/upstream-pricing` 顶部新增 4 个统计卡片：
     - **Total Models**：当前 provider / 搜索条件下的模型总数。
     - **Priced Models**：input cache miss / input cache hit / output 三项核心价格均已配置（>0）的模型数。
     - **Inactive Models**：status 为 `paused / eol_pending / eol / discontinued / inactive / disabled / deprecated / ignored / pending` 的模型数。
     - **Incomplete Models**：缺失任一核心价格的模型数。
   - 卡片数据随 provider / search 过滤条件联动，便于 admin 快速发现定价缺口。

### 7.5 实施状态补充（2026-07-18）

§6 中部分原始建议的状态需要澄清：

| 原始建议 | 当前状态 | 说明 |
|----------|----------|------|
| **Usage Logs 增加端点 / 流式状态 / Client IP** | ✅ 已完成 | `usage_records` 已存在 `endpoint`、`is_streaming`、`client_ip` 字段；`usage-logs.html` 表格已展示端点和类型，详情面板已展示 Client IP。 |
| **Dashboard KPI 卡片统一、数字格式化** | ✅ 已完成 | dashboard / usage-logs / enterprise 等页面卡片已统一为 stats-bar 风格，费用/请求/Token 格式化规则已落地。 |
| **Reconciliation 成本拆分 / 毛利率** | ✅ 已完成 | `/admin/upstream-pricing` 已上线，`reconciliation-ops` 已恢复 cost diff 并拆分 input/output/cache hit/cache write/fixed 组件。 |
| **延迟可视化（TTFT / P50/P90/P99）** | ⏳ 待实施 | `usage_records` 暂无 `ttft_ms` 字段，需要 proxy 侧写入。 |
| **IP 归属地（GeoIP）** | ⏳ 待实施 | `usage_records` 已有 `client_ip`，但尚未做 GeoIP 解析和地区字段。 |
| **推理强度（reasoning intensity）** | ⏳ 待实施 | 需要 proxy 在 usage 中携带 reasoning 档位信息。 |
| **全局深色主题** | ⏳ 待实施 / 低优先级 | 未纳入当前迭代，可作为长期体验项。 |
| **高级列设置（用户自定义列）** | ⏳ 待实施 / 低优先级 | 未纳入当前迭代。 |
| **实时 Live Stream 调用 feed** | ⏳ 待实施 / 低优先级 | 未纳入当前迭代。 |

### 7.6 总结

`apiweb.png` 展示了一个**高信息密度的 API 网关运营后台**，最适合作为 **System Admin / 运营视角** 的参考。它的核心优点是多维度聚合、可视化丰富、工具感强；缺点是信息过载、颜色对比强，不适合直接暴露给普通用户。

对 Portal 的优化应该走**“分层设计”**路线：

- **User**：精简，只看余额、今日消费、趋势、最近调用。
- **Enterprise Admin**：增加团队/分组/成员维度，关注预算和异常。
- **System Admin**：保留全部维度，支持对账、导出、深度分析。

建议先从 **admin reconciliation 用户/企业明细下钻 + reconciliation-ops 误导列清理 + admin dashboard 趋势图升级** 入手，逐步升级到 monitor-downstream 用户视角和深度成本对账。当前剩余的主要前置条件是 **proxy 侧写入 ttft_ms / reasoning 信息** 后才能做更完整的延迟与推理可视化。
