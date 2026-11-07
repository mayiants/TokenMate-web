package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/internal/auth"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPreConsumeLocal_InsufficientBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)

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
	authMW.SetBalance("poor-user", 1) // 1 micro-USD

	svc := NewHandlerService(cfg, logger)
	svc.authMiddleware = authMW
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   logger,
		Billing:  billing.NewBilling(),
		Simulate: SimulateConfig{Enabled: false},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/responses", nil)
	c.Set(string(userIDKey), "poor-user")
	c.Set("key_type", "general")

	// Estimate 1000 tokens at any price will require more than 1 micro-USD.
	amount, used, rejected := svc.preConsumeLocal(c, "poor-user", "general", "gpt-4o", "openai", 1000)

	assert.True(t, rejected, "expected pre-consume to reject due to insufficient balance")
	assert.True(t, used, "expected pre-consume to have attempted a deduction")
	assert.Greater(t, amount, int64(0), "expected a positive estimated amount")
	assert.Equal(t, http.StatusPaymentRequired, w.Code)
}

func TestPreConsumeLocal_FailOpenWithoutCachedBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)

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
	// No balance seeded: cache is empty, so pre-consume fails open.

	svc := NewHandlerService(cfg, logger)
	svc.authMiddleware = authMW
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   logger,
		Billing:  billing.NewBilling(),
		Simulate: SimulateConfig{Enabled: false},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/responses", nil)
	c.Set(string(userIDKey), "unknown-user")
	c.Set("key_type", "general")

	amount, used, rejected := svc.preConsumeLocal(c, "unknown-user", "general", "gpt-4o", "openai", 100)

	require.False(t, rejected)
	assert.False(t, used)
	assert.Equal(t, int64(0), amount)
}

func TestSettleLocal_ZeroUsageSuccessKeepsEstimate(t *testing.T) {
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
	authMW.SetBalance("zero-usage-user", 1_000_000)

	svc := NewHandlerService(cfg, logger)
	svc.authMiddleware = authMW
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   logger,
		Billing:  billing.NewBilling(),
		Simulate: SimulateConfig{Enabled: false},
	})

	preConsumed := int64(400_000)
	_, ok, handled := authMW.PreConsumeLocal("zero-usage-user", preConsumed)
	require.True(t, ok)
	require.True(t, handled)
	require.Equal(t, int64(600_000), getBalanceReflect(t, authMW, "zero-usage-user"))

	// Success with zero-usage data: the estimate must be kept (charged), not
	// refunded — otherwise the request would be completely free.
	svc.settleLocal("zero-usage-user", preConsumed, &dto.Usage{}, "gpt-4o", "openai", true, 200, "")
	assert.Equal(t, int64(600_000), getBalanceReflect(t, authMW, "zero-usage-user"))

	// Success with real usage: refund the difference between estimate and
	// actual cost.
	_, ok, _ = authMW.PreConsumeLocal("zero-usage-user", preConsumed)
	require.True(t, ok)
	svc.settleLocal("zero-usage-user", preConsumed, &dto.Usage{PromptTokens: 100, CompletionTokens: 50}, "gpt-4o", "openai", true, 200, "")
	got := getBalanceReflect(t, authMW, "zero-usage-user")
	assert.Greater(t, got, int64(200_000), "expected a partial refund against actual usage")
	assert.Less(t, got, int64(600_000), "expected actual usage to be charged")
}

func TestSettleSuccess_UsageMissingChargesEstimate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	actionCh := make(chan string, 2)
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/webhook/settle" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload struct {
			Action string `json:"action"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		actionCh <- payload.Action
		_, _ = w.Write([]byte(`{"success":true,"charged":1}`))
	}))
	defer portal.Close()

	cfg := &config.Config{Server: config.ServerConfig{Mode: "test"}}
	logger := zap.NewNop()
	svc := NewHandlerService(cfg, logger)
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   logger,
		Billing:  billing.NewBillingWithPortal(portal.URL, "secret", 0),
		Simulate: SimulateConfig{Enabled: false},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set("trace_id", "trace-1")

	// Zero-usage success must charge the pre-consumed estimate.
	svc.settleSuccess(c, 500_000, "user-1", "hash-1", "gpt-4o", "openai", &dto.Usage{}, 200, 10)
	select {
	case a := <-actionCh:
		assert.Equal(t, "charge", a)
	case <-time.After(2 * time.Second):
		t.Fatal("settle request not sent for zero-usage success")
	}

	// Success with real usage must unfreeze (normal billing charges actual).
	svc.settleSuccess(c, 500_000, "user-1", "hash-1", "gpt-4o", "openai", &dto.Usage{PromptTokens: 10, CompletionTokens: 5}, 200, 10)
	select {
	case a := <-actionCh:
		assert.Equal(t, "unfreeze", a)
	case <-time.After(2 * time.Second):
		t.Fatal("settle request not sent for usage-bearing success")
	}
}

func TestClassifyTransportError(t *testing.T) {
	if got := classifyTransportError(context.DeadlineExceeded); got != "timeout" {
		t.Errorf("deadline exceeded: got %q, want timeout", got)
	}
	if got := classifyTransportError(&net.DNSError{IsTimeout: true}); got != "timeout" {
		t.Errorf("net timeout: got %q, want timeout", got)
	}
	// Connection refused / DNS failure: upstream never saw the request.
	if got := classifyTransportError(&net.DNSError{IsTimeout: false}); got != "transport_error" {
		t.Errorf("dns error: got %q, want transport_error", got)
	}
	if got := classifyTransportError(errors.New("connection refused")); got != "transport_error" {
		t.Errorf("generic error: got %q, want transport_error", got)
	}
}

// TestSettleLocal_TransportErrorRefunds pins the fix for misclassified network
// errors: a non-timeout transport error (statusCode=0) must refund the
// pre-consumed estimate instead of charging it.
func TestSettleLocal_TransportErrorRefunds(t *testing.T) {
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
	authMW.SetBalance("transport-user", 1_000_000)

	svc := NewHandlerService(cfg, logger)
	svc.authMiddleware = authMW
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   logger,
		Billing:  billing.NewBilling(),
		Simulate: SimulateConfig{Enabled: false},
	})

	preConsumed := int64(400_000)
	_, ok, handled := authMW.PreConsumeLocal("transport-user", preConsumed)
	require.True(t, ok)
	require.True(t, handled)

	svc.settleLocal("transport-user", preConsumed, nil, "gpt-4o", "openai", false, 0, "transport_error")
	assert.Equal(t, int64(1_000_000), getBalanceReflect(t, authMW, "transport-user"),
		"transport error must refund the estimate")

	// A genuine timeout still charges the estimate.
	_, ok, _ = authMW.PreConsumeLocal("transport-user", preConsumed)
	require.True(t, ok)
	svc.settleLocal("transport-user", preConsumed, nil, "gpt-4o", "openai", false, 0, "timeout")
	assert.Equal(t, int64(600_000), getBalanceReflect(t, authMW, "transport-user"),
		"timeout must keep the charge")
}

// TestSettlePreConsume_PortalFallback verifies the combined settle helper:
// portal freeze is settled via the portal API, a local amount short-circuits
// without any portal call.
func TestSettlePreConsume_PortalFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	actionCh := make(chan string, 3)
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/webhook/settle" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload struct {
			Action string `json:"action"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		actionCh <- payload.Action
		_, _ = w.Write([]byte(`{"success":true,"charged":1}`))
	}))
	defer portal.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Billing: config.BillingConfig{
			LocalBalanceCacheEnabled: true,
			LocalBalanceCacheTTL:     10 * time.Second,
		},
	}
	logger := zap.NewNop()
	authMW := auth.NewMiddleware(cfg, logger)
	authMW.SetBalance("pcs-user", 1_000_000)

	svc := NewHandlerService(cfg, logger)
	svc.authMiddleware = authMW
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   logger,
		Billing:  billing.NewBillingWithPortal(portal.URL, "secret", 0),
		Simulate: SimulateConfig{Enabled: false},
	})

	newCtx := func() *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("POST", "/v1/responses", nil)
		c.Set(string(userIDKey), "pcs-user")
		c.Set("key_hash", "hash-1")
		c.Set("trace_id", "trace-1")
		return c
	}

	// Portal freeze, success with usage → unfreeze.
	svc.settlePreConsume(newCtx(), &preConsumeState{portal: 500_000}, &dto.Usage{PromptTokens: 10, CompletionTokens: 5}, "gpt-4o", "openai", true, 200, "", 10)
	select {
	case a := <-actionCh:
		assert.Equal(t, "unfreeze", a)
	case <-time.After(2 * time.Second):
		t.Fatal("portal settle not sent for portal pre-consume success")
	}

	// Portal freeze, upstream 4xx → unfreeze (no charge).
	svc.settlePreConsume(newCtx(), &preConsumeState{portal: 500_000}, nil, "gpt-4o", "openai", false, 400, "4xx", 10)
	select {
	case a := <-actionCh:
		assert.Equal(t, "unfreeze", a)
	case <-time.After(2 * time.Second):
		t.Fatal("portal settle not sent for portal pre-consume 4xx")
	}

	// Local pre-consume short-circuits: no portal call at all.
	_, ok, handled := authMW.PreConsumeLocal("pcs-user", 400_000)
	require.True(t, ok)
	require.True(t, handled)
	svc.settlePreConsume(newCtx(), &preConsumeState{local: 400_000}, &dto.Usage{PromptTokens: 10, CompletionTokens: 5}, "gpt-4o", "openai", true, 200, "", 10)
	select {
	case a := <-actionCh:
		t.Fatalf("unexpected portal call for local pre-consume, action=%s", a)
	case <-time.After(300 * time.Millisecond):
	}
}
