package base

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/types"
)

// SharedHTTPClient returns the shared HTTP client for connection pooling.
func SharedHTTPClient() *http.Client {
	return sharedHTTPClient
}

// Shared HTTP client with connection pooling for all adapters.
//
// Timeout is 0 (no total timeout): http.Client.Timeout covers the entire
// response body read period, which would hard-cut SSE streams at 120s.
// Streaming callers (e.g. AWS sendSigned/ConverseStream, Claude streaming)
// rely on their request context for deadline control instead.
var sharedHTTPClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	},
}

// AdaptorConfig - Adapter configuration
type AdaptorConfig struct {
	BaseURL    string
	APIKey     string
	APIVersion string
	Models     []string
	AuthType   string // "bearer", "x-api-key", "query", "aws", "api-key"
	// ExtraHeaders are client headers that should be forwarded to the upstream,
	// after filtering (see internal/server/header_filter.go). They are merged
	// after adapter auth headers so that auth cannot be overridden.
	ExtraHeaders http.Header
}

// BaseAdaptor - Base adapter with common functionality
type BaseAdaptor struct {
	Config       *AdaptorConfig
	ProviderName string
	BaseURL      string

	// endpointResolver lets concrete adapters override GetEndpoint even when
	// BaseAdaptor methods are called via the embedded pointer.
	endpointResolver func(model string, stream bool) string
}

// SetEndpointResolver registers a resolver that returns the endpoint path for
// a given model and stream flag. It is used by GetRequestURL and DoRequestBytes
// so that embedded adapters can supply their own endpoint logic.
func (a *BaseAdaptor) SetEndpointResolver(fn func(model string, stream bool) string) {
	a.endpointResolver = fn
}

func (a *BaseAdaptor) resolveEndpoint(model string, stream bool) string {
	if a.endpointResolver != nil {
		return a.endpointResolver(model, stream)
	}
	return a.GetEndpoint(model, stream)
}

// NewBaseAdaptor - Create base adapter
func NewBaseAdaptor(providerName string, cfg *AdaptorConfig) *BaseAdaptor {
	baseURL := ""
	if cfg != nil {
		baseURL = cfg.BaseURL
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	return &BaseAdaptor{
		Config:       cfg,
		ProviderName: providerName,
		BaseURL:      baseURL,
	}
}

// GetProviderName - Get provider name
func (a *BaseAdaptor) GetProviderName() string {
	return a.ProviderName
}

// GetRelayFormat - Get relay format
func (a *BaseAdaptor) GetRelayFormat() types.RelayFormat {
	return types.RelayFormatOpenAI
}

// GetHeaders - Get default headers (auth-type aware)
func (a *BaseAdaptor) GetHeaders() map[string]string {
	apiKey := ""
	authType := "bearer"
	if a.Config != nil {
		apiKey = a.Config.APIKey
		if a.Config.AuthType != "" {
			authType = a.Config.AuthType
		}
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	switch authType {
	case "x-api-key":
		headers["x-api-key"] = apiKey
		// Also include Authorization for broader compatibility
		headers["Authorization"] = "Bearer " + apiKey
	case "api-key":
		headers["api-key"] = apiKey
	case "query":
		// API key sent as query parameter in URL, not header
		headers["Authorization"] = "Bearer " + apiKey
	case "aws":
		// AWS SigV4 is handled separately; fallback to Bearer for now
		headers["Authorization"] = "Bearer " + apiKey
	default: // "bearer" and everything else
		headers["Authorization"] = "Bearer " + apiKey
	}

	return headers
}

// GetEndpoint - Get API endpoint
func (a *BaseAdaptor) GetEndpoint(model string, stream bool) string {
	return "/chat/completions"
}

// GetRequestURL - Get full request URL
func (a *BaseAdaptor) GetRequestURL(model string, stream bool) string {
	endpoint := a.resolveEndpoint(model, stream)
	if len(endpoint) > 4 && endpoint[:4] == "http" {
		return endpoint
	}
	return a.BaseURL + endpoint
}

// DoRequest - Make HTTP request
func (a *BaseAdaptor) DoRequest(body interface{}, stream bool) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	return a.DoRequestBytes(jsonBody, stream)
}

// DoRequestBytes - Make HTTP request with an already-serialized JSON body.
// This is used by the plugin chain after it has rewritten the upstream request.
func (a *BaseAdaptor) DoRequestBytes(body []byte, stream bool) (*http.Response, error) {
	return a.DoRequestBytesWithContext(context.Background(), body, stream)
}

// DoRequestBytesWithContext - Make HTTP request with an already-serialized JSON
// body and a caller-supplied context. The context allows upstream requests to be
// cancelled when the client disconnects or a timeout fires.
func (a *BaseAdaptor) DoRequestBytesWithContext(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", a.GetRequestURL("", stream), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	for k, v := range a.GetHeaders() {
		req.Header.Set(k, v)
	}

	// Merge filtered client headers. These are appended after adapter auth so
	// that forwarding cannot overwrite Authorization/x-api-key injected above.
	// Auth-related headers are skipped defensively: even though the upstream
	// filter is expected to strip them, if it doesn't, Add would otherwise
	// produce duplicate Authorization / x-api-key headers on the wire.
	if a.Config != nil {
		for k, values := range a.Config.ExtraHeaders {
			switch http.CanonicalHeaderKey(k) {
			case "Authorization", "X-Api-Key":
				continue
			}
			for _, v := range values {
				req.Header.Add(k, v)
			}
		}
	}

	// Use shared HTTP client with connection pooling
	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// DoRequestStream - Make streaming request
func (a *BaseAdaptor) DoRequestStream(body interface{}) (*http.Response, error) {
	return a.DoRequest(body, true)
}

// ReadResponse - Read response body
func (a *BaseAdaptor) ReadResponse(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

// ParseError - Parse API error
func (a *BaseAdaptor) ParseError(data []byte) error {
	var errResp struct {
		Error *dto.OpenAITextResponse `json:"error"`
	}
	if err := json.Unmarshal(data, &errResp); err != nil {
		return fmt.Errorf("failed to parse error: %w", err)
	}
	if errResp.Error != nil {
		return fmt.Errorf("API error: %v", errResp.Error)
	}
	return nil
}

// ToProvider - Default implementation (identity transform)
func (a *BaseAdaptor) ToProvider(req dto.GeneralOpenAIRequest) interface{} {
	return req
}

// ParseResponse - Default implementation (no-op)
func (a *BaseAdaptor) ParseResponse(body []byte) (interface{}, error) {
	return nil, nil
}

// FromProvider - Default implementation
func (a *BaseAdaptor) FromProvider(response interface{}, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	switch v := response.(type) {
	case *dto.OpenAITextResponse:
		return v
	case dto.OpenAITextResponse:
		return &v
	default:
		return nil
	}
}

// TransformStreamChunk - Default streaming chunk transform
func (a *BaseAdaptor) TransformStreamChunk(chunk interface{}, index int) interface{} {
	return chunk
}

// ExtractUsage - Default usage extraction
func (a *BaseAdaptor) ExtractUsage(response interface{}) *dto.Usage {
	switch v := response.(type) {
	case *dto.OpenAITextResponse:
		return &v.Usage
	case dto.OpenAITextResponse:
		return &v.Usage
	case map[string]interface{}:
		if usage, ok := v["usage"].(map[string]interface{}); ok {
			return &dto.Usage{
				PromptTokens:     getInt(usage, "prompt_tokens"),
				CompletionTokens: getInt(usage, "completion_tokens"),
				TotalTokens:      getInt(usage, "total_tokens"),
			}
		}
	}
	return &dto.Usage{}
}

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}
