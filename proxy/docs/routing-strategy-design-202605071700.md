# Routing Strategy & Failover Mechanism Design

> **⚠️ OUTDATED** — 此文档已被实际实现取代。当前路由采用 `resolveProviderAndKey` + `GetRoutingOrder` 方案（见 [cross-provider-fallback-routing-202605161900.md](cross-provider-fallback-routing-202605161900.md)）。`pkg/routing/` 包的策略接口虽已实现，但 handler 层未使用，实际路由逻辑在 `internal/server/handlers.go` 的 `resolveProviderAndKey` 中。

**Date:** 2026-05-07
**Status:** OUTDATED
**Superseded by:** cross-provider-fallback-routing-202605161900.md
**Scope:** `ai-proxy-go`
**Depends on:** Phase 3 implementation plan

---

## 1. Current State Analysis

### 1.1 Existing Key Selection Flow

```
Request arrives → Parse model name
    ↓
Lookup service_models → Get available providers
    ↓
Select provider by priority (group_priority)
    ↓
GetKeyByPriority() → Select upstream key (LRU within tier)
    ↓
Forward request to provider
```

### 1.2 Current Limitations

| Issue | Impact |
|-------|--------|
| No price optimization | Always uses highest priority provider, may not be cost-effective |
| No latency optimization | Cannot route to fastest provider |
| No automatic failover | Single provider failure causes request failure |
| Static priority only | Cannot adapt to real-time conditions |

---

## 2. Proposed Architecture

### 2.1 Routing Strategy Interface

```go
// RoutingStrategy defines how to select among multiple providers
type RoutingStrategy interface {
    // Name returns the strategy identifier
    Name() string
    
    // Select chooses the best provider from candidates
    // Returns provider ID and the reason for selection
    Select(ctx context.Context, candidates []ProviderCandidate) (selected *ProviderCandidate, reason string, err error)
}

// ProviderCandidate represents a provider available for routing
type ProviderCandidate struct {
    ID           string
    Name         string
    Priority     int      // Static priority from config
    InputPrice   float64  // Price per 1M input tokens
    OutputPrice  float64  // Price per 1M output tokens
    AvgLatencyMs int      // Average response latency
    SuccessRate  float64  // Recent success rate (0-1)
    LastError    string   // Last error message
    LastErrorAt  time.Time
}
```

### 2.2 Strategy Implementations

#### Priority Strategy (Default)
- Uses static priority from configuration
- Maintains current behavior
- No additional data needed

```go
type PriorityStrategy struct{}

func (s *PriorityStrategy) Select(ctx context.Context, candidates []ProviderCandidate) (*ProviderCandidate, string, error) {
    // Sort by priority descending, return highest
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].Priority > candidates[j].Priority
    })
    return &candidates[0], fmt.Sprintf("priority=%d", candidates[0].Priority), nil
}
```

#### Lowest Price Strategy
- Selects provider with lowest total price for the model
- Considers both input and output pricing

```go
type LowestPriceStrategy struct{}

func (s *LowestPriceStrategy) Select(ctx context.Context, candidates []ProviderCandidate) (*ProviderCandidate, string, error) {
    sort.Slice(candidates, func(i, j int) bool {
        priceI := candidates[i].InputPrice + candidates[i].OutputPrice
        priceJ := candidates[j].InputPrice + candidates[j].OutputPrice
        return priceI < priceJ
    })
    return &candidates[0], fmt.Sprintf("price=%.2f", candidates[0].InputPrice + candidates[0].OutputPrice), nil
}
```

#### Lowest Latency Strategy
- Selects provider with lowest average latency
- Uses channel_health data from Portal sync

```go
type LowestLatencyStrategy struct{}

func (s *LowestLatencyStrategy) Select(ctx context.Context, candidates []ProviderCandidate) (*ProviderCandidate, string, error) {
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].AvgLatencyMs < candidates[j].AvgLatencyMs
    })
    return &candidates[0], fmt.Sprintf("latency=%dms", candidates[0].AvgLatencyMs), nil
}
```

#### Round Robin Strategy
- Distributes requests evenly across providers
- Uses Redis counter for distributed coordination

```go
type RoundRobinStrategy struct {
    redis *redis.Client
}

func (s *RoundRobinStrategy) Select(ctx context.Context, candidates []ProviderCandidate) (*ProviderCandidate, string, error) {
    if len(candidates) == 0 {
        return nil, "", errors.New("no candidates")
    }
    
    // Get and increment counter
    key := "routing:round_robin:" + candidates[0].ID[:8] // Model prefix
    idx, _ := s.redis.Incr(ctx, key).Result()
    
    selected := candidates[int(idx)%len(candidates)]
    return &selected, fmt.Sprintf("index=%d", int(idx)%len(candidates)), nil
}
```

---

## 3. Failover Mechanism

### 3.1 Failover Flow

```
Request arrives → Get routing strategy from config
    ↓
Get provider candidates for model
    ↓
Apply strategy → Select primary provider
    ↓
Try request → Success? Return response
    ↓ (Failure)
    Mark provider as failed (temporarily)
    ↓
    Select next provider from candidates
    ↓
    Retry request (up to max_retries)
    ↓
    All failed? Return last error
```

### 3.2 Failover Configuration

```yaml
routing:
  strategy: priority          # priority | lowest_price | lowest_latency | round_robin
  failover_enabled: true      # Enable automatic failover
  max_retries: 3              # Maximum retry attempts
  retry_delay_ms: 100         # Delay between retries (exponential backoff)
  timeout_ms: 30000           # Total request timeout
  
  # Per-strategy tuning
  latency:
    max_acceptable_ms: 5000   # Skip providers with latency > this
  price:
    max_variance_percent: 20  # Don't use if price > 20% above lowest
```

### 3.3 Failover State Tracking

```go
// FailoverState tracks temporary provider failures
type FailoverState struct {
    ProviderID    string
    Model         string
    LastFailure   time.Time
    FailureCount  int
    ErrorType     string    // "rate_limit", "timeout", "error_5xx", "error_4xx"
    RetryAfter    time.Time // When to retry this provider
}

// FailoverManager manages provider failure states
type FailoverManager struct {
    states sync.Map // provider_id -> []FailoverState
    ttl    time.Duration
}

// ShouldSkip returns true if provider should be skipped
func (m *FailoverManager) ShouldSkip(providerID, model string) bool {
    key := providerID + ":" + model
    if v, ok := m.states.Load(key); ok {
        state := v.(FailoverState)
        return time.Now().Before(state.RetryAfter)
    }
    return false
}

// RecordFailure records a provider failure
func (m *FailoverManager) RecordFailure(providerID, model, errorType string) {
    key := providerID + ":" + model
    
    backoff := time.Second
    switch errorType {
    case "rate_limit":
        backoff = 30 * time.Second
    case "timeout":
        backoff = 5 * time.Second
    case "error_5xx":
        backoff = 10 * time.Second
    }
    
    state := FailoverState{
        ProviderID:   providerID,
        Model:        model,
        LastFailure:  time.Now(),
        FailureCount: 1,
        ErrorType:    errorType,
        RetryAfter:   time.Now().Add(backoff),
    }
    
    m.states.Store(key, state)
}
```

---

## 4. Implementation Plan

### 4.1 File Structure

```
pkg/routing/
├── strategy.go       # Strategy interface and factory
├── priority.go       # Priority strategy implementation
├── price.go          # Lowest price strategy
├── latency.go        # Lowest latency strategy
├── round_robin.go    # Round robin strategy
├── failover.go       # Failover manager
└── config.go         # Routing configuration
```

### 4.2 Integration Points

| Component | Changes |
|-----------|---------|
| `pkg/keypool/manager.go` | Add `GetKeyByStrategy()` method |
| `internal/server/handlers.go` | Use routing strategy in request flow |
| `internal/config/config.go` | Add routing configuration |
| Portal sync | Include price/latency data in sync |

### 4.3 Implementation Steps

| Step | Task | Effort |
|------|------|--------|
| 1 | Create routing strategy interface and factory | 1h |
| 2 | Implement priority strategy (refactor existing) | 0.5h |
| 3 | Implement lowest price strategy | 1h |
| 4 | Implement lowest latency strategy | 1h |
| 5 | Implement round robin strategy | 1h |
| 6 | Implement failover manager | 2h |
| 7 | Integrate with keypool manager | 2h |
| 8 | Add configuration support | 1h |
| 9 | Update Portal sync for price/latency data | 1h |
| 10 | Testing and validation | 2h |

**Total: ~12.5h**

---

## 5. Data Requirements

### 5.1 Provider Price Data

```sql
-- From service_models table (already exists)
SELECT provider_id, input_price, output_price
FROM service_models
WHERE model_id = ? AND status = 'active'
```

### 5.2 Provider Latency Data

```sql
-- From channel_health table (already exists)
SELECT provider_id, avg_latency_ms, success_rate
FROM channel_health
WHERE model_id = ? AND updated_at > NOW() - INTERVAL '1 hour'
ORDER BY updated_at DESC
```

### 5.3 Portal Sync Extension

```go
// Add to SyncProviderResponse
type SyncProviderResponse struct {
    // ... existing fields
    InputPrice  float64 `json:"input_price"`
    OutputPrice float64 `json:"output_price"`
}
```

---

## 6. Configuration API

### 6.1 Admin Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/admin/routing/config` | Get current routing configuration |
| PUT | `/api/admin/routing/config` | Update routing configuration |
| GET | `/api/admin/routing/stats` | Get routing statistics |

### 6.2 Configuration Storage

```sql
-- In system_settings table
INSERT INTO system_settings (key, value, category) VALUES
('routing_strategy', 'priority', 'proxy'),
('routing_failover_enabled', 'true', 'proxy'),
('routing_max_retries', '3', 'proxy'),
('routing_retry_delay_ms', '100', 'proxy');
```

---

## 7. Testing Strategy

### 7.1 Unit Tests

- Each strategy's Select() method
- Failover state management
- Configuration parsing

### 7.2 Integration Tests

- End-to-end routing with mock providers
- Failover with simulated failures
- Round-robin distribution verification

### 7.3 Performance Tests

- Latency impact of strategy selection
- Memory usage of failover state tracking

---

## 8. Rollout Plan

### Phase 1: Foundation (This PR)
- Strategy interface and factory
- Priority strategy (maintain current behavior)
- Configuration infrastructure

### Phase 2: New Strategies
- Lowest price strategy
- Lowest latency strategy
- Round robin strategy

### Phase 3: Failover
- Failover manager
- Retry logic
- Error classification

### Phase 4: Polish
- Admin UI for configuration
- Routing statistics dashboard
- Documentation

---

## 9. Implementation Summary (2026-05-07)

### 9.1 Completed Files

| File | Description |
|------|-------------|
| `pkg/routing/strategy.go` | Strategy interface, Candidate struct, Config, Factory |
| `pkg/routing/priority.go` | Priority strategy (default, maintains existing behavior) |
| `pkg/routing/price.go` | Lowest price strategy for cost optimization |
| `pkg/routing/latency.go` | Lowest latency strategy for performance |
| `pkg/routing/round_robin.go` | Round robin strategy for load distribution |
| `pkg/routing/failover.go` | Failover manager with exponential backoff |
| `pkg/routing/router.go` | Router combining strategy + failover |
| `pkg/keypool/manager.go` | Added `GetProvidersForModel()`, `GetProviderStats()` |

### 9.2 Strategy Implementations

**Priority Strategy** (Default)
- Selects providers by static priority (highest first)
- Tie-breaker: higher success rate
- Maintains backward compatibility

**Lowest Price Strategy**
- Selects provider with lowest `input_price + output_price`
- Cost optimization for budget-conscious deployments
- Tie-breaker: higher success rate

**Lowest Latency Strategy**
- Selects provider with lowest `avg_latency_ms`
- Performance optimization for latency-sensitive applications
- Handles unknown latency (treated as 999999ms)

**Round Robin Strategy**
- Distributes requests evenly using atomic counter
- Thread-safe for single-instance deployments
- Can be extended with Redis for distributed coordination

### 9.3 Failover Mechanism

**Error Classification:**
| Error Type | Backoff |
|------------|---------|
| `rate_limit` | 30s |
| `timeout` | 5s |
| `error_5xx` | 10s |
| `error_4xx` | 60s |
| `insufficient_quota` | 5m |

**Features:**
- Exponential backoff with cap (5s max)
- Per-provider, per-model failure tracking
- Automatic state cleanup after TTL (5m)
- `ShouldSkip()` for filtering failed providers

### 9.4 KeyPool Extensions

```go
// Get providers with available keys for a model
func (m *Manager) GetProvidersForModel(model string) []string

// Get statistics for providers supporting a model
func (m *Manager) GetProviderStats(model string) map[string]ProviderStats

type ProviderStats struct {
    AvailableKeys int     // Keys available for routing
    TotalKeys     int     // Total keys in pool
    AvgLatencyMs  int     // Average response latency
    TotalRequests int64   // Total requests served
    SuccessRate   float64 // Recent success rate (0-1)
}
```

### 9.5 Git Commit

```
2262bf9 feat(phase3): routing strategy and failover mechanism
```

### 9.6 Next Steps for Full Integration

1. **Configuration Support** ✅ COMPLETED
   - Add routing config to `config.yaml`
   - Load strategy from environment or config file
   - Added `RoutingConfig` struct to `internal/config/config.go`

2. **Handler Integration** ✅ COMPLETED
   - Modified `cmd/server/main.go` to initialize Router
   - Added `SetRouter()` in `internal/server/admin_handlers.go`
   - Router instance available for request handling

3. **Portal Sync Extension** ✅ COMPLETED
   - `input_price`, `output_price` already in `ServiceModel` struct
   - `GetModelPrice()` method available for routing decisions
   - `ResolveModel()` returns price info for model resolution

4. **Admin API** ✅ COMPLETED
   - `GET /admin/routing/config` - Get current routing configuration
   - `PUT /admin/routing/config` - Update routing configuration
   - `GET /admin/routing/stats` - Get routing statistics

5. **Testing** ✅ COMPLETED
   - Unit tests for each strategy
   - Integration tests with mock providers
   - Failover simulation tests

---

## 10. Testing Summary (2026-05-07)

### 10.1 Test File

`pkg/routing/routing_test.go` - Comprehensive test suite

### 10.2 Test Coverage

| Test Suite | Tests | Description |
|------------|-------|-------------|
| `TestPriorityStrategy` | 4 | Priority selection, tie-breaking, empty candidates |
| `TestLowestPriceStrategy` | 5 | Price selection, tie-breaking, zero prices |
| `TestLowestLatencyStrategy` | 5 | Latency selection, tie-breaking, unknown latency |
| `TestRoundRobinStrategy` | 4 | Distribution, single candidate, empty candidates |
| `TestFailoverManager` | 5 | Failure tracking, backoff, state cleanup |
| `TestRouter` | 9 | Strategy changes, failover, retry logic |
| `TestFactory` | 4 | Strategy registration, config creation |
| `TestSelectWithFailover` | 2 | Full failover flow |
| `TestDefaultConfig` | 1 | Default values verification |

**Total: 39 test cases**

### 10.3 Test Results

```
=== RUN   TestPriorityStrategy
--- PASS: TestPriorityStrategy (0.00s)
=== RUN   TestLowestPriceStrategy
--- PASS: TestLowestPriceStrategy (0.00s)
=== RUN   TestLowestLatencyStrategy
--- PASS: TestLowestLatencyStrategy (0.00s)
=== RUN   TestRoundRobinStrategy
--- PASS: TestRoundRobinStrategy (0.00s)
=== RUN   TestFailoverManager
--- PASS: TestFailoverManager (0.00s)
=== RUN   TestRouter
--- PASS: TestRouter (0.00s)
=== RUN   TestFactory
--- PASS: TestFactory (0.00s)
=== RUN   TestSelectWithFailover
--- PASS: TestSelectWithFailover (0.00s)
=== RUN   TestDefaultConfig
--- PASS: TestDefaultConfig (0.00s)
PASS
ok  	github.com/ai-proxy/ai-proxy-go/pkg/routing	0.003s
```

### 10.4 Key Test Scenarios

**Strategy Selection:**
- Empty candidates returns `ErrNoCandidates`
- Single candidate returns that candidate
- Multiple candidates sorted by strategy criteria
- Tie-breaking by success rate

**Failover:**
- Initially no providers are skipped
- Failures are tracked per provider/model
- Different error types have different backoffs
- Success clears failure state
- `ShouldSkip()` correctly filters failed providers

**Router Integration:**
- Strategy can be changed at runtime
- Failed providers are filtered from selection
- `SelectWithFailover` returns ordered candidates
- Retry logic with exponential backoff
- Backoff capped at 5 seconds
