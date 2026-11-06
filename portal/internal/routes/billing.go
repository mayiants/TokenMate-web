package routes

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/query"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/internal/storage"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// BillingRoutes handles billing and balance operations
type BillingRoutes struct {
	db      *db.DB
	auth    *middleware.Auth
	storage storage.Provider
}

// NewBillingRoutes creates a new billing routes handler
func NewBillingRoutes(db *db.DB, auth *middleware.Auth, storageProvider storage.Provider) *BillingRoutes {
	return &BillingRoutes{db: db, auth: auth, storage: storageProvider}
}

// Register registers all billing routes
func (r *BillingRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("/balance", r.auth.RequireAuth(), r.getBalance)
	rg.GET("/usage-details", r.auth.RequireAuth(), r.getUsageDetails)
	rg.GET("/usage-summary", r.auth.RequireAuth(), r.getUsageSummary)
	rg.GET("/usage-records", r.auth.RequireAuth(), r.getUsageRecords)
	rg.GET("/usage-by-key", r.auth.RequireAuth(), r.getUsageByKey)
	rg.GET("/usage-by-model", r.auth.RequireAuth(), r.getUsageByModel)
	rg.GET("/usage-trend", r.auth.RequireAuth(), r.getUsageTrend)
	// Billing details endpoints
	rg.GET("/realtime", r.auth.RequireAuth(), r.getRealtimeDetails)
	rg.GET("/monthly", r.auth.RequireAuth(), r.getMonthlyDetails)
	rg.GET("/requests", r.auth.RequireAuth(), r.getRequestDetails)
	rg.GET("/recharge-records", r.auth.RequireAuth(), r.getRechargeRecords)
	rg.GET("/balance-transactions", r.auth.RequireAuth(), r.getBalanceTransactions)
	rg.GET("/orders/:id/receipt", r.auth.RequireAuth(), r.downloadReceipt)
	rg.GET("/recharge-records/export", r.auth.RequireAuth(), r.exportRechargeRecordsCSV)
	rg.GET("/realtime/export", r.auth.RequireAuth(), r.exportRealtimeCSV)
	rg.GET("/monthly/export", r.auth.RequireAuth(), r.exportMonthlyCSV)
	rg.GET("/requests/export", r.auth.RequireAuth(), r.exportRequestsCSV)
}

func (r *BillingRoutes) getBalance(c *gin.Context) {
	userID := c.GetString("user_id")

	var balance, giftBalance int64
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(balance, 0), COALESCE(gift_balance, 0)
		 FROM users WHERE id = $1 AND deleted_at IS NULL`,
		userID,
	).Scan(&balance, &giftBalance)
	if err != nil {
		// Return zero balance instead of 404 for better UX
		c.JSON(http.StatusOK, gin.H{
			"balance_usd":         QuotaToUSD(0),
			"balance_quota":       0,
			"gift_balance_usd":    QuotaToUSD(0),
			"gift_balance_quota":  0,
			"total_balance_usd":   QuotaToUSD(0),
			"total_balance_quota": 0,
		})
		return
	}

	totalBalance := balance + giftBalance
	c.JSON(http.StatusOK, gin.H{
		"balance_usd":         QuotaToUSD(balance),
		"balance_quota":       balance,
		"gift_balance_usd":    QuotaToUSD(giftBalance),
		"gift_balance_quota":  giftBalance,
		"total_balance_usd":   QuotaToUSD(totalBalance),
		"total_balance_quota": totalBalance,
	})
}

func (r *BillingRoutes) getUsageDetails(c *gin.Context) {
	userID := c.GetString("user_id")

	limit := 100
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 200 {
		limit = 200
	}
	if limit < 1 {
		limit = 20
	}

	// Personal usage endpoints always scope to the current user, even for admins.
	filter := query.UsageRecordFilter{Limit: limit}
	records, _, err := query.GetUsageRecords(c.Request.Context(), r.db, userID, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get usage details"}})
		return
	}

	result := []gin.H{}
	for _, rec := range records {
		result = append(result, gin.H{
			"trace_id":       rec.TraceID,
			"model":          rec.Model,
			"provider":       rec.Provider,
			"input_tokens":   rec.InputTokens,
			"output_tokens":  rec.OutputTokens,
			"cached_tokens":  rec.CachedTokens,
			"request_count":  rec.Requests,
			"total_cost":     QuotaToUSDFloat(rec.Cost),
			"total_cost_usd": QuotaToUSD(rec.Cost),
			"latency_ms":     rec.LatencyMs,
			"created_at":     rec.CreatedAt,
			"endpoint":       rec.Endpoint,
			"is_streaming":   rec.IsStreaming,
			"status":         rec.Status,
			"group_id":       rec.GroupID,
			"group_name":     rec.GroupName,
			"api_key_name":   rec.APIKeyName,
			"key_prefix":     rec.KeyPrefix,
		})
	}

	c.JSON(http.StatusOK, gin.H{"records": result})
}

func (r *BillingRoutes) getUsageSummary(c *gin.Context) {
	userID := c.GetString("user_id")

	days := 7 // default
	if d := c.Query("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}

	dailyAsc, totals, err := query.GetUsageSummary(c.Request.Context(), r.db, userID, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get summary"}})
		return
	}

	// Preserve the original descending-by-date response order.
	daily := make([]gin.H, 0, len(dailyAsc))
	for i := len(dailyAsc) - 1; i >= 0; i-- {
		d := dailyAsc[i]
		daily = append(daily, gin.H{
			"date":          d.Date.Format("2006-01-02"),
			"requests":      d.Requests,
			"tokens":        d.Tokens,
			"cost":          d.Cost,
			"cost_usd":      QuotaToUSD(d.Cost),
			"input_tokens":  d.InputTokens,
			"output_tokens": d.OutputTokens,
			"cached_tokens": d.CachedTokens,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"daily":          daily,
		"total_requests": totals.Requests,
		"total_tokens":   totals.Tokens,
		"total_cost":     totals.Cost,
		"total_cost_usd": QuotaToUSD(totals.Cost),
		"period_days":    days,
	})
}

func (r *BillingRoutes) getUsageRecords(c *gin.Context) {
	userID := c.GetString("user_id")

	// Parse pagination and filters
	page := 1
	pageSize := 20
	if p := c.Query("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if ps := c.Query("page_size"); ps != "" {
		if parsed, err := strconv.Atoi(ps); err == nil && parsed > 0 {
			pageSize = parsed
		}
	}
	if pageSize > 200 {
		pageSize = 200
	}
	if pageSize < 1 {
		pageSize = 20
	}

	var startDate, endDate string
	if sd := c.Query("start_date"); sd != "" {
		startDate = sd
	}
	if ed := c.Query("end_date"); ed != "" {
		endDate = ed + " 23:59:59"
	}

	// Personal usage endpoints always scope to the current user, even for admins.
	filter := query.UsageRecordFilter{
		StartDate: startDate,
		EndDate:   endDate,
		APIKeyID:  c.Query("api_key_id"),
		Model:     c.Query("model"),
		GroupID:   c.Query("group_id"),
		Status:    c.Query("status"),
		Page:      page,
		PageSize:  pageSize,
	}
	if s := c.Query("is_streaming"); s != "" {
		b := s == "true" || s == "1"
		filter.IsStreaming = &b
	}

	records, total, err := query.GetUsageRecords(c.Request.Context(), r.db, userID, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get records"}})
		return
	}

	result := []gin.H{}
	for _, rec := range records {
		result = append(result, gin.H{
			"trace_id":       rec.TraceID,
			"model":          rec.Model,
			"provider":       rec.Provider,
			"input_tokens":   rec.InputTokens,
			"output_tokens":  rec.OutputTokens,
			"cached_tokens":  rec.CachedTokens,
			"request_count":  rec.Requests,
			"total_cost":     QuotaToUSDFloat(rec.Cost),
			"total_cost_usd": QuotaToUSD(rec.Cost),
			"latency_ms":     rec.LatencyMs,
			"created_at":     rec.CreatedAt,
			"endpoint":       rec.Endpoint,
			"is_streaming":   rec.IsStreaming,
			"status":         rec.Status,
			"group_id":       rec.GroupID,
			"group_name":     rec.GroupName,
			"api_key_name":   rec.APIKeyName,
			"key_prefix":     rec.KeyPrefix,
			"client_ip":      rec.ClientIP,
			"user_agent":     rec.UserAgent,
			"agent_tools":    rec.AgentTools,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"records":     result,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": (total + pageSize - 1) / pageSize,
	})
}

func (r *BillingRoutes) getUsageByKey(c *gin.Context) {
	userID := c.GetString("user_id")
	rangeKey := c.DefaultQuery("range", "7d")

	rows, err := query.GetUsageDistributionRange(c.Request.Context(), r.db, userID, rangeKey, "api_key")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get usage by key"}})
		return
	}

	results := []gin.H{}
	for _, row := range rows {
		results = append(results, gin.H{
			"api_key_id": row.KeyID,
			"key_prefix": row.KeyPrefix,
			"key_name":   row.KeyName,
			"requests":   row.Requests,
			"tokens":     row.Tokens,
			// Raw micro-USD quota — the frontend Currency.format expects
			// quota units, consistent with /api/billing/usage-by-model.
			"cost":           row.Cost,
			"cost_usd":       QuotaToUSD(row.Cost),
			"avg_latency_ms": row.AvgLatencyMs,
			"avg_tps":        row.AvgTps,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": results})
}

func (r *BillingRoutes) getUsageByModel(c *gin.Context) {
	userID := c.GetString("user_id")
	rangeKey := c.DefaultQuery("range", "7d")

	rows, err := query.GetUsageDistributionRange(c.Request.Context(), r.db, userID, rangeKey, "model")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get usage by model"}})
		return
	}

	results := []gin.H{}
	for _, row := range rows {
		results = append(results, gin.H{
			"model":          row.Name,
			"requests":       row.Requests,
			"tokens":         row.Tokens,
			"cost":           row.Cost,
			"avg_latency_ms": row.AvgLatencyMs,
			"avg_tps":        row.AvgTps,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": results})
}

func (r *BillingRoutes) getUsageTrend(c *gin.Context) {
	userID := c.GetString("user_id")
	rangeKey := c.DefaultQuery("range", "7d")

	points, err := query.GetUserUsageTrend(c.Request.Context(), r.db, userID, rangeKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get usage trend"}})
		return
	}

	daily := make([]gin.H, 0, len(points))
	for _, p := range points {
		daily = append(daily, gin.H{
			"date":          p.Date,
			"time":          p.Time,
			"requests":      p.Requests,
			"tokens":        p.Tokens,
			"input_tokens":  p.InputTokens,
			"output_tokens": p.OutputTokens,
			"cached_tokens": p.CachedTokens,
			"cost":          p.Cost,
			"cost_usd":      QuotaToUSD(p.Cost),
		})
	}

	c.JSON(http.StatusOK, gin.H{"daily": daily, "range": rangeKey})
}

// getRealtimeDetails returns realtime billing details grouped by product
func (r *BillingRoutes) getRealtimeDetails(c *gin.Context) {
	userID := c.GetString("user_id")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT
			CASE
				WHEN model LIKE '%image%' OR model LIKE '%dall%' OR model LIKE '%flux%' OR model LIKE '%midjourney%' OR model LIKE '%sd-%' THEN 'image'
				WHEN model LIKE '%video%' OR model LIKE '%sora%' OR model LIKE '%runway%' THEN 'video'
				WHEN model LIKE '%embedding%' THEN 'embedding'
				WHEN model LIKE '%tts%' OR model LIKE '%audio%' OR model LIKE '%whisper%' THEN 'audio'
				ELSE 'completion'
			END as product_name,
			'Model Inference' as product_type,
			COALESCE(SUM(total_cost), 0) as total_cost,
			COALESCE(SUM(gift_deduction), 0) as gift_deduction,
			COALESCE(SUM(recharge_deduction), 0) as recharge_deduction,
			COALESCE(SUM(credit_deduction), 0) as credit_deduction,
			COALESCE(SUM(subscription_deduction), 0) as subscription_deduction
		 FROM usage_records
		 WHERE user_id = $1::uuid
		   AND created_at >= NOW() - INTERVAL '24 hours'
		 GROUP BY product_name
		 ORDER BY total_cost DESC`,
		userID,
	)
	if err != nil {
		zap.L().Error("Failed to query realtime details", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get realtime details"}})
		return
	}
	defer rows.Close()

	records := []gin.H{}
	for rows.Next() {
		var productName, productType string
		var totalCost, giftDeduction, rechargeDeduction, creditDeduction, subscriptionDeduction int64
		if err := rows.Scan(&productName, &productType, &totalCost, &giftDeduction, &rechargeDeduction, &creditDeduction, &subscriptionDeduction); err != nil {
			zap.L().Error("Failed to scan realtime details row", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get realtime details"}})
			return
		}
		records = append(records, gin.H{
			"product_name":           productName,
			"product_type":           productType,
			"total_cost":             totalCost,
			"gift_deduction":         giftDeduction,
			"recharge_deduction":     rechargeDeduction,
			"credit_deduction":       creditDeduction,
			"subscription_deduction": subscriptionDeduction,
		})
	}

	c.JSON(http.StatusOK, gin.H{"records": records})
}

// getMonthlyDetails returns monthly billing summary
func (r *BillingRoutes) getMonthlyDetails(c *gin.Context) {
	userID := c.GetString("user_id")

	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, -1, 0).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().UTC().Format("2006-01-02"))

	query := `SELECT DATE_TRUNC('month', created_at)::date as month,
	          COALESCE(SUM(total_cost), 0) as total_cost,
	          COALESCE(SUM(gift_deduction), 0) as gift_deduction,
	          COALESCE(SUM(recharge_deduction), 0) as recharge_deduction,
	          COALESCE(SUM(credit_deduction), 0) as credit_deduction,
	          COALESCE(SUM(subscription_deduction), 0) as subscription_deduction
	          FROM usage_records
	          WHERE user_id = $1::uuid
	            AND created_at >= $2
	            AND created_at < $3::date + INTERVAL '1 day'
	          GROUP BY DATE_TRUNC('month', created_at)
	          ORDER BY month DESC`

	rows, err := r.db.Query(c.Request.Context(), query, userID, startDate, endDate)
	if err != nil {
		zap.L().Error("Failed to query monthly details", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get monthly details"}})
		return
	}
	defer rows.Close()

	records := []gin.H{}
	for rows.Next() {
		var month time.Time
		var totalCost, giftDeduction, rechargeDeduction, creditDeduction, subscriptionDeduction int64
		if err := rows.Scan(&month, &totalCost, &giftDeduction, &rechargeDeduction, &creditDeduction, &subscriptionDeduction); err != nil {
			zap.L().Error("Failed to scan monthly details row", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get monthly details"}})
			return
		}
		records = append(records, gin.H{
			"month":                  month.Format("2006-01"),
			"total_cost":             totalCost,
			"gift_deduction":         giftDeduction,
			"recharge_deduction":     rechargeDeduction,
			"credit_deduction":       creditDeduction,
			"subscription_deduction": subscriptionDeduction,
		})
	}

	c.JSON(http.StatusOK, gin.H{"records": records})
}

// getRequestDetails returns detailed request-level billing
func (r *BillingRoutes) getRequestDetails(c *gin.Context) {
	userID := c.GetString("user_id")
	role := c.GetString("role")
	isAdmin := role == "system_admin" || role == "super_admin"

	page := 1
	pageSize := 20
	if p := c.Query("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if ps := c.Query("page_size"); ps != "" {
		if parsed, err := strconv.Atoi(ps); err == nil && parsed > 0 {
			pageSize = parsed
		}
	}
	if pageSize > 200 {
		pageSize = 200
	}
	if pageSize < 1 {
		pageSize = 20
	}

	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	whereClause := "WHERE ur.user_id = $1::uuid"
	args := []interface{}{userID}
	argIndex := 2

	if startDate != "" {
		whereClause += fmt.Sprintf(" AND ur.created_at >= $%d", argIndex)
		args = append(args, startDate)
		argIndex++
	}
	if endDate != "" {
		whereClause += fmt.Sprintf(" AND ur.created_at < $%d::date + INTERVAL '1 day'", argIndex)
		args = append(args, endDate)
		argIndex++
	}
	if model := c.Query("model"); model != "" {
		whereClause += fmt.Sprintf(" AND ur.model = $%d", argIndex)
		args = append(args, model)
		argIndex++
	}
	if apiKeyID := c.Query("api_key_id"); apiKeyID != "" {
		whereClause += fmt.Sprintf(" AND ur.user_api_key_id = $%d::uuid", argIndex)
		args = append(args, apiKeyID)
		argIndex++
	}
	if groupID := c.Query("group_id"); groupID != "" {
		whereClause += fmt.Sprintf(" AND ur.group_id = $%d::uuid", argIndex)
		args = append(args, groupID)
		argIndex++
	}
	if s := c.Query("is_streaming"); s != "" {
		whereClause += fmt.Sprintf(" AND ur.is_streaming = $%d", argIndex)
		args = append(args, s == "true" || s == "1")
		argIndex++
	}
	if status := c.Query("status"); status != "" {
		whereClause += fmt.Sprintf(" AND ur.status = $%d", argIndex)
		args = append(args, status)
		argIndex++
	}

	// Get total
	var total int
	if err := r.db.QueryRow(c.Request.Context(),
		"SELECT COUNT(*) FROM usage_records ur "+whereClause, args...,
	).Scan(&total); err != nil {
		zap.L().Error("Failed to count usage records", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get records"}})
		return
	}

	offset := (page - 1) * pageSize
	queryArgs := append(args, pageSize, offset)

	clientIPSelect := "''"
	if isAdmin {
		clientIPSelect = "COALESCE(ur.client_ip, '')"
	}

	rows, err := r.db.Query(c.Request.Context(),
		fmt.Sprintf(`SELECT ur.trace_id, ur.model, ur.created_at,
			                 COALESCE(ur.project_name, 'default') as project_name,
			                 COALESCE(ur.project_id, '') as project_id,
			                 COALESCE(ur.key_prefix, '') as key_prefix,
			                 ur.input_tokens, ur.output_tokens, COALESCE(ur.cached_input_tokens, 0) as cached_tokens,
			                 COALESCE(ur.cache_creation_tokens, 0) as cache_creation_tokens,
			                 ur.total_cost, ur.latency_ms,
			                 COALESCE(ur.endpoint, ''),
			                 COALESCE(ur.is_streaming, false),
			                 COALESCE(ur.status, 'success'),
			                 COALESCE(ur.group_id::text, ''),
			                 COALESCE(g.name, ''),
			                 COALESCE(ur.api_key_name, ''),
			                 COALESCE(ur.provider, ''),
			                 COALESCE((SELECT sm.vendor FROM service_models sm
			                           WHERE sm.deleted_at IS NULL
			                             AND (sm.api_model_name = ur.model OR sm.product_id = ur.model)
			                           ORDER BY (sm.status = 'active') DESC LIMIT 1), '') as model_vendor,
			                 COALESCE(ur.user_agent, ''),
			                 COALESCE(ur.agent_tools, ''),
			                 COALESCE(ur.called_tools, ''),
			                 %s
		          FROM usage_records ur
		          LEFT JOIN user_groups g ON g.id = ur.group_id
		          %s
		          ORDER BY ur.created_at DESC LIMIT $%d OFFSET $%d`, clientIPSelect, whereClause, argIndex, argIndex+1),
		queryArgs...,
	)
	if err != nil {
		zap.L().Error("Failed to query request details", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get records"}})
		return
	}
	defer rows.Close()

	records := []gin.H{}
	for rows.Next() {
		var traceID, model string
		var createdAt time.Time
		var projectName, projectID, keyPrefix, endpoint, status, groupID, groupName, apiKeyName, clientIP, provider, modelVendor, userAgent, agentTools, calledTools string
		var inputTokens, outputTokens, cachedTokens, cacheCreationTokens, latencyMs int
		var totalCost int64
		var isStreaming bool
		if err := rows.Scan(&traceID, &model, &createdAt,
			&projectName, &projectID, &keyPrefix,
			&inputTokens, &outputTokens, &cachedTokens, &cacheCreationTokens,
			&totalCost, &latencyMs,
			&endpoint, &isStreaming, &status,
			&groupID, &groupName, &apiKeyName, &provider, &modelVendor, &userAgent, &agentTools, &calledTools, &clientIP); err != nil {
			zap.L().Error("Failed to scan request details row", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get records"}})
			return
		}
		rec := gin.H{
			"trace_id":              traceID,
			"model":                 model,
			"created_at":            createdAt,
			"project_name":          projectName,
			"project_id":            projectID,
			"key_prefix":            keyPrefix,
			"input_tokens":          inputTokens,
			"output_tokens":         outputTokens,
			"cached_tokens":         cachedTokens,
			"cache_creation_tokens": cacheCreationTokens,
			"total_cost":            totalCost,
			"total_cost_usd":        QuotaToUSD(totalCost),
			"latency_ms":            latencyMs,
			"endpoint":              endpoint,
			"is_streaming":          isStreaming,
			"status":                status,
			"group_id":              groupID,
			"group_name":            groupName,
			"api_key_name":          apiKeyName,
		}
		// Model vendor is safe to show to any user; the upstream provider
		// name (which exposes our supplier structure) is admin-only. Display
		// the company name derived from the model (same mapping as the model
		// gallery: qwen->alibaba, glm->zhipu ai, ...), falling back to the
		// catalog vendor only when the model name carries no family signal.
		vendorDisplay := modelManufacturer(model, model)
		if vendorDisplay == "" || vendorDisplay == "other" {
			vendorDisplay = modelVendor
		}
		rec["vendor"] = vendorDisplay
		// Own-request client app / agent tools are safe for any user (the query
		// is already scoped to the caller's records); client IP stays admin-only.
		rec["user_agent"] = userAgent
		rec["agent_tools"] = agentTools
		rec["called_tools"] = calledTools
		if isAdmin {
			rec["provider"] = provider
			rec["client_ip"] = clientIP
		}
		records = append(records, rec)
	}

	c.JSON(http.StatusOK, gin.H{
		"records":     records,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": (total + pageSize - 1) / pageSize,
	})
}

// getRechargeRecords returns paginated recharge orders for the current user
// getBalanceTransactions lists the caller's wallet ledger entries
// (recharges, admin adjustments, vouchers, refunds — usage rows are
// excluded by default because they are high-volume and already visible in
// the usage tabs). Pass ?include_usage=1 to include them.
func (r *BillingRoutes) getBalanceTransactions(c *gin.Context) {
	userID := c.GetString("user_id")

	where := `user_id = $1::uuid AND transaction_type <> 'usage'`
	if c.Query("include_usage") == "1" {
		where = `user_id = $1::uuid`
	}
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT created_at, transaction_type, balance_type, amount, COALESCE(balance_after, 0), COALESCE(description, '')
		 FROM balance_transactions
		 WHERE `+where+`
		 ORDER BY created_at DESC LIMIT 50`,
		userID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"transactions": []gin.H{}})
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var createdAt time.Time
		var txType, balanceType, description string
		var amount, after int64
		if err := rows.Scan(&createdAt, &txType, &balanceType, &amount, &after, &description); err != nil {
			continue
		}
		out = append(out, gin.H{
			"created_at":    createdAt,
			"type":          txType,
			"balance_type":  balanceType,
			"amount_micro":  amount,
			"amount_usd":    float64(amount) / 1_000_000.0,
			"balance_after": float64(after) / 1_000_000.0,
			"description":   description,
		})
	}
	c.JSON(http.StatusOK, gin.H{"transactions": out})
}

func (r *BillingRoutes) getRechargeRecords(c *gin.Context) {
	userID := c.GetString("user_id")

	page := 1
	pageSize := 20
	if p := c.Query("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if ps := c.Query("page_size"); ps != "" {
		if parsed, err := strconv.Atoi(ps); err == nil && parsed > 0 {
			pageSize = parsed
		}
	}
	if pageSize > 200 {
		pageSize = 200
	}
	if pageSize < 1 {
		pageSize = 20
	}

	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	status := c.Query("status")

	data, err := query.GetRechargeRecords(c.Request.Context(), r.db, userID, page, pageSize, startDate, endDate, status)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"records": []gin.H{}, "total": 0, "total_pages": 0})
		return
	}

	records := []gin.H{}
	for _, rec := range data.Items {
		records = append(records, gin.H{
			"id":             rec.ID,
			"order_id":       rec.OrderID,
			"amount":         rec.Amount,
			"currency":       rec.Currency,
			"exchange_rate":  rec.ExchangeRate,
			"status":         rec.Status,
			"payment_method": rec.PaymentMethod,
			"product_type":   rec.ProductType,
			"created_at":     rec.CreatedAt,
			"updated_at":     rec.UpdatedAt,
			"has_receipt":    rec.ReceiptKey != "",
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"records":     records,
		"total":       data.Total,
		"page":        data.Page,
		"page_size":   data.PageSize,
		"total_pages": data.TotalPages,
	})
}

// downloadReceipt streams the payment receipt PDF for the caller's own
// completed top-up order (orders.receipt_key set at payment time).
func (r *BillingRoutes) downloadReceipt(c *gin.Context) {
	userID := c.GetString("user_id")
	orderUUID := c.Param("id")

	var storageKey, status string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(receipt_key, ''), status FROM orders WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL`,
		orderUUID, userID).Scan(&storageKey, &status)
	if err != nil || status != "completed" || storageKey == "" || r.storage == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Receipt not found"}})
		return
	}
	rc, err := r.storage.Download(c.Request.Context(), storageKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Receipt file not found"}})
		return
	}
	defer func() { _ = rc.Close() }()
	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"receipt-%s.pdf\"", orderUUID[:8]))
	_, _ = io.Copy(c.Writer, rc)
}

// exportRechargeRecordsCSV exports recharge records as CSV
func (r *BillingRoutes) exportRechargeRecordsCSV(c *gin.Context) {
	userID := c.GetString("user_id")

	whereClause := "WHERE deleted_at IS NULL AND product_type IN ('recharge', 'subscription', 'subscription_renewal') AND user_id = $1::uuid"
	args := []interface{}{userID}
	argIndex := 2

	if status := c.Query("status"); status != "" {
		whereClause += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, status)
		argIndex++
	}
	if startDate := c.Query("start_date"); startDate != "" {
		whereClause += fmt.Sprintf(" AND created_at >= $%d", argIndex)
		args = append(args, startDate)
		argIndex++
	}
	if endDate := c.Query("end_date"); endDate != "" {
		whereClause += fmt.Sprintf(" AND created_at < $%d::date + INTERVAL '1 day'", argIndex)
		args = append(args, endDate)
	}

	rows, err := r.db.Query(c.Request.Context(),
		fmt.Sprintf(`SELECT order_id, amount, currency, exchange_rate, status, payment_method, created_at
					 FROM orders %s ORDER BY created_at DESC LIMIT 10000`, whereClause),
		args...,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Export failed"})
		return
	}
	defer rows.Close()

	// Resolve the user's current preferred currency / rate for the local-amount column.
	var localCurrency string
	var localRate float64
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(preferred_currency, 'USD') FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&localCurrency)
	if localCurrency == "" || localCurrency == "USD" {
		localCurrency = "USD"
		localRate = 1.0
	} else {
		var rateStr string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT value FROM system_settings WHERE key = 'usd_exchange_rate'`).
			Scan(&rateStr)
		localRate = 7.20
		if rateStr != "" {
			if rateCents, err := strconv.Atoi(rateStr); err == nil && rateCents > 0 {
				localRate = float64(rateCents) / 100.0
			}
		}
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=recharge-records-"+time.Now().UTC().Format("2006-01-02")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = c.Writer.WriteString(T("csv.header.order_id", c) + "," + T("csv.header.amount", c) + "," + T("csv.header.local_amount", c) + "," + T("csv.header.status", c) + "," + T("csv.header.payment_method", c) + "," + T("csv.header.created_at", c) + "\n")

	for rows.Next() {
		var orderID, currency, status, paymentMethod string
		var amount int64
		var exchangeRate float64
		var createdAt time.Time
		_ = rows.Scan(&orderID, &amount, &currency, &exchangeRate, &status, &paymentMethod, &createdAt)
		amountStr := QuotaToUSD(amount)
		localAmountStr := services.QuotaToCurrency(amount, localCurrency, localRate)
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%s,%s\n",
			csvSanitize(orderID), csvSanitize(amountStr), csvSanitize(localAmountStr),
			csvSanitize(status), csvSanitize(paymentMethod), createdAt.Format("2006-01-02 15:04:05"))
	}
}

// exportRealtimeCSV exports realtime billing as CSV
func (r *BillingRoutes) exportRealtimeCSV(c *gin.Context) {
	userID := c.GetString("user_id")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT
			CASE
				WHEN model LIKE '%image%' OR model LIKE '%dall%' OR model LIKE '%flux%' OR model LIKE '%midjourney%' OR model LIKE '%sd-%' THEN 'image'
				WHEN model LIKE '%video%' OR model LIKE '%sora%' OR model LIKE '%runway%' THEN 'video'
				WHEN model LIKE '%embedding%' THEN 'embedding'
				WHEN model LIKE '%tts%' OR model LIKE '%audio%' OR model LIKE '%whisper%' THEN 'audio'
				ELSE 'completion'
			END as product_name,
			'Model Inference' as product_type,
			COALESCE(SUM(total_cost), 0) as total_cost
		 FROM usage_records
		 WHERE user_id = $1::uuid AND created_at >= NOW() - INTERVAL '24 hours'
		 GROUP BY product_name ORDER BY total_cost DESC`,
		userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Export failed"})
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=billing-realtime-"+time.Now().UTC().Format("2006-01-02")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = c.Writer.WriteString(T("csv.header.product_name", c) + "," + T("csv.header.product_type", c) + "," + T("csv.header.consumption", c) + "," + T("csv.header.paid_deduction", c) + "," + T("csv.header.gift_deduction", c) + "\n")

	for rows.Next() {
		var name, ptype string
		var cost int64
		_ = rows.Scan(&name, &ptype, &cost)
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s\n", name, ptype, QuotaToUSD(cost))
	}
}

// exportMonthlyCSV exports monthly billing as CSV
func (r *BillingRoutes) exportMonthlyCSV(c *gin.Context) {
	userID := c.GetString("user_id")
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, -1, 0).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().UTC().Format("2006-01-02"))

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT DATE(created_at) as date, COALESCE(SUM(total_cost), 0) as total_cost
		 FROM usage_records
		 WHERE user_id = $1::uuid AND created_at >= $2 AND created_at < $3::date + INTERVAL '1 day'
		 GROUP BY DATE(created_at) ORDER BY date DESC`,
		userID, startDate, endDate,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Export failed"})
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=billing-monthly-"+time.Now().UTC().Format("2006-01-02")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = c.Writer.WriteString(T("csv.header.date", c) + "," + T("csv.header.consumption", c) + "," + T("csv.header.paid_deduction", c) + "," + T("csv.header.gift_deduction", c) + "\n")

	for rows.Next() {
		var date time.Time
		var cost int64
		_ = rows.Scan(&date, &cost)
		_, _ = fmt.Fprintf(c.Writer, "%s,%s\n", date.Format("2006-01-02"), QuotaToUSD(cost))
	}
}

// exportRequestsCSV exports request details as CSV
func (r *BillingRoutes) exportRequestsCSV(c *gin.Context) {
	userID := c.GetString("user_id")
	role := c.GetString("role")
	isAdmin := role == "system_admin" || role == "super_admin"

	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	whereClause := "WHERE ur.user_id = $1::uuid"
	args := []interface{}{userID}
	argIndex := 2

	if startDate != "" {
		whereClause += fmt.Sprintf(" AND ur.created_at >= $%d", argIndex)
		args = append(args, startDate)
		argIndex++
	}
	if endDate != "" {
		whereClause += fmt.Sprintf(" AND ur.created_at < $%d::date + INTERVAL '1 day'", argIndex)
		args = append(args, endDate)
		argIndex++
	}
	if model := c.Query("model"); model != "" {
		whereClause += fmt.Sprintf(" AND ur.model = $%d", argIndex)
		args = append(args, model)
		argIndex++
	}
	if apiKeyID := c.Query("api_key_id"); apiKeyID != "" {
		whereClause += fmt.Sprintf(" AND ur.user_api_key_id = $%d::uuid", argIndex)
		args = append(args, apiKeyID)
		argIndex++
	}
	if groupID := c.Query("group_id"); groupID != "" {
		whereClause += fmt.Sprintf(" AND ur.group_id = $%d::uuid", argIndex)
		args = append(args, groupID)
		argIndex++
	}
	if s := c.Query("is_streaming"); s != "" {
		whereClause += fmt.Sprintf(" AND ur.is_streaming = $%d", argIndex)
		args = append(args, s == "true" || s == "1")
		argIndex++
	}
	if status := c.Query("status"); status != "" {
		whereClause += fmt.Sprintf(" AND ur.status = $%d", argIndex)
		args = append(args, status)
	}

	clientIPSelect := "''"
	if isAdmin {
		clientIPSelect = "COALESCE(ur.client_ip, '')"
	}

	rows, err := r.db.Query(c.Request.Context(),
		fmt.Sprintf(`SELECT ur.trace_id, ur.model, ur.created_at,
		                 COALESCE(ur.project_name, 'default') as project_name,
		                 COALESCE(ur.project_id, '') as project_id,
		                 COALESCE(ur.api_key_name, '') as api_key_name,
		                 COALESCE(ur.user_api_key_id::text, '') as api_key_id,
		                 ur.input_tokens, ur.output_tokens, COALESCE(ur.cached_input_tokens, 0),
		                 ur.total_cost, ur.latency_ms,
		                 COALESCE(ur.endpoint, ''),
		                 COALESCE(ur.is_streaming, false),
		                 COALESCE(ur.status, 'success'),
		                 COALESCE(g.name, ''),
		                 COALESCE(ur.user_agent, ''),
		                 COALESCE(ur.agent_tools, ''),
		                 COALESCE(ur.called_tools, ''),
		                 %s
		          FROM usage_records ur
		          LEFT JOIN user_groups g ON g.id = ur.group_id
		          %s ORDER BY ur.created_at DESC LIMIT 10000`, clientIPSelect, whereClause),
		args...,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Export failed"})
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=billing-requests-"+time.Now().UTC().Format("2006-01-02")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	header := T("csv.header.request_id", c) + "," + T("csv.header.model", c) + "," + T("csv.header.time", c) + "," + T("csv.header.project_name", c) + "," + T("csv.header.project_id", c) + "," + T("csv.header.api_key_name", c) + "," + T("csv.header.api_key_id", c) + "," + T("csv.header.input_tokens", c) + "," + T("csv.header.output_tokens", c) + "," + T("csv.header.cached_tokens", c) + ",endpoint,type,status,group,cost,latency_ms,user_agent,agent_tools,called_tools"
	if isAdmin {
		header += ",client_ip"
	}
	_, _ = c.Writer.WriteString(header + "\n")

	for rows.Next() {
		var traceID, model string
		var createdAt time.Time
		var projectName, projectID, apiKeyName, apiKeyID, endpoint, status, groupName, clientIP, userAgent, agentTools, calledTools string
		var inputTokens, outputTokens, cachedTokens, latencyMs int
		var totalCost int64
		var isStreaming bool
		_ = rows.Scan(&traceID, &model, &createdAt,
			&projectName, &projectID, &apiKeyName, &apiKeyID,
			&inputTokens, &outputTokens, &cachedTokens,
			&totalCost, &latencyMs,
			&endpoint, &isStreaming, &status, &groupName, &userAgent, &agentTools, &calledTools, &clientIP)
		row := fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%d,%d,%d,%s,%s,%s,%s,%s,%d,%s,%s,%s",
			csvSanitize(traceID), csvSanitize(model), createdAt.Format("2006-01-02 15:04:05"),
			csvSanitize(projectName), csvSanitize(projectID), csvSanitize(apiKeyName), csvSanitize(apiKeyID),
			inputTokens, outputTokens, cachedTokens,
			csvSanitize(endpoint), boolToStreaming(isStreaming), csvSanitize(status), csvSanitize(groupName),
			csvSanitize(QuotaToUSD(totalCost)), latencyMs,
			csvSanitize(userAgent), csvSanitize(agentTools), csvSanitize(calledTools))
		if isAdmin {
			row += "," + csvSanitize(clientIP)
		}
		_, _ = fmt.Fprintln(c.Writer, row)
	}
}

func boolToStreaming(v bool) string {
	if v {
		return "streaming"
	}
	return "non-streaming"
}

// csvSanitize prevents CSV formula injection by prefixing a single quote to any
// field that starts with =, +, -, or @.
func csvSanitize(s string) string {
	if s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@') {
		s = "'" + s
	}
	if strings.ContainsAny(s, ",\"\n\r") {
		s = "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}
