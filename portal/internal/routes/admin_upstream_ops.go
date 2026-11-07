package routes

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func normalizeUpstreamOpsPeriod(p string) string {
	return query.NormalizePeriod(p)
}

func isValidUpstreamOpsPeriod(p string) bool {
	switch normalizeUpstreamOpsPeriod(p) {
	case "24h", "7d", "15d", "30d", "90d":
		return true
	}
	return false
}

// listUpstreamOpsHealth returns model × group health (default) or provider × model health when ?view=providers.
func (r *AdminRoutes) listUpstreamOpsHealth(c *gin.Context) {
	period := normalizeUpstreamOpsPeriod(c.DefaultQuery("period", "24h"))
	view := c.DefaultQuery("view", "model-group")

	if view == "providers" {
		r.listUpstreamOpsProviderHealth(c)
		return
	}

	model := c.Query("model")
	group := c.Query("group")

	rows, err := query.ListModelGroupHealth(c.Request.Context(), r.db, period, model, group, nil)
	if err != nil {
		r.logger.Warn("Failed to list model group health", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch health data"}})
		return
	}

	result := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		result = append(result, gin.H{
			"model":          row.Model,
			"group_id":       row.GroupID,
			"group_name":     row.GroupName,
			"request_count":  row.RequestCount,
			"success_count":  row.SuccessCount,
			"failure_count":  row.FailureCount,
			"avg_latency_ms": row.AvgLatencyMs,
			"availability":   row.Availability,
			"status":         mapInternalStatus(row.Status),
			"last_bucket":    row.LastBucket,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": result, "period": period})
}

func (r *AdminRoutes) listUpstreamOpsProviderHealth(c *gin.Context) {
	period := normalizeUpstreamOpsPeriod(c.DefaultQuery("period", "24h"))
	provider := c.Query("provider")
	model := c.Query("model")

	rows, err := query.ListProviderModelHealth(c.Request.Context(), r.db, period, provider, model)
	if err != nil {
		r.logger.Warn("Failed to list provider model health", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch health data"}})
		return
	}

	result := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		result = append(result, gin.H{
			"provider":       row.Provider,
			"model":          row.Model,
			"request_count":  row.RequestCount,
			"success_count":  row.SuccessCount,
			"failure_count":  row.FailureCount,
			"avg_latency_ms": row.AvgLatencyMs,
			"availability":   row.Availability,
			"status":         mapInternalStatus(row.Status),
			"last_bucket":    row.LastBucket,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": result, "period": period, "view": "providers"})
}

func (r *AdminRoutes) getUpstreamOpsHealthSummary(c *gin.Context) {
	period := normalizeUpstreamOpsPeriod(c.DefaultQuery("period", "24h"))

	rows, err := query.ListModelGroupHealth(c.Request.Context(), r.db, period, "", "", nil)
	if err != nil {
		r.logger.Warn("Failed to list model group health summary", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch summary"}})
		return
	}

	summary := gin.H{"available": 0, "degraded": 0, "unavailable": 0, "unknown": 0, "missing": 0, "total": len(rows)}
	for _, row := range rows {
		summary[mapInternalStatus(row.Status)] = summary[mapInternalStatus(row.Status)].(int) + 1
	}
	c.JSON(http.StatusOK, gin.H{"summary": summary, "period": period})
}

func (r *AdminRoutes) listUpstreamOpsErrors(c *gin.Context) {
	period := normalizeUpstreamOpsPeriod(c.DefaultQuery("period", "24h"))
	model := c.Query("model")
	group := c.Query("group")

	window, _ := query.ParsePeriod(period)

	// Aggregate error counters from provider_health_stats_model (most complete source)
	// and supplement with model_group_health_stats for group-level breakdown.
	rows, err := r.db.Query(c.Request.Context(), `
		WITH combined AS (
			-- Provider-level rows only for models with NO group-level data in the
			-- window; otherwise the same failures would be double-counted (once
			-- per provider-model row, once per model-group row) and surface as a
			-- fake "general" group.
			SELECT model, '' as group_id,
				failure_count,
				timeout_count, rate_limit_count, error_5xx_count, error_4xx_count,
				auth_error_count, network_error_count, response_timeout_count,
				content_mismatch_count, invalid_request_count
			FROM provider_health_stats_model pm
			WHERE bucket >= NOW() - $1::interval
			  AND ($2 = '' OR model = $2)
			  AND NOT EXISTS (
			      SELECT 1 FROM model_group_health_stats mg
			      WHERE mg.model = pm.model
			        AND mg.bucket >= NOW() - $1::interval
			        AND mg.group_id <> ''
			  )
			UNION ALL
			SELECT model, group_id,
				failure_count,
				timeout_count, rate_limit_count, error_5xx_count, error_4xx_count,
				auth_error_count, network_error_count, response_timeout_count,
				content_mismatch_count, invalid_request_count
			FROM model_group_health_stats
			WHERE bucket >= NOW() - $1::interval
			  AND ($2 = '' OR model = $2)
			  AND ($3 = '' OR group_id = $3)
			  AND group_id <> ''
		)
		SELECT c.model, c.group_id, COALESCE(g.name, c.group_id) as group_name,
			COALESCE(SUM(c.failure_count), 0),
			COALESCE(SUM(c.timeout_count), 0),
			COALESCE(SUM(c.rate_limit_count), 0),
			COALESCE(SUM(c.error_5xx_count), 0),
			COALESCE(SUM(c.error_4xx_count), 0),
			COALESCE(SUM(c.auth_error_count), 0),
			COALESCE(SUM(c.network_error_count), 0),
			COALESCE(SUM(c.response_timeout_count), 0),
			COALESCE(SUM(c.content_mismatch_count), 0),
			COALESCE(SUM(c.invalid_request_count), 0)
		FROM combined c
		LEFT JOIN user_groups g ON g.id::text = c.group_id
		GROUP BY c.model, c.group_id, g.name
		ORDER BY c.model, c.group_id
	`, fmt.Sprintf("%f seconds", window.Seconds()), model, group)
	if err != nil {
		r.logger.Warn("Failed to list upstream ops errors", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch error data"}})
		return
	}
	defer rows.Close()

	// Build a lookup of impacted users / enterprises from usage_records for the same window.
	impactRows, err := r.db.Query(c.Request.Context(), `
		SELECT ur.model, COALESCE(ak.group_id, '') as group_id,
			COUNT(DISTINCT ur.user_id) as impacted_users,
			COUNT(DISTINCT u.enterprise_id) as impacted_enterprises
		FROM usage_records ur
		LEFT JOIN api_keys ak ON ak.id = ur.user_api_key_id
		LEFT JOIN users u ON u.id = ur.user_id
		WHERE ur.created_at >= NOW() - $1::interval
		  AND ur.status <> 'success'
		  AND ($2 = '' OR ur.model = $2)
		  AND ($3 = '' OR ak.group_id = $3)
		GROUP BY ur.model, COALESCE(ak.group_id, '')
	`, fmt.Sprintf("%f seconds", window.Seconds()), model, group)
	if err != nil {
		r.logger.Warn("Failed to list upstream ops impact", zap.Error(err))
		impactRows = nil
	}
	impactMap := make(map[string]gin.H)
	if impactRows != nil {
		defer impactRows.Close()
		for impactRows.Next() {
			var m, g string
			var users, enterprises int
			if err := impactRows.Scan(&m, &g, &users, &enterprises); err != nil {
				continue
			}
			impactMap[m+"|"+g] = gin.H{"impacted_users": users, "impacted_enterprises": enterprises}
		}
	}

	result := []gin.H{}
	for rows.Next() {
		var m, g, gn string
		var failures, timeout, rateLimit, err5xx, err4xx, auth, network, respTimeout, contentMismatch, invalidRequest int
		if err := rows.Scan(&m, &g, &gn, &failures, &timeout, &rateLimit, &err5xx, &err4xx, &auth, &network, &respTimeout, &contentMismatch, &invalidRequest); err != nil {
			continue
		}
		impact := gin.H{"impacted_users": 0, "impacted_enterprises": 0}
		if v, ok := impactMap[m+"|"+g]; ok {
			impact = v
		}
		result = append(result, gin.H{
			"model":                m,
			"group_id":             g,
			"group_name":           gn,
			"failures":             failures,
			"timeout":              timeout,
			"rate_limit":           rateLimit,
			"error_5xx":            err5xx,
			"error_4xx":            err4xx,
			"auth_error":           auth,
			"network_error":        network,
			"response_timeout":     respTimeout,
			"content_mismatch":     contentMismatch,
			"invalid_request":      invalidRequest,
			"impacted_users":       impact["impacted_users"],
			"impacted_enterprises": impact["impacted_enterprises"],
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": result, "period": period})
}

func (r *AdminRoutes) exportUpstreamOpsHealth(c *gin.Context) {
	view := c.DefaultQuery("view", "providers")
	period := normalizeUpstreamOpsPeriod(c.DefaultQuery("period", "24h"))

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=upstream-ops-"+view+"-"+time.Now().UTC().Format("200601021504")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(c.Writer)
	defer w.Flush()

	if view == "model-group" {
		_ = w.Write([]string{"model", "group_id", "group_name", "request_count", "success_count", "failure_count", "avg_latency_ms", "availability", "status"})
		rows, err := query.ListModelGroupHealth(c.Request.Context(), r.db, period, "", "", nil)
		if err == nil {
			for _, row := range rows {
				_ = w.Write([]string{
					row.Model,
					row.GroupID,
					row.GroupName,
					strconv.Itoa(row.RequestCount),
					strconv.Itoa(row.SuccessCount),
					strconv.Itoa(row.FailureCount),
					strconv.Itoa(row.AvgLatencyMs),
					fmt.Sprintf("%.2f", row.Availability),
					mapInternalStatus(row.Status),
				})
			}
		}
		return
	}

	if view == "anomalies" {
		window, _ := query.ParsePeriod(period)
		_ = w.Write([]string{"model", "group_id", "impacted_users", "impacted_enterprises", "timeout", "rate_limit", "error_5xx", "error_4xx", "auth_error", "network_error", "response_timeout", "content_mismatch", "invalid_request"})

		// Build impact lookup from usage_records.
		impactMap := make(map[string]gin.H)
		impactRows, errImpact := r.db.Query(c.Request.Context(), `
			SELECT ur.model, COALESCE(ak.group_id, '') as group_id,
				COUNT(DISTINCT ur.user_id) as impacted_users,
				COUNT(DISTINCT u.enterprise_id) as impacted_enterprises
			FROM usage_records ur
			LEFT JOIN api_keys ak ON ak.id = ur.user_api_key_id
			LEFT JOIN users u ON u.id = ur.user_id
			WHERE ur.created_at >= NOW() - $1::interval
			  AND ur.status <> 'success'
			GROUP BY ur.model, COALESCE(ak.group_id, '')
		`, fmt.Sprintf("%f seconds", window.Seconds()))
		if errImpact == nil {
			defer impactRows.Close()
			for impactRows.Next() {
				var m, g string
				var users, enterprises int
				if err := impactRows.Scan(&m, &g, &users, &enterprises); err != nil {
					continue
				}
				impactMap[m+"|"+g] = gin.H{"impacted_users": users, "impacted_enterprises": enterprises}
			}
		}

		rows, err := r.db.Query(c.Request.Context(), `
			SELECT model, group_id,
				COALESCE(SUM(timeout_count), 0),
				COALESCE(SUM(rate_limit_count), 0),
				COALESCE(SUM(error_5xx_count), 0),
				COALESCE(SUM(error_4xx_count), 0),
				COALESCE(SUM(auth_error_count), 0),
				COALESCE(SUM(network_error_count), 0),
				COALESCE(SUM(response_timeout_count), 0),
				COALESCE(SUM(content_mismatch_count), 0),
				COALESCE(SUM(invalid_request_count), 0)
			FROM model_group_health_stats
			WHERE bucket >= NOW() - $1::interval
			GROUP BY model, group_id
			ORDER BY model, group_id
		`, fmt.Sprintf("%f seconds", window.Seconds()))
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var m, g string
				var timeout, rateLimit, err5xx, err4xx, auth, network, respTimeout, contentMismatch, invalidRequest int
				if err := rows.Scan(&m, &g, &timeout, &rateLimit, &err5xx, &err4xx, &auth, &network, &respTimeout, &contentMismatch, &invalidRequest); err != nil {
					continue
				}
				impact := gin.H{"impacted_users": 0, "impacted_enterprises": 0}
				if v, ok := impactMap[m+"|"+g]; ok {
					impact = v
				}
				_ = w.Write([]string{
					m, g,
					fmt.Sprintf("%v", impact["impacted_users"]),
					fmt.Sprintf("%v", impact["impacted_enterprises"]),
					strconv.Itoa(timeout), strconv.Itoa(rateLimit),
					strconv.Itoa(err5xx), strconv.Itoa(err4xx),
					strconv.Itoa(auth), strconv.Itoa(network),
					strconv.Itoa(respTimeout), strconv.Itoa(contentMismatch),
					strconv.Itoa(invalidRequest),
				})
			}
		}
		return
	}

	_ = w.Write([]string{"provider", "model", "request_count", "success_count", "failure_count", "avg_latency_ms", "availability", "status"})
	rows, err := query.ListProviderModelHealth(c.Request.Context(), r.db, period, "", "")
	if err == nil {
		for _, row := range rows {
			_ = w.Write([]string{
				row.Provider,
				row.Model,
				strconv.Itoa(row.RequestCount),
				strconv.Itoa(row.SuccessCount),
				strconv.Itoa(row.FailureCount),
				strconv.Itoa(row.AvgLatencyMs),
				fmt.Sprintf("%.2f", row.Availability),
				mapInternalStatus(row.Status),
			})
		}
	}
}

// listUpstreamOpsMonitorKeys returns key-level metrics for the key ops console.
func (r *AdminRoutes) listUpstreamOpsMonitorKeys(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	period := normalizeUpstreamOpsPeriod(c.DefaultQuery("period", "7d"))
	if !isValidUpstreamOpsPeriod(period) {
		period = "7d"
	}

	windowDays := 7
	switch period {
	case "24h":
		windowDays = 1
	case "15d":
		windowDays = 15
	case "30d":
		windowDays = 30
	case "90d":
		windowDays = 90
	}

	rows, err := query.ListUpstreamKeyMetrics(c.Request.Context(), r.db, period, page, pageSize)
	if err != nil {
		r.logger.Warn("Failed to list upstream key metrics", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch key metrics"}})
		return
	}

	result := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		rpmUtil := 0.0
		if row.RPMLimit > 0 {
			rpmUtil = float64(row.RequestCount) / (float64(row.RPMLimit) * 1440.0 * float64(windowDays)) * 100.0
		}
		tpmUtil := 0.0
		if row.TPMLimit > 0 {
			tpmUtil = float64(row.TokenCount) / (float64(row.TPMLimit) * 1440.0 * float64(windowDays)) * 100.0
		}
		quotaUtil := 0.0
		if row.QuotaLimit > 0 {
			quotaUtil = float64(row.QuotaUsed) / float64(row.QuotaLimit) * 100.0
		}
		result = append(result, gin.H{
			"id":                 row.ID,
			"provider":           row.Provider,
			"key_preview":        row.KeyPreview,
			"enabled":            row.Enabled,
			"validation_status":  row.ValidationStatus,
			"last_validated_at":  row.LastValidatedAt,
			"rpm_limit":          row.RPMLimit,
			"tpm_limit":          row.TPMLimit,
			"quota_limit":        row.QuotaLimit,
			"quota_used":         row.QuotaUsed,
			"request_count":      row.RequestCount,
			"error_count":        row.ErrorCount,
			"token_count":        row.TokenCount,
			"avg_latency_ms":     row.AvgLatencyMs,
			"rpm_utilization":    round2(rpmUtil),
			"tpm_utilization":    round2(tpmUtil),
			"quota_utilization":  round2(quotaUtil),
			"health_status":      mapInternalStatus(query.DeriveInternalStatus("", row.RequestCount, row.ErrorCount, 0, row.AvgLatencyMs)),
			"latency_trend":      json.RawMessage(row.LatencyTrendJSON),
			"error_distribution": json.RawMessage(row.ErrorDistJSON),
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": result, "page": page, "page_size": pageSize, "period": period})
}

// exportUpstreamMonitorKeys exports key-level metrics for the selected period as CSV.
func (r *AdminRoutes) exportUpstreamMonitorKeys(c *gin.Context) {
	period := normalizeUpstreamOpsPeriod(c.DefaultQuery("period", "7d"))
	if !isValidUpstreamOpsPeriod(period) {
		period = "7d"
	}

	windowDays := 7
	switch period {
	case "24h":
		windowDays = 1
	case "15d":
		windowDays = 15
	case "30d":
		windowDays = 30
	case "90d":
		windowDays = 90
	}

	rows, err := query.ListUpstreamKeyMetrics(c.Request.Context(), r.db, period, 1, 5000)
	if err != nil {
		r.logger.Warn("Failed to export upstream key metrics", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export"}})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=upstream-keys-"+period+"-"+time.Now().UTC().Format("200601021504")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"Provider", "Key Preview", "Enabled", "Validation Status", "Requests", "Tokens", "Errors", "Error Rate (%)", "Avg Latency (ms)", "RPM Util (%)", "TPM Util (%)", "Quota Util (%)", "Health Status"})

	for _, k := range rows {
		errRate := 0.0
		if k.RequestCount > 0 {
			errRate = round2(float64(k.ErrorCount) / float64(k.RequestCount) * 100)
		}
		rpmUtil := 0.0
		if k.RPMLimit > 0 {
			rpmUtil = float64(k.RequestCount) / (float64(k.RPMLimit) * float64(windowDays) * 1440.0) * 100.0
		}
		tpmUtil := 0.0
		if k.TPMLimit > 0 {
			tpmUtil = float64(k.TokenCount) / (float64(k.TPMLimit) * float64(windowDays) * 1440.0) * 100.0
		}
		quotaUtil := 0.0
		if k.QuotaLimit > 0 {
			quotaUtil = float64(k.QuotaUsed) / float64(k.QuotaLimit) * 100.0
		}
		health := mapInternalStatus(query.DeriveInternalStatus("", k.RequestCount, k.ErrorCount, 0, k.AvgLatencyMs))
		_ = w.Write([]string{
			k.Provider,
			k.KeyPreview,
			strconv.FormatBool(k.Enabled),
			k.ValidationStatus,
			strconv.Itoa(k.RequestCount),
			strconv.FormatInt(k.TokenCount, 10),
			strconv.Itoa(k.ErrorCount),
			fmt.Sprintf("%.2f", errRate),
			strconv.Itoa(k.AvgLatencyMs),
			fmt.Sprintf("%.2f", rpmUtil),
			fmt.Sprintf("%.2f", tpmUtil),
			fmt.Sprintf("%.2f", quotaUtil),
			health,
		})
	}
	w.Flush()
}

// exportDownstreamMonitor exports downstream key metrics for the selected period/filters as CSV.
func (r *AdminRoutes) exportDownstreamMonitor(c *gin.Context) {
	period := normalizeUpstreamOpsPeriod(c.DefaultQuery("period", "7d"))
	if !isValidUpstreamOpsPeriod(period) {
		period = "7d"
	}
	search := c.Query("search")
	status := c.Query("status")
	keyType := c.Query("type")
	sort := c.Query("sort")

	data, err := query.GetDownstreamKeyMonitorData(c.Request.Context(), r.db, period, search, status, keyType, sort, 1, 5000)
	if err != nil {
		r.logger.Warn("Failed to export downstream monitor", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export"}})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=downstream-keys-"+period+"-"+time.Now().UTC().Format("200601021504")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"Prefix", "User", "Type", "Group", "Current Model", "Requests", "Tokens", "Errors", "Error Rate (%)", "Avg Latency (ms)", "RPM Util (%)", "TPM Util (%)", "Status", "Last Used At"})

	for _, k := range data.Keys {
		lastUsed := ""
		if k.LastUsedAt != nil {
			lastUsed = k.LastUsedAt.Format("2006-01-02 15:04:05")
		}
		_ = w.Write([]string{
			k.KeyPrefix,
			k.UserName,
			k.KeyType,
			k.GroupName,
			k.CurrentModel,
			strconv.FormatInt(k.RequestCount, 10),
			strconv.FormatInt(k.TokenCount, 10),
			strconv.FormatInt(k.ErrorCount, 10),
			fmt.Sprintf("%.2f", k.ErrorRate),
			strconv.Itoa(k.AvgLatencyMs),
			fmt.Sprintf("%.2f", k.RPMUtilization),
			fmt.Sprintf("%.2f", k.TPMUtilization),
			k.Status,
			lastUsed,
		})
	}
	w.Flush()
}

// getUpstreamOpsHistory returns per-bucket history for the upstream-ops trend charts.
func (r *AdminRoutes) getUpstreamOpsHistory(c *gin.Context) {
	period := normalizeUpstreamOpsPeriod(c.DefaultQuery("period", "24h"))
	view := c.DefaultQuery("view", "model-group")

	var history map[string][]query.HealthHistoryPoint
	var err error

	switch view {
	case "providers":
		provider := c.Query("provider")
		model := c.Query("model")
		history, err = query.ProviderModelHealthHistory(c.Request.Context(), r.db, period, provider, model)
	default:
		model := c.Query("model")
		group := c.Query("group")
		history, err = query.ModelGroupHealthHistory(c.Request.Context(), r.db, period, model, group, nil)
	}

	if err != nil {
		r.logger.Warn("Failed to list upstream ops history", zap.String("view", view), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch history"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"meta": gin.H{
			"period":     period,
			"view":       view,
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
		"data": history,
	})
}

// mapInternalStatus maps the internal healthy/degraded/unhealthy/unknown status to the public taxonomy.
func mapInternalStatus(internal string) string {
	switch internal {
	case "healthy":
		return "available"
	case "degraded":
		return "degraded"
	case "unhealthy":
		return "unavailable"
	case "unknown":
		// Had traffic but not enough model-attributable requests to judge.
		// Distinct from "missing" (no data / no keys at all).
		return "unknown"
	default:
		return "missing"
	}
}
