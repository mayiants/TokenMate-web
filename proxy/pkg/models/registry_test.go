package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"go.uber.org/zap"
)

func TestNewModelRegistry(t *testing.T) {
	cfg := &config.Config{
		Portal: config.PortalConfig{
			URL:          "http://localhost:3001",
			SyncSecret:   "test-secret",
			SyncInterval: 60 * time.Second,
		},
	}

	logger, _ := zap.NewDevelopment()
	r := NewModelRegistry(cfg, logger)

	if r == nil {
		t.Fatal("NewModelRegistry returned nil")
	}

	if r.portalURL != cfg.Portal.URL {
		t.Errorf("portalURL = %s, want %s", r.portalURL, cfg.Portal.URL)
	}
}

func TestSyncFromPortal(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Header.Get("x-proxy-secret") != "test-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Return mock models
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"models": [
				{
					"id": "model-1",
					"provider": "openai",
					"productId": "gpt-4o",
					"apiModelName": "gpt-4o",
					"status": "active",
					"inputPrice": 2.5,
					"outputPrice": 10.0,
					"showInStore": true
				},
				{
					"id": "model-2",
					"provider": "anthropic",
					"productId": "claude-sonnet-4.5",
					"apiModelName": "claude-sonnet-4-5-20250514",
					"status": "active",
					"inputPrice": 3.0,
					"outputPrice": 15.0,
					"showInStore": true
				}
			]
		}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		Portal: config.PortalConfig{
			URL:          server.URL,
			SyncSecret:   "test-secret",
			SyncInterval: 60 * time.Second,
		},
	}

	logger, _ := zap.NewDevelopment()
	r := NewModelRegistry(cfg, logger)

	// Sync
	err := r.SyncFromPortal(context.Background())
	if err != nil {
		t.Fatalf("SyncFromPortal failed: %v", err)
	}

	// Verify models
	model, ok := r.GetModel("openai", "gpt-4o")
	if !ok {
		t.Fatal("Expected to find openai/gpt-4o")
	}

	if model.InputPrice != 2.5 {
		t.Errorf("InputPrice = %f, want 2.5", model.InputPrice)
	}

	if model.APIModelName != "gpt-4o" {
		t.Errorf("APIModelName = %s, want gpt-4o", model.APIModelName)
	}

	// Test GetModelByAPIName
	model2, ok := r.GetModelByAPIName("anthropic", "claude-sonnet-4-5-20250514")
	if !ok {
		t.Fatal("Expected to find anthropic model by API name")
	}

	if model2.ProductID != "claude-sonnet-4.5" {
		t.Errorf("ProductID = %s, want claude-sonnet-4.5", model2.ProductID)
	}
}

func TestResolveModel(t *testing.T) {
	// Create registry with test data
	cfg := &config.Config{}
	logger, _ := zap.NewDevelopment()
	r := NewModelRegistry(cfg, logger)

	// Manually add test models
	r.updateModels([]ServiceModel{
		{
			Provider:     "openai",
			ProductID:    "gpt-4o",
			APIModelName: "gpt-4o",
			InputPrice:   2.5,
			OutputPrice:  10.0,
		},
		{
			Provider:     "anthropic",
			ProductID:    "claude-sonnet-4.5",
			APIModelName: "claude-sonnet-4-5-20250514",
			InputPrice:   3.0,
			OutputPrice:  15.0,
		},
	})

	tests := []struct {
		requestedModel  string
		wantProvider    string
		wantAPIName     string
		wantInputPrice  float64
		wantOutputPrice float64
		wantFound       bool
	}{
		{
			requestedModel:  "gpt-4o",
			wantProvider:    "openai",
			wantAPIName:     "gpt-4o",
			wantInputPrice:  2.5,
			wantOutputPrice: 10.0,
			wantFound:       true,
		},
		{
			requestedModel:  "claude-sonnet-4-5-20250514",
			wantProvider:    "anthropic",
			wantAPIName:     "claude-sonnet-4-5-20250514",
			wantInputPrice:  3.0,
			wantOutputPrice: 15.0,
			wantFound:       true,
		},
		{
			requestedModel: "unknown-model",
			wantFound:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.requestedModel, func(t *testing.T) {
			provider, apiName, inPrice, outPrice, found := r.ResolveModel(tt.requestedModel)

			if found != tt.wantFound {
				t.Errorf("ResolveModel() found = %v, want %v", found, tt.wantFound)
			}

			if tt.wantFound {
				if provider != tt.wantProvider {
					t.Errorf("provider = %s, want %s", provider, tt.wantProvider)
				}
				if apiName != tt.wantAPIName {
					t.Errorf("apiName = %s, want %s", apiName, tt.wantAPIName)
				}
				if inPrice != tt.wantInputPrice {
					t.Errorf("inputPrice = %f, want %f", inPrice, tt.wantInputPrice)
				}
				if outPrice != tt.wantOutputPrice {
					t.Errorf("outputPrice = %f, want %f", outPrice, tt.wantOutputPrice)
				}
			}
		})
	}
}

func TestGetProviderModels(t *testing.T) {
	cfg := &config.Config{}
	logger, _ := zap.NewDevelopment()
	r := NewModelRegistry(cfg, logger)

	r.updateModels([]ServiceModel{
		{Provider: "openai", ProductID: "gpt-4o"},
		{Provider: "openai", ProductID: "gpt-5"},
		{Provider: "anthropic", ProductID: "claude-sonnet-4.5"},
	})

	openaiModels := r.GetProviderModels("openai")
	if len(openaiModels) != 2 {
		t.Errorf("GetProviderModels(openai) returned %d models, want 2", len(openaiModels))
	}

	anthropicModels := r.GetProviderModels("anthropic")
	if len(anthropicModels) != 1 {
		t.Errorf("GetProviderModels(anthropic) returned %d models, want 1", len(anthropicModels))
	}

	unknownModels := r.GetProviderModels("unknown")
	if len(unknownModels) != 0 {
		t.Errorf("GetProviderModels(unknown) returned %d models, want 0", len(unknownModels))
	}
}

func TestGetAllProviders(t *testing.T) {
	cfg := &config.Config{}
	logger, _ := zap.NewDevelopment()
	r := NewModelRegistry(cfg, logger)

	r.updateModels([]ServiceModel{
		{Provider: "openai", ProductID: "gpt-4o"},
		{Provider: "anthropic", ProductID: "claude-sonnet-4.5"},
		{Provider: "google", ProductID: "gemini-2.5-pro"},
	})

	providers := r.GetAllProviders()
	if len(providers) != 3 {
		t.Errorf("GetAllProviders returned %d providers, want 3", len(providers))
	}
}

func TestSyncFromPortalUnauthorized(t *testing.T) {
	// Create mock server that rejects unauthorized requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin-Secret") != "correct-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"models": []}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		Portal: config.PortalConfig{
			URL:        server.URL,
			SyncSecret: "wrong-secret",
		},
	}

	logger, _ := zap.NewDevelopment()
	r := NewModelRegistry(cfg, logger)

	err := r.SyncFromPortal(context.Background())
	if err == nil {
		t.Error("Expected error for unauthorized request")
	}
}

func TestGetStats(t *testing.T) {
	cfg := &config.Config{}
	logger, _ := zap.NewDevelopment()
	r := NewModelRegistry(cfg, logger)

	r.updateModels([]ServiceModel{
		{Provider: "openai", ProductID: "gpt-4o"},
		{Provider: "anthropic", ProductID: "claude-sonnet-4.5"},
	})

	stats := r.GetStats()

	totalModels, ok := stats["total_models"].(int)
	if !ok || totalModels != 2 {
		t.Errorf("total_models = %v, want 2", stats["total_models"])
	}

	providers, ok := stats["providers"].(int)
	if !ok || providers != 2 {
		t.Errorf("providers = %v, want 2", stats["providers"])
	}
}
