package openai

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/relay/base"
	"github.com/mayiants/ai-proxy-go/types"
)

// OpenAIAdaptor - OpenAI API adapter
type OpenAIAdaptor struct {
	*base.BaseAdaptor
}

// NewOpenAIAdaptor - Create OpenAI adapter
func NewOpenAIAdaptor(cfg *base.AdaptorConfig) *OpenAIAdaptor {
	baseURL := ""
	if cfg != nil {
		baseURL = cfg.BaseURL
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	a := &OpenAIAdaptor{}
	a.BaseAdaptor = base.NewBaseAdaptor("openai", &base.AdaptorConfig{
		BaseURL: baseURL,
		APIKey:  cfg.APIKey,
		Models:  cfg.Models,
	})
	a.SetEndpointResolver(a.GetEndpoint)
	return a
}

// GetRelayFormat - Get relay format
func (a *OpenAIAdaptor) GetRelayFormat() types.RelayFormat {
	return types.RelayFormatOpenAI
}

// GetEndpoint - Get OpenAI endpoint
func (a *OpenAIAdaptor) GetEndpoint(model string, stream bool) string {
	return "/chat/completions"
}

// ToProvider - Transform to OpenAI format (identity)
func (a *OpenAIAdaptor) ToProvider(req dto.GeneralOpenAIRequest) interface{} {
	// stream_options injection is now handled by the plugin chain so it can be
	// toggled via configuration. The adapter no longer mutates the request here.
	return req
}

// ParseResponse - Parse upstream response body into native struct
func (a *OpenAIAdaptor) ParseResponse(body []byte) (interface{}, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty response body")
	}
	var resp dto.OpenAITextResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// FromProvider - Transform OpenAI response
func (a *OpenAIAdaptor) FromProvider(response interface{}, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	switch v := response.(type) {
	case *dto.OpenAITextResponse:
		if v.ID == "" && len(v.Choices) == 0 {
			return nil
		}
		return v
	case dto.OpenAITextResponse:
		if v.ID == "" && len(v.Choices) == 0 {
			return nil
		}
		return &v
	case []byte:
		data := v
		// Debug: Check if data is valid JSON
		if len(data) == 0 {
			return nil
		}

		var resp dto.OpenAITextResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			// Try to parse as error response
			var errResp struct {
				Error interface{} `json:"error"`
			}
			if err2 := json.Unmarshal(data, &errResp); err2 == nil && errResp.Error != nil {
				// It's an error response, return it as-is
				resp.Error = errResp.Error
				return &resp
			}
			return nil
		}

		// Debug: Check if response has valid data
		if resp.ID == "" && len(resp.Choices) == 0 {
			return nil
		}

		return &resp
	default:
		return nil
	}
}

// ExtractUsage - Extract usage from OpenAI response
func (a *OpenAIAdaptor) ExtractUsage(response interface{}) *dto.Usage {
	var usage *dto.Usage
	switch v := response.(type) {
	case *dto.OpenAITextResponse:
		usage = &v.Usage
	case dto.OpenAITextResponse:
		usage = &v.Usage
	case []byte:
		var resp dto.OpenAITextResponse
		if err := json.Unmarshal(v, &resp); err != nil {
			return &dto.Usage{}
		}
		usage = &resp.Usage
	default:
		return &dto.Usage{}
	}

	// Check for cache tokens in prompt_tokens_details
	if usage.PromptTokensDetails.CachedTokens > 0 {
		// OpenAI prompt caching
		usage.PromptCacheHitTokens = usage.PromptTokensDetails.CachedTokens
	}

	return usage
}

// TransformStreamChunk - Transform streaming chunk
func (a *OpenAIAdaptor) TransformStreamChunk(chunk interface{}, index int) interface{} {
	data, ok := chunk.([]byte)
	if !ok {
		return nil
	}

	// Handle [DONE] marker
	if string(data) == "[DONE]" {
		return nil
	}

	var streamResp dto.ChatCompletionsStreamResponse
	if err := json.Unmarshal(data, &streamResp); err != nil {
		return nil
	}

	return &streamResp
}

// GenerateID - Generate unique response ID
func GenerateID() string {
	return fmt.Sprintf("chatcmpl-%d-%s", time.Now().UnixNano()/1e6, randomString(9))
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	randBytes := make([]byte, n)
	_, _ = rand.Read(randBytes)
	for i := range b {
		b[i] = letters[int(randBytes[i])%len(letters)]
	}
	return string(b)
}
