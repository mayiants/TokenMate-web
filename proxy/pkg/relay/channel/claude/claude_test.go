package claude

import (
	"encoding/json"
	"testing"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/relay/base"
	"github.com/mayiants/ai-proxy-go/types"
)

func TestNewClaudeAdaptor(t *testing.T) {
	tests := []struct {
		name           string
		cfg            *base.AdaptorConfig
		wantURL        string
		wantAPIVersion string
	}{
		{
			name:           "default config",
			cfg:            &base.AdaptorConfig{},
			wantURL:        "https://api.anthropic.com/v1",
			wantAPIVersion: "2023-06-01",
		},
		{
			name: "custom config",
			cfg: &base.AdaptorConfig{
				BaseURL:    "https://custom.anthropic.com/v1",
				APIVersion: "2024-01-01",
			},
			wantURL:        "https://custom.anthropic.com/v1",
			wantAPIVersion: "2024-01-01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptor := NewClaudeAdaptor(tt.cfg)
			if adaptor == nil {
				t.Fatal("NewClaudeAdaptor returned nil")
			}
			if adaptor.BaseURL != tt.wantURL {
				t.Errorf("BaseURL = %v, want %v", adaptor.BaseURL, tt.wantURL)
			}
			if adaptor.apiVersion != tt.wantAPIVersion {
				t.Errorf("apiVersion = %v, want %v", adaptor.apiVersion, tt.wantAPIVersion)
			}
		})
	}
}

func TestClaudeAdaptor_GetRelayFormat(t *testing.T) {
	adaptor := NewClaudeAdaptor(&base.AdaptorConfig{})
	if adaptor.GetRelayFormat() != types.RelayFormatClaude {
		t.Errorf("GetRelayFormat() = %v, want %v", adaptor.GetRelayFormat(), types.RelayFormatClaude)
	}
}

func TestClaudeAdaptor_GetEndpoint(t *testing.T) {
	adaptor := NewClaudeAdaptor(&base.AdaptorConfig{})
	endpoint := adaptor.GetEndpoint("claude-3-opus", false)
	if endpoint != "/messages" {
		t.Errorf("GetEndpoint() = %v, want /messages", endpoint)
	}
}

func TestClaudeAdaptor_GetHeaders(t *testing.T) {
	cfg := &base.AdaptorConfig{
		APIKey:     "test-api-key",
		APIVersion: "2023-06-01",
	}
	adaptor := NewClaudeAdaptor(cfg)

	headers := adaptor.GetHeaders()
	if headers["x-api-key"] != "test-api-key" {
		t.Errorf("x-api-key header = %v, want test-api-key", headers["x-api-key"])
	}
	if headers["anthropic-version"] != "2023-06-01" {
		t.Errorf("anthropic-version header = %v, want 2023-06-01", headers["anthropic-version"])
	}
}

func TestClaudeAdaptor_ToProvider(t *testing.T) {
	adaptor := NewClaudeAdaptor(&base.AdaptorConfig{})

	tests := []struct {
		name          string
		req           dto.GeneralOpenAIRequest
		wantMaxTokens int
		wantSystem    string
	}{
		{
			name: "basic request",
			req: dto.GeneralOpenAIRequest{
				Model: "claude-3-opus",
				Messages: []dto.Message{
					{Role: "user", Content: "Hello"},
				},
			},
			wantMaxTokens: 4096, // default
		},
		{
			name: "request with system message",
			req: dto.GeneralOpenAIRequest{
				Model: "claude-3-opus",
				Messages: []dto.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello"},
				},
			},
			wantMaxTokens: 4096,
			wantSystem:    "You are a helpful assistant.",
		},
		{
			name: "request with custom max_tokens",
			req: dto.GeneralOpenAIRequest{
				Model:     "claude-3-opus",
				Messages:  []dto.Message{{Role: "user", Content: "Hello"}},
				MaxTokens: intPtr(2048),
			},
			wantMaxTokens: 2048,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adaptor.ToProvider(tt.req)
			if result == nil {
				t.Fatal("ToProvider returned nil")
			}

			claudeReq, ok := result.(dto.ClaudeRequest)
			if !ok {
				t.Fatal("ToProvider did not return ClaudeRequest")
			}

			if claudeReq.MaxTokens == nil || *claudeReq.MaxTokens != tt.wantMaxTokens {
				t.Errorf("MaxTokens = %v, want %v", claudeReq.MaxTokens, tt.wantMaxTokens)
			}
			if tt.wantSystem != "" && claudeReq.System != tt.wantSystem {
				t.Errorf("System = %v, want %v", claudeReq.System, tt.wantSystem)
			}
		})
	}
}

func TestClaudeAdaptor_FromProvider(t *testing.T) {
	adaptor := NewClaudeAdaptor(&base.AdaptorConfig{})

	claudeResp := dto.ClaudeResponse{
		ID:    "msg_123",
		Type:  "message",
		Role:  "assistant",
		Model: "claude-3-opus",
		Content: []dto.ClaudeContent{
			{Type: "text", Text: strPtr("Hello! How can I help you?")},
		},
		StopReason: "end_turn",
		Usage: dto.ClaudeUsage{
			InputTokens:  10,
			OutputTokens: 20,
		},
	}

	data, _ := json.Marshal(claudeResp)
	req := dto.GeneralOpenAIRequest{Model: "claude-3-opus"}

	result := adaptor.FromProvider(data, req)
	if result == nil {
		t.Fatal("FromProvider returned nil")
	}

	if result.Model != "claude-3-opus" {
		t.Errorf("Model = %v, want claude-3-opus", result.Model)
	}
	if len(result.Choices) != 1 {
		t.Errorf("Choices count = %v, want 1", len(result.Choices))
	}
	if result.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %v, want 10", result.Usage.PromptTokens)
	}
}

func TestClaudeAdaptor_ExtractUsage(t *testing.T) {
	adaptor := NewClaudeAdaptor(&base.AdaptorConfig{})

	tests := []struct {
		name         string
		claudeUsage  dto.ClaudeUsage
		wantPrompt   int
		wantComplete int
		wantCached   int
	}{
		{
			name: "basic usage",
			claudeUsage: dto.ClaudeUsage{
				InputTokens:  100,
				OutputTokens: 50,
			},
			wantPrompt:   100,
			wantComplete: 50,
			wantCached:   0,
		},
		{
			name: "usage with cache read",
			claudeUsage: dto.ClaudeUsage{
				InputTokens:          100,
				OutputTokens:         50,
				CacheReadInputTokens: 30,
			},
			// W1 fix: cache_read is added back to PromptTokens to match
			// OpenAI's effective-input semantics. Anthropic's input_tokens
			// excludes cache_read; we fold it in so billing sees the full
			// billed input.
			wantPrompt:   130, // 100 + 30 cache_read
			wantComplete: 50,
			wantCached:   30,
		},
		{
			name: "usage with cache creation",
			claudeUsage: dto.ClaudeUsage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 20,
			},
			wantPrompt:   100,
			wantComplete: 50,
			wantCached:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claudeResp := dto.ClaudeResponse{Usage: tt.claudeUsage}
			data, _ := json.Marshal(claudeResp)

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

func TestMapClaudeStopReason(t *testing.T) {
	tests := []struct {
		reason string
		want   string
	}{
		{"end_turn", "stop"},
		{"max_tokens", "length"},
		{"stop_sequence", "stop"},
		{"tool_use", "tool_calls"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := mapClaudeStopReason(tt.reason)
			if got != tt.want {
				t.Errorf("mapClaudeStopReason(%v) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}

func TestConvertToClaudeMessage(t *testing.T) {
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
			name:     "assistant message",
			msg:      dto.Message{Role: "assistant", Content: "Hi there"},
			wantRole: "assistant",
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
			claudeMsg := convertToClaudeMessage(tt.msg)
			if claudeMsg.Role != tt.wantRole {
				t.Errorf("Role = %v, want %v", claudeMsg.Role, tt.wantRole)
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}

func strPtr(s string) *string {
	return &s
}
