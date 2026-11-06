package routes

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func (r *SubscriptionRoutes) listUserSubscriptions(c *gin.Context) {
	// user_subscriptions table may not exist in legacy DB
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT us.id, us.user_id, us.plan_id, us.status, us.created_at,
		        u.name as user_name, u.email as user_email, sp.name as plan_name,
		        us.rpm_limit, us.rph_limit, us.rp5h_limit, us.rpd_limit, us.rpw_limit,
		        us.tpm_limit, us.tph_limit, us.tpd_limit, us.tpw_limit,
		        us.concurrent_limit,
		        us.tpm_used, us.tph_used, us.tpw_used, us.rpm_used, us.rph_used, us.rpw_used, us.rp5h_used,
		        us.quota_used, us.quota_remaining, us.daily_limit, us.daily_quota_used
		 FROM user_subscriptions us
		 JOIN users u ON u.id = us.user_id
		 LEFT JOIN subscription_plans sp ON sp.id = us.plan_id
		 ORDER BY us.created_at DESC LIMIT 20`)
	if err != nil {
		// Table doesn't exist, return empty
		c.JSON(http.StatusOK, gin.H{"subscriptions": []gin.H{}})
		return
	}
	defer rows.Close()

	subs := []gin.H{}
	for rows.Next() {
		var id, userID, status string
		var planID *string
		var userName, userEmail, planName string
		var createdAt interface{}
		var rpmLimit, rphLimit, rp5hLimit, rpdLimit, rpwLimit int
		var tpmLimit, tphLimit, tpdLimit, tpwLimit int
		var concurrentLimit int
		var tpmUsed, tphUsed, tpwUsed, rpmUsed, rphUsed, rpwUsed, rp5hUsed int
		var quotaUsed, quotaRemaining, dailyLimit, dailyQuotaUsed int64
		_ = rows.Scan(&id, &userID, &planID, &status, &createdAt, &userName, &userEmail, &planName,
			&rpmLimit, &rphLimit, &rp5hLimit, &rpdLimit, &rpwLimit,
			&tpmLimit, &tphLimit, &tpdLimit, &tpwLimit,
			&concurrentLimit,
			&tpmUsed, &tphUsed, &tpwUsed, &rpmUsed, &rphUsed, &rpwUsed, &rp5hUsed,
			&quotaUsed, &quotaRemaining, &dailyLimit, &dailyQuotaUsed)
		subs = append(subs, gin.H{
			"id": id, "user_id": userID, "plan_id": planID, "status": status,
			"user_name": userName, "user_email": userEmail, "plan_name": planName,
			"created_at": createdAt,
			"rpm_limit":  rpmLimit, "rph_limit": rphLimit, "rp5h_limit": rp5hLimit,
			"rpd_limit": rpdLimit, "rpw_limit": rpwLimit,
			"tpm_limit": tpmLimit, "tph_limit": tphLimit, "tpd_limit": tpdLimit, "tpw_limit": tpwLimit,
			"concurrent_limit": concurrentLimit,
			"tpm_used":         tpmUsed, "tph_used": tphUsed, "tpw_used": tpwUsed,
			"rpm_used": rpmUsed, "rph_used": rphUsed, "rpw_used": rpwUsed, "rp5h_used": rp5hUsed,
			"quota_used": quotaUsed, "quota_remaining": quotaRemaining,
			"daily_limit": dailyLimit, "daily_quota_used": dailyQuotaUsed,
		})
	}

	c.JSON(http.StatusOK, gin.H{"subscriptions": subs})
}
func (r *SubscriptionRoutes) updateUserSubscription(c *gin.Context) {
	subID := c.Param("id")

	var req struct {
		RpmLimit        *int   `json:"rpm_limit"`
		RphLimit        *int   `json:"rph_limit"`
		Rp5hLimit       *int   `json:"rp5h_limit"`
		RpdLimit        *int   `json:"rpd_limit"`
		RpwLimit        *int   `json:"rpw_limit"`
		TpmLimit        *int   `json:"tpm_limit"`
		TphLimit        *int   `json:"tph_limit"`
		TpdLimit        *int   `json:"tpd_limit"`
		TpwLimit        *int   `json:"tpw_limit"`
		ConcurrentLimit *int   `json:"concurrent_limit"`
		DailyLimit      *int64 `json:"daily_limit"`
		QuotaRemaining  *int64 `json:"quota_remaining"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	var setClauses []string
	var args []interface{}
	argIdx := 1

	fields := []struct {
		val *int
		col string
	}{
		{req.RpmLimit, "rpm_limit"},
		{req.RphLimit, "rph_limit"},
		{req.Rp5hLimit, "rp5h_limit"},
		{req.RpdLimit, "rpd_limit"},
		{req.RpwLimit, "rpw_limit"},
		{req.TpmLimit, "tpm_limit"},
		{req.TphLimit, "tph_limit"},
		{req.TpdLimit, "tpd_limit"},
		{req.TpwLimit, "tpw_limit"},
		{req.ConcurrentLimit, "concurrent_limit"},
	}
	for _, f := range fields {
		if f.val != nil {
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d", f.col, argIdx))
			args = append(args, *f.val)
			argIdx++
		}
	}
	if req.DailyLimit != nil {
		setClauses = append(setClauses, fmt.Sprintf("daily_limit = $%d", argIdx))
		args = append(args, *req.DailyLimit)
		argIdx++
	}
	if req.QuotaRemaining != nil {
		setClauses = append(setClauses, fmt.Sprintf("quota_remaining = $%d", argIdx))
		args = append(args, *req.QuotaRemaining)
		argIdx++
	}

	if len(setClauses) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No fields to update"}})
		return
	}

	setClauses = append(setClauses, "updated_at = NOW()")
	sql := fmt.Sprintf("UPDATE user_subscriptions SET %s WHERE id = $%d::uuid", strings.Join(setClauses, ", "), argIdx)
	args = append(args, subID)
	if err := r.db.Exec(c.Request.Context(), sql, args...); err != nil {
		r.logger.Error("Failed to update user subscription", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update subscription"}})
		return
	}

	// Invalidate Redis validate cache for this user's API keys (per-user, not KEYS *)
	var userID string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT user_id::text FROM user_subscriptions WHERE id = $1::uuid`, subID).Scan(&userID)
	if userID != "" {
		invalidateUserValidateCaches(c.Request.Context(), r.db, r.logger, userID)
	}

	// Audit log for quota changes
	if r.audit != nil && req.QuotaRemaining != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"),
			"billing", "subscription_quota_updated", "warning",
			fmt.Sprintf("Admin updated subscription %s: quota_remaining=%d", subID, *req.QuotaRemaining))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "updated": len(setClauses) > 0})
}
func (r *SubscriptionRoutes) getStats(c *gin.Context) {
	var activeCount, expiredCount, cancelledCount int
	var totalRevenue int64
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FILTER (WHERE status = 'active'),
		        COUNT(*) FILTER (WHERE status = 'expired'),
		        COUNT(*) FILTER (WHERE status = 'cancelled'),
		        COALESCE(SUM(quota_total), 0)
		 FROM user_subscriptions`).Scan(&activeCount, &expiredCount, &cancelledCount, &totalRevenue)

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT sp.name, COUNT(us.id) as subscriber_count
		 FROM subscription_plans sp
		 LEFT JOIN user_subscriptions us ON us.plan_id = sp.id AND us.status = 'active'
		 GROUP BY sp.id, sp.name
		 ORDER BY subscriber_count DESC`)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"overview": gin.H{"active_count": activeCount, "expired_count": expiredCount, "cancelled_count": cancelledCount, "total_revenue": totalRevenue},
			"byPlan":   []gin.H{},
		})
		return
	}
	defer rows.Close()

	byPlan := []gin.H{}
	for rows.Next() {
		var name string
		var count int
		_ = rows.Scan(&name, &count)
		byPlan = append(byPlan, gin.H{"name": name, "subscriber_count": count})
	}

	c.JSON(http.StatusOK, gin.H{
		"overview": gin.H{"active_count": activeCount, "expired_count": expiredCount, "cancelled_count": cancelledCount, "total_revenue": totalRevenue},
		"byPlan":   byPlan,
	})
}

// === User-facing subscription routes ===

// listPublicPlans returns active subscription plans for public viewing
func (r *SubscriptionRoutes) getMySubscriptions(c *gin.Context) {
	userID := c.GetString("user_id")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT us.id::text, us.plan_id::text, us.plan_name, us.plan_type,
		        us.quota_total, us.quota_used, us.quota_remaining,
		        us.daily_limit, us.daily_quota_used, us.daily_quota_reset,
		        us.tpw_limit, us.tpw_used,
		        us.status, us.started_at, us.expires_at, us.created_at,
		        sp.name as original_plan_name, sp.price, sp.model_permissions
		 FROM user_subscriptions us
		 LEFT JOIN subscription_plans sp ON sp.id = us.plan_id
		 WHERE us.user_id = $1::uuid
		 ORDER BY us.created_at DESC`,
		userID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"subscriptions": []gin.H{}})
		return
	}
	defer rows.Close()

	subs := []gin.H{}
	for rows.Next() {
		var id, status, planType string
		var planID, planName, originalPlanName *string
		var quotaTotal, quotaUsed, quotaRemaining, dailyLimit, dailyQuotaUsed int64
		var tpwLimit, tpwUsed int64
		var dailyQuotaReset *time.Time
		var startedAt, expiresAt, createdAt *time.Time
		var price *int64
		var modelPerms interface{}

		if err := rows.Scan(&id, &planID, &planName, &planType,
			&quotaTotal, &quotaUsed, &quotaRemaining,
			&dailyLimit, &dailyQuotaUsed, &dailyQuotaReset,
			&tpwLimit, &tpwUsed,
			&status, &startedAt, &expiresAt, &createdAt,
			&originalPlanName, &price, &modelPerms); err != nil {
			continue
		}

		// Determine if can renew
		canRenew := status == "active" || status == "expired"
		if expiresAt != nil {
			// Can renew within 7 days before expiry
			canRenew = time.Now().Add(7*24*time.Hour).After(*expiresAt) || status == "expired"
		}

		subs = append(subs, gin.H{
			"id":                id,
			"plan_id":           planID,
			"plan_name":         coalesceString(planName, originalPlanName),
			"plan_type":         planType,
			"model_permissions": modelPerms,
			"quota_total":       quotaTotal,
			"quota_used":        quotaUsed,
			"quota_remaining":   quotaRemaining,
			"daily_limit":       dailyLimit,
			"daily_quota_used":  dailyQuotaUsed,
			"daily_quota_reset": dailyQuotaReset,
			"tpw_limit":         tpwLimit,
			"tpw_used":          tpwUsed,
			"status":            status,
			"started_at":        startedAt,
			"expires_at":        expiresAt,
			"created_at":        createdAt,
			"price":             price,
			"can_renew":         canRenew,
		})
	}

	c.JSON(http.StatusOK, gin.H{"subscriptions": subs})
}

// getSubscriptionUsage returns an aggregated usage breakdown for a single subscription.
func (r *SubscriptionRoutes) getSubscriptionUsage(c *gin.Context) {
	userID := c.GetString("user_id")
	isAdmin := c.GetString("role") == "system_admin" || c.GetString("role") == "super_admin"
	subID := c.Param("id")

	var subUserID, planName string
	var startedAt, expiresAt *time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT user_id::text, plan_name, started_at, expires_at
		 FROM user_subscriptions
		 WHERE id = $1::uuid`,
		subID,
	).Scan(&subUserID, &planName, &startedAt, &expiresAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Subscription not found"}})
		return
	}
	if !isAdmin && subUserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Access denied"}})
		return
	}

	periodStart := time.Now().UTC().AddDate(0, 0, -30)
	if startedAt != nil && startedAt.Before(periodStart) {
		periodStart = *startedAt
	}
	periodEnd := time.Now().UTC()
	if expiresAt != nil && expiresAt.Before(periodEnd) {
		periodEnd = *expiresAt
	}

	type modelRow struct {
		Model                 string `json:"model"`
		Requests              int    `json:"requests"`
		InputTokens           int64  `json:"input_tokens"`
		OutputTokens          int64  `json:"output_tokens"`
		CachedTokens          int64  `json:"cached_tokens"`
		TotalCost             int64  `json:"total_cost"`
		SubscriptionDeduction int64  `json:"subscription_deduction"`
		AvgLatencyMs          int    `json:"avg_latency_ms"`
	}

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT ur.model,
		        COUNT(*)::int as requests,
		        COALESCE(SUM(ur.input_tokens), 0)::bigint as input_tokens,
		        COALESCE(SUM(ur.output_tokens), 0)::bigint as output_tokens,
		        COALESCE(SUM(ur.cached_input_tokens), 0)::bigint as cached_tokens,
		        COALESCE(SUM(ur.total_cost), 0)::bigint as total_cost,
		        COALESCE(SUM(ur.subscription_deduction), 0)::bigint as subscription_deduction,
		        COALESCE(AVG(ur.latency_ms), 0)::int as avg_latency_ms
		 FROM usage_records ur
		 JOIN api_keys k ON k.id = ur.user_api_key_id
		 WHERE k.subscription_id = $1::uuid
		   AND ur.created_at >= $2 AND ur.created_at < $3
		 GROUP BY ur.model
		 ORDER BY total_cost DESC`,
		subID, periodStart, periodEnd)
	if err != nil {
		r.logger.Error("Failed to query subscription usage by model", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query usage"}})
		return
	}
	defer rows.Close()

	var models []modelRow
	var summary struct {
		Requests              int   `json:"requests"`
		InputTokens           int64 `json:"input_tokens"`
		OutputTokens          int64 `json:"output_tokens"`
		CachedTokens          int64 `json:"cached_tokens"`
		TotalCost             int64 `json:"total_cost"`
		SubscriptionDeduction int64 `json:"subscription_deduction"`
		AvgLatencyMs          int   `json:"avg_latency_ms"`
	}
	for rows.Next() {
		var m modelRow
		if err := rows.Scan(&m.Model, &m.Requests, &m.InputTokens, &m.OutputTokens, &m.CachedTokens,
			&m.TotalCost, &m.SubscriptionDeduction, &m.AvgLatencyMs); err != nil {
			continue
		}
		models = append(models, m)
		summary.Requests += m.Requests
		summary.InputTokens += m.InputTokens
		summary.OutputTokens += m.OutputTokens
		summary.CachedTokens += m.CachedTokens
		summary.TotalCost += m.TotalCost
		summary.SubscriptionDeduction += m.SubscriptionDeduction
	}
	if len(models) > 0 {
		// Average latency is weighted by request count across models.
		var weighted int64
		for _, m := range models {
			weighted += int64(m.AvgLatencyMs * m.Requests)
		}
		summary.AvgLatencyMs = int(weighted / int64(summary.Requests))
	}

	type dailyRow struct {
		Date         string `json:"date"`
		Requests     int    `json:"requests"`
		InputTokens  int64  `json:"input_tokens"`
		OutputTokens int64  `json:"output_tokens"`
		TotalCost    int64  `json:"total_cost"`
	}
	dailyRows, err := r.db.Query(c.Request.Context(),
		`SELECT DATE(ur.created_at) as date,
		        COUNT(*)::int as requests,
		        COALESCE(SUM(ur.input_tokens), 0)::bigint as input_tokens,
		        COALESCE(SUM(ur.output_tokens), 0)::bigint as output_tokens,
		        COALESCE(SUM(ur.total_cost), 0)::bigint as total_cost
		 FROM usage_records ur
		 JOIN api_keys k ON k.id = ur.user_api_key_id
		 WHERE k.subscription_id = $1::uuid
		   AND ur.created_at >= $2 AND ur.created_at < $3
		 GROUP BY DATE(ur.created_at)
		 ORDER BY date DESC`,
		subID, periodStart, periodEnd)
	if err != nil {
		r.logger.Error("Failed to query subscription daily usage", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query usage"}})
		return
	}
	defer dailyRows.Close()

	var daily []dailyRow
	for dailyRows.Next() {
		var d dailyRow
		var date time.Time
		if err := dailyRows.Scan(&date, &d.Requests, &d.InputTokens, &d.OutputTokens, &d.TotalCost); err != nil {
			continue
		}
		d.Date = date.Format("2006-01-02")
		daily = append(daily, d)
	}

	c.JSON(http.StatusOK, gin.H{
		"subscription_id": subID,
		"plan_name":       planName,
		"period_start":    periodStart.Format("2006-01-02"),
		"period_end":      periodEnd.Format("2006-01-02"),
		"summary":         summary,
		"models":          models,
		"daily":           daily,
	})
}

// purchasePlan creates a subscription purchase order
func (r *SubscriptionRoutes) purchasePlan(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		PlanID string `json:"plan_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Plan ID is required"}})
		return
	}

	// Get plan details
	var planID, planName, planType string
	var price int64
	var durationDays int
	var quota, dailyLimit int64
	var rphLimit, rp5hLimit, rpdLimit, rpwLimit int
	var tphLimit, tpdLimit, tpwLimit int
	var concurrentLimit int
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, name, plan_type, price, quota, duration_days, COALESCE(daily_limit, 0),
			COALESCE(rph_limit, 0), COALESCE(rp5h_limit, 0), COALESCE(rpd_limit, 0), COALESCE(rpw_limit, 0),
			COALESCE(tph_limit, 0), COALESCE(tpd_limit, 0), COALESCE(tpw_limit, 0),
			COALESCE(concurrent_limit, 0)
		 FROM subscription_plans WHERE id = $1::uuid AND status = 'active' AND deleted_at IS NULL`,
		req.PlanID,
	).Scan(&planID, &planName, &planType, &price, &quota, &durationDays, &dailyLimit,
		&rphLimit, &rp5hLimit, &rpdLimit, &rpwLimit,
		&tphLimit, &tpdLimit, &tpwLimit,
		&concurrentLimit)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Plan not found or inactive"}})
		return
	}

	// Check if user already has an active subscription of the same plan type.
	// Each plan_type (token / code) can only have one active subscription at a time.
	var existingCount int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM user_subscriptions
		 WHERE user_id = $1::uuid AND plan_type = $2 AND status = 'active'`,
		userID, planType,
	).Scan(&existingCount)
	if existingCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "You already have an active subscription of this type"}})
		return
	}

	// Get user's preferred currency and exchange rate
	var currency string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(preferred_currency, 'USD') FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&currency)
	var rateStr string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT value FROM system_settings WHERE key = 'usd_exchange_rate'`,
	).Scan(&rateStr)
	exchangeRate := 7.20
	if rateStr != "" {
		var rateCents int
		_, _ = fmt.Sscanf(rateStr, "%d", &rateCents)
		if rateCents > 0 {
			exchangeRate = float64(rateCents) / 100.0
		}
	}

	// Create order
	orderID := "SUB-" + uuid.New().String()[:8]
	var orderUUID string
	err = r.db.QueryRow(c.Request.Context(),
		`INSERT INTO orders (user_id, order_id, amount, currency, exchange_rate, status, product_type, product_name, metadata)
		 VALUES ($1::uuid, $2, $3, $4, $5, 'pending', 'subscription', $6, $7)
		 RETURNING id::text`,
		userID, orderID, price, currency, exchangeRate, planName,
		map[string]interface{}{
			"plan_id":          planID,
			"plan_name":        planName,
			"plan_type":        planType,
			"quota":            quota,
			"duration_days":    durationDays,
			"daily_limit":      dailyLimit,
			"rph_limit":        rphLimit,
			"rp5h_limit":       rp5hLimit,
			"rpd_limit":        rpdLimit,
			"rpw_limit":        rpwLimit,
			"tph_limit":        tphLimit,
			"tpd_limit":        tpdLimit,
			"tpw_limit":        tpwLimit,
			"concurrent_limit": concurrentLimit,
		},
	).Scan(&orderUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create order"}})
		return
	}

	// Create pending subscription purchase record
	var purchaseID string
	err = r.db.QueryRow(c.Request.Context(),
		`INSERT INTO subscription_purchases (user_id, plan_id, order_id, status)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 'pending')
		 RETURNING id::text`,
		userID, planID, orderUUID,
	).Scan(&purchaseID)
	if err != nil {
		// Table might not exist, continue anyway
		purchaseID = ""
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "billing", "subscription_purchased", "info",
			fmt.Sprintf("Subscription purchased: %s (%s)", planName, QuotaToUSD(int64(price))))
	}

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"order_id":      orderID,
		"order_uuid":    orderUUID,
		"purchase_id":   purchaseID,
		"plan_name":     planName,
		"plan_type":     planType,
		"price":         price,
		"quota":         quota,
		"duration_days": durationDays,
	})
}

// cancelSubscription cancels a user's subscription and refunds unused quota proportionally
func (r *SubscriptionRoutes) cancelSubscription(c *gin.Context) {
	userID := c.GetString("user_id")
	subID := c.Param("id")

	// Get user's preferred currency and exchange rate for refund order
	var currency string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(preferred_currency, 'USD') FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&currency)
	var rateStr string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT value FROM system_settings WHERE key = 'usd_exchange_rate'`,
	).Scan(&rateStr)
	exchangeRate := 7.20
	if rateStr != "" {
		var rateCents int
		_, _ = fmt.Sscanf(rateStr, "%d", &rateCents)
		if rateCents > 0 {
			exchangeRate = float64(rateCents) / 100.0
		}
	}

	var planName, planType string
	var planPrice int64
	var quotaTotal, quotaUsed int64
	var expiresAt *time.Time
	var refundQuota int64
	var cancelled bool

	// Atomic cancel + refund: prevents concurrent cancellation from issuing
	// duplicate refunds, and ties the refund amount to what the user actually
	// paid (from subscription_purchases) rather than the plan's current price.
	txErr := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		// Lock the subscription row for the duration of the tx. Use INNER JOIN
		// because FOR UPDATE cannot target the nullable side of an outer join.
		err := tx.QueryRow(c.Request.Context(),
			`SELECT us.plan_name, us.plan_type, COALESCE(sp.price, 0), us.quota_total, us.quota_used, us.expires_at
			 FROM user_subscriptions us
			 JOIN subscription_plans sp ON sp.id = us.plan_id
			 WHERE us.id = $1::uuid AND us.user_id = $2::uuid AND us.status = 'active'
			 FOR UPDATE`,
			subID, userID,
		).Scan(&planName, &planType, &planPrice, &quotaTotal, &quotaUsed, &expiresAt)
		if err != nil {
			return err
		}

		// Determine the amount the user actually paid for this subscription.
		// Falls back to the plan price when no purchase record is found, which
		// preserves backward compatibility for subscriptions created before
		// subscription_purchases existed.
		actualPaid := planPrice
		var paidAmount int64
		purchaseErr := tx.QueryRow(c.Request.Context(),
			`SELECT sp.amount
			 FROM subscription_purchases sp
			 WHERE sp.user_id = $1::uuid
			   AND sp.status = 'active'
			   AND sp.plan_id = (SELECT plan_id FROM user_subscriptions WHERE id = $2::uuid)
			 ORDER BY sp.created_at DESC LIMIT 1`,
			userID, subID,
		).Scan(&paidAmount)
		if purchaseErr == nil && paidAmount > 0 {
			actualPaid = paidAmount
		}

		// Refund proportional to unused quota, capped at actual amount paid.
		if quotaTotal > 0 && quotaUsed < quotaTotal {
			remainingRatio := float64(quotaTotal-quotaUsed) / float64(quotaTotal)
			refundQuota = int64(float64(actualPaid) * remainingRatio)
			if refundQuota < 0 {
				refundQuota = 0
			}
		}

		// Conditionally cancel; RowsAffected==0 means a concurrent request
		// already cancelled it — bail out without refunding.
		ct, err := tx.Exec(c.Request.Context(),
			`UPDATE user_subscriptions SET status = 'cancelled', updated_at = NOW()
			 WHERE id = $1::uuid AND user_id = $2::uuid AND status = 'active'`,
			subID, userID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return errors.New("already cancelled")
		}
		cancelled = true

		// Refund to user balance if there's a refund amount. Lock the row and
		// write a balance_transaction so the audit trail matches the balance change.
		if refundQuota > 0 {
			var balanceBefore int64
			if err := tx.QueryRow(c.Request.Context(),
				`SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid FOR UPDATE`,
				userID).Scan(&balanceBefore); err != nil {
				return err
			}
			balanceAfter := balanceBefore + refundQuota
			if _, err := tx.Exec(c.Request.Context(),
				`UPDATE users SET balance = $1, updated_at = NOW() WHERE id = $2::uuid`,
				balanceAfter, userID); err != nil {
				return err
			}
			refundOrderID := "REF-SUB-" + uuid.New().String()[:8]
			if _, err := tx.Exec(c.Request.Context(),
				`INSERT INTO balance_transactions
				 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
				 VALUES ($1::uuid, 'subscription_refund', $2, 'balance', $3, $4, $5, $6)`,
				userID, refundQuota, balanceBefore, balanceAfter, refundOrderID,
				fmt.Sprintf("Subscription refund: %s (%.1f%% unused)", planName, float64(quotaTotal-quotaUsed)/float64(quotaTotal)*100)); err != nil {
				return err
			}
			if _, err := tx.Exec(c.Request.Context(),
				`INSERT INTO orders (user_id, order_id, amount, currency, exchange_rate, status, product_type, product_name, metadata)
				 VALUES ($1::uuid, $2, $3, $4, $5, 'completed', 'subscription_refund', $6, $7)`,
				userID, refundOrderID, refundQuota, currency, exchangeRate, fmt.Sprintf(T("info.refund_description", c), planName),
				map[string]interface{}{
					"subscription_id":      subID,
					"plan_name":            planName,
					"plan_price_micro_usd": planPrice,
					"actual_paid":          actualPaid,
					"quota_total":          quotaTotal,
					"quota_used":           quotaUsed,
					"refund_ratio":         float64(quotaTotal-quotaUsed) / float64(quotaTotal),
				},
			); err != nil {
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		if txErr.Error() == "already cancelled" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.subscription_not_found", c)}})
			return
		}
		r.logger.Error("Failed to cancel subscription", zap.Error(txErr))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.cancel_subscription_failed", c)}})
		return
	}
	if !cancelled {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.subscription_not_found", c)}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "billing", "subscription_cancelled", "warning",
			fmt.Sprintf("Subscription cancelled: %s, refund: %s (%.1f%% unused)", planName, QuotaToUSD(refundQuota), float64(quotaTotal-quotaUsed)/float64(quotaTotal)*100))
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"message":      T("info.subscription_cancelled", c),
		"refund_quota": refundQuota,
		"refund_ratio": float64(quotaTotal-quotaUsed) / float64(quotaTotal),
		"quota_total":  quotaTotal,
		"quota_used":   quotaUsed,
	})
}

// renewSubscription creates a renewal order for an existing subscription
func (r *SubscriptionRoutes) renewSubscription(c *gin.Context) {
	userID := c.GetString("user_id")
	subID := c.Param("id")

	// Get existing subscription
	var planID, status string
	var expiresAt *time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT plan_id::text, status, expires_at FROM user_subscriptions
		 WHERE id = $1::uuid AND user_id = $2::uuid`,
		subID, userID,
	).Scan(&planID, &status, &expiresAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Subscription not found"}})
		return
	}

	// Get plan details for renewal price
	var planName string
	var price int64
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT name, price FROM subscription_plans WHERE id = $1::uuid AND status = 'active' AND deleted_at IS NULL`,
		planID,
	).Scan(&planName, &price)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Plan not available for renewal"}})
		return
	}

	// Get user's preferred currency and exchange rate
	var currency string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(preferred_currency, 'USD') FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&currency)
	var rateStr string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT value FROM system_settings WHERE key = 'usd_exchange_rate'`,
	).Scan(&rateStr)
	exchangeRate := 7.20
	if rateStr != "" {
		var rateCents int
		_, _ = fmt.Sscanf(rateStr, "%d", &rateCents)
		if rateCents > 0 {
			exchangeRate = float64(rateCents) / 100.0
		}
	}

	// Create renewal order
	orderID := "REN-" + uuid.New().String()[:8]
	var orderUUID string
	err = r.db.QueryRow(c.Request.Context(),
		`INSERT INTO orders (user_id, order_id, amount, currency, exchange_rate, status, product_type, product_name, metadata)
		 VALUES ($1::uuid, $2, $3, $4, $5, 'pending', 'subscription_renewal', $6, $7)
		 RETURNING id::text`,
		userID, orderID, price, currency, exchangeRate, fmt.Sprintf(T("info.renew_description", c), planName),
		map[string]interface{}{
			"plan_id":         planID,
			"subscription_id": subID,
			"is_renewal":      true,
		},
	).Scan(&orderUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create renewal order"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"order_id":   orderID,
		"order_uuid": orderUUID,
		"plan_name":  planName,
		"price":      price,
		"message":    "Renewal order created, please proceed to payment",
	})
}

// Helper functions
