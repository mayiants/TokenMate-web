package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/relay/base"
	"github.com/mayiants/ai-proxy-go/types"
)

// GeminiAdaptor - Google Gemini API adapter
type GeminiAdaptor struct {
	*base.BaseAdaptor
	model string // captured in ToProvider for use by DoRequest
}

// NewGeminiAdaptor - Create Gemini adapter
func NewGeminiAdaptor(cfg *base.AdaptorConfig) *GeminiAdaptor {
	baseURL := ""
	if cfg != nil {
		baseURL = cfg.BaseURL
	}
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta"
	}

	a := &GeminiAdaptor{}
	a.BaseAdaptor = base.NewBaseAdaptor("google", &base.AdaptorConfig{
		BaseURL: baseURL,
		APIKey:  cfg.APIKey,
		Models:  cfg.Models,
	})
	a.SetEndpointResolver(a.GetEndpoint)
	return a
}

// GetRelayFormat - Get relay format
func (a *GeminiAdaptor) GetRelayFormat() types.RelayFormat {
	return types.RelayFormatGemini
}

// GetHeaders - Get Gemini headers
func (a *GeminiAdaptor) GetHeaders() map[string]string {
	apiKey := ""
	if a.Config != nil {
		apiKey = a.Config.APIKey
	}
	return map[string]string{
		"Content-Type":   "application/json",
		"x-goog-api-key": apiKey,
	}
}

// GetEndpoint - Get Gemini endpoint
func (a *GeminiAdaptor) GetEndpoint(model string, stream bool) string {
	method := "generateContent"
	if stream {
		method = "streamGenerateContent"
	}
	return fmt.Sprintf("/models/%s:%s", model, method)
}

// GetRequestURL - Get full request URL (no API key in query to prevent leak in logs)
func (a *GeminiAdaptor) GetRequestURL(model string, stream bool) string {
	endpoint := a.GetEndpoint(model, stream)
	return fmt.Sprintf("%s%s", a.BaseURL, endpoint)
}

// DoRequest - Make HTTP request using Gemini-specific URL and headers
func (a *GeminiAdaptor) DoRequest(body interface{}, stream bool) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	return a.DoRequestBytes(jsonBody, stream)
}

// DoRequestBytes - Make HTTP request using Gemini-specific URL and headers with a pre-serialized body.
func (a *GeminiAdaptor) DoRequestBytes(body []byte, stream bool) (*http.Response, error) {
	return a.DoRequestBytesWithContext(context.Background(), body, stream)
}

// DoRequestBytesWithContext - Make HTTP request using Gemini-specific URL and
// headers with a pre-serialized body and a caller-supplied context.
func (a *GeminiAdaptor) DoRequestBytesWithContext(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", a.GetRequestURL(a.model, stream), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for k, v := range a.GetHeaders() {
		req.Header.Set(k, v)
	}

	resp, err := base.SharedHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return resp, nil
}

// ReadResponse - Read response body
func (a *GeminiAdaptor) ReadResponse(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// GeminiRequest - Gemini API request format
type GeminiRequest struct {
	Contents         []GeminiContent   `json:"contents"`
	GenerationConfig *GenerationConfig `json:"generationConfig,omitempty"`
	SafetySettings   []SafetySetting   `json:"safetySettings,omitempty"`
}

// GeminiContent - Gemini content
type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart - Gemini part
type GeminiPart struct {
	Text         string              `json:"text,omitempty"`
	InlineData   *GeminiInlineData   `json:"inlineData,omitempty"`
	FunctionCall *GeminiFunctionCall `json:"functionCall,omitempty"`
}

// GeminiInlineData - Inline data
type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// GeminiFunctionCall - Function call
type GeminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// GenerationConfig - Generation config
type GenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	TopK            *int     `json:"topK,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

// SafetySetting - Safety setting
type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// GeminiResponse - Gemini API response
type GeminiResponse struct {
	Candidates    []GeminiCandidate    `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
	Error         *GeminiError         `json:"error,omitempty"`
}

// GeminiCandidate - Gemini candidate
type GeminiCandidate struct {
	Content       GeminiContent  `json:"content"`
	FinishReason  string         `json:"finishReason"`
	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`
}

// SafetyRating - Safety rating
type SafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
}

// GeminiUsageMetadata - Usage metadata
type GeminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// GeminiError - Gemini error
type GeminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// ToProvider - Transform OpenAI request to Gemini format
func (a *GeminiAdaptor) ToProvider(req dto.GeneralOpenAIRequest) interface{} {
	a.model = req.Model
	geminiReq := &GeminiRequest{
		Contents: make([]GeminiContent, 0),
	}

	// Convert messages
	for _, msg := range req.Messages {
		content := convertToGeminiContent(msg)
		geminiReq.Contents = append(geminiReq.Contents, content)
	}

	// Set generation config
	geminiReq.GenerationConfig = &GenerationConfig{}
	if req.Temperature != nil {
		geminiReq.GenerationConfig.Temperature = req.Temperature
	}
	if req.TopP != nil {
		geminiReq.GenerationConfig.TopP = req.TopP
	}
	if req.TopK != nil {
		geminiReq.GenerationConfig.TopK = req.TopK
	}
	if req.MaxTokens != nil {
		geminiReq.GenerationConfig.MaxOutputTokens = req.MaxTokens
	}
	if req.Stop != nil {
		switch v := req.Stop.(type) {
		case string:
			geminiReq.GenerationConfig.StopSequences = []string{v}
		case []interface{}:
			for _, s := range v {
				if str, ok := s.(string); ok {
					geminiReq.GenerationConfig.StopSequences = append(geminiReq.GenerationConfig.StopSequences, str)
				}
			}
		}
	}

	return geminiReq
}

// ParseResponse - Parse upstream response body into native struct
func (a *GeminiAdaptor) ParseResponse(body []byte) (interface{}, error) {
	var geminiResp GeminiResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return nil, err
	}
	return &geminiResp, nil
}

// FromProvider - Transform Gemini response to OpenAI format
func (a *GeminiAdaptor) FromProvider(response interface{}, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	var geminiResp *GeminiResponse
	switch v := response.(type) {
	case *GeminiResponse:
		geminiResp = v
	case GeminiResponse:
		geminiResp = &v
	case []byte:
		geminiResp = &GeminiResponse{}
		if err := json.Unmarshal(v, geminiResp); err != nil {
			return nil
		}
	default:
		return nil
	}

	// Handle error
	if geminiResp.Error != nil {
		return &dto.OpenAITextResponse{
			ID:      fmt.Sprintf("gemini-%d", time.Now().Unix()),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []dto.OpenAITextResponseChoice{},
			Usage:   dto.Usage{},
		}
	}

	// Convert to OpenAI format
	openaiResp := &dto.OpenAITextResponse{
		ID:      fmt.Sprintf("gemini-%d", time.Now().Unix()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: make([]dto.OpenAITextResponseChoice, 0),
		Usage:   convertGeminiUsage(geminiResp.UsageMetadata),
	}

	// Convert candidates
	for i, candidate := range geminiResp.Candidates {
		text := ""
		for _, part := range candidate.Content.Parts {
			text += part.Text
		}

		openaiResp.Choices = append(openaiResp.Choices, dto.OpenAITextResponseChoice{
			Index: i,
			Message: dto.Message{
				Role:    "assistant",
				Content: text,
			},
			FinishReason: mapGeminiFinishReason(candidate.FinishReason),
		})
	}

	return openaiResp
}

// ExtractUsage - Extract usage from Gemini response
func (a *GeminiAdaptor) ExtractUsage(response interface{}) *dto.Usage {
	var geminiResp *GeminiResponse
	switch v := response.(type) {
	case *GeminiResponse:
		geminiResp = v
	case GeminiResponse:
		geminiResp = &v
	case []byte:
		geminiResp = &GeminiResponse{}
		if err := json.Unmarshal(v, geminiResp); err != nil {
			return &dto.Usage{}
		}
	default:
		return &dto.Usage{}
	}

	usage := convertGeminiUsage(geminiResp.UsageMetadata)
	return &usage
}

// convertGeminiUsage - Convert Gemini usage to OpenAI format
func convertGeminiUsage(meta *GeminiUsageMetadata) dto.Usage {
	if meta == nil {
		return dto.Usage{}
	}

	usage := dto.Usage{
		PromptTokens:     meta.PromptTokenCount,
		CompletionTokens: meta.CandidatesTokenCount,
		TotalTokens:      meta.TotalTokenCount,
	}

	// Cache tokens
	if meta.CachedContentTokenCount > 0 {
		usage.PromptTokensDetails.CachedTokens = meta.CachedContentTokenCount
		usage.PromptCacheHitTokens = meta.CachedContentTokenCount
	}

	return usage
}

// mapGeminiFinishReason - Map Gemini finish reason
func mapGeminiFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	case "RECITATION":
		return "content_filter"
	default:
		return reason
	}
}

// convertToGeminiContent - Convert OpenAI message to Gemini format
func convertToGeminiContent(msg dto.Message) GeminiContent {
	content := GeminiContent{
		Role:  msg.Role,
		Parts: make([]GeminiPart, 0),
	}

	// Map role
	if msg.Role == "assistant" {
		content.Role = "model"
	}

	// Convert content
	switch v := msg.Content.(type) {
	case string:
		content.Parts = append(content.Parts, GeminiPart{Text: v})
	case []dto.MediaContent:
		for _, m := range v {
			if m.Type == "text" {
				content.Parts = append(content.Parts, GeminiPart{Text: m.Text})
			} else if m.Type == "image_url" && m.ImageURL != nil {
				// Convert image to Gemini inlineData format
				url := m.ImageURL.URL
				if strings.HasPrefix(url, "data:") {
					// Parse data URI: data:<mediatype>;base64,<data>
					mimeType := "image/jpeg"
					data := url
					if idx := strings.Index(url, ";base64,"); idx > 0 {
						mimeType = url[5:idx]
						data = url[idx+8:]
					}
					content.Parts = append(content.Parts, GeminiPart{
						InlineData: &GeminiInlineData{
							MimeType: mimeType,
							Data:     data,
						},
					})
				} else {
					// HTTP URL — Gemini inlineData requires base64, pass URL as text fallback
					// (real implementation would fetch the image and encode it)
					content.Parts = append(content.Parts, GeminiPart{
						Text: fmt.Sprintf("[Image: %s]", url),
					})
				}
			}
		}
	}

	return content
}
