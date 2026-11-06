package dto

import (
	"encoding/json"
)

// ClaudeRequest - Claude API request format
type ClaudeRequest struct {
	Model         string          `json:"model"`
	Messages      []ClaudeMessage `json:"messages"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	Stream        *bool           `json:"stream,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	System        interface{}     `json:"system,omitempty"`
	Tools         []interface{}   `json:"tools,omitempty"`
	ToolChoice    interface{}     `json:"tool_choice,omitempty"`
	Thinking      *Thinking       `json:"thinking,omitempty"`
	OutputConfig  json.RawMessage `json:"output_config,omitempty"`
}

// ClaudeMessage - Claude message format
type ClaudeMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// ClaudeContent - Claude content block
type ClaudeContent struct {
	Type         string        `json:"type"`
	Text         *string       `json:"text,omitempty"`
	Source       *ClaudeSource `json:"source,omitempty"`
	ID           string        `json:"id,omitempty"`
	Name         string        `json:"name,omitempty"`
	Input        interface{}   `json:"input,omitempty"`
	ToolUseID    string        `json:"tool_use_id,omitempty"`
	Content      interface{}   `json:"content,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ClaudeSource - Claude media source
type ClaudeSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url,omitempty"`
}

// Thinking - Claude extended thinking
type Thinking struct {
	Type         string `json:"type"`
	BudgetTokens *int   `json:"budget_tokens,omitempty"`
	Display      string `json:"display,omitempty"`
}

// ClaudeResponse - Claude API response format
type ClaudeResponse struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Content      []ClaudeContent `json:"content"`
	Model        string          `json:"model"`
	StopReason   string          `json:"stop_reason,omitempty"`
	StopSequence string          `json:"stop_sequence,omitempty"`
	Usage        ClaudeUsage     `json:"usage"`
}

// ClaudeUsage - Claude usage format
type ClaudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`

	// Cache tokens
	CacheCreationInputTokens int                  `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                  `json:"cache_read_input_tokens,omitempty"`
	CacheCreation            *ClaudeCacheCreation `json:"cache_creation,omitempty"`
}

// ClaudeCacheCreation - Detailed cache creation tokens
type ClaudeCacheCreation struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens,omitempty"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens,omitempty"`
}

// ClaudeStreamEvent - Claude streaming event
type ClaudeStreamEvent struct {
	Type         string          `json:"type"`
	Index        *int            `json:"index,omitempty"`
	Delta        *ClaudeDelta    `json:"delta,omitempty"`
	ContentBlock *ClaudeContent  `json:"content_block,omitempty"`
	Message      *ClaudeResponse `json:"message,omitempty"`
	Usage        *ClaudeUsage    `json:"usage,omitempty"`
}

// ClaudeDelta - Claude streaming delta
type ClaudeDelta struct {
	Type         string `json:"type,omitempty"`
	Text         string `json:"text,omitempty"`
	PartialJSON  string `json:"partial_json,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
}

// ClaudeWebSearchTool - Claude web search tool
type ClaudeWebSearchTool struct {
	Type         string                       `json:"type"`
	Name         string                       `json:"name"`
	MaxUses      int                          `json:"max_uses,omitempty"`
	UserLocation *ClaudeWebSearchUserLocation `json:"user_location,omitempty"`
}

// ClaudeWebSearchUserLocation - User location for web search
type ClaudeWebSearchUserLocation struct {
	Type     string `json:"type"`
	Timezone string `json:"timezone,omitempty"`
	Country  string `json:"country,omitempty"`
	Region   string `json:"region,omitempty"`
	City     string `json:"city,omitempty"`
}
