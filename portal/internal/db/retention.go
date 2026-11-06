package db

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// retentionRule describes a table to clean up, the default age after which
// rows can be deleted, and the SQL template with a single %d placeholder.
type retentionRule struct {
	name       string
	days       int
	sql        string
	settingKey string // system_settings key that overrides days; empty means no setting override
}

// defaultRetentionRules are applied once at startup and then every 24 hours.
// Windows are conservative defaults; override globally with PORTAL_RETENTION_DAYS.
var defaultRetentionRules = []retentionRule{
	// audit_logs is a tamper-evident hash chain (migration 0034): only a
	// chain_seq PREFIX may be purged so the surviving rows still link up, and the
	// newest row always survives so anchors keep a head to point at. The DELETE
	// itself runs under SET LOCAL app.audit_purge (see execRetentionSQL).
	{name: "audit_logs", days: 2555, sql: "DELETE FROM audit_logs WHERE created_at < NOW() - INTERVAL '%[1]d days' AND (chain_seq IS NULL OR chain_seq < COALESCE((SELECT MIN(chain_seq) FROM audit_logs WHERE created_at >= NOW() - INTERVAL '%[1]d days'), (SELECT MAX(chain_seq) FROM audit_logs)))", settingKey: "audit_log_retention_days"},
	// Proxy request audits are written for every proxied request (including
	// failures), so unlike audit_logs they grow with traffic and need a bound.
	{name: "proxy_request_audits", days: 365, sql: "DELETE FROM proxy_request_audits WHERE created_at < NOW() - INTERVAL '%d days'", settingKey: "proxy_audit_retention_days"},
	{name: "verification_codes", days: 1, sql: "DELETE FROM verification_codes WHERE expires_at < NOW() - INTERVAL '%d days'"},
	{name: "email_notifications", days: 90, sql: "DELETE FROM email_notifications WHERE created_at < NOW() - INTERVAL '%d days'"},
	{name: "channel_health_history", days: 30, sql: "DELETE FROM channel_health_history WHERE tested_at < NOW() - INTERVAL '%d days'"},
	{name: "provider_health_stats", days: 90, sql: "DELETE FROM provider_health_stats WHERE bucket < NOW() - INTERVAL '%d days'"},
	{name: "provider_health_stats_model", days: 90, sql: "DELETE FROM provider_health_stats_model WHERE bucket < NOW() - INTERVAL '%d days'"},
	{name: "model_group_health_stats", days: 90, sql: "DELETE FROM model_group_health_stats WHERE bucket < NOW() - INTERVAL '%d days'"},
	{name: "upstream_key_usage", days: 365, sql: "DELETE FROM upstream_key_usage WHERE date < NOW()::DATE - INTERVAL '%d days'"},
	{name: "usage_summary_hourly", days: 180, sql: "DELETE FROM usage_summary_hourly WHERE hour < NOW() - INTERVAL '%d days'"},
	{name: "usage_summary_daily", days: 730, sql: "DELETE FROM usage_summary_daily WHERE day < NOW()::DATE - INTERVAL '%d days'"},
	{name: "user_devices", days: 180, sql: "DELETE FROM user_devices WHERE is_current = false AND (last_active_at IS NULL OR last_active_at < NOW() - INTERVAL '%d days')"},
	{name: "sensitive_check_logs", days: 90, sql: "DELETE FROM sensitive_check_logs WHERE checked_at < NOW() - INTERVAL '%d days'", settingKey: "log_retention_days"},
	{name: "enterprise_invitations", days: 30, sql: `DELETE FROM enterprise_invitations WHERE (status = 'accepted' AND accepted_at < NOW() - INTERVAL '%[1]d days') OR (status = 'expired' AND expires_at < NOW() - INTERVAL '%[1]d days')`},
}

// RunRetentionCleanup runs retention cleanup rules. Each rule has a default
// retention window. Set PORTAL_RETENTION_DAYS to override all rules uniformly.
func (db *DB) RunRetentionCleanup(ctx context.Context, logger *zap.Logger) error {
	globalDays := 0
	if v := os.Getenv("PORTAL_RETENTION_DAYS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			globalDays = parsed
		}
	}

	var hadError bool
	for _, rule := range defaultRetentionRules {
		days := rule.days
		if rule.settingKey != "" {
			if v, ok := db.getSystemSettingInt(ctx, rule.settingKey); ok && v > 0 {
				days = v
			}
		}
		if globalDays > 0 {
			days = globalDays
		}
		sql := fmt.Sprintf(rule.sql, days)
		sql = strings.Join(strings.Fields(sql), " ")
		result, err := db.execRetentionSQL(ctx, rule.name, sql)
		if err != nil {
			logger.Error("Retention cleanup failed",
				zap.String("table", rule.name),
				zap.Error(err))
			hadError = true
			continue
		}
		if rows := result.RowsAffected(); rows > 0 {
			logger.Info("Retention cleanup completed",
				zap.String("table", rule.name),
				zap.Int64("rows_deleted", rows))
		}
	}

	db.runGDPRErasure(ctx, logger)

	if hadError {
		return fmt.Errorf("one or more retention cleanup rules failed")
	}
	return nil
}

// getSystemSettingInt reads an integer value from the system_settings table.
// Returns (value, true) if found and parseable, (0, false) otherwise.
func (db *DB) getSystemSettingInt(ctx context.Context, key string) (int, bool) {
	var val string
	if err := db.Pool.QueryRow(ctx,
		`SELECT value FROM system_settings WHERE key = $1`, key,
	).Scan(&val); err != nil {
		return 0, false
	}
	parsed, err := strconv.Atoi(val)
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return parsed, true
}

// runGDPRErasure anonymizes users whose deletion request is older than the
// configured retention window (system_settings.gdpr_retention_days, default
// 90). During the window the account is deactivated but PII is kept so admins
// can export it for upstream compliance audits.
func (db *DB) runGDPRErasure(ctx context.Context, logger *zap.Logger) {
	days := 90
	var val string
	if err := db.Pool.QueryRow(ctx,
		`SELECT value FROM system_settings WHERE key = 'gdpr_retention_days'`).Scan(&val); err == nil {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			days = parsed
		}
	}

	rows, err := db.Pool.Query(ctx,
		`SELECT id::text FROM users
		 WHERE gdpr_erasure_requested_at IS NOT NULL
		   AND gdpr_erasure_requested_at < NOW() - make_interval(days => $1)
		   AND email NOT LIKE 'deleted_%@deleted.local'`, days)
	if err != nil {
		logger.Error("GDPR erasure query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	var userIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			userIDs = append(userIDs, id)
		}
	}

	for _, id := range userIDs {
		if err := db.AnonymizeUser(ctx, id); err != nil {
			logger.Error("GDPR erasure failed", zap.String("user_id", id), zap.Error(err))
			continue
		}
		logger.Info("GDPR deferred erasure completed", zap.String("user_id", id))
	}
}

// StartRetentionJobs runs retention cleanup immediately and then every 24 hours.
func (db *DB) StartRetentionJobs(ctx context.Context, logger *zap.Logger) {
	if err := db.RunRetentionCleanup(ctx, logger); err != nil {
		logger.Error("Initial retention cleanup failed", zap.Error(err))
	}

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := db.RunRetentionCleanup(context.Background(), logger); err != nil {
					logger.Error("Retention cleanup failed", zap.Error(err))
				}
			}
		}
	}()
}
