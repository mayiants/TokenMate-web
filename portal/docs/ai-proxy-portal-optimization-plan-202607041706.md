# ai-portal-go / ai-proxy-go 优化计划（基于 Bifrost/LiteLLM 借鉴分析）

> 分析时间：2026-07-04  
> 基准代码：ai-portal-go `main` (938c3e)、ai-proxy-go `main` (815c45e)，均已与 GitHub origin 同步。  
> 参考文档：`bifrost-lessons-for-ai-portal-proxy.md`、`bifrost-analysis.md`、`litellm-analysis.md`

---

## 1. 结论速览

ai-proxy-go / ai-portal-go 已经具备完整的控制平面-数据平面分离架构，功能覆盖认证、限流、路由、计费、审计、provider 适配等核心能力。但在**性能、可观测性、批量处理、缓存失效**四个方面与 Bifrost/LiteLLM 存在明显差距。

**最需要立即优化的方向**：

1. **ai-proxy-go 性能**：消除重复 JSON 解析、引入 sync.Pool、降低 keypool 锁粒度、异步化 billing/audit。
2. **可观测性补全**：采用 Prometheus client、补齐 OpenTelemetry trace、统一 trace/request ID、点亮现有“死”指标。
3. **ai-portal-go 热路径**：为 `/api/keys/validate` 和 webhook 批量处理加索引、加缓存、做批量写入。
4. **配置热更新**：从“轮询 + 全量拉取”升级为“版本号 + 增量热更新”。

---

## 2. ai-proxy-go 性能优化

### 2.1 请求生命周期中的重复解析（P0）

**现状**（`internal/server/handlers_chat.go`）：

- 请求体被 `io.ReadAll` 读取一次，再 `json.Unmarshal` 一次。
- upstream 响应在非流式路径下被解析 **3-4 次**：上游错误处理、`FromProvider`、`ExtractUsage`。
- 每个 provider adapter（openai、claude、gemini、aws）都在 `FromProvider` / `ExtractUsage` / `TransformStreamChunk` 中重复 `json.Unmarshal`。

**影响**：高并发下 CPU 和 GC 压力显著。

**优化方案**：

1. **一次解析原则**：`FromProvider` 和 `ExtractUsage` 接收已解析的 struct，而不是 `[]byte`。
2. **引入 canonical internal model**：客户端请求 → `InternalRequest` → provider 只做 `Internal → Native`；响应 `Native → Internal → Client`。
3. 保留原始 `[]byte` 仅用于 retention/affinity，避免为插件/转换反复分配 buffer。

### 2.2 sync.Pool 缺失（P0）

**现状**：项目-wide 搜索未找到任何 `sync.Pool` 使用。

**应池化的对象**：

- `dto.GeneralOpenAIRequest`、`dto.OpenAITextResponse`、`dto.ChatCompletionsStreamResponse`
- SSE chunk buffer、`bytes.Buffer`
- `billing.UsageRecord`、`audit.AuditEntry`
- keypool candidate slices、`upstreamUsageEntry`
- `http.Request` 模板、`bytes.Reader`

**优化方案**：新增 `internal/pool` 或 `pkg/pool` 包，提供类型安全池，优先在 handlers_chat、stream、audit 路径落地。

### 2.3 Provider 级 Queue + Worker Pool（P0）

**现状**：基于 Gin，每个请求一个 handler goroutine；流式再增加一个 reader goroutine；没有显式 provider 并发限制。

**风险**：流量突增时会把上游 provider 瞬间打满，导致 429/timeout 级联。

**优化方案**（借鉴 Bifrost `core/bifrost.go`）：

1. 为每个 provider 维护一个带缓冲 channel `ProviderQueue`。
2. 配置 `Concurrency` 限制同时发往该 provider 的请求数。
3. 队列满时快速返回 429，实现背压。
4. Worker goroutine 消费队列，统一处理 upstream 调用、重试、fallback。

### 2.4 keypool 锁与选择优化（P1）

**现状**（`pkg/keypool/manager.go`）：

- `GetKeyByPriority` 对单个 provider 加写锁，整个过滤/排序/选择过程串行。
- 每次选择都分配多个 candidate slice。
- `containsMatch` 每次构建 exactMap/normMap。
- `SelectRoundRobinModel` 对 `wgit-maas-latest` 每个请求都做一次 Redis `INCR`。
- `persistToRedis` 在 provider 锁内做 JSON marshal + 加密 + Redis 写入。

**优化方案**：

1. 选择阶段使用 `RLock`，只在更新状态时加写锁；或按 `(provider, model)` shard。
2. 在 key 同步时预计算 normalized model set，存入 `KeyStatic`。
3. 本地原子计数器实现 round-robin，定期同步 Redis。
4. `persistToRedis` 改为异步：锁外生成 snapshot，后台 goroutine 写入。

### 2.5 Billing/Pre-consume 异步化（P1）

**现状**（`pkg/billing/preconsume.go`）：

- `PreConsume` 在 upstream 调用前同步 POST Portal，2s timeout。
- `Settle` 每个请求都 `go` 一个 goroutine。

**优化方案**：

1. 维护本地余额缓存，由 Portal Pub/Sub 或定时同步刷新。
2. 缓存命中时直接做余额判断，避免同步 Portal RTT。
3. Settle 批量化：每个请求写入 channel，后台 worker 批量 flush 到 Portal。

### 2.6 Audit 批量刷新（P1）

**现状**（`internal/audit/audit.go`）：

- `Log` 在 batch 满时 `go l.Flush()`，每次 flush 重建 batch slice。
- 失败记录 `append(batch, l.batch...)` 重建整个队列。

**优化方案**：

1. 用 channel + 单后台 goroutine 替代 `go per request`。
2. 双 batch 交替，避免 flush 时阻塞写入。
3. 失败记录写入 fallback log，不重建队列。

### 2.7 流式响应内存优化（P1）

**现状**（`internal/server/handlers_chat.go`）：

- 每个 stream 起一个 goroutine 读 4KB chunk 并复制到新 `[]byte`。
- 每个 SSE event 做多次 `json.Marshal` / `json.Unmarshal`。
- 全局 `currentStreamState` 变量（`pkg/relay/channel/claude/claude.go`）存在并发正确性问题。

**优化方案**：

1. 去掉 per-stream reader goroutine，直接在 handler loop 用 `bufio.Reader` + `SetReadDeadline` 读取。
2. 复用 `[]byte` scratch buffer 和池化的 `ChatCompletionsStreamResponse`。
3. 只有含 `usage` 的 chunk 才解析 usage。
4. 修复 `currentStreamState` 为 per-request 状态。

### 2.8 HTTP 客户端调优（P2）

**现状**（`pkg/relay/base/adaptor.go`）：

- `MaxIdleConnsPerHost: 20`，高并发单 provider 可能成为瓶颈。

**优化方案**：

1. 使连接池参数可配置。
2. 按 provider 分离 HTTP client，避免全局竞争。
3. 评估 fasthttp（成本较高，作为 P2 长期选项）。

### 2.9 Affinity Cache 优化（P2）

**现状**（`pkg/cache/affinity.go`）：

- 每次请求对完整 body 做 SHA-256。
- Redis GET/SET 在请求热路径。
- 随机淘汰。

**优化方案**：

1. 使用 `xxhash` 替代 SHA-256。
2. 本地 `sync.Map` 为主，Redis 异步写透传。
3. 使用 LRU 替代随机淘汰。

---

## 3. 可观测性优化

### 3.1 Prometheus：从手撸文本到官方 client（P0）

**现状**：

- ai-proxy-go 手写了 `pkg/metrics/metrics.go` + `ToPrometheusFormat()`。
- ai-portal-go 也有手写 metrics，且大量声明的 counter **从未被递增**。

**优化方案**：

1. 引入 `prometheus/client_golang`。
2. ai-proxy-go 定义 histogram：`proxy_request_duration_seconds{provider, model, status_code, is_stream, is_fallback}`。
3. ai-portal-go 点亮 dead metrics：`WebhookReceived`、`WebhookFailed`、`DBQueryCount`、`AuthAttempts`、`EmailSent` 等。

### 3.2 OpenTelemetry Trace（P0/P1）

**现状**：两个项目都没有 OpenTelemetry。

**优化方案**：

1. ai-proxy-go 每个请求生成 root span，子 span 覆盖：auth、ratelimit、key selection、upstream、billing、audit。
2. ai-portal-go webhook 处理生成 span，与 Proxy span 关联。
3. 使用 W3C `traceparent` 在 Proxy ↔ Portal 间传播。
4. 遵循 OpenTelemetry GenAI semantic conventions：
   - `gen_ai.operation.name`
   - `gen_ai.system`
   - `gen_ai.request.model`
   - `gen_ai.usage.input_tokens` / `output_tokens`
   - `gen_ai.client.response.time_to_first_token`
   - `gen_ai.client.response.time_per_output_token`

### 3.3 统一 trace_id / request_id（P1）

**现状**：

- ai-proxy-go 仅在 pre-consume 分支用 `request_id` 作为 `trace_id`，其他情况生成新的 UUID。
- Portal webhook 接收 `traceId` 但 Proxy 调用 Portal webhook 时不带 `traceparent`。

**优化方案**：

1. RequestID middleware 中统一设置 `trace_id = request_id`。
2. 所有 webhook HTTP 调用携带 `X-Request-ID` 和 `traceparent`。
3. 结构化日志统一包含 `trace_id`。

### 3.4 补齐网关级指标（P1）

ai-proxy-go 应新增：

| 指标 | 用途 |
|------|------|
| `proxy_fallback_total` | fallback 次数 |
| `proxy_first_token_latency_seconds` | 流式首 token 延迟 |
| `proxy_provider_health_total{error_type}` | provider 错误分类 |
| `proxy_key_cooldown_total` | key 进入冷却次数 |
| `proxy_cache_hit_ratio` | 缓存命中率 |
| `proxy_queue_wait_seconds` | provider queue 等待（引入 queue 后） |
| `proxy_preconsume_rejected_total` | 余额不足拒绝 |
| `proxy_settle_charged_total` | 结算次数 |

### 3.5 日志 enrichment（P1）

ai-proxy-go access log 当前缺少：provider、model、upstream_key_id、is_fallback、routing_track、error_type、token counts、cost、phase timings。

**优化方案**：在中间件或 handler 收尾阶段统一注入这些字段。

### 3.6 Alerting 模块（P2）

**现状**：`ai-portal-go/internal/routes/enterprise_alerts.go` 是空文件。

**优化方案**：

1. 基于现有 provider health stats、usage_records 实现 alert rule 引擎。
2. 支持 error rate、p99 latency、cost spike、key pool exhaustion、provider down 规则。
3. 输出渠道：webhook、Slack/Discord/飞书/钉钉、email。

---

## 4. ai-portal-go 控制平面优化

### 4.1 `/api/keys/validate` 优化（P0）

**现状**：

- Redis 缓存 5 分钟，命中时快。
- 缓存未命中时：大 JOIN + enterprise 额外 2 次查询 + 每次查询所有 active service_models。
- `api_keys.key_hash` 无索引。

**优化方案**：

1. 加索引：
   ```sql
   CREATE INDEX idx_api_keys_key_hash ON api_keys(key_hash);
   CREATE INDEX idx_api_keys_key_hash_active ON api_keys(key_hash) WHERE deleted_at IS NULL;
   ```
2. 把 enterprise 查询合并进主 JOIN（`LEFT JOIN enterprises`、`LEFT JOIN enterprise_members`）。
3. active service_models 预加载到 Redis（如 `service_models:active`），带 TTL，模型变更时失效。
4. validate 响应瘦身：返回 proxy 快速路径所需的最小字段，billing 细节放到 webhook 处理。
5. 增加缓存失效：key/group/user 变更时删除或 bump generation key。

### 4.2 Webhook 批量处理（P0）

**现状**：

- `handleUsageStatsBatch` 对每个记录单独 `RPUSH`。
- `processStats` 每条记录一个 transaction。
- billing 队列每条记录查一次 `api_keys`。
- `usage_records` 每条记录一次 `UPDATE`。

**优化方案**：

1. Redis Pipeline 批量入队。
2. `processStats` 改为批量 `INSERT ... ON CONFLICT (trace_id) DO NOTHING`。
3. billing 队列 pop 时用 `WHERE key_hash = ANY($1)` 一次查完用户映射。
4. `usage_records` 批量 `UPDATE ... FROM (VALUES ...)`。
5. provider health、key-pool status 也做批量 upsert。
6. 失败队列加指数退避 + 重试上限。

### 4.3 配置热更新（P1）

**现状**：Proxy 轮询全量拉取 key-pool / models / pricing；无版本号；无 ETag。

**优化方案**：

1. Redis 中维护单调递增的 `config_version`。
2. 管理端变更时递增版本并发布 `ai-proxy:sync`。
3. Sync 接口返回 `X-Config-Version`，Proxy 仅在有变化时拉取全量。
4. key-pool / models / pricing 缓存到 Redis，带 TTL。

### 4.4 Pricing Service 优化（P1）

**现状**：

- `MatchRule` 用 `provider IN ($2, '*') AND region IN ($3, '*')`，索引利用率低。
- 每记录单独 `CalculateCost`。
- 缓存是进程本地，多实例场景命中率下降。

**优化方案**：

1. 把 active pricing rules 加载到 Redis hash，按 `(provider, region, model)` 查询。
2. `CalculateCost` 支持批量：按 `(provider, region, model, minute)` 分组，每组只查一次规则。
3. `SyncViewForProxy` 结果缓存到 Redis。

### 4.5 引入 Virtual Key 概念（P2）

**现状**：`api_keys` 直接关联 user/group/enterprise，缺乏 consumer 级抽象。

**优化方案**：

1. 新增 `virtual_keys` 表或在 `api_keys` 上增加 virtual key 语义。
2. Virtual key 上挂载 budget、rate limit、allowed models、routing rules。
3. 上游 key 选择由 Portal 负责，Proxy 只校验 virtual key。

---

## 5. 路由与 MCP（战略级）

### 5.1 路由能力补齐（P1）

**现状**：仅支持 `single/round_robin/priority`，无 weighted、无 fallback chain。

**优化方案**：

1. `model_provider_routing` 增加 `weight`、`fallback_order`。
2. 支持 per-virtual-key routing override。
3. Portal 下发结构化 routing DAG，Proxy 实现 weighted random + fallback。

### 5.2 MCP Gateway（P2）

**现状**：无 MCP 能力。

**优化方案**：

1. ai-proxy-go 增加 MCP client，连接外部 MCP server。
2. ai-portal-go 增加 MCP server/tool 注册、授权、审计 UI。
3. Virtual key 级别 MCP tool allow-list。
4. MCP 执行日志进审计，支持人工审批。

### 5.3 语义缓存（P2）

**现状**：仅有 affinity cache（提升上游 prompt cache 命中率），无网关层语义缓存。

**优化方案**：

1. 引入向量库（Qdrant/Redis/Valkey）做语义缓存。
2. 缓存命中不下发上游调用、不计入预扣费。
3. 从非流式、低敏感场景试点。

---

## 6. 优先级与实施路线图

### Phase 1：立竿见影（1-2 周）

| 项目 | 优化项 | 预期收益 |
|------|--------|----------|
| ai-portal-go | `api_keys(key_hash)` 索引 + validate 查询合并 | 降低 validate 延迟 |
| ai-portal-go | Webhook Redis Pipeline + 批量 INSERT/UPDATE | 降低 DB 写入压力 |
| ai-proxy-go | 消除 upstream 响应重复 JSON 解析 | 降低 CPU/GC |
| ai-proxy-go | sync.Pool 池化 DTO/buffer/audit record | 降低 GC |
| ai-portal-go | 点亮 dead metrics | 可观测性可用 |
| ai-proxy-go | 补齐 cache hit/miss、key cooldown 指标 | 指标完整 |

### Phase 2：架构升级（3-6 周）

| 项目 | 优化项 | 预期收益 |
|------|--------|----------|
| ai-proxy-go | Provider 级 queue + worker pool | 上游保护、尾延迟优化 |
| ai-proxy-go | 本地余额缓存 + 异步 settle | 移除 Portal RTT |
| ai-proxy-go | keypool 锁优化 | 提升并发选择吞吐 |
| ai-portal-go | 配置版本号 + 增量热更新 | 减少全量同步 |
| ai-proxy-go / ai-portal-go | OpenTelemetry trace + traceparent | 端到端可观测 |
| ai-proxy-go | Prometheus client + histogram | 真实 latency percentile |

### Phase 1 / Phase 2 工程项完成状态

本次迭代已将下列 P1/P2 工程项落地并通过测试，剩余项继续按 Phase 3 / backlog 推进。

| 阶段 | 任务 | 关键改动 | 状态 |
|------|------|----------|------|
| P0 | DB / Redis TLS 配置化 | `DB_SSLMODE` / `DB_SSL_ROOT_CERT` / `REDIS_TLS` / `REDIS_TLS_SKIP_VERIFY` 环境变量化；本地默认关闭，生产可开启 | ✅ 已完成 |
| P0 | Streaming context cancellation | 三个 streaming handler 改用 `DoRequestBytesWithContext`；client 断连时取消上游请求并退款 | ✅ 已完成 |
| P1 | keypool manager 拆分 | `pkg/keypool/manager.go` 拆为 `selection.go` / `sync.go` / `health.go` / `persist.go` / `queue.go`；现有测试通过 | ✅ 已完成 |
| P1 | 消除 upstream 响应重复 JSON 解析 | `relay.Adaptor.ParseResponse` 一次解析，`FromProvider` / `ExtractUsage` 复用解析结果 | ✅ 已完成 |
| P1 | sync.Pool 落地 | 新增 `pkg/pool`；`handleChatCompletions`、流式路径池化 request / buffer / stream response | ✅ 已完成 |
| P1 | keypool 锁优化 | `4d369d7` 减少热路径锁竞争；`072f9bd` 预计算 model matching set | ✅ 已完成 |
| P1 | 请求/响应 body 日志降级 | `handlers_chat.go` 将 `request_body` / `response_prefix` 改为 `Debug` 级别 | ✅ 已完成 |
| P1 | 上游响应 body 大小限制 | `pkg/keypool/queue.go` 的 `readProviderResult` 限制 50 MiB，超限返回 `ErrResponseTooLarge` | ✅ 已完成 |
| P1 | Webhook 批量 pricing 查询 | `PricingService.CalculateCostBatch` / `BillingService.GetPricingRatioBatch` 按 `(provider, region, model, minute)` 分组解析 | ✅ 已完成 |
| P1 | Webhook Redis 队列失败指标 | 三处 Redis push 失败点递增 `WebhookRedisQueueFailed` 并记录 `Error` 日志 | ✅ 已完成 |
| P1 | `config_version` 稳定性 | 已排除 `provider_health` / `key_health` / `syncedAt`；新增回归测试 | ✅ 已完成 |
| P1 | key-pool 配置热更新 | Portal 新增 `/key-pool/version`、支持 `X-Config-Version` 304；Proxy 轮询先查版本，回调带版本跳过 | ✅ 已完成 |
| P1 | Prometheus registry 可注入 | `pkg/metrics/prometheus.go` 用 `atomic.Pointer` 管理全局 registry，提供 `NewPrometheusRegistry` / `SetDefaultRegistry` | ✅ 已完成 |
| P1 | OpenTelemetry exporter 非阻塞初始化 | `pkg/telemetry/telemetry.go` 先安装 no-op provider，后台 goroutine 构建 OTLP exporter | ✅ 已完成 |
| P1 | ai-portal-go `/api/keys/validate` 索引 + 查询合并 | `a4ec667` 增加 `api_keys.key_hash` 索引并合并 enterprise 查询 | ✅ 已完成 |
| P1 | ai-portal-go Webhook 批量写入 | `41b0705` 批量 pricing、失败指标、`config_version` 热更新；`internal/db/db.go` 批量 INSERT/UPDATE | ✅ 已完成 |
| P1 | ai-portal-go 点亮 dead metrics | `20de980` 在 db/auth/mail/web 路由中递增 `DBQueryCount`、`AuthAttempts`、`EmailSent` 等 | ✅ 已完成 |
| P2 | Provider 级 queue + worker pool | `851cb27` 新增 `pkg/keypool/queue.go`；非流式请求可选按 provider 排队 | ✅ 已完成 |
| P2 | 本地余额缓存 + 异步 settle | `9260b27` Redis-backed balance cache + async settle；`8a0a109` 修复 enterprise key 预扣绕过 | ✅ 已完成 |
| P2 | OpenTelemetry trace + traceparent | 新增 `pkg/telemetry`；Proxy 生成 root span，Portal webhook 提取/注入 `traceparent` | ✅ 已完成 |
| P2 | Prometheus client + histogram | `e57c2c5` Proxy histogram + cache hit/miss / key cooldown / fallback 计数器；Portal `f46775d` 迁移到 `prometheus/client_golang` | ✅ 已完成 |

> 验证方式：
> - `go test ./...` 在 `ai-proxy-go` 与 `ai-portal-go` 的对应包通过。
> - 本地 `./start.sh mvp` 启动后，修改 Provider 触发 `PublishSyncEvent`，Proxy `/api/webhook/sync` 收到回调并执行 `SyncFromPortal`。
> - 余额更新场景通过 `/api/webhook/balance-update` 使 Proxy 本地余额缓存失效或置零。

### Phase 3：战略能力（6-12 周）

| 项目 | 优化项 | 预期收益 |
|------|--------|----------|
| ai-proxy-go | Canonical internal model + 插件流水线 | 降低 provider 接入成本 |
| ai-portal-go / ai-proxy-go | Virtual Key + 分层治理 | 简化权限模型 |
| ai-proxy-go | 语义缓存 | 降低成本 |
| ai-proxy-go | MCP Gateway | Agent 市场竞争力 |
| ai-portal-go | Alerting 模块 | 运维自动化 |

---

## 7. 风险提示

1. ** fasthttp 迁移**：成本高，建议先做 benchmark 确认 HTTP 层是瓶颈再决定。
2. **余额缓存**：本地缓存可能导致短暂超扣，需要设计“软拒绝 + 同步回源”策略。
3. **Provider Queue**：引入 queue 会改变延迟分布，需要充分的 stress test。
4. **OpenTelemetry**：会增加少量 overhead，但在可接受范围内。
5. **数据库索引**：大表加索引需评估锁表时间，建议在低峰期执行或使用 `CONCURRENTLY`。

---

## 8. 总结

ai-proxy-go / ai-portal-go 的下一步优化应聚焦在 **“高性能数据平面 + 可观测性 + 控制平面热路径批量化”** 三个主轴：

- **ai-proxy-go**：从 per-request goroutine 模型逐步演进到 Bifrost 式的 queue + worker pool + sync.Pool，减少重复序列化和锁竞争。
- **ai-portal-go**：把 validate 和 webhook 路径从“单条处理”改为“批量 + 缓存 + 索引”。
- **双方可观测性**：补齐 Prometheus client、OpenTelemetry、traceparent、缺失指标，形成可排查、可告警的生产系统。

这些优化不需要推翻现有架构，而是在现有控制平面-数据平面分离的基础上做工程深化，风险可控、收益明确。

---

## 9. 部署架构约束：Portal 与 Proxy Redis 必须分离

### 9.1 现状确认

| 组件 | Redis 位置 | 配置来源 | 用途 |
|------|-----------|---------|------|
| **ai-portal-go** | `internal/db/db.go:70` | `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD` / `REDIS_DB` | session/cache、Pub/Sub 通知（`ai-proxy:sync`、`ai-proxy:balance`）、Webhook 缓冲队列 |
| **ai-proxy-go** | `cmd/server/main.go:115`、`internal/ratelimit/ratelimit.go:147`、`pkg/keypool/manager.go:574` | `redis.*` 或 `REDIS_*` 环境变量 | affinity cache、rate limit counter、keypool 持久化/计数器、Pub/Sub 监听 `ai-proxy:sync` |

- 两套 Redis 客户端**完全独立**，没有任何代码把 Proxy 指向 Portal 的 Redis。
- `ai-portal-go/internal/db/db.go:153` 把 `PublishSyncEvent` 发布到 **Portal 自己的 Redis**。
- `ai-proxy-go/pkg/keypool/manager.go:2544` 订阅的也是 **Proxy 自己的 Redis** 上的 `ai-proxy:sync`。

### 9.2 关键判断：分开部署时不能假设共享 Redis

当前 `docker-compose` 演示环境把 Portal、Proxy、Redis 放在同一台机器/同一网络，容易让人误以为它们共享同一个 Redis。实际生产若 Portal 与 Proxy 位于不同服务器：

- **Proxy 无法读到 Portal Redis 里的 session、缓存、Pub/Sub 消息。**
- **Portal 发布的 `ai-proxy:sync` 不会到达 Proxy**，除非两个服务被显式配置到同一个 Redis 实例/集群。
- 把 Proxy 硬编码指向 Portal Redis 违反“控制平面-数据平面分离”原则，且会把跨地域/跨 VPC 的 Redis 流量暴露在管理平面，增加单点故障面。

### 9.3 推荐做法

1. **Proxy 必须使用独立的 Redis（或 Redis Cluster / Valkey）**，用于：
   - affinity / nonce / rate-limit 计数器；
   - keypool 本地快照与 round-robin 计数器；
   - 接收 Proxy 自己的配置热更新通道（后续可改为 Portal 主动 HTTPS 回调）。

2. **实时通知改为 HTTPS 回调，而不是跨服务 Redis Pub/Sub**（✅ 已落地）：
   - Portal 在 key/pricing/model 变更后，通过 HTTPS POST 调用 Proxy 的 `/api/webhook/sync`；余额更新时调用 `/api/webhook/balance-update`；
   - 请求头携带 `X-Sync-Secret`，Proxy 保留基于 `sync_interval` 的兜底轮询；
   - Redis Pub/Sub 仍作为同进程/同网络内的备用通知通道，但不再被假设为跨服务共享。

3. **未来“热更新”设计应以 Proxy Redis 为中心**：
   - Portal 生成配置 snapshot 后，写到 Proxy Redis（或让 Proxy 拉取后写入本地 Redis）；
   - 多 Proxy 实例通过各自的 Redis Pub/Sub 或 Sentinel 收到变更；
   - 不要把 Portal Redis 当作 Proxy 的分布式状态存储。

---

## 10. 全链路 SSL/TLS 加密审计

> 链路：User →（Edge TLS）→ Proxy/Portal → Upstream Provider → Response → User，以及内部子链路 Portal ↔ DB、Portal/Proxy ↔ Redis、Proxy ↔ Portal。

### 10.1 审计方法

- 检查 `ListenAndServeTLS` / `TLSConfig` / `tls.Config` 使用情况。
- 检查所有 `http.Client` 是否显式设置 `InsecureSkipVerify`。
- 检查 PostgreSQL DSN 中的 `sslmode`。
- 检查 Redis 客户端是否启用 TLS。
- 对照部署文档确认 TLS 终止位置。

### 10.2 结果汇总

| 链路 | 当前状态 | 风险 | 代码位置 |
|------|---------|------|----------|
| **User → Portal/Proxy（外部入口）** | ✅ TLS 由 nginx / LB / Cloudflare 终止，应用本身监听 HTTP | 应用层无内置 TLS，但在反向代理架构下可接受；需确保 LB 到 app 的内网可信 | `ai-portal-go/cmd/server/server.go:18-27`、`ai-proxy-go/internal/server/server.go:83-96` |
| **User → Portal HSTS** | ✅ 设置了 `Strict-Transport-Security` | — | `ai-portal-go/internal/web/middleware.go:15` |
| **Proxy → Portal API（auth / pricing / webhooks）** | ✅ 生产环境强制 `https://`；HTTP 客户端使用系统 CA 验证证书 | 无 `MinVersion` 限制，可能协商到旧 TLS；无 mTLS | `ai-proxy-go/internal/config/config.go:181-188`、`ai-proxy-go/internal/auth/auth.go:94-102`、`ai-proxy-go/pkg/billing/billing.go:226-233` |
| **Portal → Proxy（validate-upstream-key 等内部回调）** | ✅ 使用默认 `http.Client`，系统 CA 验证 | 无 `MinVersion` 限制 | `ai-portal-go/internal/routes/admin_common.go:480` |
| **Proxy → Upstream Provider** | ✅ 共享 HTTP 客户端默认验证 TLS；无全局 skip verify | 部分 handler 新建 `http.Client` 无超时/连接池；无 `MinVersion` | `ai-proxy-go/pkg/relay/base/adaptor.go:21-26`、`ai-proxy-go/internal/server/key_validation_handler.go:159,315,368` |
| **Portal → Upstream Provider（key 验证 / 模型同步）** | ✅ 默认校验 TLS；仅 `SKIP_UPSTREAM_TLS_VERIFY=true` 可关闭 | `admin_common.go` 未设置 `MinVersion` | `ai-portal-go/internal/routes/admin_common.go:523-531`、`ai-portal-go/internal/routes/models_sync.go:342-353` |
| **Portal → PostgreSQL** | ✅ `DB_SSLMODE` / `DB_SSL_ROOT_CERT` 环境变量化，本地默认 `disable`，生产可配 `require`/`verify-ca`/`verify-full` | 跨 AZ / 公网 / 合规场景需在 `.env` 中启用并配置 CA | `ai-portal-go/internal/db/db.go`、`ai-portal-go/internal/config/config.go` |
| **Portal → Redis** | ✅ `REDIS_TLS` / `REDIS_TLS_SKIP_VERIFY` 环境变量化 | 本地默认关闭，生产可开启 | `ai-portal-go/internal/db/db.go` |
| **Proxy → Redis** | ✅ `REDIS_TLS` / `REDIS_TLS_SKIP_VERIFY` 环境变量化 | 本地默认关闭，生产可开启 | `ai-proxy-go/internal/config/config.go`、`ai-proxy-go/cmd/server/main.go`、`ai-proxy-go/internal/ratelimit/ratelimit.go`、`ai-proxy-go/pkg/keypool/manager.go` |
| **Portal → SMTP** | ✅ `InsecureSkipVerify: false` | STARTTLS 端口需确认服务器支持 | `ai-portal-go/pkg/mail/mail.go:157-158` |
| **Portal → S3 / 对象存储** | ✅ `UseSSL` 可配置 | — | `ai-portal-go/internal/config/config.go:74` |

### 10.3 关键发现

1. **外部入口已加密**：生产部署文档明确使用 nginx + Let's Encrypt 443 端口终止 TLS，Proxy 与 Portal 均不直接暴露 80/8080/3001 到公网。只要反向代理配置正确，用户流量是加密的。

2. **Proxy ↔ Portal 通过 HTTPS**：`ai-proxy-go/internal/config/config.go:186-187` 已阻止生产环境使用 `http://`（除 loopback 外），因此跨服务器的 Proxy-Portal 通信走 TLS，证书由系统 CA 验证。

3. **上游 Provider 默认 TLS 校验**：Portal 的 key 验证和模型同步仅在 `SKIP_UPSTREAM_TLS_VERIFY=true` 时跳过证书校验，默认安全。Proxy 的 relay adaptor 也使用默认 TLS 验证。

4. **数据库与 Redis 是最大缺口**：
   - Portal DB 连接硬编码 `sslmode=disable`。
   - Portal 与 Proxy 的 Redis 客户端均未配置 `TLSConfig`。
   - 这两个缺口意味着：如果 DB/Redis 不在同一可信网络（例如云托管 RDS / ElastiCache / 跨 AZ / 跨服务器），管理平面与数据平面的内部状态传输是明文的。

### 10.4 必须补强的配置项

| 配置项 | 建议方案 | 影响文件 |
|--------|---------|----------|
| `DB_SSLMODE` | 环境变量，默认 `prefer`，可选 `disable`/`require`/`verify-ca`/`verify-full`；`require` 以上确保强制加密 | `ai-portal-go/internal/config/config.go`、`ai-portal-go/internal/db/db.go` |
| `DB_SSL_ROOT_CERT` | 指定 CA 证书路径，用于 `verify-ca`/`verify-full` | `ai-portal-go/internal/db/db.go` |
| `REDIS_TLS` / `REDIS_TLS_SKIP_VERIFY` | 为 go-redis 配置 `TLSConfig`；生产默认启用并校验 | `ai-portal-go/internal/db/db.go`、`ai-proxy-go/internal/config/config.go`、`ai-proxy-go/cmd/server/main.go`、`ai-proxy-go/internal/ratelimit/ratelimit.go`、`ai-proxy-go/pkg/keypool/manager.go` |
| `TLS_MIN_VERSION` | 所有自定义 `http.Transport` 设置 `MinVersion: tls.VersionTLS12`（建议 `TLS13`） | `ai-proxy-go/internal/auth/auth.go`、`ai-proxy-go/pkg/billing/billing.go`、`ai-portal-go/internal/routes/admin_common.go` |
| 上游 `http.Client` 连接池 | 共享客户端统一设置连接池与 TLS，避免 per-request 新建 client | `ai-proxy-go/internal/server/key_validation_handler.go` |

### 10.5 操作建议

- **✅ 已完成**：`DB_SSLMODE` / `DB_SSL_ROOT_CERT` / `REDIS_TLS` / `REDIS_TLS_SKIP_VERIFY` 已环境变量化，本地默认关闭，生产可按需开启。
- **中期（P1，2-4 周）**：为所有内部 HTTP 客户端设置 `MinVersion: tls.VersionTLS12` 并复用连接池；评估是否需要 Proxy-Portal mTLS（例如通过客户端证书双向认证内部接口）。
- **长期（P2）**：若使用 AWS RDS / ElastiCache / Azure / GCP 托管服务，启用服务端的 TLS 并配置应用端 CA；考虑 VPC peering / private link 进一步缩小暴露面。

---

## 11. 补充后的整体优先级

将第 9、10 章的发现纳入后，**Redis 分离与热更新通道改造**、**DB / Redis TLS 配置化**、**sync.Pool**、**OpenTelemetry trace** 已落地。下一步继续推进：

1. **性能优化（第 2、4 章）**：消除 upstream 响应重复 JSON 解析、keypool 锁粒度优化、批量写入。
2. **可观测性（第 3 章）**：Prometheus client、histogram、补齐 dead metrics。
3. **配置热更新**：版本号 + 增量同步，减少全量拉取。
4. **战略能力（Phase 3）**：Canonical internal model、Virtual Key、语义缓存、MCP Gateway、Alerting。

这些项相互独立，可分阶段实施，不影响现有业务逻辑。
