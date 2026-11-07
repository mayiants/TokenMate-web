package keypool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func syncResponseBody(version string) map[string]interface{} {
	return map[string]interface{}{
		"keys": []map[string]interface{}{
			{
				"id":          "k1",
				"provider":    "openai",
				"key":         "sk-xxx",
				"key_preview": "pxxx",
				"tier":        "premium",
				"priority":    "high",
				"models":      []string{"gpt-4"},
			},
		},
		"providers":       []map[string]interface{}{},
		"model_routing":   []map[string]interface{}{},
		"model_name_map":  []map[string]interface{}{},
		"provider_health": []map[string]interface{}{},
		"key_health":      []map[string]interface{}{},
		"smart_routing":   map[string]bool{},
		"config_version":  version,
	}
}

func testSyncConfig() *config.Config {
	return &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-tests-only"},
		Portal: config.PortalConfig{
			URL:        "http://invalid",
			SyncSecret: "secret",
		},
	}
}

func TestSyncFromPortal_AppliesAndStoresConfigVersion(t *testing.T) {
	m := NewManager(testSyncConfig(), zap.NewNop())
	assert.Equal(t, "", m.GetConfigVersion())

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(syncResponseBody("v1"))
	}))
	defer srv.Close()

	m.portalURL = srv.URL
	ctx := context.Background()
	require.NoError(t, m.SyncFromPortalWithContext(ctx))
	assert.Equal(t, "v1", m.GetConfigVersion())
	assert.Equal(t, 1, calls)

	// Same version: request is still made, but expensive merge/persist is skipped.
	require.NoError(t, m.SyncFromPortalWithContext(ctx))
	assert.Equal(t, "v1", m.GetConfigVersion())
	assert.Equal(t, 2, calls)
}

func TestSyncFromPortal_AppliesNewConfigVersion(t *testing.T) {
	m := NewManager(testSyncConfig(), zap.NewNop())

	version := "v1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(syncResponseBody(version))
	}))
	defer srv.Close()

	m.portalURL = srv.URL
	ctx := context.Background()
	require.NoError(t, m.SyncFromPortalWithContext(ctx))
	assert.Equal(t, "v1", m.GetConfigVersion())

	// Simulate portal config change.
	version = "v2"
	require.NoError(t, m.SyncFromPortalWithContext(ctx))
	assert.Equal(t, "v2", m.GetConfigVersion())
}

func TestSyncFromPortal_BackwardCompatibleWithoutVersion(t *testing.T) {
	m := NewManager(testSyncConfig(), zap.NewNop())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"keys":            []map[string]interface{}{},
			"providers":       []map[string]interface{}{},
			"model_routing":   []map[string]interface{}{},
			"model_name_map":  []map[string]interface{}{},
			"provider_health": []map[string]interface{}{},
			"key_health":      []map[string]interface{}{},
			"smart_routing":   map[string]bool{},
		})
	}))
	defer srv.Close()

	m.portalURL = srv.URL
	ctx := context.Background()
	require.NoError(t, m.SyncFromPortalWithContext(ctx))
	assert.Equal(t, "", m.GetConfigVersion())
}

func TestStartSyncLoop_PollsVersionBeforeFullSync(t *testing.T) {
	var versionCalls, syncCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/key-pool/version":
			versionCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"config_version": "v1",
				"synced_at":      "2026-01-01T00:00:00Z",
			})
		case "/api/admin/key-pool/sync":
			syncCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(syncResponseBody("v1"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := testSyncConfig()
	cfg.Portal.URL = srv.URL
	m := NewManager(cfg, zap.NewNop())
	atomic.StoreInt64(&m.syncInterval, int64(50*time.Millisecond))

	m.StartSyncLoop(context.Background())
	// Wait for the initial sync plus a couple of polling ticks.
	time.Sleep(180 * time.Millisecond)

	assert.GreaterOrEqual(t, versionCalls.Load(), int32(2), "version endpoint should be polled on subsequent ticks")
	assert.Equal(t, int32(1), syncCalls.Load(), "full sync should only run once (initial) when version is unchanged")
	assert.Equal(t, "v1", m.GetConfigVersion())
}

func TestStartSyncLoop_FullSyncWhenVersionChanges(t *testing.T) {
	var version atomic.Value
	version.Store("v1")
	var versionCalls, syncCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/key-pool/version":
			versionCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"config_version": version.Load(),
				"synced_at":      "2026-01-01T00:00:00Z",
			})
		case "/api/admin/key-pool/sync":
			syncCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(syncResponseBody(version.Load().(string)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := testSyncConfig()
	cfg.Portal.URL = srv.URL
	m := NewManager(cfg, zap.NewNop())
	atomic.StoreInt64(&m.syncInterval, int64(50*time.Millisecond))

	m.StartSyncLoop(context.Background())
	time.Sleep(80 * time.Millisecond)
	// Change the Portal config version.
	version.Store("v2")
	time.Sleep(150 * time.Millisecond)

	assert.GreaterOrEqual(t, versionCalls.Load(), int32(2), "version endpoint should be polled multiple times")
	assert.GreaterOrEqual(t, syncCalls.Load(), int32(2), "full sync should run again when version changes")
	assert.Equal(t, "v2", m.GetConfigVersion())
}

func TestSyncVersionFromPortal_ReturnsConfigVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/admin/key-pool/version", r.URL.Path)
		assert.Equal(t, "secret", r.Header.Get("x-proxy-secret"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"config_version": "abc123",
			"synced_at":      "2026-01-01T00:00:00Z",
		})
	}))
	defer srv.Close()

	cfg := testSyncConfig()
	cfg.Portal.URL = srv.URL
	m := NewManager(cfg, zap.NewNop())

	result, err := m.SyncVersionFromPortal(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "abc123", result.ConfigVersion)
	assert.Equal(t, "2026-01-01T00:00:00Z", result.SyncedAt)
}

func TestSyncVersionFromPortal_RequiresPortalConfig(t *testing.T) {
	m := NewManager(testSyncConfig(), zap.NewNop())
	m.portalURL = ""
	_, err := m.SyncVersionFromPortal(context.Background())
	assert.Error(t, err)
}
