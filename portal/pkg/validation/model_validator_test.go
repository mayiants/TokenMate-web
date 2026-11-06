package validation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// TestGenerateAPINameCandidates tests the candidate generation logic.
func TestGenerateAPINameCandidates(t *testing.T) {
	tests := []struct {
		name        string
		productID   string
		contains    []string
		notContains []string
	}{
		{
			name:      "simple name",
			productID: "gpt-4",
			contains:  []string{"gpt-4", "gpt.4", "gpt-4-latest"},
		},
		{
			name:      "versioned name",
			productID: "claude-3-opus",
			contains:  []string{"claude-3-opus", "claude.3.opus", "claude-3-opus-latest", "claude-3"},
		},
		{
			name:      "already has latest",
			productID: "model-latest",
			contains:  []string{"model-latest"},
		},
		{
			name:      "single segment",
			productID: "gpt4",
			contains:  []string{"gpt4", "gpt4-latest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := GenerateAPINameCandidates(tt.productID)

			for _, want := range tt.contains {
				found := false
				for _, c := range candidates {
					if c == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("GenerateAPINameCandidates(%q) missing candidate %q", tt.productID, want)
				}
			}

			for _, dontWant := range tt.notContains {
				for _, c := range candidates {
					if c == dontWant {
						t.Errorf("GenerateAPINameCandidates(%q) should not contain %q", tt.productID, dontWant)
					}
				}
			}
		})
	}
}

// TestGenerateAPINameCandidatesNoDuplicates verifies no duplicate candidates.
func TestGenerateAPINameCandidatesNoDuplicates(t *testing.T) {
	tests := []string{"gpt-4", "claude-3.5-sonnet", "kimi-k2-5", "deepseek-chat"}

	for _, productID := range tests {
		t.Run(productID, func(t *testing.T) {
			candidates := GenerateAPINameCandidates(productID)
			seen := make(map[string]bool)
			for _, c := range candidates {
				if seen[c] {
					t.Errorf("duplicate candidate %q for productID %q", c, productID)
				}
				seen[c] = true
			}
		})
	}
}

// TestGenerateAPINameCandidatesNotEmpty verifies the function always returns at least the original.
func TestGenerateAPINameCandidatesNotEmpty(t *testing.T) {
	for _, pid := range []string{"a", "ab", "abc", "", "x-y"} {
		t.Run(pid, func(t *testing.T) {
			candidates := GenerateAPINameCandidates(pid)
			if len(candidates) == 0 {
				t.Errorf("GenerateAPINameCandidates(%q) returned empty slice", pid)
			}
			if candidates[0] != pid {
				t.Errorf("first candidate should be the original productID, got %q", candidates[0])
			}
		})
	}
}

// TestMapResultToStatus tests the status mapping function.
func TestMapResultToStatus(t *testing.T) {
	tests := []struct {
		name       string
		result     *ValidationResult
		wantStatus string
	}{
		{
			name: "available with same name",
			result: &ValidationResult{
				Available:     true,
				APIModelName:  "gpt-4",
				SuggestedName: "gpt-4",
			},
			wantStatus: "valid",
		},
		{
			name: "available with better name",
			result: &ValidationResult{
				Available:     true,
				APIModelName:  "gpt-4-v1",
				SuggestedName: "gpt-4",
			},
			wantStatus: "needs_review",
		},
		{
			name: "bedrock product id tested ok despite different api name",
			result: &ValidationResult{
				Provider:      "amazon-us-east-1",
				ProductID:     "us.anthropic.claude-haiku-4-5-20251001-v1:0",
				APIModelName:  "claude-haiku-4-5",
				Available:     true,
				SuggestedName: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
			},
			wantStatus: "valid",
		},
		{
			name: "bedrock different candidate still needs review",
			result: &ValidationResult{
				Provider:      "amazon-us-east-1",
				ProductID:     "anthropic.claude-haiku-4-5-20251001-v1:0",
				APIModelName:  "anthropic.claude-haiku-4-5-20251001-v1:0",
				Available:     true,
				SuggestedName: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
			},
			wantStatus: "needs_review",
		},
		{
			name: "mantle is not treated as bedrock",
			result: &ValidationResult{
				Provider:      "amazon-mantle-us-west-2",
				ProductID:     "some-model",
				APIModelName:  "some-model",
				Available:     true,
				SuggestedName: "other-name",
			},
			wantStatus: "needs_review",
		},
		{
			name: "not available",
			result: &ValidationResult{
				Available: false,
			},
			wantStatus: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapResultToStatus(tt.result)
			if got != tt.wantStatus {
				t.Errorf("mapResultToStatus() = %q, want %q", got, tt.wantStatus)
			}
		})
	}
}

// TestNewModelValidator tests creation.
func TestNewModelValidator(t *testing.T) {
	v := NewModelValidator(nil, nil, nil)
	if v == nil {
		t.Fatal("NewModelValidator returned nil")
	}
}

// TestValidationResultStruct tests the ValidationResult struct.
func TestValidationResultStruct(t *testing.T) {
	result := ValidationResult{
		Provider:      "openai",
		ProductID:     "gpt-4",
		APIModelName:  "gpt-4",
		Available:     true,
		SuggestedName: "gpt-4",
		LatencyMs:     250,
	}

	if !result.Available {
		t.Error("Available should be true")
	}
	if result.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", result.Provider)
	}
	if result.LatencyMs != 250 {
		t.Errorf("LatencyMs = %d, want 250", result.LatencyMs)
	}
}

// Additional helper / HTTP-path coverage tests.

func TestExtractErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		body     map[string]interface{}
		expected string
	}{
		{
			name:     "openai format",
			body:     map[string]interface{}{"error": map[string]interface{}{"message": "model not found"}},
			expected: "model not found",
		},
		{
			name:     "direct message",
			body:     map[string]interface{}{"message": "bad request"},
			expected: "bad request",
		},
		{
			name:     "fallback",
			body:     map[string]interface{}{"detail": "unknown"},
			expected: "map[detail:unknown]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, extractErrorMessage(tt.body))
		})
	}
}

func TestParseBedrockCredentials(t *testing.T) {
	access, secret, session, err := parseBedrockCredentials("AKID:SECRET")
	assert.NoError(t, err)
	assert.Equal(t, "AKID", access)
	assert.Equal(t, "SECRET", secret)
	assert.Empty(t, session)

	_, _, session, err = parseBedrockCredentials("AKID:SECRET:SESSION")
	assert.NoError(t, err)
	assert.Equal(t, "SESSION", session)

	_, _, _, err = parseBedrockCredentials("only-one")
	assert.Error(t, err)
}

func TestExtractRegionFromProvider(t *testing.T) {
	assert.Equal(t, "us-east-1", extractRegionFromProvider("amazon-us-east-1"))
	assert.Equal(t, "us-west-2", extractRegionFromProvider("amazon-mantle-us-west-2"))
	assert.Equal(t, "us-east-1", extractRegionFromProvider("amazon"))
}

func TestBuildTestURL(t *testing.T) {
	v := NewModelValidator(nil, &config.Config{}, zap.NewNop())
	openaiBase := "https://api.openai.com/v1"
	customBase := "https://custom.example.com"
	anthropicBase := "https://api.anthropic.com"
	googleBase := "https://generativelanguage.googleapis.com/v1beta"

	assert.Equal(t, "https://api.openai.com/v1/chat/completions", v.buildTestURL("openai", nil, "gpt-4o", "test-key"))
	assert.Equal(t, "https://api.openai.com/v1/chat/completions", v.buildTestURL("openai", &openaiBase, "gpt-4o", "test-key"))
	assert.Equal(t, "https://custom.example.com/v1/chat/completions", v.buildTestURL("accessapi", &customBase, "model", "test-key"))
	assert.Equal(t, "https://api.anthropic.com/v1/messages", v.buildTestURL("anthropic", &anthropicBase, "claude-sonnet-4", "test-key"))
	assert.Equal(t, "https://generativelanguage.googleapis.com/v1beta/models/model:generateContent?key=test-key", v.buildTestURL("google", &googleBase, "model", "test-key"))
}

func TestSetHeaders(t *testing.T) {
	v := NewModelValidator(nil, &config.Config{}, zap.NewNop())

	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", nil)
	v.setHeaders(req, "openai", "sk-test")
	assert.Equal(t, "Bearer sk-test", req.Header.Get("Authorization"))

	req, _ = http.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	v.setHeaders(req, "anthropic", "sk-ant")
	assert.Equal(t, "sk-ant", req.Header.Get("x-api-key"))
	assert.Equal(t, "2025-01-01", req.Header.Get("anthropic-version"))
}

func TestTestModelRequest_UnsupportedProvider(t *testing.T) {
	v := NewModelValidator(nil, &config.Config{}, zap.NewNop())
	v.client = &http.Client{Timeout: 100 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	available, errType, errMsg := v.testModelRequest(ctx, "unknown", "key", nil, "model", false)
	assert.False(t, available)
	assert.Equal(t, "request_error", errType)
	assert.NotEmpty(t, errMsg)
}

func TestTestModelRequest_ModelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"error": {"message": "model not found"}}`)
	}))
	defer server.Close()

	cfg := &config.Config{}
	v := NewModelValidator(nil, cfg, zap.NewNop())
	v.client = server.Client()

	baseURL := server.URL
	available, errType, _ := v.testModelRequest(context.Background(), "openai", "key", &baseURL, "missing-model", false)
	assert.False(t, available)
	assert.Equal(t, "not_found", errType)
}

func TestTestModelRequest_ModelExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"id":"chatcmpl-1"}`)
	}))
	defer server.Close()

	cfg := &config.Config{}
	v := NewModelValidator(nil, cfg, zap.NewNop())
	v.client = server.Client()

	baseURL := server.URL
	available, errType, _ := v.testModelRequest(context.Background(), "openai", "key", &baseURL, "gpt-4o", false)
	assert.True(t, available)
	assert.Empty(t, errType)
}

func TestTestModelRequest_BadRequestFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error": {"message": "invalid message format"}}`)
	}))
	defer server.Close()

	cfg := &config.Config{}
	v := NewModelValidator(nil, cfg, zap.NewNop())
	v.client = server.Client()

	baseURL := server.URL
	available, errType, _ := v.testModelRequest(context.Background(), "openai", "key", &baseURL, "gpt-4o", false)
	// 400 containing "message" is treated as the model existing with a format issue.
	assert.True(t, available)
	assert.Empty(t, errType)
}

func TestTestModelRequest_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := &config.Config{}
	v := NewModelValidator(nil, cfg, zap.NewNop())
	v.client = server.Client()

	baseURL := server.URL
	available, errType, _ := v.testModelRequest(context.Background(), "openai", "key", &baseURL, "gpt-4o", false)
	assert.True(t, available)
	assert.Empty(t, errType)
}
