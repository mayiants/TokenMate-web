package relay

import (
	"testing"

	"github.com/ai-proxy/ai-proxy-go/pkg/relay/base"
)

func TestGetProviderFromModel(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		wantProvider string
	}{
		// OpenAI models (Company: OpenAI)
		{"gpt-4", "gpt-4", "openai"},
		{"gpt-4o", "gpt-4o", "openai"},
		{"gpt-4-turbo", "gpt-4-turbo", "openai"},
		{"gpt-3.5-turbo", "gpt-3.5-turbo", "openai"},
		{"o1-preview", "o1-preview", "openai"},
		{"o3-mini", "o3-mini", "openai"},
		{"dall-e-3", "dall-e-3", "openai"},

		// Anthropic models (Company: Anthropic, Product: Claude)
		{"claude-3-opus", "claude-3-opus-20240229", "anthropic"},
		{"claude-3-sonnet", "claude-3-sonnet-20240229", "anthropic"},
		{"claude-3-haiku", "claude-3-haiku-20240307", "anthropic"},
		{"claude-2", "claude-2.1", "anthropic"},

		// Google models (Company: Google, Product: Gemini)
		{"gemini-pro", "gemini-pro", "google"},
		{"gemini-1.5-pro", "gemini-1.5-pro", "google"},
		{"gemini-1.5-flash", "gemini-1.5-flash", "google"},

		// DeepSeek models (Company: DeepSeek, Product: DeepSeek)
		{"deepseek-chat", "deepseek-chat", "deepseek"},
		{"deepseek-coder", "deepseek-coder", "deepseek"},

		// Alibaba models (Company: Alibaba, Product: Qwen)
		{"qwen-turbo", "qwen-turbo", "alibaba"},
		{"qwen-plus", "qwen-plus", "alibaba"},
		{"qwen-max", "qwen-max", "alibaba"},

		// Moonshot models (Company: Moonshot, Product: Kimi)
		{"moonshot-v1", "moonshot-v1-8k", "moonshot"},
		{"kimi", "kimi-chat", "moonshot"},

		// Zhipu models (Company: Zhipu, Product: GLM)
		{"glm-4", "glm-4", "zhipu"},
		{"glm-3-turbo", "glm-3-turbo", "zhipu"},

		// Unknown defaults to openai
		{"unknown", "unknown-model", "openai"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetProviderFromModel(tt.model)
			if got != tt.wantProvider {
				t.Errorf("GetProviderFromModel(%v) = %v, want %v", tt.model, got, tt.wantProvider)
			}
		})
	}
}

func TestGetAdaptor(t *testing.T) {
	cfg := &base.AdaptorConfig{}

	// Test that only company names are valid provider names
	tests := []struct {
		name     string
		provider string
		wantNil  bool
	}{
		// Valid company names
		{"openai", "openai", false},
		{"azure", "azure", false},
		{"anthropic", "anthropic", false},
		{"google", "google", false},
		{"deepseek", "deepseek", false},
		{"alibaba", "alibaba", false},
		{"moonshot", "moonshot", false},
		{"zhipu", "zhipu", false},
		{"meta", "meta", false},
		{"mistral", "mistral", false},
		// Unknown provider gets OpenAI-compatible adapter
		{"unknown", "unknown-provider", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptor := GetAdaptor(tt.provider, cfg)
			if (adaptor == nil) != tt.wantNil {
				t.Errorf("GetAdaptor(%v) nil = %v, want %v", tt.provider, adaptor == nil, tt.wantNil)
			}
			if adaptor != nil && adaptor.GetProviderName() == "" {
				t.Errorf("GetAdaptor(%v).GetProviderName() returned empty string", tt.provider)
			}
		})
	}
}

func TestProviderConfig(t *testing.T) {
	// Test GetProviderConfig - should use company names
	cfg, ok := GetProviderConfig("openai")
	if !ok {
		t.Error("GetProviderConfig(openai) returned not ok")
	}
	if cfg.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("OpenAI BaseURL = %v, want https://api.openai.com/v1", cfg.BaseURL)
	}

	// Anthropic (not "claude")
	cfg, ok = GetProviderConfig("anthropic")
	if !ok {
		t.Error("GetProviderConfig(anthropic) returned not ok")
	}
	if cfg.BaseURL != "https://api.anthropic.com/v1" {
		t.Errorf("Anthropic BaseURL = %v, want https://api.anthropic.com/v1", cfg.BaseURL)
	}

	// Google (not "gemini")
	cfg, ok = GetProviderConfig("google")
	if !ok {
		t.Error("GetProviderConfig(google) returned not ok")
	}
	if cfg.BaseURL != "https://generativelanguage.googleapis.com/v1beta" {
		t.Errorf("Google BaseURL = %v, want https://generativelanguage.googleapis.com/v1beta", cfg.BaseURL)
	}

	// Alibaba (not "qwen")
	cfg, ok = GetProviderConfig("alibaba")
	if !ok {
		t.Error("GetProviderConfig(alibaba) returned not ok")
	}

	// Test unknown provider — returns OpenAI-compatible config (for third-party providers like accessapi)
	cfg, ok = GetProviderConfig("unsupported")
	if !ok {
		t.Error("GetProviderConfig(unsupported) should return ok (OpenAI-compatible fallback)")
	}
	if cfg.AuthType != "bearer" {
		t.Error("GetProviderConfig(unsupported) should return bearer auth type")
	}
}

func TestSupportedProviders(t *testing.T) {
	providers := SupportedProviders()
	if len(providers) == 0 {
		t.Error("SupportedProviders() returned empty list")
	}

	// Check that company names are included (not product names)
	companyProviders := []string{"openai", "anthropic", "google", "deepseek", "alibaba", "moonshot", "zhipu", "meta", "mistral"}
	for _, cp := range companyProviders {
		found := false
		for _, p := range providers {
			if p == cp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("SupportedProviders() missing company %v", cp)
		}
	}

	// Product names should NOT be in the provider list
	productNames := []string{"claude", "gemini", "qwen", "kimi", "glm"}
	for _, pn := range productNames {
		for _, p := range providers {
			if p == pn {
				t.Errorf("SupportedProviders() should not contain product name %v (use company name instead)", pn)
			}
		}
	}
}

func TestIsProviderSupported(t *testing.T) {
	// Company names should be supported
	if !IsProviderSupported("openai") {
		t.Error("IsProviderSupported(openai) should be true")
	}
	if !IsProviderSupported("anthropic") {
		t.Error("IsProviderSupported(anthropic) should be true")
	}
	if !IsProviderSupported("google") {
		t.Error("IsProviderSupported(google) should be true")
	}

	// Product names should NOT be supported as providers
	if IsProviderSupported("claude") {
		t.Error("IsProviderSupported(claude) should be false - use 'anthropic' instead")
	}
	if IsProviderSupported("gemini") {
		t.Error("IsProviderSupported(gemini) should be false - use 'google' instead")
	}
	if IsProviderSupported("qwen") {
		t.Error("IsProviderSupported(qwen) should be false - use 'alibaba' instead")
	}

	// Unknown provider
	if IsProviderSupported("nonexistent") {
		t.Error("IsProviderSupported(nonexistent) should be false")
	}
}

func TestGetRelayFormatFromProvider(t *testing.T) {
	tests := []struct {
		provider string
		format   string
	}{
		{"openai", "openai"},
		{"anthropic", "claude"},
		{"google", "gemini"},
		{"deepseek", "openai"},
		{"alibaba", "openai"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			format := GetRelayFormatFromProvider(tt.provider)
			if string(format) != tt.format {
				t.Errorf("GetRelayFormatFromProvider(%v) = %v, want %v", tt.provider, format, tt.format)
			}
		})
	}
}
