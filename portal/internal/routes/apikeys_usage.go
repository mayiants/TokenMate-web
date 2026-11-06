package routes

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (r *APIKeyRoutes) getKeyUsage(c *gin.Context) {
	userID := c.GetString("user_id")
	keyID := c.Param("id")

	// Verify key belongs to user
	var ownerID string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT user_id::text FROM api_keys WHERE id = $1::uuid AND deleted_at IS NULL`,
		keyID,
	).Scan(&ownerID)
	if err != nil || ownerID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}

	// Get usage records (last 30 days) for display — capped at 100 rows.
	rows, err := r.db.Query(c.Request.Context(), `
		SELECT model, provider, input_tokens, output_tokens, total_cost, latency_ms, created_at
		FROM usage_records
		WHERE user_api_key_id = $1::uuid AND created_at > NOW() - INTERVAL '30 days'
		ORDER BY created_at DESC LIMIT 100`,
		keyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load usage data"}})
		return
	}
	defer rows.Close()

	records := []gin.H{}
	for rows.Next() {
		var model, provider string
		var inputTokens, outputTokens int
		// NOTE: total_cost is in micro-USD (1 USD = 1,000,000 micro-USD).
		var cost float64
		var latencyMs *int
		var createdAt time.Time
		_ = rows.Scan(&model, &provider, &inputTokens, &outputTokens, &cost, &latencyMs, &createdAt)

		records = append(records, gin.H{
			"model":         model,
			"provider":      provider,
			"input_tokens":  inputTokens,
			"inputTokens":   inputTokens,
			"output_tokens": outputTokens,
			"outputTokens":  outputTokens,
			"total_cost":    cost,
			"totalCost":     cost,
			"latency_ms":    latencyMs,
			"latencyMs":     latencyMs,
			"created_at":    createdAt,
			"createdAt":     createdAt,
		})
	}

	// Aggregate stats over the full 30-day window with a dedicated query so
	// that the result is correct even when more than 100 records exist.
	// (Accumulating the capped record set above would systematically undercount.)
	var totalInput, totalOutput int64
	// NOTE: total_cost is in micro-USD (1 USD = 1,000,000 micro-USD).
	var totalCost float64
	var totalLatency int64
	var count int
	_ = r.db.QueryRow(c.Request.Context(), `
		SELECT COUNT(*),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(total_cost), 0),
		       COALESCE(SUM(COALESCE(latency_ms, 0)), 0)
		FROM usage_records
		WHERE user_api_key_id = $1::uuid AND created_at > NOW() - INTERVAL '30 days'`,
		keyID).Scan(&count, &totalInput, &totalOutput, &totalCost, &totalLatency)

	avgLatency := 0
	if count > 0 {
		avgLatency = int(totalLatency / int64(count))
	}

	c.JSON(http.StatusOK, gin.H{
		"records": records,
		"stats": gin.H{
			"total_requests":      count,
			"totalRequests":       count,
			"total_input_tokens":  totalInput,
			"totalInputTokens":    totalInput,
			"total_output_tokens": totalOutput,
			"totalOutputTokens":   totalOutput,
			"total_cost":          totalCost,
			"totalCost":           totalCost,
			"avg_latency":         avgLatency,
			"avgLatency":          avgLatency,
		},
	})
}
