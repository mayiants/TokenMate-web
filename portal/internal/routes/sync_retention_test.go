package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestSyncRetentionPolicies(t *testing.T) {
	d := testEnterpriseDB(t)
	t.Cleanup(d.Close) // registered first => runs last, after the deferred data cleanup
	ctx := context.Background()

	var entID string
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, retention_days) VALUES ('sync-retention-test', 45) RETURNING id::text`).Scan(&entID); err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}
	defer func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, entID) }()

	cfg := &config.Config{Portal: config.PortalConfig{SyncSecret: "sync-secret"}}
	sr := NewSyncRoutes(d, cfg, zap.NewNop(), services.NewPricingService(d, zap.NewNop()))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	sr.Register(r.Group("/api/admin"))

	call := func(secret string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/retention/policies/sync", nil)
		if secret != "" {
			req.Header.Set("x-proxy-secret", secret)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if w := call(""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no secret: got %d, want 401", w.Code)
	}
	if w := call("wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: got %d, want 401", w.Code)
	}

	w := call("sync-secret")
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Platform    struct{ Days int } `json:"platform"`
		Enterprises []struct {
			ID   string `json:"id"`
			Days int    `json:"days"`
		} `json:"enterprises"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Platform.Days < 1 || body.Platform.Days > 365 {
		t.Errorf("platform days out of range: %d", body.Platform.Days)
	}
	found := false
	for _, e := range body.Enterprises {
		if e.ID == entID {
			found = true
			if e.Days != 45 {
				t.Errorf("enterprise days = %d, want 45", e.Days)
			}
		}
	}
	if !found {
		t.Error("test enterprise missing from policy list")
	}

	// A change is visible on the next fetch (this is what the Proxy relies on).
	if _, err := d.Pool.Exec(ctx, `UPDATE enterprises SET retention_days = 120 WHERE id = $1::uuid`, entID); err != nil {
		t.Fatal(err)
	}
	w = call("sync-secret")
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	for _, e := range body.Enterprises {
		if e.ID == entID && e.Days != 120 {
			t.Errorf("after update days = %d, want 120", e.Days)
		}
	}
}
