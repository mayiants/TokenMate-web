package routes

import (
	"slices"
	"testing"
)

// TestGenerateAPIModelName tests the generateAPIModelName function.
func TestGenerateAPIModelName(t *testing.T) {
	tests := []struct {
		name      string
		productID string
		want      string
	}{
		{"simple id", "gpt-4o", "gpt-4o"},
		{"multi segment", "Pro/moonshotai/Kimi-K2.6", "kimi-k2.6"},
		{"two segment", "deepseek-ai/DeepSeek-V4-Flash", "deepseek-v4-flash"},
		{"single segment", "claude-3-opus", "claude-3-opus"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateAPIModelName(tt.productID)
			if got != tt.want {
				t.Errorf("generateAPIModelName(%q) = %q, want %q", tt.productID, got, tt.want)
			}
		})
	}
}

// TestGenerateBedrockAPIModelName tests the Bedrock-specific API model name generation.
func TestGenerateBedrockAPIModelName(t *testing.T) {
	tests := []struct {
		name      string
		productID string
		want      string
	}{
		{"anthropic claude strip prefix", "anthropic.claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"amazon nova strip prefix", "amazon.nova-lite-v1:0", "nova-lite-v1:0"},
		{"deepseek keep full", "deepseek.v3.1", "deepseek.v3.1"},
		{"qwen keep full", "qwen.qwen3-32b", "qwen.qwen3-32b"},
		{"no dot no strip", "claude-opus-4-6", "claude-opus-4-6"},
		{"unknown provider keep full", "mistral.large", "mistral.large"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateBedrockAPIModelName(tt.productID)
			if got != tt.want {
				t.Errorf("generateBedrockAPIModelName(%q) = %q, want %q", tt.productID, got, tt.want)
			}
		})
	}
}

// TestAwsInfraProviders tests the known AWS infrastructure providers.
func TestAwsInfraProviders(t *testing.T) {
	tests := []struct {
		provider  string
		wantInfra bool
	}{
		{"amazon", true},
		{"anthropic", true},
		{"deepseek", false},
		{"qwen", false},
		{"mistral", false},
		{"openai", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := awsInfraProviders[tt.provider]
			if got != tt.wantInfra {
				t.Errorf("awsInfraProviders[%q] = %v, want %v", tt.provider, got, tt.wantInfra)
			}
		})
	}
}

// TestModelStatusValues tests valid model status values.
func TestModelStatusValues(t *testing.T) {
	validStatuses := map[string]bool{
		"active":       true,
		"pending":      true,
		"eol_pending":  true,
		"eol":          true,
		"discontinued": true,
		"ignored":      true,
		"paused":       true,
	}

	tests := []struct {
		status    string
		wantValid bool
	}{
		{"active", true},
		{"pending", true},
		{"eol_pending", true},
		{"eol", true},
		{"discontinued", true},
		{"ignored", true},
		{"paused", true},
		{"unknown", false},
		{"", false},
		{"deleted", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			valid := validStatuses[tt.status]
			if valid != tt.wantValid {
				t.Errorf("model status %q valid = %v, want %v", tt.status, valid, tt.wantValid)
			}
		})
	}
}

// TestModelRoutes_TypeCheck ensures the ModelRoutes type is properly defined.
func TestModelRoutes_TypeCheck(t *testing.T) {
	var _ *ModelRoutes = nil
	t.Log("ModelRoutes type verified at compile time")
}

// TestFilterSelectedModels tests restricting a fetched model list to an admin selection.
func TestFilterSelectedModels(t *testing.T) {
	tests := []struct {
		name     string
		fetched  []string
		selected []string
		want     []string
	}{
		{"nil selection keeps all", []string{"a", "b"}, nil, []string{"a", "b"}},
		{"empty selection keeps all", []string{"a", "b"}, []string{}, []string{"a", "b"}},
		{"subset preserves fetched order", []string{"a", "b", "c"}, []string{"c", "a"}, []string{"a", "c"}},
		{"unknown ids ignored", []string{"a", "b"}, []string{"z"}, []string{}},
		{"duplicates collapse", []string{"a", "b"}, []string{"b", "b"}, []string{"b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterSelectedModels(tt.fetched, tt.selected)
			if !slices.Equal(got, tt.want) {
				t.Errorf("filterSelectedModels(%v, %v) = %v, want %v", tt.fetched, tt.selected, got, tt.want)
			}
		})
	}
}
