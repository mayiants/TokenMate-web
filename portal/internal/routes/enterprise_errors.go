package routes

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// errorDateRange returns default current-month range when not supplied.
func errorDateRange(c *gin.Context) (from, to string) {
	from = c.Query("from")
	to = c.Query("to")
	if from == "" {
		from = time.Now().UTC().AddDate(0, 0, -time.Now().UTC().Day()+1).Format("2006-01-02")
	}
	if to == "" {
		to = time.Now().UTC().Format("2006-01-02")
	}
	return
}

// buildErrorWhere builds the shared WHERE clause for enterprise error queries.
func buildErrorWhere(enterpriseID, from, to string, rateLimitOnly bool, argIdx int) (string, []interface{}) {
	where := "k.enterprise_id = $" + strconv.Itoa(argIdx) + "::uuid AND k.key_type = 'enterprise' AND ur.status != 'success'"
	args := []interface{}{enterpriseID}
	argIdx++

	if from != "" {
		where += fmt.Sprintf(" AND ur.created_at >= $%d::date", argIdx)
		args = append(args, from)
		argIdx++
	}
	if to != "" {
		where += fmt.Sprintf(" AND ur.created_at < ($%d::date + INTERVAL '1 day')", argIdx)
		args = append(args, to)
	}
	if rateLimitOnly {
		where += " AND (ur.error_code = '429' OR ur.error ILIKE '%rate%' OR ur.status = 'rate_limit')"
	}
	return where, args
}

// getEnterpriseUsageErrors returns aggregated error statistics for the enterprise.
func (r *EnterpriseRoutes) getEnterpriseUsageErrors(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	from, to := errorDateRange(c)
	groupBy := c.DefaultQuery("group_by", "code") // code | time | model | key | user

	where, args := buildErrorWhere(enterpriseID, from, to, false, 1)

	var query string
	switch groupBy {
	case "time":
		query = fmt.Sprintf(`SELECT DATE(ur.created_at) as dimension_value, COUNT(*) as count
							 FROM usage_records ur JOIN api_keys k ON ur.user_api_key_id = k.id
							 WHERE %s
							 GROUP BY DATE(ur.created_at) ORDER BY dimension_value DESC LIMIT 100`, where)
	case "model":
		query = fmt.Sprintf(`SELECT COALESCE(ur.model, 'unknown') as dimension_value, COUNT(*) as count
							 FROM usage_records ur JOIN api_keys k ON ur.user_api_key_id = k.id
							 WHERE %s
							 GROUP BY ur.model ORDER BY count DESC LIMIT 100`, where)
	case "key":
		query = fmt.Sprintf(`SELECT COALESCE(k.key_prefix, 'unknown') as dimension_value, COUNT(*) as count
							 FROM usage_records ur JOIN api_keys k ON ur.user_api_key_id = k.id
							 WHERE %s
							 GROUP BY k.id, k.key_prefix ORDER BY count DESC LIMIT 100`, where)
	case "user":
		query = fmt.Sprintf(`SELECT COALESCE(u.name, u.email, 'unknown') as dimension_value, COUNT(*) as count
							 FROM usage_records ur JOIN api_keys k ON ur.user_api_key_id = k.id
							 JOIN users u ON ur.user_id = u.id
							 WHERE %s
							 GROUP BY u.id, u.name, u.email ORDER BY count DESC LIMIT 100`, where)
	default: // code
		query = fmt.Sprintf(`SELECT COALESCE(NULLIF(ur.error_code, ''), ur.status, 'unknown') as dimension_value, COUNT(*) as count
							 FROM usage_records ur JOIN api_keys k ON ur.user_api_key_id = k.id
							 WHERE %s
							 GROUP BY ur.error_code, ur.status ORDER BY count DESC LIMIT 100`, where)
	}

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		r.logger.Error("Failed to query enterprise usage errors", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load error analysis"}})
		return
	}
	defer rows.Close()

	type row struct {
		DimensionValue string `json:"dimension_value"`
		Count          int    `json:"count"`
	}
	var results []row
	var total int
	for rows.Next() {
		var rw row
		if err := rows.Scan(&rw.DimensionValue, &rw.Count); err == nil {
			results = append(results, rw)
			total += rw.Count
		}
	}

	c.JSON(http.StatusOK, gin.H{"group_by": groupBy, "total": total, "from": from, "to": to, "data": results})
}

// getEnterpriseErrorLogs returns recent failed request logs for the enterprise.
func (r *EnterpriseRoutes) getEnterpriseErrorLogs(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	from, to := errorDateRange(c)
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	errorCode := c.Query("error_code")

	where := "k.enterprise_id = $1::uuid AND k.key_type = 'enterprise' AND ur.status != 'success'"
	args := []interface{}{enterpriseID}
	argIdx := 2
	if from != "" {
		where += fmt.Sprintf(" AND ur.created_at >= $%d::date", argIdx)
		args = append(args, from)
		argIdx++
	}
	if to != "" {
		where += fmt.Sprintf(" AND ur.created_at < ($%d::date + INTERVAL '1 day')", argIdx)
		args = append(args, to)
		argIdx++
	}
	if errorCode != "" {
		where += fmt.Sprintf(" AND ur.error_code = $%d", argIdx)
		args = append(args, errorCode)
		argIdx++
	}

	query := fmt.Sprintf(`SELECT ur.created_at, ur.model, ur.endpoint, ur.status, ur.error_code, ur.error,
						         COALESCE(u.name, u.email, '') as user_name, COALESCE(k.key_prefix, '') as key_prefix
						  FROM usage_records ur
						  JOIN api_keys k ON ur.user_api_key_id = k.id
						  LEFT JOIN users u ON ur.user_id = u.id
						  WHERE %s
						  ORDER BY ur.created_at DESC LIMIT $%d`, where, argIdx)
	args = append(args, limit)

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		r.logger.Error("Failed to query enterprise error logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load error logs"}})
		return
	}
	defer rows.Close()

	type logRow struct {
		CreatedAt time.Time `json:"created_at"`
		Model     string    `json:"model"`
		Endpoint  string    `json:"endpoint"`
		Status    string    `json:"status"`
		ErrorCode string    `json:"error_code"`
		Error     string    `json:"error"`
		UserName  string    `json:"user_name"`
		KeyPrefix string    `json:"key_prefix"`
	}
	var logs []logRow
	for rows.Next() {
		var lr logRow
		if err := rows.Scan(&lr.CreatedAt, &lr.Model, &lr.Endpoint, &lr.Status, &lr.ErrorCode, &lr.Error, &lr.UserName, &lr.KeyPrefix); err == nil {
			logs = append(logs, lr)
		}
	}

	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

// getEnterpriseRateLimitHits returns rate-limit hit statistics for the enterprise.
func (r *EnterpriseRoutes) getEnterpriseRateLimitHits(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	from, to := errorDateRange(c)
	groupBy := c.DefaultQuery("group_by", "key") // key | user | time

	where, args := buildErrorWhere(enterpriseID, from, to, true, 1)

	var query string
	switch groupBy {
	case "time":
		query = fmt.Sprintf(`SELECT DATE(ur.created_at) as dimension_value, COUNT(*) as count
							 FROM usage_records ur JOIN api_keys k ON ur.user_api_key_id = k.id
							 WHERE %s
							 GROUP BY DATE(ur.created_at) ORDER BY dimension_value DESC LIMIT 100`, where)
	case "user":
		query = fmt.Sprintf(`SELECT COALESCE(u.name, u.email, 'unknown') as dimension_value, COUNT(*) as count
							 FROM usage_records ur JOIN api_keys k ON ur.user_api_key_id = k.id
							 JOIN users u ON ur.user_id = u.id
							 WHERE %s
							 GROUP BY u.id, u.name, u.email ORDER BY count DESC LIMIT 100`, where)
	default: // key
		query = fmt.Sprintf(`SELECT COALESCE(k.key_prefix, 'unknown') as dimension_value, COUNT(*) as count
							 FROM usage_records ur JOIN api_keys k ON ur.user_api_key_id = k.id
							 WHERE %s
							 GROUP BY k.id, k.key_prefix ORDER BY count DESC LIMIT 100`, where)
	}

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		r.logger.Error("Failed to query enterprise rate limit hits", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load rate limit stats"}})
		return
	}
	defer rows.Close()

	type row struct {
		DimensionValue string `json:"dimension_value"`
		Count          int    `json:"count"`
	}
	var results []row
	var total int
	for rows.Next() {
		var rw row
		if err := rows.Scan(&rw.DimensionValue, &rw.Count); err == nil {
			results = append(results, rw)
			total += rw.Count
		}
	}

	c.JSON(http.StatusOK, gin.H{"group_by": groupBy, "total": total, "from": from, "to": to, "data": results})
}
