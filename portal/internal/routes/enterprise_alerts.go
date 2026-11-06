package routes

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// defaultAlertThresholds defines the out-of-the-box warning/exceeded thresholds.
// Admins can disable (or re-enable) each row via /enterprise/alert-settings.
var defaultAlertThresholds = []struct {
	AlertType string
	Threshold int
}{
	{"quota_warning", 80},
	{"quota_warning", 90},
	{"quota_exceeded", 100},
	{"credit_warning", 80},
	{"credit_warning", 90},
	{"credit_exceeded", 100},
}

// listEnterpriseAlerts returns the alert history for the caller's enterprise.
func (r *EnterpriseRoutes) listEnterpriseAlerts(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	status := c.Query("status")  // open | acknowledged | all
	alertType := c.Query("type") // quota_warning | quota_exceeded | credit_warning | credit_exceeded

	where := "WHERE enterprise_id = $1::uuid"
	args := []interface{}{enterpriseID}
	argIdx := 1

	switch status {
	case "open":
		where += " AND acknowledged_at IS NULL"
	case "acknowledged":
		where += " AND acknowledged_at IS NOT NULL"
	}
	if alertType != "" && alertType != "all" {
		argIdx++
		where += fmt.Sprintf(" AND alert_type = $%d", argIdx)
		args = append(args, alertType)
	}

	limit := 100
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 500 {
		limit = l
	}
	where += fmt.Sprintf(" ORDER BY notified_at DESC LIMIT $%d", argIdx+1)
	args = append(args, limit)

	rows, err := r.db.Pool.Query(c.Request.Context(),
		fmt.Sprintf(`SELECT id::text, alert_type, threshold_percent, current_value, limit_value,
						 notified_at, acknowledged_at,
						 COALESCE((SELECT email FROM users WHERE id = acknowledged_by), '')
				  FROM enterprise_alerts %s`, where),
		args...)
	if err != nil {
		r.logger.Error("Failed to list enterprise alerts", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load alerts"}})
		return
	}
	defer rows.Close()

	type alertItem struct {
		ID               string     `json:"id"`
		AlertType        string     `json:"alert_type"`
		ThresholdPercent int        `json:"threshold_percent"`
		CurrentValue     float64    `json:"current_value"`
		LimitValue       float64    `json:"limit_value"`
		NotifiedAt       time.Time  `json:"notified_at"`
		AcknowledgedAt   *time.Time `json:"acknowledged_at,omitempty"`
		AcknowledgedBy   string     `json:"acknowledged_by,omitempty"`
	}
	var alerts []alertItem
	for rows.Next() {
		var a alertItem
		var ackAt *time.Time
		if err := rows.Scan(&a.ID, &a.AlertType, &a.ThresholdPercent, &a.CurrentValue, &a.LimitValue,
			&a.NotifiedAt, &ackAt, &a.AcknowledgedBy); err != nil {
			r.logger.Warn("Failed to scan alert", zap.Error(err))
			continue
		}
		if ackAt != nil {
			a.AcknowledgedAt = ackAt
		}
		alerts = append(alerts, a)
	}

	c.JSON(http.StatusOK, gin.H{"alerts": alerts})
}

// getEnterpriseAlertSettings returns the configured alert thresholds for the
// caller's enterprise. Missing rows are considered enabled by default.
func (r *EnterpriseRoutes) getEnterpriseAlertSettings(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")

	rows, err := r.db.Pool.Query(c.Request.Context(),
		`SELECT alert_type, threshold_percent, COALESCE(enabled, true)
		 FROM enterprise_alert_settings
		 WHERE enterprise_id = $1::uuid`,
		enterpriseID)
	if err != nil {
		r.logger.Error("Failed to list alert settings", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load settings"}})
		return
	}
	defer rows.Close()

	configured := make(map[string]bool)
	for rows.Next() {
		var alertType string
		var threshold int
		var enabled bool
		if err := rows.Scan(&alertType, &threshold, &enabled); err != nil {
			continue
		}
		configured[fmt.Sprintf("%s:%d", alertType, threshold)] = enabled
	}

	type settingItem struct {
		AlertType    string `json:"alert_type"`
		Threshold    int    `json:"threshold_percent"`
		Enabled      bool   `json:"enabled"`
		Configurable bool   `json:"configurable"`
	}
	var settings []settingItem
	for _, d := range defaultAlertThresholds {
		key := fmt.Sprintf("%s:%d", d.AlertType, d.Threshold)
		enabled, ok := configured[key]
		if !ok {
			enabled = true
		}
		settings = append(settings, settingItem{
			AlertType:    d.AlertType,
			Threshold:    d.Threshold,
			Enabled:      enabled,
			Configurable: true,
		})
	}

	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

// updateEnterpriseAlertSetting enables or disables a single alert threshold.
func (r *EnterpriseRoutes) updateEnterpriseAlertSetting(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.GetString("user_id")

	var req struct {
		AlertType string `json:"alert_type" binding:"required"`
		Threshold int    `json:"threshold_percent" binding:"required"`
		Enabled   bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	valid := false
	for _, d := range defaultAlertThresholds {
		if d.AlertType == req.AlertType && d.Threshold == req.Threshold {
			valid = true
			break
		}
	}
	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Unsupported alert threshold"}})
		return
	}

	if _, err := r.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO enterprise_alert_settings (enterprise_id, alert_type, threshold_percent, enabled, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, NOW())
		 ON CONFLICT (enterprise_id, alert_type, threshold_percent)
		 DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = NOW()`,
		enterpriseID, req.AlertType, req.Threshold, req.Enabled); err != nil {
		r.logger.Error("Failed to update alert setting", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to save setting"}})
		return
	}

	oldStatus := "enabled"
	newStatus := "disabled"
	if req.Enabled {
		oldStatus = "disabled"
		newStatus = "enabled"
	}
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "settings",
		ChangeType:   "alert_setting_updated",
		FieldName:    fmt.Sprintf("%s_%d", req.AlertType, req.Threshold),
		ChangedBy:    userID,
		OldValueStr:  &oldStatus,
		NewValueStr:  &newStatus,
		TargetType:   "enterprise",
		TargetID:     enterpriseID,
		Reason:       fmt.Sprintf("Alert %s at %d%% %s", req.AlertType, req.Threshold, newStatus),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// acknowledgeAlert marks an enterprise alert as acknowledged by the calling
// enterprise admin. Only alerts belonging to the caller's own enterprise can
// be acknowledged, and only once.
func (r *EnterpriseRoutes) acknowledgeAlert(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	alertID := c.Param("id")
	userID := c.GetString("user_id")

	tag, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE enterprise_alerts
		 SET acknowledged_at = NOW(), acknowledged_by = $3::uuid
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid AND acknowledged_at IS NULL`,
		alertID, enterpriseID, userID)
	if err != nil {
		r.logger.Error("Failed to acknowledge enterprise alert", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to acknowledge alert"}})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Alert not found or already acknowledged"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "enterprise", "alert_acknowledged", "info",
			fmt.Sprintf("Acknowledged alert %s in enterprise %s", alertID, enterpriseID))
	}

	oldStatus := "open"
	newStatus := "acknowledged"
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "alert",
		ChangeType:   "alert_acknowledged",
		FieldName:    "status",
		ChangedBy:    userID,
		OldValueStr:  &oldStatus,
		NewValueStr:  &newStatus,
		Reason:       fmt.Sprintf("Alert %s acknowledged", alertID),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}
