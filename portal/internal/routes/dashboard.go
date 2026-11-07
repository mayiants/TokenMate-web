package routes

import (
	"net/http"
	"strconv"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type DashboardRoutes struct {
	db     *db.DB
	auth   *middleware.Auth
	logger *zap.Logger
}

func NewDashboardRoutes(db *db.DB, auth *middleware.Auth, logger *zap.Logger) *DashboardRoutes {
	return &DashboardRoutes{db: db, auth: auth, logger: logger}
}

func (r *DashboardRoutes) Register(rg *gin.RouterGroup) {
	// User dashboard
	rg.GET("/admin/dashboard", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.getDashboard)

	// Admin dashboard routes
	adminDashboard := rg.Group("/admin/dashboard")
	adminDashboard.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	adminDashboard.GET("/stats", r.getAdminDashboardStats)
	adminDashboard.GET("/charts/consumption-trend", r.getConsumptionTrend)
	adminDashboard.GET("/charts/model-distribution", r.getModelDistribution)
	adminDashboard.GET("/charts/provider-distribution", r.getProviderDistribution)
	adminDashboard.GET("/charts/user-ranking", r.getUserRanking)
	adminDashboard.GET("/charts/user-trend", r.getUserTrend)
	adminDashboard.GET("/quick-actions", r.getQuickActions)

	// Admin analytics and stats
	adminGroup := rg.Group("/admin")
	adminGroup.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	adminGroup.GET("/analytics", r.getAnalytics)
	adminGroup.GET("/stats", r.getAdminStats)

	adminGroup.GET("/dashboard/debug-query", r.debugConsumptionQuery)
}

func (r *DashboardRoutes) getDashboard(c *gin.Context) {
	userID := c.GetString("user_id")
	role := c.GetString("role")

	isAdmin := role == "system_admin" || role == "super_admin"

	// All-time aggregate stats for the current scope.
	statsScope := userID
	if isAdmin {
		statsScope = ""
	}
	stats, err := query.GetDashboardStats(c.Request.Context(), r.db, 0)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"total_requests": 0,
			"total_tokens":   0,
			"total_cost":     0,
			"key_count":      0,
			"recent_usage":   []gin.H{},
		})
		return
	}

	// Get key count (scoped to user unless admin).
	var keyCount int
	keyQuery := `SELECT COUNT(*) FROM api_keys WHERE deleted_at IS NULL`
	if !isAdmin {
		keyQuery += " AND user_id = $1::uuid"
		_ = r.db.QueryRow(c.Request.Context(), keyQuery, userID).Scan(&keyCount)
	} else {
		_ = r.db.QueryRow(c.Request.Context(), keyQuery).Scan(&keyCount)
	}

	// Recent usage (last 7 days), scoped to the current user or all users for admins.
	recentUsage := []gin.H{}
	recentRows, _, err := query.GetUsageSummary(c.Request.Context(), r.db, statsScope, 7)
	if err == nil {
		for _, d := range recentRows {
			recentUsage = append(recentUsage, gin.H{
				"date": d.Date.Format("2006-01-02"), "requests": d.Requests, "tokens": d.Tokens, "cost": d.Cost,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"total_requests": stats.TotalRequests,
		"total_tokens":   stats.TotalTokens,
		"total_cost":     stats.TotalCost,
		"key_count":      keyCount,
		"recent_usage":   recentUsage,
	})
}

func (r *DashboardRoutes) getAdminDashboardStats(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days < 1 {
		days = 7
	}

	stats, err := query.GetDashboardStats(c.Request.Context(), r.db, days)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"users":    0,
			"keys":     0,
			"requests": 0,
			"revenue":  0,
			"trend":    []gin.H{},
		})
		return
	}

	// Trend data
	trendRows, _, err := query.GetUsageSummary(c.Request.Context(), r.db, "", days)
	trend := []gin.H{}
	if err == nil {
		for _, d := range trendRows {
			trend = append(trend, gin.H{
				"date":     d.Date.Format(time.DateOnly),
				"requests": d.Requests,
				"revenue":  float64(d.Cost) / 1000000.0,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"users":    stats.TotalUsers,
		"keys":     stats.TotalKeys,
		"requests": stats.TotalRequests,
		"revenue":  stats.TotalCost,
		"trend":    trend,
	})
}

func (r *DashboardRoutes) getConsumptionTrend(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days < 1 {
		days = 7
	}

	// Note: input/output token split is dashboard-specific; the shared helper
	// provides the daily aggregate shape and prevents re-building the same
	// date-bucket query in every dashboard endpoint.
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT DATE(created_at) as date, COUNT(*) as requests,
		        COALESCE(SUM(input_tokens), 0) as input_tokens,
		        COALESCE(SUM(output_tokens), 0) as output_tokens,
		        COALESCE(SUM(total_cost), 0) as cost
		 FROM usage_records WHERE created_at > NOW() - INTERVAL '1 day' * $1
		 GROUP BY DATE(created_at) ORDER BY date`, days)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{}})
		return
	}
	defer rows.Close()

	data := []gin.H{}
	for rows.Next() {
		var date time.Time
		var requests int
		var inputTokens, outputTokens int64
		var cost float64
		_ = rows.Scan(&date, &requests, &inputTokens, &outputTokens, &cost)
		data = append(data, gin.H{
			"date": date.Format(time.DateOnly), "requests": requests,
			"inputTokens": inputTokens, "outputTokens": outputTokens, "cost": cost,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (r *DashboardRoutes) getModelDistribution(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days < 1 {
		days = 7
	}

	rows, err := query.GetUsageDistribution(c.Request.Context(), r.db, "", days, "model")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{}})
		return
	}

	data := []gin.H{}
	for i, row := range rows {
		if i >= 10 {
			break
		}
		data = append(data, gin.H{
			"model": row.Name, "requests": row.Requests, "cost": row.Cost, "tokens": row.Tokens,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (r *DashboardRoutes) getProviderDistribution(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days < 1 {
		days = 7
	}

	rows, err := query.GetUsageDistribution(c.Request.Context(), r.db, "", days, "provider")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{}})
		return
	}

	data := []gin.H{}
	for _, row := range rows {
		data = append(data, gin.H{
			"provider": row.Name, "requests": row.Requests, "cost": row.Cost, "avgLatency": row.AvgLatencyMs,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (r *DashboardRoutes) getUserRanking(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if days < 1 {
		days = 7
	}
	if limit < 1 {
		limit = 10
	}

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT u.id, u.name, u.email, COUNT(*) as requests,
		        COALESCE(SUM(total_cost), 0) as cost,
		        COALESCE(SUM(input_tokens + output_tokens), 0) as tokens
		 FROM usage_records ur
		 JOIN users u ON ur.user_id = u.id
		 WHERE ur.created_at > NOW() - INTERVAL '1 day' * $1
		 GROUP BY u.id, u.name, u.email
		 ORDER BY cost DESC LIMIT $2`, days, limit)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{}})
		return
	}
	defer rows.Close()

	data := []gin.H{}
	for rows.Next() {
		var id, name, email string
		var requests int
		var cost float64
		var tokens int64
		_ = rows.Scan(&id, &name, &email, &requests, &cost, &tokens)
		data = append(data, gin.H{
			"id": id, "name": name, "email": email,
			"requests": requests, "cost": cost, "tokens": tokens,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (r *DashboardRoutes) getUserTrend(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days < 1 {
		days = 7
	}

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT DATE(created_at) as date, COUNT(DISTINCT user_id) as active_users
		 FROM usage_records WHERE created_at > NOW() - INTERVAL '1 day' * $1
		 GROUP BY DATE(created_at) ORDER BY date`, days)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{}})
		return
	}
	defer rows.Close()

	data := []gin.H{}
	for rows.Next() {
		var date time.Time
		var activeUsers int
		_ = rows.Scan(&date, &activeUsers)
		data = append(data, gin.H{"date": date.Format(time.DateOnly), "activeUsers": activeUsers})
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (r *DashboardRoutes) getQuickActions(c *gin.Context) {
	// Pending orders
	var pendingOrders int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM orders WHERE status = 'pending'`).Scan(&pendingOrders)

	// Low balance users (less than ¥1 = 100 cents)
	var lowBalanceUsers int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM users WHERE deleted_at IS NULL AND balance < 100`).Scan(&lowBalanceUsers)

	// Recent errors (last 24h)
	var recentErrors int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM usage_records WHERE error IS NOT NULL AND created_at > NOW() - INTERVAL '24 hours'`).Scan(&recentErrors)

	c.JSON(http.StatusOK, gin.H{
		"pendingOrders":   pendingOrders,
		"lowBalanceUsers": lowBalanceUsers,
		"recentErrors":    recentErrors,
	})
}

func (r *DashboardRoutes) getAnalytics(c *gin.Context) {
	rangeParam := c.DefaultQuery("range", "7d")
	days := 7
	switch rangeParam {
	case "30d":
		days = 30
	case "90d":
		days = 90
	}

	stats, err := query.GetDashboardStats(c.Request.Context(), r.db, days)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"stats":      gin.H{},
			"today":      gin.H{},
			"modelUsage": []gin.H{},
			"tokenUsage": []gin.H{},
		})
		return
	}

	// Model usage
	modelUsage := []gin.H{}
	modelRows, err := query.GetUsageDistribution(c.Request.Context(), r.db, "", days, "model")
	if err == nil {
		for i, row := range modelRows {
			if i >= 10 {
				break
			}
			modelUsage = append(modelUsage, gin.H{"model": row.Name, "requests": row.Requests, "cost": row.Cost})
		}
	}

	// Token usage by day
	tokenRows, tokenErr := r.db.Query(c.Request.Context(),
		`SELECT DATE(created_at) as date, COALESCE(SUM(input_tokens), 0) as input_tokens,
		        COALESCE(SUM(output_tokens), 0) as output_tokens
		 FROM usage_records WHERE created_at > NOW() - INTERVAL '1 day' * $1
		 GROUP BY DATE(created_at) ORDER BY date`, days)

	tokenUsage := []gin.H{}
	if tokenErr == nil {
		defer tokenRows.Close()
		for tokenRows.Next() {
			var date time.Time
			var inputTokens, outputTokens int64
			_ = tokenRows.Scan(&date, &inputTokens, &outputTokens)
			tokenUsage = append(tokenUsage, gin.H{"date": date.Format(time.DateOnly), "inputTokens": inputTokens, "outputTokens": outputTokens})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"stats": gin.H{
			"totalUsers": stats.TotalUsers, "totalKeys": stats.TotalKeys, "activeKeys": stats.ActiveKeys,
			"totalRequests": stats.TotalRequests, "totalTokens": stats.TotalTokens, "totalRevenue": stats.TotalCost,
		},
		"today": gin.H{
			"requests": stats.TodayRequests, "tokens": stats.TodayTokens, "revenue": stats.TodayCost,
		},
		"modelUsage": modelUsage,
		"tokenUsage": tokenUsage,
	})
}

func (r *DashboardRoutes) getAdminStats(c *gin.Context) {
	stats, err := query.GetDashboardStats(c.Request.Context(), r.db, 0)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"totalUsers":    0,
			"activeKeys":    0,
			"upstreamKeys":  0,
			"totalRequests": 0,
			"todayRequests": 0,
		})
		return
	}

	var upstreamKeysCount int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM upstream_keys`).Scan(&upstreamKeysCount)

	c.JSON(http.StatusOK, gin.H{
		"totalUsers":    stats.TotalUsers,
		"activeKeys":    stats.ActiveKeys,
		"upstreamKeys":  upstreamKeysCount,
		"totalRequests": stats.TotalRequests,
		"todayRequests": stats.TodayRequests,
	})
}

func (r *DashboardRoutes) debugConsumptionQuery(c *gin.Context) {
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT created_at::date, COALESCE(SUM(request_count), 0)::bigint
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= $2 AND created_at < $3
		 GROUP BY created_at::date ORDER BY 1`,
		c.GetString("user_id"), monthStart, monthStart.AddDate(0, 1, 0))
	if err != nil {
		r.logger.Error("debug query failed", zap.Error(err))
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type rowResult struct {
		Date  string
		Count int64
	}
	var results []rowResult
	for rows.Next() {
		var date time.Time
		var cnt int64
		if err := rows.Scan(&date, &cnt); err != nil {
			r.logger.Error("debug scan failed", zap.Error(err))
			continue
		}
		r.logger.Info("debug row", zap.String("date", date.Format(time.DateOnly)), zap.Int64("count", cnt))
		results = append(results, rowResult{Date: date.Format(time.DateOnly), Count: cnt})
	}

	r.logger.Info("debug query params",
		zap.String("user_id", c.GetString("user_id")),
		zap.String("month_start", monthStart.Format(time.RFC3339)),
		zap.String("month_end", monthStart.AddDate(0, 1, 0).Format(time.RFC3339)),
		zap.Int("num_rows", len(results)),
	)

	c.JSON(http.StatusOK, gin.H{
		"user_id":     c.GetString("user_id"),
		"month_start": monthStart.Format(time.RFC3339),
		"month_end":   monthStart.AddDate(0, 1, 0).Format(time.RFC3339),
		"results":     results,
		"num_rows":    len(results),
	})
}
