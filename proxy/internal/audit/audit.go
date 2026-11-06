package audit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"github.com/ai-proxy/ai-proxy-go/pkg/telemetry"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// UsageReport - Usage report sent to Portal via webhook
type UsageReport struct {
	TraceID             string  `json:"traceId"`
	ClientIP            string  `json:"clientIp,omitempty"`
	UserAgent           string  `json:"userAgent,omitempty"`
	APIKeyHash          string  `json:"apiKeyHash"`
	Model               string  `json:"model"`
	Provider            string  `json:"provider"`
	Region              string  `json:"region,omitempty"`
	InputTokens         int     `json:"inputTokens"`
	OutputTokens        int     `json:"outputTokens"`
	CachedTokens        int     `json:"cachedTokens"`
	CacheCreationTokens int     `json:"cacheCreationTokens"`
	ReasoningTokens     int     `json:"reasoningTokens"`
	Cost                float64 `json:"cost"`
	LatencyMs           int     `json:"latencyMs"`
	Timestamp           string  `json:"timestamp"`
	KeyID               string  `json:"keyId"`
	UpstreamKeyID       string  `json:"upstreamKeyId"`
	ServerID            string  `json:"serverId"`
	RequestCount        int     `json:"requestCount"`
	StatusCode          int     `json:"statusCode"`
	ActualGroupID       string  `json:"actualGroupId,omitempty"`
	GroupID             string  `json:"groupId,omitempty"`
	IsFallback          bool    `json:"isFallback,omitempty"`
	PricingRatio        float64 `json:"pricingRatio,omitempty"`
	RoutingTrack        string  `json:"routingTrack,omitempty"`
	OriginalTrack       string  `json:"originalTrack,omitempty"`
	Error               string  `json:"error,omitempty"`
	CacheHit            bool    `json:"cacheHit"`
	IsStreaming         bool    `json:"isStreaming,omitempty"`
	Endpoint            string  `json:"endpoint,omitempty"`
}

// BatchUsageReport - Batch usage report
type BatchUsageReport struct {
	Records  []UsageReport `json:"records"`
	ServerID string        `json:"serverId"`
}

// AuditEntry - Simplified audit entry for logging
type AuditEntry struct {
	TraceID        string // Internal request trace id; must match usage_records.trace_id. Empty falls back to a random UUID.
	ClientIP       string // Caller IP as resolved by the gateway
	UserAgent      string // Caller User-Agent (truncated by the logger)
	Timestamp      time.Time
	KeyID          string
	UpstreamKeyID  string // UUID of the upstream key actually used for the request
	UserID         string
	Provider       string
	ChannelID      int
	Model          string
	InputTokens    int
	OutputTokens   int
	CacheHitTokens int
	Cost           float64
	LatencyMs      int64
	StatusCode     int
	IsStream       bool
	RequestType    string
	APIKeyHash     string
	CacheCreation  int
	Reasoning      int
	ActualGroupID  string  // Group ID that was actually used for billing
	Region         string  // Provider region
	GroupID        string  // Key's group ID
	IsFallback     bool    // Whether this request used fallback routing
	PricingRatio   float64 // Pricing ratio at request time
	RoutingTrack   string  // Routing track (bedrock/standard/mantle)
	OriginalTrack  string  // Original track before fallback
	Error          string  // Error message on failure
	CacheHit       bool    // Whether cache was hit
	Endpoint       string  // Request path (e.g. /v1/chat/completions)
}

// Logger - Audit logger with batch webhook reporting
type Logger struct {
	cfg         *config.Config
	logger      *zap.Logger
	client      *http.Client
	batch       []UsageReport
	batchMutex  sync.Mutex
	flushTicker *time.Ticker
	stopChan    chan struct{}
	serverID    string

	// logged remembers recently audited trace ids so the failure-audit
	// middleware does not duplicate rows the handlers already wrote.
	logged *ttlSet
	// anonLimiter bounds failure audits that carry no resolved API key, so
	// unauthenticated scanners cannot flood proxy_request_audits.
	anonLimiter *ipLimiter
}

// NewLogger - Create audit logger
func NewLogger(cfg *config.Config, logger *zap.Logger) (*Logger, error) {
	serverID := cfg.Server.ID
	if serverID == "" {
		serverID = "proxy-" + uuid.New().String()[:8]
	}

	auditLogger := &Logger{
		cfg:    cfg,
		logger: logger,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		batch:       make([]UsageReport, 0, cfg.Audit.BatchSize),
		stopChan:    make(chan struct{}),
		serverID:    serverID,
		logged:      newTTLSet(loggedTraceTTL, loggedTraceMax),
		anonLimiter: newIPLimiter(anonFailuresPerMinute, time.Minute, anonLimiterMaxIPs),
	}

	// Start flush ticker
	auditLogger.flushTicker = time.NewTicker(cfg.Audit.FlushInterval)
	go auditLogger.flushLoop()

	logger.Info("Audit logger initialized",
		zap.String("portal_url", cfg.Portal.URL),
		zap.Int("batch_size", cfg.Audit.BatchSize),
		zap.Duration("flush_interval", cfg.Audit.FlushInterval),
		zap.String("server_id", serverID),
	)

	return auditLogger, nil
}

// Log - Log an audit entry (simplified interface)
func (l *Logger) Log(entry AuditEntry) {
	if !l.cfg.Audit.Enabled {
		return
	}

	// Reuse the request's trace id so audit rows join with usage_records and
	// retention files; a random id is only a fallback for callers without one.
	traceID := entry.TraceID
	if traceID == "" {
		traceID = uuid.New().String()
	}
	l.MarkLogged(traceID)

	// Success paths build entries without a status code; leaving it 0 stores
	// NULL and breaks status-code reporting. An entry with no error is a 200.
	statusCode := entry.StatusCode
	if statusCode == 0 && entry.Error == "" {
		statusCode = http.StatusOK
	}

	report := UsageReport{
		TraceID:             traceID,
		ClientIP:            entry.ClientIP,
		UserAgent:           truncateUA(entry.UserAgent),
		APIKeyHash:          entry.APIKeyHash,
		Model:               entry.Model,
		Provider:            entry.Provider,
		Region:              entry.Region,
		InputTokens:         entry.InputTokens,
		OutputTokens:        entry.OutputTokens,
		CachedTokens:        entry.CacheHitTokens,
		CacheCreationTokens: entry.CacheCreation,
		ReasoningTokens:     entry.Reasoning,
		Cost:                entry.Cost,
		LatencyMs:           int(entry.LatencyMs),
		Timestamp:           entry.Timestamp.Format(time.RFC3339),
		KeyID:               entry.KeyID,
		UpstreamKeyID:       entry.UpstreamKeyID,
		ServerID:            l.serverID,
		RequestCount:        1,
		StatusCode:          statusCode,
		ActualGroupID:       entry.ActualGroupID,
		GroupID:             entry.GroupID,
		IsFallback:          entry.IsFallback,
		PricingRatio:        entry.PricingRatio,
		RoutingTrack:        entry.RoutingTrack,
		OriginalTrack:       entry.OriginalTrack,
		Error:               entry.Error,
		CacheHit:            entry.CacheHit,
		IsStreaming:         entry.IsStream,
		Endpoint:            entry.Endpoint,
	}

	l.batchMutex.Lock()
	l.batch = append(l.batch, report)
	batchLen := len(l.batch)
	l.batchMutex.Unlock()

	// Flush if batch is full
	if batchLen >= l.cfg.Audit.BatchSize {
		go func() { _ = l.Flush() }()
	}
}

// Record - Add usage report to batch
func (l *Logger) Record(report UsageReport) {
	if !l.cfg.Audit.Enabled {
		return
	}

	if report.TraceID == "" {
		report.TraceID = uuid.New().String()
	}
	report.ServerID = l.serverID

	l.batchMutex.Lock()
	l.batch = append(l.batch, report)
	batchLen := len(l.batch)
	l.batchMutex.Unlock()

	// Flush if batch is full
	if batchLen >= l.cfg.Audit.BatchSize {
		go func() { _ = l.Flush() }()
	}
}

// flushLoop - Periodic flush goroutine
func (l *Logger) flushLoop() {
	for {
		select {
		case <-l.flushTicker.C:
			_ = l.Flush()
		case <-l.stopChan:
			_ = l.Flush()
			return
		}
	}
}

// Flush - Flush batch to Portal via webhook
func (l *Logger) Flush() error {
	l.batchMutex.Lock()
	if len(l.batch) == 0 {
		l.batchMutex.Unlock()
		return nil
	}

	batch := l.batch
	l.batch = make([]UsageReport, 0, l.cfg.Audit.BatchSize)
	l.batchMutex.Unlock()

	// Send to Portal webhook
	if err := l.sendBatchToPortal(batch); err != nil {
		l.logger.Error("Failed to send usage batch to portal",
			zap.Error(err),
			zap.Int("batch_size", len(batch)),
		)
		// Put failed records back
		l.batchMutex.Lock()
		l.batch = append(batch, l.batch...)
		l.batchMutex.Unlock()
		return err
	}

	l.logger.Debug("Flushed usage batch to portal",
		zap.Int("count", len(batch)),
	)
	return nil
}

// sendBatchToPortal - Send batch usage report to Portal
func (l *Logger) sendBatchToPortal(batch []UsageReport) error {
	if l.cfg.Portal.URL == "" {
		return fmt.Errorf("portal URL not configured")
	}

	payload := BatchUsageReport{
		Records:  batch,
		ServerID: l.serverID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	url := l.cfg.Portal.URL + "/api/webhook/usage-batch"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Add signature if secret is configured
	if l.cfg.Portal.SyncSecret != "" {
		timestamp := fmt.Sprintf("%d", time.Now().Unix())
		signature := l.computeSignature(body, timestamp)
		req.Header.Set("X-Timestamp", timestamp)
		req.Header.Set("X-Signature", signature)
	}
	telemetry.InjectTraceparent(context.Background(), req)

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	return nil
}

// computeSignature - Compute HMAC signature for webhook
func (l *Logger) computeSignature(body []byte, timestamp string) string {
	h := hmac.New(sha256.New, []byte(l.cfg.Portal.SyncSecret))
	h.Write([]byte(timestamp + "."))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Close - Close audit logger
func (l *Logger) Close() error {
	close(l.stopChan)
	l.flushTicker.Stop()
	return l.Flush()
}

// QueryParams - Query parameters (kept for compatibility)
type QueryParams struct {
	UserID    int
	Model     string
	Provider  string
	StartTime time.Time
	EndTime   time.Time
	Status    string
	CacheHit  *bool
	Limit     int
	Offset    int
}

// AuditStats - Audit statistics (kept for compatibility)
type AuditStats struct {
	TotalRequests      int64   `json:"total_requests"`
	SuccessfulRequests int64   `json:"successful_requests"`
	CacheHitRate       float64 `json:"cache_hit_rate"`
	TotalInputTokens   int64   `json:"total_input_tokens"`
	TotalOutputTokens  int64   `json:"total_output_tokens"`
	TotalCachedTokens  int64   `json:"total_cached_tokens"`
	TotalCost          float64 `json:"total_cost"`
}

// GetStats - Get audit statistics (not implemented for webhook mode)
func (l *Logger) GetStats(ctx context.Context, startTime, endTime time.Time) (*AuditStats, error) {
	// Stats should be queried from Portal, not from Proxy
	return &AuditStats{}, nil
}

// Query - Query audit logs (not implemented for webhook mode)
func (l *Logger) Query(ctx context.Context, params QueryParams) ([]UsageReport, int64, error) {
	// Logs should be queried from Portal, not from Proxy
	return nil, 0, nil
}

// Search - Search audit logs (kept for compatibility)
type AuditQuery struct {
	KeyID    string
	UserID   string
	Provider string
	Model    string
	Start    time.Time
	End      time.Time
	Limit    int
}

// Search - Search audit logs (not implemented for webhook mode)
func (l *Logger) Search(query AuditQuery) ([]UsageReport, error) {
	return nil, nil
}

// RecordRequest - Convenience method to record a request
func (l *Logger) RecordRequest(
	traceID string,
	userID int,
	apiKeyID int,
	model string,
	provider string,
	inputTokens int,
	outputTokens int,
	cachedTokens int,
	totalCost float64,
	latencyMs int,
	status string,
) {
	l.Log(AuditEntry{
		TraceID:        traceID,
		Timestamp:      time.Now(),
		Model:          model,
		Provider:       provider,
		InputTokens:    inputTokens,
		OutputTokens:   outputTokens,
		CacheHitTokens: cachedTokens,
		Cost:           totalCost,
		LatencyMs:      int64(latencyMs),
	})
}
