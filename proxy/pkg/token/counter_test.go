package token

import (
	"testing"

	"github.com/mayiants/ai-proxy-go/dto"
)

func TestCountTextToken(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		model    string
		minToken int
		maxToken int
	}{
		{
			name:     "empty text",
			text:     "",
			model:    "gpt-4",
			minToken: 0,
			maxToken: 0,
		},
		{
			name:     "simple text gpt-4",
			text:     "Hello, world!",
			model:    "gpt-4",
			minToken: 1,
			maxToken: 10,
		},
		{
			name:     "longer text gpt-4",
			text:     "This is a longer piece of text that should have more tokens.",
			model:    "gpt-4",
			minToken: 5,
			maxToken: 20,
		},
		{
			name:     "claude model estimation",
			text:     "Hello, this is a test for Claude token estimation.",
			model:    "claude-3-opus",
			minToken: 5,
			maxToken: 20,
		},
		{
			name:     "gemini model estimation",
			text:     "Testing Gemini token estimation with this sentence.",
			model:    "gemini-pro",
			minToken: 5,
			maxToken: 20,
		},
		{
			name:     "chinese text qwen",
			text:     "这是一段中文测试文本",
			model:    "qwen-turbo",
			minToken: 3,
			maxToken: 15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := CountTextToken(tt.text, tt.model)
			if tokens < tt.minToken || tokens > tt.maxToken {
				t.Errorf("CountTextToken() = %v, want between %v and %v", tokens, tt.minToken, tt.maxToken)
			}
		})
	}
}

func TestEstimateTokenByModel(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		model    string
		minToken int
	}{
		{
			name:     "claude model",
			text:     "Hello world",
			model:    "claude-3-sonnet",
			minToken: 2, // ~10 chars / 3.5
		},
		{
			name:     "gemini model",
			text:     "Hello world",
			model:    "gemini-1.5-pro",
			minToken: 2,
		},
		{
			name:     "qwen model (chinese optimized)",
			text:     "Hello world",
			model:    "qwen-turbo",
			minToken: 2, // ~10 chars / 2.5 = 4, but estimation varies
		},
		{
			name:     "deepseek model",
			text:     "Hello world",
			model:    "deepseek-chat",
			minToken: 2,
		},
		{
			name:     "unknown model defaults to gpt-4 style",
			text:     "Hello world",
			model:    "unknown-model",
			minToken: 2, // ~10 chars / 4
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := EstimateTokenByModel(tt.text, tt.model)
			if tokens < tt.minToken {
				t.Errorf("EstimateTokenByModel() = %v, want at least %v", tokens, tt.minToken)
			}
		})
	}
}

func TestCounter_EstimateMessages(t *testing.T) {
	counter := NewCounter()

	tests := []struct {
		name     string
		messages []dto.Message
		model    string
		minToken int
	}{
		{
			name:     "empty messages",
			messages: []dto.Message{},
			model:    "gpt-4",
			minToken: 0,
		},
		{
			name: "single message",
			messages: []dto.Message{
				{Role: "user", Content: "Hello, how are you?"},
			},
			model:    "gpt-4",
			minToken: 1,
		},
		{
			name: "multiple messages",
			messages: []dto.Message{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "user", Content: "Hello!"},
				{Role: "assistant", Content: "Hi there! How can I help you today?"},
			},
			model:    "gpt-4",
			minToken: 5,
		},
		{
			name: "message with media content",
			messages: []dto.Message{
				{
					Role: "user",
					Content: []dto.MediaContent{
						{Type: "text", Text: "What's in this image?"},
						{Type: "image_url", ImageURL: &dto.MessageImageURL{URL: "https://example.com/image.png"}},
					},
				},
			},
			model:    "gpt-4o",
			minToken: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := counter.EstimateMessages(tt.messages, tt.model)
			if tokens < tt.minToken {
				t.Errorf("EstimateMessages() = %v, want at least %v", tokens, tt.minToken)
			}
		})
	}
}

func TestExtractTextFromMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []dto.Message
		want     string
	}{
		{
			name:     "empty messages",
			messages: []dto.Message{},
			want:     "",
		},
		{
			name: "string content",
			messages: []dto.Message{
				{Role: "user", Content: "Hello"},
			},
			want: "Hello ",
		},
		{
			name: "media content",
			messages: []dto.Message{
				{
					Role: "user",
					Content: []dto.MediaContent{
						{Type: "text", Text: "Hello"},
						{Type: "text", Text: "World"},
					},
				},
			},
			want: "Hello World ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTextFromMessages(tt.messages)
			if got != tt.want {
				t.Errorf("ExtractTextFromMessages() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCountAudioToken(t *testing.T) {
	tests := []struct {
		name            string
		durationSeconds float64
		inputTokens     int
		outputTokens    int
	}{
		{"0 seconds", 0, 0, 0},
		{"30 seconds", 30, 100, 200}, // ceil(30/60) * 100 = 100
		{"60 seconds", 60, 100, 200},
		{"90 seconds", 90, 200, 400}, // ceil(90/60) * 100 = 200
		{"45.5 seconds", 45.5, 100, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := CountAudioTokenInput(tt.durationSeconds)
			output := CountAudioTokenOutput(tt.durationSeconds)

			if input != tt.inputTokens {
				t.Errorf("CountAudioTokenInput(%v) = %v, want %v", tt.durationSeconds, input, tt.inputTokens)
			}
			if output != tt.outputTokens {
				t.Errorf("CountAudioTokenOutput(%v) = %v, want %v", tt.durationSeconds, output, tt.outputTokens)
			}
		})
	}
}
