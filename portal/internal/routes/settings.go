package routes

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type SettingsRoutes struct {
	db            *db.DB
	auth          *middleware.Auth
	mail          *mail.Sender
	envSMTP       config.SMTPConfig
	encryptionKey string
	logger        *zap.Logger
	audit         *audit.Logger // optional; set via WithAudit
}

func NewSettingsRoutes(db *db.DB, auth *middleware.Auth, mailSender *mail.Sender, envSMTP config.SMTPConfig, encryptionKey string, logger *zap.Logger) *SettingsRoutes {
	return &SettingsRoutes{
		db:            db,
		auth:          auth,
		mail:          mailSender,
		envSMTP:       envSMTP,
		encryptionKey: encryptionKey,
		logger:        logger,
	}
}

// WithAudit attaches the audit logger used for super-admin-only settings that
// must leave a trail (retention defaults). Optional so existing callers and
// tests keep working unchanged.
func (r *SettingsRoutes) WithAudit(l *audit.Logger) *SettingsRoutes {
	r.audit = l
	return r
}

func (r *SettingsRoutes) Register(rg *gin.RouterGroup) {
	rg.Use(r.auth.RequireAuth())
	rg.GET("", r.auth.RequireAdmin(), r.getSettings)
	rg.PUT("", r.auth.RequireAdmin(), r.updateSettings)
	rg.POST("/reset", r.auth.RequireAdmin(), r.resetSettings)
	// SMTP configuration endpoints (static prefix to avoid clashing with PUT "/:key")
	rg.GET("/smtp-config", r.auth.RequireAdmin(), r.getSMTPConfig)
	rg.PUT("/smtp-config", r.auth.RequireAdmin(), r.updateSMTPSettings)
	rg.DELETE("/smtp-config", r.auth.RequireAdmin(), r.deleteSMTPConfig)
	rg.POST("/smtp-config/test", r.auth.RequireAdmin(), r.testSMTPConfig)
	// Platform-wide default data-retention policy (super_admin only; see enterprise_retention.go)
	rg.GET("/retention-defaults", r.auth.RequireSuperAdmin(), r.getRetentionDefaults)
	rg.PUT("/retention-defaults", r.auth.RequireSuperAdmin(), r.updateRetentionDefaults)
	// Platform MFA policy: roles that must enrol (super_admin only; see mfa_policy_routes.go)
	rg.GET("/mfa-required-roles", r.auth.RequireSuperAdmin(), r.getMFARequiredRoles)
	rg.PUT("/mfa-required-roles", r.auth.RequireSuperAdmin(), r.updateMFARequiredRoles)
	// Individual setting endpoints (used by invitation config UI)
	rg.GET("/:key/json", r.auth.RequireAdmin(), r.getSettingJSON)
	rg.PUT("/:key", r.auth.RequireAdmin(), r.updateSetting)
}

var simpleEmailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

var settingGroups = map[string]struct {
	Name string
	Keys []string
}{
	"operation":     {Name: "Operations", Keys: []string{"quota_for_new_user", "topup_link", "usd_exchange_rate", "retry_times"}},
	"monitoring":    {Name: "Monitoring", Keys: []string{"channel_disable_threshold", "quota_remind_threshold", "automatic_disable_channel_enabled"}},
	"token":         {Name: "Token", Keys: []string{"max_user_tokens", "default_token_quota", "token_expire_days"}},
	"log":           {Name: "Logging", Keys: []string{"log_retention_days", "log_consume_enabled", "audit_log_retention_days"}},
	"payment":       {Name: "Payment", Keys: []string{"alipay_enabled", "wechat_enabled", "payment_notify_url", "payment_simulation_mode"}},
	"announcement":  {Name: "Announcement", Keys: []string{"announcement_enabled", "announcement_content"}},
	"compliance":    {Name: "Compliance", Keys: []string{"gdpr_retention_days"}},
	"rate_limit":    {Name: "Rate Limit", Keys: []string{"default_user_rpm", "default_user_tpm", "default_user_daily_requests"}},
	"notifications": {Name: "Notification Settings", Keys: []string{"balance_low_threshold_usd"}},
}

var defaultSettings = map[string]string{
	"quota_for_new_user":                "1000000",
	"topup_link":                        "",
	"usd_exchange_rate":                 "720",
	"retry_times":                       "3",
	"channel_disable_threshold":         "0.5",
	"quota_remind_threshold":            "100000",
	"automatic_disable_channel_enabled": "true",
	"max_user_tokens":                   "10",
	"default_token_quota":               "1000000",
	"token_expire_days":                 "30",
	"log_retention_days":                "30",
	"log_consume_enabled":               "true",
	"audit_log_retention_days":          "90",
	"alipay_enabled":                    "true",
	"wechat_enabled":                    "false",
	"payment_notify_url":                "",
	"payment_simulation_mode":           "false",
	"announcement_enabled":              "false",
	"announcement_content":              "",
	"gdpr_retention_days":               "90",
	"default_user_rpm":                  "120",
	"default_user_tpm":                  "500000",
	"default_user_daily_requests":       "10000",
	"balance_low_threshold_usd":         "1",
}

// deadSettings are keys that exist in defaultSettings for backwards compatibility
// but currently have no production consumer (P2 audit). updateSettings rejects
// writes to these keys until they are wired or removed.
var deadSettings = map[string]bool{
	"quota_for_new_user":                true,
	"topup_link":                        true,
	"retry_times":                       true,
	"channel_disable_threshold":         true,
	"quota_remind_threshold":            true,
	"automatic_disable_channel_enabled": true,
	"max_user_tokens":                   true,
	"default_token_quota":               true,
	"token_expire_days":                 true,
	"log_retention_days":                true,
	"log_consume_enabled":               true,
	"audit_log_retention_days":          true,
	"alipay_enabled":                    true,
	"wechat_enabled":                    true,
	"payment_notify_url":                true,
	"announcement_enabled":              true,
	"announcement_content":              true,
}

// allowedSettingKeys is the whitelist for the generic PUT /:key endpoint.
// Sensitive keys (smtp_*, encryption_key) are excluded — they must use
// their dedicated handlers to ensure proper encryption/validation.
var allowedSettingKeys = map[string]bool{
	// Non-dead defaultSettings keys
	"usd_exchange_rate":           true,
	"gdpr_retention_days":         true,
	"default_user_rpm":            true,
	"default_user_tpm":            true,
	"default_user_daily_requests": true,
	"payment_simulation_mode":     true, // toggled from super/payment UI
	// Invitation settings (managed via PUT /:key by the invitation config UI)
	"invitation_max_codes_per_user":  true,
	"invitation_default_max_uses":    true,
	"invitation_default_expire_days": true,
	"invitation_inviter_reward":      true,
	"invitation_invitee_reward":      true,
	// Notification thresholds (notification system P2)
	"balance_low_threshold_usd": true,
}

func (r *SettingsRoutes) getSettings(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT key, value, updated_at FROM system_settings ORDER BY key`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get settings"}})
		return
	}
	defer rows.Close()

	settingsMap := make(map[string]gin.H)
	for rows.Next() {
		var key, value string
		var updatedAt interface{}
		_ = rows.Scan(&key, &value, &updatedAt)
		settingsMap[key] = gin.H{"value": value, "updatedAt": updatedAt}
	}

	// Build grouped response
	settings := gin.H{}
	for groupKey, group := range settingGroups {
		groupSettings := gin.H{}
		for _, key := range group.Keys {
			if s, ok := settingsMap[key]; ok {
				groupSettings[key] = s
			} else {
				groupSettings[key] = gin.H{"value": defaultSettings[key], "updatedAt": nil}
			}
		}
		settings[groupKey] = gin.H{"name": T("settings."+groupKey, c), "settings": groupSettings}
	}

	c.JSON(http.StatusOK, gin.H{"settings": settings, "groups": settingGroups, "defaults": defaultSettings})
}

func (r *SettingsRoutes) updateSettings(c *gin.Context) {
	var req struct {
		Settings map[string]string `json:"settings"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	userID := c.GetString("user_id")
	updated := []string{}

	for key, value := range req.Settings {
		if _, ok := defaultSettings[key]; !ok {
			continue
		}
		if deadSettings[key] {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Setting %q is not wired to any feature; write rejected (P2 audit)", key)}})
			return
		}
		_, err := r.db.Pool.Exec(c.Request.Context(),
			`INSERT INTO system_settings (key, value, updated_by, updated_at, managed_by)
			 VALUES ($1, $2, $3::uuid, NOW(), 'admin')
			 ON CONFLICT (key) DO UPDATE SET value = $2, updated_by = $3::uuid, updated_at = NOW(), managed_by = 'admin'`,
			key, value, userID)
		if err == nil {
			updated = append(updated, key)
		}
	}

	if len(updated) > 0 {
		_ = ReloadSystemSettings(r.db)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "updated": updated})
}

func (r *SettingsRoutes) resetSettings(c *gin.Context) {
	var req struct {
		Keys []string `json:"keys"`
	}
	_ = c.ShouldBindJSON(&req)

	userID := c.GetString("user_id")
	keysToReset := req.Keys
	if len(keysToReset) == 0 {
		for k := range defaultSettings {
			keysToReset = append(keysToReset, k)
		}
	}

	reset := []string{}
	for _, key := range keysToReset {
		if defaultVal, ok := defaultSettings[key]; ok {
			// Resetting returns the row to the seed default and to seed management,
			// so a future seed run can refresh it again.
			_, err := r.db.Pool.Exec(c.Request.Context(),
				`UPDATE system_settings SET value = $1, updated_by = $2::uuid, updated_at = NOW(), managed_by = 'seed' WHERE key = $3`,
				defaultVal, userID, key)
			if err == nil {
				reset = append(reset, key)
			}
		}
	}

	if len(reset) > 0 {
		_ = ReloadSystemSettings(r.db)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "reset": reset})
}

func (r *SettingsRoutes) getSettingJSON(c *gin.Context) {
	key := c.Param("key")

	defaults := map[string]string{
		"invitation_max_codes_per_user":  "10",
		"invitation_default_max_uses":    "10",
		"invitation_default_expire_days": "365",
		// Invitation rewards are stored in micro-USD (1 USD = 1_000_000) as
		// int64, matching defaultInvitationInviterReward/defaultInvitationInviteeReward
		// and the readers/writers in vouchers_admin.go and vouchers_user.go.
		"invitation_inviter_reward": "5000000",
		"invitation_invitee_reward": "2000000",
	}

	var value, category, description string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT value, COALESCE(category, 'general'), COALESCE(description, '')
		 FROM system_settings WHERE key = $1`, key,
	).Scan(&value, &category, &description)
	if err != nil {
		if def, ok := defaults[key]; ok {
			c.JSON(http.StatusOK, gin.H{"key": key, "value": def, "category": "general", "description": ""})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": T("error.settings_not_found", c)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": key, "value": value, "category": category, "description": description})
}

func (r *SettingsRoutes) updateSetting(c *gin.Context) {
	key := c.Param("key")

	// Reject sensitive keys — they must use dedicated handlers (e.g. PUT /smtp-config)
	// to ensure proper encryption and validation.
	if strings.HasPrefix(key, "smtp_") || key == "encryption_key" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Sensitive settings must use their dedicated endpoint"}})
		return
	}
	if !allowedSettingKeys[key] {
		if deadSettings[key] {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Setting %q is not wired to any feature; write rejected", key)}})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Setting key %q is not allowed through this endpoint", key)}})
		}
		return
	}

	var req struct {
		Value       string `json:"value"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": T("error.invalid_request", c)})
		return
	}

	err := r.db.Exec(c.Request.Context(),
		`INSERT INTO system_settings (key, value, description, updated_at, managed_by) VALUES ($3, $1, $2, NOW(), 'admin')
		 ON CONFLICT (key) DO UPDATE SET value = $1, description = COALESCE(NULLIF($2, ''), system_settings.description), updated_at = NOW(), managed_by = 'admin'`,
		req.Value, req.Description, key,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": T("error.update_failed", c)})
		return
	}

	// Refresh cached rate-limit defaults if a related key changed.
	if key == "default_user_rpm" || key == "default_user_tpm" || key == "default_user_daily_requests" {
		_ = ReloadSystemSettings(r.db)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// readSMTPSettings reads the smtp_* rows from system_settings.
func (r *SettingsRoutes) readSMTPSettings(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT key, value FROM system_settings WHERE key IN ('smtp_host','smtp_port','smtp_secure','smtp_user','smtp_password','smtp_from')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	vals := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err == nil {
			vals[key] = value
		}
	}
	return vals, rows.Err()
}

func (r *SettingsRoutes) getSMTPConfig(c *gin.Context) {
	vals, err := r.readSMTPSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get SMTP settings"}})
		return
	}

	if host, ok := vals["smtp_host"]; ok {
		port, _ := strconv.Atoi(vals["smtp_port"])
		c.JSON(http.StatusOK, gin.H{
			"source":       "db",
			"has_password": vals["smtp_password"] != "",
			"config": gin.H{
				"host":   host,
				"port":   port,
				"secure": vals["smtp_secure"] == "true",
				"user":   vals["smtp_user"],
				"from":   vals["smtp_from"],
			},
		})
		return
	}

	env := r.mail.Config()
	c.JSON(http.StatusOK, gin.H{
		"source":       "env",
		"has_password": env.Password != "",
		"config": gin.H{
			"host":   env.Host,
			"port":   env.Port,
			"secure": env.Secure,
			"user":   env.User,
			"from":   env.From,
		},
	})
}

func (r *SettingsRoutes) updateSMTPSettings(c *gin.Context) {
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Secure   bool   `json:"secure"`
		User     string `json:"user"`
		Password string `json:"password"`
		From     string `json:"from"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	req.Host = strings.TrimSpace(req.Host)
	req.User = strings.TrimSpace(req.User)
	req.From = strings.TrimSpace(req.From)
	if req.Host == "" || req.User == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Host and user are required"}})
		return
	}
	if req.Port <= 0 {
		req.Port = 465
	}

	ctx := c.Request.Context()
	userID := c.GetString("user_id")

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to save SMTP settings"}})
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Keep the existing encrypted password when the form sends an empty one.
	var encPassword string
	if req.Password != "" {
		enc, err := crypto.Encrypt(req.Password, r.encryptionKey)
		if err != nil {
			r.logger.Error("Failed to encrypt SMTP password", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to encrypt password"}})
			return
		}
		encPassword = enc
	} else {
		_ = tx.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = 'smtp_password'`).Scan(&encPassword)
	}

	settings := map[string]string{
		"smtp_host":     req.Host,
		"smtp_port":     strconv.Itoa(req.Port),
		"smtp_secure":   strconv.FormatBool(req.Secure),
		"smtp_user":     req.User,
		"smtp_password": encPassword,
		"smtp_from":     req.From,
	}
	for key, value := range settings {
		_, err := tx.Exec(ctx,
			`INSERT INTO system_settings (key, value, updated_by, updated_at, managed_by)
			 VALUES ($1, $2, $3::uuid, NOW(), 'admin')
			 ON CONFLICT (key) DO UPDATE SET value = $2, updated_by = $3::uuid, updated_at = NOW(), managed_by = 'admin'`,
			key, value, userID)
		if err != nil {
			r.logger.Error("Failed to save SMTP setting", zap.String("key", key), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to save SMTP settings"}})
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to save SMTP settings"}})
		return
	}

	password := req.Password
	if password == "" && encPassword != "" {
		dec, err := crypto.Decrypt(encPassword, r.encryptionKey)
		if err != nil {
			r.logger.Error("Failed to decrypt stored SMTP password", zap.Error(err))
		} else {
			password = dec
		}
	}
	r.mail.Reload(config.SMTPConfig{
		Host:     req.Host,
		Port:     req.Port,
		Secure:   req.Secure,
		User:     req.User,
		Password: password,
		From:     req.From,
	})

	r.logger.Info("SMTP settings updated by admin",
		zap.String("host", req.Host),
		zap.Int("port", req.Port),
		zap.Bool("secure", req.Secure),
		zap.String("user", req.User),
		zap.String("updated_by", userID),
	)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *SettingsRoutes) deleteSMTPConfig(c *gin.Context) {
	err := r.db.Exec(c.Request.Context(),
		`DELETE FROM system_settings WHERE key IN ('smtp_host','smtp_port','smtp_secure','smtp_user','smtp_password','smtp_from')`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reset SMTP settings"}})
		return
	}

	r.mail.Reload(r.envSMTP)
	r.logger.Info("SMTP settings reset to environment configuration")

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *SettingsRoutes) testSMTPConfig(c *gin.Context) {
	var req struct {
		To string `json:"to"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	to := strings.TrimSpace(req.To)
	if to == "" || !simpleEmailRe.MatchString(to) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid email address"}})
		return
	}

	if err := r.mail.SendTestEmail(to, GetLocale(c)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// LoadSMTPFromDB loads SMTP settings stored in system_settings and reloads the
// mail sender. Called at startup so admin-configured SMTP overrides env vars.
func LoadSMTPFromDB(ctx context.Context, database *db.DB, sender *mail.Sender, encryptionKey string, logger *zap.Logger) {
	rows, err := database.Query(ctx,
		`SELECT key, value FROM system_settings WHERE key IN ('smtp_host','smtp_port','smtp_secure','smtp_user','smtp_password','smtp_from')`)
	if err != nil {
		logger.Warn("Failed to load SMTP settings from database", zap.Error(err))
		return
	}
	defer rows.Close()

	vals := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err == nil {
			vals[key] = value
		}
	}

	host, ok := vals["smtp_host"]
	if !ok || host == "" {
		return // no DB override, keep environment configuration
	}

	password := ""
	if enc := vals["smtp_password"]; enc != "" {
		dec, err := crypto.Decrypt(enc, encryptionKey)
		if err != nil {
			logger.Error("Failed to decrypt SMTP password from database, keeping environment configuration", zap.Error(err))
			return
		}
		password = dec
	}

	port, _ := strconv.Atoi(vals["smtp_port"])
	sender.Reload(config.SMTPConfig{
		Host:     host,
		Port:     port,
		Secure:   vals["smtp_secure"] == "true",
		User:     vals["smtp_user"],
		Password: password,
		From:     vals["smtp_from"],
	})
}
