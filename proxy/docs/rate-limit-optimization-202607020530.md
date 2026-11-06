# Rate Limit 优化方案

**日期：** 2026-07-02  
**状态：** 设计提案（2026-07-18 复核：Pre-consume 已落地，限流专项优化仍待实施）  
**影响范围：** ai-proxy-go 的 `internal/ratelimit/` 模块

---

## 一、当前实现分析

### 架构

```
Client Request
    ↓
[Auth Middleware] → 获取 key RPM / subscription limits / group limits
    ↓
[Rate Limit Middleware] → 检查 RPM/RPH/RP5H/RPW → 429 if exceeded
    ↓
[Handler] → 处理请求 → 返回响应
    ↓
[Post-request] → 记录 tokens → 检查 TPM/TPH/TPD/TPW (async)
```

### 限流维度

| 维度 | 窗口 | 算法 | 粒度 | 触发时机 |
|------|------|------|------|---------|
| RPM | 1 分钟 | Redis INCR + EXPIRE | per-key | pre-request |
| RPH | 1 小时 | 同上 | per-key | pre-request |
| RP5H | 5 小时 | 同上 | per-key | pre-request |
| RPW | 1 周 | 同上 | per-key | pre-request |
| RPD | 1 天 | 同上 | per-key | pre-request |
| TPM | 1 分钟 | Redis INCRBY + EXPIRE | per-key | **post-request** |
| TPH | 1 小时 | 同上 | per-key | post-request |
| TPD | 1 天 | 同上 | per-key | post-request |
| TPW | 1 周 | 同上 | per-key | post-request |

### 限制来源优先级

```
effective_limit = min(non-zero values of):
  1. API Key level (rate_limit_rpm 字段)
  2. Subscription level (sub_rpm_limit)
  3. Group level (group_rpm)
```

### 429 响应格式（OpenAI 兼容）

```json
HTTP/1.1 429 Too Many Requests
Retry-After: 35

{
  "error": {
    "message": "Rate limit exceeded (60 RPM). Please slow down your requests.",
    "type": "rate_limit_error",
    "code": "RATE_LIMIT_EXCEEDED"
  }
}
```

---

## 二、现有实现的问题

### 问题 1：固定窗口突发（Window Boundary Burst）

**现象：** 用户在窗口末尾和下一个窗口开头各发满额请求，实际 2 秒内发出 2x RPM。

```
Window 1 (0:00-1:00):  .......[60 requests at 0:59]
Window 2 (1:00-2:00):  [60 requests at 1:01].......

→ 2秒内 120 个请求通过（RPM=60 却允许了 2x 突发）
```

**影响：** 上游 provider 可能触发其自己的限流。

### 问题 2：TPM 仅 Post-request 检查

**现象：** 用户发送含 100K context 的请求。请求转发到上游，消耗了 token 配额后才发现超限。

```
TPM limit: 200K
Current usage: 150K
New request: 100K estimated input → actual 100K
→ 请求完成后才知道超了 (250K > 200K)
→ 上游已收费，下游 TPM 超标
```

**影响：** 无法提前拒绝明知会超限的大请求，浪费上游成本。

### 问题 3：无模型差异化

**现象：** gpt-5.4-nano（$0.15/M）和 claude-opus-4-6（$15/M）共享同一个 RPM 限制。

**影响：** 用户可能用满 RPM 发送高价模型请求，产生极高账单。

### 问题 4：无异常行为检测

**现象：** 客户端 bug 或恶意探测导致大量 4xx 请求，每个都消耗 proxy 资源（auth 查询、路由决策），但不触发任何保护。

**影响：** 资源浪费，可能影响其他用户的延迟。

---

## 三、优化方案

### P1：Pre-request TPM 预检（与 Pre-consume 结合）

**目标：** 在请求转发到上游前，用 estimated tokens 做 TPM 预检，提前拒绝明知会超限的大请求。

**实现位置：** `handlers_chat.go`，在 pre-consume 之后、upstream call 之前。

```go
// After pre-consume, before upstream call
if s.cfg.RateLimit.Enabled && effectiveTPM > 0 {
    estimatedTotal := inputTokens + estimatedOutputTokens(req.MaxTokens)
    currentTPM := s.rateLimitMiddleware.GetCurrentTPM(redisKey)
    
    if currentTPM + int64(estimatedTotal) > int64(effectiveTPM) {
        c.Header("Retry-After", s.rateLimitMiddleware.RetryAfter("tpm"))
        c.JSON(429, gin.H{"error": gin.H{
            "message": fmt.Sprintf("Token rate limit will be exceeded. Current: %d, Estimated: %d, Limit: %d TPM",
                currentTPM, estimatedTotal, effectiveTPM),
            "type": "rate_limit_error",
            "code": "TPM_LIMIT_EXCEEDED",
        }})
        // Unfreeze pre-consume if applicable
        return
    }
}
```

**注意事项：**
- `estimatedOutputTokens` = `min(req.MaxTokens, 4096)` 作为保守估算
- 如果估算超限但实际不超限，下一个请求会正常通过（不会永久阻塞）
- 只做 TPM 预检；TPH/TPD/TPW 仍然 post-request 检查（因为窗口更大，突发影响小）

**工时：** ~2h

---

### P2：滑动窗口 RPM

**目标：** 消除固定窗口边界突发，确保任意 60 秒内请求数不超过 RPM。

**算法：** Redis Sorted Set（时间戳为 score）

```go
// AllowRPM_SlidingWindow - Sliding window rate limit using Redis sorted set
func (m *Middleware) AllowRPM_SlidingWindow(redisKey string, rpmLimit int) bool {
    ctx := context.Background()
    now := time.Now()
    windowStart := now.Add(-time.Minute)
    key := redisKey + ":rpm:sw"
    
    pipe := m.redis.Pipeline()
    // Remove expired entries
    pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart.UnixNano()))
    // Count current window
    countCmd := pipe.ZCard(ctx, key)
    pipe.Exec(ctx)
    
    count := countCmd.Val()
    if count >= int64(rpmLimit) {
        return false
    }
    
    // Add current request
    m.redis.ZAdd(ctx, key, redis.Z{
        Score:  float64(now.UnixNano()),
        Member: fmt.Sprintf("%d:%d", now.UnixNano(), rand.Int63()),
    })
    m.redis.Expire(ctx, key, 2*time.Minute) // TTL > window for safety
    
    return true
}
```

**对比：**

| 方面 | 固定窗口（当前） | 滑动窗口（提案） |
|------|---------------|---------------|
| Redis 操作 | 1x INCR | 1x ZREMRANGEBYSCORE + ZCARD + ZADD |
| 内存占用 | O(1) per key | O(RPM) per key |
| 突发控制 | ❌ 边界可突发 2x | ✅ 任意 60s 严格限制 |
| 性能 | ~0.1ms | ~0.3ms |
| 复杂度 | 低 | 中 |

**建议：** 仅对 RPM 使用滑动窗口（最敏感）。RPH/RPW 等长窗口保持固定窗口（边界突发影响可忽略）。

**工时：** ~3h

---

### P3：异常行为检测与临时降级

**目标：** 检测异常请求模式（高错误率），临时降低 RPM 保护系统。

**不做惩罚性 lockout（不学 accessapi）。** 我们作为中间代理应保持公平，只是降速而非封禁。

**规则：**

| 条件 | 动作 | 恢复 |
|------|------|------|
| 最近 20 个请求中 4xx 率 > 50% | RPM 降为 max(RPM/4, 5) | 5 分钟后恢复 |
| 最近 10 个请求全部 4xx | RPM 降为 max(RPM/8, 2) | 10 分钟后恢复 |
| 任何请求成功 | 立即恢复正常 RPM | — |

**实现：**

```go
// ErrorRateTracker tracks recent error rates per key
type ErrorRateTracker struct {
    mu      sync.RWMutex
    windows map[string]*errorWindow // key_hash → window
}

type errorWindow struct {
    results  [20]bool // ring buffer: true=success, false=4xx
    idx      int
    degraded bool
    degradeUntil time.Time
}

func (t *ErrorRateTracker) RecordResult(keyHash string, success bool) {
    // ...
}

func (t *ErrorRateTracker) GetEffectiveRPM(keyHash string, baseRPM int) int {
    t.mu.RLock()
    w, ok := t.windows[keyHash]
    t.mu.RUnlock()
    if !ok || !w.degraded || time.Now().After(w.degradeUntil) {
        return baseRPM
    }
    return max(baseRPM/4, 5)
}
```

**集成点：** 在 rate limit middleware 中调用 `GetEffectiveRPM()` 替代直接使用 `effectiveRPM`。

**工时：** ~3h

---

### P4（可选）：模型权重 RPM

**目标：** 高价模型消耗更多"配额点数"，防止用户用满 RPM 全部调用 opus 级模型。

**方案：** 引入 RPM 权重（cost weight），每个模型的一次请求消耗不同的 RPM "点数"：

```
Model weights (configurable):
  nano/mini/flash-lite: 1
  gpt-4o/sonnet/flash: 2
  opus/pro/o3: 5
  o3-pro/opus-4-8: 10
```

```go
// Instead of: count++ and check count >= rpmLimit
// Do: count += modelWeight and check count >= rpmLimit * baseWeight(1)
```

**效果：** RPM=60 的用户可以每分钟发 60 个 mini 请求，或 12 个 opus 请求，或 6 个 o3-pro 请求。

**工时：** ~4h（含 Portal 端配置 UI）

---

## 四、Retry-After 优化

### 当前行为

```go
func retryAfter(counterType string) string {
    // 返回当前窗口的剩余时间
    // RPM: 60 - now.Second()  → 可能返回 55s
}
```

### 问题

返回大值（如 55s）会导致客户端等待很长时间。OpenAI 的做法是返回较小的值（1-5s），鼓励 exponential backoff。

### 建议

```go
func retryAfter(counterType string) string {
    switch counterType {
    case "rpm":
        return "1"  // 鼓励立即重试（with backoff）
    case "rph":
        return "60" // 1 分钟后重试
    case "rpd", "tpd":
        return "3600" // 1 小时后重试
    default:
        return "5"
    }
}
```

或者采用 OpenAI 的做法，在 body 中提供详细信息，header 中给短值：
```
Retry-After: 1
X-RateLimit-Limit-Requests: 60
X-RateLimit-Remaining-Requests: 0
X-RateLimit-Reset-Requests: 35s
```

---

## 五、与其他系统的配合

### Pre-consume + TPM 预检组合

```
Request arrives
    ↓
[RPM check] → 429 if exceeded
    ↓
[Pre-consume freeze] → 402 if balance insufficient
    ↓
[TPM pre-check] → 429 if tokens will exceed limit
    ↓
[Forward to upstream]
    ↓
[Post-request TPM record]
    ↓
[Settle] + [Usage billing queue]
```

### Model Health + Rate Limit

当 model health 禁用了一个模型：
- 请求被 proxy 直接 503（不到达上游）
- **不计入 RPM**（不消耗用户的速率配额）
- 这是合理的：用户不应因为 proxy 内部的模型禁用而浪费 RPM

---

## 六、实施优先级

| 优先级 | 方案 | 工时 | 收益 | 状态（2026-07-18 复核） |
|--------|------|------|------|------------------------|
| 🔴 P1 | Pre-request TPM 预检 | 2h | 防止大请求穿透，节省上游成本 | ⏳ 待实施（`handlers_chat.go` 当前仅在请求完成后记录 TPM，无预检逻辑） |
| 🟡 P2 | 滑动窗口 RPM | 3h | 消除边界突发，更公平 | ⏳ 待实施 |
| 🟡 P3 | 异常行为检测 | 3h | 保护系统免受异常流量影响 | ⏳ 待实施 |
| 🟢 P4 | 模型权重 RPM | 4h | 防止高价模型 abuse | ⏳ 待实施 |
| 🟢 P5 | Retry-After 优化 | 1h | 更好的客户端体验 | ⏳ 待实施 |

**前置条件说明**：
- 与 P1 配合的 **Pre-consume（余额预扣）** 已在 `pkg/billing/preconsume.go` 落地，但 TPM 预检逻辑尚未接入。
- 与 P3 相关的 **Model Health** 已在 `pkg/keypool/model_health.go` 落地，但异常行为导致的 RPM 临时降级尚未实现。

**建议分两期交付：**
- 第一期：P1 + P5（3h，快速见效）
- 第二期：P2 + P3（6h，系统加固）
- P4 视业务需求决定

---

## 七、对比：我们 vs 上游 Providers

| 行为 | OpenAI | Anthropic | accessapi | 我们（当前） | 我们（优化后） |
|------|--------|-----------|-----------|------------|-------------|
| 基础限流 | RPM + TPM | RPM + TPM | RPM | RPM + TPM | 滑动窗口 RPM + pre-check TPM |
| 窗口 | 固定 | 固定 | 不明 | 固定 | 滑动（RPM）+ 固定（其他） |
| 惩罚机制 | 无 | 无 | 120s ban | 无 | 无（临时降速，非 ban） |
| 429 后恢复 | 立即（backoff） | 立即 | 强制 120s | 窗口结束 | 1s（鼓励 backoff） |
| 余额检查 | pre-request | pre-request | ? | **pre-request (new!)** | pre-request |
| Token 预检 | 无 | 无 | 无 | 无 | **pre-request 估算 (new)** |

---

*文档作者：Kiro*  
*审核：待定*
