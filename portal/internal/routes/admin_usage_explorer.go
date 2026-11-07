package routes

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// allowedUsageExplorerDimensions restricts the grouping dimensions to prevent SQL injection.
var allowedUsageExplorerDimensions = map[string]bool{
	"user":              true,
	"enterprise":        true,
	"model":             true,
	"key":               true,
	"endpoint":          true,
	"subscription_plan": true,
}

// explorerRange describes a parsed time window for the explorer APIs.
type explorerRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Label string    `json:"label"`
}

// parseExplorerRange converts ?period or ?date_from/?date_to into a safe half-open
// time window [start, end). When both date_from and date_to are provided they take
// precedence over period.
func parseExplorerRange(c *gin.Context) explorerRange {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")
	if dateFrom != "" && dateTo != "" {
		start, err1 := time.Parse("2006-01-02", dateFrom)
		end, err2 := time.Parse("2006-01-02", dateTo)
		if err1 == nil && err2 == nil {
			end = end.AddDate(0, 0, 1)
			return explorerRange{Start: start.UTC(), End: end.UTC(), Label: fmt.Sprintf("%s ~ %s", dateFrom, dateTo)}
		}
	}

	period := query.NormalizePeriod(c.DefaultQuery("period", "7d"))
	switch period {
	case "24h":
		return explorerRange{Start: today, End: today.AddDate(0, 0, 1), Label: "1day"}
	case "15d":
		return explorerRange{Start: today.AddDate(0, 0, -14), End: today.AddDate(0, 0, 1), Label: "15days"}
	case "30d":
		return explorerRange{Start: today.AddDate(0, 0, -29), End: today.AddDate(0, 0, 1), Label: "30days"}
	case "90d":
		return explorerRange{Start: today.AddDate(0, 0, -89), End: today.AddDate(0, 0, 1), Label: "quarter"}
	default: // 7d
		return explorerRange{Start: today.AddDate(0, 0, -6), End: today.AddDate(0, 0, 1), Label: "7days"}
	}
}

// UsageExplorerRow holds one aggregated dimension row.
type UsageExplorerRow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email,omitempty"`
	KeyPrefix    string `json:"key_prefix,omitempty"`
	Subscribers  int    `json:"subscribers,omitempty"`
	Requests     int    `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
	TotalCost    int64  `json:"total_cost"`
	AvgLatencyMs int    `json:"avg_latency_ms"`
}

func (r *AdminRoutes) listUsageExplorerAggregate(c *gin.Context) {
	ctx := c.Request.Context()
	dimension := c.Query("dimension")
	if !allowedUsageExplorerDimensions[dimension] {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid dimension"}})
		return
	}

	rng := parseExplorerRange(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 || limit > 200 {
		limit = 50
	}

	var rows []UsageExplorerRow
	var err error
	switch dimension {
	case "user":
		rows, err = r.aggregateByUser(ctx, rng.Start, rng.End, limit)
	case "enterprise":
		rows, err = r.aggregateByEnterprise(ctx, rng.Start, rng.End, limit)
	case "model":
		rows, err = r.aggregateByModel(ctx, rng.Start, rng.End, limit)
	case "key":
		rows, err = r.aggregateByKey(ctx, rng.Start, rng.End, limit)
	case "endpoint":
		rows, err = r.aggregateByEndpoint(ctx, rng.Start, rng.End, limit)
	case "subscription_plan":
		rows, err = r.aggregateBySubscriptionPlan(ctx, rng.Start, rng.End, limit)
	}
	if err != nil {
		r.logger.Error("Failed to query usage explorer aggregate", zap.String("dimension", dimension), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query aggregate"}})
		return
	}

	var totalRequests int
	var totalInput, totalOutput, totalCached, totalCost int64
	for i := range rows {
		totalRequests += rows[i].Requests
		totalInput += rows[i].InputTokens
		totalOutput += rows[i].OutputTokens
		totalCached += rows[i].CachedTokens
		totalCost += rows[i].TotalCost
	}

	c.JSON(http.StatusOK, gin.H{
		"dimension": dimension,
		"range":     rng.Label,
		"start":     rng.Start.Format("2006-01-02"),
		"end":       rng.End.AddDate(0, 0, -1).Format("2006-01-02"),
		"limit":     limit,
		"rows":      rows,
		"summary": gin.H{
			"requests":      totalRequests,
			"input_tokens":  totalInput,
			"output_tokens": totalOutput,
			"cached_tokens": totalCached,
			"total_cost":    totalCost,
		},
	})
}

func (r *AdminRoutes) aggregateByUser(ctx context.Context, start, end time.Time, limit int) ([]UsageExplorerRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id::text,
		       COALESCE(u.name, u.email) as name,
		       u.email,
		       COUNT(*)::int as requests,
		       COALESCE(SUM(ur.input_tokens), 0)::bigint as input_tokens,
		       COALESCE(SUM(ur.output_tokens), 0)::bigint as output_tokens,
		       COALESCE(SUM(ur.cached_input_tokens), 0)::bigint as cached_tokens,
		       COALESCE(SUM(ur.total_cost), 0)::bigint as total_cost,
		       COALESCE(AVG(ur.latency_ms), 0)::int as avg_latency_ms
		FROM usage_records ur
		JOIN users u ON u.id = ur.user_id
		WHERE ur.created_at >= $1 AND ur.created_at < $2
		GROUP BY u.id, u.name, u.email
		ORDER BY total_cost DESC
		LIMIT $3`, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UsageExplorerRow
	for rows.Next() {
		var row UsageExplorerRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Email, &row.Requests,
			&row.InputTokens, &row.OutputTokens, &row.CachedTokens,
			&row.TotalCost, &row.AvgLatencyMs); err == nil {
			results = append(results, row)
		}
	}
	return results, nil
}

func (r *AdminRoutes) aggregateByEnterprise(ctx context.Context, start, end time.Time, limit int) ([]UsageExplorerRow, error) {
	// Aggregate strictly by the enterprise that owns the API key used for the
	// request. Personal keys used by enterprise members remain under the user
	// dimension, matching the platform's billing principle.
	rows, err := r.db.Query(ctx, `
		SELECT e.id::text,
		       e.name,
		       COUNT(*)::int as requests,
		       COALESCE(SUM(ur.input_tokens), 0)::bigint as input_tokens,
		       COALESCE(SUM(ur.output_tokens), 0)::bigint as output_tokens,
		       COALESCE(SUM(ur.cached_input_tokens), 0)::bigint as cached_tokens,
		       COALESCE(SUM(ur.total_cost), 0)::bigint as total_cost,
		       COALESCE(AVG(ur.latency_ms), 0)::int as avg_latency_ms
		FROM usage_records ur
		JOIN api_keys k ON k.id = ur.user_api_key_id
		JOIN enterprises e ON e.id = k.enterprise_id
		WHERE ur.created_at >= $1 AND ur.created_at < $2 AND k.key_type = 'enterprise'
		GROUP BY e.id, e.name
		ORDER BY total_cost DESC
		LIMIT $3`, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UsageExplorerRow
	for rows.Next() {
		var row UsageExplorerRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Requests,
			&row.InputTokens, &row.OutputTokens, &row.CachedTokens,
			&row.TotalCost, &row.AvgLatencyMs); err == nil {
			results = append(results, row)
		}
	}
	return results, nil
}

func (r *AdminRoutes) aggregateBySubscriptionPlan(ctx context.Context, start, end time.Time, limit int) ([]UsageExplorerRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT sp.id::text,
		       sp.name,
		       COUNT(DISTINCT ur.user_id)::int as subscribers,
		       COUNT(*)::int as requests,
		       COALESCE(SUM(ur.input_tokens), 0)::bigint as input_tokens,
		       COALESCE(SUM(ur.output_tokens), 0)::bigint as output_tokens,
		       COALESCE(SUM(ur.cached_input_tokens), 0)::bigint as cached_tokens,
		       COALESCE(SUM(ur.total_cost), 0)::bigint as total_cost,
		       COALESCE(AVG(ur.latency_ms), 0)::int as avg_latency_ms
		FROM usage_records ur
		JOIN api_keys k ON k.id = ur.user_api_key_id AND k.key_type = 'subscription'
		JOIN user_subscriptions us ON us.id = k.subscription_id
		JOIN subscription_plans sp ON sp.id = us.plan_id
		WHERE ur.created_at >= $1 AND ur.created_at < $2
		GROUP BY sp.id, sp.name
		ORDER BY total_cost DESC
		LIMIT $3`, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UsageExplorerRow
	for rows.Next() {
		var row UsageExplorerRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Subscribers, &row.Requests,
			&row.InputTokens, &row.OutputTokens, &row.CachedTokens,
			&row.TotalCost, &row.AvgLatencyMs); err == nil {
			results = append(results, row)
		}
	}
	return results, nil
}

func (r *AdminRoutes) aggregateByModel(ctx context.Context, start, end time.Time, limit int) ([]UsageExplorerRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ur.model as name,
		       COUNT(*)::int as requests,
		       COALESCE(SUM(ur.input_tokens), 0)::bigint as input_tokens,
		       COALESCE(SUM(ur.output_tokens), 0)::bigint as output_tokens,
		       COALESCE(SUM(ur.cached_input_tokens), 0)::bigint as cached_tokens,
		       COALESCE(SUM(ur.total_cost), 0)::bigint as total_cost,
		       COALESCE(AVG(ur.latency_ms), 0)::int as avg_latency_ms
		FROM usage_records ur
		WHERE ur.created_at >= $1 AND ur.created_at < $2
		GROUP BY ur.model
		ORDER BY total_cost DESC
		LIMIT $3`, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UsageExplorerRow
	for rows.Next() {
		var row UsageExplorerRow
		if err := rows.Scan(&row.Name, &row.Requests,
			&row.InputTokens, &row.OutputTokens, &row.CachedTokens,
			&row.TotalCost, &row.AvgLatencyMs); err == nil {
			row.ID = row.Name
			results = append(results, row)
		}
	}
	return results, nil
}

func (r *AdminRoutes) aggregateByKey(ctx context.Context, start, end time.Time, limit int) ([]UsageExplorerRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT k.id::text,
		       COALESCE(NULLIF(k.name, ''), k.key_prefix, '') as name,
		       COALESCE(k.key_prefix, '') as key_prefix,
		       COUNT(*)::int as requests,
		       COALESCE(SUM(ur.input_tokens), 0)::bigint as input_tokens,
		       COALESCE(SUM(ur.output_tokens), 0)::bigint as output_tokens,
		       COALESCE(SUM(ur.cached_input_tokens), 0)::bigint as cached_tokens,
		       COALESCE(SUM(ur.total_cost), 0)::bigint as total_cost,
		       COALESCE(AVG(ur.latency_ms), 0)::int as avg_latency_ms
		FROM usage_records ur
		JOIN api_keys k ON k.id = ur.user_api_key_id
		WHERE ur.created_at >= $1 AND ur.created_at < $2
		GROUP BY k.id, k.key_prefix
		ORDER BY total_cost DESC
		LIMIT $3`, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UsageExplorerRow
	for rows.Next() {
		var row UsageExplorerRow
		if err := rows.Scan(&row.ID, &row.Name, &row.KeyPrefix, &row.Requests,
			&row.InputTokens, &row.OutputTokens, &row.CachedTokens,
			&row.TotalCost, &row.AvgLatencyMs); err == nil {
			results = append(results, row)
		}
	}
	return results, nil
}

func (r *AdminRoutes) aggregateByEndpoint(ctx context.Context, start, end time.Time, limit int) ([]UsageExplorerRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT COALESCE(ur.endpoint, '') as name,
		       COUNT(*)::int as requests,
		       COALESCE(SUM(ur.input_tokens), 0)::bigint as input_tokens,
		       COALESCE(SUM(ur.output_tokens), 0)::bigint as output_tokens,
		       COALESCE(SUM(ur.cached_input_tokens), 0)::bigint as cached_tokens,
		       COALESCE(SUM(ur.total_cost), 0)::bigint as total_cost,
		       COALESCE(AVG(ur.latency_ms), 0)::int as avg_latency_ms
		FROM usage_records ur
		WHERE ur.created_at >= $1 AND ur.created_at < $2
		GROUP BY ur.endpoint
		ORDER BY total_cost DESC
		LIMIT $3`, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UsageExplorerRow
	for rows.Next() {
		var row UsageExplorerRow
		if err := rows.Scan(&row.Name, &row.Requests,
			&row.InputTokens, &row.OutputTokens, &row.CachedTokens,
			&row.TotalCost, &row.AvgLatencyMs); err == nil {
			row.ID = row.Name
			results = append(results, row)
		}
	}
	return results, nil
}

func (r *AdminRoutes) listUsageExplorerLogs(c *gin.Context) {
	ctx := c.Request.Context()
	dimension := c.Query("dimension")
	if !allowedUsageExplorerDimensions[dimension] {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid dimension"}})
		return
	}
	value := c.Query("value")
	if value == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "value is required"}})
		return
	}

	rng := parseExplorerRange(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	records, total, err := r.queryExplorerLogs(ctx, dimension, value, rng.Start, rng.End, page, limit)
	if err != nil {
		r.logger.Error("Failed to query usage explorer logs", zap.String("dimension", dimension), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query logs"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"dimension":   dimension,
		"value":       value,
		"range":       rng.Label,
		"records":     records,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": (total + limit - 1) / limit,
	})
}

func (r *AdminRoutes) queryExplorerLogs(ctx context.Context, dimension, value string, start, end time.Time, page, limit int) ([]query.UsageRecord, int, error) {
	var where string
	var args []interface{}
	switch dimension {
	case "user":
		where = "ur.user_id = $1::uuid"
		args = append(args, value)
	case "enterprise":
		// Drill down by the enterprise that owns the enterprise API key used for the request.
		where = "ur.user_api_key_id IN (SELECT id FROM api_keys WHERE enterprise_id = $1::uuid AND key_type = 'enterprise')"
		args = append(args, value)
	case "model":
		where = "ur.model = $1"
		args = append(args, value)
	case "key":
		where = "ur.user_api_key_id = $1::uuid"
		args = append(args, value)
	case "endpoint":
		where = "ur.endpoint = $1"
		args = append(args, value)
	case "subscription_plan":
		where = "ur.user_api_key_id IN (SELECT k.id FROM api_keys k JOIN user_subscriptions us ON us.id = k.subscription_id WHERE us.plan_id = $1::uuid)"
		args = append(args, value)
	}

	countArgs := append([]interface{}{}, args...)
	countArgs = append(countArgs, start, end)
	var total int
	_ = r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT COUNT(*) FROM usage_records ur
		WHERE %s AND ur.created_at >= $%d AND ur.created_at < $%d`, where, len(args)+1, len(args)+2),
		countArgs...).Scan(&total)

	offset := (page - 1) * limit
	queryArgs := append([]interface{}{}, args...)
	queryArgs = append(queryArgs, start, end, limit, offset)

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT ur.trace_id, ur.model, ur.provider, ur.input_tokens, ur.output_tokens, ur.cached_input_tokens,
		       ur.request_count, ur.total_cost, ur.latency_ms, ur.created_at,
		       COALESCE(ur.endpoint, ''),
		       COALESCE(ur.is_streaming, false),
		       COALESCE(ur.client_ip, ''),
		       COALESCE(ur.user_agent, ''),
		       COALESCE(ur.agent_tools, ''),
		       COALESCE(ur.called_tools, ''),
		       COALESCE(ur.status, 'success'),
		       COALESCE(ur.group_id::text, ''),
		       COALESCE(g.name, ''),
		       COALESCE(NULLIF(ur.api_key_name, ''), k.name, ''),
		       COALESCE(NULLIF(ur.key_prefix, ''), k.key_prefix,
		                CASE WHEN ur.user_api_key_id IS NULL THEN '(pre-charge)'
		                     ELSE '(deleted key)' END)
		FROM usage_records ur
		LEFT JOIN user_groups g ON g.id = ur.group_id
		LEFT JOIN api_keys k ON k.id = ur.user_api_key_id
		WHERE %s AND ur.created_at >= $%d AND ur.created_at < $%d
		ORDER BY ur.created_at DESC
		LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2, len(args)+3, len(args)+4),
		queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var records []query.UsageRecord
	for rows.Next() {
		var rec query.UsageRecord
		if err := rows.Scan(&rec.TraceID, &rec.Model, &rec.Provider, &rec.InputTokens, &rec.OutputTokens, &rec.CachedTokens,
			&rec.Requests, &rec.Cost, &rec.LatencyMs, &rec.CreatedAt,
			&rec.Endpoint, &rec.IsStreaming, &rec.ClientIP, &rec.UserAgent, &rec.AgentTools, &rec.CalledTools, &rec.Status,
			&rec.GroupID, &rec.GroupName, &rec.APIKeyName, &rec.KeyPrefix); err == nil {
			records = append(records, rec)
		} else {
			r.logger.Warn("Failed to scan usage explorer log row", zap.Error(err))
		}
	}
	return records, total, nil
}

func (r *AdminRoutes) exportUsageExplorerAggregate(c *gin.Context) {
	ctx := c.Request.Context()
	dimension := c.Query("dimension")
	if !allowedUsageExplorerDimensions[dimension] {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid dimension"}})
		return
	}

	rng := parseExplorerRange(c)
	var rows []UsageExplorerRow
	var err error
	switch dimension {
	case "user":
		rows, err = r.aggregateByUser(ctx, rng.Start, rng.End, 10000)
	case "enterprise":
		rows, err = r.aggregateByEnterprise(ctx, rng.Start, rng.End, 10000)
	case "model":
		rows, err = r.aggregateByModel(ctx, rng.Start, rng.End, 10000)
	case "key":
		rows, err = r.aggregateByKey(ctx, rng.Start, rng.End, 10000)
	case "endpoint":
		rows, err = r.aggregateByEndpoint(ctx, rng.Start, rng.End, 10000)
	}
	if err != nil {
		r.logger.Error("Failed to export usage explorer aggregate", zap.String("dimension", dimension), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
		return
	}

	filename := fmt.Sprintf("usage-explorer-%s-%s-%s.csv", dimension, rng.Start.Format("20060102"), rng.End.AddDate(0, 0, -1).Format("20060102"))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = c.Writer.WriteString("Name,Requests,Input Tokens,Output Tokens,Cache Tokens,Total Cost (USD),Avg Latency (ms)\n")
	for _, row := range rows {
		name := row.Name
		if row.Email != "" {
			name = fmt.Sprintf("%s (%s)", row.Name, row.Email)
		}
		_, _ = fmt.Fprintf(c.Writer, "%s,%d,%d,%d,%d,%.6f,%d\n",
			csvEscape(name), row.Requests, row.InputTokens, row.OutputTokens, row.CachedTokens,
			QuotaToUSDFloat(row.TotalCost), row.AvgLatencyMs)
	}
}

func (r *AdminRoutes) exportUsageExplorerLogs(c *gin.Context) {
	ctx := c.Request.Context()
	dimension := c.Query("dimension")
	if !allowedUsageExplorerDimensions[dimension] {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid dimension"}})
		return
	}
	value := c.Query("value")
	if value == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "value is required"}})
		return
	}

	rng := parseExplorerRange(c)
	records, _, err := r.queryExplorerLogs(ctx, dimension, value, rng.Start, rng.End, 1, 10000)
	if err != nil {
		r.logger.Error("Failed to export usage explorer logs", zap.String("dimension", dimension), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
		return
	}

	filename := fmt.Sprintf("usage-explorer-logs-%s-%s-%s-%s.csv", dimension, value, rng.Start.Format("20060102"), rng.End.AddDate(0, 0, -1).Format("20060102"))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = c.Writer.WriteString("Time,Model,Endpoint,Status,Streaming,Requests,Input Tokens,Output Tokens,Cache Tokens,Latency(ms),Cost(USD),Key Prefix,Client IP,Client App,Agent Tools,Called Tools\n")
	for _, rec := range records {
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%v,%d,%d,%d,%d,%d,%.6f,%s,%s,%s,%s,%s\n",
			rec.CreatedAt.Format("2006-01-02 15:04:05"),
			csvEscape(rec.Model),
			csvEscape(rec.Endpoint),
			csvEscape(rec.Status),
			rec.IsStreaming,
			rec.Requests,
			rec.InputTokens,
			rec.OutputTokens,
			rec.CachedTokens,
			rec.LatencyMs,
			QuotaToUSDFloat(rec.Cost),
			csvEscape(rec.KeyPrefix),
			csvEscape(rec.ClientIP),
			csvEscape(rec.UserAgent),
			csvEscape(rec.AgentTools),
			csvEscape(rec.CalledTools),
		)
	}
}

// csvEscape quotes a field if it contains commas, quotes, or newlines.
func csvEscape(s string) string {
	if s == "" {
		return ""
	}
	if strings.ContainsAny(s, ",\"\n\r") {
		s = strings.ReplaceAll(s, "\"", "\"\"")
		return fmt.Sprintf("\"%s\"", s)
	}
	return s
}
