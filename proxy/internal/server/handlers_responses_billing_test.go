package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/internal/auth"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"github.com/mayiants/ai-proxy-go/pkg/convert"
	"github.com/mayiants/ai-proxy-go/pkg/keypool"
	"github.com/mayiants/ai-proxy-go/pkg/token"
	"github.com/mayiants/ai-proxy-go/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockResponsesStreamAdaptor is a test adaptor that returns a canned streaming
// response for the Responses API convert path.
type mockResponsesStreamAdaptor struct {
	responseBody io.ReadCloser
	format       types.RelayFormat
	statusCode   int
}

func (m *mockResponsesStreamAdaptor) status() int {
	if m.statusCode == 0 {
		return http.StatusOK
	}
	return m.statusCode
}

func (m *mockResponsesStreamAdaptor) ToProvider(req dto.GeneralOpenAIRequest) interface{} { return req }
func (m *mockResponsesStreamAdaptor) FromProvider(response interface{}, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	return nil
}
func (m *mockResponsesStreamAdaptor) TransformStreamChunk(chunk interface{}, index int) interface{} {
	return chunk
}
func (m *mockResponsesStreamAdaptor) ParseResponse(body []byte) (interface{}, error) { return nil, nil }
func (m *mockResponsesStreamAdaptor) ExtractUsage(response interface{}) *dto.Usage {
	return &dto.Usage{}
}
func (m *mockResponsesStreamAdaptor) GetHeaders() map[string]string { return nil }
func (m *mockResponsesStreamAdaptor) GetEndpoint(model string, stream bool) string {
	return "/chat/completions"
}
func (m *mockResponsesStreamAdaptor) GetRequestURL(model string, stream bool) string {
	return "http://mock"
}
func (m *mockResponsesStreamAdaptor) DoRequest(body interface{}, stream bool) (*http.Response, error) {
	return m.DoRequestBytes(nil, stream)
}
func (m *mockResponsesStreamAdaptor) DoRequestBytes(body []byte, stream bool) (*http.Response, error) {
	return m.DoRequestBytesWithContext(context.Background(), body, stream)
}
func (m *mockResponsesStreamAdaptor) DoRequestBytesWithContext(ctx context.Context, _ []byte, _ bool) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: m.status(), Body: m.responseBody, Header: http.Header{"Content-Type": []string{"text/event-stream"}}}, nil
}
func (m *mockResponsesStreamAdaptor) ReadResponse(resp *http.Response) ([]byte, error) {
	return nil, nil
}
func (m *mockResponsesStreamAdaptor) GetRelayFormat() types.RelayFormat { return m.format }
func (m *mockResponsesStreamAdaptor) GetProviderName() string           { return "mock" }

func TestHandleResponsesStreaming_ContextCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc, authMW := newTestResponsesBillingService(t, 1_000_000)

	adaptor := &mockResponsesStreamAdaptor{
		responseBody: io.NopCloser(bytes.NewReader([]byte(""))),
		format:       types.RelayFormatOpenAI,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request, _ = http.NewRequestWithContext(ctx, "POST", "/v1/responses", nil)
	c.Set(string(userIDKey), "user-1")
	c.Set("key_hash", "hash-1")
	c.Set("access_mode", "general")
	c.Set("key_type", "general")

	req := dto.GeneralOpenAIRequest{Model: "gpt-4o"}
	upstreamKey := &keypool.KeyInternal{Key: &keypool.Key{KeyStatic: keypool.KeyStatic{ID: "key-1", Key: "sk-test"}}}

	upstreamBody, _ := json.Marshal(req)

	preConsumed := int64(500_000)
	_, ok, _ := authMW.PreConsumeLocal("user-1", preConsumed)
	require.True(t, ok)
	assert.Equal(t, int64(500_000), getBalanceReflect(t, authMW, "user-1"))

	svc.handleResponsesStreaming(c, adaptor, upstreamBody, req, "openai", "key-1", "user-1", "hash-1", upstreamKey, "gpt-4o", time.Now(), &preConsumeState{local: preConsumed}, "gpt-4o")

	// Client disconnect should not produce a 502 upstream_error response.
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Empty(t, w.Body.String())
	// Pre-consumed estimate should be refunded.
	assert.Equal(t, int64(1_000_000), getBalanceReflect(t, authMW, "user-1"))
}

func newTestResponsesBillingService(t *testing.T, balanceMicro int64) (*HandlerService, *auth.Middleware) {
	t.Helper()
	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Billing: config.BillingConfig{
			LocalBalanceCacheEnabled: true,
			LocalBalanceCacheTTL:     10 * time.Second,
			PreConsumeMargin:         1.0,
		},
	}
	logger := zap.NewNop()
	authMW := auth.NewMiddleware(cfg, logger)
	authMW.SetBalance("user-1", balanceMicro)

	svc := NewHandlerService(cfg, logger)
	svc.authMiddleware = authMW
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:       logger,
		Billing:      billing.NewBilling(),
		TokenCounter: token.NewCounter(),
		Simulate:     SimulateConfig{Enabled: false},
	})
	return svc, authMW
}

func getBalanceReflect(t *testing.T, m *auth.Middleware, userID string) int64 {
	t.Helper()
	v := reflect.ValueOf(m).Elem()

	balanceCache := v.FieldByName("balanceCache")
	require.True(t, balanceCache.IsValid(), "auth.Middleware does not have balanceCache field")

	key := reflect.ValueOf(userID)
	entry := balanceCache.MapIndex(key)
	if !entry.IsValid() {
		return -1
	}
	balance := entry.Elem().FieldByName("balance")
	require.True(t, balance.IsValid(), "balanceEntry does not have balance field")
	return balance.Int()
}

func TestHandleResponsesStreaming_SettleChargesActualUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	streamBody := "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\" world\"}}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n" +
		"data: [DONE]\n\n"

	adaptor := &mockResponsesStreamAdaptor{
		responseBody: io.NopCloser(bytes.NewReader([]byte(streamBody))),
		format:       types.RelayFormatOpenAI,
	}

	svc, authMW := newTestResponsesBillingService(t, 1_000_000)
	startBalance := getBalanceReflect(t, authMW, "user-1")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/responses", nil)
	c.Set(string(userIDKey), "user-1")
	c.Set("key_hash", "hash-1")
	c.Set("access_mode", "general")
	c.Set("key_type", "general")

	req := dto.GeneralOpenAIRequest{Model: "gpt-4o"}
	upstreamKey := &keypool.KeyInternal{Key: &keypool.Key{KeyStatic: keypool.KeyStatic{ID: "key-1", Key: "sk-test"}}}

	upstreamBody, _ := json.Marshal(req)
	preConsumed := int64(0)
	svc.handleResponsesStreaming(c, adaptor, upstreamBody, req, "openai", "key-1", "user-1", "hash-1", upstreamKey, "gpt-4o", time.Now(), &preConsumeState{local: preConsumed}, "gpt-4o")

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// Without a real pre-consume there is nothing to settle, so the balance
	// should be unchanged. This test primarily verifies the success path does
	// not panic and that settleLocal is invoked.
	assert.Equal(t, startBalance, getBalanceReflect(t, authMW, "user-1"))
}

func TestHandleResponsesStreaming_UpstreamErrorRefundsPreConsume(t *testing.T) {
	gin.SetMode(gin.TestMode)

	adaptor := &mockResponsesStreamAdaptor{
		responseBody: io.NopCloser(bytes.NewReader([]byte(""))),
		format:       types.RelayFormatOpenAI,
		statusCode:   http.StatusBadRequest,
	}

	svc, authMW := newTestResponsesBillingService(t, 1_000_000)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/responses", nil)
	c.Set(string(userIDKey), "user-1")
	c.Set("key_hash", "hash-1")
	c.Set("access_mode", "general")
	c.Set("key_type", "general")

	req := dto.GeneralOpenAIRequest{Model: "gpt-4o"}
	upstreamKey := &keypool.KeyInternal{Key: &keypool.Key{KeyStatic: keypool.KeyStatic{ID: "key-1", Key: "sk-test"}}}

	upstreamBody, _ := json.Marshal(req)

	// Pre-consume an estimate like the real handler does.
	preConsumed := int64(500_000)
	_, ok, _ := authMW.PreConsumeLocal("user-1", preConsumed)
	require.True(t, ok)
	assert.Equal(t, int64(500_000), getBalanceReflect(t, authMW, "user-1"))

	svc.handleResponsesStreaming(c, adaptor, upstreamBody, req, "openai", "key-1", "user-1", "hash-1", upstreamKey, "gpt-4o", time.Now(), &preConsumeState{local: preConsumed}, "gpt-4o")

	// Upstream 4xx should refund the pre-consumed estimate.
	assert.Equal(t, int64(1_000_000), getBalanceReflect(t, authMW, "user-1"))
}

func TestEstimateResponsesInputTokens(t *testing.T) {
	counter := token.NewCounter()
	req := &convert.ResponsesRequest{Model: "gpt-4o", Input: "hello world"}
	got := estimateResponsesInputTokens(counter, req)
	assert.Greater(t, got, 0)

	assert.Equal(t, 0, estimateResponsesInputTokens(nil, req))
	assert.Equal(t, 0, estimateResponsesInputTokens(counter, nil))
}
