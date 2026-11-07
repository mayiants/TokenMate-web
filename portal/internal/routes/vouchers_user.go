package routes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func generateInviteCode() string {
	return "INV-" + generateRandomCode("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", invitationCodeRandomLength)
}

func generateAffCode() string {
	return generateRandomCode("abcdefghijklmnopqrstuvwxyz0123456789", affiliateCodeRandomLength)
}

func (r *VoucherRoutes) redeem(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.enter_redemption_code", c)}})
		return
	}

	userID := c.GetString("user_id")
	code := strings.ToUpper(strings.TrimSpace(req.Code))

	// Read voucher details for validation (no lock yet).
	var id, codeType, ownerType, status string
	var amount int64
	var maxUses, usedCount int
	var expiresAt *time.Time
	var planID *string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id, type, owner_type, amount, max_uses, used_count, status, expires_at, plan_id
		 FROM vouchers WHERE code = $1 AND deleted_at IS NULL`, code,
	).Scan(&id, &codeType, &ownerType, &amount, &maxUses, &usedCount, &status, &expiresAt, &planID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_voucher_code", c)}})
		return
	}

	if status != "active" {
		msg := T("error.voucher_disabled", c)
		if status == "exhausted" {
			msg = T("error.voucher_already_used", c)
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": msg}})
		return
	}

	if codeType == "welcome" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.welcome_code_only_on_register", c)}})
		return
	}

	if expiresAt != nil && expiresAt.Before(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.voucher_expired", c)}})
		return
	}

	if maxUses > 0 && usedCount >= maxUses {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.voucher_already_used", c)}})
		return
	}

	// Check if user already used this voucher
	var existingID string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT id FROM voucher_uses WHERE voucher_id = $1::uuid AND user_id = $2::uuid`,
		id, userID).Scan(&existingID)
	if existingID != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.voucher_already_used", c)}})
		return
	}

	// Plan voucher: activate subscription instead of adding gift_balance
	if (codeType == "plan_token" || codeType == "plan_code") && planID != nil && *planID != "" {
		r.redeemPlanVoucher(c, id, *planID, codeType, amount, maxUses, usedCount, status, userID, code)
		return
	}

	// Redeem balance voucher inside a transaction with row-level locking to
	// prevent concurrent over-redemption and ensure atomic balance updates.
	var giftBalanceBefore int64
	txErr := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		// Lock the voucher row to serialize concurrent redemptions.
		var txUsedCount int
		var txStatus string
		if err := tx.QueryRow(c.Request.Context(),
			`SELECT used_count, status FROM vouchers WHERE id = $1::uuid FOR UPDATE`, id,
		).Scan(&txUsedCount, &txStatus); err != nil {
			return err
		}
		if txStatus != "active" || (maxUses > 0 && txUsedCount >= maxUses) {
			return errors.New("voucher exhausted")
		}

		// Get user gift_balance for tracking (within tx for consistency)
		_ = tx.QueryRow(c.Request.Context(),
			`SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1::uuid`, userID).Scan(&giftBalanceBefore)

		// Update user gift_balance only (Plan B: balance = real funds, gift_balance = promo)
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE users SET gift_balance = COALESCE(gift_balance, 0) + $1, updated_at = NOW() WHERE id = $2::uuid`,
			amount, userID); err != nil {
			return err
		}

		// Conditionally update voucher used_count / status (prevents over-redemption)
		newUsedCount := txUsedCount + 1
		newStatus := txStatus
		if maxUses > 0 && newUsedCount >= maxUses {
			newStatus = "exhausted"
		}
		ct, err := tx.Exec(c.Request.Context(),
			`UPDATE vouchers SET used_count = $1, status = $2, updated_at = NOW()
			 WHERE id = $3::uuid AND status = 'active' AND (max_uses = 0 OR used_count < max_uses)`,
			newUsedCount, newStatus, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return errors.New("voucher exhausted")
		}

		// Record use in voucher_uses
		if _, err := tx.Exec(c.Request.Context(),
			`INSERT INTO voucher_uses (voucher_id, user_id, consumer_type, amount, balance_before, balance_after)
			 VALUES ($1::uuid, $2::uuid, 'redeemer', $3, $4, $5)`,
			id, userID, amount, giftBalanceBefore, giftBalanceBefore+amount); err != nil {
			return err
		}
		// Record balance transaction
		if _, err := tx.Exec(c.Request.Context(),
			`INSERT INTO balance_transactions
			 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
			 VALUES ($1::uuid, 'voucher', $2, 'gift', $3, $4, $5, $6)`,
			userID, amount, giftBalanceBefore, giftBalanceBefore+amount, id,
			fmt.Sprintf("Voucher redemption: %s", code)); err != nil {
			return err
		}
		return nil
	})
	if txErr != nil {
		if txErr.Error() == "voucher exhausted" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.voucher_already_used", c)}})
			return
		}
		r.logger.Error("Failed to redeem voucher", zap.Error(txErr))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to redeem voucher"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"message":    T("info.voucher_redeemed", c),
		"amount":     amount,
		"newBalance": giftBalanceBefore + amount,
	})
}

func (r *VoucherRoutes) redeemPlanVoucher(c *gin.Context, voucherID, planIDStr, codeType string, amount int64, maxUses, usedCount int, status, userID, code string) {
	var planName, planType string
	var planQuota int64
	var durationDays int
	var dailyLimit int64
	var rphLimit, rp5hLimit, rpdLimit, rpwLimit, tphLimit, tpdLimit, tpwLimit, concurrentLimit int
	var actualQuota int64
	var subID, action string

	// Wrap the full redemption in a transaction with row-level locking to
	// prevent concurrent over-redemption and ensure atomic subscription changes.
	txErr := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		// Lock the voucher row to serialize concurrent redemptions.
		var txUsedCount int
		var txStatus string
		if err := tx.QueryRow(c.Request.Context(),
			`SELECT used_count, status FROM vouchers WHERE id = $1::uuid FOR UPDATE`, voucherID,
		).Scan(&txUsedCount, &txStatus); err != nil {
			return err
		}
		if txStatus != "active" || (maxUses > 0 && txUsedCount >= maxUses) {
			return errors.New("voucher exhausted")
		}

		// Fetch plan details
		var planPrice int64
		if err := tx.QueryRow(c.Request.Context(),
			`SELECT name, plan_type, price, quota, duration_days, COALESCE(daily_limit,0),
			        COALESCE(rph_limit,0), COALESCE(rp5h_limit,0), COALESCE(rpd_limit,0), COALESCE(rpw_limit,0),
			        COALESCE(tph_limit,0), COALESCE(tpd_limit,0), COALESCE(tpw_limit,0), COALESCE(concurrent_limit,0)
			 FROM subscription_plans WHERE id = $1::uuid AND status = 'active' AND deleted_at IS NULL`, planIDStr,
		).Scan(&planName, &planType, &planPrice, &planQuota, &durationDays, &dailyLimit,
			&rphLimit, &rp5hLimit, &rpdLimit, &rpwLimit,
			&tphLimit, &tpdLimit, &tpwLimit, &concurrentLimit); err != nil {
			return errors.New("plan not found")
		}

		// Calculate prorated quota: ratio = min(1.0, voucher.amount / plan.price)
		ratio := 1.0
		if planPrice > 0 {
			ratio = float64(amount) / float64(planPrice)
			if ratio > 1.0 {
				ratio = 1.0
			}
		}
		actualQuota = int64(float64(planQuota) * ratio)

		// Conditionally update voucher used_count / status (prevents over-redemption)
		newUsedCount := txUsedCount + 1
		newStatus := txStatus
		if maxUses > 0 && newUsedCount >= maxUses {
			newStatus = "exhausted"
		}
		ct, err := tx.Exec(c.Request.Context(),
			`UPDATE vouchers SET used_count = $1, status = $2, updated_at = NOW()
			 WHERE id = $3::uuid AND status = 'active' AND (max_uses = 0 OR used_count < max_uses)`,
			newUsedCount, newStatus, voucherID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return errors.New("voucher exhausted")
		}

		// Record in voucher_uses
		if _, err := tx.Exec(c.Request.Context(),
			`INSERT INTO voucher_uses (voucher_id, user_id, consumer_type, amount, balance_before, balance_after)
			 VALUES ($1::uuid, $2::uuid, 'redeemer', $3, 0, 0)`,
			voucherID, userID, amount); err != nil {
			return err
		}

		// Check if user already has active subscription for this plan
		var existingSubID string
		err = tx.QueryRow(c.Request.Context(),
			`SELECT id::text FROM user_subscriptions WHERE user_id = $1::uuid AND plan_id = $2::uuid AND status = 'active'`,
			userID, planIDStr).Scan(&existingSubID)

		var expiresAt interface{}
		if durationDays > 0 {
			expiresAt = time.Now().AddDate(0, 0, durationDays)
		}

		if err == nil && existingSubID != "" {
			// Prorate duration to match prorated quota for extensions
			actualDurationDays := durationDays
			if ratio < 1.0 && durationDays > 0 {
				actualDurationDays = int(float64(durationDays) * ratio)
				if actualDurationDays < 1 {
					actualDurationDays = 1
				}
			}
			var extensionExpiresAt interface{}
			if actualDurationDays > 0 {
				extensionExpiresAt = time.Now().AddDate(0, 0, actualDurationDays)
			}
			// Extend existing subscription
			if _, err := tx.Exec(c.Request.Context(),
				`UPDATE user_subscriptions SET
						quota_total = quota_total + $1,
						quota_remaining = quota_remaining + $1,
						expires_at = CASE
							WHEN $2::timestamp IS NOT NULL AND expires_at IS NOT NULL THEN expires_at + INTERVAL '1 day' * $3
							WHEN $2::timestamp IS NOT NULL THEN $2::timestamp
							ELSE NULL
						END,
						updated_at = NOW()
					WHERE id = $4::uuid`,
				actualQuota, extensionExpiresAt, actualDurationDays, existingSubID); err != nil {
				return err
			}
			subID = existingSubID
			action = "extended"
		} else {
			// Create new subscription
			if err := tx.QueryRow(c.Request.Context(),
				`INSERT INTO user_subscriptions (
					user_id, plan_id, plan_name, plan_type,
					quota_total, quota_used, quota_remaining,
					daily_limit, duration_days,
					rph_limit, rp5h_limit, rpd_limit, rpw_limit,
					tph_limit, tpd_limit, tpw_limit,
					concurrent_limit,
					status, started_at, expires_at)
				VALUES (
					$1::uuid, $2::uuid, $3, $4,
					$5, 0, $5,
					$6, $7,
					$8, $9, $10, $11,
					$12, $13, $14,
					$15,
					'active', NOW(), $16)
				RETURNING id::text`,
				userID, planIDStr, planName, planType,
				actualQuota, dailyLimit, durationDays,
				rphLimit, rp5hLimit, rpdLimit, rpwLimit,
				tphLimit, tpdLimit, tpwLimit,
				concurrentLimit,
				expiresAt).Scan(&subID); err != nil {
				return err
			}
			action = "created"
		}
		return nil
	})
	if txErr != nil {
		switch txErr.Error() {
		case "voucher exhausted":
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.voucher_already_used", c)}})
		case "plan not found":
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.voucher_no_plan", c)}})
		default:
			r.logger.Error("Failed to redeem plan voucher", zap.Error(txErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to activate subscription"}})
		}
		return
	}

	if action == "created" {
		metrics.GetMetrics().SubscriptionCreated.Add(1)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"message":         fmt.Sprintf(T("info.voucher_subscription_activated", c), planName, QuotaToUSD(actualQuota)),
		"plan_name":       planName,
		"quota":           actualQuota,
		"duration_days":   durationDays,
		"subscription_id": subID,
		"action":          action,
	})
}

func (r *VoucherRoutes) generate(c *gin.Context) {
	userID := c.GetString("user_id")

	var maxCodesStr string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(value, $1) FROM system_settings WHERE key = 'invitation_max_codes_per_user'`,
		strconv.Itoa(defaultInvitationMaxCodesPerUser)).Scan(&maxCodesStr)
	maxCodes := defaultInvitationMaxCodesPerUser
	_, _ = fmt.Sscanf(maxCodesStr, "%d", &maxCodes)

	var activeCount int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM vouchers WHERE user_id = $1::uuid AND type = 'referral' AND status = 'active'`, userID).Scan(&activeCount)
	if activeCount >= maxCodes {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf(T("error.invitation_limit_reached", c), maxCodes)}})
		return
	}

	// Ensure aff_code exists
	var affCode string
	_ = r.db.QueryRow(c.Request.Context(), "SELECT aff_code FROM users WHERE id = $1::uuid", userID).Scan(&affCode)
	if affCode == "" {
		affCode = generateAffCode()
		if err := r.db.Exec(c.Request.Context(), "UPDATE users SET aff_code = $1 WHERE id = $2::uuid", affCode, userID); err != nil {
			r.logger.Error("Failed to set aff code", zap.Error(err))
		}
	}

	// Get defaults from settings
	var inviterReward, inviteeReward int64
	var s string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(value, $1) FROM system_settings WHERE key = 'invitation_inviter_reward'`,
		strconv.FormatInt(defaultInvitationInviterReward, 10)).Scan(&s)
	_, _ = fmt.Sscanf(s, "%d", &inviterReward)
	if inviterReward == 0 {
		inviterReward = defaultInvitationInviterReward
	}
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(value, $1) FROM system_settings WHERE key = 'invitation_invitee_reward'`,
		strconv.FormatInt(defaultInvitationInviteeReward, 10)).Scan(&s)
	_, _ = fmt.Sscanf(s, "%d", &inviteeReward)
	if inviteeReward == 0 {
		inviteeReward = defaultInvitationInviteeReward
	}

	var maxUsesStr, expireDaysStr string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(value, $1) FROM system_settings WHERE key = 'invitation_default_max_uses'`,
		strconv.Itoa(defaultInvitationMaxUses)).Scan(&maxUsesStr)
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(value, $1) FROM system_settings WHERE key = 'invitation_default_expire_days'`,
		strconv.Itoa(defaultInvitationExpireDays)).Scan(&expireDaysStr)
	defaultMaxUses := defaultInvitationMaxUses
	defaultExpireDays := defaultInvitationExpireDays
	_, _ = fmt.Sscanf(maxUsesStr, "%d", &defaultMaxUses)
	_, _ = fmt.Sscanf(expireDaysStr, "%d", &defaultExpireDays)
	if defaultMaxUses > maxInvitationMaxUses {
		defaultMaxUses = maxInvitationMaxUses
	}
	if defaultMaxUses < minInvitationMaxUses {
		defaultMaxUses = minInvitationMaxUses
	}
	if defaultExpireDays > defaultInvitationExpireDays {
		defaultExpireDays = defaultInvitationExpireDays
	}
	if defaultExpireDays < minInvitationExpireDays {
		defaultExpireDays = minInvitationExpireDays
	}

	expiresAt := time.Now().Add(time.Duration(defaultExpireDays) * 24 * time.Hour)
	code := generateInviteCode()

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO vouchers (code, type, owner_type, user_id, amount, reward_type, reward_config, max_uses, expires_at, status)
		 VALUES ($1, 'referral', 'user', $2::uuid, $3, 'gift_balance', $4, $5, $6, 'active') RETURNING id`,
		code, userID, inviteeReward,
		fmt.Sprintf(`{"inviter":{"amount":%d,"destination":"gift_balance"},"invitee":{"amount":%d,"destination":"gift_balance"}}`, inviterReward, inviteeReward),
		defaultMaxUses, expiresAt).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.generate_failed", c)}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id": id, "code": code, "aff_code": affCode,
		"max_uses": defaultMaxUses, "expires_at": expiresAt,
	})
}

func (r *VoucherRoutes) myCodes(c *gin.Context) {
	userID := c.GetString("user_id")
	currency, exRate := getUserCurrencyAndRate(c, r.db, userID)

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, code, type, amount, reward_config, max_uses, used_count, status, expires_at, created_at
		 FROM vouchers WHERE user_id = $1::uuid ORDER BY created_at DESC`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.query_failed", c)}})
		return
	}
	defer rows.Close()

	codes := []gin.H{}
	for rows.Next() {
		var id, code, codeType, status string
		var amount int64
		var rewardConfigJSON string
		var maxUses, usedCount int
		var expiresAt *time.Time
		var createdAt time.Time
		_ = rows.Scan(&id, &code, &codeType, &amount, &rewardConfigJSON, &maxUses, &usedCount, &status, &expiresAt, &createdAt)

		var inviterReward, inviteeReward int64
		if rewardConfigJSON != "" {
			var cfg struct {
				Inviter struct {
					Amount int64 `json:"amount"`
				} `json:"inviter"`
				Invitee struct {
					Amount int64 `json:"amount"`
				} `json:"invitee"`
			}
			if err := json.Unmarshal([]byte(rewardConfigJSON), &cfg); err == nil {
				inviterReward = cfg.Inviter.Amount
				inviteeReward = cfg.Invitee.Amount
			}
		}
		if inviteeReward == 0 {
			inviteeReward = amount
		}

		codes = append(codes, gin.H{
			"id": id, "code": code, "type": codeType, "amount": amount,
			"reward_amount":          FormatQuota(amount, currency, exRate),
			"inviter_reward":         inviterReward,
			"invitee_reward":         inviteeReward,
			"inviter_reward_display": FormatQuota(inviterReward, currency, exRate),
			"invitee_reward_display": FormatQuota(inviteeReward, currency, exRate),
			"max_uses":               maxUses, "used_count": usedCount,
			"status": status, "expires_at": expiresAt, "created_at": createdAt,
		})
	}

	var affCode string
	var affQuota, affHistory int64
	_ = r.db.QueryRow(c.Request.Context(),
		"SELECT aff_code, aff_quota, aff_history_quota FROM users WHERE id = $1::uuid", userID).Scan(&affCode, &affQuota, &affHistory)

	var maxCodesStr string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(value, $1) FROM system_settings WHERE key = 'invitation_max_codes_per_user'`,
		strconv.Itoa(defaultInvitationMaxCodesPerUser)).Scan(&maxCodesStr)
	maxCodes := 0
	_, _ = fmt.Sscanf(maxCodesStr, "%d", &maxCodes)

	c.JSON(http.StatusOK, gin.H{
		"codes": codes, "aff_code": affCode,
		"aff_quota":         FormatQuota(affQuota, currency, exRate),
		"aff_history_quota": FormatQuota(affHistory, currency, exRate),
		"aff_quota_raw":     affQuota, "aff_history_quota_raw": affHistory,
		"max_codes": maxCodes,
	})
}

func (r *VoucherRoutes) myRecords(c *gin.Context) {
	userID := c.GetString("user_id")
	page, limit := parseVoucherPagination(c, voucherPaginationDefaultLimit)
	consumerType := c.Query("consumer_type")

	where := " WHERE vu.user_id = $1::uuid"
	args := []interface{}{userID}
	argIdx := 2

	if consumerType != "" && consumerType != "all" {
		where += fmt.Sprintf(" AND vu.consumer_type = $%d", argIdx)
		args = append(args, consumerType)
		argIdx++
	}

	var total int
	countSQL := "SELECT COUNT(*) FROM voucher_uses vu" + where
	_ = r.db.QueryRow(c.Request.Context(), countSQL, args...).Scan(&total)

	offset := (page - 1) * limit
	dataSQL := `SELECT vu.id, v.code, v.type, vu.amount, vu.consumer_type, vu.reward_granted, vu.balance_before, vu.balance_after, vu.created_at
		 FROM voucher_uses vu
		 JOIN vouchers v ON vu.voucher_id = v.id` + where +
		` ORDER BY vu.created_at DESC LIMIT $` + fmt.Sprintf("%d", argIdx) + ` OFFSET $` + fmt.Sprintf("%d", argIdx+1)
	args = append(args, limit, offset)

	rows, err := r.db.Query(c.Request.Context(), dataSQL, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.query_failed", c)}})
		return
	}
	defer rows.Close()

	records := []gin.H{}
	for rows.Next() {
		var id, code, codeType, consumerType string
		var amount, balanceBefore, balanceAfter int64
		var rewardGranted bool
		var createdAt time.Time
		_ = rows.Scan(&id, &code, &codeType, &amount, &consumerType, &rewardGranted, &balanceBefore, &balanceAfter, &createdAt)
		records = append(records, gin.H{
			"id": id, "code": code, "type": codeType, "amount": amount,
			"consumer_type": consumerType, "reward_granted": rewardGranted,
			"balance_before": balanceBefore, "balance_after": balanceAfter,
			"created_at": createdAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"records": records, "total": total, "page": page,
		"total_pages": (total + limit - 1) / limit,
	})
}
