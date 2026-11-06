package routes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/ai-proxy/ai-portal-go/pkg/metrics"
	"github.com/ai-proxy/ai-portal-go/pkg/telemetry"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// WebhookRoutes handles webhook endpoints from the Go Proxy
type WebhookRoutes struct {
	db          *db.DB
	cfg         *config.Config
	logger      *zap.Logger
	mailSender  *mail.Sender
	exchangeSvc *services.ExchangeService
	billingSvc  *services.BillingService
	notif       *services.NotificationService
}

// recordWebhookReceived increments the received webhook metric.
func (r *WebhookRoutes) recordWebhookReceived() {
	metrics.GetMetrics().WebhookReceived.Add(1)
}

// recordWebhookFailed increments the failed webhook metric.
func (r *WebhookRoutes) recordWebhookFailed() {
	metrics.GetMetrics().WebhookFailed.Add(1)
}

const maxWebhookBodySize = 1 * 1024 * 1024 // 1MB

// NewWebhookRoutes creates a new webhook routes handler
func NewWebhookRoutes(db *db.DB, cfg *config.Config, logger *zap.Logger, mailSender *mail.Sender, exchangeSvc *services.ExchangeService, notif *services.NotificationService) *WebhookRoutes {
	if cfg.Portal.SyncSecret == "" {
		logger.Warn("[WARN] Webhook signature validation disabled — set PORTAL_SYNC_SECRET for production")
	}
	return &WebhookRoutes{
		db:          db,
		cfg:         cfg,
		logger:      logger,
		mailSender:  mailSender,
		exchangeSvc: exchangeSvc,
		billingSvc:  services.NewBillingService(db, logger),
		notif:       notif,
	}
}

// Register registers all webhook routes
func (r *WebhookRoutes) Register(rg *gin.RouterGroup) {
	rg.POST("/usage", r.traceWebhook("usage"), r.requireWebhookSignature(), r.handleUsage)
	rg.POST("/usage-batch", r.traceWebhook("usage-batch"), r.requireWebhookSignature(), r.handleUsageBatch)
	rg.POST("/usage-stats-batch", r.traceWebhook("usage-stats-batch"), r.requireWebhookSignature(), r.handleUsageStatsBatch)
	rg.POST("/upstream-usage-batch", r.traceWebhook("upstream-usage-batch"), r.requireWebhookSignature(), r.handleUpstreamUsageBatch)
	rg.POST("/provider-health-batch", r.traceWebhook("provider-health-batch"), r.requireWebhookSignature(), r.handleProviderHealthBatch)
	rg.POST("/key-pool-status", r.traceWebhook("key-pool-status"), r.requireWebhookSignature(), r.handleKeyPoolStatus)
	rg.POST("/heartbeat", r.traceWebhook("heartbeat"), r.requireWebhookSignature(), r.handleHeartbeat)
	rg.POST("/pre-consume", r.traceWebhook("pre-consume"), r.requireWebhookSignature(), r.handlePreConsume)
	rg.POST("/settle", r.traceWebhook("settle"), r.requireWebhookSignature(), r.handleSettle)
}

// traceWebhook extracts the incoming traceparent and starts a span named
// webhook.<name>. The span context is attached to c.Request.Context() so
// downstream DB/Redis operations can carry it.
func (r *WebhookRoutes) traceWebhook(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := telemetry.Extract(c.Request.Context(), c.Request.Header)
		ctx, span := telemetry.Tracer().Start(ctx, "webhook."+name)
		defer span.End()

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// requireWebhookSignature validates HMAC-SHA256 signature from proxy
func (r *WebhookRoutes) requireWebhookSignature() gin.HandlerFunc {
	return func(c *gin.Context) {
		if r.cfg.Portal.SyncSecret == "" {
			// No secret configured, reject in production
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Webhook signature validation not configured"}})
			return
		}

		timestamp := c.GetHeader("X-Timestamp")
		signature := c.GetHeader("X-Signature")
		if signature == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Missing signature"}})
			return
		}

		// Validate timestamp (prevent replay attacks)
		if timestamp == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Missing timestamp"}})
			return
		}
		tsInt, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Invalid timestamp format"}})
			return
		}
		if math.Abs(float64(time.Now().Unix()-tsInt)) > 300 {
			r.logger.Warn("Webhook timestamp out of tolerance",
				zap.String("timestamp", timestamp),
				zap.String("remote_ip", c.ClientIP()),
			)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Timestamp out of tolerance"}})
			return
		}

		// Read body with size limit
		body, _ := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodySize))
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		// Compute expected signature: HMAC-SHA256(timestamp + "." + body)
		mac := hmac.New(sha256.New, []byte(r.cfg.Portal.SyncSecret))
		mac.Write([]byte(timestamp + "."))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))

		if !hmac.Equal([]byte(signature), []byte(expected)) {
			r.logger.Warn("Webhook signature mismatch",
				zap.String("path", c.Request.URL.Path),
				zap.String("remote_ip", c.ClientIP()),
			)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Invalid signature"}})
			return
		}

		c.Next()
	}
}

// UsageRecord represents a single usage record from the proxy
// NOTE: New proxy versions do NOT send Cost. Portal calculates cost from model pricing.
type UsageRecord struct {
	TraceID              string  `json:"traceId"`
	APIKeyHash           string  `json:"apiKeyHash"`
	Model                string  `json:"model"`
	Provider             string  `json:"provider"`
	Region               string  `json:"region"`
	InputTokens          int     `json:"inputTokens"`
	OutputTokens         int     `json:"outputTokens"`
	CachedTokens         int     `json:"cachedTokens"`        // cache hit tokens
	CacheCreationTokens  int     `json:"cacheCreationTokens"` // cache write tokens (new proxy)
	EffectiveInputTokens int     `json:"effectiveInputTokens"`
	ReasoningTokens      int     `json:"reasoningTokens"`
	Cost                 float64 `json:"cost,omitempty"` // deprecated: old proxy sends this
	LatencyMs            int     `json:"latencyMs"`
	RequestCount         int     `json:"requestCount"`
	ServerID             string  `json:"serverId"`
	UpstreamKeyID        string  `json:"upstreamKeyId"`
	RetryCount           int     `json:"retryCount"`
	Timestamp            string  `json:"timestamp"`
	CacheHit             bool    `json:"cacheHit"`
	Error                string  `json:"error"`
	ErrorCode            string  `json:"errorCode,omitempty"`
	GroupID              string  `json:"groupId,omitempty"`
	ActualGroupID        string  `json:"actualGroupId,omitempty"`
	IsFallback           bool    `json:"isFallback,omitempty"`
	PricingRatio         float64 `json:"pricingRatio,omitempty"`
	RoutingTrack         string  `json:"routingTrack,omitempty"`
	OriginalTrack        string  `json:"originalTrack,omitempty"`
	Endpoint             string  `json:"endpoint,omitempty"`
	IsStreaming          bool    `json:"isStreaming,omitempty"`
	ClientIP             string  `json:"clientIp,omitempty"`
	ClientGeo            string  `json:"clientGeo,omitempty"`
	UserAgent            string  `json:"userAgent,omitempty"`
	AgentTools           string  `json:"agentTools,omitempty"`
	CalledTools          string  `json:"calledTools,omitempty"`
	TTFTMs               int     `json:"ttftMs,omitempty"`
	ReasoningIntensity   string  `json:"reasoningIntensity,omitempty"`
	Status               string  `json:"status,omitempty"`
	PricingError         bool    `json:"pricingError,omitempty"`
	// StatusCode is the HTTP status returned to the client. It lets failed
	// requests be audited even when the Proxy sends no error text. The field
	// order must mirror services.UsageRecord (direct struct conversion).
	StatusCode int `json:"statusCode,omitempty"`

	// Media generation metadata used by media-mode pricing rules.
	ImageSize    string `json:"imageSize,omitempty"`
	ImageQuality string `json:"imageQuality,omitempty"`
	ImageCount   int    `json:"imageCount,omitempty"`
	Duration     int    `json:"duration,omitempty"` // video/audio seconds
}

// BatchUsageReport represents a batch of usage records
type BatchUsageReport struct {
	Records  []UsageRecord `json:"records"`
	ServerID string        `json:"serverId"`
}

// UpstreamUsageRecord represents an aggregated upstream key usage entry from Proxy
type UpstreamUsageRecord struct {
	UpstreamKeyID       string `json:"upstreamKeyId"`
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	Date                string `json:"date"` // YYYY-MM-DD
	RequestCount        int    `json:"requestCount"`
	InputTokens         int64  `json:"inputTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	CacheReadTokens     int64  `json:"cacheReadTokens"`
	CacheCreationTokens int64  `json:"cacheCreationTokens"`
	ErrorCount          int    `json:"errorCount"`
	LatencyMs           int    `json:"latencyMs"`
}

// BatchUpstreamUsageReport represents a batch of upstream usage records
type BatchUpstreamUsageReport struct {
	Records []UpstreamUsageRecord `json:"records"`
}

// ProviderHealthRecord represents a single health event from the proxy
type ProviderHealthRecord struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	GroupID      string `json:"groupId"`
	TraceID      string `json:"traceId"`
	StatusCode   int    `json:"statusCode"`
	LatencyMs    int    `json:"latencyMs"`
	ErrorType    string `json:"errorType"`
	ErrorMessage string `json:"errorMessage"`
	ServerID     string `json:"serverId"`
	Timestamp    string `json:"timestamp"`
}

// BatchProviderHealthReport represents a batch of provider health records
type BatchProviderHealthReport struct {
	Records []ProviderHealthRecord `json:"records"`
}

// StartQueueProcessor starts background Redis queue processors for stats and billing
func (r *WebhookRoutes) StartQueueProcessor(ctx context.Context) {
	// Audit stats processor: lightweight, high throughput (legacy audit pipeline)
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							r.logger.Error("Audit stats queue processor panic recovered", zap.Any("panic", rec))
						}
					}()
					r.processStatsQueue()
				}()
			}
		}
	}()

	// Usage stats processor: new primary billing path from proxy-side UsageBuffer
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							r.logger.Error("Usage stats queue processor panic recovered", zap.Any("panic", rec))
						}
					}()
					r.processUsageStatsQueue()
				}()
			}
		}
	}()

	// Billing processor: heavier, processes in user-grouped batches
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							r.logger.Error("Billing queue processor panic recovered", zap.Any("panic", rec))
						}
					}()
					r.processBillingQueue()
				}()
			}
		}
	}()

	r.logger.Info("Webhook queue processors started (audit-stats + usage-stats + billing)")
}

// processUsageStatsQueue pops records from the usage-stats queue (new primary billing path),
// inserts usage_records, and pushes to the billing queue for async deduction.
func (r *WebhookRoutes) processUsageStatsQueue() {
	ctx := context.Background()
	var records []*UsageRecord

	for i := 0; i < 200; i++ {
		data, err := r.db.RedisRPop(ctx, "webhook:usage-stats:queue").Result()
		if err != nil {
			break
		}

		var record UsageRecord
		if err := json.Unmarshal([]byte(data), &record); err != nil {
			r.recordWebhookFailed()
			r.logger.Warn("Failed to unmarshal usage-stats record", zap.Error(err))
			continue
		}

		records = append(records, &record)
	}

	// Batch DB write for all popped records.
	if len(records) > 0 {
		if err := r.processStatsBatch(ctx, records); err != nil {
			r.recordWebhookFailed()
			r.logger.Error("Failed to process usage-stats batch", zap.Error(err), zap.Int("count", len(records)))
			// Re-queue the popped records so they are not silently lost.
			// Records exceeding the retry threshold are moved to a dead-letter queue.
			r.requeueStatsRecords(ctx, "webhook:usage-stats:queue", "webhook:usage-stats:deadletter", records)
			return
		}
	}

	// Push to billing queue for async deduction.
	// Re-serialize from the mutated records so that any TraceID generated by
	// processStatsBatch is carried through — the billing batch UPDATE matches
	// on trace_id, so pushing the original JSON (with empty traceId) would
	// leave charge fields permanently at 0.
	if len(records) > 0 && r.db.Redis != nil {
		pipe := r.db.RedisPipeline()
		for _, rec := range records {
			data, _ := json.Marshal(rec)
			pipe.RPush(ctx, "webhook:billing:queue", data)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			metrics.GetMetrics().WebhookRedisQueueFailed.Add(1)
			r.logger.Error("Failed to push usage-stats to billing queue, billing directly", zap.Error(err))
			// Records are already persisted; bill them synchronously so the
			// usage is still charged instead of being silently dropped.
			r.billRecordsDirectly(ctx, records)
		}
	}
}

// requeueStatsRecords pushes records back onto their origin queue with an incremented
// RetryCount; records that exceed maxStatsRetries are diverted to the dead-letter queue
// for manual inspection. This prevents permanent record loss when processStatsBatch fails.
func (r *WebhookRoutes) requeueStatsRecords(ctx context.Context, queueKey, deadKey string, records []*UsageRecord) {
	if r.db.Redis == nil || len(records) == 0 {
		return
	}
	const maxStatsRetries = 3
	pipe := r.db.RedisPipeline()
	for _, rec := range records {
		if rec.RetryCount >= maxStatsRetries {
			data, _ := json.Marshal(rec)
			pipe.RPush(ctx, deadKey, data)
			r.logger.Warn("usage-stats record exceeded max retries, moved to dead-letter",
				zap.String("trace_id", rec.TraceID),
				zap.Int("retry_count", rec.RetryCount))
			continue
		}
		rec.RetryCount++
		data, _ := json.Marshal(rec)
		pipe.RPush(ctx, queueKey, data)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		metrics.GetMetrics().WebhookRedisQueueFailed.Add(1)
		r.logger.Error("Failed to requeue usage-stats records", zap.Error(err), zap.Int("count", len(records)))
	}
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
