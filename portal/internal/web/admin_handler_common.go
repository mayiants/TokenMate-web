package web

import (
	"crypto/rand"
	"math/big"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mayiants/ai-portal-go/internal/routes"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
)

// --- Admin Action Handlers ---

// HandleGetPlanDetail returns plan details for editing
func (h *Handler) HandleGetPlanDetail(c *gin.Context) {
	planID := c.Param("id")
	var plan struct {
		ID              string
		Name            string
		PlanType        string
		Price           int64
		Quota           int64
		DurationDays    int
		DailyLimit      int
		RphLimit        int
		Rp5hLimit       int
		RpdLimit        int
		RpwLimit        int
		TphLimit        int
		TpdLimit        int
		TpwLimit        int
		ConcurrentLimit int
		RpmLimit        int
		TpmLimit        int
		Status          string
		ModelPerms      []byte
	}

	err := h.db.QueryRow(c.Request.Context(),
		`SELECT id, name, plan_type, price, quota, duration_days, COALESCE(daily_limit,0),
			COALESCE(rph_limit,0), COALESCE(rp5h_limit,0), COALESCE(rpd_limit,0), COALESCE(rpw_limit,0),
			COALESCE(tph_limit,0), COALESCE(tpd_limit,0), COALESCE(tpw_limit,0),
			COALESCE(concurrent_limit,0), COALESCE(rpm_limit,0), COALESCE(tpm_limit,0),
			status, COALESCE(model_permissions, 'null'::jsonb)::text
		 FROM subscription_plans WHERE id = $1::uuid AND deleted_at IS NULL`, planID,
	).Scan(&plan.ID, &plan.Name, &plan.PlanType, &plan.Price, &plan.Quota, &plan.DurationDays, &plan.DailyLimit,
		&plan.RphLimit, &plan.Rp5hLimit, &plan.RpdLimit, &plan.RpwLimit,
		&plan.TphLimit, &plan.TpdLimit, &plan.TpwLimit,
		&plan.ConcurrentLimit, &plan.RpmLimit, &plan.TpmLimit,
		&plan.Status, &plan.ModelPerms)
	if err != nil {
		h.Partial(c, "plan-detail-partial", gin.H{"Error": h.i18n.T("error.plan_not_found", h.getLocale(c))})
		return
	}

	currency := h.getCurrency(c)
	exchangeRate := h.getExchangeRate(c)
	displayPrice := quotaToDisplay(plan.Price, currency, exchangeRate)

	h.RenderPartial(c, "plan-detail-partial", gin.H{
		"ID":              plan.ID,
		"Name":            plan.Name,
		"PlanType":        plan.PlanType,
		"Price":           displayPrice,
		"Quota":           plan.Quota,
		"DurationDays":    plan.DurationDays,
		"DailyLimit":      plan.DailyLimit,
		"RphLimit":        plan.RphLimit,
		"Rp5hLimit":       plan.Rp5hLimit,
		"RpdLimit":        plan.RpdLimit,
		"RpwLimit":        plan.RpwLimit,
		"TphLimit":        plan.TphLimit,
		"TpdLimit":        plan.TpdLimit,
		"TpwLimit":        plan.TpwLimit,
		"ConcurrentLimit": plan.ConcurrentLimit,
		"RpmLimit":        plan.RpmLimit,
		"TpmLimit":        plan.TpmLimit,
		"Status":          plan.Status,
		"ModelPerms":      string(plan.ModelPerms),
		"Currency":        currency,
		"ExchangeRate":    exchangeRate,
	})
}

// Helper functions
func generateRandomCode(length int) string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	result := make([]byte, length)
	max := big.NewInt(int64(len(chars)))
	for i := range result {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			result[i] = chars[0]
		} else {
			result[i] = chars[n.Int64()]
		}
	}
	return string(result)
}

func parseInt(s string) int {
	var result int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			result = result*10 + int(c-'0')
		}
	}
	return result
}

// quotaToDisplay converts micro-USD quota to USD float for admin edit forms.
func quotaToDisplay(quota int64, currency string, exchangeRate float64) float64 {
	// Admin pages always display in USD; ignore currency parameter
	return routes.QuotaToUSDFloat(quota)
}

// HandleAuditLogDetail returns audit log detail HTML for modal
func (h *Handler) HandleAuditLogDetail(c *gin.Context) {
	logID := c.Param("id")

	var id int64
	var email, category, action, level, message string
	var detailsJSON []byte
	var createdAt time.Time

	err := h.db.QueryRow(c.Request.Context(),
		`SELECT id, COALESCE(user_email, ''), category, action, level, message, details, created_at
		 FROM audit_logs WHERE id = $1`, logID,
	).Scan(&id, &email, &category, &action, &level, &message, &detailsJSON, &createdAt)

	if err != nil {
		h.Partial(c, "audit-log-detail-partial", gin.H{
			"Error": h.i18n.T("error.log_not_found", h.getLocale(c)),
		})
		return
	}

	var detailsStr string
	if detailsJSON != nil {
		detailsStr = string(detailsJSON)
	}

	h.Partial(c, "audit-log-detail-partial", gin.H{
		"ID":        id,
		"Email":     email,
		"Category":  category,
		"Action":    action,
		"Level":     level,
		"Message":   message,
		"Details":   detailsStr,
		"CreatedAt": createdAt,
	})
}

// HandleGetSettingJSON returns setting as JSON
func (h *Handler) HandleGetSettingJSON(c *gin.Context) {
	key := c.Param("key")

	// Default values for invitation settings
	defaults := map[string]string{
		"invitation_max_codes_per_user":  "10",
		"invitation_default_max_uses":    "10",
		"invitation_default_expire_days": "365",
		"invitation_inviter_reward":      "5",
		"invitation_invitee_reward":      "2",
	}

	var value, category, description string
	err := h.db.QueryRow(c.Request.Context(),
		`SELECT value, COALESCE(category, 'general'), COALESCE(description, '')
		 FROM system_settings WHERE key = $1`, key,
	).Scan(&value, &category, &description)
	if err != nil {
		// Return default value if setting doesn't exist in DB yet
		if def, ok := defaults[key]; ok {
			c.JSON(http.StatusOK, gin.H{
				"key":         key,
				"value":       def,
				"category":    "general",
				"description": "",
			})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": h.i18n.T("error.setting_not_found", h.getLocale(c))})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"key":         key,
		"value":       value,
		"category":    category,
		"description": description,
	})
}

// HandleEnterpriseDetail returns enterprise detail HTML for modal
func (h *Handler) HandleEnterpriseDetail(c *gin.Context) {
	enterpriseID := c.Param("id")

	var name, status, contactEmail string
	var quotaTotal, quotaUsed int64
	var memberCount int
	err := h.db.QueryRow(c.Request.Context(),
		`SELECT e.name, e.status, COALESCE(e.contact_email, ''),
		        COALESCE(e.quota_total, 0), COALESCE(e.quota_used, 0),
		        (SELECT COUNT(*) FROM users WHERE enterprise_id = e.id AND deleted_at IS NULL)
		 FROM enterprises e WHERE e.id = $1::uuid`, enterpriseID,
	).Scan(&name, &status, &contactEmail, &quotaTotal, &quotaUsed, &memberCount)
	if err != nil {
		c.HTML(http.StatusOK, "enterprise-detail-partial", gin.H{
			"ID":    enterpriseID,
			"Error": h.i18n.T("error.enterprise_not_found", h.getLocale(c)),
		})
		return
	}

	c.HTML(http.StatusOK, "enterprise-detail-partial", gin.H{
		"ID":           enterpriseID,
		"Name":         name,
		"Status":       status,
		"ContactEmail": contactEmail,
		"QuotaTotal":   quotaTotal,
		"QuotaUsed":    quotaUsed,
		"MemberCount":  memberCount,
	})
}

// --- Enterprise Admin Action Handlers ---

// HandleViewEnterpriseKey returns the full enterprise key (encrypted storage, decrypted for admin)
func (h *Handler) HandleViewEnterpriseKey(c *gin.Context) {
	keyID := c.Param("id")
	enterpriseID := c.GetString("enterprise_id")

	var keyEncrypted string
	err := h.db.QueryRow(c.Request.Context(),
		`SELECT key_encrypted FROM api_keys WHERE id = $1::uuid AND enterprise_id = $2::uuid AND key_type = 'enterprise' AND deleted_at IS NULL`,
		keyID, enterpriseID,
	).Scan(&keyEncrypted)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}

	// Decrypt key
	decrypted, decErr := crypto.Decrypt(keyEncrypted, h.cfg.Auth.EncryptionKey)
	if decErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to decrypt key"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": decrypted})
}

// HandleViewEnterpriseMemberKey returns the full enterprise key for any enterprise member
// (unlike HandleViewEnterpriseKey which requires enterprise_admin role).
// The key must be assigned to the requesting user and belong to their enterprise.
func (h *Handler) HandleViewEnterpriseMemberKey(c *gin.Context) {
	keyID := c.Param("id")
	userID := c.GetString("user_id")

	// Look up the key, verifying it belongs to the user's enterprise and is assigned to them
	var keyEncrypted string
	err := h.db.QueryRow(c.Request.Context(),
		`SELECT k.key_encrypted FROM api_keys k
		 JOIN users u ON u.enterprise_id = k.enterprise_id
		 WHERE k.id = $1::uuid AND u.id = $2::uuid
		   AND k.assigned_user_id = $2::uuid AND k.key_type = 'enterprise'
		   AND k.deleted_at IS NULL AND u.deleted_at IS NULL`,
		keyID, userID,
	).Scan(&keyEncrypted)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found or not assigned to you"}})
		return
	}

	decrypted, decErr := crypto.Decrypt(keyEncrypted, h.cfg.Auth.EncryptionKey)
	if decErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to decrypt key"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": decrypted})
}

// defaultIfEmpty returns fallback if s is empty, otherwise returns s.
func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
