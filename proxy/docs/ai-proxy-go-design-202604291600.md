# AI-Proxy Go Implementation Design

**Version:** 1.0  
**Date:** 2026-04-29  
**Status:** Design Phase

---

## 1. Overview

### 1.1 Project Goals

Rewrite `ai-proxy-singapore` in Go, merging with `new-api` source code to create a unified, high-performance AI API gateway.

**Key Objectives:**
- Maximize code reuse from new-api (Go)
- Maintain data structure consistency for token counting and format conversion
- Preserve 7-year audit log compliance
- Keep ai-proxy-portal (Node.js) unchanged
- Deploy as independent service in separate container/server

### 1.2 Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           Client Requests                                │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                    ai-proxy-portal (Node.js) - UNCHANGED                 │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐          │
│  │ Admin Dashboard │  │ Billing/Invoice │  │ User Management │          │
│  │ (React)         │  │ System          │  │ API             │          │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘          │
│                                                                          │
│  PostgreSQL: users, invoices, billing_records, api_keys                 │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ HTTP REST API
                                    │ (Key sync, pricing, audit)
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                    ai-proxy-singapore (Go) - REWRITTEN                   │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                    API Gateway Layer                              │   │
│  │  - JWT Authentication (validate Portal tokens)                    │   │
│  │  - Rate Limiting (user-level, Redis-backed)                       │   │
│  │  - Request Routing                                                │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                    │                                     │
│                                    ▼                                     │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                    Smart Proxy Layer (from new-api)               │   │
│  │  - Token Estimation (service/token_counter.go)                    │   │
│  │  - Channel Affinity Cache (service/channel_affinity.go)           │   │
│  │  - Format Conversion (service/convert.go)                         │   │
│  │  - Pre-consume Billing (service/quota.go)                         │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                    │                                     │
│                                    ▼                                     │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                    Provider Adapters (from new-api)               │   │
│  │  relay/channel/openai/    - OpenAI, Azure                         │   │
│  │  relay/channel/claude/    - Anthropic Claude                      │   │
│  │  relay/channel/gemini/    - Google Gemini                         │   │
│  │  relay/channel/deepseek/  - DeepSeek                              │   │
│  │  relay/channel/ali/       - Alibaba Qwen                          │   │
│  │  relay/channel/moonshot/  - Moonshot Kimi                         │   │
│  │  relay/channel/zhipu/     - Zhipu GLM                             │   │
│  │  ... 40+ providers                                                │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                    │                                     │
│                                    ▼                                     │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                    Audit & Logging Layer                          │   │
│  │  - 7-year audit log retention                                     │   │
│  │  - Usage tracking (cached tokens, cache hits)                     │   │
│  │  - Cost calculation with cache discounts                          │   │
│  │  - Dead Letter Queue for failed requests                          │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  Redis: rate limits, channel affinity cache, session data              │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
                    Upstream Providers (OpenAI, Claude, Gemini, etc.)
```

---

## 2. Project Structure

### 2.1 Directory Layout

```
ai-proxy-go/
├── cmd/
│   └── server/
│       └── main.go              # Entry point
│
├── internal/
│   ├── config/
│   │   ├── config.go           # Configuration management
│   │   └── config.yaml         # Default config
│   │
│   ├── auth/
│   │   ├── jwt.go              # JWT validation (Portal tokens)
│   │   └── middleware.go       # Auth middleware
│   │
│   ├── ratelimit/
│   │   ├── limiter.go          # Rate limiting logic
│   │   └── redis_store.go      # Redis-backed rate limit store
│   │
│   ├── portal/
│   │   ├── client.go           # Portal API client
│   │   ├── key_sync.go         # Key pool synchronization
│   │   └── pricing_sync.go     # Pricing synchronization
│   │
│   ├── audit/
│   │   ├── logger.go           # Audit logging
│   │   ├── retention.go        # 7-year retention policy
│   │   └── archive.go          # Archive to cold storage
│   │
│   └── server/
│       ├── router.go           # HTTP routing
│       └── handlers.go         # Request handlers
│
├── pkg/
│   ├── token/                  # Token estimation (from new-api)
│   │   ├── counter.go          # Copy from new-api/service/token_counter.go
│   │   ├── estimator.go        # Copy from new-api/service/token_estimator.go
│   │   └── tokenizer.go        # Copy from new-api/service/tokenizer.go
│   │
│   ├── cache/                  # Cache optimization (from new-api)
│   │   ├── affinity.go         # Copy from new-api/service/channel_affinity.go
│   │   └── hybrid.go           # Copy from new-api/pkg/cachex/
│   │
│   ├── convert/                # Format conversion (from new-api)
│   │   ├── claude_to_openai.go # Copy from new-api/service/convert.go
│   │   ├── openai_to_claude.go # 
│   │   └── gemini.go           # 
│   │
│   ├── billing/                # Billing calculation (from new-api)
│   │   ├── quota.go            # Copy from new-api/service/quota.go
│   │   ├── text_quota.go       # Copy from new-api/service/text_quota.go
│   │   └── cache_billing.go    # Cache token billing logic
│   │
│   └── relay/                  # Provider adapters (from new-api)
│       ├── adaptor.go          # Copy from new-api/relay/
│       ├── relay_task.go       # 
│       └── channel/            # All provider adapters
│           ├── openai/
│           ├── claude/
│           ├── gemini/
│           ├── deepseek/
│           ├── ali/
│           └── ... (40+ providers)
│
├── dto/                        # Data transfer objects (from new-api)
│   ├── openai_request.go       # Copy from new-api/dto/
│   ├── openai_response.go      # 
│   ├── claude.go               # 
│   └── usage.go                # Usage with cache tokens
│
├── types/                      # Type definitions (from new-api)
│   ├── relay.go                # Relay format types
│   └── file.go                 # File source types
│
├── migrations/
│   └── 001_audit_partition.sql # Audit table partitioning
│
├── scripts/
│   ├── build.sh
│   └── deploy.sh
│
├── go.mod
├── go.sum
├── Dockerfile
├── Makefile
└── README.md
```

### 2.2 Code Reuse from new-api

| Source File (new-api) | Target Location | Reuse Type |
|-----------------------|-----------------|------------|
| `service/token_counter.go` | `pkg/token/counter.go` | Direct copy |
| `service/token_estimator.go` | `pkg/token/estimator.go` | Direct copy |
| `service/channel_affinity.go` | `pkg/cache/affinity.go` | Direct copy |
| `service/convert.go` | `pkg/convert/` | Direct copy |
| `service/quota.go` | `pkg/billing/quota.go` | Direct copy |
| `service/text_quota.go` | `pkg/billing/text_quota.go` | Direct copy |
| `relay/relay_task.go` | `pkg/relay/relay_task.go` | Direct copy |
| `relay/channel/*` | `pkg/relay/channel/` | Direct copy |
| `dto/*` | `dto/` | Direct copy |
| `pkg/cachex/*` | `pkg/cache/` | Direct copy |

---

## 3. Data Structures

### 3.1 Token Usage (Consistent with new-api)

```go
// dto/usage.go - Copied from new-api/dto/openai_response.go

type Usage struct {
    PromptTokens         int    `json:"prompt_tokens"`
    CompletionTokens     int    `json:"completion_tokens"`
    TotalTokens          int    `json:"total_tokens"`
    PromptCacheHitTokens int    `json:"prompt_cache_hit_tokens,omitempty"` // OpenAI style

    PromptTokensDetails    InputTokenDetails  `json:"prompt_tokens_details"`
    CompletionTokenDetails OutputTokenDetails `json:"completion_tokens_details"`
    InputTokens            int                `json:"input_tokens"`
    OutputTokens           int                `json:"output_tokens"`
    InputTokensDetails     *InputTokenDetails `json:"input_tokens_details"`

    // Claude cache tokens
    ClaudeCacheCreation5mTokens int `json:"claude_cache_creation_5_m_tokens"`
    ClaudeCacheCreation1hTokens int `json:"claude_cache_creation_1_h_tokens"`
}

type InputTokenDetails struct {
    CachedTokens         int `json:"cached_tokens"`           // Cache read
    CachedCreationTokens int `json:"cached_creation_tokens"`  // Cache write
    TextTokens           int `json:"text_tokens"`
    AudioTokens          int `json:"audio_tokens"`
    ImageTokens          int `json:"image_tokens"`
}

type OutputTokenDetails struct {
    TextTokens      int `json:"text_tokens"`
    AudioTokens     int `json:"audio_tokens"`
    ImageTokens     int `json:"image_tokens"`
    ReasoningTokens int `json:"reasoning_tokens"`
}
```

### 3.2 Audit Log Structure (7-Year Retention)

```go
// internal/audit/logger.go

type AuditLog struct {
    ID              int64     `json:"id" db:"id"`
    TraceID         string    `json:"trace_id" db:"trace_id"`
    UserID          int       `json:"user_id" db:"user_id"`
    UserAPIKeyID    int       `json:"user_api_key_id" db:"user_api_key_id"`
    UpstreamKeyID   string    `json:"upstream_key_id" db:"upstream_key_id"`
    
    // Model and Provider
    Model           string    `json:"model" db:"model"`
    Provider        string    `json:"provider" db:"provider"`
    Region          string    `json:"region" db:"region"` // china, overseas
    
    // Token Usage (consistent with new-api)
    InputTokens         int   `json:"input_tokens" db:"input_tokens"`
    OutputTokens        int   `json:"output_tokens" db:"output_tokens"`
    CachedInputTokens   int   `json:"cached_input_tokens" db:"cached_input_tokens"`
    CacheCreationTokens int   `json:"cache_creation_tokens" db:"cache_creation_tokens"`
    CacheCreation5m     int   `json:"cache_creation_5m" db:"cache_creation_5m"`
    CacheCreation1h     int   `json:"cache_creation_1h" db:"cache_creation_1h"`
    ReasoningTokens     int   `json:"reasoning_tokens" db:"reasoning_tokens"`
    AudioInputTokens    int   `json:"audio_input_tokens" db:"audio_input_tokens"`
    AudioOutputTokens   int   `json:"audio_output_tokens" db:"audio_output_tokens"`
    ImageTokens         int   `json:"image_tokens" db:"image_tokens"`
    
    // Cost Breakdown
    InputCost       float64   `json:"input_cost" db:"input_cost"`
    OutputCost      float64   `json:"output_cost" db:"output_cost"`
    CacheReadCost   float64   `json:"cache_read_cost" db:"cache_read_cost"`
    CacheWriteCost  float64   `json:"cache_write_cost" db:"cache_write_cost"`
    ReasoningCost   float64   `json:"reasoning_cost" db:"reasoning_cost"`
    TotalCost       float64   `json:"total_cost" db:"total_cost"`
    
    // Performance
    LatencyMs       int       `json:"latency_ms" db:"latency_ms"`
    UpstreamLatency int       `json:"upstream_latency_ms" db:"upstream_latency_ms"`
    
    // Status
    Status          string    `json:"status" db:"status"` // success, error, timeout
    ErrorCode       string    `json:"error_code" db:"error_code"`
    ErrorMessage    string    `json:"error_message" db:"error_message"`
    
    // Cache Optimization
    CacheHit        bool      `json:"cache_hit" db:"cache_hit"`
    ChannelID       int       `json:"channel_id" db:"channel_id"`
    AffinityRule    string    `json:"affinity_rule" db:"affinity_rule"`
    
    // Timestamps
    CreatedAt       time.Time `json:"created_at" db:"created_at"`
    ArchivedAt      *time.Time `json:"archived_at" db:"archived_at"`
    
    // Retention
    RetentionUntil  time.Time `json:"retention_until" db:"retention_until"` // 7 years from created_at
}

// Table partitioning for 7-year retention
// Hot: 0-3 months (PostgreSQL hot partition)
// Warm: 3-12 months (PostgreSQL warm partition)
// Cold: 12-84 months (S3/Glacier archive)
```

### 3.3 Channel Affinity Cache (from new-api)

```go
// pkg/cache/affinity.go - Copied from new-api/service/channel_affinity.go

type ChannelAffinityUsageCacheStats struct {
    RuleName            string `json:"rule_name"`
    UsingGroup          string `json:"using_group"`
    KeyFingerprint      string `json:"key_fp"`
    CachedTokenRateMode string `json:"cached_token_rate_mode"`

    Hit           int64 `json:"hit"`
    Total         int64 `json:"total"`
    WindowSeconds int64 `json:"window_seconds"`

    PromptTokens         int64 `json:"prompt_tokens"`
    CompletionTokens     int64 `json:"completion_tokens"`
    TotalTokens          int64 `json:"total_tokens"`
    CachedTokens         int64 `json:"cached_tokens"`
    PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
    LastSeenAt           int64 `json:"last_seen_at"`
}
```

---

## 4. Core Components

### 4.1 Token Estimation (from new-api)

```go
// pkg/token/counter.go

// CountTextToken - Count tokens for text content
// Uses tiktoken for OpenAI models, estimation for others
func CountTextToken(text string, model string) int

// EstimateRequestToken - Estimate total tokens for a request
// Includes text, images, audio, and formatting overhead
func EstimateRequestToken(c *gin.Context, meta *types.TokenCountMeta, info *relaycommon.RelayInfo) (int, error)

// getImageToken - Calculate image tokens based on model
// Tile-based for GPT-4o/4.1, patch-based for mini/nano variants
func getImageToken(c *gin.Context, fileMeta *types.FileMeta, model string, stream bool) (int, error)

// CountAudioTokenInput/Output - Audio token calculation
// Input: 100 tokens/min, Output: 200 tokens/min
func CountAudioTokenInput(audioBase64 string, audioFormat string) (int, error)
func CountAudioTokenOutput(audioBase64 string, audioFormat string) (int, error)
```

### 4.2 Channel Affinity Cache (from new-api)

```go
// pkg/cache/affinity.go

// GetPreferredChannelByAffinity - Route requests to same channel for cache hits
// Uses Redis-backed hybrid cache for distributed deployments
func GetPreferredChannelByAffinity(c *gin.Context, modelName string, usingGroup string) (int, bool)

// RecordChannelAffinity - Record successful channel for future affinity
func RecordChannelAffinity(c *gin.Context, channelID int)

// ObserveChannelAffinityUsageCache - Track cache hit rates per channel
func ObserveChannelAffinityUsageCacheByRelayFormat(c *gin.Context, usage *dto.Usage, relayFormat types.RelayFormat)
```

### 4.3 Format Conversion (from new-api)

```go
// pkg/convert/convert.go

// ClaudeToOpenAIRequest - Convert Claude request to OpenAI format
// Preserves cache_control during conversion
func ClaudeToOpenAIRequest(claudeRequest dto.ClaudeRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error)

// RequestOpenAI2ClaudeMessage - Convert OpenAI request to Claude format
// Handles tools, system messages, cache control
func RequestOpenAI2ClaudeMessage(c *gin.Context, textRequest dto.GeneralOpenAIRequest) (*dto.ClaudeRequest, error)

// OpenAI to Gemini conversion
func OpenAIToGeminiRequest(openAIRequest dto.GeneralOpenAIRequest) (*GeminiRequest, error)
```

### 4.4 Billing with Cache Discounts (from new-api)

```go
// pkg/billing/quota.go

// Pre-consume quota before API call (using estimated tokens)
func PreConsumeBilling(c *gin.Context, quota int, info *relaycommon.RelayInfo) error

// Settle billing after response (adjust for actual vs estimated)
func SettleBilling(c *gin.Context, info *relaycommon.RelayInfo, quota int) error

// Calculate quota with cache discounts
// effectiveInput = promptTokens - cachedTokens
// baseCost = effectiveInput × modelRatio
// cacheCost = cachedTokens × cacheRatio
// totalCost = baseCost + cacheCost + outputCost
func CalculateQuotaWithCache(usage *dto.Usage, priceData types.PriceData) int
```

---

## 5. Audit System (7-Year Retention)

### 5.1 Storage Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         Audit Log Storage                                │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │  Hot Storage (PostgreSQL) - 0-3 months                          │    │
│  │  - usage_records_hot table (partitioned by month)               │    │
│  │  - Full query capability                                        │    │
│  │  - Indexed by user_id, model, created_at                        │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                           │                                              │
│                           │ Monthly migration job                        │
│                           ▼                                              │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │  Warm Storage (PostgreSQL) - 3-12 months                        │    │
│  │  - usage_records_warm table (partitioned by quarter)            │    │
│  │  - Compressed, limited query                                    │    │
│  │  - Indexed by user_id, created_at                               │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                           │                                              │
│                           │ Quarterly archive job                        │
│                           ▼                                              │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │  Cold Storage (S3/Glacier) - 12-84 months (7 years)             │    │
│  │  - Parquet format, compressed                                   │    │
│  │  - Restore on demand for compliance audits                       │    │
│  │  - Retention policy: delete after 7 years                       │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 5.2 Audit Log Schema

```sql
-- Hot partition (0-3 months)
CREATE TABLE usage_records_hot (
    id BIGSERIAL,
    trace_id VARCHAR(64) NOT NULL,
    user_id INTEGER NOT NULL,
    user_api_key_id INTEGER,
    upstream_key_id VARCHAR(64),
    
    model VARCHAR(128) NOT NULL,
    provider VARCHAR(64) NOT NULL,
    region VARCHAR(32),
    
    -- Token usage (consistent with new-api)
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    cached_input_tokens INTEGER DEFAULT 0,
    cache_creation_tokens INTEGER DEFAULT 0,
    cache_creation_5m INTEGER DEFAULT 0,
    cache_creation_1h INTEGER DEFAULT 0,
    reasoning_tokens INTEGER DEFAULT 0,
    audio_input_tokens INTEGER DEFAULT 0,
    audio_output_tokens INTEGER DEFAULT 0,
    image_tokens INTEGER DEFAULT 0,
    
    -- Cost breakdown
    input_cost DECIMAL(18,10) DEFAULT 0,
    output_cost DECIMAL(18,10) DEFAULT 0,
    cache_read_cost DECIMAL(18,10) DEFAULT 0,
    cache_write_cost DECIMAL(18,10) DEFAULT 0,
    reasoning_cost DECIMAL(18,10) DEFAULT 0,
    total_cost DECIMAL(18,10) DEFAULT 0,
    
    -- Performance
    latency_ms INTEGER,
    upstream_latency_ms INTEGER,
    
    -- Status
    status VARCHAR(32) NOT NULL,
    error_code VARCHAR(64),
    error_message TEXT,
    
    -- Cache optimization
    cache_hit BOOLEAN DEFAULT FALSE,
    channel_id INTEGER,
    affinity_rule VARCHAR(128),
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    retention_until TIMESTAMP WITH TIME ZONE DEFAULT NOW() + INTERVAL '7 years',
    
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Create monthly partitions
CREATE TABLE usage_records_hot_2026_04 PARTITION OF usage_records_hot
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');

CREATE TABLE usage_records_hot_2026_05 PARTITION OF usage_records_hot
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');

-- Warm partition (3-12 months)
CREATE TABLE usage_records_warm (LIKE usage_records_hot INCLUDING ALL)
    PARTITION BY RANGE (created_at);

-- Create quarterly partitions for warm
CREATE TABLE usage_records_warm_2025_q1 PARTITION OF usage_records_warm
    FOR VALUES FROM ('2025-01-01') TO ('2025-04-01');

-- Indexes for query performance
CREATE INDEX idx_usage_hot_user ON usage_records_hot (user_id, created_at);
CREATE INDEX idx_usage_hot_model ON usage_records_hot (model, created_at);
CREATE INDEX idx_usage_hot_cache ON usage_records_hot (cache_hit, created_at);
```

### 5.3 Retention Policy Implementation

```go
// internal/audit/retention.go

type RetentionPolicy struct {
    HotDuration    time.Duration // 3 months
    WarmDuration   time.Duration // 12 months
    ColdDuration   time.Duration // 7 years (84 months)
    ArchiveTarget  string        // S3 bucket path
}

// MigrateHotToWarm - Move records older than 3 months to warm storage
func (r *RetentionPolicy) MigrateHotToWarm(ctx context.Context) error {
    cutoff := time.Now().Add(-r.HotDuration)
    
    // Copy to warm table
    _, err := db.Exec(`
        INSERT INTO usage_records_warm
        SELECT * FROM usage_records_hot
        WHERE created_at < $1
    `, cutoff)
    
    // Delete from hot
    _, err = db.Exec(`
        DELETE FROM usage_records_hot
        WHERE created_at < $1
    `, cutoff)
    
    return err
}

// ArchiveWarmToCold - Archive records older than 12 months to S3
func (r *RetentionPolicy) ArchiveWarmToCold(ctx context.Context) error {
    cutoff := time.Now().Add(-r.WarmDuration)
    
    // Query records
    rows, err := db.Query(`
        SELECT * FROM usage_records_warm
        WHERE created_at < $1
        ORDER BY created_at
    `, cutoff)
    
    // Convert to Parquet
    parquetData := convertToParquet(rows)
    
    // Upload to S3 with Glacier tier
    key := fmt.Sprintf("audit/%s/%s.parquet", 
        cutoff.Format("2006-01"), 
        time.Now().Format("20060102-150405"))
    
    err = s3Client.PutObject(&s3.PutObjectInput{
        Bucket:          aws.String(r.ArchiveTarget),
        Key:             aws.String(key),
        Body:            bytes.NewReader(parquetData),
        StorageClass:    s3.StorageClassGlacier,
    })
    
    // Delete from warm after successful archive
    _, err = db.Exec(`
        DELETE FROM usage_records_warm
        WHERE created_at < $1
    `, cutoff)
    
    return err
}

// RestoreFromCold - Restore archived records for compliance audit
func (r *RetentionPolicy) RestoreFromCold(ctx context.Context, startDate, endDate time.Time) ([]AuditLog, error) {
    // List objects in S3 for the date range
    objects := s3Client.ListObjects(...)
    
    // Restore from Glacier (may take hours)
    for _, obj := range objects {
        s3Client.RestoreObject(&s3.RestoreObjectInput{
            Bucket:         aws.String(r.ArchiveTarget),
            Key:            obj.Key,
            RestoreRequest: &s3.RestoreRequest{
                Days: aws.Int64(7), // Temporary restore for 7 days
            },
        })
    }
    
    // Download and parse Parquet
    // ...
}
```

### 5.4 Audit Log Writing

```go
// internal/audit/logger.go

type AuditLogger struct {
    db          *sql.DB
    batch       []AuditLog
    batchSize   int
    flushTicker *time.Ticker
}

func NewAuditLogger(db *sql.DB) *AuditLogger {
    logger := &AuditLogger{
        db:        db,
        batchSize: 100,
    }
    
    // Flush every 1 second
    logger.flushTicker = time.NewTicker(1 * time.Second)
    go logger.flushLoop()
    
    return logger
}

// Record - Add audit log to batch
func (l *AuditLogger) Record(log AuditLog) {
    log.RetentionUntil = time.Now().Add(7 * 365 * 24 * time.Hour)
    
    l.batch = append(l.batch, log)
    
    if len(l.batch) >= l.batchSize {
        l.flush()
    }
}

// flush - Batch insert to database
func (l *AuditLogger) flush() error {
    if len(l.batch) == 0 {
        return nil
    }
    
    tx, _ := l.db.Begin()
    
    stmt, _ := tx.Prepare(`
        INSERT INTO usage_records_hot (
            trace_id, user_id, user_api_key_id, upstream_key_id,
            model, provider, region,
            input_tokens, output_tokens, cached_input_tokens,
            cache_creation_tokens, cache_creation_5m, cache_creation_1h,
            reasoning_tokens, audio_input_tokens, audio_output_tokens, image_tokens,
            input_cost, output_cost, cache_read_cost, cache_write_cost,
            reasoning_cost, total_cost,
            latency_ms, upstream_latency_ms,
            status, error_code, error_message,
            cache_hit, channel_id, affinity_rule,
            retention_until
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32)
    `)
    
    for _, log := range l.batch {
        stmt.Exec(
            log.TraceID, log.UserID, log.UserAPIKeyID, log.UpstreamKeyID,
            log.Model, log.Provider, log.Region,
            log.InputTokens, log.OutputTokens, log.CachedInputTokens,
            log.CacheCreationTokens, log.CacheCreation5m, log.CacheCreation1h,
            log.ReasoningTokens, log.AudioInputTokens, log.AudioOutputTokens, log.ImageTokens,
            log.InputCost, log.OutputCost, log.CacheReadCost, log.CacheWriteCost,
            log.ReasoningCost, log.TotalCost,
            log.LatencyMs, log.UpstreamLatency,
            log.Status, log.ErrorCode, log.ErrorMessage,
            log.CacheHit, log.ChannelID, log.AffinityRule,
            log.RetentionUntil,
        )
    }
    
    tx.Commit()
    l.batch = l.batch[:0]
    
    return nil
}
```

---

## 6. API Endpoints

### 6.1 Proxy Endpoints

```go
// internal/server/router.go

func SetupRouter() *gin.Engine {
    r := gin.Default()
    
    // Health check
    r.GET("/health", handlers.HealthCheck)
    
    // Proxy endpoints (require authentication)
    proxy := r.Group("/v1")
    proxy.Use(auth.Middleware())
    proxy.Use(ratelimit.Middleware())
    {
        proxy.POST("/chat/completions", handlers.ChatCompletions)
        proxy.POST("/completions", handlers.Completions)
        proxy.POST("/embeddings", handlers.Embeddings)
        proxy.POST("/images/generations", handlers.ImageGenerations)
        proxy.POST("/audio/transcriptions", handlers.AudioTranscriptions)
        proxy.POST("/audio/translations", handlers.AudioTranslations)
        
        // Claude native endpoint
        proxy.POST("/messages", handlers.ClaudeMessages)
        
        // Gemini native endpoint
        proxy.POST("/models/:model:generateContent", handlers.GeminiGenerate)
    }
    
    // Admin endpoints (require admin auth)
    admin := r.Group("/admin")
    admin.Use(auth.AdminMiddleware())
    {
        admin.GET("/stats", handlers.GetStats)
        admin.GET("/cache/affinity", handlers.GetAffinityStats)
        admin.DELETE("/cache/affinity", handlers.ClearAffinityCache)
    }
    
    return r
}
```

### 6.2 Request Handler

```go
// internal/server/handlers.go

func ChatCompletions(c *gin.Context) {
    traceID := uuid.New().String()
    startTime := time.Now()
    
    // 1. Parse request
    var req dto.GeneralOpenAIRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }
    
    // 2. Get user info from context (set by auth middleware)
    userID := c.GetInt("user_id")
    apiKeyID := c.GetInt("api_key_id")
    
    // 3. Estimate tokens (from new-api)
    tokenMeta := &types.TokenCountMeta{
        CombineText:   extractTextFromMessages(req.Messages),
        MessagesCount: len(req.Messages),
        ToolsCount:    len(req.Tools),
        Files:         extractFilesFromMessages(req.Messages),
    }
    
    relayInfo := &relaycommon.RelayInfo{
        IsStream: req.Stream,
    }
    
    estimatedTokens, err := token.EstimateRequestToken(c, tokenMeta, relayInfo)
    if err != nil {
        log.Warnf("Token estimation failed: %v", err)
    }
    
    // 4. Pre-consume quota (optional, based on config)
    if config.PreConsumeEnabled && estimatedTokens > 0 {
        if err := billing.PreConsumeBilling(c, estimatedTokens, relayInfo); err != nil {
            c.JSON(429, gin.H{"error": "Quota exceeded"})
            return
        }
    }
    
    // 5. Get channel with affinity (from new-api)
    channelID, hasAffinity := cache.GetPreferredChannelByAffinity(c, req.Model, "default")
    
    // 6. Get upstream key
    keyInfo, err := portalClient.GetKey(c, req.Model, channelID)
    if err != nil {
        c.JSON(503, gin.H{"error": "No available upstream keys"})
        return
    }
    
    // 7. Create adapter (from new-api)
    adapter := relay.GetAdaptor(keyInfo.Provider, &relay.AdaptorConfig{
        BaseURL: keyInfo.BaseURL,
        APIKey:  keyInfo.Key,
    })
    
    // 8. Convert request format if needed (from new-api)
    providerReq := adapter.ToProvider(req)
    
    // 9. Make upstream request
    upstreamStart := time.Now()
    response, err := adapter.DoRequest(c, providerReq)
    if err != nil {
        // Record failure
        audit.Record(AuditLog{
            TraceID:      traceID,
            UserID:       userID,
            Model:        req.Model,
            Provider:     keyInfo.Provider,
            Status:       "error",
            ErrorCode:    "upstream_error",
            ErrorMessage: err.Error(),
        })
        
        c.JSON(502, gin.H{"error": err.Error()})
        return
    }
    
    // 10. Parse response and extract usage (from new-api)
    usage := adapter.ExtractUsage(response)
    
    // 11. Record affinity for cache optimization (from new-api)
    if hasAffinity {
        cache.RecordChannelAffinity(c, keyInfo.ChannelID)
        cache.ObserveChannelAffinityUsageCacheByRelayFormat(c, usage, adapter.GetRelayFormat())
    }
    
    // 12. Calculate cost with cache discounts (from new-api)
    pricing := portalClient.GetPricing(req.Model)
    cost := billing.CalculateCostWithCache(usage, pricing)
    
    // 13. Settle billing
    billing.SettleBilling(c, relayInfo, cost.Quota)
    
    // 14. Record audit log
    audit.Record(AuditLog{
        TraceID:             traceID,
        UserID:              userID,
        UserAPIKeyID:        apiKeyID,
        UpstreamKeyID:       keyInfo.ID,
        Model:               req.Model,
        Provider:            keyInfo.Provider,
        InputTokens:         usage.PromptTokens,
        OutputTokens:        usage.CompletionTokens,
        CachedInputTokens:   usage.PromptTokensDetails.CachedTokens,
        CacheCreationTokens: usage.PromptTokensDetails.CachedCreationTokens,
        CacheCreation5m:     usage.ClaudeCacheCreation5mTokens,
        CacheCreation1h:     usage.ClaudeCacheCreation1hTokens,
        ReasoningTokens:     usage.CompletionTokenDetails.ReasoningTokens,
        InputCost:           cost.InputCost,
        OutputCost:          cost.OutputCost,
        CacheReadCost:       cost.CacheReadCost,
        CacheWriteCost:      cost.CacheWriteCost,
        TotalCost:           cost.TotalCost,
        LatencyMs:           int(time.Since(startTime).Milliseconds()),
        UpstreamLatency:     int(time.Since(upstreamStart).Milliseconds()),
        Status:              "success",
        CacheHit:            usage.PromptTokensDetails.CachedTokens > 0,
        ChannelID:           keyInfo.ChannelID,
    })
    
    // 15. Return response
    c.JSON(200, response)
}
```

---

## 7. Configuration

### 7.1 Configuration File

```yaml
# config.yaml

server:
  port: 8080
  mode: release  # debug, release

database:
  host: ${DB_HOST:localhost}
  port: ${DB_PORT:5432}
  name: ${DB_NAME:ai_proxy}
  user: ${DB_USER:postgres}
  password: ${DB_PASSWORD:}
  max_connections: 20

redis:
  host: ${REDIS_HOST:localhost}
  port: ${REDIS_PORT:6379}
  password: ${REDIS_PASSWORD:}
  db: ${REDIS_DB:0}

portal:
  url: ${PORTAL_URL:http://localhost:3001}
  sync_secret: ${PORTAL_SYNC_SECRET:}
  sync_interval: 60s

auth:
  jwt_secret: ${JWT_SECRET:}
  jwt_issuer: ${JWT_ISSUER:ai-proxy-portal}

rate_limit:
  enabled: true
  default_rpm: 60
  default_tpm: 100000
  redis_prefix: "ratelimit:"

cache:
  affinity:
    enabled: true
    max_entries: 100000
    default_ttl: 3600s
    redis_enabled: true

audit:
  enabled: true
  batch_size: 100
  flush_interval: 1s
  retention_years: 7
  hot_duration: 2160h      # 3 months
  warm_duration: 8760h     # 12 months
  archive:
    enabled: true
    s3_bucket: ${AUDIT_S3_BUCKET:}
    s3_prefix: "audit/"

billing:
  pre_consume_enabled: true
  cache_read_ratio: 0.1    # 10% of input price
  cache_write_ratio: 0.25  # 25% of input price

providers:
  openai:
    base_url: https://api.openai.com/v1
    timeout: 120s
  anthropic:
    base_url: https://api.anthropic.com
    timeout: 120s
  google:
    base_url: https://generativelanguage.googleapis.com
    timeout: 120s
```

---

## 8. Deployment

### 8.1 Dockerfile

```dockerfile
# Dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o ai-proxy ./cmd/server

# Final image
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/ai-proxy .
COPY --from=builder /app/config.yaml .

EXPOSE 8080

CMD ["./ai-proxy"]
```

### 8.2 Docker Compose

```yaml
# docker-compose.yml
version: '3.8'

services:
  ai-proxy:
    build: .
    image: ai-proxy-go:latest
    container_name: ai-proxy-go
    restart: always
    ports:
      - "8080:8080"
    environment:
      - DB_HOST=postgres
      - DB_PORT=5432
      - DB_NAME=ai_proxy
      - DB_USER=${DB_USER}
      - DB_PASSWORD=${DB_PASSWORD}
      - REDIS_HOST=redis
      - REDIS_PORT=6379
      - PORTAL_URL=http://portal:3001
      - PORTAL_SYNC_SECRET=${PORTAL_SYNC_SECRET}
      - JWT_SECRET=${JWT_SECRET}
    depends_on:
      - postgres
      - redis
    networks:
      - ai-proxy-net
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:8080/health"]
      interval: 30s
      timeout: 3s
      retries: 3

  postgres:
    image: postgres:15-alpine
    container_name: ai-proxy-postgres
    restart: always
    environment:
      - POSTGRES_DB=ai_proxy
      - POSTGRES_USER=${DB_USER}
      - POSTGRES_PASSWORD=${DB_PASSWORD}
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./migrations:/docker-entrypoint-initdb.d
    networks:
      - ai-proxy-net

  redis:
    image: redis:7-alpine
    container_name: ai-proxy-redis
    restart: always
    volumes:
      - redis_data:/data
    networks:
      - ai-proxy-net

volumes:
  postgres_data:
  redis_data:

networks:
  ai-proxy-net:
```

---

## 9. Migration Plan

### 9.1 Phase 1: Infrastructure Setup (Day 1-2)

- [ ] Create Go project structure
- [ ] Copy reusable code from new-api
- [ ] Setup configuration management
- [ ] Setup database migrations

### 9.2 Phase 2: Core Components (Day 3-5)

- [ ] Implement JWT authentication
- [ ] Implement rate limiting
- [ ] Implement Portal sync client
- [ ] Implement audit logger

### 9.3 Phase 3: Proxy Layer (Day 6-7)

- [ ] Integrate token estimation
- [ ] Integrate channel affinity cache
- [ ] Integrate format conversion
- [ ] Integrate provider adapters

### 9.4 Phase 4: Testing (Day 8-9)

- [ ] Unit tests
- [ ] Integration tests
- [ ] Load tests
- [ ] Feature parity validation with Node.js version

### 9.5 Phase 5: Deployment (Day 10)

- [ ] Build Docker image
- [ ] Deploy to staging
- [ ] Run parallel with Node.js version
- [ ] Gradual traffic migration
- [ ] Monitor and validate

---

## 10. Monitoring & Observability

### 10.1 Metrics

```go
// Prometheus metrics
var (
    RequestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "ai_proxy_request_duration_seconds",
            Help:    "Request duration in seconds",
            Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
        },
        []string{"model", "provider", "status"},
    )
    
    TokenUsage = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "ai_proxy_tokens_total",
            Help: "Total tokens processed",
        },
        []string{"type", "model"}, // type: input, output, cached
    )
    
    CacheHitRate = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "ai_proxy_cache_hit_rate",
            Help: "Cache hit rate per model",
        },
        []string{"model"},
    )
    
    UpstreamLatency = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "ai_proxy_upstream_latency_seconds",
            Help:    "Upstream API latency",
            Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60},
        },
        []string{"provider"},
    )
)
```

### 10.2 Logging

```go
// Structured logging with zap
logger, _ := zap.NewProduction()

// Request logging
logger.Info("request completed",
    zap.String("trace_id", traceID),
    zap.String("model", req.Model),
    zap.String("provider", keyInfo.Provider),
    zap.Int("input_tokens", usage.PromptTokens),
    zap.Int("output_tokens", usage.CompletionTokens),
    zap.Int("cached_tokens", usage.PromptTokensDetails.CachedTokens),
    zap.Float64("cost", cost.TotalCost),
    zap.Duration("latency", time.Since(startTime)),
)
```

---

## 11. Appendix

### A. Code Reuse Checklist

| Component | Source File | Copied | Adapted |
|-----------|-------------|--------|---------|
| Token Counter | new-api/service/token_counter.go | ☐ | ☐ |
| Token Estimator | new-api/service/token_estimator.go | ☐ | ☐ |
| Channel Affinity | new-api/service/channel_affinity.go | ☐ | ☐ |
| Format Conversion | new-api/service/convert.go | ☐ | ☐ |
| Quota/Billing | new-api/service/quota.go | ☐ | ☐ |
| Text Quota | new-api/service/text_quota.go | ☐ | ☐ |
| OpenAI Adapter | new-api/relay/channel/openai/ | ☐ | ☐ |
| Claude Adapter | new-api/relay/channel/claude/ | ☐ | ☐ |
| Gemini Adapter | new-api/relay/channel/gemini/ | ☐ | ☐ |
| DeepSeek Adapter | new-api/relay/channel/deepseek/ | ☐ | ☐ |
| Ali Adapter | new-api/relay/channel/ali/ | ☐ | ☐ |
| DTO Types | new-api/dto/ | ☐ | ☐ |
| Cache Utils | new-api/pkg/cachex/ | ☐ | ☐ |

### B. Feature Parity Checklist

| Feature | Node.js | Go | Status |
|---------|---------|-----|--------|
| JWT Authentication | ✅ | ☐ | |
| Rate Limiting | ✅ | ☐ | |
| Key Pool Sync | ✅ | ☐ | |
| OpenAI Proxy | ✅ | ☐ | |
| Claude Proxy | ✅ | ☐ | |
| Gemini Proxy | ✅ | ☐ | |
| Token Estimation | ❌ | ☐ | New feature |
| Channel Affinity | ❌ | ☐ | New feature |
| Format Conversion | ❌ | ☐ | New feature |
| Cache Token Billing | Basic | ☐ | Enhanced |
| 7-Year Audit | ✅ | ☐ | |
| Multi-Region | ✅ | ☐ | |

### C. Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | 8080 |
| `DB_HOST` | PostgreSQL host | localhost |
| `DB_PORT` | PostgreSQL port | 5432 |
| `DB_NAME` | Database name | ai_proxy |
| `DB_USER` | Database user | postgres |
| `DB_PASSWORD` | Database password | |
| `REDIS_HOST` | Redis host | localhost |
| `REDIS_PORT` | Redis port | 6379 |
| `REDIS_PASSWORD` | Redis password | |
| `PORTAL_URL` | Portal API URL | http://localhost:3001 |
| `PORTAL_SYNC_SECRET` | Portal sync secret | |
| `JWT_SECRET` | JWT signing secret | |
| `AUDIT_S3_BUCKET` | S3 bucket for cold archive | |
