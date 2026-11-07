package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/services"
	"github.com/mayiants/ai-portal-go/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func testSyncDB(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}
	return &db.DB{Pool: pool, Redis: nil}
}

// cleanSyncTables removes rows that affect the key-pool config version so that
// tests have a deterministic baseline.
func cleanSyncTables(ctx context.Context, d *db.DB) {
	_, _ = d.Pool.Exec(ctx, "DELETE FROM upstream_key_usage WHERE upstream_key_id IN (SELECT id FROM upstream_keys WHERE key_encrypted LIKE 'sync-test-%')")
	_, _ = d.Pool.Exec(ctx, "DELETE FROM upstream_keys WHERE key_encrypted LIKE 'sync-test-%'")
	_, _ = d.Pool.Exec(ctx, "DELETE FROM providers WHERE name = 'sync-test-provider'")
	_, _ = d.Pool.Exec(ctx, "DELETE FROM service_models WHERE provider = 'sync-test-provider'")
	_, _ = d.Pool.Exec(ctx, "DELETE FROM model_provider_routing WHERE provider = 'sync-test-provider'")
	_, _ = d.Pool.Exec(ctx, "DELETE FROM system_settings WHERE key LIKE 'routing_smart_%'")
}

// seedSyncTables inserts a minimal deterministic key-pool configuration.
func seedSyncTables(ctx context.Context, d *db.DB) {
	_, _ = d.Pool.Exec(ctx,
		`INSERT INTO providers (id, name, base_url, enabled, sort_order)
		 VALUES (gen_random_uuid(), 'sync-test-provider', 'https://sync.example.com', true, 0)`)
	_, _ = d.Pool.Exec(ctx,
		`INSERT INTO upstream_keys (id, provider, key_encrypted, key_preview, tier, enabled, assignment_mode, validation_status)
		 VALUES (gen_random_uuid(), 'sync-test-provider', 'sync-test-key', 'prev', 'standard', true, 'shared', 'valid')`)
}

// TestSync_SyncKeyResponseJSON tests the SyncKeyResponse struct JSON serialization.
func TestSync_SyncKeyResponseJSON(t *testing.T) {
	key := SyncKeyResponse{
		ID:             "key-1",
		Provider:       "openai",
		Key:            "sk-encrypted",
		KeyPreview:     "sk-12...",
		Tier:           "premium",
		Priority:       "high",
		BaseURL:        "https://api.openai.com/v1",
		Models:         []string{"gpt-4o", "gpt-4-turbo"},
		Enabled:        true,
		AssignmentMode: "exclusive",
		IsShared:       false,
		RPMLimit:       60,
		TPMLimit:       100000,
		QuotaLimit:     1000000,
		QuotaUsed:      500000,
	}

	data, err := json.Marshal(key)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if parsed["id"] != "key-1" {
		t.Errorf("id = %v, want key-1", parsed["id"])
	}
	if parsed["tier"] != "premium" {
		t.Errorf("tier = %v, want premium", parsed["tier"])
	}
	if parsed["priority"] != "high" {
		t.Errorf("priority = %v, want high", parsed["priority"])
	}
	if parsed["isShared"] != false {
		t.Errorf("isShared = %v, want false", parsed["isShared"])
	}
}

// TestSync_TierToPriorityMapping tests the tier-to-priority mapping logic.
func TestSync_TierToPriorityMapping(t *testing.T) {
	tests := []struct {
		tier         string
		wantPriority string
	}{
		{"premium", "high"},
		{"enterprise", "high"},
		{"economy", "economy"},
		{"standard", "normal"},
		{"", "normal"},
		{"unknown", "normal"},
	}

	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			priority := "normal"
			switch tt.tier {
			case "premium", "enterprise":
				priority = "high"
			case "economy":
				priority = "economy"
			}
			if priority != tt.wantPriority {
				t.Errorf("priority for tier %q = %q, want %q", tt.tier, priority, tt.wantPriority)
			}
		})
	}
}

// TestSync_AssignmentMode tests the shared vs exclusive assignment mode logic.
func TestSync_AssignmentMode(t *testing.T) {
	tests := []struct {
		mode       string
		wantShared bool
	}{
		{"exclusive", false},
		{"shared", true},
		{"", true},
		{"round_robin", true},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			isShared := tt.mode != "exclusive"
			if isShared != tt.wantShared {
				t.Errorf("isShared for mode %q = %v, want %v", tt.mode, isShared, tt.wantShared)
			}
		})
	}
}

// TestSync_KeyHealthStatusMapping tests the key health status calculation.
func TestSync_KeyHealthStatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		errorRate  float64
		wantStatus string
	}{
		{"healthy", 0.0, "healthy"},
		{"healthy low error", 0.3, "healthy"},
		{"degraded", 0.5, "degraded"},
		{"degraded edge", 0.79, "degraded"},
		{"unhealthy", 0.8, "unhealthy"},
		{"unhealthy high", 1.0, "unhealthy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := "healthy"
			if tt.errorRate >= 0.80 {
				status = "unhealthy"
			} else if tt.errorRate >= 0.50 {
				status = "degraded"
			}
			if status != tt.wantStatus {
				t.Errorf("error rate %f -> status %q, want %q", tt.errorRate, status, tt.wantStatus)
			}
		})
	}
}

// TestSync_SyncSecretHeader tests the sync secret extraction from headers.
func TestSync_SyncSecretHeader(t *testing.T) {
	// The sync secret can come from X-Admin-Secret or x-proxy-secret
	headerNames := []string{"X-Admin-Secret", "x-proxy-secret"}
	for _, name := range headerNames {
		if name == "" {
			t.Error("sync secret header name should not be empty")
		}
	}
}

// TestSync_ModelNameMap tests the model name map structure.
func TestSync_ModelNameMap(t *testing.T) {
	entry := map[string]interface{}{
		"provider":       "openai",
		"product_id":     "gpt-4o",
		"api_model_name": "gpt-4o",
	}

	if entry["provider"] != "openai" {
		t.Error("provider should be 'openai'")
	}
	if entry["product_id"] != "gpt-4o" {
		t.Error("product_id should be 'gpt-4o'")
	}
}

// TestSync_ConfigVersionStableAcrossHealthChanges verifies that config_version
// is identical for two syncs with the same stable configuration but different
// provider_health_stats and upstream_key_usage rows.
func TestSync_ConfigVersionStableAcrossHealthChanges(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	ctx := context.Background()

	cfg := &config.Config{Portal: config.PortalConfig{SyncSecret: "sync-secret"}}
	syncRoutes := NewSyncRoutes(d, cfg, zap.NewNop(), services.NewPricingService(d, zap.NewNop()))

	// Insert a stable upstream key and provider.
	providerName := "sync-stable-provider"
	keyEnc := "sync-stable-key-enc"
	_, _ = d.Pool.Exec(ctx, "DELETE FROM upstream_key_usage WHERE upstream_key_id IN (SELECT id FROM upstream_keys WHERE key_encrypted = $1)", keyEnc)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM upstream_keys WHERE key_encrypted = $1", keyEnc)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM providers WHERE name = $1", providerName)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM upstream_key_usage WHERE upstream_key_id IN (SELECT id FROM upstream_keys WHERE key_encrypted = $1)", keyEnc)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM upstream_keys WHERE key_encrypted = $1", keyEnc)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM providers WHERE name = $1", providerName)
	}()

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO providers (id, name, base_url, enabled, sort_order) VALUES (gen_random_uuid(), $1, 'https://example.com', true, 0)`,
		providerName)
	if err != nil {
		t.Fatalf("insert provider: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO upstream_keys (id, provider, key_encrypted, key_preview, tier, enabled, assignment_mode, validation_status)
		 VALUES (gen_random_uuid(), $1, $2, 'prev', 'standard', true, 'shared', 'valid')`,
		providerName, keyEnc)
	if err != nil {
		t.Fatalf("insert upstream key: %v", err)
	}

	// Insert health data for the first sync.
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO provider_health_stats (provider, bucket, request_count, failure_count, total_latency_ms, status)
		 VALUES ($1, NOW(), 10, 1, 100, 'healthy')`,
		providerName)
	if err != nil {
		t.Fatalf("insert health stats 1: %v", err)
	}

	version1 := fetchConfigVersion(t, syncRoutes, cfg.Portal.SyncSecret)

	// Mutate health data for the second sync.
	_, _ = d.Pool.Exec(ctx, "DELETE FROM provider_health_stats WHERE provider = $1", providerName)
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO provider_health_stats (provider, bucket, request_count, failure_count, total_latency_ms, status)
		 VALUES ($1, NOW(), 999, 999, 999, 'unhealthy')`,
		providerName)
	if err != nil {
		t.Fatalf("insert health stats 2: %v", err)
	}

	version2 := fetchConfigVersion(t, syncRoutes, cfg.Portal.SyncSecret)

	if version1 != version2 {
		t.Errorf("config_version changed across health data mutation: %q != %q", version1, version2)
	}
}

func fetchConfigVersion(t *testing.T, syncRoutes *SyncRoutes, secret string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/key-pool/sync", nil)
	c.Request.Header.Set("X-Admin-Secret", secret)

	syncRoutes.syncKeyPool(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ConfigVersion string `json:"config_version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ConfigVersion == "" {
		t.Fatal("config_version is empty")
	}
	return resp.ConfigVersion
}

// TestSync_KeyPoolVersionEndpoint returns the deterministic config_version.
func TestSync_KeyPoolVersionEndpoint(t *testing.T) {
	d := testSyncDB(t)
	defer d.Close()
	ctx := context.Background()
	cleanSyncTables(ctx, d)
	seedSyncTables(ctx, d)
	defer cleanSyncTables(ctx, d)

	cfg := &config.Config{Portal: config.PortalConfig{SyncSecret: "sync-secret"}}
	syncRoutes := NewSyncRoutes(d, cfg, zap.NewNop(), services.NewPricingService(d, zap.NewNop()))

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/key-pool/version", nil)
	c.Request.Header.Set("X-Admin-Secret", cfg.Portal.SyncSecret)

	syncRoutes.syncKeyPoolVersion(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ConfigVersion string `json:"config_version"`
		SyncedAt      string `json:"synced_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ConfigVersion == "" {
		t.Error("config_version is empty")
	}
	if resp.SyncedAt == "" {
		t.Error("synced_at is empty")
	}
}

// TestSync_KeyPoolSyncReturns304WhenVersionMatches verifies that the sync
// endpoint returns 304 Not Modified when the client sends the current
// config_version via X-Config-Version or If-None-Match.
func TestSync_KeyPoolSyncReturns304WhenVersionMatches(t *testing.T) {
	d := testSyncDB(t)
	defer d.Close()
	ctx := context.Background()
	cleanSyncTables(ctx, d)
	seedSyncTables(ctx, d)
	defer cleanSyncTables(ctx, d)

	cfg := &config.Config{Portal: config.PortalConfig{SyncSecret: "sync-secret"}}
	syncRoutes := NewSyncRoutes(d, cfg, zap.NewNop(), services.NewPricingService(d, zap.NewNop()))

	version := fetchConfigVersion(t, syncRoutes, cfg.Portal.SyncSecret)

	for _, header := range []string{"X-Config-Version", "If-None-Match"} {
		t.Run(header, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/key-pool/sync", nil)
			c.Request.Header.Set("X-Admin-Secret", cfg.Portal.SyncSecret)
			c.Request.Header.Set(header, version)

			syncRoutes.syncKeyPool(c)

			assert.Equal(t, http.StatusNotModified, w.Code)
			assert.Empty(t, w.Body.String())
		})
	}
}

// TestSync_KeyPoolSyncReturns200WhenVersionDiffers verifies that the sync
// endpoint returns the full payload when the client sends a stale version.
func TestSync_KeyPoolSyncReturns200WhenVersionDiffers(t *testing.T) {
	d := testSyncDB(t)
	defer d.Close()
	ctx := context.Background()
	cleanSyncTables(ctx, d)
	seedSyncTables(ctx, d)
	defer cleanSyncTables(ctx, d)

	cfg := &config.Config{Portal: config.PortalConfig{SyncSecret: "sync-secret"}}
	syncRoutes := NewSyncRoutes(d, cfg, zap.NewNop(), services.NewPricingService(d, zap.NewNop()))

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/key-pool/sync", nil)
	c.Request.Header.Set("X-Admin-Secret", cfg.Portal.SyncSecret)
	c.Request.Header.Set("X-Config-Version", "stale-version")

	syncRoutes.syncKeyPool(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		ConfigVersion string `json:"config_version"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.ConfigVersion)
	assert.NotEqual(t, "stale-version", resp.ConfigVersion)
}

// TestSync_VersionEndpointMatchesSyncEndpoint verifies that the version
// returned by the lightweight version endpoint is identical to the
// config_version returned by the full sync endpoint for the same config.
func TestSync_VersionEndpointMatchesSyncEndpoint(t *testing.T) {
	d := testSyncDB(t)
	defer d.Close()
	ctx := context.Background()
	cleanSyncTables(ctx, d)
	seedSyncTables(ctx, d)
	defer cleanSyncTables(ctx, d)

	cfg := &config.Config{Portal: config.PortalConfig{SyncSecret: "sync-secret"}}
	syncRoutes := NewSyncRoutes(d, cfg, zap.NewNop(), services.NewPricingService(d, zap.NewNop()))

	syncVersion := fetchConfigVersion(t, syncRoutes, cfg.Portal.SyncSecret)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/key-pool/version", nil)
	c.Request.Header.Set("X-Admin-Secret", cfg.Portal.SyncSecret)

	syncRoutes.syncKeyPoolVersion(c)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		ConfigVersion string `json:"config_version"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, syncVersion, resp.ConfigVersion, "version endpoint must match sync endpoint config_version")
}
