# Downstream Usage & Audit Separation Design

> **ACTIVE** - Implementation plan for decoupling downstream usage statistics from audit logging

## Background

Current architecture has all downstream usage flowing through a single pipe:

```
handleRequest()
  └→ recordUsageAsync()
       └→ AuditLogger.Log(entry)    ← 唯一路径
            └→ batch → POST /api/webhook/usage-batch
                 └→ handleUsageBatch → Redis queue → processStats/processRecord
                                                  → INSERT usage_records
```

**Problems:**
1. **耦合** — 用量统计依赖 audit logger 的 batch flush 机制，审计日志失败回放可能导致重复
2. **`UsageBuffer` 是死代码** — 已初始化、flush loop 在跑，但 handler 从未调用 `usageBuffer.Add()`
3. **单一故障点** — Audit logger 的 flush 失败（audit.go:231 回放逻辑）会阻塞整个 pipeline

## Design

### Target Architecture

```
handleRequest()
  ├→ UsageBuffer.Add(record)         ← 统计主路径：可靠、独立
  │    └→ batch → POST /api/webhook/usage-stats-batch (NEW)
  │         └→ handleUsageStatsBatch → processStats
  │                                   → INSERT usage_records + UPDATE api_keys
  │                                   → push billing queue → async billing deductions
  │
  └→ AuditLogger.Log(entry)          ← 审计旁路：可丢失、可延迟
       └→ batch → POST /api/webhook/usage-batch
            └→ handleUsageBatch → (modified) write to audit_logs table only
```

### Data Flow

#### 统计管道 (NEW - primary)
```
Proxy                           Portal
  recordUsageAsync()            POST /api/webhook/usage-stats-batch
    └→ build UsageRecord               └→ handleUsageStatsBatch
    └→ usageBuffer.Add(record)               └→ push to Redis "webhook:usage-stats:queue"
         └→ batch (3s / 100)                     └→ processStatsQueue
              └→ SendUsageBatchToPortal               └→ processStats (insert usage_records)
                   └→ POST .../usage-stats-batch           └→ push to "webhook:billing:queue"
```

#### 审计管道 (modified - secondary)
```
Proxy                           Portal
  recordUsageAsync()            POST /api/webhook/usage-batch
    └→ AuditLogger.Log(entry)          └→ handleUsageBatch → (modified) write to audit_logs table
         └→ batch (configurable)              └→ 纯审计，不参与计费
              └→ POST .../usage-batch
```

## Changes

### Proxy Side (`ai-proxy-go`)

#### 1. `handlers.go` — Modify `recordUsageAsync()`

Add construction of `billing.UsageRecord` and call to `usageBuffer.Add()`.

```go
func recordUsageAsync(ctx UsageRecordContext, usage *dto.Usage, latencyMs int, errMsg string) {
    if usage == nil {
        return
    }
    totalTokens := int64(usage.PromptTokens + usage.CompletionTokens)
    if totalTokens <= 0 {
        return
    }

    // [NEW] Send to stats pipeline (primary)
    if usageBuffer != nil {
        effectiveInput := usage.PromptTokens - usage.PromptTokensDetails.CachedTokens
        if effectiveInput < 0 {
            effectiveInput = 0
        }
        usageBuffer.Add(&billing.UsageRecord{
            TraceID:              ctx.TraceID,
            APIKeyHash:           ctx.KeyHash,
            Model:                ctx.Model,
            Provider:             ctx.Provider,
            Region:               ctx.Region,
            InputTokens:          usage.PromptTokens,
            OutputTokens:         usage.CompletionTokens,
            CachedTokens:         ctx.CacheHitTokens,
            EffectiveInputTokens: effectiveInput,
            ReasoningTokens:      ctx.ReasoningTokens,
            Cost:                 ctx.Cost,
            LatencyMs:            int(latencyMs),
            RequestCount:         1,
            ServerID:             deps.ServerID,  // need to expose from deps
            UpstreamKeyID:        ctx.UpstreamKeyID,
            Timestamp:            time.Now().UTC().Format(time.RFC3339),
            CacheHit:             ctx.CacheHitTokens > 0,
            Error:                errMsg,
            GroupID:              ctx.GroupID,
            ActualGroupID:        ctx.ActualGroupID,
            IsFallback:           ctx.IsFallback,
            PricingRatio:         ctx.PricingRatio,
            RoutingTrack:         ctx.RoutingTrack,
            OriginalTrack:        ctx.OriginalTrack,
        })
    }

    // [KEEP] Audit logging (secondary, can be lossy)
    go func() {
        if deps == nil || deps.AuditLogger == nil {
            return
        }
        cacheHit := ctx.CacheHitTokens > 0
        deps.AuditLogger.Log(audit.AuditEntry{
            Timestamp:      time.Now(),
            APIKeyHash:     ctx.KeyHash,
            UpstreamKeyID:  ctx.UpstreamKeyID,
            Model:          ctx.Model,
            // ... existing fields unchanged
        })
    }()
}
```

**Key decision**: `UsageBuffer` 的 `Add()` 在 goroutine **外部**同步调用（非阻塞方法，内部使用 channel 或 mutex），而 audit logger 的 `Log()` 保持在 goroutine 内。这样统计路径更可靠（不会因为 goroutine 调度延迟而丢失），审计路径保持异步。

#### 2. `handlers.go` — Ensure `ServerID` is available

Add `ServerID` field to `HandlerDependencies` or use a package-level variable:

```go
var serverID string

func SetServerID(id string) {
    serverID = id
}
```

#### 3. `handlers.go` — Remove audit-only `AuditLogger.Log()` calls that bypass `recordUsageAsync`

There are many direct `deps.AuditLogger.Log()` calls (lines 975, 1762, 1852, etc.) that bypass `recordUsageAsync`. These should also go through `UsageBuffer` if they represent billable usage, or stay as audit-only entries.

**Decision**: Keep direct `AuditLogger.Log()` calls as-is (they go to audit endpoint only). The `recordUsageAsync` path is what feeds the new stats pipeline.

### Portal Side (`ai-portal-go`)

#### 1. `webhook_common.go` — Add new route

```go
func (r *WebhookRoutes) Register(rg *gin.RouterGroup) {
    rg.POST("/usage", r.requireWebhookSignature(), r.handleUsage)
    rg.POST("/usage-batch", r.requireWebhookSignature(), r.handleUsageBatch)
    rg.POST("/usage-stats-batch", r.requireWebhookSignature(), r.handleUsageStatsBatch) // NEW
    // ... existing routes
}
```

#### 2. `webhook_usage.go` — Add `handleUsageStatsBatch`

```go
func (r *WebhookRoutes) handleUsageStatsBatch(c *gin.Context) {
    var batch BatchUsageReport
    if err := c.ShouldBindJSON(&batch); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
        return
    }

    // Queue in Redis for async processing
    queued := false
    if r.db.Redis != nil {
        for _, record := range batch.Records {
            data, _ := json.Marshal(record)
            if err := r.db.Redis.RPush(c.Request.Context(), "webhook:usage-stats:queue", data).Err(); err == nil {
                queued = true
            }
        }
    }

    if queued {
        c.JSON(http.StatusOK, gin.H{"ok": true, "queued": true, "count": len(batch.Records)})
        return
    }

    // Fallback: process directly
    for i := range batch.Records {
        _ = r.processStats(c.Request.Context(), &batch.Records[i])
    }

    c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(batch.Records)})
}
```

#### 3. `webhook_common.go` — Modify `handleUsageBatch` (audit path)

The existing `/api/webhook/usage-batch` handler should be modified to NOT push to the same Redis queue, since it's now audit-only. It can either:
- **Option A**: Write to an `audit_logs` table (if needed for debugging)
- **Option B**: Simply acknowledge and discard (true fire-and-forget)
- **Option C**: Keep as-is but separate the Redis queue (push to `webhook:audit:queue`)

**Recommendation**: Option B (discard on portal side) for simplicity. The audit data stays in proxy's local logs. If audit persistence is needed later, add an `audit_logs` table.

```go
func (r *WebhookRoutes) handleUsageBatch(c *gin.Context) {
    // Audit-only path — acknowledge and discard
    var batch BatchUsageReport
    if err := c.ShouldBindJSON(&batch); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
        return
    }
    // Audit data is for debugging only; not processed for billing
    c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(batch.Records), "audit": true})
}
```

#### 4. `webhook_common.go` — Start new stats queue processor

```go
func (r *WebhookRoutes) StartQueueProcessor() {
    // Existing billing processor (for billing queue)
    go r.processBillingQueue()

    // NEW: Stats-only processor
    go r.processUsageStatsQueue()

    r.logger.Info("Queue processors started")
}

func (r *WebhookRoutes) processUsageStatsQueue() {
    ctx := context.Background()
    for {
        data, err := r.db.Redis.BLPop(ctx, 1*time.Second, "webhook:usage-stats:queue").Result()
        if err != nil {
            time.Sleep(100 * time.Millisecond)
            continue
        }
        var record UsageRecord
        if err := json.Unmarshal([]byte(data[1]), &record); err != nil {
            r.logger.Warn("Failed to unmarshal stats record", zap.Error(err))
            continue
        }
        if err := r.processStats(ctx, &record); err != nil {
            r.logger.Error("Failed to process stats", zap.Error(err), zap.String("traceId", record.TraceID))
            continue
        }
        // Push to billing queue for async deduction
        if r.db.Redis != nil {
            r.db.Redis.RPush(ctx, "webhook:billing:queue", data[1])
        }
    }
}
```

### Summary of Changed Files

| File | Change Type | Description |
|------|-------------|-------------|
| **PROXY** | | |
| `internal/server/handlers.go` | Modify | Add `usageBuffer.Add()` in `recordUsageAsync()`; add `ServerID` |
| **PORTAL** | | |
| `internal/routes/webhook_common.go` | Modify | Add `/usage-stats-batch` route; add `StartQueueProcessor` changes |
| `internal/routes/webhook_usage.go` | New + Modify | Add `handleUsageStatsBatch`; modify `handleUsageBatch` to audit-only |

### Total Changes
- **Proxy**: ~30 lines (handlers.go)
- **Portal**: ~60 lines (webhook_common.go + webhook_usage.go)
- **Total**: ~90 lines net new/modified

## Data Format Compatibility

The `UsageBuffer` uses `billing.UsageRecord`, which maps to portal's `UsageRecord` struct:

| billing.UsageRecord | portal.UsageRecord | Match? |
|---|---|---|
| TraceID | TraceID | ✅ |
| APIKeyHash | APIKeyHash | ✅ |
| Model | Model | ✅ |
| Provider | Provider | ✅ |
| Region | Region | ✅ |
| InputTokens | InputTokens | ✅ |
| OutputTokens | OutputTokens | ✅ |
| CachedTokens | CachedTokens | ✅ |
| EffectiveInputTokens | EffectiveInputTokens | ✅ |
| ReasoningTokens | ReasoningTokens | ✅ |
| Cost | Cost | ✅ |
| LatencyMs | LatencyMs | ✅ |
| RequestCount | RequestCount | ✅ |
| ServerID | ServerID | ✅ |
| UpstreamKeyID | UpstreamKeyID | ✅ |
| Timestamp | Timestamp | ✅ |
| CacheHit | CacheHit | ✅ |
| Error | Error | ✅ |
| GroupID | GroupID | ✅ |
| ActualGroupID | ActualGroupID | ✅ |
| IsFallback | IsFallback | ✅ |
| PricingRatio | PricingRatio | ✅ |
| RoutingTrack | RoutingTrack | ✅ |
| OriginalTrack | OriginalTrack | ✅ |

**NOTE**: `billing.UsageRecord` lacks `CacheCreationTokens` and `RetryCount`. These are non-essential — portal's `processStats` already handles defaults for missing fields (`effectiveInput` fallback, `routingTrack` default).

## Migration Strategy

**No downtime required.** The changes are additive:

1. **Deploy portal first** — Add new endpoint, start new queue processor. Old audit-batch path still works.
2. **Deploy proxy second** — Enable `usageBuffer.Add()` calls. Both old and new paths send data briefly.
3. **Verify** — Check `usage_records` count increase. There will be a brief period of double-counting (audit + stats). The `ON CONFLICT (trace_id, created_at) DO NOTHING` on portal side handles this as long as both paths use the same `trace_id`.
4. **Cleanup** — Optionally disable audit logger's portal webhook (local logs only).

## Verification

1. **Unit tests**: 
   - Proxy: `recordUsageAsync` produces correct `UsageRecord`
   - Portal: `handleUsageStatsBatch` correctly processes batch
2. **Integration**:
   - Proxy sends stats batch → portal inserts `usage_records`
   - Billing deductions proceed from stats pipeline
3. **No regression**:
   - Existing audit logger path still works (but data is discarded)
   - Upstream usage path unchanged
