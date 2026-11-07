package token

import (
	"math"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/types"
	"github.com/pkoukk/tiktoken-go"
)

// Counter - Token counter
type Counter struct{}

// NewCounter - Create new token counter
func NewCounter() *Counter {
	return &Counter{}
}

// EstimateMessages - Estimate tokens for messages
func (c *Counter) EstimateMessages(messages []dto.Message, model string) int {
	text := ExtractTextFromMessages(messages)
	return CountTextToken(text, model)
}

// Token encoder cache with thread-safe access
var (
	tokenEncoderCache sync.Map // key: string (model), value: *tiktoken.Tiktoken
)

// CountTextToken - Count tokens for text content
// Uses tiktoken for OpenAI models, estimation for others
func CountTextToken(text string, model string) int {
	if text == "" {
		return 0
	}

	if isOpenAITextModel(model) {
		encoder := getTokenEncoder(model)
		if encoder != nil {
			return len(encoder.Encode(text, nil, nil))
		}
	}

	// Non-OpenAI model, use estimation
	return EstimateTokenByModel(model, text)
}

// EstimateTokenByModel - Estimate tokens based on model type
func EstimateTokenByModel(model, text string) int {
	// Different models have different tokenization
	// Use character-based estimation as fallback
	charCount := utf8.RuneCountInString(text)

	modelLower := strings.ToLower(model)

	// Claude uses similar tokenization to GPT-4
	if strings.Contains(modelLower, "claude") {
		return int(float64(charCount) / 3.5)
	}

	// Gemini
	if strings.Contains(modelLower, "gemini") {
		return int(float64(charCount) / 3.5)
	}

	// Chinese models (Qwen, GLM, etc.) - more tokens for Chinese
	if strings.Contains(modelLower, "qwen") ||
		strings.Contains(modelLower, "glm") ||
		strings.Contains(modelLower, "ernie") {
		return int(float64(charCount) / 2.5)
	}

	// Default: GPT-4 style
	return int(float64(charCount) / 4.0)
}

// EstimateRequestToken - Estimate total tokens for a request
func EstimateRequestToken(meta *types.TokenCountMeta, isStream bool) int {
	if meta == nil {
		return 0
	}

	totalTokens := 0

	// Text tokens
	if meta.TokenType == types.TokenTypeTextNumber {
		totalTokens += utf8.RuneCountInString(meta.CombineText)
	} else {
		totalTokens += CountTextToken(meta.CombineText, "gpt-4")
	}

	// Add overhead for message formatting
	totalTokens += meta.MessagesCount * 3
	totalTokens += meta.ToolsCount * 8
	totalTokens += meta.NameCount * 3
	totalTokens += 3

	// Add file tokens
	for _, file := range meta.Files {
		switch file.FileType {
		case types.FileTypeImage:
			totalTokens += estimateImageTokens(file)
		case types.FileTypeAudio:
			totalTokens += 256 // Default audio tokens
		case types.FileTypeVideo:
			totalTokens += 4096 * 2
		case types.FileTypeFile:
			totalTokens += 4096
		default:
			totalTokens += 4096
		}
	}

	return totalTokens
}

// estimateImageTokens - Estimate image tokens
func estimateImageTokens(file *types.FileMeta) int {
	// GPT-4o style: tile-based calculation
	// Default: 85 base + 170 per tile
	baseTokens := 85
	tileTokens := 170

	// For low detail, just return base
	if file.Detail == "low" {
		return baseTokens
	}

	// For high/auto detail, estimate based on typical image size
	// Assume 1024x1024 image = 4 tiles
	return baseTokens + 4*tileTokens
}

// isOpenAITextModel - Check if model uses tiktoken
func isOpenAITextModel(model string) bool {
	modelLower := strings.ToLower(model)

	openaiPrefixes := []string{
		"gpt-4", "gpt-3.5", "gpt-4o", "gpt-4-turbo",
		"o1-", "o3-", "o4-",
		"text-davinci", "text-embedding",
	}

	for _, prefix := range openaiPrefixes {
		if strings.HasPrefix(modelLower, prefix) {
			return true
		}
	}

	return false
}

// getTokenEncoder - Get or create token encoder for model
func getTokenEncoder(model string) *tiktoken.Tiktoken {
	encoding := "cl100k_base"

	switch {
	case strings.HasPrefix(model, "gpt-4o"):
		encoding = "o200k_base"
	case strings.HasPrefix(model, "o1-"), strings.HasPrefix(model, "o3-"):
		encoding = "o200k_base"
	}

	encoder, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return nil
	}

	val, _ := tokenEncoderCache.LoadOrStore(model, encoder)
	return val.(*tiktoken.Tiktoken)
}

// ExtractTextFromMessages - Extract text from messages for token counting
func ExtractTextFromMessages(messages []dto.Message) string {
	var text strings.Builder

	for _, msg := range messages {
		switch content := msg.Content.(type) {
		case string:
			text.WriteString(content)
			text.WriteString(" ")
		case []interface{}:
			for _, item := range content {
				if m, ok := item.(map[string]interface{}); ok {
					if t, ok := m["text"].(string); ok {
						text.WriteString(t)
						text.WriteString(" ")
					}
				}
			}
		case []dto.MediaContent:
			for _, m := range content {
				if m.Type == "text" {
					text.WriteString(m.Text)
					text.WriteString(" ")
				}
			}
		}
	}

	return text.String()
}

// CountAudioTokenInput - Count audio tokens for input
// Duration-based: 100 tokens per minute
func CountAudioTokenInput(durationSeconds float64) int {
	return int(math.Ceil(durationSeconds/60.0) * 100)
}

// CountAudioTokenOutput - Count audio tokens for output
// Duration-based: 200 tokens per minute
func CountAudioTokenOutput(durationSeconds float64) int {
	return int(math.Ceil(durationSeconds/60.0) * 200)
}
