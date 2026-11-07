package routes

import (
	"fmt"
	"net/http"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/gin-gonic/gin"
)

type StatusRoutes struct {
	db *db.DB
}

func NewStatusRoutes(db *db.DB) *StatusRoutes {
	return &StatusRoutes{db: db}
}

func (r *StatusRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("", r.getStatus)
	rg.GET("/providers", r.getProviders)
	rg.GET("/metrics", r.getMetrics)
}

func (r *StatusRoutes) RegisterHealth(rg *gin.RouterGroup) {
	rg.GET("", r.getHealth)
}

// RegisterUpstream registers the login-required upstream service status endpoint.
func (r *StatusRoutes) RegisterUpstream(rg *gin.RouterGroup) {
	rg.GET("/upstream", r.getUpstreamStatus)
	rg.GET("/upstream/history", r.getUpstreamStatusHistory)
}

func (r *StatusRoutes) getHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (r *StatusRoutes) getStatus(c *gin.Context) {
	// Check database
	dbOK := r.db.Pool.Ping(c.Request.Context()) == nil

	// Check Redis
	redisOK := r.db.Redis.Ping(c.Request.Context()).Err() == nil

	// Get proxy status
	var proxyCount int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM proxy_instances WHERE status = 'active' AND last_heartbeat_at > NOW() - INTERVAL '5 minutes'`,
	).Scan(&proxyCount)

	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"timestamp": time.Now().Format(time.RFC3339),
		"version":   "2.0.0",
		"services": gin.H{
			"database": dbOK,
			"redis":    redisOK,
		},
		"active_proxies": proxyCount,
	})
}

func (r *StatusRoutes) getProviders(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, name, type, base_url, enabled FROM providers ORDER BY sort_order, name`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get providers"}})
		return
	}
	defer rows.Close()

	providers := []gin.H{}
	for rows.Next() {
		var id, name, ptype string
		var baseURL *string
		var enabled bool
		_ = rows.Scan(&id, &name, &ptype, &baseURL, &enabled)

		providers = append(providers, gin.H{
			"id": id, "name": name, "type": ptype,
			"base_url": baseURL, "enabled": enabled,
		})
	}

	c.JSON(http.StatusOK, gin.H{"providers": providers})
}

// getMetrics returns Prometheus-style metrics for the portal
func (r *StatusRoutes) getMetrics(c *gin.Context) {
	var result string

	// Request counts (last hour)
	var totalRequests int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM usage_records WHERE created_at > NOW() - INTERVAL '1 hour'`,
	).Scan(&totalRequests)

	result += "# HELP portal_requests_total Total requests in last hour\n"
	result += "# TYPE portal_requests_total gauge\n"
	result += fmt.Sprintf("portal_requests_total %d\n", totalRequests)

	// Total tokens (last hour)
	var totalTokens int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(total_tokens), 0) FROM usage_records WHERE created_at > NOW() - INTERVAL '1 hour'`,
	).Scan(&totalTokens)

	result += "\n# HELP portal_tokens_total Total tokens in last hour\n"
	result += "# TYPE portal_tokens_total gauge\n"
	result += fmt.Sprintf("portal_tokens_total %d\n", totalTokens)

	// Active users
	var activeUsers int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(DISTINCT user_id) FROM usage_records WHERE created_at > NOW() - INTERVAL '1 hour'`,
	).Scan(&activeUsers)

	result += "\n# HELP portal_active_users Active users in last hour\n"
	result += "# TYPE portal_active_users gauge\n"
	result += fmt.Sprintf("portal_active_users %d\n", activeUsers)

	// Total users
	var totalUsers int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`,
	).Scan(&totalUsers)

	result += "\n# HELP portal_users_total Total registered users\n"
	result += "# TYPE portal_users_total gauge\n"
	result += fmt.Sprintf("portal_users_total %d\n", totalUsers)

	// Active API keys
	var activeKeys int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM api_keys WHERE deleted_at IS NULL AND status = 'active'`,
	).Scan(&activeKeys)

	result += "\n# HELP portal_api_keys_active Active API keys\n"
	result += "# TYPE portal_api_keys_active gauge\n"
	result += fmt.Sprintf("portal_api_keys_active %d\n", activeKeys)

	// Active subscriptions
	var activeSubs int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM user_subscriptions WHERE status = 'active' AND expires_at > NOW()`,
	).Scan(&activeSubs)

	result += "\n# HELP portal_subscriptions_active Active subscriptions\n"
	result += "# TYPE portal_subscriptions_active gauge\n"
	result += fmt.Sprintf("portal_subscriptions_active %d\n", activeSubs)

	c.Header("Content-Type", "text/plain; version=0.0.4")
	c.String(http.StatusOK, result)
}

// getUpstreamStatus returns the login-required model × group service status.
// Non-admin users only see model + group combinations they can access through
// their active API keys.
func (r *StatusRoutes) getUpstreamStatus(c *gin.Context) {
	period := c.DefaultQuery("period", "24h")
	model := c.Query("model")
	group := c.Query("group")

	allowedPairs, err := r.userAllowedPairs(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to resolve user access"}})
		return
	}

	rows, err := query.ListModelGroupHealth(c.Request.Context(), r.db, period, model, group, allowedPairs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch status"}})
		return
	}

	data := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		data = append(data, gin.H{
			"model":          row.Model,
			"group_id":       row.GroupID,
			"group_name":     row.GroupName,
			"availability":   row.Availability,
			"avg_latency_ms": row.AvgLatencyMs,
			"status":         mapInternalStatus(row.Status),
			"request_count":  row.RequestCount,
			"success_count":  row.SuccessCount,
			"failure_count":  row.FailureCount,
			"last_bucket":    row.LastBucket,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"meta": gin.H{
			"period":     period,
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
		"data": data,
	})
}

// getUpstreamStatusHistory returns per-bucket history for the status page heatmap/sparkline.
func (r *StatusRoutes) getUpstreamStatusHistory(c *gin.Context) {
	period := c.DefaultQuery("period", "24h")
	model := c.Query("model")
	group := c.Query("group")

	allowedPairs, err := r.userAllowedPairs(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to resolve user access"}})
		return
	}

	history, err := query.ModelGroupHealthHistory(c.Request.Context(), r.db, period, model, group, allowedPairs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch status history"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"meta": gin.H{
			"period":     period,
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
		"data": history,
	})
}

// userAllowedPairs returns the model + group pairs the current user may view.
// Admins get nil (unrestricted). Unauthenticated requests get an empty slice.
func (r *StatusRoutes) userAllowedPairs(c *gin.Context) ([]query.UserModelGroupPair, error) {
	userID := c.GetString("user_id")
	role := c.GetString("role")
	if userID == "" {
		return []query.UserModelGroupPair{}, nil
	}
	return query.GetUserAccessibleModelGroups(c.Request.Context(), r.db, userID, role)
}
