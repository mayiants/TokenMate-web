package relay

import (
	"encoding/json"
	"sync"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/relay/base"
	"github.com/mayiants/ai-proxy-go/pkg/relay/channel/aws"
	"github.com/mayiants/ai-proxy-go/pkg/relay/channel/azure"
	"github.com/mayiants/ai-proxy-go/pkg/relay/channel/claude"
	"github.com/mayiants/ai-proxy-go/pkg/relay/channel/gemini"
	"github.com/mayiants/ai-proxy-go/pkg/relay/channel/openai"
	"github.com/mayiants/ai-proxy-go/types"
)

// OpenAICompatibleAdaptor - OpenAI-compatible adapter for other providers
type OpenAICompatibleAdaptor struct {
	*base.BaseAdaptor
	providerName string
}

// NewOpenAICompatibleAdaptor - Create OpenAI-compatible adapter
func NewOpenAICompatibleAdaptor(providerName string, cfg *base.AdaptorConfig) *OpenAICompatibleAdaptor {
	a := &OpenAICompatibleAdaptor{providerName: providerName}
	a.BaseAdaptor = base.NewBaseAdaptor(providerName, cfg)
	a.SetEndpointResolver(a.GetEndpoint)
	return a
}

// GetRelayFormat - Get relay format
func (a *OpenAICompatibleAdaptor) GetRelayFormat() types.RelayFormat {
	return types.RelayFormatOpenAI
}

// ParseResponse - Parse upstream response body into native struct
func (a *OpenAICompatibleAdaptor) ParseResponse(body []byte) (interface{}, error) {
	var resp dto.OpenAITextResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// FromProvider - Convert response from provider format (handles []byte and *dto.OpenAITextResponse)
func (a *OpenAICompatibleAdaptor) FromProvider(response interface{}, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	switch v := response.(type) {
	case *dto.OpenAITextResponse:
		return v
	case dto.OpenAITextResponse:
		return &v
	case []byte:
		var resp dto.OpenAITextResponse
		if err := json.Unmarshal(v, &resp); err != nil {
			return nil
		}
		return &resp
	default:
		// Try base implementation for already parsed responses
		return a.BaseAdaptor.FromProvider(response, req)
	}
}

// ExtractUsage - Extract usage from response (handles []byte and *dto.OpenAITextResponse)
func (a *OpenAICompatibleAdaptor) ExtractUsage(response interface{}) *dto.Usage {
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
		return a.BaseAdaptor.ExtractUsage(response)
	}
}

// Re-export adapter constructors for convenience
var (
	NewOpenAIAdaptor = openai.NewOpenAIAdaptor
	NewClaudeAdaptor = claude.NewClaudeAdaptor
	NewGeminiAdaptor = gemini.NewGeminiAdaptor
	NewAzureAdaptor  = azure.NewAzureAdaptor
	NewAWSAdaptor    = aws.NewAWSAdaptor
)

// ProviderConfig - Provider configuration
type ProviderConfig struct {
	Name     string
	BaseURL  string
	AuthType string // "bearer", "x-api-key", "query"
	APIKey   string
	Models   []string
}

// DefaultProviderConfigs - Default provider configurations
// 18 providers aligned with ai-portal-go database
// BedrockRegions — AWS Bedrock 商业区域列表（33 个）
// 每个区域对应一个 provider：amazon-{region}
// 保留 amazon 作为 us-east-1 的通用别名（向后兼容）
var BedrockRegions = []string{
	"us-east-1", "us-east-2", "us-west-1", "us-west-2",
	"ca-central-1", "ca-west-1",
	"eu-central-1", "eu-central-2", "eu-north-1", "eu-south-1", "eu-south-2",
	"eu-west-1", "eu-west-2", "eu-west-3",
	"ap-east-2",
	"ap-northeast-1", "ap-northeast-2", "ap-northeast-3",
	"ap-south-1", "ap-south-2",
	"ap-southeast-1", "ap-southeast-2", "ap-southeast-3", "ap-southeast-4",
	"ap-southeast-5", "ap-southeast-6", "ap-southeast-7",
	"il-central-1",
	"me-central-1", "me-south-1",
	"af-south-1",
	"sa-east-1",
	"mx-central-1",
}

// MantleRegions — AWS Bedrock Mantle 区域列表（13 个）
// Mantle 提供 OpenAI-compatible API（Responses / Chat Completions / Anthropic Messages）
// 认证方式：Bearer Token（Bedrock API Key），不是 SigV4
var MantleRegions = []string{
	"us-east-1", "us-east-2", "us-west-2",
	"ap-northeast-1", "ap-south-1", "ap-southeast-2", "ap-southeast-3",
	"eu-central-1", "eu-north-1", "eu-south-1", "eu-west-1", "eu-west-2",
	"sa-east-1",
}

// GetMantleProviderNames returns the list of registered mantle provider names.
func GetMantleProviderNames() []string {
	var names []string
	for _, region := range MantleRegions {
		names = append(names, "amazon-mantle-"+region)
	}
	return names
}

func init() {
	// Auto-register per-region Bedrock Runtime providers: amazon-{region}
	for _, region := range BedrockRegions {
		providerName := "amazon-" + region
		DefaultProviderConfigs[providerName] = ProviderConfig{
			Name:     providerName,
			BaseURL:  "https://bedrock-runtime." + region + ".amazonaws.com",
			AuthType: "aws",
		}
	}
	// Auto-register per-region Bedrock Mantle providers: amazon-mantle-{region}
	for _, region := range MantleRegions {
		providerName := "amazon-mantle-" + region
		DefaultProviderConfigs[providerName] = ProviderConfig{
			Name:     providerName,
			BaseURL:  "https://bedrock-mantle." + region + ".api.aws/v1",
			AuthType: "bearer",
		}
	}
}

var DefaultProviderConfigs = map[string]ProviderConfig{
	// === International Providers ===
	"openai": {
		Name:     "openai",
		BaseURL:  "https://api.openai.com/v1",
		AuthType: "bearer",
	},
	"anthropic": {
		Name:     "anthropic",
		BaseURL:  "https://api.anthropic.com/v1",
		AuthType: "x-api-key",
	},
	"google": {
		Name:     "google",
		BaseURL:  "https://generativelanguage.googleapis.com/v1beta",
		AuthType: "query",
	},
	"meta": {
		Name:     "meta",
		BaseURL:  "https://api.llama.com/compat/v1",
		AuthType: "bearer",
	},
	"mistral": {
		Name:     "mistral",
		BaseURL:  "https://api.mistral.ai/v1",
		AuthType: "bearer",
	},
	"cohere": {
		Name:     "cohere",
		BaseURL:  "https://api.cohere.ai/compatibility/v1",
		AuthType: "bearer",
	},
	// amazon — 通用别名，默认指向 us-east-1（向后兼容）
	"amazon": {
		Name:     "amazon",
		BaseURL:  "https://bedrock-runtime.us-east-1.amazonaws.com",
		AuthType: "aws",
	},
	"microsoft": {
		Name:     "microsoft",
		BaseURL:  "https://{resource}.openai.azure.com/openai/deployments/{deployment}",
		AuthType: "api-key",
	},
	"xai": {
		Name:     "xai",
		BaseURL:  "https://api.x.ai/v1",
		AuthType: "bearer",
	},
	"perplexity": {
		Name:     "perplexity",
		BaseURL:  "https://api.perplexity.ai",
		AuthType: "bearer",
	},
	// === Domestic (China) Providers ===
	"deepseek": {
		Name:     "deepseek",
		BaseURL:  "https://api.deepseek.com/v1",
		AuthType: "bearer",
	},
	"alibaba": {
		Name:     "alibaba",
		BaseURL:  "https://dashscope.aliyuncs.com/compatible-mode/v1",
		AuthType: "bearer",
	},
	"moonshot": {
		Name:     "moonshot",
		BaseURL:  "https://api.moonshot.cn/v1",
		AuthType: "bearer",
	},
	"zhipu": {
		Name:     "zhipu",
		BaseURL:  "https://open.bigmodel.cn/api/paas/v4",
		AuthType: "bearer",
	},
	"bytedance": {
		Name:     "bytedance",
		BaseURL:  "https://ark.cn-beijing.volces.com/api/v3",
		AuthType: "bearer",
	},
	"tencent": {
		Name:     "tencent",
		BaseURL:  "https://api.hunyuan.cloud.tencent.com/v1",
		AuthType: "bearer",
	},
	"minimax": {
		Name:     "minimax",
		BaseURL:  "https://api.minimax.chat/v1",
		AuthType: "bearer",
	},
	"baidu": {
		Name:     "baidu",
		BaseURL:  "https://qianfan.baidubce.com/v2",
		AuthType: "bearer",
	},
	"groq": {
		Name:     "groq",
		BaseURL:  "https://api.groq.com/openai/v1",
		AuthType: "bearer",
	},
	"xiaomi": {
		Name:     "xiaomi",
		BaseURL:  "https://api.xiaomimimo.com/v1",
		AuthType: "bearer",
	},
	"xunfei": {
		Name:     "xunfei",
		BaseURL:  "https://maas-coding-api.cn-huabei-1.xf-yun.com/v2",
		AuthType: "bearer",
	},
	"accessapi": {
		Name:     "accessapi",
		BaseURL:  "https://accessapi.ai/v1",
		AuthType: "bearer",
	},
}

// providerConfigsMu guards DefaultProviderConfigs. The package init() populates
// the map before main runs (single-threaded, no lock needed), but
// RegisterProvider can mutate it at runtime, concurrent with GetProviderConfig
// / SupportedProviders / IsProviderSupported reads — so all runtime accessors
// take the lock.
var providerConfigsMu sync.RWMutex

// GetProviderConfig - Get provider config by name.
// For unknown providers, returns an OpenAI-compatible config since third-party
// providers (e.g. accessapi) typically use the OpenAI API format.
func GetProviderConfig(name string) (ProviderConfig, bool) {
	providerConfigsMu.RLock()
	cfg, ok := DefaultProviderConfigs[name]
	providerConfigsMu.RUnlock()
	if ok {
		return cfg, true
	}
	// Unknown provider → OpenAI-compatible (base URL comes from upstream key)
	return ProviderConfig{
		Name:     name,
		BaseURL:  "",
		AuthType: "bearer",
	}, true
}

// RegisterProvider - Register custom provider
func RegisterProvider(name string, cfg ProviderConfig) {
	providerConfigsMu.Lock()
	DefaultProviderConfigs[name] = cfg
	providerConfigsMu.Unlock()
}

// SupportedProviders - List supported providers
func SupportedProviders() []string {
	providerConfigsMu.RLock()
	providers := make([]string, 0, len(DefaultProviderConfigs))
	for name := range DefaultProviderConfigs {
		providers = append(providers, name)
	}
	providerConfigsMu.RUnlock()
	return providers
}

// IsProviderSupported - Check if provider is supported
func IsProviderSupported(name string) bool {
	providerConfigsMu.RLock()
	_, ok := DefaultProviderConfigs[name]
	providerConfigsMu.RUnlock()
	return ok
}

// ConvertRequest - Convert request between formats
func ConvertRequest(req dto.GeneralOpenAIRequest, targetFormat types.RelayFormat) interface{} {
	switch targetFormat {
	case types.RelayFormatClaude:
		adaptor := claude.NewClaudeAdaptor(&base.AdaptorConfig{})
		return adaptor.ToProvider(req)
	case types.RelayFormatGemini:
		adaptor := gemini.NewGeminiAdaptor(&base.AdaptorConfig{})
		return adaptor.ToProvider(req)
	default:
		return req
	}
}

// ConvertResponse - Convert response to OpenAI format
func ConvertResponse(response interface{}, sourceFormat types.RelayFormat, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	switch sourceFormat {
	case types.RelayFormatClaude:
		adaptor := claude.NewClaudeAdaptor(&base.AdaptorConfig{})
		return adaptor.FromProvider(response, req)
	case types.RelayFormatGemini:
		adaptor := gemini.NewGeminiAdaptor(&base.AdaptorConfig{})
		return adaptor.FromProvider(response, req)
	default:
		adaptor := openai.NewOpenAIAdaptor(&base.AdaptorConfig{})
		return adaptor.FromProvider(response, req)
	}
}
