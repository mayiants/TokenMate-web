package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/internal/auth"
	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"github.com/ai-proxy/ai-proxy-go/internal/ratelimit"
	"github.com/ai-proxy/ai-proxy-go/pkg/keypool"
	"github.com/ai-proxy/ai-proxy-go/pkg/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func noopLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

func noopAuthMiddleware() *auth.Middleware {
	cfg := &config.Config{}
	return auth.NewMiddleware(cfg, noopLogger())
}

func noopRateLimitMiddleware() *ratelimit.Middleware {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{Enabled: false},
	}
	return ratelimit.NewMiddleware(cfg, noopLogger())
}

func TestCheckAllowedModels(t *testing.T) {
	tests := []struct {
		name          string
		allowedModels interface{}
		model         string
		shouldAllow   bool
	}{
		{"no restriction (nil)", nil, "gpt-4o", true},
		{"no restriction (empty)", []string{}, "gpt-4o", true},
		{"model in list", []string{"gpt-4o", "claude-3"}, "gpt-4o", true},
		{"model not in list", []string{"gpt-4o", "claude-3"}, "gpt-3.5", false},
		{"single model allowed", []string{"gpt-4o"}, "gpt-4o", true},
		{"single model denied", []string{"gpt-4o"}, "claude-3", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			if tt.allowedModels != nil {
				c.Set("allowed_models", tt.allowedModels)
			}
			assert.Equal(t, tt.shouldAllow, checkAllowedModels(c, tt.model))
		})
	}
}

func TestGetUserPriority(t *testing.T) {
	tests := []struct {
		accessMode string
		expected   string
	}{
		{"enterprise", "high"},
		{"subscription", "high"},
		{"token_plan", "high"},
		{"single_model", "high"},
		{"economy", "economy"},
		{"general", "normal"},
		{"", "normal"},
		{"unknown", "normal"},
	}

	for _, tt := range tests {
		t.Run(tt.accessMode, func(t *testing.T) {
			assert.Equal(t, tt.expected, getUserPriority(tt.accessMode))
		})
	}
}

func TestResolveModel(t *testing.T) {
	tests := []struct {
		name           string
		reqModel       string
		routingMode    string
		currentModel   string
		selectedModels []string
		allowedModels  []string
		expectError    bool
		expectedModel  string
	}{
		{"direct model", "gpt-4o", "single", "", nil, nil, false, "gpt-4o"},
		{"wgit-maas-latest single", "wgit-maas-latest", "single", "gpt-4o", nil, nil, false, "gpt-4o"},
		{"wgit-maas-latest single no current", "wgit-maas-latest", "single", "", nil, nil, true, ""},
		{"wgit-maas-latest round robin", "wgit-maas-latest", "round_robin", "", []string{"gpt-4o", "claude-3"}, nil, false, ""},
		{"wgit-maas-latest priority", "wgit-maas-latest", "priority", "", []string{"gpt-4o", "claude-3"}, nil, false, "gpt-4o"},
		{"model not in allowed list", "gpt-4o", "single", "", nil, []string{"claude-3"}, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewHandlerService(&config.Config{}, noopLogger())
			result, err := svc.resolveModel(tt.reqModel, tt.routingMode, tt.currentModel, tt.selectedModels, tt.allowedModels, "test-key")
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.expectedModel != "" {
					assert.Equal(t, tt.expectedModel, result)
				}
			}
		})
	}
}

func TestNormalizeReasoningFields(t *testing.T) {
	raw := json.RawMessage(`"thinking step by step"`)
	rawObj := json.RawMessage(`{"text":"obj form"}`)
	existing := "already standard"

	t.Run("deepseek maps reasoning to reasoning_content", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "deepseek-v4-flash"}
		req.Messages = []dto.Message{{Role: "assistant", Reasoning: raw}}
		normalizeReasoningFields(req)
		assert.NotNil(t, req.Messages[0].ReasoningContent)
		assert.Equal(t, "thinking step by step", *req.Messages[0].ReasoningContent)
		assert.Nil(t, req.Messages[0].Reasoning)
	})

	t.Run("deepseek keeps existing reasoning_content", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "deepseek-v4-pro"}
		req.Messages = []dto.Message{{Role: "assistant", Reasoning: raw, ReasoningContent: &existing}}
		normalizeReasoningFields(req)
		assert.Equal(t, "already standard", *req.Messages[0].ReasoningContent)
		assert.Nil(t, req.Messages[0].Reasoning)
	})

	t.Run("non-deepseek only strips reasoning", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "claude-opus-4-6"}
		req.Messages = []dto.Message{{Role: "assistant", Reasoning: raw}}
		normalizeReasoningFields(req)
		assert.Nil(t, req.Messages[0].ReasoningContent)
		assert.Nil(t, req.Messages[0].Reasoning)
	})

	t.Run("non-string reasoning stripped without mapping", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "deepseek-v4-flash"}
		req.Messages = []dto.Message{{Role: "assistant", Reasoning: rawObj}}
		normalizeReasoningFields(req)
		assert.Nil(t, req.Messages[0].ReasoningContent)
		assert.Nil(t, req.Messages[0].Reasoning)
	})
}

func TestSetupRoutes_Health(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
	}
	logger := noopLogger()
	service := NewHandlerService(cfg, logger)
	service.authMiddleware = noopAuthMiddleware()
	service.rateLimitMiddleware = noopRateLimitMiddleware()
	service.router = gin.New()

	setupRoutes(service)
	router := service.router

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "healthy", resp["status"])
}

func TestSetupRoutes_Metrics(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
	}
	logger := noopLogger()
	service := NewHandlerService(cfg, logger)
	service.authMiddleware = noopAuthMiddleware()
	service.rateLimitMiddleware = noopRateLimitMiddleware()
	service.router = gin.New()

	setupRoutes(service)
	router := service.router

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/metrics", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
}

func TestSetupRoutes_StatusEndpoints(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
	}
	logger := noopLogger()
	service := NewHandlerService(cfg, logger)
	service.authMiddleware = noopAuthMiddleware()
	service.rateLimitMiddleware = noopRateLimitMiddleware()
	service.router = gin.New()

	setupRoutes(service)
	router := service.router

	endpoints := []string{
		"/api/status",
		"/api/status/history",
	}

	for _, endpoint := range endpoints {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", endpoint, nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "endpoint %s should return 200", endpoint)
	}

	// /api/status/providers now requires admin auth
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/status/providers", nil)
	router.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusOK, w.Code, "/api/status/providers should require admin auth")
}

func TestSetupRoutes_InternalValidationRequiresSecret(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Admin:     config.AdminConfig{Secret: "test-admin-secret"},
	}
	logger := noopLogger()
	service := NewHandlerService(cfg, logger)
	service.authMiddleware = noopAuthMiddleware()
	service.rateLimitMiddleware = noopRateLimitMiddleware()
	service.router = gin.New()

	setupRoutes(service)
	router := service.router

	// Without secret
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/internal/validate-model", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "should require admin secret")

	// With secret
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/internal/validate-model", nil)
	req.Header.Set("X-Admin-Secret", "test-admin-secret")
	router.ServeHTTP(w, req)
	// Will be 400 because body is empty, but auth passes
	assert.Equal(t, http.StatusBadRequest, w.Code, "should pass auth but fail on empty body")
}

func TestSetupRoutes_AdminRequiresAuth(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Portal:    config.PortalConfig{SyncSecret: "admin-secret"},
	}
	logger := noopLogger()
	service := NewHandlerService(cfg, logger)
	service.authMiddleware = noopAuthMiddleware()
	service.rateLimitMiddleware = noopRateLimitMiddleware()
	service.router = gin.New()

	setupRoutes(service)
	router := service.router

	// Without auth
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/stats", nil)
	router.ServeHTTP(w, req)
	// Admin auth returns 403 (timestamp/nonce required) rather than 401
	assert.Equal(t, http.StatusForbidden, w.Code, "admin endpoint should require auth")
}

func TestHandleListModels(t *testing.T) {
	// Mock Portal /models/sync endpoint
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": []map[string]interface{}{
				{
					"id":           "model-1",
					"provider":     "openai",
					"productId":    "gpt-4o-product",
					"apiModelName": "gpt-4o",
					"status":       "active",
					"showInStore":  true,
					"capabilities": map[string]interface{}{
						"context_window":               128000,
						"max_output_tokens":            8192,
						"supports_thinking":            true,
						"supports_vision":              true,
						"supports_tool_calls":          true,
						"supports_json_mode":           true,
						"supports_parallel_tool_calls": true,
						"supports_streaming":           true,
						"reasoning_efforts":            []string{"low", "medium", "high"},
						"speed_tiers":                  []string{"default", "high"},
					},
				},
				{
					"id":           "model-2",
					"provider":     "anthropic",
					"productId":    "claude-product",
					"apiModelName": "claude-sonnet-4-6",
					"status":       "active",
					"showInStore":  false,
					"capabilities": map[string]interface{}{
						"context_window": 200000,
					},
				},
				{
					"id":           "model-3",
					"provider":     "openai",
					"productId":    "gpt-3.5-product",
					"apiModelName": "gpt-3.5-turbo",
					"status":       "paused",
					"showInStore":  true,
					"capabilities": map[string]interface{}{"context_window": 16000},
				},
			},
		})
	}))
	defer portal.Close()

	cfg := &config.Config{
		Portal: config.PortalConfig{
			URL:        portal.URL,
			SyncSecret: "test-secret",
		},
	}
	registry := models.NewModelRegistry(cfg, noopLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, registry.SyncFromPortal(ctx))

	svc := NewHandlerService(cfg, noopLogger())
	svc.SetModelRegistry(registry)

	t.Run("no restriction returns all active models with full capabilities", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		svc.handleListModels(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Object string `json:"object"`
			Data   []struct {
				ID           string                 `json:"id"`
				Object       string                 `json:"object"`
				OwnedBy      string                 `json:"owned_by"`
				Capabilities map[string]interface{} `json:"capabilities"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "list", resp.Object)
		require.Len(t, resp.Data, 2)
		assert.Equal(t, "claude-sonnet-4-6", resp.Data[0].ID)
		assert.Equal(t, "model", resp.Data[0].Object)
		assert.Equal(t, "woodgit", resp.Data[0].OwnedBy)
		assert.Equal(t, float64(200000), resp.Data[0].Capabilities["context_window"])
		assert.Equal(t, "gpt-4o", resp.Data[1].ID)
		assert.Equal(t, "woodgit", resp.Data[1].OwnedBy)
		assert.Equal(t, float64(128000), resp.Data[1].Capabilities["context_window"])
		assert.Equal(t, float64(8192), resp.Data[1].Capabilities["max_output_tokens"])
		assert.Equal(t, true, resp.Data[1].Capabilities["supports_thinking"])
		assert.Equal(t, true, resp.Data[1].Capabilities["supports_vision"])
		assert.Equal(t, []interface{}{"low", "medium", "high"}, resp.Data[1].Capabilities["reasoning_efforts"])
		assert.Equal(t, []interface{}{"default", "high"}, resp.Data[1].Capabilities["speed_tiers"])
	})

	t.Run("allowed_models filters to permitted models", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("allowed_models", []string{"claude-sonnet-4-6"})
		svc.handleListModels(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Data []struct {
				ID      string `json:"id"`
				OwnedBy string `json:"owned_by"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		assert.Equal(t, "claude-sonnet-4-6", resp.Data[0].ID)
		assert.Equal(t, "woodgit", resp.Data[0].OwnedBy)
	})

	t.Run("round_robin uses selected_models when set", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("allowed_models", []string{"gpt-4o", "claude-sonnet-4-6"})
		c.Set("selected_models", []string{"gpt-4o"})
		c.Set("model_routing_mode", "round_robin")
		svc.handleListModels(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		assert.Equal(t, "gpt-4o", resp.Data[0].ID)
	})

	t.Run("single mode uses current_model when set", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("allowed_models", []string{"gpt-4o", "claude-sonnet-4-6"})
		c.Set("current_model", "claude-sonnet-4-6")
		c.Set("model_routing_mode", "single")
		svc.handleListModels(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		assert.Equal(t, "claude-sonnet-4-6", resp.Data[0].ID)
	})

	t.Run("key_limits reflects subscription and group limits", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("allowed_models", []string{"gpt-4o"})
		c.Set("rate_limit_rpm", 60)
		c.Set("rate_limit_tpm", 100000)
		c.Set("sub_rpm_limit", 120)
		c.Set("sub_tpm_limit", 200000)
		c.Set("sub_concurrent_limit", 5)
		c.Set("group_rpm", 200)
		c.Set("group_tpm", 500000)
		svc.handleListModels(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			KeyLimits struct {
				Concurrent int `json:"concurrent"`
				RPM        int `json:"rpm"`
				TPM        int `json:"tpm"`
			} `json:"key_limits"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		assert.Equal(t, 5, resp.KeyLimits.Concurrent)
		// minPositive across keyRPM=60, subRPM=120, groupRPM=200 → 60
		assert.Equal(t, 60, resp.KeyLimits.RPM)
		// minPositive across keyTPM=100000, subTPM=200000, groupTPM=500000 → 100000
		assert.Equal(t, 100000, resp.KeyLimits.TPM)
	})
}

func TestContainsString(t *testing.T) {
	assert.True(t, containsString([]string{"a", "b", "c"}, "b"))
	assert.False(t, containsString([]string{"a", "b", "c"}, "d"))
	assert.False(t, containsString([]string{}, "a"))
	assert.False(t, containsString(nil, "a"))
}

func TestSmartRoute(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{EncryptionKey: "this-is-a-32-character-test-key!!"}}
	svc := NewHandlerService(cfg, noopLogger())
	svc.keyPoolManager = keypool.NewManager(cfg, zap.NewNop())

	now := time.Now()
	mk := func(id, provider, tier string, lastUsed time.Time) *keypool.CandidateKey {
		ki := &keypool.KeyInternal{Key: &keypool.Key{KeyStatic: keypool.KeyStatic{
			ID: id, Provider: provider, Tier: tier, Enabled: true,
		}}}
		ki.SetLastUsed(lastUsed)
		return &keypool.CandidateKey{Provider: provider, Key: ki}
	}

	t.Run("affinity key wins outright", func(t *testing.T) {
		cands := []*keypool.CandidateKey{
			mk("k1", "deepseek", "default", now.Add(-time.Hour)),
			mk("k2", "zimaocloud", "default", now), // most recent = last by LRU
		}
		got := svc.smartRoute(cands, "deepseek-v4-flash", "deepseek", "k2")
		assert.Equal(t, "k2", got[0].Key.ID, "affinity key must be pinned first regardless of LRU")
	})

	t.Run("tier beats LRU (no LRU override)", func(t *testing.T) {
		cands := []*keypool.CandidateKey{
			mk("k-eco", "deepseek", "economy", now.Add(-time.Hour)), // oldest, but economy tier
			mk("k-prem", "deepseek", "premium", now),                // newest, but premium tier
		}
		got := svc.smartRoute(cands, "deepseek-v4-flash", "deepseek", "")
		assert.Equal(t, "k-prem", got[0].Key.ID, "tier rank must dominate LRU")
	})

	t.Run("LRU is the final tie-breaker within same tier", func(t *testing.T) {
		cands := []*keypool.CandidateKey{
			mk("k-new", "deepseek", "default", now),
			mk("k-old", "deepseek", "default", now.Add(-time.Hour)),
		}
		got := svc.smartRoute(cands, "deepseek-v4-flash", "deepseek", "")
		assert.Equal(t, "k-old", got[0].Key.ID)
	})

	t.Run("affinity candidate missing falls back to composite order", func(t *testing.T) {
		cands := []*keypool.CandidateKey{
			mk("k-new", "deepseek", "default", now),
			mk("k-old", "deepseek", "default", now.Add(-time.Hour)),
		}
		got := svc.smartRoute(cands, "deepseek-v4-flash", "deepseek", "k-gone")
		assert.Equal(t, "k-old", got[0].Key.ID)
	})
}
