> **ACTIVE** — Billing Pipeline Optimization Design

# Billing Pipeline Optimization Design

> 目标：优化 Portal 计费处理性能，从每条记录 14 次 DB 操作降至 6-8 次；将 token 统计与计费扣款分离为异步 pipeline；评估 Proxy 报告模式的 batch 化可行性
> **Created:** 2026-05-16
> **Scope:** `ai-proxy-go` (Proxy) + `ai-portal-go` (Portal)

---

## 1. 当前架构与瓶颈分析

### 1.1 数据流

```
Proxy 请求完成 → go func() 计算cost → HTTP POST /api/webhook/usage (逐条)
  → Portal Redis RPush → 每1秒 pop 100条 → processRecord() 逐条处理
    → 单事务 14次DB操作 → commit
```

### 1.2 Proxy 侧现状

- **报告方式**：逐条 HTTP POST，`SendUsageToPortal()` 每个请求一个 goroutine
- **已有 batch 函数**：`SendUsageBatchToPortal()` 已实现但未使用
- **无本地缓冲**：请求完成后立即发送，无聚合窗口
- **计费参与**：Proxy 计算 `record.Cost`（通过 `CalculateCostWithCache()`），违反"proxy仅统计，portal计费"原则

### 1.3 Portal 侧现状

- **Redis 队列**：`webhook:usage:queue`，每秒 pop 最多 100 条
- **processRecord()**：单条记录、单事务、14次DB操作
- **瓶颈**：每秒最多处理 100 条记录，QPS=1000 时 900 条/秒积压

### 1.4 processRecord 的 14 次 DB 操作

| # | 操作 | general | subscription | enterprise |
|---|------|---------|-------------|------------|
| 1 | SELECT api_keys (key→user→group→key_type) | ✅ | ✅ | ✅ |
| 2 | SELECT service_models (pricing) | ✅ | ✅ | ✅ |
| 3 | SELECT system_settings (exchange rate) | ✅ | ✅ | ✅ |
| 4 | SELECT user_groups (pricing_ratio) | ✅ | ✅ | ✅ |
| 5 | SELECT user_subscriptions (check active) | ❌ | ✅ | ✅ |
| 6 | UPDATE user_subscriptions (deduct quota) | ❌ | ✅ | ✅ |
| 7 | SELECT users FOR UPDATE (lock + balances) | ✅ | ✅ | ✅ |
| 8 | SELECT users FOR UPDATE (totalBalance) | ✅ | ✅ | ✅ |
| 9 | SELECT enterprises (credit) | ❌ | ❌ | ✅ |
| 10 | UPDATE enterprises (deduct credit) | ❌ | ❌ | ✅ |
| 11 | UPDATE users (deduct balances) | ✅ | ✅ | ✅ |
| 12 | INSERT balance_transactions ×1~3 | 1次 | 1~2次 | 2~3次 |
| 13 | INSERT usage_records | ✅ | ✅ | ✅ |
| 14 | UPDATE api_keys (stats) | ✅ | ✅ | ✅ |
| 15 | UPDATE user_subscriptions (tpm/rpm counters) | ❌ | ✅ | ✅ |

**问题**：所有 key_type 完全相同路径，enterprise key 不应查 subscription，general key 不应查 subscription/enterprise。

### 1.5 计费架构原则冲突

原始设计文档（`docs/ai-proxy-go-design-202604291600.md`）第4.4节 handler 流程第12步：

```
pricing := portalClient.GetPricing(req.Model)
cost := billing.CalculateCostWithCache(usage, pricing)
```

**原始设计就是 Proxy 计算cost**，与后来提出的"proxy仅统计，portal计费"原则冲突。

当前实现：
- Proxy 计算 `record.Cost` → 传给 Portal
- Portal `calculateCostFromPricing()` 发现 `record.Cost > 0` → 直接使用 → 再乘 `pricingRatio`
- **没有重复计费**（ratio 只乘一次），但 Portal 依赖 Proxy 的预计算值，不是计费唯一权威

风险：如果有人在 Proxy 侧也加 ratio 计算，就会 double-count。

---

## 2. 优化方案

### 2.1 优化一：key_type 短路 — DB 操作数优化

**原理**：根据 `api_keys.key_type` 决定计费路径，跳过无关查询。

#### 各 key_type 的计费路径

| key_type | 计费路径 | DB操作数 |
|----------|---------|---------|
| general | Gift → Recharge → Balance | 6-7次 |
| subscription | Subscription → Gift → Recharge → Balance | 8-9次 |
| enterprise | Gift → Enterprise Credit → Balance | 7-8次 |

#### General key 优化后路径（6-7次）

1. SELECT api_keys (key→user→group→key_type) — 1次
2. SELECT service_models + exchange rate — 可合并或缓存 → 1次（缓存后0次）
3. SELECT user_groups (pricing_ratio) — 可缓存 → 1次（缓存后0次）
4. SELECT users FOR UPDATE（合并 #7+#8，一次取 gift/recharge/total balance + enterprise_id）— 1次
5. UPDATE users (deduct balances) — 1次
6. INSERT balance_transactions ×1 — 1次
7. INSERT usage_records — 1次
8. UPDATE api_keys (stats) — 1次

**缓存 pricing_ratio 和 exchange rate 后：6次**

#### Enterprise key 优化后路径（7-8次）

1. SELECT api_keys — 1次
2. SELECT service_models + exchange rate（缓存后0次）— 1次
3. SELECT user_groups (pricing_ratio，缓存后0次) — 1次
4. SELECT users FOR UPDATE（合并取所有余额 + enterprise_id）— 1次
5. SELECT enterprises (credit) — 1次
6. UPDATE enterprises (deduct credit) — 1次
7. UPDATE users (deduct gift + balance) — 1次
8. INSERT balance_transactions ×2 — 2次
9. INSERT usage_records — 1次
10. UPDATE api_keys — 1次

**缓存后：7次**

#### Subscription key 优化后路径（8-9次）

1. SELECT api_keys — 1次
2. SELECT service_models + exchange rate（缓存后0次）— 1次
3. SELECT user_groups (pricing_ratio，缓存后0次) — 1次
4. SELECT user_subscriptions (check active) — 1次
5. UPDATE user_subscriptions (deduct quota) — 1次
6. SELECT users FOR UPDATE（合并取所有余额）— 1次
7. UPDATE users (deduct balances) — 1次
8. INSERT balance_transactions ×1~2 — 1~2次
9. INSERT usage_records — 1次
10. UPDATE api_keys — 1次

**缓存后：8次**

#### 合并 #7+#8 的具体改动

当前代码分两次 SELECT users FOR UPDATE：
```go
// #7: 取 gift_balance, recharge_balance, enterprise_id
SELECT COALESCE(gift_balance, 0), COALESCE(recharge_balance, 0), enterprise_id
 FROM users WHERE id = $1::uuid FOR UPDATE

// #8: 取 total balance
SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid FOR UPDATE
```

合并为一次：
```go
SELECT COALESCE(gift_balance, 0), COALESCE(recharge_balance, 0),
       COALESCE(balance, 0), enterprise_id
 FROM users WHERE id = $1::uuid FOR UPDATE
```

#### 缓存策略

| 数据 | 缓存方式 | TTL | 更新触发 |
|------|---------|-----|---------|
| pricing_ratio | 内存 map[groupID→ratio] | 5分钟 | group 设置变更时主动刷新 |
| exchange rate | ExchangeService 已缓存 | 8小时 | 定时自动更新 |
| service_models pricing | 内存 map[model+provider→pricing] | 5分钟 | 模型价格变更时主动刷新 |

---

### 2.2 优化二：统计与计费分离 — Pipeline 模式

**核心思路**：将 `processRecord` 拆为两个阶段：

1. **统计阶段（轻量、实时）**：INSERT usage_records + UPDATE api_keys stats
2. **计费阶段（重量、可 batch）**：查余额、扣款、写 balance_transactions

#### 分离后的流程

```
Proxy → Portal webhook → Redis queue "webhook:usage:queue"
  → 统计处理器（每秒 pop 200条）：
      → INSERT usage_records（批量 INSERT，单事务）
      → UPDATE api_keys stats（批量 UPDATE）
      → 将记录推入计费队列 "webhook:billing:queue"
  → 计费处理器（每秒 pop 50条，按 user 分组 batch）：
      → 同一 user 的多条记录合并扣款（1次 FOR UPDATE + 1次 UPDATE）
      → INSERT balance_transactions（批量）
```

#### 统计阶段（轻量）

每条记录仅需 2 次 DB 操作：
1. INSERT usage_records（可批量：`INSERT ... VALUES ($1,...), ($2,...), ...`）
2. UPDATE api_keys stats（可批量：按 key_id 聚合后 UPDATE）

**200条/秒 → 批量 INSERT 1次 + 批量 UPDATE N次（N=不同key数） → 约 5-10次DB操作处理200条**

#### 计费阶段（重量，可 batch）

按 user_id 分组后，同一 user 的多条记录合并：
- 总 cost = Σ(cost_i)
- 1次 SELECT users FOR UPDATE（取所有余额）
- 1次 UPDATE users（扣款）
- N次 INSERT balance_transactions（每种余额类型1条）
- subscription/enterprise 按需：+2~4次

**50条/秒 → 按 user 分组后约 10-20 个 user → 约 30-60次DB操作处理50条 → 远优于当前 50×14=700次**

#### 关键问题：余额不足检测延迟

分离后，计费阶段有延迟（统计实时，扣款异步）。用户余额为 0 后，可能继续使用数秒直到计费处理器扣款并触发拒绝。

**用户要求**：可允许统计和计费有细微误差，但不能让用户 quota 满了之后还能继续使用数十秒。

#### 解决方案：Proxy 侧余额预检 + Portal 侧余额缓存

**方案 A：Proxy 侧实时余额预检（推荐）**

Proxy 已有余额检查机制（`auth.go:713` `Balance <= 0 → BALANCE_EXHAUSTED`）。当前是每次请求都查 Portal 的 `/api/auth/validate`。

优化：在 Proxy 内存缓存用户余额，TTL=10秒：
```
Proxy auth cache (5分钟 TTL) → 增加 balance 字段
  → 每次请求检查 cached_balance > 0
  → cached_balance ≤ 0 → 立即拒绝（402 BALANCE_EXHAUSTED）
  → cached_balance > 0 → 允许请求
  → 10秒后刷新 balance（从 Portal /api/auth/validate 或专用 /api/user/balance 接口）
```

**延迟窗口**：最多 10 秒。用户余额耗尽后，最多多使用 10 秒（而非数十秒）。

**进一步收紧**：当 Portal 计费处理器发现余额接近 0 时，主动推送余额更新给 Proxy（通过 Redis pub/sub 或专用 API），Proxy 立即更新缓存。

**方案 B：Portal 侧余额阈值告警**

计费处理器扣款后，如果余额低于阈值（如 ¥1），立即：
1. 更新 Redis 中的用户余额缓存（`user_balance:{user_id}`）
2. Proxy 下次 validate 时读到余额 ≤ 0 → 拒绝

延迟取决于 Proxy validate 的调用频率（当前每次请求都调用，但结果缓存5分钟）。

**推荐方案 A + B 组合**：
- Proxy 内存缓存余额，10秒 TTL，余额 ≤ 0 立即拒绝
- Portal 计费处理器发现余额 ≤ 阈值 → 主动通知 Proxy 更新缓存
- 最大延迟：10秒（Proxy 缓存 TTL）

---

### 2.3 优化三：Proxy 报告模式评估

#### 当前模式：逐条 HTTP POST

```
每个请求 → go func() → HTTP POST /api/webhook/usage
```

**优点**：
- 实时性最高，Portal 立即收到统计数据
- 实现简单，无缓冲逻辑

**缺点**：
- HTTP 连接开销大（每条记录一次 TCP+TLS+HTTP）
- Proxy 侧 goroutine 数量与请求量成正比
- Portal webhook handler 压力大（每秒 N 次 HTTP 请求）

#### Batch 模式：本地缓冲 + 定时批量发送

```
请求完成 → 写入本地缓冲 channel（容量 1000）
  → 定时器（每 2-5 秒）或缓冲满时 → SendUsageBatchToPortal()
```

**优点**：
- HTTP 连接数大幅减少（每 2-5 秒 1 次 vs 每请求 1 次）
- Portal webhook handler 压力降低
- `SendUsageBatchToPortal()` 已实现，改动小

**缺点**：
- 统计数据延迟 2-5 秒到达 Portal
- 余额耗尽检测延迟增加（但方案 A 的 Proxy 缓存预检独立于统计数据到达时间）

#### 推荐方案

**Proxy 采用 batch 模式，缓冲窗口 3-5 秒**：

理由：
1. 统计数据延迟 3-5 秒可接受（用户要求允许细微误差）
2. 余额耗尽检测不依赖统计数据到达时间——由 Proxy 侧余额缓存预检解决
3. HTTP 连接开销大幅降低，Proxy 和 Portal 双方受益
4. `SendUsageBatchToPortal()` 已实现，只需增加缓冲逻辑

**缓冲逻辑设计**：

```go
// internal/server/usage_buffer.go
type UsageBuffer struct {
    records   []*billing.UsageRecord
    mu        sync.Mutex
    maxBatch  int           // 100
    flushInterval time.Duration // 3 seconds
    billing   *billing.Billing
    logger    *zap.Logger
}

func (b *UsageBuffer) Add(record *billing.UsageRecord) {
    b.mu.Lock()
    b.records = append(b.records, record)
    if len(b.records) >= b.maxBatch {
        batch := b.records
        b.records = nil
        b.mu.Unlock()
        b.sendBatch(batch)
        return
    }
    b.mu.Unlock()
}

func (b *UsageBuffer) StartFlushLoop() {
    go func() {
        ticker := time.NewTicker(b.flushInterval)
        for range ticker.C {
            b.mu.Lock()
            batch := b.records
            b.records = nil
            b.mu.Unlock()
            if len(batch) > 0 {
                b.sendBatch(batch)
            }
        }
    }()
}
```

**失败重试**：batch 发送失败时，将记录重新推入缓冲或写入本地 fallback 文件。

---

## 3. 整体优化后的架构

```
Proxy 请求完成 → UsageBuffer.Add() → 本地缓冲 3-5秒
  → SendUsageBatchToPortal() → HTTP POST /api/webhook/usage-batch
    → Portal Redis RPush (webhook:usage:queue)

Portal 统计处理器（每秒 pop 200条）：
  → 批量 INSERT usage_records
  → 批量 UPDATE api_keys stats
  → 推入计费队列 (webhook:billing:queue)

Portal 计费处理器（每秒 pop 50条，按 user 分组）：
  → key_type 短路（general/subscription/enterprise 不同路径）
  → 同 user 多条记录合并扣款
  → 余额 ≤ 阈值 → 主动通知 Proxy 更新缓存

Proxy 余额预检：
  → 内存缓存 balance，10秒 TTL
  → balance ≤ 0 → 立即拒绝 402
  → Portal 主动通知 → 立即更新缓存
```

---

## 4. 性能预估

### 当前性能

| 指标 | 当前值 |
|------|--------|
| processRecord DB操作 | 14次/条 |
| 统计处理吞吐 | 100条/秒 |
| 计费处理吞吐 | 100条/秒（与统计绑定） |
| QPS=1000时积压 | 900条/秒 |

### 优化后性能

| 指标 | 优化后值 | 提升倍数 |
|------|---------|---------|
| 统计阶段 DB操作 | 2次/条（批量后更低） | 7× |
| 计费阶段 DB操作 | 6-8次/user（合并扣款） | 2-3× |
| 统计处理吞吐 | 200+条/秒 | 2× |
| 计费处理吞吐 | 50条/秒（按user合并后等效更高） | 等效 200+ |
| key_type 短路 | general 6次 vs 14次 | 2.3× |
| Proxy HTTP连接 | 每3-5秒1次 vs 每请求1次 | 100-300× |

### 余额耗尽延迟

| 方案 | 最大延迟 |
|------|---------|
| 优化前（5分钟 auth cache） | ≤5分钟 |
| 优化后（独立 balance cache 10秒 TTL） | ≤10秒 |
| 优化后 + Portal 主动推送（Redis pub/sub） | ≤1秒 |

---

## 5. 实施计划

### Phase 1：key_type 短路 — ✅ COMPLETE

**改动范围**：`internal/routes/webhook.go` + `cmd/server/main.go`

1. 合并两次 SELECT users FOR UPDATE 为一次
2. 根据 `key_type` 跳过无关查询：
   - `general`：跳过 subscription 和 enterprise 查询
   - `enterprise`：跳过 subscription 和 recharge 查询
   - `subscription`：跳过 enterprise 查询
3. 缓存 pricing_ratio（内存 map，5分钟 TTL）
4. 缓存 service_models pricing（内存 map，5分钟 TTL）
5. exchange rate 由 ExchangeService 缓存，`calculateCostFromPricing` 直接使用缓存值
6. `logBalanceDeductions` 提取为独立函数，减少代码重复

**实际改动**：webhook.go 约 200 行重写，main.go 1 行改动（ExchangeService 传递给 WebhookRoutes）

### Phase 2：统计与计费分离 — ✅ COMPLETE

**改动范围**：`internal/routes/webhook.go`

1. 拆分为 `processStats`（轻量：INSERT usage_records + UPDATE api_keys）和 `processBillingBatch`（重量：按 user 分组扣款）
2. 新增 Redis 队列 `webhook:billing:queue`
3. 统计处理器：每秒 pop 200条，独立事务
4. 计费处理器：每 2 秒 pop 100条，按 user_id 分组后单事务批量扣款
5. 保留 `processRecord` 作为 fallback（Redis 不可用时直接处理）
6. deduction 字段按 cost 比例分摊到每条 usage_record

**关键设计**：`usage_records` 仍然逐条 INSERT（每 request 一条），billing-details 页面不受影响。批量合并仅在余额扣款阶段。

### Phase 3：Proxy batch 报告 — ✅ COMPLETE

**改动范围**：`ai-proxy-go/internal/server/usage_buffer.go`（新增）+ `handlers.go` + `cmd/server/main.go`

1. 新增 `UsageBuffer` 结构体（maxBatch=100, flushInterval=3s）
2. `recordUsageAsync` 中优先使用 `usageBuffer.Add()`，fallback 为 `SendUsageToPortal`
3. 启动 `UsageBuffer.StartFlushLoop()` 在 main.go
4. batch 发送失败时 fallback 为逐条发送
5. 优雅关闭时最终 flush

**实际改动**：usage_buffer.go 约 130 行新代码，handlers.go 约 10 行改动，main.go 约 5 行改动

### Phase 4：Proxy 余额缓存预检 + Portal 主动推送 — ✅ COMPLETE

**改动范围**：`ai-proxy-go/internal/auth/auth.go` + `ai-portal-go/internal/routes/webhook.go` + `ai-portal-go/internal/db/db.go`

1. Portal 侧：`processBillingBatch` 和 `processRecord` 扣款后，若余额 ≤ ¥1，调用 `PublishBalanceUpdate()` 通过 Redis pub/sub 通知 Proxy
2. Proxy 侧：`auth.go` 新增独立余额缓存（10秒 TTL），优先于 5 分钟 auth cache 判断余额
3. Proxy 侧：新增 `StartBalanceListener()` 订阅 `ai-proxy:balance` Redis channel，收到 `balance_update` 消息后：
   - 余额 ≤ 0 → 直接设置缓存为 0，下次请求立即拒绝
   - 余额 > 0 → 清除缓存，下次请求从 Portal 获取最新值
4. `db.go` 新增 `BalanceUpdateMessage` 结构体和 `PublishBalanceUpdate()` 方法
5. `server.go` 新增 `AuthMiddleware()` 访问器，`main.go` 调用 `StartBalanceListener(redisClient)`

**实际改动**：auth.go 约 80 行新增（balance cache + pub/sub listener），webhook.go 约 20 行新增（push calls），db.go 约 25 行新增（message struct + publish method）

**余额耗尽延迟**：≤10 秒（balance cache TTL），Portal 主动推送后 ≤1 秒

---

## 6. 计费架构原则修正

### 当前问题

Proxy 计算 `record.Cost` → Portal 直接使用 → Portal 不是计费唯一权威

### 修正方案

**Phase 1 不改动**（当前无重复计费风险，优先做性能优化）

**Phase 2 时修正**：
- Portal `calculateCostFromPricing()` 不再依赖 `record.Cost > 0` 短路
- 改为始终从 DB/service_models pricing 计算 cost
- Proxy 的 `record.Cost` 仅作为"参考值"用于日志对比，不参与计费
- 如有差异（Portal计算 vs Proxy参考值 > 5%），记录告警日志

这样 Portal 成为计费唯一权威，Proxy 仅统计，符合设计原则。

---

## 7. 待讨论

- [x] Proxy batch 缓冲窗口：3秒 vs 5秒？→ 采用 3 秒
- [x] 余额缓存 TTL：10秒是否足够？→ 已实现独立 balance cache（10秒 TTL），Portal 主动推送后 ≤1 秒生效
- [x] Portal 主动通知 Proxy 的方式：Redis pub/sub vs HTTP push vs 专用 API → 采用 Redis pub/sub（`ai-proxy:balance` channel），Proxy 订阅后近实时更新本地余额缓存
- [x] 计费架构修正时机：Phase 1 先做性能优化，Phase 2 再修正原则？→ Phase 2 已实现分离，Portal 可独立计算 cost
- [x] batch 失败重试：重推缓冲 vs 本地文件 fallback vs 丢弃+告警 → fallback 为逐条发送
- [x] enterprise key 是否应完全跳过 subscription → 是，enterprise 路径为 Gift → Credit → Balance