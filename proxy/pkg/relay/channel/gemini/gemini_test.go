package gemini

import (
	"encoding/json"
	"testing"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/pkg/relay/base"
	"github.com/ai-proxy/ai-proxy-go/types"
)

func TestNewGeminiAdaptor(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *base.AdaptorConfig
		wantURL string
	}{
		{
			name:    "default config",
			cfg:     &base.AdaptorConfig{},
			wantURL: "https://generativelanguage.googleapis.com/v1beta",
		},
		{
			name: "custom base URL",
			cfg: &base.AdaptorConfig{
				BaseURL: "https://custom.googleapis.com/v1",
			},
			wantURL: "https://custom.googleapis.com/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptor := NewGeminiAdaptor(tt.cfg)
			if adaptor == nil {
				t.Fatal("NewGeminiAdaptor returned nil")
			}
			if adaptor.BaseURL != tt.wantURL {
				t.Errorf("BaseURL = %v, want %v", adaptor.BaseURL, tt.wantURL)
			}
		})
	}
}

func TestGeminiAdaptor_GetRelayFormat(t *testing.T) {
	adaptor := NewGeminiAdaptor(&base.AdaptorConfig{})
	if adaptor.GetRelayFormat() != types.RelayFormatGemini {
		t.Errorf("GetRelayFormat() = %v, want %v", adaptor.GetRelayFormat(), types.RelayFormatGemini)
	}
}

func TestGeminiAdaptor_GetEndpoint(t *testing.T) {
	adaptor := NewGeminiAdaptor(&base.AdaptorConfig{})

	tests := []struct {
		model    string
		stream   bool
		wantPath string
	}{
		{"gemini-pro", false, "/models/gemini-pro:generateContent"},
		{"gemini-pro", true, "/models/gemini-pro:streamGenerateContent"},
		{"gemini-1.5-flash", false, "/models/gemini-1.5-flash:generateContent"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			endpoint := adaptor.GetEndpoint(tt.model, tt.stream)
			if endpoint != tt.wantPath {
				t.Errorf("GetEndpoint(%v, %v) = %v, want %v", tt.model, tt.stream, endpoint, tt.wantPath)
			}
		})
	}
}

func TestGeminiAdaptor_GetHeaders(t *testing.T) {
	cfg := &base.AdaptorConfig{
		APIKey: "test-api-key",
	}
	adaptor := NewGeminiAdaptor(cfg)
	headers := adaptor.GetHeaders()

	if headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type header = %v, want application/json", headers["Content-Type"])
	}
	if headers["x-goog-api-key"] != "test-api-key" {
		t.Errorf("x-goog-api-key header = %v, want test-api-key", headers["x-goog-api-key"])
	}
}

func TestGeminiAdaptor_GetRequestURL(t *testing.T) {
	cfg := &base.AdaptorConfig{
		APIKey: "test-api-key",
	}
	adaptor := NewGeminiAdaptor(cfg)

	url := adaptor.GetRequestURL("gemini-pro", false)
	if url == "" {
		t.Error("GetRequestURL returned empty string")
	}
	// API key should NOT be in URL query (security: prevents key leak in logs)
	if contains(url, "key=test-api-key") {
		t.Errorf("GetRequestURL should NOT contain API key in query param, got: %v", url)
	}
	expected := "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent"
	if url != expected {
		t.Errorf("GetRequestURL = %v, want %v", url, expected)
	}
}

func TestGeminiAdaptor_ToProvider(t *testing.T) {
	adaptor := NewGeminiAdaptor(&base.AdaptorConfig{})

	tests := []struct {
		name string
		req  dto.GeneralOpenAIRequest
	}{
		{
			name: "basic request",
			req: dto.GeneralOpenAIRequest{
				Model:    "gemini-pro",
				Messages: []dto.Message{{Role: "user", Content: "Hello"}},
			},
		},
		{
			name: "request with parameters",
			req: dto.GeneralOpenAIRequest{
				Model:       "gemini-1.5-flash",
				Messages:    []dto.Message{{Role: "user", Content: "Hello"}},
				Temperature: floatPtr(0.7),
				TopP:        floatPtr(0.9),
				MaxTokens:   intPtr(1024),
			},
		},
		{
			name: "multi-turn conversation",
			req: dto.GeneralOpenAIRequest{
				Model: "gemini-pro",
				Messages: []dto.Message{
					{Role: "user", Content: "Hi"},
					{Role: "assistant", Content: "Hello!"},
					{Role: "user", Content: "How are you?"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adaptor.ToProvider(tt.req)
			if result == nil {
				t.Fatal("ToProvider returned nil")
			}

			geminiReq, ok := result.(*GeminiRequest)
			if !ok {
				t.Fatal("ToProvider did not return *GeminiRequest")
			}

			if len(geminiReq.Contents) == 0 {
				t.Error("GeminiRequest should have at least one content")
			}
		})
	}
}

func TestGeminiAdaptor_FromProvider(t *testing.T) {
	adaptor := NewGeminiAdaptor(&base.AdaptorConfig{})

	geminiResp := GeminiResponse{
		Candidates: []GeminiCandidate{
			{
				Content: GeminiContent{
					Role:  "model",
					Parts: []GeminiPart{{Text: "Hello from Gemini!"}},
				},
				FinishReason: "STOP",
			},
		},
		UsageMetadata: &GeminiUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 20,
			TotalTokenCount:      30,
		},
	}

	data, _ := json.Marshal(geminiResp)
	req := dto.GeneralOpenAIRequest{Model: "gemini-pro"}

	result := adaptor.FromProvider(data, req)
	if result == nil {
		t.Fatal("FromProvider returned nil")
	}

	if len(result.Choices) != 1 {
		t.Errorf("Choices count = %v, want 1", len(result.Choices))
	}
	if result.Choices[0].Message.Content != "Hello from Gemini!" {
		t.Errorf("Content = %v, want 'Hello from Gemini!'", result.Choices[0].Message.Content)
	}
	if result.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %v, want 10", result.Usage.PromptTokens)
	}
}

func TestGeminiAdaptor_ExtractUsage(t *testing.T) {
	adaptor := NewGeminiAdaptor(&base.AdaptorConfig{})

	tests := []struct {
		name          string
		usageMetadata *GeminiUsageMetadata
		wantPrompt    int
		wantComplete  int
		wantCached    int
	}{
		{
			name: "basic usage",
			usageMetadata: &GeminiUsageMetadata{
				PromptTokenCount:     100,
				CandidatesTokenCount: 50,
				TotalTokenCount:      150,
			},
			wantPrompt:   100,
			wantComplete: 50,
			wantCached:   0,
		},
		{
			name: "usage with cache",
			usageMetadata: &GeminiUsageMetadata{
				PromptTokenCount:        100,
				CandidatesTokenCount:    50,
				TotalTokenCount:         150,
				CachedContentTokenCount: 30,
			},
			wantPrompt:   100,
			wantComplete: 50,
			wantCached:   30,
		},
		{
			name:          "nil metadata",
			usageMetadata: nil,
			wantPrompt:    0,
			wantComplete:  0,
			wantCached:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			geminiResp := GeminiResponse{UsageMetadata: tt.usageMetadata}
			data, _ := json.Marshal(geminiResp)

			usage := adaptor.ExtractUsage(data)

			if usage.PromptTokens != tt.wantPrompt {
				t.Errorf("PromptTokens = %v, want %v", usage.PromptTokens, tt.wantPrompt)
			}
			if usage.CompletionTokens != tt.wantComplete {
				t.Errorf("CompletionTokens = %v, want %v", usage.CompletionTokens, tt.wantComplete)
			}
			if usage.PromptCacheHitTokens != tt.wantCached {
				t.Errorf("PromptCacheHitTokens = %v, want %v", usage.PromptCacheHitTokens, tt.wantCached)
			}
		})
	}
}

func TestMapGeminiFinishReason(t *testing.T) {
	tests := []struct {
		reason string
		want   string
	}{
		{"STOP", "stop"},
		{"MAX_TOKENS", "length"},
		{"SAFETY", "content_filter"},
		{"RECITATION", "content_filter"},
		{"UNKNOWN", "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := mapGeminiFinishReason(tt.reason)
			if got != tt.want {
				t.Errorf("mapGeminiFinishReason(%v) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}

func TestConvertToGeminiContent(t *testing.T) {
	tests := []struct {
		name     string
		msg      dto.Message
		wantRole string
	}{
		{
			name:     "user message",
			msg:      dto.Message{Role: "user", Content: "Hello"},
			wantRole: "user",
		},
		{
			name:     "assistant message becomes model",
			msg:      dto.Message{Role: "assistant", Content: "Hi there"},
			wantRole: "model",
		},
		{
			name: "media content",
			msg: dto.Message{
				Role: "user",
				Content: []dto.MediaContent{
					{Type: "text", Text: "What's this?"},
				},
			},
			wantRole: "user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			geminiContent := convertToGeminiContent(tt.msg)
			if geminiContent.Role != tt.wantRole {
				t.Errorf("Role = %v, want %v", geminiContent.Role, tt.wantRole)
			}
		})
	}
}

func TestGeminiAdaptor_ErrorHandling(t *testing.T) {
	adaptor := NewGeminiAdaptor(&base.AdaptorConfig{})

	// Test error response
	geminiResp := GeminiResponse{
		Error: &GeminiError{
			Code:    400,
			Message: "Invalid request",
			Status:  "INVALID_ARGUMENT",
		},
	}

	data, _ := json.Marshal(geminiResp)
	req := dto.GeneralOpenAIRequest{Model: "gemini-pro"}

	result := adaptor.FromProvider(data, req)
	if result == nil {
		t.Fatal("FromProvider should not return nil for error response")
	}

	// Should return empty choices for error
	if len(result.Choices) != 0 {
		t.Errorf("Choices should be empty for error, got %d", len(result.Choices))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[len(s)-len(substr):] == substr ||
		len(s) > len(substr) && containsHelper(s, substr)
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func floatPtr(f float64) *float64 {
	return &f
}

func intPtr(i int) *int {
	return &i
}
