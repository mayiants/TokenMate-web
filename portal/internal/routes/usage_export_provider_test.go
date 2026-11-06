package routes

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
)

func usageExportDB(t *testing.T) *db.DB {
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

// TestExportUsageCSV_ProviderOnlyForAdmin verifies that the upstream provider
// (and region) column is present only for system/super admins; a regular
// user's usage CSV export must be provider-agnostic. The check is on the CSV
// header, which is where the upstream columns are emitted/omitted.
func TestExportUsageCSV_ProviderOnlyForAdmin(t *testing.T) {
	d := usageExportDB(t)
	defer d.Close()
	cfg := &config.Config{}
	logger := zap.NewNop()
	auth := middleware.NewAuth(cfg, logger)
	auth.SetDB(d)
	r := NewUsageRoutes(d, auth)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID string
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, created_at, updated_at)
		 VALUES (gen_random_uuid(), 'Exp User', $1, 'hash', 'user', NOW(), NOW()) RETURNING id::text`,
		"exp-"+suffix+"@example.com").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id=$1::uuid", userID) })

	call := func(role string) string {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/usage/export", nil)
		c.Set("user_id", userID)
		c.Set("role", role)
		r.exportUsageCSV(c)
		if w.Code != 200 {
			t.Fatalf("role=%s status=%d body=%s", role, w.Code, w.Body.String())
		}
		return w.Body.String()
	}

	userCSV := call("user")
	if strings.Contains(userCSV, "Provider") || strings.Contains(userCSV, "Region") {
		t.Errorf("user export must not contain upstream Provider/Region columns:\n%s", userCSV)
	}

	adminCSV := call("system_admin")
	if !strings.Contains(adminCSV, "Provider") {
		t.Errorf("admin export should include the Provider column but did not:\n%s", adminCSV)
	}
}
