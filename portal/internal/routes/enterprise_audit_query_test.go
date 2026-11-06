package routes

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// TestEnterpriseAuditQueryNullUserID is a regression test for the bug where
// audit rows with NULL user_id (e.g. settings events) aborted pgx row
// iteration on scan error, making the unfiltered "All" audit view empty
// whenever such a row was among the newest 100.
func TestEnterpriseAuditQueryNullUserID(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		t.Fatalf("schema: %v", err)
	}

	entID := "4c922b4a-b8cd-4efc-b88a-8cb407bb81ed"
	_, _ = pool.Exec(ctx, "DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid", entID)
	_, _ = pool.Exec(ctx, "DELETE FROM enterprises WHERE id = $1::uuid", entID)
	if _, err := pool.Exec(ctx, `INSERT INTO enterprises (id, name, status, created_at, updated_at) VALUES ($1::uuid, 'Repro Ent', 'active', NOW(), NOW())`, entID); err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM enterprises WHERE id = $1::uuid", entID)
		_, _ = pool.Exec(ctx, "DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid", entID)
	}()

	// 9 billing rows with user_id, 4 settings rows without
	for i := 0; i < 9; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, old_value, new_value, delta, reason, changed_by, user_id, created_at)
			VALUES ($1::uuid, 'billing', 'member_quota_limit_set', 'quota_limit', 1, 2, 1, 'r', NULL, NULL, NOW())`, entID); err != nil {
			t.Fatalf("insert billing: %v", err)
		}
	}
	for i := 0; i < 4; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, old_value, new_value, delta, reason, changed_by, user_id, created_at)
			VALUES ($1::uuid, 'settings', 'alert_setting_updated', 'quota_warning_80', NULL, NULL, NULL, 'r', NULL, NULL, NOW())`, entID); err != nil {
			t.Fatalf("insert settings: %v", err)
		}
	}

	query := `
		SELECT cl.id::text, cl.category, cl.change_type, cl.field_name, cl.old_value, cl.new_value, cl.delta,
		       COALESCE(cl.reason, ''), cl.created_at, COALESCE(operator.email, 'system'),
		       COALESCE(member.email, ''), COALESCE(member.name, ''), COALESCE(cl.user_id::text, '')
		FROM enterprise_audit_log cl
		LEFT JOIN users operator ON cl.changed_by = operator.id
		LEFT JOIN users member ON cl.user_id = member.id
		WHERE cl.enterprise_id = $1::uuid ORDER BY cl.created_at DESC LIMIT 100`

	var auditLogs []gin.H
	auditRows, err := pool.Query(ctx, query, entID)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer auditRows.Close()
	skipped := 0
	for auditRows.Next() {
		var id, category, changeType, fieldName, reason, changedBy string
		var memberEmail, memberName, memberUserID string
		var oldVal, newVal, delta sql.NullInt64
		var createdAt interface{}
		if err := auditRows.Scan(&id, &category, &changeType, &fieldName, &oldVal, &newVal, &delta, &reason, &createdAt, &changedBy, &memberEmail, &memberName, &memberUserID); err == nil {
			auditLogs = append(auditLogs, gin.H{"id": id})
		} else {
			skipped++
			fmt.Println("scan error:", err)
		}
	}
	var dbCount int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM enterprise_audit_log WHERE enterprise_id = $1::uuid", entID).Scan(&dbCount)
	t.Logf("db rows=%d appended=%d skipped=%d rowsErr=%v", dbCount, len(auditLogs), skipped, auditRows.Err())
	if len(auditLogs) != 13 {
		t.Fatalf("expected all 13 rows (incl. NULL user_id settings rows), got %d (skipped %d)", len(auditLogs), skipped)
	}
}
