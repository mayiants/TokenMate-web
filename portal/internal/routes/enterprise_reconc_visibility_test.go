package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/testutil"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/mail"
)

func reconcVisibilityDB(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	if err := db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		t.Fatalf("init test schema: %v", err)
	}
	pool, err := pgxpool.New(ctx, testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}
	return &db.DB{Pool: pool}
}

func newReconcVisibilityRoutes(t *testing.T, database *db.DB) *AdminRoutes {
	t.Helper()
	cfg := &config.Config{}
	logger := zap.NewNop()
	auth := middleware.NewAuth(cfg, logger)
	auth.SetDB(database)
	return NewAdminRoutes(database, cfg, logger, auth, &audit.Logger{}, nil, &mail.Sender{})
}

// TestEnterpriseReconciliation_SoftDeletedKeyUsageStillVisible is a regression
// test for the audit-visibility gap where an enterprise admin lost all of a
// key's historical usage/cost the moment the key was deleted. The enterprise
// reconciliation member summary scopes usage through api_keys; previously the
// JOIN filtered k.deleted_at IS NULL, so a (soft) deleted key's records
// vanished from the view. A deleted key must NOT erase its usage history.
func TestEnterpriseReconciliation_SoftDeletedKeyUsageStillVisible(t *testing.T) {
	d := reconcVisibilityDB(t)
	defer d.Close()
	r := newReconcVisibilityRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := "11111111-1111-1111-1111-" + fmt.Sprintf("%012d", time.Now().UnixNano()%1e12)
	memberEmail := "recon-member-" + suffix + "@example.com"

	// Enterprise.
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO enterprises (id, name, status, created_at, updated_at)
		 VALUES ($1::uuid, $2, 'active', NOW(), NOW())`,
		entID, "Recon Vis Ent "+suffix); err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, "DELETE FROM enterprises WHERE id=$1::uuid", entID) })

	// Member user.
	var memberID string
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, enterprise_id, created_at, updated_at)
		 VALUES (gen_random_uuid(), 'Recon Member', $1, 'hash', 'user', $2::uuid, NOW(), NOW())
		 RETURNING id::text`,
		memberEmail, entID).Scan(&memberID); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id=$1::uuid", memberID) })

	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at)
		 VALUES ($1::uuid, $2::uuid, 'member', NOW())
		 ON CONFLICT (enterprise_id, user_id) DO NOTHING`,
		entID, memberID); err != nil {
		t.Fatalf("insert enterprise_member: %v", err)
	}

	// A SOFT-DELETED enterprise key assigned to the member.
	var keyID string
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status,
		                       key_type, enterprise_id, assigned_user_id,
		                       created_at, updated_at, deleted_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-wgit-en-r', 'Deleted Key', 'disabled',
		         'enterprise', $3::uuid, $1::uuid, NOW(), NOW(), NOW())
		 RETURNING id::text`,
		memberID, "hash-"+suffix, entID).Scan(&keyID); err != nil {
		t.Fatalf("insert soft-deleted key: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE id=$1::uuid", keyID) })

	// A usage record on that deleted key.
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO usage_records (trace_id, user_id, user_api_key_id, model, provider,
		                            input_tokens, output_tokens, total_cost, request_count, status, created_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 'glm-5.3', 'approach',
		         100, 50, 12345, 1, 'success', NOW())`,
		memberID, keyID); err != nil {
		t.Fatalf("insert usage_record: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_api_key_id=$1::uuid", keyID) })

	// Call the handler.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/admin/reconciliation/enterprises/"+entID+"/usage?range=30d", nil)
	c.Params = []gin.Param{{Key: "id", Value: entID}}
	r.getEnterpriseReconciliation(c)

	if w.Code != 200 {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Members []struct {
			UserID        string  `json:"user_id"`
			TotalRequests int64   `json:"total_requests"`
			TotalCost     float64 `json:"total_cost"`
		} `json:"members"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, w.Body.String())
	}

	var found bool
	for _, m := range resp.Members {
		if m.UserID == memberID {
			found = true
			if m.TotalRequests < 1 {
				t.Errorf("soft-deleted key usage lost: member total_requests=%d, want >=1", m.TotalRequests)
			}
		}
	}
	if !found {
		t.Errorf("member %s not present in reconciliation members; soft-deleted-key usage hidden (body=%s)", memberID, w.Body.String())
	}
}
