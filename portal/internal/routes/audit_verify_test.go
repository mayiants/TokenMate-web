package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/testutil"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/jwt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const auditVerifyTestKey = "route-test-encryption-key-0123456789ab"

type auditVerifyEnv struct {
	d       *db.DB
	router  *gin.Engine
	mgr     *jwt.Manager
	adminID string
	logger  *audit.Logger
}

func newAuditVerifyEnv(t *testing.T) *auditVerifyEnv {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()))
	pool, err := pgxpool.New(ctx, testutil.PostgresDSNStrict())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	d := &db.DB{Pool: pool}

	reset := func() {
		require.NoError(t, d.WithAuditPurge(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `TRUNCATE audit_logs, enterprise_audit_log, audit_anchors`)
			return err
		}))
	}
	reset()
	t.Cleanup(reset)

	adminID := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, role) VALUES ($1::uuid, 'sa', $2, 'x', 'super_admin')`,
		adminID, "sa-"+adminID+"@example.test")
	require.NoError(t, err)
	t.Cleanup(func() {
		// deleted_by references users(id); clear it before removing the user.
		bypass, berr := pgxpool.New(ctx, testutil.PostgresDSN())
		if berr != nil {
			return
		}
		defer bypass.Close()
		_, _ = bypass.Exec(ctx, `UPDATE audit_logs SET deleted_by = NULL WHERE deleted_by = $1::uuid`, adminID)
		_, _ = bypass.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, adminID)
	})

	cfg := &config.Config{}
	mgr := jwt.NewManager("test-secret-key-at-least-32-chars!!", time.Hour)
	auth := middleware.NewAuthFromManager(mgr, cfg, zap.NewNop())
	logger := audit.NewLogger(d, zap.NewNop())
	t.Cleanup(func() { logger.Stop(ctx) })
	ar := NewAuditRoutes(d, auth, logger)
	ar.SetAnchorKey(auditVerifyTestKey)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	ar.Register(router.Group("/api/admin/audit-logs"))
	ar.RegisterVerify(router.Group("/api/admin/audit"))
	return &auditVerifyEnv{d: d, router: router, mgr: mgr, adminID: adminID, logger: logger}
}

func (e *auditVerifyEnv) do(t *testing.T, role, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if role != "" {
		tok, err := e.mgr.Generate(e.adminID, "sa@example.test", role, 0)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *auditVerifyEnv) insertLog(t *testing.T, msg string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`INSERT INTO audit_logs (category, action, level, message) VALUES ('route-test', 'act', 'info', $1) RETURNING id`, msg).Scan(&id))
	return id
}

func TestAuditVerifyEndpoint_AccessControl(t *testing.T) {
	e := newAuditVerifyEnv(t)
	e.insertLog(t, "x")
	const path = "/api/admin/audit/verify?table=audit_logs"
	assert.Equal(t, http.StatusUnauthorized, e.do(t, "", "GET", path, "").Code, "anonymous")
	assert.Equal(t, http.StatusForbidden, e.do(t, "user", "GET", path, "").Code)
	assert.Equal(t, http.StatusForbidden, e.do(t, "enterprise_admin", "GET", path, "").Code)
	assert.Equal(t, http.StatusForbidden, e.do(t, "system_admin", "GET", path, "").Code, "system_admin may read logs but not attest integrity")
	assert.Equal(t, http.StatusOK, e.do(t, "super_admin", "GET", path, "").Code)
}

func TestAuditVerifyEndpoint_ResultShape(t *testing.T) {
	e := newAuditVerifyEnv(t)
	for n := 0; n < 3; n++ {
		e.insertLog(t, fmt.Sprintf("m%d", n))
	}
	w := e.do(t, "super_admin", "GET", "/api/admin/audit/verify?table=audit_logs", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	for _, k := range []string{"ok", "checked", "first_broken_id", "reason", "anchors_checked", "anchor_mismatches", "soft_deleted_count", "purged_before_id"} {
		assert.Contains(t, body, k)
	}
	assert.Equal(t, true, body["ok"])
	assert.EqualValues(t, 3, body["checked"])
	assert.Nil(t, body["first_broken_id"])
	assert.Equal(t, []any{}, body["anchor_mismatches"], "empty array, not null")

	// Default table is audit_logs; the enterprise table is selectable.
	w = e.do(t, "super_admin", "GET", "/api/admin/audit/verify", "")
	assert.Equal(t, http.StatusOK, w.Code)
	w = e.do(t, "super_admin", "GET", "/api/admin/audit/verify?table=enterprise_audit_log", "")
	assert.Equal(t, http.StatusOK, w.Code)

	// Validation.
	assert.Equal(t, http.StatusBadRequest, e.do(t, "super_admin", "GET", "/api/admin/audit/verify?table=users", "").Code)
	assert.Equal(t, http.StatusBadRequest, e.do(t, "super_admin", "GET", "/api/admin/audit/verify?from=not-a-date", "").Code)
	assert.Equal(t, http.StatusOK, e.do(t, "super_admin", "GET", "/api/admin/audit/verify?from=2020-01-01&to=2100-01-01", "").Code)
}

func TestAuditVerifyEndpoint_ReportsTamperingAndAnchors(t *testing.T) {
	e := newAuditVerifyEnv(t)
	ctx := context.Background()
	var ids []int64
	for n := 0; n < 4; n++ {
		ids = append(ids, e.insertLog(t, fmt.Sprintf("m%d", n)))
	}
	_, err := e.d.CreateAuditAnchors(ctx, auditVerifyTestKey, time.Now().UTC())
	require.NoError(t, err)

	w := e.do(t, "super_admin", "GET", "/api/admin/audit/verify?table=audit_logs", "")
	var ok map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ok))
	assert.Equal(t, true, ok["ok"])
	assert.EqualValues(t, 1, ok["anchors_checked"])

	// Attacker edits a row with the triggers disabled.
	tx, err := e.d.Pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `ALTER TABLE audit_logs DISABLE TRIGGER USER`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE audit_logs SET message = 'forged' WHERE id = $1`, ids[1])
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `ALTER TABLE audit_logs ENABLE TRIGGER USER`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	w = e.do(t, "super_admin", "GET", "/api/admin/audit/verify?table=audit_logs", "")
	require.Equal(t, http.StatusOK, w.Code, "a failed verification is still a successful answer")
	var bad map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bad))
	assert.Equal(t, false, bad["ok"])
	assert.Equal(t, fmt.Sprint(ids[1]), bad["first_broken_id"], "ids are always rendered as strings (enterprise_audit_log ids are UUIDs)")
	assert.Contains(t, bad["reason"], "modified")
}

func TestAuditSoftDelete_RequiresReasonAndRecordsIt(t *testing.T) {
	e := newAuditVerifyEnv(t)
	ctx := context.Background()
	id := e.insertLog(t, "needs hiding")
	path := fmt.Sprintf("/api/admin/audit-logs/%d/soft-delete", id)

	// Missing / blank / oversized reason: rejected, nothing changes.
	assert.Equal(t, http.StatusBadRequest, e.do(t, "super_admin", "POST", path, "").Code)
	assert.Equal(t, http.StatusBadRequest, e.do(t, "super_admin", "POST", path, `{"reason":"   "}`).Code)
	long := make([]byte, maxSoftDeleteReasonLen+1)
	for i := range long {
		long[i] = 'a'
	}
	assert.Equal(t, http.StatusBadRequest, e.do(t, "super_admin", "POST", path, `{"reason":"`+string(long)+`"}`).Code)
	var deletedAt *time.Time
	require.NoError(t, e.d.Pool.QueryRow(ctx, `SELECT deleted_at FROM audit_logs WHERE id = $1`, id).Scan(&deletedAt))
	assert.Nil(t, deletedAt)

	// Only super_admin may soft-delete.
	assert.Equal(t, http.StatusForbidden, e.do(t, "system_admin", "POST", path, `{"reason":"because"}`).Code)

	w := e.do(t, "super_admin", "POST", path, `{"reason":"  duplicate of #12  "}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var reason *string
	var deletedBy *string
	require.NoError(t, e.d.Pool.QueryRow(ctx, `SELECT deleted_at, delete_reason, deleted_by::text FROM audit_logs WHERE id = $1`, id).Scan(&deletedAt, &reason, &deletedBy))
	require.NotNil(t, deletedAt)
	require.NotNil(t, reason)
	assert.Equal(t, "duplicate of #12", *reason, "reason is trimmed")
	assert.Equal(t, e.adminID, *deletedBy)

	// The act of hiding a row is itself in the audit trail, with the reason.
	e.logger.Stop(ctx) // flush the async buffer
	var msg string
	require.NoError(t, e.d.Pool.QueryRow(ctx,
		`SELECT message FROM audit_logs WHERE action = 'audit_soft_deleted' ORDER BY id DESC LIMIT 1`).Scan(&msg))
	assert.Contains(t, msg, "duplicate of #12")
	assert.Contains(t, msg, fmt.Sprint(id))

	// The soft-deleted count is surfaced by verify, and the chain stays valid.
	w = e.do(t, "super_admin", "GET", "/api/admin/audit/verify?table=audit_logs", "")
	var v map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	assert.Equal(t, true, v["ok"])
	assert.EqualValues(t, 1, v["soft_deleted_count"])

	// Deleting twice is a 404 (already hidden); restore brings it back.
	assert.Equal(t, http.StatusNotFound, e.do(t, "super_admin", "POST", path, `{"reason":"again"}`).Code)
	w = e.do(t, "super_admin", "POST", fmt.Sprintf("/api/admin/audit-logs/%d/restore", id), "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, e.d.Pool.QueryRow(ctx, `SELECT deleted_at, delete_reason FROM audit_logs WHERE id = $1`, id).Scan(&deletedAt, &reason))
	assert.Nil(t, deletedAt)
	assert.Nil(t, reason)
	w = e.do(t, "super_admin", "GET", "/api/admin/audit/verify?table=audit_logs", "")
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	assert.Equal(t, true, v["ok"], "restore must not break the chain")
}
