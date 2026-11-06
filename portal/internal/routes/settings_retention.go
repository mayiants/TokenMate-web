package routes

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// getRetentionDefaults returns the platform default retention policy applied to
// keys that belong to no enterprise.
// GET /api/admin/settings/retention-defaults
func (r *SettingsRoutes) getRetentionDefaults(c *gin.Context) {
	// Read straight from the DB (not the validate-path cache) so an admin
	// always sees what is stored.
	mode, days := loadPlatformRetentionDefaults(c.Request.Context(), r.db)
	c.JSON(http.StatusOK, gin.H{
		"mode": mode, "days": days,
		"min_days": retentionDaysMin, "max_days": retentionDaysMax,
	})
}

// updateRetentionDefaults changes the platform default retention policy.
// PUT /api/admin/settings/retention-defaults {mode, days}
//
// Cached validate responses are NOT flushed: for keys without an enterprise the
// defaults are applied at response time (see validateKey), and the Proxy's own
// cache picks the change up within its ~5 min TTL.
func (r *SettingsRoutes) updateRetentionDefaults(c *gin.Context) {
	var req struct {
		Mode string `json:"mode"`
		Days *int   `json:"days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	if !validRetentionMode(req.Mode) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "mode must be one of: off, metadata, standard"}})
		return
	}
	if req.Days != nil && !validRetentionDays(*req.Days) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("days must be between %d and %d", retentionDaysMin, retentionDaysMax)}})
		return
	}

	ctx := c.Request.Context()
	userID := c.GetString("user_id")
	oldMode, oldDays := loadPlatformRetentionDefaults(ctx, r.db)
	newDays := oldDays
	if req.Days != nil {
		newDays = *req.Days
	}

	for _, kv := range [][2]string{
		{settingRetentionDefaultMode, req.Mode},
		{settingRetentionDefaultDays, strconv.Itoa(newDays)},
	} {
		if _, err := r.db.Pool.Exec(ctx,
			`INSERT INTO system_settings (key, value, category, updated_by, updated_at, managed_by)
			 VALUES ($1, $2, 'compliance', $3::uuid, NOW(), 'admin')
			 ON CONFLICT (key) DO UPDATE SET value = $2, updated_by = $3::uuid, updated_at = NOW(), managed_by = 'admin'`,
			kv[0], kv[1], userID); err != nil {
			r.logger.Error("Failed to update retention default", zap.String("key", kv[0]), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update retention defaults"}})
			return
		}
	}
	resetPlatformRetentionCache()

	changed := oldMode != req.Mode || oldDays != newDays
	if changed && r.audit != nil {
		r.audit.LogStructured(ctx, userID, c.GetString("email"), "settings", "retention_defaults_updated", "info",
			fmt.Sprintf("Platform retention defaults changed from %s/%dd to %s/%dd", oldMode, oldDays, req.Mode, newDays),
			"system_settings", "retention_defaults",
			map[string]interface{}{"mode": oldMode, "days": oldDays},
			map[string]interface{}{"mode": req.Mode, "days": newDays})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "changed": changed, "mode": req.Mode, "days": newDays})
}
