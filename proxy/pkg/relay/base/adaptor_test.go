package base

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBaseAdaptor(t *testing.T) {
	t.Run("with full config", func(t *testing.T) {
		cfg := &AdaptorConfig{
			BaseURL:  "https://custom.api.com/v1",
			APIKey:   "test-key",
			AuthType: "bearer",
			Models:   []string{"model-a", "model-b"},
		}
		adaptor := NewBaseAdaptor("test-provider", cfg)

		assert.Equal(t, "test-provider", adaptor.ProviderName)
		assert.Equal(t, "https://custom.api.com/v1", adaptor.BaseURL)
		assert.Equal(t, cfg, adaptor.Config)
	})

	t.Run("with nil config", func(t *testing.T) {
		adaptor := NewBaseAdaptor("default-provider", nil)

		assert.Equal(t, "default-provider", adaptor.ProviderName)
		assert.Equal(t, "https://api.openai.com/v1", adaptor.BaseURL)
		assert.Nil(t, adaptor.Config)
	})

	t.Run("with empty base URL in config", func(t *testing.T) {
		cfg := &AdaptorConfig{
			BaseURL: "",
			APIKey:  "key",
		}
		adaptor := NewBaseAdaptor("provider", cfg)

		assert.Equal(t, "https://api.openai.com/v1", adaptor.BaseURL)
	})
}

func TestBaseAdaptor_GetProviderName(t *testing.T) {
	adaptor := NewBaseAdaptor("my-provider", &AdaptorConfig{BaseURL: "https://api.example.com"})
	assert.Equal(t, "my-provider", adaptor.GetProviderName())
}

func TestBaseAdaptor_GetRelayFormat(t *testing.T) {
	adaptor := NewBaseAdaptor("test", &AdaptorConfig{BaseURL: "https://api.example.com"})
	assert.Equal(t, types.RelayFormatOpenAI, adaptor.GetRelayFormat())
}

func TestBaseAdaptor_GetHeaders(t *testing.T) {
	tests := []struct {
		name         string
		config       *AdaptorConfig
		expectBearer bool
		expectXAPI   bool
		expectAPIKey bool
	}{
		{
			name:         "default bearer auth",
			config:       &AdaptorConfig{BaseURL: "https://api.example.com", APIKey: "sk-test", AuthType: "bearer"},
			expectBearer: true,
		},
		{
			name:         "empty auth type defaults to bearer",
			config:       &AdaptorConfig{BaseURL: "https://api.example.com", APIKey: "sk-test"},
			expectBearer: true,
		},
		{
			name:       "x-api-key auth type",
			config:     &AdaptorConfig{BaseURL: "https://api.example.com", APIKey: "api-key-123", AuthType: "x-api-key"},
			expectXAPI: true,
		},
		{
			name:         "api-key auth type",
			config:       &AdaptorConfig{BaseURL: "https://api.example.com", APIKey: "api-key-456", AuthType: "api-key"},
			expectAPIKey: true,
		},
		{
			name:         "query auth type uses bearer in header",
			config:       &AdaptorConfig{BaseURL: "https://api.example.com", APIKey: "query-key", AuthType: "query"},
			expectBearer: true,
		},
		{
			name:         "aws auth type uses bearer fallback",
			config:       &AdaptorConfig{BaseURL: "https://api.example.com", APIKey: "aws-key", AuthType: "aws"},
			expectBearer: true,
		},
		{
			name:   "nil config",
			config: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptor := NewBaseAdaptor("provider", tt.config)
			headers := adaptor.GetHeaders()

			assert.Equal(t, "application/json", headers["Content-Type"])

			if tt.expectBearer {
				assert.Contains(t, headers, "Authorization")
				assert.Contains(t, headers["Authorization"], "Bearer ")
			}
			if tt.expectXAPI {
				assert.Contains(t, headers, "x-api-key")
				assert.Equal(t, "api-key-123", headers["x-api-key"])
			}
			if tt.expectAPIKey {
				assert.Contains(t, headers, "api-key")
				assert.Equal(t, "api-key-456", headers["api-key"])
			}
		})
	}
}

func TestBaseAdaptor_GetEndpoint(t *testing.T) {
	adaptor := NewBaseAdaptor("test", nil)

	endpoint := adaptor.GetEndpoint("gpt-4", false)
	assert.Equal(t, "/chat/completions", endpoint)

	// Same endpoint regardless of stream flag
	endpointStream := adaptor.GetEndpoint("gpt-4", true)
	assert.Equal(t, "/chat/completions", endpointStream)
}

func TestBaseAdaptor_GetRequestURL(t *testing.T) {
	adaptor := NewBaseAdaptor("test", &AdaptorConfig{BaseURL: "https://api.openai.com/v1"})

	url := adaptor.GetRequestURL("gpt-4", false)
	assert.Equal(t, "https://api.openai.com/v1/chat/completions", url)

	// With full URL endpoint
	adaptor2 := NewBaseAdaptor("test", &AdaptorConfig{BaseURL: "https://custom.example.com"})
	// Override endpoint behavior by testing concatenation
	url2 := adaptor2.GetRequestURL("gpt-4", true)
	assert.Equal(t, "https://custom.example.com/chat/completions", url2)
}

func TestBaseAdaptor_DoRequest(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":"test"}`))
		}))
		defer server.Close()

		adaptor := NewBaseAdaptor("test", &AdaptorConfig{BaseURL: server.URL})
		body := map[string]interface{}{
			"model": "gpt-4",
			"messages": []map[string]string{
				{"role": "user", "content": "Hello"},
			},
		}

		resp, err := adaptor.DoRequest(body, false)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("failed marshal", func(t *testing.T) {
		adaptor := NewBaseAdaptor("test", &AdaptorConfig{BaseURL: "https://api.example.com"})
		// Channel cannot be marshaled to JSON
		ch := make(chan int)
		_, err := adaptor.DoRequest(ch, false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to marshal")
	})

	t.Run("connection refused", func(t *testing.T) {
		// Use a port that nothing is listening on
		adaptor := NewBaseAdaptor("test", &AdaptorConfig{BaseURL: "http://127.0.0.1:1"})
		body := map[string]string{"test": "data"}
		_, err := adaptor.DoRequest(body, false)
		assert.Error(t, err)
	})
}

func TestBaseAdaptor_DoRequestStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: test\n\n"))
	}))
	defer server.Close()

	adaptor := NewBaseAdaptor("test", &AdaptorConfig{BaseURL: server.URL})
	body := map[string]string{"test": "data"}

	resp, err := adaptor.DoRequestStream(body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestBaseAdaptor_ReadResponse(t *testing.T) {
	t.Run("successful response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"result": "success"}`))
		}))
		defer server.Close()

		adaptor := NewBaseAdaptor("test", &AdaptorConfig{BaseURL: server.URL})
		resp, err := http.Get(server.URL)
		require.NoError(t, err)

		body, err := adaptor.ReadResponse(resp)
		require.NoError(t, err)
		assert.Equal(t, `{"result": "success"}`, string(body))
	})

	t.Run("error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "rate limited"}`))
		}))
		defer server.Close()

		adaptor := NewBaseAdaptor("test", &AdaptorConfig{BaseURL: server.URL})
		resp, err := http.Get(server.URL)
		require.NoError(t, err)

		_, err = adaptor.ReadResponse(resp)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "429")
	})
}

func TestBaseAdaptor_ParseError(t *testing.T) {
	adaptor := NewBaseAdaptor("test", nil)

	t.Run("valid error response", func(t *testing.T) {
		data := []byte(`{"error": {"message": "Invalid API key"}}`)
		err := adaptor.ParseError(data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "API error")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		data := []byte(`not json`)
		err := adaptor.ParseError(data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse error")
	})
}

func TestBaseAdaptor_ToProvider(t *testing.T) {
	adaptor := NewBaseAdaptor("test", nil)

	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	result := adaptor.ToProvider(req)
	assert.Equal(t, req, result)
}

func TestBaseAdaptor_FromProvider(t *testing.T) {
	adaptor := NewBaseAdaptor("test", nil)

	t.Run("pointer to OpenAITextResponse", func(t *testing.T) {
		original := &dto.OpenAITextResponse{
			ID:    "chatcmpl-123",
			Model: "gpt-4",
		}
		result := adaptor.FromProvider(original, dto.GeneralOpenAIRequest{})
		assert.Equal(t, original, result)
	})

	t.Run("value OpenAITextResponse", func(t *testing.T) {
		original := dto.OpenAITextResponse{
			ID:    "chatcmpl-456",
			Model: "gpt-4",
		}
		result := adaptor.FromProvider(original, dto.GeneralOpenAIRequest{})
		assert.Equal(t, "chatcmpl-456", result.ID)
		assert.Equal(t, "gpt-4", result.Model)
	})

	t.Run("unknown type returns nil", func(t *testing.T) {
		result := adaptor.FromProvider("string", dto.GeneralOpenAIRequest{})
		assert.Nil(t, result)
	})

	t.Run("nil response returns nil", func(t *testing.T) {
		result := adaptor.FromProvider(nil, dto.GeneralOpenAIRequest{})
		assert.Nil(t, result)
	})
}

func TestBaseAdaptor_TransformStreamChunk(t *testing.T) {
	adaptor := NewBaseAdaptor("test", nil)

	chunk := map[string]string{"data": "test"}
	result := adaptor.TransformStreamChunk(chunk, 0)

	assert.Equal(t, chunk, result)
}

func TestBaseAdaptor_ExtractUsage(t *testing.T) {
	adaptor := NewBaseAdaptor("test", nil)

	t.Run("pointer to OpenAITextResponse", func(t *testing.T) {
		resp := &dto.OpenAITextResponse{
			Usage: dto.Usage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
		}
		usage := adaptor.ExtractUsage(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 100, usage.PromptTokens)
		assert.Equal(t, 50, usage.CompletionTokens)
		assert.Equal(t, 150, usage.TotalTokens)
	})

	t.Run("map response", func(t *testing.T) {
		resp := map[string]interface{}{
			"usage": map[string]interface{}{
				"prompt_tokens":     float64(200),
				"completion_tokens": float64(100),
				"total_tokens":      float64(300),
			},
		}
		usage := adaptor.ExtractUsage(resp)
		require.NotNil(t, usage)
		assert.Equal(t, 200, usage.PromptTokens)
		assert.Equal(t, 100, usage.CompletionTokens)
		assert.Equal(t, 300, usage.TotalTokens)
	})

	t.Run("unknown type returns empty usage", func(t *testing.T) {
		usage := adaptor.ExtractUsage("unknown")
		require.NotNil(t, usage)
		assert.Equal(t, 0, usage.PromptTokens)
	})

	t.Run("nil response returns empty usage", func(t *testing.T) {
		usage := adaptor.ExtractUsage(nil)
		require.NotNil(t, usage)
		assert.Equal(t, 0, usage.PromptTokens)
	})
}

func TestSharedHTTPClient(t *testing.T) {
	client := SharedHTTPClient()
	require.NotNil(t, client)
	assert.Equal(t, sharedHTTPClient, client)

	// W1 fix: sharedHTTPClient.Timeout is intentionally 0 (no total timeout).
	// http.Client.Timeout covers the entire response body read period and
	// would hard-cut SSE streams at 120s. Streaming callers rely on their
	// request context for deadline control instead. A non-streaming client
	// with a timeout is available separately when needed.
	assert.Equal(t, time.Duration(0), client.Timeout)
}

func TestGetInt(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		expected int
	}{
		{"float64 value", map[string]interface{}{"count": float64(42)}, "count", 42},
		{"zero value", map[string]interface{}{"count": float64(0)}, "count", 0},
		{"missing key", map[string]interface{}{"other": 1}, "count", 0},
		{"non-float value", map[string]interface{}{"count": "not-a-number"}, "count", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getInt(tt.input, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}
