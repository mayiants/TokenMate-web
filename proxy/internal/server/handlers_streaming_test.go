package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/internal/auth"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"github.com/mayiants/ai-proxy-go/pkg/keypool"
	"github.com/mayiants/ai-proxy-go/pkg/token"
	"github.com/mayiants/ai-proxy-go/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockStreamAdaptor is a test adaptor that returns a canned streaming response.
type mockStreamAdaptor struct {
	responseBody io.ReadCloser
	format       types.RelayFormat
}

func (m *mockStreamAdaptor) ToProvider(req dto.GeneralOpenAIRequest) interface{} { return req }
func (m *mockStreamAdaptor) FromProvider(response interface{}, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	return nil
}
func (m *mockStreamAdaptor) TransformStreamChunk(chunk interface{}, index int) interface{} {
	// chunk is "data: {...}", strip prefix and process based on format
	data, ok := chunk.([]byte)
	if !ok {
		return nil
	}
	data = bytes.TrimPrefix(data, []byte("data: "))

	if m.format == types.RelayFormatClaude {
		var claudeEvent struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(data, &claudeEvent); err != nil {
			return nil
		}
		if claudeEvent.Type == "content_block_delta" && claudeEvent.Delta.Type == "text_delta" {
			content := claudeEvent.Delta.Text
			return &dto.ChatCompletionsStreamResponse{
				Object:  "chat.completion.chunk",
				Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &content}}},
			}
		}
		return nil
	}

	var resp dto.ChatCompletionsStreamResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil
	}
	return &resp
}
func (m *mockStreamAdaptor) ParseResponse(body []byte) (interface{}, error) { return nil, nil }
func (m *mockStreamAdaptor) ExtractUsage(response interface{}) *dto.Usage   { return &dto.Usage{} }
func (m *mockStreamAdaptor) GetHeaders() map[string]string                  { return nil }
func (m *mockStreamAdaptor) GetEndpoint(model string, stream bool) string   { return "/chat/completions" }
func (m *mockStreamAdaptor) GetRequestURL(model string, stream bool) string { return "http://mock" }
func (m *mockStreamAdaptor) DoRequest(body interface{}, stream bool) (*http.Response, error) {
	return m.DoRequestBytes(nil, stream)
}
func (m *mockStreamAdaptor) DoRequestBytes(body []byte, stream bool) (*http.Response, error) {
	return m.DoRequestBytesWithContext(context.Background(), body, stream)
}
func (m *mockStreamAdaptor) DoRequestBytesWithContext(_ context.Context, _ []byte, stream bool) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       m.responseBody,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}, nil
}
func (m *mockStreamAdaptor) ReadResponse(resp *http.Response) ([]byte, error) { return nil, nil }
func (m *mockStreamAdaptor) GetRelayFormat() types.RelayFormat                { return m.format }
func (m *mockStreamAdaptor) GetProviderName() string                          { return "mock" }

// cancellableStreamAdaptor returns context.Canceled when the supplied context is
// already canceled, allowing regression tests for the client-disconnect path.
type cancellableStreamAdaptor struct {
	mockStreamAdaptor
}

func (m *cancellableStreamAdaptor) DoRequestBytesWithContext(ctx context.Context, _ []byte, stream bool) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.mockStreamAdaptor.DoRequestBytesWithContext(ctx, nil, stream)
}

func TestHandleStreamingResponse_ContextCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server:  config.ServerConfig{Mode: "test"},
		Billing: config.BillingConfig{LocalBalanceCacheEnabled: true, LocalBalanceCacheTTL: 10 * time.Second, PreConsumeMargin: 1.0},
	}
	logger := zap.NewNop()
	authMW := auth.NewMiddleware(cfg, logger)
	authMW.SetBalance("user-1", 1_000_000)

	svc := NewHandlerService(cfg, logger)
	svc.authMiddleware = authMW
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:       logger,
		Billing:      billing.NewBilling(),
		TokenCounter: token.NewCounter(),
		Simulate:     SimulateConfig{Enabled: false},
	})

	adaptor := &cancellableStreamAdaptor{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request, _ = http.NewRequestWithContext(ctx, "POST", "/v1/chat/completions", nil)
	c.Set(string(userIDKey), "user-1")
	c.Set("key_hash", "hash-1")
	c.Set("access_mode", "general")
	c.Set("key_type", "general")

	req := dto.GeneralOpenAIRequest{Model: "gpt-4o", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	upstreamKey := &keypool.KeyInternal{Key: &keypool.Key{KeyStatic: keypool.KeyStatic{ID: "key-1", Key: "sk-test"}}}

	upstreamBody, _ := json.Marshal(req)
	svc.handleStreamingResponse(c, adaptor, upstreamBody, req, "gpt-4o", "openai", "key-1", "user-1", "hash-1", upstreamKey, 10, time.Now())

	// Client disconnect should not produce a 502 upstream_error response.
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Empty(t, w.Body.String())
	// Pre-consumed estimate should be refunded (settled to zero actual usage).
	assert.Equal(t, int64(1_000_000), getBalanceReflect(t, authMW, "user-1"))
}

func TestHandleAnthropicStreamingResponse_ContextCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server:  config.ServerConfig{Mode: "test"},
		Billing: config.BillingConfig{LocalBalanceCacheEnabled: true, LocalBalanceCacheTTL: 10 * time.Second, PreConsumeMargin: 1.0},
	}
	logger := zap.NewNop()
	authMW := auth.NewMiddleware(cfg, logger)
	authMW.SetBalance("user-1", 1_000_000)

	svc := NewHandlerService(cfg, logger)
	svc.authMiddleware = authMW
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:       logger,
		Billing:      billing.NewBilling(),
		TokenCounter: token.NewCounter(),
		Simulate:     SimulateConfig{Enabled: false},
	})

	adaptor := &cancellableStreamAdaptor{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request, _ = http.NewRequestWithContext(ctx, "POST", "/v1/messages", nil)
	c.Set(string(userIDKey), "user-1")
	c.Set("key_hash", "hash-1")
	c.Set("access_mode", "general")
	c.Set("key_type", "general")

	req := dto.GeneralOpenAIRequest{Model: "claude-sonnet-4", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	upstreamKey := &keypool.KeyInternal{Key: &keypool.Key{KeyStatic: keypool.KeyStatic{ID: "key-1", Key: "sk-test"}}}

	upstreamBody, _ := json.Marshal(req)
	svc.handleAnthropicStreamingResponse(c, adaptor, upstreamBody, req, "anthropic", "key-1", "user-1", "hash-1", upstreamKey, "claude-sonnet-4", time.Now())

	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Empty(t, w.Body.String())
	assert.Equal(t, int64(1_000_000), getBalanceReflect(t, authMW, "user-1"))
}

func TestHandleStreamingResponse_OpenAI(t *testing.T) {
	gin.SetMode(gin.TestMode)

	streamBody := "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: [DONE]\n\n"

	adaptor := &mockStreamAdaptor{
		responseBody: io.NopCloser(bytes.NewReader([]byte(streamBody))),
		format:       types.RelayFormatOpenAI,
	}

	cfg := &config.Config{Server: config.ServerConfig{Mode: "test"}}
	svc := NewHandlerService(cfg, zap.NewNop())
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   zap.NewNop(),
		Simulate: SimulateConfig{Enabled: false},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set(string(userIDKey), "user-1")
	c.Set("key_hash", "hash-1")

	req := dto.GeneralOpenAIRequest{Model: "gpt-4o"}
	upstreamKey := &keypool.KeyInternal{Key: &keypool.Key{KeyStatic: keypool.KeyStatic{ID: "key-1"}}}

	upstreamBody, _ := json.Marshal(req)
	svc.handleStreamingResponse(c, adaptor, upstreamBody, req, "gpt-4o", "openai", "key-1", "user-1", "hash-1", upstreamKey, 10, time.Now())

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code, "body: %s", body)
	assert.Contains(t, body, "Hello")
	assert.Contains(t, body, " world")
	assert.Contains(t, body, "[DONE]")
}

func TestHandleStreamingResponse_Anthropic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	streamBody := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	adaptor := &mockStreamAdaptor{
		responseBody: io.NopCloser(bytes.NewReader([]byte(streamBody))),
		format:       types.RelayFormatClaude,
	}

	cfg := &config.Config{Server: config.ServerConfig{Mode: "test"}}
	svc := NewHandlerService(cfg, zap.NewNop())
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   zap.NewNop(),
		Simulate: SimulateConfig{Enabled: false},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set(string(userIDKey), "user-1")
	c.Set("key_hash", "hash-1")

	req := dto.GeneralOpenAIRequest{Model: "claude-sonnet-4"}
	upstreamKey := &keypool.KeyInternal{Key: &keypool.Key{KeyStatic: keypool.KeyStatic{ID: "key-1"}}}

	upstreamBody, _ := json.Marshal(req)
	svc.handleStreamingResponse(c, adaptor, upstreamBody, req, "claude-sonnet-4", "anthropic", "key-1", "user-1", "hash-1", upstreamKey, 10, time.Now())

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code, "body: %s", body)
	assert.Contains(t, body, "Hi")
	assert.Contains(t, body, "[DONE]")
}
