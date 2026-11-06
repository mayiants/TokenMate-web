package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRunRetentionCleanup_DeletesExpiredVerificationCodes(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	userID := uuid.NewString()

	// Create a test user.
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO users (id, name, password_hash) VALUES ($1::uuid, $2, $3)`,
		userID, "retention-test-user", "hashed",
	)
	require.NoError(t, err)
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	}()

	// Insert an expired verification code (older than the 1-day retention window).
	_, err = db.Pool.Exec(ctx,
		`INSERT INTO verification_codes (user_id, code, type, value, expires_at, created_at)
		 VALUES ($1::uuid, '123456', 'email', 'test@example.com', NOW() - INTERVAL '2 days', NOW() - INTERVAL '2 days')`,
		userID,
	)
	require.NoError(t, err)

	// Insert a code that expires in the future (different type to avoid unique conflict).
	_, err = db.Pool.Exec(ctx,
		`INSERT INTO verification_codes (user_id, code, type, value, expires_at, created_at)
		 VALUES ($1::uuid, '654321', 'phone', 'test2@example.com', NOW() + INTERVAL '1 hour', NOW())`,
		userID,
	)
	require.NoError(t, err)

	// Ensure env override does not interfere.
	t.Setenv("PORTAL_RETENTION_DAYS", "")

	require.NoError(t, db.RunRetentionCleanup(ctx, zap.NewNop()))

	var count int
	err = db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM verification_codes WHERE user_id = $1::uuid`, userID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "expired verification code should be deleted")
}

// TestRunRetentionCleanup_ProxyRequestAudits verifies the proxy_request_audits
// rule exists, deletes rows older than the default (365d) window, keeps recent
// rows, and honours the proxy_audit_retention_days system_settings override.
func TestRunRetentionCleanup_ProxyRequestAudits(t *testing.T) {
	var rule *retentionRule
	for i := range defaultRetentionRules {
		if defaultRetentionRules[i].name == "proxy_request_audits" {
			rule = &defaultRetentionRules[i]
		}
	}
	require.NotNil(t, rule, "proxy_request_audits retention rule must be registered")
	assert.Equal(t, 365, rule.days)
	assert.Equal(t, "proxy_audit_retention_days", rule.settingKey)

	db := testDB(t)
	defer db.Close()
	ctx := context.Background()
	t.Setenv("PORTAL_RETENTION_DAYS", "")

	marker := "ret-test-" + uuid.NewString()
	insert := func(ageDays int) {
		_, err := db.Pool.Exec(ctx,
			`INSERT INTO proxy_request_audits (trace_id, model, server_id, created_at)
			 VALUES ($1::uuid, 'm', $2, NOW() - make_interval(days => $3))`,
			uuid.NewString(), marker, ageDays)
		require.NoError(t, err)
	}
	count := func() int {
		var n int
		require.NoError(t, db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM proxy_request_audits WHERE server_id = $1`, marker).Scan(&n))
		return n
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM proxy_request_audits WHERE server_id = $1`, marker)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM system_settings WHERE key = 'proxy_audit_retention_days'`)
	}()

	// Default window: 400d-old row goes, 100d-old row stays.
	insert(400)
	insert(100)
	require.NoError(t, db.RunRetentionCleanup(ctx, zap.NewNop()))
	assert.Equal(t, 1, count(), "only the row older than 365 days should be purged")

	// Setting override: shrink the window to 30 days.
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO system_settings (key, value) VALUES ('proxy_audit_retention_days', '30')
		 ON CONFLICT (key) DO UPDATE SET value = '30'`)
	require.NoError(t, err)
	require.NoError(t, db.RunRetentionCleanup(ctx, zap.NewNop()))
	assert.Equal(t, 0, count(), "the 100-day-old row must be purged once the override is 30 days")
}
