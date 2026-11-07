package routes

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (r *SubscriptionRoutes) listPlans(c *gin.Context) {
	data, err := query.GetAdminPlansData(c.Request.Context(), r.db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list plans"}})
		return
	}

	plans := []gin.H{}
	for _, p := range data.Plans {
		plans = append(plans, gin.H{
			"id": p.ID, "name": p.Name, "description": p.Description,
			"price": p.Price, "quota": p.Quota, "quota_limit": p.Quota, "daily_limit": p.DailyLimit,
			"plan_type": p.PlanType, "duration_days": p.DurationDays,
			"status": p.Status, "created_at": p.CreatedAt,
			"rpm_limit": p.RPMLimit, "rph_limit": p.RPHLimit, "rp5h_limit": p.RP5HLimit,
			"rpd_limit": p.RPDLimit, "rpw_limit": p.RPWLimit,
			"tpm_limit": p.TPMLimit, "tph_limit": p.TPHLimit,
			"tpd_limit": p.TPDLimit, "tpw_limit": p.TPWLimit,
			"concurrent_limit":  p.ConcurrentLimit,
			"model_permissions": p.ModelPermissions,
		})
	}
	c.JSON(http.StatusOK, gin.H{"plans": plans})
}
func (r *SubscriptionRoutes) getPlan(c *gin.Context) {
	planID := c.Param("id")

	var p struct {
		ID              string      `json:"id"`
		Name            string      `json:"name"`
		Description     *string     `json:"description"`
		Price           int64       `json:"price"`
		Quota           int64       `json:"quota"`
		DailyLimit      int64       `json:"daily_limit"`
		PlanType        string      `json:"plan_type"`
		DurationDays    int         `json:"duration_days"`
		Status          string      `json:"status"`
		CreatedAt       interface{} `json:"created_at"`
		RPMLimit        int         `json:"rpm_limit"`
		RPHLimit        int         `json:"rph_limit"`
		RP5HLimit       int         `json:"rp5h_limit"`
		RPDLimit        int         `json:"rpd_limit"`
		RPWLimit        int         `json:"rpw_limit"`
		TPMLimit        int         `json:"tpm_limit"`
		TPHLimit        int         `json:"tph_limit"`
		TPDLimit        int64       `json:"tpd_limit"`
		TPWLimit        int64       `json:"tpw_limit"`
		ConcurrentLimit int         `json:"concurrent_limit"`
	}

	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id, name, description, price, quota, COALESCE(daily_limit, 0), COALESCE(plan_type, 'token'), duration_days,
		        status, created_at,
		        COALESCE(rpm_limit, 0), COALESCE(rph_limit, 0), COALESCE(rp5h_limit, 0),
		        COALESCE(rpd_limit, 0), COALESCE(rpw_limit, 0),
		        COALESCE(tpm_limit, 0), COALESCE(tph_limit, 0), COALESCE(tpd_limit, 0),
		        COALESCE(tpw_limit, 0), COALESCE(concurrent_limit, 0)
		 FROM subscription_plans WHERE id = $1::uuid AND deleted_at IS NULL`, planID,
	).Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.Quota, &p.DailyLimit, &p.PlanType, &p.DurationDays,
		&p.Status, &p.CreatedAt,
		&p.RPMLimit, &p.RPHLimit, &p.RP5HLimit, &p.RPDLimit, &p.RPWLimit,
		&p.TPMLimit, &p.TPHLimit, &p.TPDLimit, &p.TPWLimit, &p.ConcurrentLimit)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Plan not found"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"plan": p})
}
func (r *SubscriptionRoutes) createPlan(c *gin.Context) {
	var req struct {
		Name             string      `json:"name" binding:"required"`
		Description      string      `json:"description"`
		Price            int64       `json:"price" binding:"required"`
		Quota            int64       `json:"quota" binding:"required"`
		DurationDays     int         `json:"duration_days" binding:"required"`
		DailyLimit       int         `json:"daily_limit"`
		RpmLimit         int         `json:"rpm_limit"`
		RphLimit         int         `json:"rph_limit"`
		Rp5hLimit        int         `json:"rp5h_limit"`
		RpdLimit         int         `json:"rpd_limit"`
		RpwLimit         int         `json:"rpw_limit"`
		TpmLimit         int         `json:"tpm_limit"`
		TphLimit         int         `json:"tph_limit"`
		TpdLimit         int         `json:"tpd_limit"`
		TpwLimit         int         `json:"tpw_limit"`
		ConcurrentLimit  int         `json:"concurrent_limit"`
		ModelPermissions interface{} `json:"model_permissions"`
		Features         interface{} `json:"features"`
		PlanType         string      `json:"plan_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Price <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Name and price required"}})
		return
	}
	if req.PlanType == "" {
		req.PlanType = "token"
	}

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO subscription_plans (name, description, price, quota, duration_days, daily_limit,
		 rpm_limit, rph_limit, rp5h_limit, rpd_limit, rpw_limit,
		 tpm_limit, tph_limit, tpd_limit, tpw_limit,
		 concurrent_limit, model_permissions, plan_type, managed_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, 'admin') RETURNING id`,
		req.Name, req.Description, req.Price, req.Quota, req.DurationDays, req.DailyLimit,
		req.RpmLimit, req.RphLimit, req.Rp5hLimit, req.RpdLimit, req.RpwLimit,
		req.TpmLimit, req.TphLimit, req.TpdLimit, req.TpwLimit,
		req.ConcurrentLimit, req.ModelPermissions, req.PlanType,
	).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create plan"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "plan": gin.H{"id": id}})
}
func (r *SubscriptionRoutes) updatePlan(c *gin.Context) {
	planID := c.Param("id")
	var req struct {
		Name             string      `json:"name"`
		Description      string      `json:"description"`
		Price            *int64      `json:"price"`
		Quota            *int64      `json:"quota"`
		DurationDays     *int        `json:"duration_days"`
		DailyLimit       *int        `json:"daily_limit"`
		ModelPermissions interface{} `json:"model_permissions"`
		Features         interface{} `json:"features"`
		Status           string      `json:"status"`
		PlanType         string      `json:"plan_type"`
		RpmLimit         *int        `json:"rpm_limit"`
		RphLimit         *int        `json:"rph_limit"`
		Rp5hLimit        *int        `json:"rp5h_limit"`
		RpdLimit         *int        `json:"rpd_limit"`
		RpwLimit         *int        `json:"rpw_limit"`
		TpmLimit         *int        `json:"tpm_limit"`
		TphLimit         *int        `json:"tph_limit"`
		TpdLimit         *int        `json:"tpd_limit"`
		TpwLimit         *int        `json:"tpw_limit"`
		ConcurrentLimit  *int        `json:"concurrent_limit"`
	}
	_ = c.ShouldBindJSON(&req)

	var setClauses []string
	var args []interface{}
	argIdx := 1

	addField := func(col string, val interface{}) {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", col, argIdx))
		args = append(args, val)
		argIdx++
	}

	if req.Name != "" {
		addField("name", req.Name)
	}
	if req.Description != "" {
		addField("description", req.Description)
	}
	if req.Price != nil {
		addField("price", *req.Price)
	}
	if req.Quota != nil {
		addField("quota", *req.Quota)
	}
	if req.DurationDays != nil {
		addField("duration_days", *req.DurationDays)
	}
	if req.DailyLimit != nil {
		addField("daily_limit", *req.DailyLimit)
	}
	if req.Status != "" {
		addField("status", req.Status)
	}
	if req.PlanType != "" {
		addField("plan_type", req.PlanType)
	}
	if req.RpmLimit != nil {
		addField("rpm_limit", *req.RpmLimit)
	}
	if req.RphLimit != nil {
		addField("rph_limit", *req.RphLimit)
	}
	if req.Rp5hLimit != nil {
		addField("rp5h_limit", *req.Rp5hLimit)
	}
	if req.RpdLimit != nil {
		addField("rpd_limit", *req.RpdLimit)
	}
	if req.RpwLimit != nil {
		addField("rpw_limit", *req.RpwLimit)
	}
	if req.TpmLimit != nil {
		addField("tpm_limit", *req.TpmLimit)
	}
	if req.TphLimit != nil {
		addField("tph_limit", *req.TphLimit)
	}
	if req.TpdLimit != nil {
		addField("tpd_limit", *req.TpdLimit)
	}
	if req.TpwLimit != nil {
		addField("tpw_limit", *req.TpwLimit)
	}
	if req.ConcurrentLimit != nil {
		addField("concurrent_limit", *req.ConcurrentLimit)
	}
	// ModelPermissions uses interface{} (JSON array); nil = leave unchanged,
	// empty array clears the restriction.
	if req.ModelPermissions != nil {
		addField("model_permissions", req.ModelPermissions)
	}

	if len(setClauses) > 0 {
		// Mark the row as admin-managed so future seed runs do not overwrite it.
		setClauses = append(setClauses, "managed_by = 'admin'", "updated_at = NOW()")
		sql := fmt.Sprintf("UPDATE subscription_plans SET %s WHERE id = $%d::uuid", strings.Join(setClauses, ", "), argIdx)
		args = append(args, planID)
		if err := r.db.Exec(c.Request.Context(), sql, args...); err != nil {
			r.logger.Error("Failed to update subscription plan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update plan"}})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
func (r *SubscriptionRoutes) togglePlan(c *gin.Context) {
	planID := c.Param("id")
	var currentStatus string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT status FROM subscription_plans WHERE id = $1::uuid AND deleted_at IS NULL`, planID,
	).Scan(&currentStatus)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": gin.H{"message": T("error.plan_not_found", c)}})
		return
	}

	newStatus := "inactive"
	if currentStatus == "inactive" {
		newStatus = "active"
	}

	if err := r.db.Exec(c.Request.Context(),
		`UPDATE subscription_plans SET status = $1, managed_by = 'admin', updated_at = NOW() WHERE id = $2::uuid`, newStatus, planID,
	); err != nil {
		r.logger.Error("Failed to toggle subscription plan", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": gin.H{"message": T("error.update_failed", c)}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": T("info.plan_status_updated", c)})
}
func (r *SubscriptionRoutes) deletePlan(c *gin.Context) {
	planID := c.Param("id")

	var count int
	if err := r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM user_subscriptions WHERE plan_id = $1::uuid AND status = 'active'`, planID,
	).Scan(&count); err != nil {
		r.logger.Error("Failed to check active subscriptions", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": gin.H{"message": T("error.query_failed", c)}})
		return
	}
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": gin.H{"message": T("error.plan_has_active_subscriptions", c)}})
		return
	}

	var planName, planType string
	var planPrice int64
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT name, plan_type, price FROM subscription_plans WHERE id = $1::uuid`, planID,
	).Scan(&planName, &planType, &planPrice)

	if err := r.db.Exec(c.Request.Context(),
		`UPDATE subscription_plans SET status = 'inactive', deleted_at = NOW() WHERE id = $1::uuid`, planID); err != nil {
		r.logger.Error("Failed to delete plan", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": gin.H{"message": fmt.Sprintf(T("error.delete_failed_with_reason", c), err.Error())}})
		return
	}

	if r.audit != nil {
		detailsJSON := fmt.Sprintf(`{"plan_id":"%s","plan_name":"%s","plan_type":"%s","price_micro_usd":%d}`, planID, planName, planType, planPrice)
		r.audit.LogWithDetails(c.Request.Context(), c.GetString("user_id"), c.GetString("email"),
			"admin", "plan_deleted", "warning",
			fmt.Sprintf(T("info.plan_deleted", c), planName, planID),
			detailsJSON)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": T("info.plan_deleted_msg", c)})
}
func (r *SubscriptionRoutes) listPublicPlans(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT sp.id::text, sp.name, sp.description, sp.price, sp.quota, sp.duration_days,
		        COALESCE(sp.daily_limit, 0), sp.plan_type, sp.sort_order,
		        COALESCE(sp.rpm_limit, 0), COALESCE(sp.rpw_limit, 0),
		        COALESCE(sp.tpm_limit, 0), COALESCE(sp.tpw_limit, 0), COALESCE(sp.concurrent_limit, 0),
		        sp.model_permissions
		 FROM subscription_plans sp
		 WHERE sp.status = 'active' AND sp.deleted_at IS NULL
		 ORDER BY sp.sort_order ASC, sp.price ASC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list plans"}})
		return
	}
	defer rows.Close()

	plans := []gin.H{}
	for rows.Next() {
		var id, name, planType string
		var description *string
		var price int64
		var quota, dailyLimit int64
		var durationDays int
		var sortOrder int
		var rpmLimit, rpwLimit, tpmLimit, tpwLimit, concurrentLimit int
		var modelPerms interface{}
		if err := rows.Scan(&id, &name, &description, &price, &quota, &durationDays, &dailyLimit, &planType, &sortOrder,
			&rpmLimit, &rpwLimit, &tpmLimit, &tpwLimit, &concurrentLimit, &modelPerms); err != nil {
			continue
		}

		// Calculate display values
		durationDisplay := T("duration.forever", c)
		if durationDays > 0 {
			switch durationDays {
			case 30:
				durationDisplay = T("duration.one_month", c)
			case 365:
				durationDisplay = T("duration.one_year", c)
			default:
				durationDisplay = fmt.Sprintf(T("duration.n_days", c), stringInt(durationDays))
			}
		}

		plans = append(plans, gin.H{
			"id":                id,
			"name":              name,
			"description":       description,
			"price":             price,
			"quota":             quota,
			"duration_days":     durationDays,
			"duration_display":  durationDisplay,
			"daily_limit":       dailyLimit,
			"plan_type":         planType,
			"sort_order":        sortOrder,
			"rpm_limit":         rpmLimit,
			"rpw_limit":         rpwLimit,
			"tpm_limit":         tpmLimit,
			"tpw_limit":         tpwLimit,
			"concurrent_limit":  concurrentLimit,
			"model_permissions": modelPerms,
		})
	}

	c.JSON(http.StatusOK, gin.H{"plans": plans})
}

// getMySubscriptions returns the current user's subscriptions
