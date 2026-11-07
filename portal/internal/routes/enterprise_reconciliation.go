package routes

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// getEnterpriseReconciliation returns member-level reconciliation for enterprise admin
func (r *EnterpriseRoutes) getEnterpriseReconciliation(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	from := c.Query("from")
	to := c.Query("to")

	// Default: current month
	if from == "" {
		from = time.Now().Format("2006-01") + "-01"
	}
	if to == "" {
		to = time.Now().UTC().Format("2006-01-02")
	}

	ctx := c.Request.Context()

	// Enterprise summary
	var name string
	var quotaTotal, quotaUsed, creditTotal, creditUsed int64
	_ = r.db.QueryRow(ctx,
		`SELECT name, COALESCE(quota_total,0), COALESCE(quota_used,0), COALESCE(credit_total,0), COALESCE(credit_used,0)
		 FROM enterprises WHERE id = $1::uuid`, enterpriseID,
	).Scan(&name, &quotaTotal, &quotaUsed, &creditTotal, &creditUsed)

	// Member consumption breakdown
	rows, err := r.db.Query(ctx, `
		SELECT u.id::text, COALESCE(u.name, u.email) as display_name,
			COUNT(*) as requests, SUM(ur.input_tokens + ur.output_tokens) as tokens, SUM(ur.total_cost) as cost
		FROM usage_records ur
		JOIN api_keys k ON ur.user_api_key_id = k.id
		JOIN users u ON ur.user_id = u.id
		WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
			AND ur.created_at >= $2::date AND ur.created_at < ($3::date + INTERVAL '1 day')
		GROUP BY u.id, u.name, u.email
		ORDER BY cost DESC`, enterpriseID, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query reconciliation"}})
		return
	}
	defer rows.Close()

	type memberRow struct {
		UserID   string  `json:"user_id"`
		UserName string  `json:"user_name"`
		Requests int     `json:"requests"`
		Tokens   int64   `json:"tokens"`
		Cost     float64 `json:"cost"` // micro-USD (1e6 = 1 USD)
		Percent  float64 `json:"percent"`
	}
	members := []memberRow{}
	var totalRequests int
	var totalTokens int64
	var totalCost float64
	for rows.Next() {
		var m memberRow
		if err := rows.Scan(&m.UserID, &m.UserName, &m.Requests, &m.Tokens, &m.Cost); err == nil {
			members = append(members, m)
			totalRequests += m.Requests
			totalTokens += m.Tokens
			totalCost += m.Cost
		}
	}
	if totalCost > 0 {
		for i := range members {
			members[i].Percent = members[i].Cost / totalCost * 100
		}
	}

	// Credit log (last 20)
	logRows, _ := r.db.Query(ctx, `
		SELECT created_at::text, change_type, field_name, action, COALESCE(reason,''), delta
		FROM enterprise_audit_log
		WHERE enterprise_id = $1::uuid
		ORDER BY created_at DESC LIMIT 20`, enterpriseID)

	type logEntry struct {
		CreatedAt  string  `json:"created_at"`
		ChangeType string  `json:"change_type"`
		FieldName  string  `json:"field_name"`
		Action     string  `json:"action"`
		Reason     string  `json:"reason"`
		Delta      float64 `json:"delta"`
	}
	logs := []logEntry{}
	if logRows != nil {
		defer logRows.Close()
		for logRows.Next() {
			var l logEntry
			if err := logRows.Scan(&l.CreatedAt, &l.ChangeType, &l.FieldName, &l.Action, &l.Reason, &l.Delta); err == nil {
				logs = append(logs, l)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"enterprise": gin.H{
			"name": name, "quota_total": quotaTotal, "quota_used": quotaUsed,
			"credit_total": creditTotal, "credit_used": creditUsed,
		},
		"from": from, "to": to,
		"summary":     gin.H{"requests": totalRequests, "tokens": totalTokens, "cost": totalCost},
		"members":     members,
		"credit_logs": logs,
	})
}

// exportEnterpriseReconciliationCSV exports member reconciliation as CSV
func (r *EnterpriseRoutes) exportEnterpriseReconciliationCSV(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	from := c.DefaultQuery("from", time.Now().Format("2006-01")+"-01")
	to := c.DefaultQuery("to", time.Now().UTC().Format("2006-01-02"))

	ctx := c.Request.Context()

	// Total cost for percentage calculation.
	var totalCost float64
	_ = r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(ur.total_cost), 0)
		FROM usage_records ur
		JOIN api_keys k ON ur.user_api_key_id = k.id
		WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
			AND ur.created_at >= $2::date AND ur.created_at < ($3::date + INTERVAL '1 day')`,
		enterpriseID, from, to).Scan(&totalCost)

	rows, err := r.db.Query(ctx, `
		SELECT COALESCE(u.name, u.email), COUNT(*), SUM(ur.input_tokens + ur.output_tokens), SUM(ur.total_cost)
		FROM usage_records ur
		JOIN api_keys k ON ur.user_api_key_id = k.id
		JOIN users u ON ur.user_id = u.id
		WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
			AND ur.created_at >= $2::date AND ur.created_at < ($3::date + INTERVAL '1 day')
		GROUP BY u.id, u.name, u.email
		ORDER BY SUM(ur.total_cost) DESC`, enterpriseID, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=reconciliation_%s_%s.csv", from, to))
	_, _ = c.Writer.WriteString("\xEF\xBB\xBF") // BOM for Excel
	_, _ = c.Writer.WriteString("Member,Requests,Tokens,Cost (USD),Cost %\n")
	for rows.Next() {
		var name string
		var requests int
		var tokens int64
		var cost float64
		if err := rows.Scan(&name, &requests, &tokens, &cost); err == nil {
			percent := 0.0
			if totalCost > 0 {
				percent = cost / totalCost * 100
			}
			_, _ = fmt.Fprintf(c.Writer, "%s,%d,%d,%.6f,%.2f\n", name, requests, tokens, cost/1000000, percent)
		}
	}
}

// getEnterpriseUsageTrend returns daily usage trend for enterprise (for chart)
func (r *EnterpriseRoutes) getEnterpriseUsageTrend(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	from := c.DefaultQuery("from", time.Now().AddDate(0, 0, -29).Format("2006-01-02"))
	to := c.DefaultQuery("to", time.Now().UTC().Format("2006-01-02"))

	rows, err := r.db.Query(c.Request.Context(), `
		SELECT d::date::text, COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0), COALESCE(COUNT(ur.id), 0), COALESCE(SUM(ur.total_cost), 0)
		FROM generate_series($2::date, $3::date, '1 day'::interval) d
		LEFT JOIN usage_records ur ON ur.created_at::date = d::date
			AND ur.user_api_key_id IN (SELECT id FROM api_keys WHERE enterprise_id = $1::uuid AND key_type = 'enterprise')
		GROUP BY d::date ORDER BY d::date`,
		enterpriseID, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query trend"}})
		return
	}
	defer rows.Close()

	type point struct {
		Date     string  `json:"date"`
		Tokens   int64   `json:"tokens"`
		Requests int     `json:"requests"`
		Cost     float64 `json:"cost"` // micro-USD (1e6 = 1 USD)
	}
	points := []point{}
	for rows.Next() {
		var p point
		if err := rows.Scan(&p.Date, &p.Tokens, &p.Requests, &p.Cost); err == nil {
			points = append(points, p)
		}
	}
	c.JSON(http.StatusOK, gin.H{"trend": points, "from": from, "to": to})
}

// getEnterpriseBillingTrend returns daily cost trend for enterprise billing chart
func (r *EnterpriseRoutes) getEnterpriseBillingTrend(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	days := 30
	if d := c.Query("days"); d != "" {
		_, _ = fmt.Sscanf(d, "%d", &days)
		if days > 90 {
			days = 90
		}
	}
	from := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	to := time.Now().UTC().Format("2006-01-02")

	rows, err := r.db.Query(c.Request.Context(), `
		SELECT d::date::text,
			COALESCE(SUM(ur.total_cost), 0),
			COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0),
			COALESCE(COUNT(ur.id), 0)
		FROM generate_series($2::date, $3::date, '1 day'::interval) d
		LEFT JOIN usage_records ur ON ur.created_at::date = d::date
			AND ur.user_api_key_id IN (SELECT id FROM api_keys WHERE enterprise_id = $1::uuid AND key_type = 'enterprise')
		GROUP BY d::date ORDER BY d::date`,
		enterpriseID, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query billing trend"}})
		return
	}
	defer rows.Close()

	type point struct {
		Date     string  `json:"date"`
		Cost     float64 `json:"cost"` // micro-USD (1e6 = 1 USD)
		Tokens   int64   `json:"tokens"`
		Requests int     `json:"requests"`
	}
	points := []point{}
	for rows.Next() {
		var p point
		if err := rows.Scan(&p.Date, &p.Cost, &p.Tokens, &p.Requests); err == nil {
			points = append(points, p)
		}
	}
	c.JSON(http.StatusOK, gin.H{"trend": points, "from": from, "to": to})
}

// getEnterpriseMemberReconciliationDetail returns single member's usage detail
func (r *EnterpriseRoutes) getEnterpriseMemberReconciliationDetail(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.Param("userId")
	from := c.DefaultQuery("from", time.Now().Format("2006-01")+"-01")
	to := c.DefaultQuery("to", time.Now().UTC().Format("2006-01-02"))

	// Verify member belongs to enterprise
	var memberName string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(name, email) FROM users WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL`,
		userID, enterpriseID).Scan(&memberName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found"}})
		return
	}

	// Model breakdown for this member
	rows, err := r.db.Query(c.Request.Context(), `
		SELECT ur.model, COUNT(*), SUM(ur.input_tokens + ur.output_tokens), SUM(ur.total_cost)
		FROM usage_records ur
		JOIN api_keys k ON ur.user_api_key_id = k.id
		WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise' AND ur.user_id = $2::uuid
			AND ur.created_at >= $3::date AND ur.created_at < ($4::date + INTERVAL '1 day')
		GROUP BY ur.model ORDER BY SUM(ur.total_cost) DESC`,
		enterpriseID, userID, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Query failed"}})
		return
	}
	defer rows.Close()

	type modelRow struct {
		Model    string  `json:"model"`
		Requests int     `json:"requests"`
		Tokens   int64   `json:"tokens"`
		Cost     float64 `json:"cost"` // micro-USD (1e6 = 1 USD)
	}
	models := []modelRow{}
	for rows.Next() {
		var m modelRow
		if err := rows.Scan(&m.Model, &m.Requests, &m.Tokens, &m.Cost); err == nil {
			models = append(models, m)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id": userID, "user_name": memberName,
		"from": from, "to": to,
		"models": models,
	})
}

// getEnterpriseMemberLogs returns recent API call logs for a single enterprise member.
func (r *EnterpriseRoutes) getEnterpriseMemberLogs(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.Query("user_id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "user_id is required"}})
		return
	}

	// Verify the user belongs to this enterprise.
	var exists bool
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL)`,
		userID, enterpriseID).Scan(&exists)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found"}})
		return
	}

	from := c.DefaultQuery("from", time.Now().AddDate(0, 0, -29).Format("2006-01-02"))
	to := c.DefaultQuery("to", time.Now().UTC().Format("2006-01-02"))
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := fmt.Sscanf(l, "%d", &limit); err != nil || n != 1 || limit < 1 {
			limit = 50
		}
		if limit > 200 {
			limit = 200
		}
	}

	records, total, err := query.GetUsageRecords(c.Request.Context(), r.db, "", query.UsageRecordFilter{
		IsAdmin:      true,
		EnterpriseID: enterpriseID,
		UserID:       userID,
		StartDate:    from + " 00:00:00",
		EndDate:      to + " 23:59:59",
		Limit:        limit,
	})
	if err != nil {
		r.logger.Error("Failed to query enterprise member logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query logs"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"logs": records, "total": total, "from": from, "to": to})
}

// exportEnterpriseMemberLogsCSV streams ALL of a member's usage records for the
// selected date range as CSV. Unlike the on-screen member-logs panel (capped at
// 200 rows) this exports the full set so historical months can be downloaded.
func (r *EnterpriseRoutes) exportEnterpriseMemberLogsCSV(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.Query("user_id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "user_id is required"}})
		return
	}

	// Verify the user belongs to this enterprise.
	var exists bool
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL)`,
		userID, enterpriseID).Scan(&exists)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found"}})
		return
	}

	from := c.DefaultQuery("from", time.Now().AddDate(0, 0, -29).Format("2006-01-02"))
	to := c.DefaultQuery("to", time.Now().UTC().Format("2006-01-02"))

	records, _, err := query.GetUsageRecords(c.Request.Context(), r.db, "", query.UsageRecordFilter{
		IsAdmin:      true,
		EnterpriseID: enterpriseID,
		UserID:       userID,
		StartDate:    from + " 00:00:00",
		EndDate:      to + " 23:59:59",
		Limit:        100000,
	})
	if err != nil {
		r.logger.Error("Failed to export enterprise member logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=member-logs-%s-%s_%s.csv", userID[:8], from, to))
	// Provider (upstream) info is intentionally omitted for enterprise-admin exports;
	// only system-admin exports expose the upstream provider.
	_, _ = c.Writer.WriteString("Time,Model,Endpoint,Input Tokens,Output Tokens,Cached Tokens,Cost (micro-USD),Latency (ms),Status\n")
	for _, rec := range records {
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%d,%d,%d,%d,%d,%s\n",
			rec.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
			csvSafe(rec.Model), csvSafe(rec.Endpoint),
			rec.InputTokens, rec.OutputTokens, rec.CachedTokens,
			rec.Cost, rec.LatencyMs, csvSafe(rec.Status))
	}
}

// csvSafe neutralizes commas so a value stays within one CSV field.
func csvSafe(s string) string { return strings.ReplaceAll(s, ",", ";") }

// searchEnterpriseMember searches members by name/email within enterprise
func (r *EnterpriseRoutes) searchEnterpriseMember(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusOK, gin.H{"members": []interface{}{}})
		return
	}

	rows, err := r.db.Query(c.Request.Context(), `
		SELECT id::text, COALESCE(name, ''), email
		FROM users WHERE enterprise_id = $1::uuid AND deleted_at IS NULL
			AND (email ILIKE $2 OR name ILIKE $2)
		LIMIT 10`, enterpriseID, "%"+q+"%")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Search failed"}})
		return
	}
	defer rows.Close()

	type member struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	members := []member{}
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.ID, &m.Name, &m.Email); err == nil {
			members = append(members, m)
		}
	}
	c.JSON(http.StatusOK, gin.H{"members": members})
}
