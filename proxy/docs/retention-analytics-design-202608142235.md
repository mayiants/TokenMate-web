# Retention Data Analytics — 用途分析与实施方案

> **Status:** ACTIVE  
> **Date:** 2026-08-14  
> **Scope:** ai-proxy-go retention data analysis for operational intelligence

---

## 1. 背景

Proxy 的 data retention 系统（设计文档见 `data-retention-design-202606281159.md`）已在生产环境激活，当前积累了 5,605 条记录（~801 MB），覆盖 2026-07-13 至 2026-08-14。

除合规审计用途（Anthropic/OpenAI 30-90 天保留要求）外，这些数据对运营优化具有高价值。本文档规划如何利用这些数据提升平台运营水平。

---

## 2. 数据结构

每条 retention 记录包含：

```json
{
  "schema_version": "1.0",
  "trace_id": "uuid",
  "received_at": "RFC3339 timestamp",
  "metadata": {
    "user_id": "uuid",
    "api_key_hash": "sha256",
    "client_ip": "ip",
    "upstream_key_id": "uuid",
    "provider": "tokenrouter|openai|...",
    "model": "model-name",
    "vendor_model_name": "provider/model-name",
    "client_format": "openai|anthropic|...",
    "upstream_format": "openai|anthropic|...",
    "status_code": 200,
    "latency_ms": 1650,
    "first_token_latency_ms": 19,
    "input_tokens": 63541,
    "output_tokens": 226,
    "cached_tokens": 63488,
    "is_stream": true,
    "is_converted": false,
    "routing_track": "standard|bedrock|mantle",
    "is_fallback": true,
    "request_type": "chat_completions|chat_completions_stream|image_generation|video_generation"
  },
  "request": {
    "method": "POST",
    "path": "/v1/chat/completions",
    "headers": {"Content-Type": "application/json", ...},
    "body": "<base64-encoded request body>"
  },
  "response": {
    "body": "<base64-encoded response body>"  // 流式请求为空
  }
}
```

**关键限制：** 流式响应（`is_stream=true`，占多数请求）的 response body 为空，因为 SSE 是逐块发送的。

---

## 3. Metadata 层分析（轻量级，无需解码 body）

### 3.1 模型使用排行

| 指标 | 说明 | 运营价值 |
|------|------|---------|
| 请求次数 by model | 各模型的调用频率排名 | 采购决策、热门模型缓存优化 |
| Token 消耗 by model | input/output/cached token 分布 | 成本归因、cache 效果评估 |
| 用户 × 模型矩阵 | 哪些用户使用哪些模型 | 用户画像、个性化推荐 |
| 时间趋势 | 模型使用量随时间变化 | 识别模型热度变化、新模型adoption速度 |

### 3.2 性能监控

| 指标 | 说明 | 运营价值 |
|------|------|---------|
| 延迟 P50/P95/P99 by model | 各模型的响应时间分布 | SLA 监控、发现性能退化 |
| First Token Latency | 流式首 token 时间 | 用户体验核心指标 |
| 延迟趋势 | 同模型不同时段的延迟变化 | 识别上游性能退化 |
| Token/秒吞吐 | output_tokens / (latency - first_token_latency) | 模型速度对比 |

### 3.3 可靠性分析

| 指标 | 说明 | 运营价值 |
|------|------|---------|
| 成功率 by model/provider | status_code 非 200 的比例 | 识别不稳定模型 |
| Fallback 触发率 | is_fallback=true 的比例 | 评估路由策略效果 |
| 错误分类 | 按 status_code 分布 (429/500/502/503) | 区分限流、宕机、超时 |
| 上游 Key 负载 | 每个 upstream_key_id 的请求量 | 负载均衡评估 |

### 3.4 Cache 效率

| 指标 | 说明 | 运营价值 |
|------|------|---------|
| Cache Hit Ratio | cached_tokens / input_tokens | prompt cache 整体效果 |
| Cache Hit by model | 分模型的缓存命中 | 哪些模型受益于缓存 |
| Cache 节省成本 | cached_tokens × (input_price - cache_price) | 量化缓存带来的成本节省 |

---

## 4. Body 解码分析（重量级）

### 4.1 可行的分析

| 分析 | 数据要求 | 说明 |
|------|---------|------|
| 请求长度分布 | 解码 request body → 计算字符/token 数 | 了解用户典型输入规模 |
| 模型参数偏好 | 解码 request → 提取 temperature/max_tokens/top_p | 了解用户调参习惯 |
| 系统提示分类 | 解码 request → 提取 system message → 关键词分类 | 了解用例类型（coding/writing/analysis） |
| 回复质量指标 | 解码 response → 长度/结构化程度 | 粗粒度质量评估（仅非流式） |

### 4.2 资源评估

**解码单条记录的成本：**
- base64 解码：CPU 几乎为零
- JSON 解析：~1ms per record
- 内存：平均 request body ≈ 146 KB × 0.75 (base64 overhead) ≈ 110 KB decoded
- 峰值记录 (73K tokens)：~400 KB decoded

**全量分析 5,605 条：**
- 预计内存峰值：< 500 MB（逐条处理，不全部加载）
- 预计 CPU 时间：< 30 秒
- 磁盘 IO：读取 801 MB gzip 文件 → 解压后 ~3-4 GB

**t3.small (2 vCPU, 2 GB RAM) 上的可行性：**
- ✅ 逐条处理完全可行（内存 < 500 MB）
- ⚠️ 不应全量加载到内存
- ⚠️ 应在低峰期运行，避免与请求处理竞争 CPU
- ✅ 读取 IO 对 NVMe SSD 无压力

**结论：body 解码分析可以实现，但应采用流式逐条处理、限制并发、在低峰期运行。**

---

## 5. 模型能力评估

当前各公司模型迭代极快，retention 数据可以从实际使用中评估模型表现：

### 5.1 基于 Metadata 的模型对比

| 维度 | 计算方式 | 意义 |
|------|---------|------|
| 速度 | output_tokens / latency_ms × 1000 = tokens/sec | 生成速度排名 |
| 首 Token 速度 | first_token_latency_ms 中位数 | 交互体验排名 |
| 可靠性 | success_rate = count(200) / total | 稳定性排名 |
| Cache 友好度 | avg(cached_tokens / input_tokens) | 支持 prompt cache 程度 |
| 受欢迎程度 | request_count / total_requests | 用户投票 |
| 复杂任务偏好 | avg(input_tokens) when model=X | 用户倾向用它做什么规模的任务 |

### 5.2 模型迭代追踪

- 通过 `vendor_model_name` 中的版本号跟踪同系模型迭代
- 对比同系模型新旧版本的延迟、token 效率、使用量变化
- 自动检测新模型上线（首次出现的 model name）

### 5.3 能力边界探测（需 body 解码）

- 分析哪些 prompt 模式在哪个模型上成功/失败
- 按用例类型（code/writing/analysis）统计各模型使用比例
- Token 效率对比：相似长度输入下，哪个模型输出更精练

---

## 6. 实施架构

### 后台分析任务（Proxy 侧）

```
ai-proxy-go/
  internal/
    retention/
      analytics/           ← 新增
        analyzer.go        — 核心分析引擎
        metrics.go         — 指标计算逻辑
        scheduler.go       — 定时调度器
        report.go          — 报告生成与持久化
        models.go          — 模型能力评分
```

### 运行策略

- 定时执行：每 6 小时一次全量统计
- 增量处理：记录上次分析位置，只处理新文件
- 资源限制：单 goroutine 运行，限制内存 < 256 MB
- 输出格式：JSON 报告文件，同时暴露 HTTP API 供 Portal 查询

### Admin API

```
GET /admin/analytics/summary       — 最新分析摘要
GET /admin/analytics/models        — 模型能力排名
GET /admin/analytics/performance   — 性能趋势
GET /admin/analytics/users         — 用户使用模式
GET /admin/analytics/health        — 可靠性/错误分析
```

---

## 7. UI 方案

**推荐方案：在 Portal admin 面板新增 "Analytics" 页面**

理由：
- Portal 已有完整的 admin UI 框架（HTMX + Alpine.js + Tailwind）
- Admin 已有认证/授权机制
- 避免在 Proxy 上开发独立前端（Proxy 的职责是数据面）
- Portal 通过调用 Proxy 的 `/admin/analytics/*` API 获取数据展示

页面设计：
1. **Dashboard 总览** — 今日请求量、热门模型 Top 5、平均延迟、成功率
2. **Models Leaderboard** — 模型排行榜（速度/可靠性/受欢迎度/cache效率）
3. **Performance** — 延迟趋势图、P95/P99 追踪
4. **Usage Patterns** — 用户活跃度、模型使用热力图

---

## 8. 不适合做的事项

- ❌ 训练数据（流量不足，且非平台目标）
- ❌ 实时流处理（t3.small 资源不足，且无此需求）
- ❌ 长期存储所有 body（90 天后按策略清理）
- ❌ 跨用户内容对比（隐私风险）

---

## 9. 里程碑

| 阶段 | 内容 | 预计时间 |
|------|------|---------|
| Phase 1 | Metadata 分析后台任务 + Admin API | 本次实现 |
| Phase 2 | Portal Analytics 页面 | 后续 session |
| Phase 3 | Body 解码分析（参数偏好/用例分类） | 流量增长后 |
| Phase 4 | S3 迁移 + 长期分析 | 2026-09-15 评估 |
