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

// rateLimitWindow returns the PostgreSQL interval and window length in minutes
// for supported rate-limit views. Defaults to 1 hour.
func rateLimitWindow(c *gin.Context) (interval string, minutes float64) {
	switch c.Query("window") {
	case "24h":
		return "24 hours", 24 * 60
	case "7d":
		return "7 days", 7 * 24 * 60
	default:
		return "1 hour", 60
	}
}

// getRateLimitUsage returns rate limit usage overview for admin dashboard
func (r *AdminRoutes) getRateLimitUsage(c *gin.Context) {
	ctx := c.Request.Context()
	interval, _ := rateLimitWindow(c)

	rows, err := r.db.Query(ctx,
		`SELECT u.id::text, u.name, u.email, u.role,
		        COALESCE(ur.request_count, 0) as request_count,
		        COALESCE(ur.total_tokens, 0) as total_tokens,
		        COALESCE(ur.total_cost, 0) as total_cost,
		        us.rpm_limit, us.rph_limit, us.rpd_limit, us.rpw_limit, us.rp5h_limit,
		        us.tpm_limit, us.tph_limit, us.tpd_limit, us.tpw_limit, us.tpmonth_limit, us.concurrent_limit,
		        us.rpm_used, us.rph_used, us.rpw_used, us.rp5h_used,
		        us.tpm_used, us.tph_used, us.tpw_used, us.daily_quota_used,
		        us.quota_used, us.quota_remaining,
		        us.rpm_reset_at, us.rph_reset_at, us.rpw_reset_at, us.rp5h_reset_at,
		        us.tpm_reset_at, us.tph_reset_at, us.tpw_reset_at
		 FROM users u
		 LEFT JOIN LATERAL (
		     SELECT COUNT(*) as request_count,
		            COALESCE(SUM(input_tokens + output_tokens), 0) as total_tokens,
		            COALESCE(SUM(total_cost), 0) as total_cost
		     FROM usage_records
		     WHERE user_id = u.id AND created_at > NOW() - INTERVAL '`+interval+`'
		 ) ur ON true
		 LEFT JOIN LATERAL (
		     SELECT *
		     FROM user_subscriptions
		     WHERE user_id = u.id AND status = 'active' AND expires_at > NOW()
		     ORDER BY created_at DESC LIMIT 1
		 ) us ON true
		 WHERE u.deleted_at IS NULL
		 ORDER BY request_count DESC
		 LIMIT 100`)
	if err != nil {
		r.logger.Warn("Failed to get rate limit usage", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get usage data"}})
		return
	}
	defer rows.Close()

	users := []gin.H{}
	for rows.Next() {
		var id, name, email, role string
		var requestCount, totalTokens int
		var totalCost int64
		var rpmLimit, rphLimit, rpdLimit, rpwLimit, rp5hLimit, concurrentLimit *int
		var tpmLimit, tphLimit, tpdLimit, tpwLimit, tpmonthLimit *int
		var rpmUsed, rphUsed, rpwUsed, rp5hUsed, tpmUsed, tphUsed, tpwUsed *int
		var dailyQuotaUsed, quotaUsed, quotaRemaining *int64
		var rpmResetAt, rphResetAt, rpwResetAt, rp5hResetAt, tpmResetAt, tphResetAt, tpwResetAt *time.Time
		_ = rows.Scan(&id, &name, &email, &role,
			&requestCount, &totalTokens, &totalCost,
			&rpmLimit, &rphLimit, &rpdLimit, &rpwLimit, &rp5hLimit,
			&tpmLimit, &tphLimit, &tpdLimit, &tpwLimit, &tpmonthLimit, &concurrentLimit,
			&rpmUsed, &rphUsed, &rpwUsed, &rp5hUsed,
			&tpmUsed, &tphUsed, &tpwUsed, &dailyQuotaUsed,
			&quotaUsed, &quotaRemaining,
			&rpmResetAt, &rphResetAt, &rpwResetAt, &rp5hResetAt,
			&tpmResetAt, &tphResetAt, &tpwResetAt)
		limits := gin.H{
			"rpm": rpmLimit, "rph": rphLimit, "rpd": rpdLimit, "rpw": rpwLimit, "rp5h": rp5hLimit, "rpmonth": nil,
			"tpm": tpmLimit, "tph": tphLimit, "tpd": tpdLimit, "tpw": tpwLimit, "tpmonth": tpmonthLimit,
			"concurrent": concurrentLimit,
		}
		used := gin.H{
			"rpm": rpmUsed, "rph": rphUsed, "rpw": rpwUsed, "rp5h": rp5hUsed,
			"tpm": tpmUsed, "tph": tphUsed, "tpw": tpwUsed, "daily_quota": dailyQuotaUsed,
		}
		util, status := computeUserUtilization(limits, used, quotaUsed, quotaRemaining)
		users = append(users, gin.H{
			"id": id, "name": name, "email": email, "role": role,
			"request_count": requestCount, "total_tokens": totalTokens, "total_cost": QuotaToUSDFloat(totalCost),
			"limits": limits,
			"used":   used,
			"resets": gin.H{
				"rpm": rpmResetAt, "rph": rphResetAt, "rpw": rpwResetAt, "rp5h": rp5hResetAt,
				"tpm": tpmResetAt, "tph": tphResetAt, "tpw": tpwResetAt,
			},
			"quota": gin.H{
				"used":      quotaUsed,
				"remaining": quotaRemaining,
			},
			"utilization": util,
			"status":      status,
		})
	}

	limitedCount := 0
	for _, u := range users {
		if u["status"] == "limited" {
			limitedCount++
		}
	}

	// Get overall stats
	var totalRequests int
	var totalTokens int
	var totalCost int64
	_ = r.db.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(input_tokens + output_tokens), 0), COALESCE(SUM(total_cost), 0)
		 FROM usage_records WHERE created_at > NOW() - INTERVAL '`+interval+`'`,
	).Scan(&totalRequests, &totalTokens, &totalCost)

	c.JSON(http.StatusOK, gin.H{
		"users": users,
		"summary": gin.H{
			"total_requests": totalRequests,
			"total_tokens":   totalTokens,
			"total_cost":     QuotaToUSDFloat(totalCost),
			"period":         interval,
			"total_users":    len(users),
			"limited_count":  limitedCount,
		},
	})
}

func (r *AdminRoutes) getRateLimitGroups(c *gin.Context) {
	ctx := c.Request.Context()
	interval, minutes := rateLimitWindow(c)

	rows, err := r.db.Query(ctx,
		`SELECT g.id::text, g.name,
		        g.rpm, g.rph, g.rp5h, g.rpw, g.rpmonth,
		        g.tpm, g.tph, g.tpd, g.tpw, g.tpmonth, g.concurrent_limit,
		        COALESCE(SUM(ur.request_count), 0)::int as requests,
		        COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tokens
		 FROM user_groups g
		 LEFT JOIN usage_records ur ON ur.group_id = g.id AND ur.created_at > NOW() - INTERVAL '`+interval+`'
		 WHERE g.deleted_at IS NULL
		 GROUP BY g.id, g.name, g.rpm, g.rph, g.rp5h, g.rpw, g.rpmonth,
		          g.tpm, g.tph, g.tpd, g.tpw, g.tpmonth, g.concurrent_limit
		 ORDER BY requests DESC
		 LIMIT 500`)
	if err != nil {
		r.logger.Warn("Failed to get group rate limits", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get group rate limits"}})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, name string
		var rpm, rph, rp5h, rpw, rpmonth *int64
		var tpm, tph, tpd, tpw, tpmonth *int64
		var concurrent *int
		var requests int
		var tokens int64
		_ = rows.Scan(&id, &name,
			&rpm, &rph, &rp5h, &rpw, &rpmonth,
			&tpm, &tph, &tpd, &tpw, &tpmonth, &concurrent,
			&requests, &tokens)

		limits := gin.H{
			"rpm": rpm, "rph": rph, "rp5h": rp5h, "rpw": rpw, "rpmonth": rpmonth,
			"tpm": tpm, "tph": tph, "tpd": tpd, "tpw": tpw, "tpmonth": tpmonth,
			"concurrent": concurrent,
		}
		usage := gin.H{"requests": requests, "tokens": tokens}
		util, status := computeGroupUtilization(rpm, rph, rp5h, rpw, rpmonth, tpm, tph, tpd, tpw, tpmonth, concurrent, requests, tokens, minutes)
		items = append(items, gin.H{
			"id": id, "name": name,
			"limits": limits, "usage": usage, "utilization": util, "status": status,
		})
	}

	limitedCount := 0
	for _, it := range items {
		if it["status"] == "limited" {
			limitedCount++
		}
	}

	var totalRequests int
	var totalTokens int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(request_count),0), COALESCE(SUM(input_tokens+output_tokens),0)
		 FROM usage_records WHERE created_at > NOW() - INTERVAL '`+interval+`' AND group_id IS NOT NULL`).
		Scan(&totalRequests, &totalTokens)

	c.JSON(http.StatusOK, gin.H{
		"groups": items,
		"summary": gin.H{
			"total_groups":   len(items),
			"total_requests": totalRequests,
			"total_tokens":   totalTokens,
			"period":         interval,
			"limited_count":  limitedCount,
		},
	})
}

func (r *AdminRoutes) getRateLimitUpstreamKeys(c *gin.Context) {
	ctx := c.Request.Context()
	interval, minutes := rateLimitWindow(c)

	rows, err := r.db.Query(ctx,
		`SELECT k.id::text, k.provider, k.key_preview,
		        k.rpm_limit, k.tpm_limit, k.quota_limit, k.quota_used, k.enabled,
		        COALESCE(SUM(ur.request_count), 0)::int as requests,
		        COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tokens
		 FROM upstream_keys k
		 LEFT JOIN usage_records ur ON ur.upstream_key_id = k.id::text AND ur.created_at > NOW() - INTERVAL '`+interval+`'
		 WHERE k.deleted_at IS NULL
		 GROUP BY k.id, k.provider, k.key_preview, k.rpm_limit, k.tpm_limit, k.quota_limit, k.quota_used, k.enabled
		 ORDER BY requests DESC
		 LIMIT 500`)
	if err != nil {
		r.logger.Warn("Failed to get upstream key rate limits", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get upstream key rate limits"}})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, provider, keyPreview string
		var rpmLimit, tpmLimit *int
		var quotaLimit, quotaUsed *int64
		var enabled bool
		var requests int
		var tokens int64
		_ = rows.Scan(&id, &provider, &keyPreview,
			&rpmLimit, &tpmLimit, &quotaLimit, &quotaUsed, &enabled,
			&requests, &tokens)

		name := keyPreview
		if name == "" {
			name = provider
		}
		limits := gin.H{"rpm": rpmLimit, "tpm": tpmLimit, "quota": quotaLimit}
		usage := gin.H{"requests": requests, "tokens": tokens}
		util, status := computeUpstreamKeyUtilization(rpmLimit, tpmLimit, quotaLimit, quotaUsed, requests, tokens, minutes)
		items = append(items, gin.H{
			"id": id, "name": name, "provider": provider, "enabled": enabled,
			"limits": limits, "usage": usage, "utilization": util, "status": status,
		})
	}

	limitedCount := 0
	for _, it := range items {
		if it["status"] == "limited" {
			limitedCount++
		}
	}

	var totalRequests int
	var totalTokens int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(request_count),0), COALESCE(SUM(input_tokens+output_tokens),0)
		 FROM usage_records WHERE created_at > NOW() - INTERVAL '`+interval+`' AND upstream_key_id IS NOT NULL`).
		Scan(&totalRequests, &totalTokens)

	c.JSON(http.StatusOK, gin.H{
		"upstream_keys": items,
		"summary": gin.H{
			"total_keys":     len(items),
			"total_requests": totalRequests,
			"total_tokens":   totalTokens,
			"period":         interval,
			"limited_count":  limitedCount,
		},
	})
}

func (r *AdminRoutes) getRateLimitAPIKeys(c *gin.Context) {
	ctx := c.Request.Context()
	interval, minutes := rateLimitWindow(c)

	rows, err := r.db.Query(ctx,
		`SELECT k.id::text, k.name, k.key_prefix, k.status, k.key_type,
		        k.rate_limit_rpm, k.rate_limit_tpm,
		        g.rpm as group_rpm, g.tpm as group_tpm,
		        COALESCE(SUM(ur.request_count), 0)::int as requests,
		        COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tokens
		 FROM api_keys k
		 LEFT JOIN user_groups g ON k.group_id = g.id
		 LEFT JOIN usage_records ur ON ur.user_api_key_id = k.id AND ur.created_at > NOW() - INTERVAL '`+interval+`'
		 WHERE k.deleted_at IS NULL
		 GROUP BY k.id, k.name, k.key_prefix, k.status, k.key_type, k.rate_limit_rpm, k.rate_limit_tpm, g.rpm, g.tpm
		 ORDER BY requests DESC
		 LIMIT 500`)
	if err != nil {
		r.logger.Warn("Failed to get api key rate limits", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get api key rate limits"}})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, name, keyPrefix, status, keyType string
		var rpmLimit, tpmLimit, groupRPMLimit, groupTPMLimit *int
		var requests int
		var tokens int64
		_ = rows.Scan(&id, &name, &keyPrefix, &status, &keyType,
			&rpmLimit, &tpmLimit,
			&groupRPMLimit, &groupTPMLimit,
			&requests, &tokens)

		displayName := keyPrefix

		// Resolve display limits: 0 means "use system default"
		displayRPM := rpmLimit
		if displayRPM == nil || *displayRPM <= 0 {
			v := DefaultUserRPM(r.db)
			displayRPM = &v
		}
		displayTPM := tpmLimit
		if displayTPM == nil || *displayTPM <= 0 {
			v := DefaultUserTPM(r.db)
			displayTPM = &v
		}
		limits := gin.H{"rpm": displayRPM, "tpm": displayTPM}
		groupLimits := gin.H{"rpm": groupRPMLimit, "tpm": groupTPMLimit}

		// Effective limit: key > 0 ? min(key, group) : group > 0 ? group : system default.
		effectiveRPM := rpmLimit
		if effectiveRPM == nil || *effectiveRPM <= 0 {
			if groupRPMLimit != nil && *groupRPMLimit > 0 {
				effectiveRPM = groupRPMLimit
			}
		} else if groupRPMLimit != nil && *groupRPMLimit > 0 && *groupRPMLimit < *effectiveRPM {
			effectiveRPM = groupRPMLimit
		}
		if effectiveRPM == nil || *effectiveRPM <= 0 {
			v := DefaultUserRPM(r.db)
			effectiveRPM = &v
		}

		effectiveTPM := tpmLimit
		if effectiveTPM == nil || *effectiveTPM <= 0 {
			if groupTPMLimit != nil && *groupTPMLimit > 0 {
				effectiveTPM = groupTPMLimit
			}
		} else if groupTPMLimit != nil && *groupTPMLimit > 0 && *groupTPMLimit < *effectiveTPM {
			effectiveTPM = groupTPMLimit
		}
		if effectiveTPM == nil || *effectiveTPM <= 0 {
			v := DefaultUserTPM(r.db)
			effectiveTPM = &v
		}
		effective := gin.H{"rpm": effectiveRPM, "tpm": effectiveTPM}
		usage := gin.H{"requests": requests, "tokens": tokens}
		util, itemStatus := computeAPIKeyUtilization(effectiveRPM, effectiveTPM, requests, tokens, minutes)
		items = append(items, gin.H{
			"id": id, "name": displayName, "key_prefix": keyPrefix, "status": status, "key_type": keyType,
			"limits": limits, "group_limits": groupLimits, "effective_limits": effective,
			"usage": usage, "utilization": util, "item_status": itemStatus,
		})
	}

	limitedCount := 0
	for _, it := range items {
		if it["item_status"] == "limited" {
			limitedCount++
		}
	}

	var totalRequests int
	var totalTokens int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(request_count),0), COALESCE(SUM(input_tokens+output_tokens),0)
		 FROM usage_records WHERE created_at > NOW() - INTERVAL '`+interval+`' AND user_api_key_id IS NOT NULL`).
		Scan(&totalRequests, &totalTokens)

	c.JSON(http.StatusOK, gin.H{
		"api_keys": items,
		"summary": gin.H{
			"total_keys":     len(items),
			"total_requests": totalRequests,
			"total_tokens":   totalTokens,
			"period":         interval,
			"limited_count":  limitedCount,
		},
	})
}

func computeAPIKeyUtilization(rpmLimit, tpmLimit *int, requests int, tokens int64, minutes float64) (gin.H, string) {
	util := gin.H{}
	maxUtil := 0.0
	if rpmLimit != nil && *rpmLimit > 0 {
		u := float64(requests) / (float64(*rpmLimit) * minutes)
		util["rpm"] = u
		if u > maxUtil {
			maxUtil = u
		}
	}
	if tpmLimit != nil && *tpmLimit > 0 {
		u := float64(tokens) / (float64(*tpmLimit) * minutes)
		util["tpm"] = u
		if u > maxUtil {
			maxUtil = u
		}
	}
	return util, rateLimitStatus(maxUtil)
}

func computeGroupUtilization(rpm, rph, rp5h, rpw, rpmonth *int64, tpm, tph, tpd, tpw, tpmonth *int64, concurrent *int, requests int, tokens int64, minutes float64) (gin.H, string) {
	util := gin.H{}
	maxUtil := 0.0
	setUtil := func(name string, limit float64, used float64, periodMinutes float64) {
		if limit <= 0 {
			util[name] = nil
			return
		}
		// normalize used to the limit's period so utilization is comparable across windows
		normalizedUsed := used * (periodMinutes / minutes)
		u := normalizedUsed / limit
		util[name] = u
		if u > maxUtil {
			maxUtil = u
		}
	}
	setUtil("rpm", float64(ptrInt64(rpm)), float64(requests), 1)
	setUtil("rph", float64(ptrInt64(rph)), float64(requests), 60)
	setUtil("rp5h", float64(ptrInt64(rp5h)), float64(requests), 300)
	setUtil("rpw", float64(ptrInt64(rpw)), float64(requests), 7*24*60)
	setUtil("rpmonth", float64(ptrInt64(rpmonth)), float64(requests), 30*24*60)
	setUtil("tpm", float64(ptrInt64(tpm)), float64(tokens), 1)
	setUtil("tph", float64(ptrInt64(tph)), float64(tokens), 60)
	setUtil("tpd", float64(ptrInt64(tpd)), float64(tokens), 24*60)
	setUtil("tpw", float64(ptrInt64(tpw)), float64(tokens), 7*24*60)
	setUtil("tpmonth", float64(ptrInt64(tpmonth)), float64(tokens), 30*24*60)
	if concurrent != nil && *concurrent > 0 {
		util["concurrent"] = nil
	}
	return util, rateLimitStatus(maxUtil)
}

func computeUpstreamKeyUtilization(rpmLimit, tpmLimit *int, quotaLimit, quotaUsed *int64, requests int, tokens int64, minutes float64) (gin.H, string) {
	util := gin.H{}
	maxUtil := 0.0
	if rpmLimit != nil && *rpmLimit > 0 {
		u := float64(requests) / (float64(*rpmLimit) * minutes)
		util["rpm"] = u
		if u > maxUtil {
			maxUtil = u
		}
	}
	if tpmLimit != nil && *tpmLimit > 0 {
		u := float64(tokens) / (float64(*tpmLimit) * minutes)
		util["tpm"] = u
		if u > maxUtil {
			maxUtil = u
		}
	}
	if quotaLimit != nil && *quotaLimit > 0 && quotaUsed != nil {
		u := float64(*quotaUsed) / float64(*quotaLimit)
		util["quota"] = u
		if u > maxUtil {
			maxUtil = u
		}
	}
	return util, rateLimitStatus(maxUtil)
}

func rateLimitStatus(util float64) string {
	if util >= 1.0 {
		return "limited"
	}
	if util >= 0.8 {
		return "warning"
	}
	if util >= 0.6 {
		return "caution"
	}
	return "healthy"
}

func computeUserUtilization(limits, used gin.H, quotaUsed, quotaRemaining *int64) (gin.H, string) {
	util := gin.H{}
	maxUtil := 0.0
	setUtil := func(name string, limit, usedVal *int) {
		if limit == nil || usedVal == nil || *limit <= 0 {
			util[name] = nil
			return
		}
		u := float64(*usedVal) / float64(*limit)
		util[name] = u
		if u > maxUtil {
			maxUtil = u
		}
	}
	setUtil("rpm", limits["rpm"].(*int), used["rpm"].(*int))
	setUtil("rph", limits["rph"].(*int), used["rph"].(*int))
	setUtil("rp5h", limits["rp5h"].(*int), used["rp5h"].(*int))
	setUtil("rpw", limits["rpw"].(*int), used["rpw"].(*int))
	setUtil("tpm", limits["tpm"].(*int), used["tpm"].(*int))
	setUtil("tph", limits["tph"].(*int), used["tph"].(*int))
	setUtil("tpw", limits["tpw"].(*int), used["tpw"].(*int))
	// Monthly limits are shown but not tracked in subscription used counters;
	// utilization is intentionally left nil so the UI displays the limit only.
	if limits["rpmonth"] != nil {
		util["rpmonth"] = nil
	}
	if limits["tpmonth"] != nil {
		util["tpmonth"] = nil
	}

	if limits["concurrent"] != nil {
		util["concurrent"] = nil
	}

	if quotaUsed != nil && quotaRemaining != nil {
		total := *quotaUsed + *quotaRemaining
		if total > 0 {
			u := float64(*quotaUsed) / float64(total)
			util["quota"] = u
			if u > maxUtil {
				maxUtil = u
			}
		}
	}

	return util, rateLimitStatus(maxUtil)
}

func ptrInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// ============================================
// Upstream Usage Statistics
// ============================================

func (r *AdminRoutes) listUpstreamUsage(c *gin.Context) {
	ctx := c.Request.Context()

	// Query params
	provider := c.Query("provider")
	model := c.Query("model")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")
	page := 1
	limit := 50
	if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 0 {
		page = p
	}
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 1000 {
		limit = l
	}
	offset := (page - 1) * limit

	// Build query
	where := []string{"1=1"}
	args := []interface{}{}
	argIdx := 1

	if provider != "" {
		where = append(where, fmt.Sprintf("u.provider = $%d", argIdx))
		args = append(args, provider)
		argIdx++
	}
	if model != "" {
		where = append(where, fmt.Sprintf("u.model = $%d", argIdx))
		args = append(args, model)
		argIdx++
	}
	if dateFrom != "" {
		where = append(where, fmt.Sprintf("u.date >= $%d", argIdx))
		args = append(args, dateFrom)
		argIdx++
	}
	if dateTo != "" {
		where = append(where, fmt.Sprintf("u.date <= $%d", argIdx))
		args = append(args, dateTo)
		argIdx++
	}

	whereClause := strings.Join(where, " AND ")

	// Count
	var total int
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM upstream_key_usage u WHERE %s", whereClause)
	_ = r.db.QueryRow(ctx, countSQL, args...).Scan(&total)

	// Data
	querySQL := fmt.Sprintf(`
		SELECT u.id, u.upstream_key_id, u.provider, u.model, u.date,
			u.request_count, u.input_tokens, u.output_tokens,
			u.cache_read_tokens, u.cache_creation_tokens, u.error_count,
			u.avg_latency_ms, u.created_at, u.updated_at,
			k.key_preview
		FROM upstream_key_usage u
		LEFT JOIN upstream_keys k ON k.id = u.upstream_key_id
		WHERE %s
		ORDER BY u.date DESC, u.provider, u.model
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, querySQL, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query upstream usage"}})
		return
	}
	defer rows.Close()

	results := []gin.H{}
	for rows.Next() {
		var id int64
		var keyID, prov, mdl string
		var dt time.Time
		var reqCount, errorCount, avgLatency int
		var inputTokens, outputTokens, cacheRead, cacheCreation int64
		var createdAt, updatedAt time.Time
		var keyPreview *string
		if err := rows.Scan(&id, &keyID, &prov, &mdl, &dt,
			&reqCount, &inputTokens, &outputTokens,
			&cacheRead, &cacheCreation, &errorCount,
			&avgLatency, &createdAt, &updatedAt, &keyPreview); err != nil {
			r.logger.Warn("Failed to scan upstream usage row", zap.Error(err))
			continue
		}
		results = append(results, gin.H{
			"id": id, "upstream_key_id": keyID, "provider": prov, "model": mdl, "date": dt.Format("2006-01-02"),
			"request_count": reqCount, "input_tokens": inputTokens, "output_tokens": outputTokens,
			"cache_hit_tokens": cacheRead, "cache_creation_tokens": cacheCreation,
			"error_count": errorCount, "avg_latency_ms": avgLatency,
			"key_preview": keyPreview,
			"created_at":  createdAt, "updated_at": updatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  results,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}
func (r *AdminRoutes) getUpstreamUsageSummary(c *gin.Context) {
	ctx := c.Request.Context()
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")
	if dateFrom == "" {
		dateFrom = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	if dateTo == "" {
		dateTo = time.Now().UTC().Format("2006-01-02")
	}

	rows, err := r.db.Query(ctx, `
		SELECT provider,
			SUM(request_count) as total_requests,
			SUM(input_tokens) as total_input_tokens,
			SUM(output_tokens) as total_output_tokens,
			SUM(cache_read_tokens) as total_cache_hit,
			SUM(cache_creation_tokens) as total_cache_creation,
			SUM(error_count) as total_errors,
			AVG(avg_latency_ms)::int as avg_latency
		FROM upstream_key_usage
		WHERE date >= $1 AND date <= $2
		GROUP BY provider
		ORDER BY total_requests DESC
	`, dateFrom, dateTo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query summary"}})
		return
	}
	defer rows.Close()

	summary := []gin.H{}
	for rows.Next() {
		var provider string
		var totalRequests, avgLatency int
		var inputTokens, outputTokens, cacheRead, cacheCreation int64
		var totalErrors int
		_ = rows.Scan(&provider, &totalRequests, &inputTokens, &outputTokens,
			&cacheRead, &cacheCreation, &totalErrors, &avgLatency)
		summary = append(summary, gin.H{
			"provider":             provider,
			"total_requests":       totalRequests,
			"total_input_tokens":   inputTokens,
			"total_output_tokens":  outputTokens,
			"total_cache_hit":      cacheRead,
			"total_cache_creation": cacheCreation,
			"total_errors":         totalErrors,
			"avg_latency_ms":       avgLatency,
		})
	}

	c.JSON(http.StatusOK, gin.H{"summary": summary})
}
func (r *AdminRoutes) exportUpstreamUsage(c *gin.Context) {
	ctx := c.Request.Context()
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")
	if dateFrom == "" {
		dateFrom = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	if dateTo == "" {
		dateTo = time.Now().UTC().Format("2006-01-02")
	}

	rows, err := r.db.Query(ctx, `
		SELECT u.provider, u.model, u.date, u.request_count,
			u.input_tokens, u.output_tokens,
			u.cache_read_tokens, u.cache_creation_tokens,
			u.error_count, u.avg_latency_ms,
			k.key_preview
		FROM upstream_key_usage u
		LEFT JOIN upstream_keys k ON k.id = u.upstream_key_id
		WHERE u.date >= $1 AND u.date <= $2
		ORDER BY u.date DESC, u.provider, u.model
	`, dateFrom, dateTo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export"}})
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=upstream-usage-"+time.Now().UTC().Format("2006-01-02")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = c.Writer.WriteString(T("csv.header.provider", c) + "," + T("csv.header.model", c) + "," + T("csv.header.date", c) + "," + T("csv.header.key_preview", c) + "," + T("csv.header.request_count", c) + "," + T("csv.header.input_tokens", c) + "," + T("csv.header.output_tokens", c) + "," + T("csv.header.cache_hit_tokens", c) + "," + T("csv.header.cache_creation_tokens", c) + "," + T("csv.header.error_count", c) + "," + T("csv.header.avg_latency_ms", c) + "\n")

	for rows.Next() {
		var provider, model string
		var date time.Time
		var reqCount, errorCount, avgLatency int
		var inputTokens, outputTokens, cacheRead, cacheCreation int64
		var keyPreview *string
		if err := rows.Scan(&provider, &model, &date, &reqCount,
			&inputTokens, &outputTokens, &cacheRead, &cacheCreation,
			&errorCount, &avgLatency, &keyPreview); err != nil {
			r.logger.Warn("Failed to scan export row", zap.Error(err))
			continue
		}
		preview := ""
		if keyPreview != nil {
			preview = *keyPreview
		}
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%d,%d,%d,%d,%d,%d,%d\n",
			provider, model, date.Format("2006-01-02"), preview,
			reqCount, inputTokens, outputTokens,
			cacheRead, cacheCreation, errorCount, avgLatency)
	}

}

// --- Provider Health Monitoring ---

// parseHealthWindow converts the ?window query param to a safe PostgreSQL interval string.
func parseHealthWindow(c *gin.Context) string {
	switch c.Query("window") {
	case "15m":
		return "15 minutes"
	case "30m":
		return "30 minutes"
	case "1h":
		return "1 hour"
	case "12h":
		return "12 hours"
	case "24h":
		return "24 hours"
	default:
		// Default to a 1-hour window: long enough to collect a
		// meaningful sample (see MinHealthSampleSize) so status is not
		// dominated by a handful of recent requests.
		return "1 hour"
	}
}
func (r *AdminRoutes) listProviderHealth(c *gin.Context) {
	ctx := c.Request.Context()
	window := parseHealthWindow(c)

	// Aggregate over the selected window for "current" status.
	// When a provider has no traffic in the window, fall back to its upstream
	// key validation status so the page does not show a blanket "unknown".
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT p.id, p.name, p.enabled,
			COALESCE(SUM(h.request_count), 0) as req_count,
			COALESCE(SUM(h.success_count), 0) as success_count,
			COALESCE(SUM(h.failure_count), 0) as failure_count,
			COALESCE(SUM(h.auth_error_count), 0) as auth_err,
			COALESCE(SUM(h.invalid_request_count), 0) as invalid_req,
			COALESCE(SUM(h.error_4xx_count), 0) as err_4xx,
			COALESCE(SUM(h.total_latency_ms), 0) as total_latency,
			COALESCE(MAX(h.bucket) IS NOT NULL, false) as has_traffic,
			COALESCE(k.valid_keys, 0) as valid_keys,
			COALESCE(k.invalid_keys, 0) as invalid_keys
		FROM providers p
		LEFT JOIN provider_health_stats h ON h.provider = p.id
			AND h.bucket >= NOW() - INTERVAL '%s'
		LEFT JOIN (
			SELECT provider,
				COUNT(*) FILTER (WHERE enabled AND validation_status = 'valid') as valid_keys,
				COUNT(*) FILTER (WHERE enabled AND validation_status IN ('invalid', 'failed')) as invalid_keys
			FROM upstream_keys WHERE deleted_at IS NULL GROUP BY provider
		) k ON k.provider = p.id
		GROUP BY p.id, p.name, p.enabled, k.valid_keys, k.invalid_keys
		ORDER BY p.sort_order, p.name
	`, window))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list provider health"}})
		return
	}
	defer rows.Close()

	results := []gin.H{}
	for rows.Next() {
		var id, name string
		var enabled bool
		var reqCount, successCount, failureCount, authErr, invalidReq, err4xx int
		var totalLatency int64
		var hasTraffic bool
		var validKeys, invalidKeys int
		if err := rows.Scan(&id, &name, &enabled, &reqCount, &successCount, &failureCount,
			&authErr, &invalidReq, &err4xx, &totalLatency, &hasTraffic, &validKeys, &invalidKeys); err != nil {
			r.logger.Warn("Failed to scan provider health row", zap.Error(err))
			continue
		}
		var avgLat int
		if reqCount > 0 {
			avgLat = int(totalLatency / int64(reqCount))
		}
		// SUM-then-classify over the whole window (not the worst bucket), with
		// client/credential errors excluded from the rate — consistent with the
		// Status page's DeriveInternalStatus.
		excluded := authErr + invalidReq + err4xx
		var status, statusSource string
		if hasTraffic {
			status = mapInternalStatus(query.DeriveInternalStatus("", reqCount, failureCount, excluded, avgLat))
			statusSource = "traffic"
		} else if validKeys > 0 {
			status, statusSource = "available", "validation"
		} else if invalidKeys > 0 {
			status, statusSource = "unavailable", "validation"
		} else {
			status, statusSource = "unknown", "none"
		}
		// error_rate shown = window-aggregate rate over model-attributable
		// traffic (excludes auth/invalid/4xx), as a percentage.
		var errorRate, successRate float64
		effective := reqCount - excluded
		if effective > 0 {
			realFailures := failureCount - excluded
			if realFailures < 0 {
				realFailures = 0
			}
			errorRate = float64(realFailures) / float64(effective) * 100
			successRate = 100 - errorRate
		}
		results = append(results, gin.H{
			"provider":          id,
			"name":              name,
			"enabled":           enabled,
			"request_count":     reqCount,
			"success_count":     successCount,
			"failure_count":     failureCount,
			"auth_error_count":  authErr,
			"invalid_req_count": invalidReq,
			"error_4xx_count":   err4xx,
			"avg_latency_ms":    avgLat,
			"latest_latency_ms": avgLat,
			"error_rate":        round2(errorRate),
			"success_rate":      round2(successRate),
			"status":            status,
			"status_source":     statusSource,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": results})
}
func (r *AdminRoutes) exportProviderHealth(c *gin.Context) {
	ctx := c.Request.Context()
	window := parseHealthWindow(c)

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT p.id, p.name, p.enabled,
			COALESCE(SUM(h.request_count), 0) as req_count,
			COALESCE(SUM(h.success_count), 0) as success_count,
			COALESCE(SUM(h.failure_count), 0) as failure_count,
			COALESCE(SUM(h.auth_error_count), 0) as auth_err,
			COALESCE(SUM(h.invalid_request_count), 0) as invalid_req,
			COALESCE(SUM(h.error_4xx_count), 0) as err_4xx,
			COALESCE(SUM(h.total_latency_ms), 0) as total_latency,
			COALESCE(MAX(h.bucket) IS NOT NULL, false) as has_traffic,
			COALESCE(k.valid_keys, 0) as valid_keys,
			COALESCE(k.invalid_keys, 0) as invalid_keys
		FROM providers p
		LEFT JOIN provider_health_stats h ON h.provider = p.id
			AND h.bucket >= NOW() - INTERVAL '%s'
		LEFT JOIN (
			SELECT provider,
				COUNT(*) FILTER (WHERE enabled AND validation_status = 'valid') as valid_keys,
				COUNT(*) FILTER (WHERE enabled AND validation_status IN ('invalid', 'failed')) as invalid_keys
			FROM upstream_keys WHERE deleted_at IS NULL GROUP BY provider
		) k ON k.provider = p.id
		GROUP BY p.id, p.name, p.enabled, k.valid_keys, k.invalid_keys
		ORDER BY p.sort_order, p.name
	`, window))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export provider health"}})
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=provider-health-"+time.Now().UTC().Format("2006-01-02")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = c.Writer.WriteString(T("csv.header.provider", c) + "," + T("csv.header.name", c) + "," + T("csv.header.enabled", c) + "," + T("csv.header.request_count", c) + "," + T("csv.header.success_count", c) + "," + T("csv.header.failure_count", c) + "," + T("csv.header.avg_latency_ms", c) + "," + T("csv.header.error_rate", c) + "," + T("csv.header.status", c) + "\n")

	for rows.Next() {
		var id, name string
		var enabled bool
		var reqCount, successCount, failureCount, authErr, invalidReq, err4xx int
		var totalLatency int64
		var hasTraffic bool
		var validKeys, invalidKeys int
		if err := rows.Scan(&id, &name, &enabled, &reqCount, &successCount, &failureCount,
			&authErr, &invalidReq, &err4xx, &totalLatency, &hasTraffic, &validKeys, &invalidKeys); err != nil {
			r.logger.Warn("Failed to scan provider health export row", zap.Error(err))
			continue
		}
		var avgLat int
		if reqCount > 0 {
			avgLat = int(totalLatency / int64(reqCount))
		}
		excluded := authErr + invalidReq + err4xx
		var status string
		if hasTraffic {
			status = mapInternalStatus(query.DeriveInternalStatus("", reqCount, failureCount, excluded, avgLat))
		} else if validKeys > 0 {
			status = "available"
		} else if invalidKeys > 0 {
			status = "unavailable"
		} else {
			status = "unknown"
		}
		var errorRate float64
		effective := reqCount - excluded
		if effective > 0 {
			realFailures := failureCount - excluded
			if realFailures < 0 {
				realFailures = 0
			}
			errorRate = float64(realFailures) / float64(effective) * 100
		}
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%t,%d,%d,%d,%d,%.2f,%s\n",
			id, name, enabled, reqCount, successCount, failureCount, avgLat, errorRate, status)
	}
}
func (r *AdminRoutes) getProviderHealthSummary(c *gin.Context) {
	ctx := c.Request.Context()
	window := parseHealthWindow(c)

	// Per-provider health. SUM the breakdown over the window and classify in Go
	// (SUM-then-classify) so a single bad 5-minute bucket no longer paints a
	// provider red all window, and client/credential errors are excluded.
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT provider,
			COALESCE(SUM(request_count), 0) as req_count,
			COALESCE(SUM(success_count), 0) as success_count,
			COALESCE(SUM(failure_count), 0) as failure_count,
			COALESCE(SUM(auth_error_count), 0) as auth_err,
			COALESCE(SUM(invalid_request_count), 0) as invalid_req,
			COALESCE(SUM(error_4xx_count), 0) as err_4xx,
			CASE WHEN COALESCE(SUM(request_count), 0) > 0
			     THEN ROUND(COALESCE(SUM(total_latency_ms), 0)::numeric / SUM(request_count), 0)
			     ELSE 0
			END as avg_latency_ms
		FROM provider_health_stats
		WHERE bucket >= NOW() - INTERVAL '%s'
		GROUP BY provider
		ORDER BY provider`, window))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"providers": []gin.H{}, "summary": gin.H{}})
		return
	}
	defer rows.Close()

	var healthy, degraded, unhealthy, unknown int
	providers := []gin.H{}
	for rows.Next() {
		var provider string
		var reqCount, successCount, failureCount, authErr, invalidReq, err4xx int
		var avgLatency int64
		_ = rows.Scan(&provider, &reqCount, &successCount, &failureCount, &authErr, &invalidReq, &err4xx, &avgLatency)

		excluded := authErr + invalidReq + err4xx
		status := mapInternalStatus(query.DeriveInternalStatus("", reqCount, failureCount, excluded, int(avgLatency)))

		errorRate := 0.0
		effective := reqCount - excluded
		if effective > 0 {
			realFailures := failureCount - excluded
			if realFailures < 0 {
				realFailures = 0
			}
			errorRate = float64(realFailures) / float64(effective)
		}

		switch status {
		case "available":
			healthy++
		case "degraded":
			degraded++
		case "unavailable":
			unhealthy++
		default:
			unknown++
		}

		providers = append(providers, gin.H{
			"provider":       provider,
			"status":         status,
			"request_count":  reqCount,
			"success_count":  successCount,
			"failure_count":  failureCount,
			"error_rate":     errorRate,
			"avg_latency_ms": avgLatency,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"providers": providers,
		"summary": gin.H{
			"healthy":   healthy,
			"degraded":  degraded,
			"unhealthy": unhealthy,
			"unknown":   unknown,
			"total":     healthy + degraded + unhealthy + unknown,
		},
	})
}
func (r *AdminRoutes) getProviderHealthDetail(c *gin.Context) {
	ctx := c.Request.Context()
	providerID := c.Param("provider")
	window := parseHealthWindow(c)

	// Current status (selected window from list page). SUM-then-classify with
	// client/credential errors excluded, matching the list page.
	var reqCount, successCount, failureCount, authErr, invalidReq, err4xx int
	var totalLatency int64
	_ = r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT COALESCE(SUM(request_count), 0),
			COALESCE(SUM(success_count), 0),
			COALESCE(SUM(failure_count), 0),
			COALESCE(SUM(auth_error_count), 0),
			COALESCE(SUM(invalid_request_count), 0),
			COALESCE(SUM(error_4xx_count), 0),
			COALESCE(SUM(total_latency_ms), 0)
		FROM provider_health_stats
		WHERE provider = $1 AND bucket >= NOW() - INTERVAL '%s'
	`, window), providerID).Scan(&reqCount, &successCount, &failureCount, &authErr, &invalidReq, &err4xx, &totalLatency)
	avgLatencyOverall := 0
	if reqCount > 0 {
		avgLatencyOverall = int(totalLatency / int64(reqCount))
	}
	latestStatus := mapInternalStatus(query.DeriveInternalStatus("", reqCount, failureCount, authErr+invalidReq+err4xx, avgLatencyOverall))

	// History: last 7 days, hourly aggregation. Each hour is classified by
	// SUM-then-classify over its buckets (excluding client/credential errors),
	// not by its worst 5-minute bucket.
	historyRows, err := r.db.Query(ctx, `
		SELECT
			date_trunc('hour', bucket) as hour,
			SUM(request_count) as req_count,
			SUM(success_count) as success_count,
			SUM(failure_count) as failure_count,
			SUM(auth_error_count) as auth_err,
			SUM(invalid_request_count) as invalid_req,
			SUM(error_4xx_count) as err_4xx,
			CASE WHEN SUM(request_count) > 0
			     THEN (SUM(total_latency_ms) / SUM(request_count))::int ELSE 0 END as avg_latency
		FROM provider_health_stats
		WHERE provider = $1 AND bucket >= NOW() - INTERVAL '7 days'
		GROUP BY date_trunc('hour', bucket)
		ORDER BY hour ASC
	`, providerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query history"}})
		return
	}
	defer historyRows.Close()

	history := []gin.H{}
	for historyRows.Next() {
		var hour time.Time
		var hReq, hSuccess, hFailure, hAuth, hInvalid, h4xx int
		var hLatency int
		if err := historyRows.Scan(&hour, &hReq, &hSuccess, &hFailure, &hAuth, &hInvalid, &h4xx, &hLatency); err != nil {
			continue
		}
		hExcluded := hAuth + hInvalid + h4xx
		hStatus := mapInternalStatus(query.DeriveInternalStatus("", hReq, hFailure, hExcluded, hLatency))
		var errorRate float64
		hEffective := hReq - hExcluded
		if hEffective > 0 {
			hReal := hFailure - hExcluded
			if hReal < 0 {
				hReal = 0
			}
			errorRate = float64(hReal) / float64(hEffective) * 100
		}
		history = append(history, gin.H{
			"hour":           hour.Format("2006-01-02 15:04"),
			"request_count":  hReq,
			"success_count":  hSuccess,
			"failure_count":  hFailure,
			"avg_latency_ms": hLatency,
			"error_rate":     errorRate,
			"status":         hStatus,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"provider":       providerID,
		"request_count":  reqCount,
		"success_count":  successCount,
		"failure_count":  failureCount,
		"avg_latency_ms": avgLatencyOverall,
		"status":         latestStatus,
		"history":        history,
	})
}

// ============================================
// Rate Limits Capacity Overview
// ============================================

// getRateLimitCapacity returns upstream total capacity vs downstream demand.
func (r *AdminRoutes) getRateLimitCapacity(c *gin.Context) {
	ctx := c.Request.Context()
	interval, minutes := rateLimitWindow(c)

	var totalKeys, enabledKeys int
	var totalRPM, totalTPM int64
	var totalQuota, quotaUsed int64
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE enabled),
		       COALESCE(SUM(rpm_limit), 0), COALESCE(SUM(tpm_limit), 0),
		       COALESCE(SUM(quota_limit), 0), COALESCE(SUM(quota_used), 0)
		FROM upstream_keys
		WHERE deleted_at IS NULL
	`).Scan(&totalKeys, &enabledKeys, &totalRPM, &totalTPM, &totalQuota, &quotaUsed)
	if err != nil {
		r.logger.Warn("Failed to get upstream capacity", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get capacity data"}})
		return
	}

	var totalRequests int
	var totalTokens int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(request_count), 0), COALESCE(SUM(input_tokens + output_tokens), 0)
		 FROM usage_records WHERE created_at > NOW() - INTERVAL '`+interval+`'`).
		Scan(&totalRequests, &totalTokens)

	var peakRPM, peakTPM int64
	_ = r.db.QueryRow(ctx, `
		SELECT COALESCE(MAX(requests), 0), COALESCE(MAX(tokens), 0)
		FROM (
			SELECT date_trunc('minute', created_at) as minute,
			       SUM(request_count) as requests,
			       SUM(input_tokens + output_tokens) as tokens
			FROM usage_records
			WHERE created_at > NOW() - INTERVAL '`+interval+`'
			GROUP BY minute
		) t
	`).Scan(&peakRPM, &peakTPM)

	avgRPM := float64(totalRequests) / minutes
	avgTPM := float64(totalTokens) / minutes

	util := gin.H{}
	maxUtil := 0.0
	if totalRPM > 0 {
		u := avgRPM / float64(totalRPM)
		util["rpm"] = u
		if u > maxUtil {
			maxUtil = u
		}
	}
	if totalTPM > 0 {
		u := avgTPM / float64(totalTPM)
		util["tpm"] = u
		if u > maxUtil {
			maxUtil = u
		}
	}
	if totalQuota > 0 {
		u := float64(quotaUsed) / float64(totalQuota)
		util["quota"] = u
		if u > maxUtil {
			maxUtil = u
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"upstream": gin.H{
			"total_rpm":    totalRPM,
			"total_tpm":    totalTPM,
			"total_quota":  totalQuota,
			"quota_used":   quotaUsed,
			"total_keys":   totalKeys,
			"enabled_keys": enabledKeys,
		},
		"downstream": gin.H{
			"total_requests": totalRequests,
			"total_tokens":   totalTokens,
			"peak_rpm":       peakRPM,
			"peak_tpm":       peakTPM,
			"avg_rpm":        avgRPM,
			"avg_tpm":        avgTPM,
		},
		"utilization": util,
		"summary": gin.H{
			"period":  interval,
			"status":  rateLimitStatus(maxUtil),
			"minutes": minutes,
		},
	})
}

// exportRateLimits exports rate-limit usage data as CSV.
func (r *AdminRoutes) exportRateLimits(c *gin.Context) {
	ctx := c.Request.Context()
	dimension := c.Query("dimension")
	interval, _ := rateLimitWindow(c)

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=rate-limits-"+dimension+"-"+time.Now().UTC().Format("2006-01-02")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	switch dimension {
	case "users":
		r.exportRateLimitUsersCSV(ctx, c, interval)
	case "groups":
		r.exportRateLimitGroupsCSV(ctx, c, interval)
	case "apikeys":
		r.exportRateLimitAPIKeysCSV(ctx, c, interval)
	case "upstream":
		r.exportRateLimitUpstreamKeysCSV(ctx, c, interval)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid dimension"}})
	}
}

func csvCell(v string) string {
	if strings.ContainsAny(v, ",\"\n\r") {
		return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
	}
	return v
}

func (r *AdminRoutes) exportRateLimitUsersCSV(ctx context.Context, c *gin.Context, interval string) {
	rows, err := r.db.Query(ctx,
		`SELECT u.id::text, u.name, u.email, u.role,
		        COALESCE(ur.request_count, 0) as request_count,
		        COALESCE(ur.total_tokens, 0) as total_tokens,
		        COALESCE(ur.total_cost, 0) as total_cost,
		        us.rpm_limit, us.rph_limit, us.rpd_limit, us.rpw_limit, us.rp5h_limit,
		        us.tpm_limit, us.tph_limit, us.tpd_limit, us.tpw_limit,
		        us.quota_used, us.quota_remaining
		 FROM users u
		 LEFT JOIN LATERAL (
		     SELECT COUNT(*) as request_count,
		            COALESCE(SUM(input_tokens + output_tokens), 0) as total_tokens,
		            COALESCE(SUM(total_cost), 0) as total_cost
		     FROM usage_records
		     WHERE user_id = u.id AND created_at > NOW() - INTERVAL '`+interval+`'
		 ) ur ON true
		 LEFT JOIN LATERAL (
		     SELECT *
		     FROM user_subscriptions
		     WHERE user_id = u.id AND status = 'active' AND expires_at > NOW()
		     ORDER BY created_at DESC LIMIT 1
		 ) us ON true
		 WHERE u.deleted_at IS NULL
		 ORDER BY request_count DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export users"}})
		return
	}
	defer rows.Close()

	header := []string{
		"User ID", "Name", "Email", "Role",
		"Requests", "Tokens", "Cost",
		"RPM Limit", "RPH Limit", "RPD Limit",
		"RPW Limit", "RP5H Limit",
		"TPM Limit", "TPH Limit", "TPD Limit",
		"TPW Limit", "Quota Used", "Quota Remaining",
	}
	_, _ = c.Writer.WriteString(strings.Join(header, ",") + "\n")

	for rows.Next() {
		var id, name, email, role string
		var requestCount, totalTokens int
		var totalCost float64
		var rpmLimit, rphLimit, rpdLimit, rpwLimit, rp5hLimit *int
		var tpmLimit, tphLimit, tpdLimit, tpwLimit *int
		var quotaUsed, quotaRemaining *int64
		_ = rows.Scan(&id, &name, &email, &role,
			&requestCount, &totalTokens, &totalCost,
			&rpmLimit, &rphLimit, &rpdLimit, &rpwLimit, &rp5hLimit,
			&tpmLimit, &tphLimit, &tpdLimit, &tpwLimit,
			&quotaUsed, &quotaRemaining)
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%d,%d,%.6f,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
			csvCell(id), csvCell(name), csvCell(email), csvCell(role),
			requestCount, totalTokens, totalCost,
			csvCell(fmtPtrInt(rpmLimit)), csvCell(fmtPtrInt(rphLimit)), csvCell(fmtPtrInt(rpdLimit)),
			csvCell(fmtPtrInt(rpwLimit)), csvCell(fmtPtrInt(rp5hLimit)),
			csvCell(fmtPtrInt(tpmLimit)), csvCell(fmtPtrInt(tphLimit)), csvCell(fmtPtrInt(tpdLimit)),
			csvCell(fmtPtrInt(tpwLimit)), csvCell(fmtPtrInt64(quotaUsed)), csvCell(fmtPtrInt64(quotaRemaining)))
	}
}

func (r *AdminRoutes) exportRateLimitGroupsCSV(ctx context.Context, c *gin.Context, interval string) {
	rows, err := r.db.Query(ctx,
		`SELECT g.id::text, g.name,
		        g.rpm, g.rph, g.rp5h, g.rpw, g.rpmonth,
		        g.tpm, g.tph, g.tpd, g.tpw, g.tpmonth,
		        COALESCE(SUM(ur.request_count), 0)::int as requests,
		        COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tokens
		 FROM user_groups g
		 LEFT JOIN usage_records ur ON ur.group_id = g.id AND ur.created_at > NOW() - INTERVAL '`+interval+`'
		 WHERE g.deleted_at IS NULL
		 GROUP BY g.id, g.name, g.rpm, g.rph, g.rp5h, g.rpw, g.rpmonth,
		          g.tpm, g.tph, g.tpd, g.tpw, g.tpmonth
		 ORDER BY requests DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export groups"}})
		return
	}
	defer rows.Close()

	header := []string{
		"Group ID", "Name",
		"RPM", "RPH", "RP5H",
		"RPW", "RPMonth",
		"TPM", "TPH", "TPD",
		"TPW", "TPMonth",
		"Requests", "Tokens",
	}
	_, _ = c.Writer.WriteString(strings.Join(header, ",") + "\n")

	for rows.Next() {
		var id, name string
		var rpm, rph, rp5h, rpw, rpmonth *int64
		var tpm, tph, tpd, tpw, tpmonth *int64
		var requests int
		var tokens int64
		_ = rows.Scan(&id, &name,
			&rpm, &rph, &rp5h, &rpw, &rpmonth,
			&tpm, &tph, &tpd, &tpw, &tpmonth,
			&requests, &tokens)
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%d,%d\n",
			csvCell(id), csvCell(name),
			csvCell(fmtPtrInt64(rpm)), csvCell(fmtPtrInt64(rph)), csvCell(fmtPtrInt64(rp5h)),
			csvCell(fmtPtrInt64(rpw)), csvCell(fmtPtrInt64(rpmonth)),
			csvCell(fmtPtrInt64(tpm)), csvCell(fmtPtrInt64(tph)), csvCell(fmtPtrInt64(tpd)),
			csvCell(fmtPtrInt64(tpw)), csvCell(fmtPtrInt64(tpmonth)),
			requests, tokens)
	}
}

func (r *AdminRoutes) exportRateLimitAPIKeysCSV(ctx context.Context, c *gin.Context, interval string) {
	rows, err := r.db.Query(ctx,
		`SELECT k.id::text, k.name, k.key_prefix, k.status,
		        k.rate_limit_rpm, k.rate_limit_tpm,
		        COALESCE(SUM(ur.request_count), 0)::int as requests,
		        COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tokens
		 FROM api_keys k
		 LEFT JOIN usage_records ur ON ur.user_api_key_id = k.id AND ur.created_at > NOW() - INTERVAL '`+interval+`'
		 WHERE k.deleted_at IS NULL
		 GROUP BY k.id, k.name, k.key_prefix, k.status, k.rate_limit_rpm, k.rate_limit_tpm
		 ORDER BY requests DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export api keys"}})
		return
	}
	defer rows.Close()

	header := []string{
		"Key ID", "Name", "Key Prefix",
		"Status", "RPM Limit", "TPM Limit",
		"Requests", "Tokens",
	}
	_, _ = c.Writer.WriteString(strings.Join(header, ",") + "\n")

	for rows.Next() {
		var id, name, keyPrefix, status string
		var rpmLimit, tpmLimit *int
		var requests int
		var tokens int64
		_ = rows.Scan(&id, &name, &keyPrefix, &status,
			&rpmLimit, &tpmLimit,
			&requests, &tokens)
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%s,%s,%d,%d\n",
			csvCell(id), csvCell(name), csvCell(keyPrefix), csvCell(status),
			csvCell(fmtPtrInt(rpmLimit)), csvCell(fmtPtrInt(tpmLimit)),
			requests, tokens)
	}
}

func (r *AdminRoutes) exportRateLimitUpstreamKeysCSV(ctx context.Context, c *gin.Context, interval string) {
	rows, err := r.db.Query(ctx,
		`SELECT k.id::text, k.provider, k.key_preview,
		        k.rpm_limit, k.tpm_limit, k.quota_limit, k.quota_used, k.enabled,
		        COALESCE(SUM(ur.request_count), 0)::int as requests,
		        COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tokens
		 FROM upstream_keys k
		 LEFT JOIN usage_records ur ON ur.upstream_key_id = k.id::text AND ur.created_at > NOW() - INTERVAL '`+interval+`'
		 WHERE k.deleted_at IS NULL
		 GROUP BY k.id, k.provider, k.key_preview, k.rpm_limit, k.tpm_limit, k.quota_limit, k.quota_used, k.enabled
		 ORDER BY requests DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export upstream keys"}})
		return
	}
	defer rows.Close()

	header := []string{
		"Key ID", "Provider", "Key Preview",
		"Enabled", "RPM Limit", "TPM Limit",
		"Quota Limit", "Quota Used",
		"Requests", "Tokens",
	}
	_, _ = c.Writer.WriteString(strings.Join(header, ",") + "\n")

	for rows.Next() {
		var id, provider, keyPreview string
		var rpmLimit, tpmLimit *int
		var quotaLimit, quotaUsed *int64
		var enabled bool
		var requests int
		var tokens int64
		_ = rows.Scan(&id, &provider, &keyPreview,
			&rpmLimit, &tpmLimit, &quotaLimit, &quotaUsed, &enabled,
			&requests, &tokens)
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%s,%s,%s,%s,%d,%d\n",
			csvCell(id), csvCell(provider), csvCell(keyPreview), fmt.Sprintf("%t", enabled),
			csvCell(fmtPtrInt(rpmLimit)), csvCell(fmtPtrInt(tpmLimit)),
			csvCell(fmtPtrInt64(quotaLimit)), csvCell(fmtPtrInt64(quotaUsed)),
			requests, tokens)
	}
}

func fmtPtrInt(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

func fmtPtrInt64(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}
