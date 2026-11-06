package routes

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ChannelHealthRoutes

type ChannelHealthRoutes struct {
	db   *db.DB
	auth *middleware.Auth
}

func NewChannelHealthRoutes(db *db.DB, auth *middleware.Auth) *ChannelHealthRoutes {
	return &ChannelHealthRoutes{db: db, auth: auth}
}

func (r *ChannelHealthRoutes) Register(rg *gin.RouterGroup) {
	authGroup := rg.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	authGroup.GET("", r.listChannels)
	authGroup.POST("/test-all", r.testAll)
	authGroup.GET("/:providerId", r.getChannel)
	authGroup.POST("/:providerId/test", r.testChannel)
}

func (r *ChannelHealthRoutes) listChannels(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT p.id, p.name, p.type, p.enabled,
		        COALESCE(ch.status, 'unknown') as status,
		        COALESCE(ch.latency_ms, 0) as latency_ms,
		        CASE WHEN COALESCE(ch.success_count, 0) + COALESCE(ch.failure_count, 0) > 0
		             THEN ROUND(ch.success_count * 100.0 / (ch.success_count + ch.failure_count), 2)
		             ELSE 0 END as success_rate,
		        ch.last_tested_at as last_check
		 FROM providers p
		 LEFT JOIN channel_health ch ON p.id = ch.provider
		 ORDER BY p.sort_order, p.name`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list channels"}})
		return
	}
	defer rows.Close()

	channels := []gin.H{}
	for rows.Next() {
		var id, name, ptype, status string
		var enabled bool
		var latencyMs int
		var successRate float64
		var lastCheck interface{}
		_ = rows.Scan(&id, &name, &ptype, &enabled, &status, &latencyMs, &successRate, &lastCheck)
		channels = append(channels, gin.H{
			"id": id, "name": name, "type": ptype, "enabled": enabled, "status": status,
			"latency_ms": latencyMs, "success_rate": successRate, "last_check": lastCheck,
		})
	}
	c.JSON(http.StatusOK, gin.H{"channels": channels})
}

func (r *ChannelHealthRoutes) testAll(c *gin.Context) {
	// Get all enabled providers
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, name, base_url FROM providers WHERE enabled = true`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to test channels"}})
		return
	}
	defer rows.Close()

	results := []gin.H{}
	for rows.Next() {
		var id, name string
		var baseURL *string
		_ = rows.Scan(&id, &name, &baseURL)

		success, latency, errMsg := testHTTPConnectivity(baseURL)

		// Get existing counts
		var existingSuccess, existingFailure int
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(success_count, 0), COALESCE(failure_count, 0) FROM channel_health WHERE provider = $1`,
			id).Scan(&existingSuccess, &existingFailure)

		newSuccess := existingSuccess
		newFailure := existingFailure
		if success {
			newSuccess++
		} else {
			newFailure++
		}
		total := newSuccess + newFailure
		errorRate := 0.0
		if total > 0 {
			errorRate = float64(newFailure) / float64(total)
		}

		status := "healthy"
		if errorRate > 0.5 {
			status = "unhealthy"
		} else if errorRate > 0.2 {
			status = "degraded"
		}

		// Upsert channel health
		if err := r.db.Exec(c.Request.Context(), `INSERT INTO channel_health (provider, status, latency_ms, error_rate, success_count, failure_count, last_tested_at, last_error, updated_at)
					 VALUES ($1, $2, $3, $4, $5, $6, NOW(), $7, NOW())
					 ON CONFLICT (provider) DO UPDATE SET
					   status = $2, latency_ms = $3, error_rate = $4,
					   success_count = $5, failure_count = $6,
					   last_tested_at = NOW(), last_error = $7, updated_at = NOW()`, id, status, latency, errorRate, newSuccess, newFailure, errMsg); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}

		// Record history
		if err := r.db.Exec(c.Request.Context(), `INSERT INTO channel_health_history (provider, latency_ms, status, error_message)
					 VALUES ($1, $2, $3, $4)`, id, latency, status, errMsg); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}

		results = append(results, gin.H{
			"id": id, "name": name, "status": status, "latency": latency, "error": errMsg,
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "results": results})
}

func (r *ChannelHealthRoutes) getChannel(c *gin.Context) {
	providerID := c.Param("providerId")
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))

	var id, name string
	var enabled bool
	var status string
	var latencyMs, successCount, failureCount int
	var errorRate float64
	var lastTestedAt, updatedAt interface{}
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT p.id, p.name, p.enabled, COALESCE(ch.status, 'unknown'), COALESCE(ch.latency_ms, 0),
		        COALESCE(ch.error_rate, 0), COALESCE(ch.success_count, 0), COALESCE(ch.failure_count, 0),
		        ch.last_tested_at, ch.updated_at
		 FROM providers p LEFT JOIN channel_health ch ON p.id = ch.provider WHERE p.id = $1`, providerID,
	).Scan(&id, &name, &enabled, &status, &latencyMs, &errorRate, &successCount, &failureCount, &lastTestedAt, &updatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}

	// Get history
	historyRows, histErr := r.db.Query(c.Request.Context(),
		`SELECT latency_ms, status, error_message, tested_at
		 FROM channel_health_history
		 WHERE provider = $1 AND tested_at > NOW() - INTERVAL '1 day' * $2
		 ORDER BY tested_at DESC LIMIT 100`, providerID, days)
	if histErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get channel history"}})
		return
	}
	defer historyRows.Close()

	history := []gin.H{}
	for historyRows.Next() {
		var latency int
		var status, testedAt string
		var errorMessage *string
		_ = historyRows.Scan(&latency, &status, &errorMessage, &testedAt)
		history = append(history, gin.H{
			"latency_ms": latency, "status": status, "error_message": errorMessage, "tested_at": testedAt,
		})
	}

	// Get usage stats
	usageRows, usageErr := r.db.Query(c.Request.Context(),
		`SELECT DATE(created_at) as date, COUNT(*) as requests, SUM(total_cost) as cost
		 FROM usage_records WHERE provider = $1 AND created_at > NOW() - INTERVAL '1 day' * $2
		 GROUP BY DATE(created_at) ORDER BY date`, providerID, days)

	usage := []gin.H{}
	if usageErr == nil {
		defer usageRows.Close()
		for usageRows.Next() {
			var date string
			var requests int
			var cost float64
			_ = usageRows.Scan(&date, &requests, &cost)
			usage = append(usage, gin.H{"date": date, "requests": requests, "cost": cost})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"channel": gin.H{
			"id": id, "name": name, "enabled": enabled, "status": status,
			"latency_ms": latencyMs, "error_rate": errorRate,
			"success_count": successCount, "failure_count": failureCount,
			"last_tested_at": lastTestedAt, "updated_at": updatedAt,
			"history": history, "usage": usage,
		},
	})
}

func (r *ChannelHealthRoutes) testChannel(c *gin.Context) {
	providerID := c.Param("providerId")

	var baseURL *string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT base_url FROM providers WHERE id = $1`, providerID).Scan(&baseURL)

	success, latency, errMsg := testHTTPConnectivity(baseURL)

	// Get existing counts
	var existingSuccess, existingFailure int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(success_count, 0), COALESCE(failure_count, 0) FROM channel_health WHERE provider = $1`,
		providerID).Scan(&existingSuccess, &existingFailure)

	newSuccess := existingSuccess
	newFailure := existingFailure
	if success {
		newSuccess++
	} else {
		newFailure++
	}
	total := newSuccess + newFailure
	errorRate := 0.0
	if total > 0 {
		errorRate = float64(newFailure) / float64(total)
	}

	status := "healthy"
	if errorRate > 0.5 {
		status = "unhealthy"
	} else if errorRate > 0.2 {
		status = "degraded"
	}

	// Upsert
	if err := r.db.Exec(c.Request.Context(), `INSERT INTO channel_health (provider, status, latency_ms, error_rate, success_count, failure_count, last_tested_at, last_error, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, NOW(), $7, NOW())
			 ON CONFLICT (provider) DO UPDATE SET
			   status = $2, latency_ms = $3, error_rate = $4,
			   success_count = $5, failure_count = $6,
			   last_tested_at = NOW(), last_error = $7, updated_at = NOW()`, providerID, status, latency, errorRate, newSuccess, newFailure, errMsg); err != nil {
		zap.L().Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	// Record history
	if err := r.db.Exec(c.Request.Context(), `INSERT INTO channel_health_history (provider, latency_ms, status, error_message)
			 VALUES ($1, $2, $3, $4)`, providerID, latency, status, errMsg); err != nil {
		zap.L().Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"result": gin.H{
			"provider": providerID, "status": status, "latency": latency, "error": errMsg,
		},
	})
}

func testHTTPConnectivity(baseURL *string) (bool, int, *string) {
	if baseURL == nil || *baseURL == "" {
		s := "No base URL configured"
		return false, 0, &s
	}

	u, err := url.Parse(*baseURL)
	if err != nil {
		s := "Invalid URL: " + err.Error()
		return false, 0, &s
	}

	client := &http.Client{
		Timeout: 8 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("User-Agent", "AI-Proxy-Health-Check/1.0")

	start := time.Now()
	resp, err := client.Do(req)
	latency := int(time.Since(start).Milliseconds())

	if err != nil {
		errStr := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			s := "Connection timeout"
			return false, latency, &s
		}
		return false, latency, &errStr
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 500 {
		s := "HTTP " + strconv.Itoa(resp.StatusCode)
		return false, latency, &s
	}

	return true, latency, nil
}
