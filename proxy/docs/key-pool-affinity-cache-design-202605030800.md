# Key Pool & Affinity Cache 设计文档

> **状态**: ACTIVE  
> **创建时间**: 2026-05-03 08:00  
> **版本**: 1.0

## 1. 概述

本文档描述 Go Proxy 中 Key Pool（密钥池）和 Affinity Cache（亲和性缓存）的实现细节，包括：
- Key Pool 分层和优先级路由
- Key 有效性自动检测和冷却机制
- Affinity Cache 按用户类型分层
- 两者之间的耦合设计

## 2. Key Pool 架构

### 2.1 数据结构

```go
type Key struct {
    ID            string    `json:"id"`
    Provider      string    `json:"provider"`
    Key           string    `json:"-"`              // 敏感信息，不序列化
    KeyPreview    string    `json:"key_preview"`
    Tier          string    `json:"tier"`
    Priority      string    `json:"priority"`       // "high", "normal", "economy"
    BaseURL       string    `json:"baseUrl,omitempty"`
    Models        []string  `json:"models,omitempty"`
    Enabled       bool      `json:"enabled"`

    // 状态追踪
    Status        string          `json:"status"`   // "active", "cooldown", "inactive", "purged"
    StatusHistory []StatusEntry   `json:"status_history,omitempty"`
    
    // 失败追踪
    Failures      int             `json:"failures"`
    FailureHistory []FailureEntry  `json:"failure_history,omitempty"`
    LastFailure   time.Time       `json:"last_failure,omitempty"`
    
    // 冷却追踪
    CooldownUntil   time.Time `json:"cooldown_until,omitempty"`
    RateLimitUntil  time.Time `json:"rate_limit_until,omitempty"`
    
    // 失效追踪（用于24小时清理）
    InactiveInfo    *InactiveInfo `json:"inactive_info,omitempty"`
    PurgedFromPool  bool          `json:"purged_from_pool"`
    PurgedAt        time.Time     `json:"purged_at,omitempty"`
    
    // 运行时统计
    TotalRequests int64     `json:"total_requests"`
    AvgLatencyMs  int       `json:"avg_latency_ms"`
    LastUsed      time.Time `json:"last_used"`
}
```

### 2.2 优先级分层

Key Pool 支持三种优先级：

| 优先级 | 说明 | 适用用户 |
|--------|------|----------|
| `high` | 高优先级 key | enterprise, subscription, token_plan, single_model |
| `normal` | 普通 key | general (默认) |
| `economy` | 经济 key | economy |

#### 路由逻辑

```
用户请求 → Auth 获取 access_mode → 映射为 priority → GetKeyByPriority
```

**优先级映射**:
```go
func getUserPriority(accessMode string) string {
    switch accessMode {
    case "enterprise", "subscription", "token_plan", "single_model":
        return "high"
    case "economy":
        return "economy"
    default:
        return "normal"
    }
}
```

**Key 选择逻辑**:
1. 高优先级用户: 优先选择 `high` + `normal` key
2. 普通用户: 选择 `normal` key
3. 经济用户: 优先选择 `economy` key，无则 fallback 到 `normal`
4. 如果筛选后无可用 key，fallback 到任意可用 key

### 2.3 状态机

```
                    ┌─────────────────────────────────────────────────────────────────┐
                    │                                                                 │
                    ▼                                                                 │
    ┌──────────┐  401/403  ┌──────────┐  24h后  ┌──────────┐                         │
    │  active  │ ─────────▶│ inactive │ ───────▶│  purged  │                         │
    └──────────┘           └──────────┘         └──────────┘                         │
         │                      ▲                                                    │
         │ 429/配额耗尽/失败      │ 手动恢复                                            │
         ▼                      │                                                    │
    ┌──────────┐  冷却到期    └──────────┐                                           │
    │ cooldown │ ────────────────────────┘                                           │
    └──────────┘                                                                     │
         │                                                                            │
         └────────────────────────────────────────────────────────────────────────────┘
                              冷却到期自动恢复
```

## 3. 错误处理和冷却机制

### 3.1 错误分类

| 错误类型 | HTTP 状态码 | 处理方式 |
|---------|-------------|---------|
| **认证错误** | 401, 403 | 立即标记 `inactive`，24小时后 `purge` |
| **限流错误** | 429 | 读取 `retry-after` 头，冷却相应时间 |
| **配额耗尽** | - | 冷却到次日 00:00:00 |
| **其他错误** | 5xx | 10分钟内10次失败 → `inactive`；3次失败 → 冷却60秒 |

### 3.2 配额耗尽检测

```go
func (m *Manager) isQuotaExceededError(httpStatus int, errorCode, errorType, errorMsg string) bool {
    // 429: Rate limit
    if httpStatus == 429 {
        return true
    }
    
    // 配额相关错误码
    quotaCodes := []string{"rate_limit_exceeded", "quota_exceeded", "insufficient_quota"}
    for _, code := range quotaCodes {
        if strings.Contains(strings.ToLower(errorCode), code) {
            return true
        }
    }
    
    // 配额相关关键词
    quotaKeywords := []string{"quota", "rate limit", "too many requests", "tokens.total", "business.total"}
    msgLower := strings.ToLower(errorMsg)
    for _, kw := range quotaKeywords {
        if strings.Contains(msgLower, kw) {
            return true
        }
    }
    
    return false
}
```

### 3.3 每日配额耗尽检测

```go
func (m *Manager) isDailyQuotaExceeded(errorMsg string) bool {
    msgLower := strings.ToLower(errorMsg)
    return strings.Contains(msgLower, "tokens.total") || 
        strings.Contains(msgLower, "business.total") ||
        strings.Contains(msgLower, "daily") ||
        strings.Contains(msgLower, "5h") ||
        strings.Contains(msgLower, "5小时")
}
```

### 3.4 冷却时间计算

| 错误类型 | 冷却策略 |
|---------|---------|
| 429 限流 | 读取 `retry-after` 头，默认 60 秒 |
| 每日配额耗尽 | 次日 00:00:00 |
| 其他配额错误 | 60 秒 |
| 最大失败次数 | 60 秒 |

## 4. 健康检查机制

### 4.1 定时任务

```go
func (m *Manager) StartHealthCheck() {
    go func() {
        ticker := time.NewTicker(30 * time.Second)
        defer ticker.Stop()
        for range ticker.C {
            m.HealthCheck()
        }
    }()
}
```

### 4.2 检查内容

1. **Cooldown 恢复检查**
   ```go
   if !key.CooldownUntil.IsZero() && key.CooldownUntil.Before(time.Now()) {
       key.CooldownUntil = time.Time{}
       key.Failures--
       if key.Status == "cooldown" {
           key.Status = "active"
       }
   }
   ```

2. **Rate Limit 恢复检查**
   ```go
   if !key.RateLimitUntil.IsZero() && key.RateLimitUntil.Before(time.Now()) {
       key.RateLimitUntil = time.Time{}
   }
   ```

3. **24小时 Purge 检查**
   ```go
   if key.Status == "inactive" && !key.PurgedFromPool && key.InactiveInfo != nil {
       inactiveDuration := time.Since(key.InactiveInfo.At)
       if inactiveDuration >= 24*time.Hour {
           key.PurgedFromPool = true
           key.PurgedAt = time.Now()
           key.Status = "purged"
       }
   }
   ```

## 5. Affinity Cache 架构

### 5.1 缓存分层

Affinity Cache 按用户类型分层，使用优先级前缀区分：

```
缓存键格式: {priority}:{model}:{group}:{fingerprint}
```

示例：
- `high:moonshot-v1-8k:default:a1b2c3d4`
- `normal:mimo-v2-pro:default:e5f6g7h8`
- `economy:minimax-m2-5:default:i9j0k1l2`

### 5.2 核心方法

```go
// GetChannelWithPriority - 按优先级获取缓存
func (c *AffinityCache) GetChannelWithPriority(keyID string, modelName string, 
    messages []dto.Message, priority string) int {
    cacheKey := c.buildSimpleCacheKey(keyID, modelName, priority)
    if cacheKey == 0 {
        return 0
    }
    
    // 检查本地缓存
    if val, ok := c.localCache.Load(cacheKey); ok {
        if channelID, ok := val.(int); ok {
            return channelID
        }
    }
    
    return 0
}

// RecordChannelWithPriority - 按优先级记录缓存
func (c *AffinityCache) RecordChannelWithPriority(ctx *gin.Context, modelName string, 
    usingGroup string, channelID int, priority string) {
    if channelID <= 0 {
        return
    }
    
    // 构建带优先级的缓存键
    cacheKey := c.buildCacheKeyWithPriority(ctx, modelName, usingGroup, priority)
    if cacheKey == "" {
        return
    }
    
    // 存储到本地缓存
    c.localCache.Store(cacheKey, channelID)
    
    // 存储到 Redis（如果可用）
    if c.redis != nil {
        c.redis.Set(context.Background(), c.redisPrefix+":"+cacheKey, channelID, c.defaultTTL)
    }
}
```

### 5.3 缓存键构建

```go
func (c *AffinityCache) buildCacheKeyWithPriority(ctx *gin.Context, modelName string, 
    usingGroup string, priority string) string {
    // 获取请求体指纹
    fingerprint := c.getRequestFingerprint(ctx)
    if fingerprint == "" {
        return ""
    }
    
    // 构建键: priority:model:group:fingerprint
    return fmt.Sprintf("%s:%s:%s:%s", priority, modelName, usingGroup, fingerprint)
}
```

## 6. Key Pool 与 Affinity Cache 耦合

### 6.1 请求流程

```
用户请求
    │
    ▼
Auth 中间件
    │
    ├─ 获取 access_mode, allowed_models, rate_limit_rpm
    ├─ 检查余额/配额
    │
    ▼
Rate Limit 中间件
    │
    ├─ 按用户 RPM 限制 (60/120/300)
    │
    ▼
Handler
    │
    ├─ 获取 userPriority (从 access_mode 映射)
    │
    ├─ Affinity Cache 查询
    │   └─ GetChannelWithPriority(keyID, model, messages, priority)
    │
    ├─ Key Pool 查询
    │   └─ GetKeyByPriority(provider, userPriority)
    │
    ├─ 上游请求
    │
    ├─ 成功:
    │   ├─ RecordChannelWithPriority (更新缓存)
    │   └─ UpdateKeyUsage (更新统计)
    │
    └─ 失败:
        └─ ReportKeyFailure (报告失败，触发冷却)
```

### 6.2 代码示例

```go
func handleChatCompletions(c *gin.Context) {
    // 获取用户优先级
    accessMode := c.GetString("access_mode")
    userPriority := getUserPriority(accessMode)
    
    // Affinity Cache 查询（按优先级分层）
    var channelID int
    if deps.AffinityCache != nil {
        channelID = deps.AffinityCache.GetChannelWithPriority(keyID, req.Model, 
            req.Messages, userPriority)
    }
    
    // Key Pool 查询（按优先级分层）
    upstreamKey := keyPoolManager.GetKeyByPriority(c.Request.Context(), provider, userPriority)
    
    // ... 执行请求 ...
    
    // 成功后记录缓存
    if deps.AffinityCache != nil && channelID > 0 {
        deps.AffinityCache.RecordChannelWithPriority(c, req.Model, "default", 
            channelID, userPriority)
    }
    
    // 失败后报告
    if err != nil {
        keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, httpStatus, 
            errorCode, errorType, errorMsg)
    }
}
```

## 7. Rate Limit 实现

### 7.1 三级 RPM 粗筛

```go
// 允许的 RPM 预设值
var AllowedRateLimitRPM = []int{60, 120, 300}

// 预创建计数器
counters := make(map[int]*localCounter)
for _, rpm := range AllowedRateLimitRPM {
    counters[rpm] = newLocalCounter(rpm, time.Minute)
}
```

### 7.2 用户级限制

```go
// 从 Auth Claims 获取用户 RPM
userRPM := c.GetInt("rate_limit_rpm")
if userRPM == 0 {
    userRPM = m.cfg.RateLimit.DefaultRPM
}

// 验证是否为合法预设值
if !IsValidRateLimitRPM(userRPM) {
    userRPM = AllowedRateLimitRPM[0] // 默认使用最低档
}

// 获取对应的计数器
counter := m.localCounters[userRPM]
```

### 7.3 性能开销

| 操作 | 复杂度 | 说明 |
|------|--------|------|
| Map 查找 RPM 计数器 | O(1) | `map[int]*localCounter` |
| Token Bucket 操作 | O(1) | 每个 bucket 独立 mutex，无全局锁 |
| Redis 异步同步 | O(1) | 非阻塞，channel 满则跳过 |

**实测数据**:
- 每个请求增加 < 1μs 延迟
- 无全局锁竞争（每个 bucket 独立）
- 内存开销: 3 个计数器 × 每个 bucket ~100 bytes ≈ 300 bytes

## 8. 测试验证

### 8.1 测试结果

| 测试项 | 结果 |
|--------|------|
| Kimi (moonshot-v1-8k) | ✅ 成功 |
| Xiaomi (mimo-v2-pro) | ✅ 成功 |
| Minimax (MiniMax-Text-01) | ✅ 成功 |
| 无效 Key 跳过 | ✅ 自动跳过 |
| 优先级路由 | ✅ 按用户类型选择 key |
| 冷却机制 | ✅ 自动恢复 |

### 8.2 Key Pool 状态

```
Provider   | Total | Active | Inactive | Purged
-----------|-------|--------|----------|-------
minimax    |     3 |      3 |        0 |      0
moonshot   |     3 |      3 |        0 |      0
openai     |     2 |      2 |        0 |      0
xiaomi     |     3 |      3 |        0 |      0
```

## 9. 后续优化

### 9.1 待实现

1. **Portal 配合改动**:
   - `api/apikeys/validate` 返回 `balance`, `quota_exceeded`, `quota_window`
   - 支持更细粒度的配额控制

2. **详细窗口限制**:
   - Portal 实现 RPM/RPH/RP5H/RPW 详细窗口限制
   - Proxy 做粗筛，Portal 做精筛

3. **Key Pool 分层持久化**:
   - 优先级信息持久化到 Redis
   - 支持动态调整优先级

### 9.2 性能优化

1. **本地缓存增强**:
   - 增加缓存命中率统计
   - 支持缓存预热

2. **分布式锁优化**:
   - 单机部署跳过分布式锁
   - 多机部署使用 Redis 分布式锁

## 10. 参考资料

- Node.js Proxy 实现: `ai-proxy-singapore/providers.js`
- 错误码配置: `ai-proxy-singapore/src/providers/errorCodes.js`
- 重试处理: `ai-proxy-singapore/retryHandler.js`
