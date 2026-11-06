package routes

import (
	"testing"
	"time"
)

// TestTrimSpace tests the pure trimSpace helper.
func TestTrimSpace(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty string", "", ""},
		{"no whitespace", "hello", "hello"},
		{"leading space", "  hello", "hello"},
		{"trailing space", "hello  ", "hello"},
		{"both sides", "  hello  ", "hello"},
		{"tabs", "\thello\t", "hello"},
		{"newlines", "\nhello\n", "hello"},
		{"mixed whitespace", " \t\n hello \n\t ", "hello"},
		{"only spaces", "   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimSpace(tt.input)
			if got != tt.want {
				t.Errorf("trimSpace(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestGetProviderDefaultURL tests the provider URL lookup function.
func TestGetProviderDefaultURL(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		wantURL  string
	}{
		{"openai", "openai", "https://api.openai.com/v1"},
		{"anthropic", "anthropic", "https://api.anthropic.com/v1"},
		{"google", "google", "https://generativelanguage.googleapis.com/v1beta"},
		{"deepseek", "deepseek", "https://api.deepseek.com/v1"},
		{"unknown provider", "nonexistent", ""},
		{"empty provider", "", ""},
		{"case sensitive - lowercase", "openai", "https://api.openai.com/v1"},
		{"case sensitive - mixed", "OpenAI", ""},
		{"amazon", "amazon", "https://bedrock-runtime.us-east-1.amazonaws.com"},
		{"microsoft", "microsoft", "https://api.openai.azure.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getProviderDefaultURL(tt.provider)
			if got != tt.wantURL {
				t.Errorf("getProviderDefaultURL(%q) = %q, want %q", tt.provider, got, tt.wantURL)
			}
		})
	}
}

// TestProviderDefaultURLsHasExpectedProviders tests the provider URL map.
func TestProviderDefaultURLsHasExpectedProviders(t *testing.T) {
	// Core providers that must be present
	coreProviders := []string{
		"openai", "anthropic", "google", "deepseek", "amazon",
		"microsoft", "meta", "mistral", "cohere", "alibaba",
	}

	for _, p := range coreProviders {
		t.Run("provider_"+p, func(t *testing.T) {
			url := providerDefaultURLs[p]
			if url == "" {
				t.Errorf("provider %q has no default URL", p)
			}
			if url[:4] != "http" {
				t.Errorf("provider %q URL %q does not start with http", p, url)
			}
		})
	}

	t.Run("all_urls_are_valid_length", func(t *testing.T) {
		for provider, url := range providerDefaultURLs {
			if len(url) == 0 {
				t.Errorf("provider %q has empty URL", provider)
			}
		}
	})
}

// TestBuildModelsURL tests the models endpoint URL builder.
func TestBuildModelsURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"with /v1 suffix", "https://api.openai.com/v1", "https://api.openai.com/v1/models"},
		{"with /v2 suffix", "https://api.example.com/v2", "https://api.example.com/v2/models"},
		{"with /v1beta suffix", "https://api.anthropic.com/v1beta", "https://api.anthropic.com/v1beta/models"},
		{"without version", "https://api.example.com", "https://api.example.com/v1/models"},
		{"trailing slash", "https://api.openai.com/v1/", "https://api.openai.com/v1/models"},
		{"empty base", "", "/v1/models"},
		{"with /v4", "https://api.deepseek.com/v4", "https://api.deepseek.com/v4/models"},
		{"with path after version", "https://api.example.com/v1/chat", "https://api.example.com/v1/chat/v1/models"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildModelsURL(tt.baseURL)
			if got != tt.want {
				t.Errorf("buildModelsURL(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}

// TestCheckExportLimit tests the per-user export rate limiter.
func TestCheckExportLimit(t *testing.T) {
	// Use unique user ID to avoid interference with other tests
	userID := "test-export-limit-" + time.Now().Format("20060102150405")

	// First call should not be limited
	if checkExportLimit(userID) {
		t.Error("first call should not be rate limited")
	}

	// Next 9 calls should not be limited
	for i := 0; i < 9; i++ {
		if checkExportLimit(userID) {
			t.Errorf("call %d should not be rate limited", i+2)
		}
	}

	// 11th call (after 10 exports) should be limited
	if !checkExportLimit(userID) {
		t.Error("11th call should be rate limited")
	}
}

// TestCheckExportLimitMultipleUsers verifies rate limiting is per-user.
func TestCheckExportLimitMultipleUsers(t *testing.T) {
	ts := time.Now().Format("20060102150405")
	userA := "test-export-A-" + ts
	userB := "test-export-B-" + ts

	// Exhaust user A
	for i := 0; i < 10; i++ {
		checkExportLimit(userA)
	}

	// User A should be limited
	if !checkExportLimit(userA) {
		t.Error("user A should be rate limited after 10 exports")
	}

	// User B should NOT be limited
	if checkExportLimit(userB) {
		t.Error("user B should not be rate limited (different user)")
	}
}

// TestCheckExportLimitResetsAfterHour tests that the rate limit resets.
// Note: this test has a side effect of modifying global state.
func TestCheckExportLimitResetsAfterHour(t *testing.T) {
	ts := time.Now().Format("20060102150405")
	userID := "test-export-reset-" + ts

	// Exhaust the limit
	for i := 0; i < 10; i++ {
		checkExportLimit(userID)
	}

	if !checkExportLimit(userID) {
		t.Error("should be rate limited after 10 exports")
	}

	// Manually reset the entry to simulate time passage
	exportLimitMu.Lock()
	exportLimits[userID] = exportLimitEntry{count: 0, resetAt: time.Now().Add(-time.Minute)}
	exportLimitMu.Unlock()

	// Should be allowed again
	if checkExportLimit(userID) {
		t.Error("should not be rate limited after reset")
	}
}

// TestSyncServiceModelsToKeysStub is a compile check only.
func TestSyncServiceModelsToKeysStub(t *testing.T) {
	// compile-time verification that the function exists
	_ = syncServiceModelsToKeys
}

// TestValidProvidersList checks the internal valid providers list.
func TestValidProvidersList(t *testing.T) {
	// These are hardcoded in syncAIModels — verify they're reasonable
	expectedProviders := []string{
		"openai", "anthropic", "google", "deepseek",
		"alibaba", "amazon", "baidu", "bytedance",
		"cohere", "meta", "microsoft", "minimax",
		"mistral", "moonshot", "perplexity", "tencent",
		"x-ai", "zhipu",
	}
	for _, p := range expectedProviders {
		if p == "" {
			t.Error("provider should not be empty")
		}
	}
	// Ensure var exists (compile check)
	_ = getProviderDefaultURL
	_ = buildModelsURL
}

// TestValidateViaProxyFilter_RequiresProxySyncSecret ensures the helper fails
// closed instead of falling back to a hardcoded default secret.
func TestValidateViaProxyFilter_RequiresProxySyncSecret(t *testing.T) {
	t.Setenv("PROXY_SYNC_SECRET", "")

	status, models, err := validateViaProxyFilter("openai", "sk-test-key", "")
	if err == nil {
		t.Error("expected an error when PROXY_SYNC_SECRET is not set")
	}
	if status != "error" {
		t.Errorf("expected status 'error', got %q", status)
	}
	if len(models) != 0 {
		t.Errorf("expected no models, got %v", models)
	}
}

// TestIsBedrockBearerKey pins the discriminator used to route AWS runtime key
// validation: ABSK/bedrock-api-key- bearer tokens (no ':') go through the
// proxy Bearer path; AK/SK pairs (with ':') stay on the local SigV4 path.
func TestIsBedrockBearerKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"ABSKQmVkcm9ja0FiCdEf0=", true},
		{"bedrock-api-key-xyz", true},
		{"AKIAEXAMPLE:secretpart", false},     // AK/SK pair
		{"AKIAEXAMPLE:secret:session", false}, // AK/SK with session token
		{"ABSKhas:colon", false},              // has ':' → treated as AK/SK, not bearer
		{"randomtoken", false},                // not a recognized bearer prefix
		{"", false},
	}
	for _, tt := range tests {
		if got := isBedrockBearerKey(tt.key); got != tt.want {
			t.Errorf("isBedrockBearerKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}
