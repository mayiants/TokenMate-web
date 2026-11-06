package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/pkg/relay/base"
	"github.com/ai-proxy/ai-proxy-go/types"
)

// AzureAdaptor - Azure OpenAI API adapter
// Handles Azure-specific URL construction (deployments) and api-key auth.
type AzureAdaptor struct {
	*base.BaseAdaptor
	deployment string
}

// NewAzureAdaptor - Create Azure OpenAI adapter
// Expects cfg.BaseURL to contain the full deployment path:
//
//	https://{resource}.openai.azure.com/openai/deployments/{deployment-id}
//
// If only resource is provided, deployment is extracted or defaults to empty.
func NewAzureAdaptor(cfg *base.AdaptorConfig) *AzureAdaptor {
	baseURL := ""
	deployment := ""
	if cfg != nil {
		baseURL = cfg.BaseURL
	}
	if baseURL == "" {
		baseURL = "https://{resource}.openai.azure.com/openai/deployments/{deployment}"
	}

	// Extract deployment from BaseURL if present
	if idx := strings.LastIndex(baseURL, "/deployments/"); idx >= 0 {
		rest := baseURL[idx+len("/deployments/"):]
		if end := strings.Index(rest, "/"); end >= 0 {
			deployment = rest[:end]
			baseURL = baseURL[:idx+len("/deployments/")+end]
		} else {
			deployment = rest
		}
	}

	a := &AzureAdaptor{deployment: deployment}
	a.BaseAdaptor = base.NewBaseAdaptor("microsoft", &base.AdaptorConfig{
		BaseURL:    baseURL,
		APIKey:     cfg.APIKey,
		APIVersion: cfg.APIVersion,
		Models:     cfg.Models,
		AuthType:   "api-key",
	})
	a.SetEndpointResolver(a.GetEndpoint)
	return a
}

// GetRelayFormat - Get relay format
func (a *AzureAdaptor) GetRelayFormat() types.RelayFormat {
	return types.RelayFormatOpenAI
}

// GetHeaders - Azure uses api-key header, not Bearer
func (a *AzureAdaptor) GetHeaders() map[string]string {
	apiKey := ""
	if a.Config != nil {
		apiKey = a.Config.APIKey
	}
	return map[string]string{
		"Content-Type": "application/json",
		"api-key":      apiKey,
	}
}

// GetEndpoint - Azure OpenAI endpoint with api-version
func (a *AzureAdaptor) GetEndpoint(model string, stream bool) string {
	apiVersion := "2024-02-01"
	if a.Config != nil && a.Config.APIVersion != "" {
		apiVersion = a.Config.APIVersion
	}
	return "/chat/completions?api-version=" + apiVersion
}

// GetRequestURL - Build full Azure URL including deployment
func (a *AzureAdaptor) GetRequestURL(model string, stream bool) string {
	endpoint := a.GetEndpoint(model, stream)
	base := a.BaseURL

	// If deployment is not in BaseURL yet, try to append it from model
	if a.deployment == "" && model != "" {
		// Normalize: strip any provider prefix if present
		modelClean := model
		if dot := strings.Index(modelClean, "."); dot >= 0 {
			modelClean = modelClean[dot+1:]
		}
		base = strings.TrimSuffix(base, "/") + "/openai/deployments/" + modelClean
	}

	return base + endpoint
}

// ToProvider - Azure accepts OpenAI-compatible format directly
func (a *AzureAdaptor) ToProvider(req dto.GeneralOpenAIRequest) interface{} {
	// Azure supports the same format; ensure stream options for usage
	if req.Stream != nil && *req.Stream {
		req.StreamOptions = &dto.StreamOptions{
			IncludeUsage: true,
		}
	}
	return req
}

// ParseResponse - Parse upstream response body into native struct
func (a *AzureAdaptor) ParseResponse(body []byte) (interface{}, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty response body")
	}
	var resp dto.OpenAITextResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// FromProvider - Parse Azure OpenAI response (OpenAI-compatible)
func (a *AzureAdaptor) FromProvider(response interface{}, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	switch v := response.(type) {
	case *dto.OpenAITextResponse:
		return v
	case dto.OpenAITextResponse:
		return &v
	case []byte:
		data := v
		if len(data) == 0 {
			return nil
		}

		var resp dto.OpenAITextResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			var errResp struct {
				Error interface{} `json:"error"`
			}
			if err2 := json.Unmarshal(data, &errResp); err2 == nil && errResp.Error != nil {
				resp.Error = errResp.Error
				return &resp
			}
			return nil
		}
		return &resp
	default:
		return nil
	}
}

// ExtractUsage - Extract usage from Azure response
func (a *AzureAdaptor) ExtractUsage(response interface{}) *dto.Usage {
	switch v := response.(type) {
	case *dto.OpenAITextResponse:
		return &v.Usage
	case dto.OpenAITextResponse:
		return &v.Usage
	case []byte:
		var resp dto.OpenAITextResponse
		if err := json.Unmarshal(v, &resp); err != nil {
			return &dto.Usage{}
		}
		return &resp.Usage
	default:
		return &dto.Usage{}
	}
}

// TransformStreamChunk - Transform streaming chunk
func (a *AzureAdaptor) TransformStreamChunk(chunk interface{}, index int) interface{} {
	data, ok := chunk.([]byte)
	if !ok {
		return nil
	}
	if string(data) == "[DONE]" {
		return nil
	}
	var streamResp dto.ChatCompletionsStreamResponse
	if err := json.Unmarshal(data, &streamResp); err != nil {
		return nil
	}
	return &streamResp
}
