package routes

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (r *VoucherRoutes) list(c *gin.Context) {
	page, limit := parseVoucherPagination(c, voucherPaginationDefaultLimit)
	voucherType := c.Query("type")
	status := c.Query("status")

	where := " WHERE v.deleted_at IS NULL"
	countWhere := " WHERE deleted_at IS NULL"
	args := []interface{}{}
	argIdx := 1

	if voucherType != "" && voucherType != "all" {
		where += fmt.Sprintf(" AND v.type = $%d", argIdx)
		countWhere += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, voucherType)
		argIdx++
	}
	if status != "" && status != "all" {
		if status == "expired" {
			where += " AND v.status = 'active' AND v.expires_at < NOW()"
			countWhere += " AND status = 'active' AND expires_at < NOW()"
		} else {
			where += fmt.Sprintf(" AND v.status = $%d", argIdx)
			countWhere += fmt.Sprintf(" AND status = $%d", argIdx)
			args = append(args, status)
			argIdx++
		}
	}

	var total int
	countSQL := "SELECT COUNT(*) FROM vouchers" + countWhere
	_ = r.db.QueryRow(c.Request.Context(), countSQL, args...).Scan(&total)

	offset := (page - 1) * limit
	dataSQL := `SELECT v.id, v.code, COALESCE(v.name, ''), v.type, v.owner_type, v.amount, v.reward_type, v.max_uses, v.used_count,
				   v.status, v.expires_at, v.created_at, COALESCE(v.description, ''), v.plan_id, COALESCE(u.name, ''), COALESCE(u.email, '')
			 FROM vouchers v
			 LEFT JOIN users u ON v.created_by = u.id OR v.user_id = u.id` + where +
		` ORDER BY v.created_at DESC LIMIT $` + fmt.Sprintf("%d", argIdx) + ` OFFSET $` + fmt.Sprintf("%d", argIdx+1)
	args = append(args, limit, offset)

	rows, err := r.db.Query(c.Request.Context(), dataSQL, args...)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"vouchers": []gin.H{}})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, code, name, vtype, ownerType, status, rewardType, creatorName, creatorEmail string
		var amount int64
		var maxUses, usedCount int
		var description string
		var planID *string
		var expiresAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&id, &code, &name, &vtype, &ownerType, &amount, &rewardType, &maxUses, &usedCount,
			&status, &expiresAt, &createdAt, &description, &planID, &creatorName, &creatorEmail); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Scan error: " + err.Error()}})
			return
		}
		item := gin.H{
			"id": id, "code": code, "name": name, "type": vtype, "owner_type": ownerType,
			"amount": amount, "reward_type": rewardType, "max_uses": maxUses, "used_count": usedCount,
			"status": status, "expires_at": expiresAt, "created_at": createdAt,
			"description": description, "creator_name": creatorName, "creator_email": creatorEmail,
		}
		if planID != nil {
			item["plan_id"] = *planID
		}
		items = append(items, item)
	}

	totalPages := (total + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}
	c.JSON(http.StatusOK, gin.H{"vouchers": items, "total": total, "page": page, "total_pages": totalPages})
}

func (r *VoucherRoutes) create(c *gin.Context) {
	var req struct {
		Name        string  `json:"name" binding:"required"`
		Type        string  `json:"type"`
		Amount      float64 `json:"amount" binding:"required"`
		Count       int     `json:"count"`
		MaxUses     int     `json:"max_uses"`
		RewardType  string  `json:"reward_type"`
		Description string  `json:"description"`
		PlanID      string  `json:"plan_id"`
		ExpiresAt   *string `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.name_amount_required", c)}})
		return
	}

	if req.Count < 1 {
		req.Count = 1
	}
	if req.Count > maxVoucherCreateCount {
		req.Count = maxVoucherCreateCount
	}
	if req.Type == "" {
		req.Type = "gift"
	}
	if req.MaxUses < minInvitationMaxUses {
		req.MaxUses = minInvitationMaxUses
	}
	if req.RewardType == "" {
		req.RewardType = "gift_balance"
	}

	amountQuota := USDtoQuota(req.Amount)
	userID := c.GetString("user_id")

	var expires interface{}
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		parsedTime, err := time.Parse("2006-01-02", *req.ExpiresAt)
		if err == nil {
			expires = parsedTime
		}
	}

	codes := []string{}
	tx, err := r.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.create_redemption_failed", c)}})
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	prefix := "VG"
	if req.Type == "welcome" {
		prefix = "WC"
	}
	voucherType := req.Type
	if req.PlanID != "" {
		if voucherType == "plan" {
			voucherType = "plan_token"
		}
	}
	for i := 0; i < req.Count; i++ {
		code := generateVoucherCode(prefix)
		var planID interface{}
		if req.PlanID != "" {
			planID = req.PlanID
		}
		_, err := tx.Exec(c.Request.Context(),
			`INSERT INTO vouchers (code, name, type, owner_type, amount, max_uses, reward_type, description, plan_id, expires_at, created_by)
			 VALUES ($1, $2, $3, 'admin', $4, $5, $6, $7, $8, $9, $10::uuid)`,
			code, req.Name, voucherType, amountQuota, req.MaxUses, req.RewardType, req.Description, planID, expires, userID)
		if err != nil {
			code = generateVoucherCode(prefix)
			_, err = tx.Exec(c.Request.Context(),
				`INSERT INTO vouchers (code, name, type, owner_type, amount, max_uses, reward_type, description, plan_id, expires_at, created_by)
				 VALUES ($1, $2, $3, 'admin', $4, $5, $6, $7, $8, $9, $10::uuid)`,
				code, req.Name, voucherType, amountQuota, req.MaxUses, req.RewardType, req.Description, planID, expires, userID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
				return
			}
		}
		codes = append(codes, code)
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.create_redemption_failed", c)}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": T("info.codes_created", c), "codes": codes, "count": len(codes)})
}

func (r *VoucherRoutes) update(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Status      string  `json:"status"`
		Name        string  `json:"name"`
		Description string  `json:"description"`
		ExpiresAt   *string `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	updates := []string{}
	args := []interface{}{}
	idx := 1

	if req.Status != "" {
		updates = append(updates, "status = $"+strconv.Itoa(idx))
		args = append(args, req.Status)
		idx++
	}
	if req.Name != "" {
		updates = append(updates, "name = $"+strconv.Itoa(idx))
		args = append(args, req.Name)
		idx++
	}
	if req.Description != "" {
		updates = append(updates, "description = $"+strconv.Itoa(idx))
		args = append(args, req.Description)
		idx++
	}
	if req.ExpiresAt != nil {
		if *req.ExpiresAt == "" {
			updates = append(updates, "expires_at = NULL")
		} else {
			updates = append(updates, "expires_at = $"+strconv.Itoa(idx))
			args = append(args, *req.ExpiresAt)
			idx++
		}
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.no_changes", c)}})
		return
	}

	updates = append(updates, "updated_at = NOW()")
	args = append(args, id)

	_, err := r.db.Pool.Exec(c.Request.Context(),
		"UPDATE vouchers SET "+strings.Join(updates, ", ")+" WHERE id = $"+strconv.Itoa(idx)+"::uuid",
		args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.update_redemption_failed", c)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *VoucherRoutes) toggle(c *gin.Context) {
	id := c.Param("id")
	var current string
	_ = r.db.QueryRow(c.Request.Context(), `SELECT status FROM vouchers WHERE id = $1::uuid`, id).Scan(&current)
	newStatus := "active"
	if current == "active" {
		newStatus = "disabled"
	}
	err := r.db.Exec(c.Request.Context(), "UPDATE vouchers SET status = $1 WHERE id = $2::uuid", newStatus, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.operation_failed", c)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "status": newStatus})
}

func (r *VoucherRoutes) delete(c *gin.Context) {
	id := c.Param("id")
	if err := r.db.Exec(c.Request.Context(),
		`UPDATE vouchers SET deleted_at = NOW() WHERE id = $1::uuid AND deleted_at IS NULL`, id); err != nil {
		r.logger.Error("Failed to delete voucher", zap.Error(err))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *VoucherRoutes) listRecords(c *gin.Context) {
	page, limit := parseVoucherPagination(c, voucherPaginationDefaultLimit)

	var total int
	_ = r.db.QueryRow(c.Request.Context(), `SELECT COUNT(*) FROM voucher_uses`).Scan(&total)

	offset := (page - 1) * limit
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT vu.id, v.code, v.type, vu.amount, vu.consumer_type, vu.reward_granted,
				   u.name as user_name, u.email as user_email, vu.created_at
		 FROM voucher_uses vu
		 JOIN vouchers v ON vu.voucher_id = v.id
		 JOIN users u ON vu.user_id = u.id
		 ORDER BY vu.created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"records": []gin.H{}})
		return
	}
	defer rows.Close()

	records := []gin.H{}
	for rows.Next() {
		var id, code, vtype, consumerType, userName, userEmail string
		var amount int64
		var rewardGranted bool
		var createdAt time.Time
		_ = rows.Scan(&id, &code, &vtype, &amount, &consumerType, &rewardGranted, &userName, &userEmail, &createdAt)
		records = append(records, gin.H{
			"id": id, "code": code, "type": vtype, "amount": amount,
			"consumer_type": consumerType, "reward_granted": rewardGranted,
			"user_name": userName, "user_email": userEmail, "created_at": createdAt,
		})
	}

	totalPages := (total + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}
	c.JSON(http.StatusOK, gin.H{"records": records, "total": total, "page": page, "total_pages": totalPages})
}

func (r *VoucherRoutes) stats(c *gin.Context) {
	var total, active, disabled, exhausted int
	var totalAmount int64
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'active'),
				COUNT(*) FILTER (WHERE status = 'disabled'),
				COUNT(*) FILTER (WHERE status = 'exhausted'),
				COALESCE(SUM(amount * used_count), 0)
		 FROM vouchers WHERE deleted_at IS NULL`).Scan(&total, &active, &disabled, &exhausted, &totalAmount)

	var totalUses int
	var totalUsedAmount int64
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM voucher_uses`).Scan(&totalUses, &totalUsedAmount)

	c.JSON(http.StatusOK, gin.H{
		"vouchers": gin.H{"total": total, "active": active, "disabled": disabled, "exhausted": exhausted, "total_redeemed_amount": totalAmount},
		"uses":     gin.H{"total": totalUses, "total_amount": totalUsedAmount},
	})
}

func (r *VoucherRoutes) getWelcome(c *gin.Context) {
	var id, code, status string
	var amount int64
	var expiresAt *time.Time
	var createdAt time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, code, amount, status, expires_at, created_at
		 FROM vouchers WHERE type = 'welcome' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`,
	).Scan(&id, &code, &amount, &status, &expiresAt, &createdAt)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"welcome": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"welcome": gin.H{
			"id": id, "code": code, "amount": amount, "amount_display": QuotaToUSD(amount),
			"status": status, "expires_at": expiresAt, "created_at": createdAt,
		},
	})
}

func (r *VoucherRoutes) saveWelcome(c *gin.Context) {
	var req struct {
		Amount    float64 `json:"amount"`
		Status    string  `json:"status"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_parameter", c)}})
		return
	}
	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.amount_must_be_positive", c)}})
		return
	}

	amountQuota := USDtoQuota(req.Amount)
	userID := c.GetString("user_id")

	var existingID string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT id::text FROM vouchers WHERE type = 'welcome' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`,
	).Scan(&existingID)

	if existingID != "" {
		updates := "amount = $1, updated_at = NOW()"
		args := []interface{}{amountQuota}
		idx := 2
		if req.Status != "" {
			updates += fmt.Sprintf(", status = $%d", idx)
			args = append(args, req.Status)
			idx++
		}
		if req.ExpiresAt != nil {
			if *req.ExpiresAt == "" {
				updates += ", expires_at = NULL"
			} else {
				updates += fmt.Sprintf(", expires_at = $%d::date", idx)
				args = append(args, *req.ExpiresAt)
				idx++
			}
		}
		args = append(args, existingID)
		_, err := r.db.Pool.Exec(c.Request.Context(),
			"UPDATE vouchers SET "+updates+" WHERE id = $"+strconv.Itoa(idx)+"::uuid", args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.update_failed", c)}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": T("info.welcome_code_updated", c)})
		return
	}

	code := generateVoucherCode("WC")
	var expires interface{}
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		parsedTime, err := time.Parse("2006-01-02", *req.ExpiresAt)
		if err == nil {
			expires = parsedTime
		}
	}
	status := "active"
	if req.Status != "" {
		status = req.Status
	}

	_, err := r.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO vouchers (code, name, type, owner_type, amount, max_uses, status, reward_type, expires_at, created_by)
		 VALUES ($1, '欢迎奖励', 'welcome', 'system', $2, $3, $4, 'gift_balance', $5, $6::uuid)`,
		code, amountQuota, welcomeVoucherMaxUses, status, expires, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.create_failed", c)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": T("info.welcome_code_created", c), "code": code})
}

func (r *VoucherRoutes) getSettings(c *gin.Context) {
	settings := map[string]string{
		"invitation_max_codes_per_user":  strconv.Itoa(defaultInvitationMaxCodesPerUser),
		"invitation_inviter_reward":      strconv.FormatInt(defaultInvitationInviterReward, 10),
		"invitation_invitee_reward":      strconv.FormatInt(defaultInvitationInviteeReward, 10),
		"invitation_default_max_uses":    strconv.Itoa(defaultInvitationMaxUses),
		"invitation_default_expire_days": strconv.Itoa(defaultInvitationExpireDays),
	}
	result := gin.H{}
	for key, defaultVal := range settings {
		var val string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(value, $1) FROM system_settings WHERE key = $2`, defaultVal, key).Scan(&val)
		result[key] = val
	}
	c.JSON(http.StatusOK, gin.H{"settings": result})
}

func (r *VoucherRoutes) saveSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_parameter", c)}})
		return
	}
	validInvitationSettings := map[string]bool{
		"invitation_max_codes_per_user":  true,
		"invitation_inviter_reward":      true,
		"invitation_invitee_reward":      true,
		"invitation_default_max_uses":    true,
		"invitation_default_expire_days": true,
	}
	for key, val := range req {
		if !strings.HasPrefix(key, "invitation_") {
			continue
		}
		if !validInvitationSettings[key] {
			continue
		}
		// Reject non-numeric values (e.g. "NaN" from a broken client) instead
		// of persisting them into system_settings.
		if _, err := strconv.ParseInt(val, 10, 64); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_parameter", c)}})
			return
		}
		if err := r.db.Exec(c.Request.Context(),
			`INSERT INTO system_settings (key, value, updated_at) VALUES ($1, $2, NOW())
			 ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = NOW()`,
			key, val); err != nil {
			r.logger.Error("Failed to save setting", zap.String("key", key), zap.Error(err))
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": T("info.settings_saved", c)})
}

func (r *VoucherRoutes) listPlans(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, name, plan_type FROM subscription_plans WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"plans": []gin.H{}})
		return
	}
	defer rows.Close()
	plans := []gin.H{}
	for rows.Next() {
		var id, name, planType string
		_ = rows.Scan(&id, &name, &planType)
		plans = append(plans, gin.H{"id": id, "name": name, "plan_type": planType})
	}
	c.JSON(http.StatusOK, gin.H{"plans": plans})
}
