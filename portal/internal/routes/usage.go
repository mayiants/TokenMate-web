package routes

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/query"
	"github.com/gin-gonic/gin"
)

type UsageRoutes struct {
	db   *db.DB
	auth *middleware.Auth
}

func NewUsageRoutes(db *db.DB, auth *middleware.Auth) *UsageRoutes {
	return &UsageRoutes{db: db, auth: auth}
}

// Register registers user-level usage routes under /usage
func (r *UsageRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("", r.auth.RequireAuth(), r.getUsage)
	rg.GET("/summary", r.auth.RequireAuth(), r.getUsageSummary)
	rg.GET("/recent", r.auth.RequireAuth(), r.getRecentUsage)
	rg.GET("/export", r.auth.RequireAuth(), r.exportUsageCSV)
}

// RegisterAdmin registers admin-level usage routes under /admin/usage
func (r *UsageRoutes) RegisterAdmin(rg *gin.RouterGroup) {
	rg.GET("/upstream", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.getUpstreamKeyStats)
	rg.GET("/upstream-keys", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.getUpstreamKeyStats)
	rg.GET("/users", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.getUserKeyStats)
	rg.GET("/user-keys", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.getUserKeyStats)
	rg.GET("/summary", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.getAdminUsageSummary)
	rg.GET("/chart", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.getUsageChart)
}

func (r *UsageRoutes) getUsage(c *gin.Context) {
	userID := c.GetString("user_id")
	role := c.GetString("role")
	isAdmin := role == "system_admin" || role == "super_admin"

	limit := 100

	query := `SELECT trace_id, model, provider, input_tokens, output_tokens, cached_input_tokens,
	                 total_cost, latency_ms, created_at
	          FROM usage_records`
	args := []interface{}{}

	if !isAdmin {
		query += " WHERE user_id = $1::uuid"
		args = append(args, userID)
	}

	query += " ORDER BY created_at DESC LIMIT $" + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get usage"}})
		return
	}
	defer rows.Close()

	records := []gin.H{}
	for rows.Next() {
		var traceID, model, provider string
		var inputTokens, outputTokens, cachedTokens, latencyMs int
		var totalCost float64
		var createdAt interface{}
		_ = rows.Scan(&traceID, &model, &provider, &inputTokens, &outputTokens, &cachedTokens,
			&totalCost, &latencyMs, &createdAt)
		records = append(records, gin.H{
			"trace_id": traceID, "model": model, "provider": provider,
			"input_tokens": inputTokens, "output_tokens": outputTokens,
			"cached_tokens": cachedTokens, "total_cost": totalCost,
			"latency_ms": latencyMs, "created_at": createdAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"records": records})
}

func (r *UsageRoutes) getUsageSummary(c *gin.Context) {
	userID := c.GetString("user_id")

	var todayRequests int
	var todayTokens int64
	var monthRequests int
	var monthTokens int64

	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*), COALESCE(SUM(input_tokens + output_tokens), 0)
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= CURRENT_DATE`, userID,
	).Scan(&todayRequests, &todayTokens)

	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*), COALESCE(SUM(input_tokens + output_tokens), 0)
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= DATE_TRUNC('month', CURRENT_DATE)`, userID,
	).Scan(&monthRequests, &monthTokens)

	c.JSON(http.StatusOK, gin.H{
		"today": gin.H{"requests": todayRequests, "tokens": todayTokens},
		"month": gin.H{"requests": monthRequests, "tokens": monthTokens},
	})
}

// getRecentUsage returns daily usage for the last N days
func (r *UsageRoutes) getRecentUsage(c *gin.Context) {
	userID := c.GetString("user_id")
	role := c.GetString("role")
	isAdmin := role == "system_admin" || role == "super_admin"

	days := 7
	if d := c.Query("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 && parsed <= 90 {
			days = parsed
		}
	}

	query := `SELECT DATE(created_at) as date,
	                 COUNT(*) as requests,
	                 COALESCE(SUM(input_tokens + output_tokens), 0) as tokens,
	                 COALESCE(SUM(total_cost), 0) as cost
	          FROM usage_records WHERE created_at >= NOW() - INTERVAL '1 day' * $1`
	args := []interface{}{days}

	if !isAdmin {
		query += " AND user_id = $2::uuid"
		args = append(args, userID)
	}

	query += " GROUP BY DATE(created_at) ORDER BY date"

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{}})
		return
	}
	defer rows.Close()

	data := []gin.H{}
	for rows.Next() {
		var date time.Time
		var requests int
		var tokens int64
		var cost float64
		_ = rows.Scan(&date, &requests, &tokens, &cost)
		data = append(data, gin.H{
			"date":     date.Format("2006-01-02"),
			"requests": requests,
			"tokens":   tokens,
			"cost":     cost,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": data})
}

// getUpstreamKeyStats returns usage stats for all upstream keys
func (r *UsageRoutes) getUpstreamKeyStats(c *gin.Context) {
	days := 7
	if d := c.Query("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}
	startDate := time.Now().AddDate(0, 0, -days)

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT
			uk.id::text, uk.provider, uk.key_preview, uk.enabled,
			COALESCE(stats.total_requests, 0) as total_requests,
			COALESCE(stats.total_input_tokens, 0) as total_input_tokens,
			COALESCE(stats.total_output_tokens, 0) as total_output_tokens,
			COALESCE(stats.total_cost, 0) as total_cost,
			COALESCE(stats.avg_latency, 0) as avg_latency,
			stats.last_used
		 FROM upstream_keys uk
		 LEFT JOIN (
		 	SELECT provider, COUNT(*) as total_requests,
		 	       COALESCE(SUM(input_tokens), 0) as total_input_tokens,
		 	       COALESCE(SUM(output_tokens), 0) as total_output_tokens,
		 	       COALESCE(SUM(total_cost), 0) as total_cost,
		 	       COALESCE(AVG(latency_ms), 0) as avg_latency,
		 	       MAX(created_at) as last_used
		 	FROM usage_records
		 	WHERE created_at > $1
		 	GROUP BY provider
		 ) stats ON stats.provider = uk.provider
		 ORDER BY total_requests DESC`,
		startDate,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get upstream key stats"}})
		return
	}
	defer rows.Close()

	stats := []gin.H{}
	for rows.Next() {
		var id, provider, keyPreview string
		var enabled bool
		var totalRequests int
		var totalInputTokens, totalOutputTokens int64
		var totalCost float64
		var avgLatency float64
		var lastUsed interface{}
		_ = rows.Scan(&id, &provider, &keyPreview, &enabled,
			&totalRequests, &totalInputTokens, &totalOutputTokens,
			&totalCost, &avgLatency, &lastUsed)
		stats = append(stats, gin.H{
			"id":                  id,
			"provider":            provider,
			"key_preview":         keyPreview,
			"enabled":             enabled,
			"total_requests":      totalRequests,
			"total_input_tokens":  totalInputTokens,
			"total_output_tokens": totalOutputTokens,
			"total_cost":          totalCost,
			"avg_latency":         avgLatency,
			"last_used":           lastUsed,
		})
	}

	c.JSON(http.StatusOK, gin.H{"stats": stats, "period": fmt.Sprintf("%dd", days)})
}

// getUserKeyStats returns usage stats for all user API keys
func (r *UsageRoutes) getUserKeyStats(c *gin.Context) {
	days := 7
	if d := c.Query("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}
	startDate := time.Now().AddDate(0, 0, -days)

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT 
			ak.id::text, ak.key_hash, ak.key_prefix, ak.name, ak.status,
			ak.access_mode, ak.allowed_models,
			u.name as user_name, u.email as user_email,
			COUNT(ur.id) as total_requests,
			COALESCE(SUM(ur.input_tokens), 0) as total_input_tokens,
			COALESCE(SUM(ur.output_tokens), 0) as total_output_tokens,
			COALESCE(SUM(ur.total_cost), 0) as total_cost,
			MAX(ur.created_at) as last_used
		 FROM api_keys ak
		 JOIN users u ON u.id = ak.user_id
		 LEFT JOIN usage_records ur ON ur.user_api_key_id = ak.id AND ur.created_at > $1
		 GROUP BY ak.id, ak.key_hash, ak.key_prefix, ak.name, ak.status,
		          ak.access_mode, ak.allowed_models, u.name, u.email
		 ORDER BY total_requests DESC`,
		startDate,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get user key stats"}})
		return
	}
	defer rows.Close()

	stats := []gin.H{}
	for rows.Next() {
		var id, keyHash, keyPrefix, name, status, accessMode string
		var allowedModels interface{}
		var userName, userEmail string
		var totalRequests int
		var totalInputTokens, totalOutputTokens int64
		var totalCost float64
		var lastUsed interface{}
		_ = rows.Scan(&id, &keyHash, &keyPrefix, &name, &status, &accessMode, &allowedModels,
			&userName, &userEmail,
			&totalRequests, &totalInputTokens, &totalOutputTokens, &totalCost, &lastUsed)
		stats = append(stats, gin.H{
			"id":                  id,
			"key_hash":            keyHash,
			"key_prefix":          keyPrefix,
			"name":                name,
			"status":              status,
			"access_mode":         accessMode,
			"allowed_models":      allowedModels,
			"user_name":           userName,
			"user_email":          userEmail,
			"total_requests":      totalRequests,
			"total_input_tokens":  totalInputTokens,
			"total_output_tokens": totalOutputTokens,
			"total_cost":          totalCost,
			"last_used":           lastUsed,
		})
	}

	c.JSON(http.StatusOK, gin.H{"stats": stats, "period": fmt.Sprintf("%dd", days)})
}

// getAdminUsageSummary returns overall usage summary for admin dashboard
func (r *UsageRoutes) getAdminUsageSummary(c *gin.Context) {
	days := 7
	if d := c.Query("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}
	startDate := time.Now().AddDate(0, 0, -days)

	// Overall summary
	var totalRequests int
	var totalInputTokens, totalOutputTokens int64
	var totalCost float64
	var activeUserKeys, activeUpstreamKeys int

	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT 
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(total_cost), 0),
			COUNT(DISTINCT user_api_key_id),
			COUNT(DISTINCT upstream_key_id)
		 FROM usage_records 
		 WHERE created_at > $1`,
		startDate,
	).Scan(&totalRequests, &totalInputTokens, &totalOutputTokens, &totalCost, &activeUserKeys, &activeUpstreamKeys)

	// By provider
	providerRows, err := r.db.Query(c.Request.Context(),
		`SELECT provider, COUNT(*) as requests, COALESCE(SUM(total_cost), 0) as cost
		 FROM usage_records 
		 WHERE created_at > $1
		 GROUP BY provider
		 ORDER BY requests DESC`,
		startDate,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get usage summary"}})
		return
	}
	defer providerRows.Close()

	byProvider := []gin.H{}
	for providerRows.Next() {
		var provider string
		var requests int
		var cost float64
		_ = providerRows.Scan(&provider, &requests, &cost)
		byProvider = append(byProvider, gin.H{
			"provider": provider,
			"requests": requests,
			"cost":     cost,
		})
	}

	// By model
	modelRows, err := r.db.Query(c.Request.Context(),
		`SELECT model, COUNT(*) as requests, COALESCE(SUM(total_cost), 0) as cost
		 FROM usage_records 
		 WHERE created_at > $1
		 GROUP BY model
		 ORDER BY requests DESC
		 LIMIT 10`,
		startDate,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get usage summary"}})
		return
	}
	defer modelRows.Close()

	byModel := []gin.H{}
	for modelRows.Next() {
		var model string
		var requests int
		var cost float64
		_ = modelRows.Scan(&model, &requests, &cost)
		byModel = append(byModel, gin.H{
			"model":    model,
			"requests": requests,
			"cost":     cost,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"summary": gin.H{
			"total_requests":       totalRequests,
			"total_input_tokens":   totalInputTokens,
			"total_output_tokens":  totalOutputTokens,
			"total_cost":           totalCost,
			"active_user_keys":     activeUserKeys,
			"active_upstream_keys": activeUpstreamKeys,
		},
		"byProvider": byProvider,
		"byModel":    byModel,
	})
}

// getUsageChart returns per-bucket requests, tokens and spend for the admin dashboard trend.
// Query param ?range=1d|7d|15d|30d|quarter (defaults to 7d).
func (r *UsageRoutes) getUsageChart(c *gin.Context) {
	rangeKey := c.DefaultQuery("range", "7d")
	if _, ok := query.AllowedRanges[rangeKey]; !ok {
		rangeKey = "7d"
	}

	points, err := query.GetAdminUsageTrend(c.Request.Context(), r.db, rangeKey)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"range": rangeKey, "labels": []string{}, "requests": []int{}, "tokens": []int64{}, "cost": []int64{}})
		return
	}

	labels := make([]string, len(points))
	requests := make([]int, len(points))
	tokens := make([]int64, len(points))
	cost := make([]int64, len(points))
	for i, p := range points {
		if p.Time != "" {
			labels[i] = p.Time
		} else if rangeKey == "quarter" && len(p.Date) >= 7 {
			labels[i] = p.Date[:7]
		} else if len(p.Date) >= 10 {
			labels[i] = p.Date[5:10]
		} else {
			labels[i] = p.Date
		}
		requests[i] = p.Requests
		tokens[i] = p.Tokens
		cost[i] = p.Cost
	}

	c.JSON(http.StatusOK, gin.H{"range": rangeKey, "labels": labels, "requests": requests, "tokens": tokens, "cost": cost})
}

// exportUsageCSV exports usage records as CSV
func (r *UsageRoutes) exportUsageCSV(c *gin.Context) {
	userID := c.GetString("user_id")
	role := c.GetString("role")
	isAdmin := role == "system_admin" || role == "super_admin"

	// Parse date range
	startDate := time.Now().AddDate(0, 0, -30)
	if start := c.Query("start"); start != "" {
		if parsed, err := time.Parse("2006-01-02", start); err == nil {
			startDate = parsed
		}
	}
	endDate := time.Now()
	if end := c.Query("end"); end != "" {
		if parsed, err := time.Parse("2006-01-02", end); err == nil {
			endDate = parsed.Add(24 * time.Hour)
		}
	}

	// Provider/region are upstream-infrastructure details. Only system/super
	// admins may export them; regular users get a provider-agnostic export.
	selCols := "trace_id, model, "
	if isAdmin {
		selCols += "provider, region, "
	}
	query := `SELECT ` + selCols + `input_tokens, output_tokens, 
	                 cached_input_tokens, effective_input_tokens, reasoning_tokens,
	                 total_cost, latency_ms, cache_hit, error, created_at
	          FROM usage_records WHERE created_at >= $1 AND created_at < $2`
	args := []interface{}{startDate, endDate}

	if !isAdmin {
		query += " AND user_id = $3::uuid"
		args = append(args, userID)
	}

	query += " ORDER BY created_at DESC LIMIT 10000"

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export usage"}})
		return
	}
	defer rows.Close()

	// Build CSV
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=usage-records-"+time.Now().UTC().Format("200601021504")+".csv")

	// Write header (provider/region only for admins)
	providerRegionHdr := ""
	if isAdmin {
		providerRegionHdr = T("csv.header.provider", c) + "," + T("csv.header.region", c) + ","
	}
	_, _ = c.Writer.WriteString(T("csv.header.trace_id", c) + "," + T("csv.header.model", c) + "," + providerRegionHdr + T("csv.header.input_tokens", c) + "," + T("csv.header.output_tokens", c) + "," + T("csv.header.cached_input_tokens", c) + "," + T("csv.header.effective_tokens", c) + "," + T("csv.header.reasoning_tokens", c) + "," + T("csv.header.cost", c) + "," + T("csv.header.latency_ms", c) + "," + T("csv.header.cache_hit", c) + "," + T("csv.header.error", c) + "," + T("csv.header.created_at", c) + "\n")

	for rows.Next() {
		var traceID, model, provider, region string
		var inputTokens, outputTokens, cachedTokens, effectiveTokens, reasoningTokens, latencyMs int
		var totalCost float64
		var cacheHit bool
		var errorStr *string
		var createdAt time.Time

		var scanErr error
		if isAdmin {
			scanErr = rows.Scan(&traceID, &model, &provider, &region, &inputTokens, &outputTokens,
				&cachedTokens, &effectiveTokens, &reasoningTokens,
				&totalCost, &latencyMs, &cacheHit, &errorStr, &createdAt)
		} else {
			scanErr = rows.Scan(&traceID, &model, &inputTokens, &outputTokens,
				&cachedTokens, &effectiveTokens, &reasoningTokens,
				&totalCost, &latencyMs, &cacheHit, &errorStr, &createdAt)
		}
		if scanErr != nil {
			continue
		}

		errorVal := ""
		if errorStr != nil {
			errorVal = *errorStr
		}

		// Write CSV row (provider/region only for admins)
		if isAdmin {
			_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%d,%d,%d,%d,%d,%.6f,%d,%t,%s,%s\n",
				traceID, model, provider, region,
				inputTokens, outputTokens, cachedTokens, effectiveTokens, reasoningTokens,
				totalCost, latencyMs, cacheHit, errorVal, createdAt.Format(time.RFC3339))
		} else {
			_, _ = fmt.Fprintf(c.Writer, "%s,%s,%d,%d,%d,%d,%d,%.6f,%d,%t,%s,%s\n",
				traceID, model,
				inputTokens, outputTokens, cachedTokens, effectiveTokens, reasoningTokens,
				totalCost, latencyMs, cacheHit, errorVal, createdAt.Format(time.RFC3339))
		}
	}
}
