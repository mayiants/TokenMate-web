package openai

import (
	"encoding/json"
	"testing"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/relay/base"
	"github.com/mayiants/ai-proxy-go/types"
)

func TestNewOpenAIAdaptor(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *base.AdaptorConfig
		wantURL string
	}{
		{
			name:    "default config",
			cfg:     &base.AdaptorConfig{},
			wantURL: "https://api.openai.com/v1",
		},
		{
			name: "custom base URL",
			cfg: &base.AdaptorConfig{
				BaseURL: "https://custom.openai.com/v1",
			},
			wantURL: "https://custom.openai.com/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptor := NewOpenAIAdaptor(tt.cfg)
			if adaptor == nil {
				t.Fatal("NewOpenAIAdaptor returned nil")
			}
			if adaptor.BaseURL != tt.wantURL {
				t.Errorf("BaseURL = %v, want %v", adaptor.BaseURL, tt.wantURL)
			}
		})
	}
}

func TestOpenAIAdaptor_GetRelayFormat(t *testing.T) {
	adaptor := NewOpenAIAdaptor(&base.AdaptorConfig{})
	if adaptor.GetRelayFormat() != types.RelayFormatOpenAI {
		t.Errorf("GetRelayFormat() = %v, want %v", adaptor.GetRelayFormat(), types.RelayFormatOpenAI)
	}
}

func TestOpenAIAdaptor_GetEndpoint(t *testing.T) {
	adaptor := NewOpenAIAdaptor(&base.AdaptorConfig{})
	endpoint := adaptor.GetEndpoint("gpt-4", false)
	if endpoint != "/chat/completions" {
		t.Errorf("GetEndpoint() = %v, want /chat/completions", endpoint)
	}
}

func TestOpenAIAdaptor_ToProvider(t *testing.T) {
	adaptor := NewOpenAIAdaptor(&base.AdaptorConfig{})

	tests := []struct {
		name       string
		req        dto.GeneralOpenAIRequest
		wantStream bool
	}{
		{
			name: "non-streaming request",
			req: dto.GeneralOpenAIRequest{
				Model:    "gpt-4",
				Messages: []dto.Message{{Role: "user", Content: "Hello"}},
			},
			wantStream: false,
		},
		{
			name: "streaming request",
			req: dto.GeneralOpenAIRequest{
				Model:    "gpt-4",
				Messages: []dto.Message{{Role: "user", Content: "Hello"}},
				Stream:   boolPtr(true),
			},
			wantStream: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adaptor.ToProvider(tt.req)
			if result == nil {
				t.Fatal("ToProvider returned nil")
			}

			req, ok := result.(dto.GeneralOpenAIRequest)
			if !ok {
				t.Fatal("ToProvider did not return GeneralOpenAIRequest")
			}

			// StreamOptions is now injected by the plugin chain, not the adapter.
			if tt.wantStream && req.StreamOptions != nil {
				t.Error("StreamOptions should not be set by adapter anymore")
			}
		})
	}
}

func TestOpenAIAdaptor_FromProvider(t *testing.T) {
	adaptor := NewOpenAIAdaptor(&base.AdaptorConfig{})

	response := dto.OpenAITextResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: dto.Message{
					Role:    "assistant",
					Content: "Hello! How can I help you?",
				},
				FinishReason: "stop",
			},
		},
		Usage: dto.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	req := dto.GeneralOpenAIRequest{Model: "gpt-4"}
	result := adaptor.FromProvider(data, req)
	if result == nil {
		t.Fatal("FromProvider returned nil")
	}

	if result.ID != response.ID {
		t.Errorf("ID = %v, want %v", result.ID, response.ID)
	}
	if len(result.Choices) != 1 {
		t.Errorf("Choices count = %v, want 1", len(result.Choices))
	}
}

func TestOpenAIAdaptor_ExtractUsage(t *testing.T) {
	adaptor := NewOpenAIAdaptor(&base.AdaptorConfig{})

	tests := []struct {
		name         string
		response     dto.OpenAITextResponse
		wantPrompt   int
		wantComplete int
		wantCached   int
	}{
		{
			name: "basic usage",
			response: dto.OpenAITextResponse{
				Usage: dto.Usage{
					PromptTokens:     100,
					CompletionTokens: 50,
					TotalTokens:      150,
				},
			},
			wantPrompt:   100,
			wantComplete: 50,
			wantCached:   0,
		},
		{
			name: "usage with cache",
			response: dto.OpenAITextResponse{
				Usage: dto.Usage{
					PromptTokens:     100,
					CompletionTokens: 50,
					TotalTokens:      150,
					PromptTokensDetails: dto.InputTokenDetails{
						CachedTokens: 30,
					},
				},
			},
			wantPrompt:   100,
			wantComplete: 50,
			wantCached:   30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, _ := json.Marshal(tt.response)
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

func TestOpenAIAdaptor_TransformStreamChunk(t *testing.T) {
	adaptor := NewOpenAIAdaptor(&base.AdaptorConfig{})

	// Test [DONE] marker
	result := adaptor.TransformStreamChunk([]byte("[DONE]"), 0)
	if result != nil {
		t.Error("TransformStreamChunk([DONE]) should return nil")
	}

	// Test valid chunk
	chunk := dto.ChatCompletionsStreamResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Index: 0,
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Content: strPtr("Hello"),
				},
			},
		},
	}

	data, _ := json.Marshal(chunk)
	result = adaptor.TransformStreamChunk(data, 0)
	if result == nil {
		t.Fatal("TransformStreamChunk returned nil for valid chunk")
	}

	streamResp, ok := result.(*dto.ChatCompletionsStreamResponse)
	if !ok {
		t.Fatal("TransformStreamChunk did not return *ChatCompletionsStreamResponse")
	}

	if len(streamResp.Choices) != 1 {
		t.Errorf("Choices count = %v, want 1", len(streamResp.Choices))
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func strPtr(s string) *string {
	return &s
}
