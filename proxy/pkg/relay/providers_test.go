package relay

import (
	"testing"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/pkg/relay/base"
	"github.com/ai-proxy/ai-proxy-go/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertRequest(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	t.Run("openai format returns identity", func(t *testing.T) {
		result := ConvertRequest(req, types.RelayFormatOpenAI)
		converted, ok := result.(dto.GeneralOpenAIRequest)
		assert.True(t, ok)
		assert.Equal(t, req.Model, converted.Model)
	})

	t.Run("claude format converts", func(t *testing.T) {
		result := ConvertRequest(req, types.RelayFormatClaude)
		assert.NotNil(t, result)
		// Should return a ClaudeRequest
		_, ok := result.(dto.ClaudeRequest)
		assert.True(t, ok)
	})

	t.Run("gemini format converts", func(t *testing.T) {
		result := ConvertRequest(req, types.RelayFormatGemini)
		assert.NotNil(t, result)
		// Should return an interface{} (Gemini request format)
		assert.NotNil(t, result)
	})

	t.Run("unknown format returns identity", func(t *testing.T) {
		result := ConvertRequest(req, types.RelayFormat("unknown"))
		converted, ok := result.(dto.GeneralOpenAIRequest)
		assert.True(t, ok)
		assert.Equal(t, req.Model, converted.Model)
	})
}

func TestConvertResponse(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	t.Run("openai response conversion", func(t *testing.T) {
		responseJSON := []byte(`{"id":"chatcmpl-123","model":"gpt-4","object":"chat.completion","created":1700000000,"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)
		result := ConvertResponse(responseJSON, types.RelayFormatOpenAI, req)
		require.NotNil(t, result)
		assert.Equal(t, "chatcmpl-123", result.ID)
	})

	t.Run("non-matching type for format returns nil", func(t *testing.T) {
		result := ConvertResponse(&dto.OpenAITextResponse{}, types.RelayFormatClaude, req)
		assert.Nil(t, result)
	})
}

func TestRegisterProvider(t *testing.T) {
	// Save original state
	originalConfigs := make(map[string]ProviderConfig)
	for k, v := range DefaultProviderConfigs {
		originalConfigs[k] = v
	}

	t.Run("register new provider", func(t *testing.T) {
		newCfg := ProviderConfig{
			Name:     "custom-provider",
			BaseURL:  "https://custom.example.com/v1",
			AuthType: "bearer",
			APIKey:   "custom-key",
			Models:   []string{"model-1"},
		}

		RegisterProvider("custom-provider", newCfg)

		cfg, ok := GetProviderConfig("custom-provider")
		assert.True(t, ok)
		assert.Equal(t, "custom-provider", cfg.Name)
		assert.Equal(t, "https://custom.example.com/v1", cfg.BaseURL)
	})

	t.Run("overwrite existing provider", func(t *testing.T) {
		original, ok := DefaultProviderConfigs["openai"]
		require.True(t, ok)

		overwriteCfg := ProviderConfig{
			Name:     "openai",
			BaseURL:  "https://overwritten.example.com/v1",
			AuthType: "bearer",
		}
		RegisterProvider("openai", overwriteCfg)

		cfg, ok := GetProviderConfig("openai")
		assert.True(t, ok)
		assert.Equal(t, "https://overwritten.example.com/v1", cfg.BaseURL)

		// Restore original
		RegisterProvider("openai", original)
	})

	// Restore original state
	for k, v := range originalConfigs {
		DefaultProviderConfigs[k] = v
	}
}

func TestProviderConfigStruct(t *testing.T) {
	t.Run("provider config struct", func(t *testing.T) {
		cfg := ProviderConfig{
			Name:     "test",
			BaseURL:  "https://test.example.com",
			AuthType: "x-api-key",
			APIKey:   "test-key",
			Models:   []string{"model-a", "model-b"},
		}

		assert.Equal(t, "test", cfg.Name)
		assert.Equal(t, "https://test.example.com", cfg.BaseURL)
		assert.Equal(t, "x-api-key", cfg.AuthType)
		assert.Equal(t, "test-key", cfg.APIKey)
		assert.Len(t, cfg.Models, 2)
	})

	t.Run("get provider config for all default providers", func(t *testing.T) {
		providers := []string{
			"openai", "anthropic", "google", "meta", "mistral", "cohere",
			"deepseek", "alibaba", "moonshot", "zhipu", "bytedance",
			"tencent", "minimax", "baidu", "microsoft", "xai", "perplexity",
			"groq", "xiaomi", "xunfei", "accessapi", "amazon",
		}

		for _, name := range providers {
			t.Run(name, func(t *testing.T) {
				cfg, ok := GetProviderConfig(name)
				assert.True(t, ok, "provider %q should be found", name)
				assert.Equal(t, name, cfg.Name)
				assert.NotEmpty(t, cfg.AuthType)
			})
		}
	})

	t.Run("get provider config for amazon regions", func(t *testing.T) {
		for _, region := range BedrockRegions {
			providerName := "amazon-" + region
			cfg, ok := GetProviderConfig(providerName)
			assert.True(t, ok, "provider %q should exist", providerName)
			assert.Equal(t, providerName, cfg.Name)
			assert.Equal(t, "aws", cfg.AuthType)
			assert.Contains(t, cfg.BaseURL, region)
		}
	})

	t.Run("get provider config for amazon mantle regions", func(t *testing.T) {
		for _, region := range MantleRegions {
			providerName := "amazon-mantle-" + region
			cfg, ok := GetProviderConfig(providerName)
			assert.True(t, ok, "provider %q should exist", providerName)
			assert.Equal(t, providerName, cfg.Name)
			assert.Equal(t, "bearer", cfg.AuthType)
			assert.Contains(t, cfg.BaseURL, region)
		}
	})
}

func TestBedrockRegions(t *testing.T) {
	t.Run("has expected regions", func(t *testing.T) {
		expectedRegions := map[string]bool{
			"us-east-1": true, "us-east-2": true, "us-west-1": true, "us-west-2": true,
			"ap-northeast-1": true, "ap-southeast-1": true,
		}

		for _, region := range BedrockRegions {
			if expectedRegions[region] {
				delete(expectedRegions, region)
			}
		}

		assert.Empty(t, expectedRegions, "all expected regions should be present")
	})

	t.Run("contains at least 30 regions", func(t *testing.T) {
		assert.GreaterOrEqual(t, len(BedrockRegions), 30)
	})
}

func TestMantleRegions(t *testing.T) {
	t.Run("contains at least 10 regions", func(t *testing.T) {
		assert.GreaterOrEqual(t, len(MantleRegions), 10)
	})
}

func TestGetMantleProviderNames(t *testing.T) {
	names := GetMantleProviderNames()

	assert.Len(t, names, len(MantleRegions))

	for _, name := range names {
		assert.Contains(t, name, "amazon-mantle-")
	}
}

func TestDefaultProviderConfigs(t *testing.T) {
	t.Run("openai config", func(t *testing.T) {
		cfg, ok := DefaultProviderConfigs["openai"]
		assert.True(t, ok)
		assert.Equal(t, "bearer", cfg.AuthType)
		assert.Equal(t, "https://api.openai.com/v1", cfg.BaseURL)
	})

	t.Run("anthropic config", func(t *testing.T) {
		cfg, ok := DefaultProviderConfigs["anthropic"]
		assert.True(t, ok)
		assert.Equal(t, "x-api-key", cfg.AuthType)
		assert.Equal(t, "https://api.anthropic.com/v1", cfg.BaseURL)
	})

	t.Run("google config", func(t *testing.T) {
		cfg, ok := DefaultProviderConfigs["google"]
		assert.True(t, ok)
		assert.Equal(t, "query", cfg.AuthType)
		assert.Contains(t, cfg.BaseURL, "generativelanguage.googleapis.com")
	})

	t.Run("microsoft config", func(t *testing.T) {
		cfg, ok := DefaultProviderConfigs["microsoft"]
		assert.True(t, ok)
		assert.Equal(t, "api-key", cfg.AuthType)
		assert.Contains(t, cfg.BaseURL, ".openai.azure.com")
	})

	t.Run("amazon config", func(t *testing.T) {
		cfg, ok := DefaultProviderConfigs["amazon"]
		assert.True(t, ok)
		assert.Equal(t, "aws", cfg.AuthType)
		assert.Contains(t, cfg.BaseURL, "bedrock-runtime")
	})

	t.Run("deepseek config", func(t *testing.T) {
		cfg, ok := DefaultProviderConfigs["deepseek"]
		assert.True(t, ok)
		assert.Equal(t, "bearer", cfg.AuthType)
		assert.Equal(t, "https://api.deepseek.com/v1", cfg.BaseURL)
	})

	t.Run("alibaba config", func(t *testing.T) {
		cfg, ok := DefaultProviderConfigs["alibaba"]
		assert.True(t, ok)
		assert.Equal(t, "bearer", cfg.AuthType)
		assert.Contains(t, cfg.BaseURL, "aliyuncs.com")
	})

	t.Run("accessapi config", func(t *testing.T) {
		cfg, ok := DefaultProviderConfigs["accessapi"]
		assert.True(t, ok)
		assert.Equal(t, "bearer", cfg.AuthType)
		assert.Equal(t, "https://accessapi.ai/v1", cfg.BaseURL)
	})
}

func TestSupportedProviders_IncludesAll(t *testing.T) {
	providers := SupportedProviders()
	providerSet := make(map[string]bool)
	for _, p := range providers {
		providerSet[p] = true
	}

	// All 18+ major providers should be present
	majors := []string{
		"openai", "anthropic", "google", "meta", "mistral", "cohere",
		"amazon", "microsoft", "xai", "perplexity",
		"deepseek", "alibaba", "moonshot", "zhipu",
		"bytedance", "tencent", "minimax", "baidu",
		"groq", "xiaomi", "xunfei", "accessapi",
	}

	for _, major := range majors {
		assert.True(t, providerSet[major], "major provider %q should be supported", major)
	}
}

func TestOpenAICompatibleAdaptor(t *testing.T) {
	cfg := &base.AdaptorConfig{
		BaseURL: "https://custom.example.com/v1",
		APIKey:  "test-key",
	}
	adaptor := NewOpenAICompatibleAdaptor("custom-provider", cfg)

	t.Run("GetRelayFormat returns OpenAI", func(t *testing.T) {
		assert.Equal(t, types.RelayFormatOpenAI, adaptor.GetRelayFormat())
	})

	t.Run("GetProviderName returns provider name", func(t *testing.T) {
		assert.Equal(t, "custom-provider", adaptor.GetProviderName())
	})

	t.Run("FromProvider handles []byte", func(t *testing.T) {
		data := []byte(`{"id":"chatcmpl-123","model":"gpt-4","object":"chat.completion","created":1700000000,"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)
		result := adaptor.FromProvider(data, dto.GeneralOpenAIRequest{})
		require.NotNil(t, result)
		assert.Equal(t, "chatcmpl-123", result.ID)
	})

	t.Run("FromProvider handles invalid JSON", func(t *testing.T) {
		data := []byte(`not valid json`)
		result := adaptor.FromProvider(data, dto.GeneralOpenAIRequest{})
		assert.Nil(t, result)
	})

	t.Run("FromProvider falls back to base for non-[]byte", func(t *testing.T) {
		openAIResp := &dto.OpenAITextResponse{
			ID:    "chatcmpl-456",
			Model: "gpt-4",
		}
		result := adaptor.FromProvider(openAIResp, dto.GeneralOpenAIRequest{})
		require.NotNil(t, result)
		assert.Equal(t, "chatcmpl-456", result.ID)
	})

	t.Run("ExtractUsage handles []byte", func(t *testing.T) {
		data := []byte(`{"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`)
		usage := adaptor.ExtractUsage(data)
		require.NotNil(t, usage)
		assert.Equal(t, 100, usage.PromptTokens)
		assert.Equal(t, 50, usage.CompletionTokens)
		assert.Equal(t, 150, usage.TotalTokens)
	})

	t.Run("ExtractUsage handles invalid JSON", func(t *testing.T) {
		data := []byte(`bad json`)
		usage := adaptor.ExtractUsage(data)
		require.NotNil(t, usage)
		assert.Equal(t, 0, usage.PromptTokens)
	})

	t.Run("ExtractUsage handles non-[]byte", func(t *testing.T) {
		openAIResp := &dto.OpenAITextResponse{
			Usage: dto.Usage{
				PromptTokens:     200,
				CompletionTokens: 100,
				TotalTokens:      300,
			},
		}
		usage := adaptor.ExtractUsage(openAIResp)
		require.NotNil(t, usage)
		assert.Equal(t, 200, usage.PromptTokens)
	})
}

func TestGetProviderConfig_Fallback(t *testing.T) {
	t.Run("unknown provider returns OpenAI-compatible config", func(t *testing.T) {
		cfg, ok := GetProviderConfig("completely-unknown-provider")
		assert.True(t, ok, "unknown providers should fall back to OpenAI-compatible")
		assert.Equal(t, "completely-unknown-provider", cfg.Name)
		assert.Equal(t, "", cfg.BaseURL)
		assert.Equal(t, "bearer", cfg.AuthType)
	})
}

func TestGetProviderConfig_AmazonRegions(t *testing.T) {
	t.Run("amazon-us-east-1", func(t *testing.T) {
		cfg, ok := GetProviderConfig("amazon-us-east-1")
		assert.True(t, ok)
		assert.Equal(t, "amazon-us-east-1", cfg.Name)
		assert.Equal(t, "aws", cfg.AuthType)
	})
}

func TestNewOpenAIAdaptor_Alias(t *testing.T) {
	// Verify the constructor alias works
	cfg := &base.AdaptorConfig{
		BaseURL: "https://api.openai.com/v1",
	}

	adaptor := NewOpenAIAdaptor(cfg)
	require.NotNil(t, adaptor)
	assert.Equal(t, "openai", adaptor.GetProviderName())
}

func TestNewClaudeAdaptor_Alias(t *testing.T) {
	cfg := &base.AdaptorConfig{
		BaseURL: "https://api.anthropic.com/v1",
	}

	adaptor := NewClaudeAdaptor(cfg)
	require.NotNil(t, adaptor)
	assert.NotEmpty(t, adaptor.GetProviderName())
}

func TestNewGeminiAdaptor_Alias(t *testing.T) {
	cfg := &base.AdaptorConfig{
		BaseURL: "https://generativelanguage.googleapis.com/v1beta",
	}

	adaptor := NewGeminiAdaptor(cfg)
	require.NotNil(t, adaptor)
	assert.NotEmpty(t, adaptor.GetProviderName())
}
