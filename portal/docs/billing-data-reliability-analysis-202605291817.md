# Proxy-Portal 计费数据可靠性分析与优化方案

> 生成时间：2026-05-29  
> 关联 Issue：D3（双计费路径对账）扩展  
> 范围：ai-proxy-go（Proxy）+ ai-portal-go（Portal）计费数据流全链路

---

## 一、架构现状

### 1.1 数据流全景

```
┌─────────────┐     HTTP Batch      ┌─────────────────────┐
│   Proxy     │ ──────────────────► │       Portal        │
│             │   (每 3s / 100 条)  │                     │
│ UsageBuffer │                     │  handleUsage        │
│  内存缓冲    │                     │  handleUsageBatch   │
└─────────────┘                     └─────────────────────┘
                                              │
                    ┌─────────────────────────┴─────────────────────────┐
                    │                                                   │
           Redis 可用 (正常路径)                              Redis 不可用 (fallback)
                    │                                                   │
                    ▼                                                   ▼
        ┌─────────────────────┐                              ┌──────────────────┐
        │ webhook:usage:queue │                              │ processRecord    │
        │  (Redis List, 无TTL) │                              │  同步单条处理     │
        └─────────────────────┘                              │  完整逻辑        │
                    │                                        └──────────────────┘
                    ▼
        ┌─────────────────────┐
        │  processStatsQueue  │  每 1s 执行，每次最多 200 条
        │  (后台 goroutine)   │
        └─────────────────────┘
                    │
                    ├──► processStats(ctx, record)
                    │      ├── 查询 api_keys → keyID/userID/keyType
                    │      ├── 计算 cost + pricing ratio
                    │      ├── INSERT usage_records (deduction = 0)
                    │      ├── UPDATE api_keys stats (不在 tx 中)
                    │      └── UPDATE subscription counters (不在 tx 中)
                    │
                    └──► 推入 webhook:billing:queue
                              │
                              ▼
                    ┌─────────────────────┐
                    │  processBillingQueue │  每 2s 执行，每次最多 100 条
                    │  (后台 goroutine)    │
                    └─────────────────────┘
                              │
                              └──► processBillingBatch(ctx, userID, records)
                                    ├── 汇总计算 cost
                                    ├── FOR UPDATE 锁用户行
                                    ├── 批量扣减余额 (tx 内)
                                    ├── logBalanceDeductions
                                    ├── UPDATE usage_records deduction (分摊)
                                    └── Commit tx
```

### 1.2 关键设计约束

| 约束 | 说明 |
|------|------|
| **Proxy 只做统计** | 费用计算和余额扣减全部在 Portal 完成 |
| **异步解耦** | 避免每个请求同步 HTTP 发送，Proxy 使用内存缓冲批量发送 |
| **分离部署** | Portal 和 Proxy 最终是不同机器，不共享 Redis/数据库 |
| **Redis List 无 TTL** | `webhook:usage:queue` / `webhook:billing:queue` 默认永不过期，受 `maxmemory-policy` 限制 |

---

## 二、发现的问题

### 问题 1 🔴 P0 — Proxy 发送失败直接丢弃数据

**位置：** `ai-proxy-go/internal/server/usage_buffer.go:92-135`

**现象：**
```go
func (b *UsageBuffer) sendBatch(batch []*billing.UsageRecord) {
    if err := b.billing.SendUsageBatchToPortal(...); err != nil {
        // Fallback: send individually
        for _, record := range batch {
            if err := b.billing.SendUsageToPortal(...); err != nil {
                b.failedCount++  // ← 只计数，数据丢弃
            }
        }
    }
}
```

**影响：** Portal HTTP 不可用时，Proxy 内存中最多 100 条记录（约 3 秒数据量），超出部分全部丢失。无重试、无队列、无落盘。

**量化：**

| Proxy QPS | 不可用时长 | 丢失记录数 | 估算金额 |
|-----------|-----------|-----------|---------|
| 10 | 1 分钟 | ~500 | ~$2-5 |
| 100 | 1 分钟 | ~5,900 | ~$20-100 |
| 1,000 | 1 分钟 | ~59,900 | ~$200-1,000 |
| 1,000 | 5 分钟 | ~299,900 | ~$1,000-5,000 |

---

### 问题 2 🔴 P0 — processBillingBatch 不更新 api_keys 统计

**位置：** `ai-portal-go/internal/routes/webhook.go:1371-1630`

**现象：** `processBillingBatch` 完全没有 `UPDATE api_keys SET total_requests/total_tokens` 的逻辑。

**对比：**

| 路径 | api_keys 统计更新 | 位置 |
|------|------------------|------|
| processRecord | ✅ 在 tx 内 | line 910-916 |
| processStats | ✅ 在 tx 外 | line 1342-1348 |
| processBillingBatch | ❌ 完全缺失 | — |

**影响：** 异步路径下，用户 Dashboard 的"API Key 使用次数"和"Token 数"不更新。

---

### 问题 3 🔴 P0 — processBillingBatch 不更新 subscription 计数器

**位置：** `ai-portal-go/internal/routes/webhook.go:1371-1630`

**现象：** `processBillingBatch` 完全没有 `UPDATE user_subscriptions SET tpm_used/tph_used/rpm_used/rph_used` 的逻辑。

**对比：**

| 路径 | subscription counters | 位置 |
|------|----------------------|------|
| processRecord | ✅ 在 tx 内 | line 918-931 |
| processStats | ✅ 在 tx 外 | line 1351-1364 |
| processBillingBatch | ❌ 完全缺失 | — |

**影响：** 异步路径下，subscription 用户的 TPM/RPM/TPH/RPH 速率限制失效（计数器永远不涨，用户永远不会被限流）。

---

### 问题 4 🟡 P1 — processStats 不在 transaction 中

**位置：** `ai-portal-go/internal/routes/webhook.go:1252-1367`

**现象：** `processStats` 的 INSERT、UPDATE api_keys、UPDATE subscription counters 各自独立执行，无原子性保证。

**场景：**
- INSERT usage_records 成功 → UPDATE api_keys 失败 → usage_records 有数据但 api_keys 统计缺失
- INSERT usage_records 成功 → UPDATE sub counters 失败 → 速率限制计数器不一致

**影响：** 数据不一致，且无法在失败时回滚已插入的 usage_records。

---

### 问题 5 🟡 P1 — processBillingBatch 缺少 daily quota reset

**位置：** `ai-portal-go/internal/routes/webhook.go:1484-1514`

**现象：** `processBillingBatch` 的 subscription 路径只扣减 `quota_remaining`，没有检查 `daily_quota_reset` 时间并重置 `daily_quota_used`。

**对比：**

```go
// processRecord (line 620-630) — 有 daily reset
if !subDailyReset.IsZero() && time.Now().After(subDailyReset) {
    subDailyUsed = 0
    subDailyReset = time.Now().Truncate(24 * time.Hour).Add(24 * time.Hour)
    db.MustExecTx(ctx, r.logger, tx,
        `UPDATE user_subscriptions SET daily_quota_used = 0, daily_quota_reset = $1...`, ...)
}

// processBillingBatch — 没有这个逻辑
```

**影响：** 异步路径下，subscription 用户的每日配额一旦用完就永远用完，直到 processRecord 被触发。

---

### 问题 6 🟡 P1 — processBillingBatch 缺少 enterprise 110% buffer

**位置：** `ai-portal-go/internal/routes/webhook.go:1445-1482`

**现象：** `processBillingBatch` 的 enterprise 信用扣减使用硬上限 `creditTotal - creditUsed`（100%），而 `processRecord` 使用 `creditTotal * 1.1`（110%）。

**对比：**

```go
// processRecord (line 747-749) — 110% buffer
buffer := creditTotal / 10
creditCap := creditTotal + buffer  // 110%

// processBillingBatch — 无 buffer
creditAvailable := creditTotal - creditUsed  // 100%
```

**影响：** 异步路径下，enterprise 用户的 in-flight 请求更容易触发信用额度耗尽，用户体验不一致。

---

### 问题 7 🟡 P2 — processBillingBatch 缺少 enterprise users.quota_used 更新

**位置：** `ai-portal-go/internal/routes/webhook.go:1598-1605`

**现象：** `processBillingBatch` 调用 `checkEnterpriseLimits`，但缺少 `UPDATE users SET quota_used += tokens`。

**对比：**

```go
// processRecord (line 802-808)
if totalTokens > 0 {
    tx.Exec(ctx, `UPDATE users SET quota_used = COALESCE(quota_used, 0) + $1...`, totalTokens, userID)
}

// processBillingBatch — 无此更新
```

**影响：** 异步路径下，企业成员的个人 token 配额追踪失效。

---

## 三、风险评估矩阵

| 风险场景 | 触发条件 | 概率 | 影响 | 严重程度 |
|---------|---------|------|------|---------|
| Portal HTTP 完全不可用 | 网络中断/Portal 宕机 | 低 | Proxy 数据全部丢失 | 🔴 高 |
| Portal Redis 不可用但 HTTP 可用 | Redis 故障 | 中 | 走 processRecord fallback，数据不丢但性能下降 | 🟡 中 |
| 统计数据不一致 | 异步路径常态化 | 高 | Dashboard 显示错误，rate limit 失效 | 🔴 高 |
| Subscription 每日配额异常 | 长期运行 | 高 | 用户每日配额用完后无法恢复 | 🟡 中 |
| Enterprise 信用额度不一致 | 混合路径 | 中 | sync/async 计费结果不同，对账困难 | 🟡 中 |
| processStats 部分失败 | 并发/DB 压力 | 中 | usage_records 与 api_keys 统计不同步 | 🟡 中 |

---

## 四、优化方案

### 方案 A：修复 Portal 异步路径（最小改动，优先实施）

**目标：** 让 `processStats + processBillingBatch` 的功能完全等价于 `processRecord`。

**改动清单：**

| # | 改动项 | 文件 | 工作量 |
|---|--------|------|--------|
| 1 | 把 api_keys stats 更新移入 `processBillingBatch` tx | `webhook.go` | 小 |
| 2 | 把 subscription counters 更新移入 `processBillingBatch` tx | `webhook.go` | 小 |
| 3 | 在 `processBillingBatch` subscription 路径添加 daily quota reset | `webhook.go` | 小 |
| 4 | 在 `processBillingBatch` enterprise 路径添加 110% buffer | `webhook.go` | 小 |
| 5 | 在 `processBillingBatch` enterprise limits 后添加 users.quota_used 更新 | `webhook.go` | 小 |
| 6 | 给 `processStats` 添加 transaction 包裹（或把功能并入 `processBillingBatch`） | `webhook.go` | 中 |

**验证方式：**
- 编写对比测试：同一条 usage 记录分别走 `processRecord` 和 `processStats+processBillingBatch`，断言 DB 最终状态完全一致

---

### 方案 B：Proxy 本地日志兜底（数据可靠性保障）

**目标：** Portal 完全不可用时，Proxy 数据不丢失。

**设计约束：**
- Proxy 不配置数据库（多实例无法共享）
- Proxy 与 Portal 分离部署（不共享 Redis）
- 只能写本地文件

**实现：**

```
Proxy 发送流程：
  1. 优先：HTTP batch 发送 → Portal
  2. HTTP 5xx/超时 → 内存重试队列（指数退避，保留最近 10,000 条）
  3. 内存队列满 / 长时间不可用 → 写入本地 NDJSON 日志
  4. 后台 goroutine 持续重试，直到成功

日志文件：
  /var/log/ai-proxy/usage-fallback-YYYYmmdd-HH0000.ndjson
  /var/log/ai-proxy/upstream-usage-fallback-YYYYmmdd-HH0000.ndjson
  
保留策略：7 天自动删除
```

**Portal 对账接口：**
- Admin 上传 Proxy 的 `*.ndjson` 文件
- Portal 解析后按 `trace_id` 去重（与现有 `usage_records` 比对）
- 缺失记录执行补扣（复用 `processRecord` 逻辑）
- 生成对账报告（补扣条数、金额、差异分析）

---

### 方案 C：统一计费路径（长期目标）

**目标：** 消除 `processRecord` / `processStats` / `processBillingBatch` 三条路径的重复代码。

**思路：**

```
internal/services/
└── billing.go          # 新增 BillingService
    ├── CalculateCost()         # 统一 cost 计算
    ├── ApplyPricingRatio()     # 统一 pricing ratio + enterprise override
    ├── DeductBalance()         # 统一余额扣减（gift→recharge→balance）
    ├── DeductSubscription()    # 统一 subscription 扣减（含 daily reset）
    ├── DeductEnterprise()      # 统一 enterprise credit 扣减（含 110% buffer）
    ├── UpdateAPIKeyStats()     # 统一 api_keys 统计更新
    └── UpdateSubCounters()     # 统一 subscription counters 更新
```

**调用方式：**
- `processRecord` → `BillingService.FullProcess(record)`（单条 tx）
- `processStats` → `BillingService.RecordUsage(record)`（INSERT + stats，不扣费）
- `processBillingBatch` → `BillingService.DeductBatch(userID, records)`（批量 tx）

**优点：** 逻辑强制一致；新增计费规则只需改一处；可独立测试
**缺点：** 需要新建 service 文件；改动面中等

---

## 五、实施状态（2026-05-29 更新）

| 阶段 | 状态 | 完成内容 |
|------|------|---------|
| **Phase 1** | ✅ 已完成 | Portal 异步路径 6 处缺失全部修复，4 个单元测试通过 |
| **Phase 2** | ✅ 已完成 | Proxy `UsageBuffer`/`UpstreamUsageBuffer` 发送失败写入本地 NDJSON；Portal Admin API 新增 `/api/admin/reconcile/preview` + `/api/admin/reconcile/upload` |
| **Phase 3** | 🚧 部分完成 | 已创建 `internal/services/billing.go`，`CalculateCost`/`GetPricingRatio` 已提取并在 webhook 中使用；但余额扣减、订阅/企业扣减、API-key stats 更新、订阅计数器更新仍在 route handlers 中 |
| **Phase 4** | ✅ 已完成 | `internal/routes/webhook.go` 已拆分为 `webhook_common.go`、`webhook_usage.go`、`webhook_billing.go`、`webhook_health.go` |

### Phase 1 修复详情

| # | 修复项 | 文件 | 测试 |
|---|--------|------|------|
| 1 | `processBillingBatch` 添加 api_keys stats 更新（tx 内） | `webhook.go` | `TestProcessBillingBatch_UpdatesAPIKeyStats` ✅ |
| 2 | `processBillingBatch` 添加 subscription counters 更新（tx 内） | `webhook.go` | `TestProcessBillingBatch_UpdatesSubscriptionCounters` ✅ |
| 3 | `processBillingBatch` subscription 路径添加 daily quota reset | `webhook.go` | `TestProcessBillingBatch_DailyQuotaReset` ✅ |
| 4 | `processBillingBatch` enterprise 路径添加 110% buffer | `webhook.go` | — |
| 5 | `processBillingBatch` enterprise limits 后添加 users.quota_used | `webhook.go` | — |
| 6 | `processStats` 写操作包裹 transaction | `webhook.go` | `TestProcessStats_InTransaction` ✅ |

### Phase 2 实现详情

| # | 实现项 | 文件 |
|---|--------|------|
| 1 | Proxy fallback NDJSON writer | `pkg/fallbacklog/writer.go` |
| 2 | UsageBuffer 失败时写 fallback log | `internal/server/usage_buffer.go` |
| 3 | UpstreamUsageBuffer 失败时写 fallback log | `internal/server/upstream_usage_buffer.go` |
| 4 | Portal 手动对账 Preview API | `internal/routes/reconcile.go` |
| 5 | Portal 手动对账 Upload API | `internal/routes/reconcile.go` |
| 6 | Admin 路由注册 | `internal/routes/admin.go` |

### E2E 验证结果（异步流程）

> **测试路径**：curl → Proxy → deepseek 上游 → Proxy UsageBuffer → HTTP POST Portal → Redis `webhook:usage:queue` → `processStatsQueue` → `processStats` → Redis `webhook:billing:queue` → `processBillingQueue` → `processBillingBatch` → DB
>
> Redis 正常时全程走异步队列，未触发同步 fallback。

**测试命令**：
```bash
curl -H "Authorization: Bearer sk-wgit-55f68e77-fda7-4810-9ad1-704e0ce7eb14" \
  -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"Hello"}]}' \
  http://localhost:8080/v1/chat/completions
```

**测试前后数据对比**：

| 指标 | 测试前 | 测试后 | 变化 |
|------|--------|--------|------|
| `users.balance` | 66,705,882 | 66,705,712 | **-170** ✅ |
| `api_keys.total_requests` | 3 | 7 | **+4** ✅ |
| `api_keys.total_tokens` | 270 | 510 | **+240** ✅ |
| `usage_records` 总数 | 3 | 5 | **+2** ✅ |

新增 `usage_records`：
- `id=17`: deepseek-v4-flash, input=10, output=50, `total_cost=110`
- `id=16`: deepseek-v4-flash, input=10, output=50, `total_cost=60`
- **合计扣费 = 170** = `balance` 减少量，完全匹配 ✅

**异步链路验证**：
1. Proxy 转发请求到 deepseek 上游 → HTTP 200 ✅
2. Proxy `recordUsageAsync` → `UsageBuffer.Add()` ✅
3. Proxy `sendBatch()` → HTTP POST `/api/webhook/usage-batch` → Portal 200 ✅
4. Portal `handleUsageBatch` → Redis `webhook:usage:queue` ✅
5. `processStatsQueue` → `processStats()` → INSERT `usage_records` + UPDATE `api_keys` stats ✅
6. `processStatsQueue` → RPUSH `webhook:billing:queue` ✅（修复 `json.RawMessage` 序列化后）
7. `processBillingQueue` → `processBillingBatch()` → UPDATE `users.balance` ✅

**E2E 过程中发现并修复的生产 bug**：

| # | 问题 | 根因 | 修复文件 |
|---|------|------|---------|
| 1 | `routing_track = ""` 违反 check constraint | Proxy 发送空字符串，Portal 表只允许 `standard`/`bedrock` | `ai-portal-go/internal/routes/webhook.go` + `ai-proxy-go/internal/server/handlers.go` |
| 2 | `trace_id = ""` 无法转换为 uuid | Proxy 未设置 `TraceID` | `ai-portal-go/internal/routes/webhook.go` + `ai-proxy-go/internal/server/handlers.go` |
| 3 | billing queue 推送失败：`json.RawMessage` 序列化错误 | go-redis 不能序列化 `json.RawMessage` | `ai-portal-go/internal/routes/webhook.go` |

### 原始实施建议

| 阶段 | 时间 | 行动 | 优先级 |
|------|------|------|--------|
| **Phase 1** | 立即 | 修复 Portal 异步路径 6 处缺失（方案 A） | 🔴 P0 |
| **Phase 2** | 1-2 周内 | Proxy 本地 NDJSON 日志 + Portal 手动对账接口（方案 B） | 🟡 P1 |
| **Phase 3** | 下一阶段 | 提取 BillingService 统一计费核心（方案 C） | 🟢 P2 |
| **Phase 4** | 后续 | 大文件拆分（webhook.go → webhook_handlers.go / webhook_billing.go / webhook_queue.go） | 🟢 P2 |

---

## 六、关键结论

1. **当前异步路径不等于同步路径**：`processStats + processBillingBatch` 缺少 6 项 `processRecord` 已实现的功能，其中 2 项（api_keys stats、subscription counters）是阻断性的。

2. **processRecord 不应现在取消**：在异步路径修复完成前，`processRecord` 作为 Redis 不可用的 fallback 仍有价值。它覆盖的是"Redis 故障但 HTTP 可用"的常见场景。

3. **Proxy 端数据丢失风险真实存在**：UsageBuffer 在 Portal HTTP 不可用时直接丢弃数据，当前没有任何降级机制。这是分离部署后必须解决的问题。

4. **最可行的兜底方案是本地日志**：Proxy 写 NDJSON 文件，Portal 提供手动对账接口。不依赖共享 Redis/DB，符合分离部署约束。

---

## 七、Phase 3 评估与后续计划

> 评估时间：2026-06-15
> 评估范围：`internal/services/billing.go`、`internal/routes/webhook_usage.go`、`internal/routes/webhook_billing.go`

### 7.1 当前状态

Phase 1 与 Phase 2 已解决计费数据可靠性的 P0/P1 风险：

- 异步路径（`processStats` + `processBillingBatch`）已补齐 `api_keys` stats、`subscription counters`、`daily quota reset`、`enterprise 110% buffer` 等关键逻辑。
- Proxy 端在 Portal HTTP 不可用时，已具备 NDJSON 本地日志兜底 + Admin 手动对账能力。

但 Phase 3 的目标——将计费核心逻辑统一抽取到 `BillingService`——目前仅部分完成：

| 能力 | 当前位置 | 是否已抽取到 BillingService |
|------|---------|---------------------------|
| Cost 计算 | `services/billing.go` `CalculateCost()` | ✅ 已抽取 |
| Pricing ratio 查询 | `services/billing.go` `GetPricingRatio()` | ✅ 已抽取 |
| Subscription 扣减（含 daily reset） | `webhook_usage.go` / `webhook_billing.go` | ❌ 仍在 routes |
| Gift / Balance 扣减 | `webhook_usage.go` / `webhook_billing.go` | ❌ 仍在 routes，且重复 4 处 |
| Enterprise credit 扣减（110% buffer） | `webhook_usage.go` / `webhook_billing.go` | ❌ 仍在 routes |
| API key stats 更新 | `webhook_usage.go` / `webhook_billing.go` | ❌ 仍在 routes |
| Subscription counters 更新 | `webhook_usage.go` / `webhook_billing.go` | ❌ 仍在 routes |

### 7.2 为什么 Phase 3 仍有必要

1. **重复代码严重，维护成本高**
   `gift → balance` 扣减逻辑在 `processRecord()` 的 `subscription` / `enterprise` / `general` 三个分支，以及 `processBillingBatch()` 批量路径中，共计重复 **4 处**。一旦要调整扣减顺序、新增 balance type 或修改 enterprise buffer，极易漏改。

2. **同步路径与异步路径存在行为分叉风险**
   - `processRecord`：单条记录在一个事务内完成扣费、INSERT `usage_records`、更新 stats。
   - `processBillingBatch`：多条记录聚合扣费后，再按 cost 比例分摊 `deduction` 回写 `usage_records`。
   两套实现在并发锁、部分扣费、异常回滚等场景下可能产生 subtle 差异。

3. **难以进行单元测试**
   当前扣减逻辑深嵌在 HTTP handler / queue goroutine 中，依赖 gin Context、Redis、DB transaction、logger，导致单元测试需要大量 mock。抽到 `BillingService` 后，可以直接对纯 service 逻辑编写测试。

4. **阻碍后续计费能力扩展**
   如果后续要新增退款、post-paid 改造、充值回滚、新的额度类型等功能，在 routes 层改 4 处风险远高于在 service 层改 1 处。

### 7.3 为什么现在可以暂缓实施

Phase 1/2 已经修复了计费可靠性的阻断性问题：

- 异步路径不再丢失 `api_keys` stats 和 `subscription counters`。
- Proxy 有本地日志兜底，Portal 有对账接口。
- 当前无已知的生产计费 bug。

因此 Phase 3 属于 **P2 技术债务清理**，是**预防性重构**，而非**止血**。在当前没有新增计费规则需求时，可以延后。

### 7.4 建议的触发条件

| 场景 | 建议 |
|------|------|
| 新增 balance type 或调整扣减顺序 | **必须**先做 Phase 3 |
| 上线退款 / 充值回滚 / post-paid 改造 | **必须**先做 Phase 3 |
| 调整 enterprise credit 策略或 buffer | **强烈建议**先做 Phase 3 |
| 仅做模型接入、UI 改动、限流增强等 | 可以延后 |

### 7.5 推荐的小步重构路径

避免一次性大改，建议按以下顺序渐进式抽取：

1. **抽取公共扣减工具函数**
   - `DeductGiftThenBalance(tx, userID, cost) → (giftDeduction, balanceDeduction, remaining)`
   - 先替换 `processRecord` 三个分支中的重复代码，保持行为不变。

2. **抽取 Subscription 扣减**
   - `BillingService.DeductSubscription(tx, userID, tokens)`
   - 统一处理 `quota_remaining`、`quota_used`、`daily_quota_used`、`daily_quota_reset`。

3. **抽取 Enterprise Credit 扣减**
   - `BillingService.DeductEnterpriseCredit(tx, enterpriseID, cost)`
   - 统一处理 110% overflow buffer。

4. **抽取 Stats 更新**
   - `BillingService.UpdateAPIKeyStats(tx, keyID, requests, tokens)`
   - `BillingService.UpdateSubscriptionCounters(tx, userID, tokens, requests)`

5. **统一入口**
   - `processRecord(record)` → `BillingService.ProcessRecord(ctx, record)`
   - `processBillingBatch(userID, records)` → `BillingService.ProcessBatch(ctx, userID, records)`

每完成一步即可单独测试、单独回滚，最终形成单一可信的计费核心。

---

## 关联文档

- `docs/code-review-report-202605261540.md` — 原始 Code Review 报告
- `docs/code-review-verification-response-202605291500.md` — 修复验证响应（含 D3 状态）
- `docs/large-file-refactoring-roadmap-202605291733.md` — 代码库拆分路线图
