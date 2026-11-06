package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func (r *WebhookRoutes) handleKeyPoolStatus(c *gin.Context) {
	r.recordWebhookReceived()

	var statuses []struct {
		Provider   string `json:"provider"`
		KeyID      string `json:"keyId"`
		KeyPreview string `json:"keyPreview"`
		RPM        int    `json:"rpm"`
		TPM        int    `json:"tpm"`
		Status     string `json:"status"`
	}
	if err := c.ShouldBindJSON(&statuses); err != nil {
		r.recordWebhookFailed()
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	for _, s := range statuses {
		if err := r.db.Exec(c.Request.Context(),
			`INSERT INTO key_pool_status (provider, key_id, key_preview, rpm, tpm, status, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, NOW())
			 ON CONFLICT (provider, key_id) DO UPDATE SET
			   key_preview = EXCLUDED.key_preview,
			   rpm = EXCLUDED.rpm,
			   tpm = EXCLUDED.tpm,
			   status = EXCLUDED.status,
			   updated_at = EXCLUDED.updated_at`,
			s.Provider, s.KeyID, s.KeyPreview, s.RPM, s.TPM, s.Status); err != nil {
			r.logger.Error("Failed to upsert key pool status", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (r *WebhookRoutes) handleHeartbeat(c *gin.Context) {
	r.recordWebhookReceived()

	var data struct {
		ServerID      string      `json:"serverId"`
		Uptime        int         `json:"uptime"`
		TotalRequests int         `json:"totalRequests"`
		Providers     interface{} `json:"providers"`
	}
	if err := c.ShouldBindJSON(&data); err != nil {
		r.recordWebhookFailed()
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	providersJSON, _ := json.Marshal(data.Providers)

	if err := r.db.Exec(c.Request.Context(),
		`INSERT INTO proxy_heartbeats (server_id, uptime, total_requests, providers, created_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (server_id) DO UPDATE SET
		   uptime = EXCLUDED.uptime,
		   total_requests = EXCLUDED.total_requests,
		   providers = EXCLUDED.providers,
		   created_at = EXCLUDED.created_at`,
		data.ServerID, data.Uptime, data.TotalRequests, string(providersJSON)); err != nil {
		r.logger.Error("Failed to upsert heartbeat", zap.Error(err))
	}

	// Also update proxy_instances
	if err := r.db.Exec(c.Request.Context(),
		`INSERT INTO proxy_instances (proxy_id, last_heartbeat_at, status)
		 VALUES ($1, NOW(), 'active')
		 ON CONFLICT (proxy_id) DO UPDATE SET
		   last_heartbeat_at = EXCLUDED.last_heartbeat_at,
		   status = 'active'`,
		data.ServerID); err != nil {
		r.logger.Error("Failed to upsert proxy instance", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (r *WebhookRoutes) handleProviderHealthBatch(c *gin.Context) {
	r.recordWebhookReceived()

	var batch BatchProviderHealthReport
	if err := c.ShouldBindJSON(&batch); err != nil {
		r.recordWebhookFailed()
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	ctx := c.Request.Context()
	updated := 0
	for i := range batch.Records {
		if err := r.upsertProviderHealth(ctx, &batch.Records[i]); err != nil {
			r.recordWebhookFailed()
			r.logger.Warn("Failed to upsert provider health",
				zap.Error(err),
				zap.String("provider", batch.Records[i].Provider),
			)
		} else {
			updated++
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "updated": updated, "count": len(batch.Records)})
}

// healthCounters holds per-bucket counters derived from a single record.
type healthCounters struct {
	requestCount    int
	successCount    int
	failureCount    int
	timeout         int
	rateLimit       int
	err5xx          int
	err4xx          int
	authErr         int
	networkErr      int
	respTimeout     int
	contentMismatch int
	invalidRequest  int
	latencyMs       int
}

func countersFromRecord(record *ProviderHealthRecord) healthCounters {
	c := healthCounters{latencyMs: record.LatencyMs}
	if record.ErrorType == "" {
		c.successCount = 1
	} else {
		c.failureCount = 1
		switch record.ErrorType {
		case "timeout":
			c.timeout = 1
		case "rate_limit":
			c.rateLimit = 1
		case "error_5xx":
			c.err5xx = 1
		case "error_4xx":
			c.err4xx = 1
		case "auth_error":
			c.authErr = 1
		case "network_error":
			c.networkErr = 1
		case "response_timeout":
			c.respTimeout = 1
		case "content_mismatch":
			c.contentMismatch = 1
		case "invalid_request":
			c.invalidRequest = 1
		}
	}
	c.requestCount = c.successCount + c.failureCount
	return c
}

func (r *WebhookRoutes) upsertProviderHealth(ctx context.Context, record *ProviderHealthRecord) error {
	if record.Provider == "" {
		return fmt.Errorf("missing provider")
	}

	// Floor timestamp to 5-minute bucket
	bucket := time.Now().Truncate(5 * time.Minute)
	c := countersFromRecord(record)

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.upsertProviderHealthStats(ctx, tx, record.Provider, bucket, c); err != nil {
		return fmt.Errorf("upsert provider stats: %w", err)
	}

	if record.Model != "" {
		if err := r.upsertProviderHealthStatsModel(ctx, tx, record.Provider, record.Model, bucket, c); err != nil {
			return fmt.Errorf("upsert provider model stats: %w", err)
		}

		if record.GroupID != "" {
			if err := r.upsertModelGroupHealthStats(ctx, tx, record.Model, record.GroupID, bucket, c); err != nil {
				return fmt.Errorf("upsert model group stats: %w", err)
			}
		}
	}

	return tx.Commit(ctx)
}

func (r *WebhookRoutes) upsertProviderHealthStats(ctx context.Context, tx pgx.Tx, provider string, bucket time.Time, c healthCounters) error {
	return r.upsertHealthStats(ctx, tx, "provider_health_stats",
		[]string{"provider"}, []interface{}{provider},
		bucket, c)
}

func (r *WebhookRoutes) upsertProviderHealthStatsModel(ctx context.Context, tx pgx.Tx, provider, model string, bucket time.Time, c healthCounters) error {
	return r.upsertHealthStats(ctx, tx, "provider_health_stats_model",
		[]string{"provider", "model"}, []interface{}{provider, model},
		bucket, c)
}

func (r *WebhookRoutes) upsertModelGroupHealthStats(ctx context.Context, tx pgx.Tx, model, groupID string, bucket time.Time, c healthCounters) error {
	return r.upsertHealthStats(ctx, tx, "model_group_health_stats",
		[]string{"model", "group_id"}, []interface{}{model, groupID},
		bucket, c)
}

func computeAvailability(c healthCounters) float64 {
	if c.requestCount == 0 {
		return -1.0
	}
	return float64(c.successCount) * 100.0 / float64(c.requestCount)
}

// minHealthSampleSize is the minimum number of requests in a bucket before
// its error rate / latency is trusted enough to judge health. Smaller
// samples keep the previous status (or 'unknown' for new buckets) so a
// single failure out of two requests cannot flag a provider unhealthy.
const minHealthSampleSize = 10

func computeStatus(c healthCounters) string {
	// Exclude client/credential errors (auth_error + invalid_request +
	// error_4xx) from both numerator and denominator: a 401/403 is a key
	// problem and a 400/4xx is the caller's bad request — neither means the
	// model is down. The raw counts are still stored for auditing.
	excluded := c.authErr + c.invalidRequest + c.err4xx
	effective := c.requestCount - excluded
	if effective < minHealthSampleSize {
		return "unknown"
	}
	realFailures := c.failureCount - excluded
	if realFailures < 0 {
		realFailures = 0
	}
	errorRate := float64(realFailures) / float64(effective)
	avgLatency := 0
	if c.requestCount > 0 {
		avgLatency = c.latencyMs / c.requestCount
	}
	// Error rate is the only signal that can mark a model "unhealthy". A model
	// that succeeds but is slow is "degraded" at worst — never "unhealthy" on
	// latency alone (matches query.DeriveInternalStatus).
	switch {
	case errorRate >= 0.20:
		return "unhealthy"
	case errorRate >= 0.05:
		return "degraded"
	case avgLatency < 15000:
		return "healthy"
	default:
		return "degraded"
	}
}

func (r *WebhookRoutes) upsertHealthStats(ctx context.Context, tx pgx.Tx, table string, keyCols []string, keyValues []interface{}, bucket time.Time, c healthCounters) error {
	if len(keyCols) != len(keyValues) {
		return fmt.Errorf("key column/value mismatch")
	}

	conflictList := strings.Join(append(keyCols, "bucket"), ", ")

	// Build column and value lists. After key columns come bucket and counters.
	cols := append([]string{}, keyCols...)
	cols = append(cols, "bucket", "request_count", "success_count", "failure_count",
		"timeout_count", "rate_limit_count", "error_5xx_count", "error_4xx_count",
		"auth_error_count", "network_error_count", "response_timeout_count",
		"content_mismatch_count", "invalid_request_count",
		"total_latency_ms", "avg_latency_ms", "slow_latency_ms", "availability", "error_rate", "status")

	availability := computeAvailability(c)
	errorRate := 0.0
	if c.requestCount > 0 {
		errorRate = float64(c.failureCount) / float64(c.requestCount)
	}
	status := computeStatus(c)

	values := append(keyValues, bucket,
		c.requestCount, c.successCount, c.failureCount,
		c.timeout, c.rateLimit, c.err5xx, c.err4xx,
		c.authErr, c.networkErr, c.respTimeout, c.contentMismatch, c.invalidRequest,
		int64(c.latencyMs), c.latencyMs, 5000, availability, errorRate, status)

	placeholders := make([]string, len(values))
	for i := range placeholders {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	var setParts []string
	for _, col := range []string{"request_count", "success_count", "failure_count",
		"timeout_count", "rate_limit_count", "error_5xx_count", "error_4xx_count",
		"auth_error_count", "network_error_count", "response_timeout_count",
		"content_mismatch_count", "invalid_request_count", "total_latency_ms"} {
		setParts = append(setParts, fmt.Sprintf("%s = %s.%s + EXCLUDED.%s", col, table, col, col))
	}
	// Excluded = auth_error + invalid_request + error_4xx (client/credential,
	// not model). Effective sample and real failures subtract these; error_rate
	// stored stays the raw rate (the health VERDICT uses the excluded rate).
	excludedExpr := "((" + table + ".auth_error_count + EXCLUDED.auth_error_count) + (" + table + ".invalid_request_count + EXCLUDED.invalid_request_count) + (" + table + ".error_4xx_count + EXCLUDED.error_4xx_count))"
	effectiveExpr := "GREATEST(1, (" + table + ".request_count + EXCLUDED.request_count) - " + excludedExpr + ")"
	realFailExpr := "GREATEST(0, (" + table + ".failure_count + EXCLUDED.failure_count) - " + excludedExpr + ")"
	avgLatExpr := "(" + table + ".total_latency_ms + EXCLUDED.total_latency_ms) / GREATEST(1, " + table + ".request_count + EXCLUDED.request_count)"
	setParts = append(setParts,
		"avg_latency_ms = "+avgLatExpr,
		"error_rate = ("+table+".failure_count + EXCLUDED.failure_count)::decimal / GREATEST(1, "+table+".request_count + EXCLUDED.request_count)",
		"availability = ("+table+".success_count + EXCLUDED.success_count)::decimal * 100.0 / GREATEST(1, "+table+".request_count + EXCLUDED.request_count)",
		"status = CASE "+
			// Not enough model-attributable traffic -> keep previous status.
			"WHEN ("+table+".request_count + EXCLUDED.request_count) - "+excludedExpr+" < 10 THEN "+table+".status "+
			// Error rate is the only thing that makes a model 'unhealthy'.
			"WHEN "+realFailExpr+"::decimal / "+effectiveExpr+" >= 0.20 THEN 'unhealthy' "+
			// Acceptable error rate but elevated -> degraded.
			"WHEN "+realFailExpr+"::decimal / "+effectiveExpr+" >= 0.05 THEN 'degraded' "+
			// Healthy error rate: latency only decides healthy vs degraded,
			// never unhealthy (a slow but succeeding model is not down).
			"WHEN "+avgLatExpr+" < 15000 THEN 'healthy' "+
			"ELSE 'degraded' END",
		"updated_at = NOW()",
	)

	sql := fmt.Sprintf(`
		INSERT INTO %s (%s)
		VALUES (%s)
		ON CONFLICT (%s) DO UPDATE SET
			%s
	`, table, strings.Join(cols, ", "), strings.Join(placeholders, ", "), conflictList, strings.Join(setParts, ",\n\t\t\t"))

	_, err := tx.Exec(ctx, sql, values...)
	return err
}
