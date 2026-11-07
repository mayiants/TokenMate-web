package models

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/pkg/telemetry"
	"go.uber.org/zap"
)

// ServiceModel represents a model configuration from Portal
type ServiceModel struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	ProductID    string `json:"productId"`
	APIModelName string `json:"apiModelName"`
	Status       string `json:"status"`

	// Pricing
	InputPrice          float64 `json:"inputPrice"`
	OutputPrice         float64 `json:"outputPrice"`
	InputPriceCacheHit  float64 `json:"inputPriceCacheHit"`
	InputPriceCacheMiss float64 `json:"inputPriceCacheMiss"`
	ReasoningPrice      float64 `json:"reasoningPrice"`
	CacheReadPrice      float64 `json:"cacheReadPrice"`

	// Capabilities exposed to downstream clients (context window, vision, etc.)
	Capabilities ModelCapabilities `json:"capabilities"`

	// Validation
	ValidationStatus string                 `json:"validationStatus"`
	ValidationResult map[string]interface{} `json:"validation_result"`
	ValidatedAt      time.Time              `json:"validated_at"`
	SuggestedAPIName string                 `json:"suggested_api_name"`

	// Routing
	AvgLatencyMs int     `json:"avgLatencyMs"`
	SuccessRate  float64 `json:"successRate"`
	Priority     int     `json:"priority"`

	// Provider aliases for third-party platforms
	ProviderAliases map[string]string `json:"providerAliases"`

	ShowInStore bool      `json:"showInStore"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ModelCapabilities describes model abilities returned on /v1/models.
// Keep field names stable: they are part of the downstream API contract.
type ModelCapabilities struct {
	ContextWindow             int      `json:"context_window"`
	MaxOutputTokens           int      `json:"max_output_tokens"`
	MaxInputTokens            int      `json:"max_input_tokens"`
	SupportsThinking          bool     `json:"supports_thinking"`
	ReasoningEfforts          []string `json:"reasoning_efforts"`
	SpeedTiers                []string `json:"speed_tiers"`
	SupportsVision            bool     `json:"supports_vision"`
	SupportsAudio             bool     `json:"supports_audio"`
	SupportsToolCalls         bool     `json:"supports_tool_calls"`
	SupportsJSONMode          bool     `json:"supports_json_mode"`
	SupportsParallelToolCalls bool     `json:"supports_parallel_tool_calls"`
	SupportsStreaming         bool     `json:"supports_streaming"`
}

// ModelRegistry manages service models synced from Portal
type ModelRegistry struct {
	cfg    *config.Config
	logger *zap.Logger

	// Model cache: provider -> product_id -> ServiceModel
	models   map[string]map[string]*ServiceModel
	modelsMu sync.RWMutex

	// Provider -> model list for quick lookup
	providerModels map[string][]string

	// Portal sync
	portalURL    string
	portalSecret string
	syncInterval time.Duration
	lastSync     time.Time

	// HTTP client
	client *http.Client
}

// NewModelRegistry creates a new model registry
func NewModelRegistry(cfg *config.Config, logger *zap.Logger) *ModelRegistry {
	r := &ModelRegistry{
		cfg:            cfg,
		logger:         logger,
		models:         make(map[string]map[string]*ServiceModel),
		providerModels: make(map[string][]string),
		portalURL:      cfg.Portal.URL,
		portalSecret:   cfg.Portal.SyncSecret,
		syncInterval:   cfg.Portal.SyncInterval,
		client:         &http.Client{Timeout: 30 * time.Second},
	}

	return r
}

// SyncFromPortal syncs service models from Portal
func (r *ModelRegistry) SyncFromPortal(ctx context.Context) error {
	if r.portalURL == "" || r.portalSecret == "" {
		r.logger.Warn("Portal URL or secret not configured, skipping model sync")
		return nil
	}

	url := r.portalURL + "/api/admin/models/sync"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("x-proxy-secret", r.portalSecret)
	telemetry.InjectTraceparent(ctx, req)

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("portal request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	var result struct {
		Models []ServiceModel `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	// Update cache
	r.updateModels(result.Models)

	r.lastSync = time.Now()
	r.logger.Info("Synced service models from portal",
		zap.Int("total_models", len(result.Models)),
		zap.Int("providers", len(r.providerModels)),
	)

	return nil
}

// GetLastSync returns the timestamp of the last successful Portal sync.
func (r *ModelRegistry) GetLastSync() time.Time {
	return r.lastSync
}

// updateModels updates the internal model cache
func (r *ModelRegistry) updateModels(models []ServiceModel) {
	r.modelsMu.Lock()
	defer r.modelsMu.Unlock()

	// Clear and rebuild
	r.models = make(map[string]map[string]*ServiceModel)
	r.providerModels = make(map[string][]string)

	for _, m := range models {
		// Initialize provider map if needed
		if r.models[m.Provider] == nil {
			r.models[m.Provider] = make(map[string]*ServiceModel)
		}

		// Store model
		r.models[m.Provider][m.ProductID] = &m

		// Add to provider list
		r.providerModels[m.Provider] = append(r.providerModels[m.Provider], m.ProductID)
	}
}

// GetModel returns a specific model by provider and product_id
func (r *ModelRegistry) GetModel(provider, productID string) (*ServiceModel, bool) {
	r.modelsMu.RLock()
	defer r.modelsMu.RUnlock()

	if r.models[provider] == nil {
		return nil, false
	}

	model, ok := r.models[provider][productID]
	return model, ok
}

// GetModelByAPIName returns a model by provider and API model name
func (r *ModelRegistry) GetModelByAPIName(provider, apiModelName string) (*ServiceModel, bool) {
	r.modelsMu.RLock()
	defer r.modelsMu.RUnlock()

	if r.models[provider] == nil {
		return nil, false
	}

	for _, model := range r.models[provider] {
		if model.APIModelName == apiModelName {
			return model, true
		}
	}

	return nil, false
}

// GetProviderModels returns all models for a provider
func (r *ModelRegistry) GetProviderModels(provider string) []string {
	r.modelsMu.RLock()
	defer r.modelsMu.RUnlock()

	return r.providerModels[provider]
}

// GetAllProviders returns all providers with active models
func (r *ModelRegistry) GetAllProviders() []string {
	r.modelsMu.RLock()
	defer r.modelsMu.RUnlock()

	providers := make([]string, 0, len(r.providerModels))
	for p := range r.providerModels {
		providers = append(providers, p)
	}
	return providers
}

// ListModels returns a snapshot of all active service models.
// The returned slice is a copy; callers may filter freely.
func (r *ModelRegistry) ListModels() []*ServiceModel {
	r.modelsMu.RLock()
	defer r.modelsMu.RUnlock()

	models := make([]*ServiceModel, 0)
	for _, byProduct := range r.models {
		for _, m := range byProduct {
			if m.Status != "active" {
				continue
			}
			// Return a shallow copy so callers can't mutate cached values.
			copy := *m
			models = append(models, &copy)
		}
	}
	return models
}

// GetModelPrice returns the input and output price for a model
func (r *ModelRegistry) GetModelPrice(provider, productID string) (inputPrice, outputPrice float64, ok bool) {
	model, ok := r.GetModel(provider, productID)
	if !ok {
		return 0, 0, false
	}
	return model.InputPrice, model.OutputPrice, true
}

// ResolveModel resolves a model request to the correct provider and API model name
// Returns: provider, apiModelName, price, found
func (r *ModelRegistry) ResolveModel(requestedModel string) (provider, apiModelName string, inputPrice, outputPrice float64, found bool) {
	r.modelsMu.RLock()
	defer r.modelsMu.RUnlock()

	// Search across all providers
	for p, models := range r.models {
		for productID, model := range models {
			// Match by product_id or api_model_name
			if productID == requestedModel || model.APIModelName == requestedModel {
				return p, model.APIModelName, model.InputPrice, model.OutputPrice, true
			}
		}
	}

	return "", "", 0, 0, false
}

// StartSyncLoop starts periodic sync with Portal
func (r *ModelRegistry) StartSyncLoop(ctx context.Context) {
	if r.syncInterval == 0 {
		r.syncInterval = 60 * time.Second
	}

	go func() {
		ticker := time.NewTicker(r.syncInterval)
		defer ticker.Stop()

		// Initial sync
		initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := r.SyncFromPortal(initCtx); err != nil {
			r.logger.Error("Initial model sync failed", zap.Error(err))
		}
		cancel()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
				if err := r.SyncFromPortal(syncCtx); err != nil {
					r.logger.Error("Model sync failed", zap.Error(err))
				}
				syncCancel()
			}
		}
	}()
}

// GetStats returns registry statistics
func (r *ModelRegistry) GetStats() map[string]interface{} {
	r.modelsMu.RLock()
	defer r.modelsMu.RUnlock()

	totalModels := 0
	for _, models := range r.models {
		totalModels += len(models)
	}

	return map[string]interface{}{
		"total_models":   totalModels,
		"providers":      len(r.providerModels),
		"last_sync":      r.lastSync,
		"sync_interval":  r.syncInterval.String(),
		"provider_stats": r.providerModels,
	}
}
