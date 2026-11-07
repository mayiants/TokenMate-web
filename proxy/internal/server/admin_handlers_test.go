package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mayiants/ai-proxy-go/internal/auth"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/pkg/keypool"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{"empty URL", "", false},
		{"valid HTTPS", "https://api.openai.com", false},
		{"valid HTTP", "http://api.openai.com", false},
		{"invalid scheme ftp", "ftp://api.openai.com", true},
		{"localhost", "http://localhost:8080", true},
		{"127.0.0.1", "http://127.0.0.1:8080", true},
		{"0.0.0.0", "http://0.0.0.0:8080", true},
		{"::1", "http://[::1]:8080", true},
		{"metadata service 169.254.x.x", "http://169.254.169.254/latest", true},
		{"private IP 10.x.x.x", "http://10.0.0.1", true},
		{"private IP 192.168.x.x", "http://192.168.1.1", true},
		{"private IP 172.16.x.x", "http://172.16.0.1", true},
		{"no host", "http:///path", true},
		{"invalid URL format", "not-a-url", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBaseURL(tt.baseURL)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"openai", "OpenAI GPT"},
		{"anthropic", "Anthropic (Claude)"},
		{"google", "Google Gemini"},
		{"deepseek", "DeepSeek"},
		{"ali", "Ali (阿里云)"},
		{"moonshot", "Moonshot (Kimi)"},
		{"zhipu", "Zhipu (智谱)"},
		{"tencent", "Tencent (腾讯混元)"},
		{"bytedance", "Bytedance (字节Seed)"},
		{"minimax", "MiniMax"},
		{"mistral", "Mistral"},
		{"cohere", "Cohere"},
		{"xai", "xAI (Grok)"},
		{"meta", "Meta Llama"},
		{"amazon", "Amazon Bedrock"},
		{"microsoft", "Microsoft Azure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, getDisplayName(tt.name))
		})
	}

	// Unknown provider returns its own name
	t.Run("unknown provider", func(t *testing.T) {
		assert.Equal(t, "unknown-provider", getDisplayName("unknown-provider"))
	})
}

func TestHandleGetStatus_NoManager(t *testing.T) {
	// Save and restore global
	service := NewHandlerService(nil, nil)
	service.keyPoolManager = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	service.handleGetStatus(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "operational", resp["status"])
	assert.NotEmpty(t, resp["lastUpdate"])
	assert.Empty(t, resp["providers"])
}

func TestHandleGetStatusProviders_NoManager(t *testing.T) {
	service := NewHandlerService(nil, nil)
	service.keyPoolManager = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	service.handleGetStatusProviders(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Empty(t, resp["providers"])
}

func TestHandleGetStatusHistory(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	service.handleGetStatusHistory(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Empty(t, resp["history"])
}

func TestHandleHeartbeat_InvalidBody(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/status/heartbeat", bytes.NewReader([]byte("not json")))

	service.handleHeartbeat(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleUsageReport_InvalidBody(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/webhook/usage", bytes.NewReader([]byte("not json")))

	service.handleUsageReport(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleBatchUsageReport_ValidBody(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"records": [{"id": "1"}], "serverId": "svr-1"}`
	c.Request = httptest.NewRequest("POST", "/api/webhook/usage-batch", bytes.NewReader([]byte(body)))

	service.handleBatchUsageReport(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, true, resp["ok"])
	assert.Equal(t, float64(1), resp["processed"])
}

func TestHandleAddKey_NoManager(t *testing.T) {
	service := NewHandlerService(nil, nil)
	service.keyPoolManager = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"provider": "openai", "key": "sk-test", "tier": "subscription"}`
	c.Request = httptest.NewRequest("POST", "/admin/keys", bytes.NewReader([]byte(body)))

	service.handleAddKey(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleDeleteKey_NoManager(t *testing.T) {
	service := NewHandlerService(nil, nil)
	service.keyPoolManager = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "provider", Value: "openai"}, {Key: "keyId", Value: "key-1"}}
	c.Request = httptest.NewRequest("DELETE", "/admin/keys/openai/key-1", nil)

	service.handleDeleteKey(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleToggleKey_NoManager(t *testing.T) {
	service := NewHandlerService(nil, nil)
	service.keyPoolManager = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "provider", Value: "openai"}, {Key: "keyId", Value: "key-1"}}
	c.Request = httptest.NewRequest("PATCH", "/admin/keys/openai/key-1", bytes.NewReader([]byte(`{"enabled": true}`)))

	service.handleToggleKey(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleGetKeyHistory_NoManager(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "provider", Value: "openai"}, {Key: "keyId", Value: "key-1"}}
	c.Request = httptest.NewRequest("GET", "/admin/keys/openai/key-1/history", nil)

	service.handleGetKeyHistory(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "openai", resp["provider"])
	assert.Equal(t, "key-1", resp["keyId"])
	assert.Empty(t, resp["history"])
}

func TestHandleGetProviderStats_NoManager(t *testing.T) {
	service := NewHandlerService(nil, nil)
	service.keyPoolManager = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	service.handleGetProviderStats(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// Returns empty JSON object when no manager
	assert.Equal(t, "{}", w.Body.String())
}

func TestHandleGetModelStats_NoRegistry(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	service.handleGetModelStats(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "not_initialized", resp["status"])
}

func TestHandleGetModelList_NoRegistry(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	service.handleGetModelList(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Empty(t, resp["models"])
}

func TestHandleSyncKeys_NoManager(t *testing.T) {
	service := NewHandlerService(nil, nil)
	service.keyPoolManager = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/admin/key-pool/sync", nil)

	service.handleSyncKeys(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleImportKeys_NoManager(t *testing.T) {
	service := NewHandlerService(nil, nil)
	service.keyPoolManager = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"provider": "openai", "keys": [{"key": "sk-test"}], "baseUrl": "https://api.openai.com"}`
	c.Request = httptest.NewRequest("POST", "/admin/keys/import", bytes.NewReader([]byte(body)))

	service.handleImportKeys(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleWebhookSync_NoManager(t *testing.T) {
	service := NewHandlerService(nil, nil)
	service.keyPoolManager = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/webhook/sync", nil)

	service.handleWebhookSync(c)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleWebhookSync_WithManager(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-unit-tests"}}
	service := NewHandlerService(cfg, zap.NewNop())
	service.keyPoolManager = keypool.NewManager(cfg, zap.NewNop())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/webhook/sync", nil)

	service.handleWebhookSync(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["ok"])
}

func TestHandleWebhookBalanceUpdate(t *testing.T) {
	cfg := &config.Config{}
	service := NewHandlerService(cfg, zap.NewNop())
	service.authMiddleware = auth.NewMiddleware(cfg, zap.NewNop())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"type":"balance_update","user_id":"user-123","balance":0,"threshold":1}`
	c.Request = httptest.NewRequest("POST", "/api/webhook/balance-update", bytes.NewReader([]byte(body)))

	service.handleWebhookBalanceUpdate(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["ok"])
}

// TestHandleWebhookBalanceUpdate_Revocation verifies the HTTPS callback accepts
// the key_revoked / user_banned events Portal now sends to drop auth caches
// immediately (cross-host path, since Portal and Proxy use separate Redis).
func TestHandleWebhookBalanceUpdate_Revocation(t *testing.T) {
	cfg := &config.Config{}
	service := NewHandlerService(cfg, zap.NewNop())
	service.authMiddleware = auth.NewMiddleware(cfg, zap.NewNop())

	cases := []struct {
		name string
		body string
		code int
	}{
		{"key_revoked ok", `{"type":"key_revoked","key_hash":"abc123"}`, http.StatusOK},
		{"user_banned ok", `{"type":"user_banned","user_id":"user-123"}`, http.StatusOK},
		{"key_revoked missing hash", `{"type":"key_revoked"}`, http.StatusBadRequest},
		{"user_banned missing user", `{"type":"user_banned"}`, http.StatusBadRequest},
		{"unknown type", `{"type":"nope","user_id":"u"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/webhook/balance-update", bytes.NewReader([]byte(tc.body)))
			service.handleWebhookBalanceUpdate(c)
			assert.Equal(t, tc.code, w.Code)
		})
	}
}

func TestHandleWebhookSync_SkipsOnVersionMatch(t *testing.T) {
	var syncCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/key-pool/sync":
			syncCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"keys":            []map[string]interface{}{},
				"providers":       []map[string]interface{}{},
				"model_routing":   []map[string]interface{}{},
				"model_name_map":  []map[string]interface{}{},
				"provider_health": []map[string]interface{}{},
				"key_health":      []map[string]interface{}{},
				"smart_routing":   map[string]bool{},
				"config_version":  "v1",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-unit-tests"},
		Portal: config.PortalConfig{
			URL:        srv.URL,
			SyncSecret: "secret",
		},
	}
	service := NewHandlerService(cfg, zap.NewNop())
	mgr := keypool.NewManager(cfg, zap.NewNop())
	service.keyPoolManager = mgr

	require.NoError(t, mgr.SyncFromPortalWithContext(context.Background()))
	require.Equal(t, "v1", mgr.GetConfigVersion())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/webhook/sync", bytes.NewReader([]byte(`{"config_version":"v1"}`)))

	service.handleWebhookSync(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["ok"])
	assert.Equal(t, true, resp["skipped"])
	// Sync was already called once to seed the manager; the webhook must not
	// trigger a second sync call.
	assert.Equal(t, 1, syncCalls)
}

func TestHandleWebhookSync_FullSyncOnVersionMismatch(t *testing.T) {
	var syncCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/key-pool/sync":
			syncCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"keys": []map[string]interface{}{
					{"id": "k1", "provider": "openai", "key": "sk-xxx", "key_preview": "pxxx", "tier": "premium", "priority": "high", "models": []string{"gpt-4"}},
				},
				"providers":       []map[string]interface{}{},
				"model_routing":   []map[string]interface{}{},
				"model_name_map":  []map[string]interface{}{},
				"provider_health": []map[string]interface{}{},
				"key_health":      []map[string]interface{}{},
				"smart_routing":   map[string]bool{},
				"config_version":  "v2",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-unit-tests"},
		Portal: config.PortalConfig{
			URL:        srv.URL,
			SyncSecret: "secret",
		},
	}
	service := NewHandlerService(cfg, zap.NewNop())
	mgr := keypool.NewManager(cfg, zap.NewNop())
	service.keyPoolManager = mgr

	require.NoError(t, mgr.SyncFromPortalWithContext(context.Background()))
	require.Equal(t, "v2", mgr.GetConfigVersion())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/webhook/sync", bytes.NewReader([]byte(`{"config_version":"v1"}`)))

	service.handleWebhookSync(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["ok"])
	// The handler decided the version differs, so it did not skip synchronously.
	assert.Nil(t, resp["skipped"])
}

func TestHandleWebhookSync_FullSyncOnMissingVersion(t *testing.T) {
	var syncCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/key-pool/sync":
			syncCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"keys":            []map[string]interface{}{},
				"providers":       []map[string]interface{}{},
				"model_routing":   []map[string]interface{}{},
				"model_name_map":  []map[string]interface{}{},
				"provider_health": []map[string]interface{}{},
				"key_health":      []map[string]interface{}{},
				"smart_routing":   map[string]bool{},
				"config_version":  "v1",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-unit-tests"},
		Portal: config.PortalConfig{
			URL:        srv.URL,
			SyncSecret: "secret",
		},
	}
	service := NewHandlerService(cfg, zap.NewNop())
	mgr := keypool.NewManager(cfg, zap.NewNop())
	service.keyPoolManager = mgr
	require.NoError(t, mgr.SyncFromPortalWithContext(context.Background()))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/webhook/sync", bytes.NewReader([]byte(`{}`)))

	service.handleWebhookSync(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["ok"])
	// Missing config_version falls back to full sync.
	assert.Nil(t, resp["skipped"])
}

func TestHandleWebhookSync_FullSyncOnEmptyLocalVersion(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-unit-tests"},
		Portal: config.PortalConfig{
			URL:        "http://invalid",
			SyncSecret: "secret",
		},
	}
	service := NewHandlerService(cfg, zap.NewNop())
	mgr := keypool.NewManager(cfg, zap.NewNop())
	service.keyPoolManager = mgr
	// Local config version is empty.
	require.Equal(t, "", mgr.GetConfigVersion())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/webhook/sync", bytes.NewReader([]byte(`{"config_version":"v1"}`)))

	service.handleWebhookSync(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// A non-empty remote version with an empty local version should trigger sync.
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp["skipped"])
}

func TestHandleWebhookBalanceUpdate_InvalidBody(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/webhook/balance-update", bytes.NewReader([]byte(`not-json`)))

	service.handleWebhookBalanceUpdate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleGetSelectionState(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-unit-tests"}}
	service := NewHandlerService(cfg, zap.NewNop())
	mgr := keypool.NewManager(cfg, zap.NewNop())
	require.NoError(t, mgr.AddKey(&keypool.Key{KeyStatic: keypool.KeyStatic{
		ID: "key-1", Provider: "openai", Priority: "normal", Models: []string{"gpt-4o"}, Enabled: true,
	}}))
	service.keyPoolManager = mgr

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/admin/keypool/selection-state?provider=openai", nil)

	service.handleGetSelectionState(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Count     int `json:"count"`
		Providers []struct {
			Provider string `json:"provider"`
			Keys     []struct {
				KeyID      string `json:"key_id"`
				HealthTier int    `json:"health_tier"`
			} `json:"keys"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Count)
	require.Len(t, resp.Providers, 1)
	assert.Equal(t, "openai", resp.Providers[0].Provider)
	require.Len(t, resp.Providers[0].Keys, 1)
	assert.Equal(t, "key-1", resp.Providers[0].Keys[0].KeyID)
	assert.Equal(t, 0, resp.Providers[0].Keys[0].HealthTier)
	assert.NotContains(t, w.Body.String(), "sk-", "response must not contain key material")
}

func TestHandleGetSelectionState_NoManager(t *testing.T) {
	service := NewHandlerService(nil, nil)
	service.keyPoolManager = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/admin/keypool/selection-state", nil)

	service.handleGetSelectionState(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
