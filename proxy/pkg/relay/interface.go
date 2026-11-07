package relay

import (
	"context"
	"net/http"
	"strings"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/relay/base"
	"github.com/mayiants/ai-proxy-go/pkg/relay/channel/aws"
	"github.com/mayiants/ai-proxy-go/pkg/relay/channel/azure"
	"github.com/mayiants/ai-proxy-go/types"
)

// Adaptor - Provider adapter interface
type Adaptor interface {
	// Request transformation
	ToProvider(req dto.GeneralOpenAIRequest) interface{}
	FromProvider(response interface{}, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse

	// Streaming
	TransformStreamChunk(chunk interface{}, index int) interface{}

	// Response parsing and usage extraction
	ParseResponse(body []byte) (interface{}, error)
	ExtractUsage(response interface{}) *dto.Usage

	// HTTP
	GetHeaders() map[string]string
	GetEndpoint(model string, stream bool) string
	GetRequestURL(model string, stream bool) string
	DoRequest(body interface{}, stream bool) (*http.Response, error)
	DoRequestBytes(body []byte, stream bool) (*http.Response, error)
	DoRequestBytesWithContext(ctx context.Context, body []byte, stream bool) (*http.Response, error)
	ReadResponse(resp *http.Response) ([]byte, error)

	// Info
	GetRelayFormat() types.RelayFormat
	GetProviderName() string
}

// AdaptorConfig - Adapter configuration (alias to base package)
type AdaptorConfig = base.AdaptorConfig

// GetAdaptor - Create adapter by provider name (company name)
// 18 providers: openai, anthropic, google, meta, mistral, cohere, amazon, microsoft, xai, perplexity,
//
//	deepseek, alibaba, moonshot, zhipu, bytedance, tencent, minimax, baidu
func GetAdaptor(provider string, cfg *base.AdaptorConfig) Adaptor {
	// Auto-populate AuthType from default provider configs if not set
	if cfg != nil && cfg.AuthType == "" {
		if pc, ok := DefaultProviderConfigs[provider]; ok {
			cfg.AuthType = pc.AuthType
		}
	}

	switch provider {
	case "openai", "azure":
		return NewOpenAIAdaptor(cfg)
	case "anthropic":
		return NewClaudeAdaptor(cfg)
	case "google":
		return NewGeminiAdaptor(cfg)
	case "deepseek":
		return NewOpenAICompatibleAdaptor("deepseek", cfg)
	case "alibaba":
		return NewOpenAICompatibleAdaptor("alibaba", cfg)
	case "moonshot":
		return NewOpenAICompatibleAdaptor("moonshot", cfg)
	case "zhipu":
		return NewOpenAICompatibleAdaptor("zhipu", cfg)
	case "meta", "mistral", "cohere", "xai", "perplexity",
		"bytedance", "tencent", "minimax", "baidu":
		return NewOpenAICompatibleAdaptor(provider, cfg)
	case "microsoft":
		return azure.NewAzureAdaptor(cfg)
	case "amazon":
		return aws.NewAWSAdaptor(cfg)
	default:
		if strings.HasPrefix(provider, "amazon-mantle-") {
			return NewOpenAICompatibleAdaptor(provider, cfg)
		}
		if strings.HasPrefix(provider, "amazon-") {
			return aws.NewAWSAdaptor(cfg)
		}
		return NewOpenAICompatibleAdaptor(provider, cfg)
	}
}

// GetProviderFromModel - Determine provider (company) from model name
// Returns the company name, not the product name
// Convention: Company + Product (e.g., Anthropic + Claude, Google + Gemini, Ali + Qwen)
func GetProviderFromModel(model string) string {
	modelLower := strings.ToLower(model)

	// OpenAI models: gpt-*, o1-*, o3-*, o4-*, dall-e-*, whisper-*, tts-*
	if strings.HasPrefix(modelLower, "gpt") ||
		strings.HasPrefix(modelLower, "o1-") ||
		strings.HasPrefix(modelLower, "o3-") ||
		strings.HasPrefix(modelLower, "o4-") ||
		strings.HasPrefix(modelLower, "dall-e") ||
		strings.HasPrefix(modelLower, "whisper") ||
		strings.HasPrefix(modelLower, "tts") ||
		strings.HasPrefix(modelLower, "text-embedding") ||
		strings.HasPrefix(modelLower, "text-davinci") {
		return "openai"
	}

	// Anthropic models: claude-*
	if strings.HasPrefix(modelLower, "claude") {
		return "anthropic"
	}

	// Google models: gemini-*, palm-*
	if strings.HasPrefix(modelLower, "gemini") ||
		strings.HasPrefix(modelLower, "palm") {
		return "google"
	}

	// DeepSeek models: deepseek-*
	if strings.HasPrefix(modelLower, "deepseek") {
		return "deepseek"
	}

	// Meta/Llama models: llama-*
	if strings.HasPrefix(modelLower, "llama") {
		return "meta"
	}

	// Mistral models: mistral-*, codestral-*, pixtral-*
	if strings.HasPrefix(modelLower, "mistral") ||
		strings.HasPrefix(modelLower, "codestral") ||
		strings.HasPrefix(modelLower, "pixtral") {
		return "mistral"
	}

	// Cohere models: command-*, c4ai-*
	if strings.HasPrefix(modelLower, "command") ||
		strings.HasPrefix(modelLower, "c4ai") {
		return "cohere"
	}

	// xAI/Grok models: grok-*
	if strings.HasPrefix(modelLower, "grok") {
		return "xai"
	}

	// Perplexity models: sonar-*, llama-3.1-sonar-*
	if strings.HasPrefix(modelLower, "sonar") {
		return "perplexity"
	}

	// Alibaba/Qwen models: qwen-*
	if strings.HasPrefix(modelLower, "qwen") {
		return "alibaba"
	}

	// Moonshot models: moonshot-*, kimi-*
	if strings.HasPrefix(modelLower, "moonshot") ||
		strings.HasPrefix(modelLower, "kimi") {
		return "moonshot"
	}

	// Zhipu models: glm-*, chatglm-*
	if strings.HasPrefix(modelLower, "glm") ||
		strings.HasPrefix(modelLower, "chatglm") {
		return "zhipu"
	}

	// Minimax models: abab*, minimax-*, MiniMax-*
	if strings.HasPrefix(modelLower, "abab") ||
		strings.HasPrefix(modelLower, "minimax") ||
		strings.HasPrefix(model, "MiniMax-") {
		return "minimax"
	}

	// ByteDance/Doubao models: doubao-*, seed-*
	if strings.HasPrefix(modelLower, "doubao") ||
		strings.HasPrefix(modelLower, "seed") {
		return "bytedance"
	}

	// Tencent models: hunyuan-*
	if strings.HasPrefix(modelLower, "hunyuan") {
		return "tencent"
	}

	// Baidu models: ernie-*
	if strings.HasPrefix(modelLower, "ernie") {
		return "baidu"
	}

	// Xiaomi models: xiaomi-*, mimo-*
	if strings.HasPrefix(modelLower, "xiaomi") ||
		strings.HasPrefix(modelLower, "mimo") {
		return "xiaomi"
	}

	// Xunfei/iFlytek models: astron-*
	if strings.HasPrefix(modelLower, "astron") {
		return "xunfei"
	}

	// Amazon/Bedrock models: amazon.*, anthropic.claude-* (on Bedrock)
	if strings.HasPrefix(modelLower, "amazon.") {
		return "amazon"
	}

	// AWS Bedrock cross-region inference models: global.*
	// These are exclusively Bedrock model identifiers (e.g. global.anthropic.claude-opus-4-6-v1)
	if strings.HasPrefix(modelLower, "global.") {
		return "amazon"
	}

	// Default to OpenAI-compatible
	return "openai"
}

// GetRelayFormatFromProvider - Get relay format from provider name
func GetRelayFormatFromProvider(provider string) types.RelayFormat {
	switch provider {
	case "anthropic":
		return types.RelayFormatClaude
	case "google":
		return types.RelayFormatGemini
	default:
		return types.RelayFormatOpenAI
	}
}
