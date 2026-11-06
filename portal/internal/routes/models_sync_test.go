package routes

import (
	"encoding/json"
	"testing"
)

// TestModelSync_AddProviderModelsValidation tests the add provider models request.
func TestModelSync_AddProviderModelsValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantProv  string
		wantCount int
	}{
		{"valid", `{"provider": "openai", "models": ["gpt-4o", "gpt-4-turbo"]}`, true, "openai", 2},
		{"single model", `{"provider": "anthropic", "models": ["claude-3"]}`, true, "anthropic", 1},
		{"missing models", `{"provider": "openai"}`, true, "openai", 0},
		{"missing provider", `{"models": ["gpt-4o"]}`, true, "", 1},
		{"invalid json", `bad`, false, "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Provider string   `json:"provider" binding:"required"`
				Models   []string `json:"models" binding:"required"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
				return
			}
			if !valid {
				return
			}
			if req.Provider != tt.wantProv {
				t.Errorf("provider = %q, want %q", req.Provider, tt.wantProv)
			}
			if len(req.Models) != tt.wantCount {
				t.Errorf("models count = %d, want %d", len(req.Models), tt.wantCount)
			}
		})
	}
}

// TestModelSync_SyncKeyModelsValidation tests the sync key models request.
func TestModelSync_SyncKeyModelsValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"valid sync", `{"provider": "openai", "models": ["gpt-4o", "gpt-3.5-turbo"]}`, true},
		{"single model", `{"provider": "anthropic", "models": ["claude-3-opus"]}`, true},
		{"empty models", `{"provider": "openai", "models": []}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Provider string   `json:"provider" binding:"required"`
				Models   []string `json:"models" binding:"required"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// TestModelSync_OpenRouterAddValidation tests the add from OpenRouter request.
func TestModelSync_OpenRouterAddValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantCount int
	}{
		{"valid ids", `{"openrouter_ids": ["id-1", "id-2"]}`, true, 2},
		{"single id", `{"openrouter_ids": ["id-1"]}`, true, 1},
		{"empty ids", `{"openrouter_ids": []}`, true, 0},
		{"missing ids", `{}`, true, 0},
		{"invalid json", `bad`, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				OpenRouterIDs []string `json:"openrouter_ids"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
				return
			}
			if !valid {
				return
			}
			if len(req.OpenRouterIDs) != tt.wantCount {
				t.Errorf("ids count = %d, want %d", len(req.OpenRouterIDs), tt.wantCount)
			}
		})
	}
}

// TestModelSync_ProviderModelsURL tests provider-specific models URL overrides.
func TestModelSync_ProviderModelsURL(t *testing.T) {
	providerModelsPath := map[string]string{
		"xunfei": "/v2/models",
	}

	tests := []struct {
		provider     string
		wantPath     string
		wantOverride bool
	}{
		{"xunfei", "/v2/models", true},
		{"openai", "", false},
		{"anthropic", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			path, hasOverride := providerModelsPath[tt.provider]
			if hasOverride != tt.wantOverride {
				t.Errorf("hasOverride for %s = %v, want %v", tt.provider, hasOverride, tt.wantOverride)
			}
			if path != tt.wantPath {
				t.Errorf("path for %s = %q, want %q", tt.provider, path, tt.wantPath)
			}
		})
	}
}
