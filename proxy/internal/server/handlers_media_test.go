package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/auth"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"github.com/mayiants/ai-proxy-go/pkg/keypool"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mediaTestService builds a HandlerService with the local balance cache
// enabled, mirroring the setup in handlers_billing_test.go.
func mediaTestService(t *testing.T, balance int64, userID string) *HandlerService {
	t.Helper()
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
	if balance >= 0 {
		authMW.SetBalance(userID, balance)
	}

	svc := NewHandlerService(cfg, logger)
	svc.authMiddleware = authMW
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   logger,
		Billing:  billing.NewBilling(),
		Simulate: SimulateConfig{Enabled: false},
	})
	return svc
}

func TestValidTaskIDRE(t *testing.T) {
	long65 := make([]byte, 65)
	for i := range long65 {
		long65[i] = 'a'
	}
	valid := []string{"task_abc123", "cgt-123_XYZ", "a", "A1_b-"}
	for _, id := range valid {
		assert.True(t, validTaskIDRE.MatchString(id), "expected %q to be valid", id)
	}
	invalid := []string{"../evil", "a/b", "a?x=1", "a b", "", "x:y", "%2e%2e", string(long65)}
	for _, id := range invalid {
		assert.False(t, validTaskIDRE.MatchString(id), "expected %q to be invalid", id)
	}
}

func TestHandleGetImageGeneration_RejectsMalformedTaskID(t *testing.T) {
	svc := mediaTestService(t, -1, "user-1")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/images/generations/..%2Fevil?model=gpt-image-1", nil)
	c.Params = gin.Params{{Key: "task_id", Value: "../evil"}}

	svc.handleGetImageGeneration(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"].(map[string]interface{})["message"], "task_id")
}

func TestHandleGetVideoGeneration_RejectsMalformedTaskID(t *testing.T) {
	svc := mediaTestService(t, -1, "user-1")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/video/generations/a%3Fx?model=veo-3", nil)
	c.Params = gin.Params{{Key: "task_id", Value: "a?x"}}

	svc.handleGetVideoGeneration(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAsyncTaskTerminalStatus(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		status   string
		terminal bool
	}{
		{"ark success", `{"id":"cgt-1","status":"success"}`, "success", true},
		{"kling uppercase SUCCESS", `{"data":{"status":"SUCCESS"}}`, "success", true},
		{"kling uppercase FAILED", `{"data":{"status":"FAILED"}}`, "failed", true},
		{"tokenrouter FAILURE", `{"data":{"status":"FAILURE"}}`, "failed", true},
		{"ark running", `{"id":"cgt-1","status":"running"}`, "", false},
		{"ark queued", `{"id":"cgt-1","status":"queued"}`, "", false},
		{"ark failed", `{"id":"cgt-1","status":"failed"}`, "failed", true},
		{"ark expired", `{"id":"cgt-1","status":"expired"}`, "failed", true},
		{"ark canceled", `{"id":"cgt-1","status":"canceled"}`, "failed", true},
		{"data object success", `{"data":{"status":"completed"}}`, "success", true},
		{"data object processing", `{"data":{"status":"processing"}}`, "", false},
		{"data array failed", `{"data":[{"status":"failed"}]}`, "failed", true},
		{"no status", `{"data":{"url":"http://x/y.mp4"}}`, "", false},
		{"invalid json", `{not json`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, terminal := asyncTaskTerminalStatus([]byte(tc.body))
			assert.Equal(t, tc.terminal, terminal)
			assert.Equal(t, tc.status, status)
		})
	}
}

func TestPreConsumeMedia_InsufficientBalance(t *testing.T) {
	svc := mediaTestService(t, 1, "media-poor-user") // 1 micro-USD

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/images/generations", nil)
	c.Set(string(userIDKey), "media-poor-user")
	c.Set("key_type", "general")

	pcs, rejected := svc.preConsumeMedia(c, "media-poor-user", "general", "gpt-image-1", "openai", 50_000)

	assert.True(t, rejected)
	assert.Nil(t, pcs)
	assert.Equal(t, http.StatusPaymentRequired, w.Code)
}

func TestPreConsumeMedia_SufficientBalanceReserves(t *testing.T) {
	svc := mediaTestService(t, 1_000_000, "media-rich-user")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/video/generations", nil)
	c.Set(string(userIDKey), "media-rich-user")
	c.Set("key_type", "general")

	pcs, rejected := svc.preConsumeMedia(c, "media-rich-user", "general", "veo-3", "vertex", 50_000)

	require.False(t, rejected)
	require.NotNil(t, pcs)
	assert.Equal(t, int64(50_000), pcs.local)
	// The reservation is visible in the local balance cache.
	remaining, ok, handled := svc.authMiddleware.PreConsumeLocal("media-rich-user", 50_000)
	require.True(t, handled)
	require.True(t, ok)
	assert.Equal(t, int64(900_000), remaining)
}

// mediaRedisClient returns a Redis client when one is reachable (proxy Redis
// on 6380, otherwise standard 6379), skipping the test otherwise — mirroring
// internal/auth's balance cache tests.
func mediaRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	addrs := []string{"127.0.0.1:6380", "127.0.0.1:6379"}
	for _, addr := range addrs {
		rdb := redis.NewClient(&redis.Options{Addr: addr})
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		err := rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			return rdb
		}
		_ = rdb.Close()
	}
	t.Skip("No reachable Redis for media async billing test")
	return nil
}

func TestBillAsyncMediaTask_BillsExactlyOnce(t *testing.T) {
	rdb := mediaRedisClient(t)
	defer rdb.Close()

	svc := mediaTestService(t, -1, "user-1")
	svc.SetRedisClient(rdb)
	svc.usageBuffer = NewUsageBuffer(billing.NewBilling(), zap.NewNop(), 100, time.Hour)

	taskID := "task-test-bill-once"
	_ = rdb.Del(context.Background(), mediaTaskBillingKey(taskID), mediaTaskBilledKey(taskID)).Err()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/images/generations/"+taskID+"?model=gpt-image-1", nil)
	c.Set("key_hash", "hash-1")
	c.Set("image_count", 2)

	svc.storeMediaTaskBilling(c, taskID, "upkey-1", "gpt-image-1", "openai", 0.08, false)

	// Two consecutive successful polls must bill exactly one usage record.
	svc.billAsyncMediaTask(c, taskID, "success", 10)
	svc.billAsyncMediaTask(c, taskID, "success", 10)

	svc.usageBuffer.mu.Lock()
	records := len(svc.usageBuffer.records)
	svc.usageBuffer.mu.Unlock()
	assert.Equal(t, 1, records, "task must be billed exactly once across repeated polls")

	record := svc.usageBuffer.records[0]
	assert.Equal(t, "hash-1", record.APIKeyHash)
	assert.Equal(t, "gpt-image-1", record.Model)
	assert.Equal(t, 2, record.ImageCount)
	assert.Equal(t, 1, record.RequestCount)
}

func TestBillAsyncMediaTask_MissingContextDoesNotPoisonGuard(t *testing.T) {
	// Regression: a terminal-success poll that cannot load the billing
	// context (transient Redis GET failure or expiry) must NOT consume the
	// SETNX idempotency guard. Otherwise the task becomes permanently
	// unbillable — a silent revenue leak. A later poll that recovers the
	// context must still settle the task exactly once.
	rdb := mediaRedisClient(t)
	defer rdb.Close()

	svc := mediaTestService(t, -1, "user-1")
	svc.SetRedisClient(rdb)
	svc.usageBuffer = NewUsageBuffer(billing.NewBilling(), zap.NewNop(), 100, time.Hour)

	taskID := "task-test-guard-not-poisoned"
	_ = rdb.Del(context.Background(), mediaTaskBillingKey(taskID), mediaTaskBilledKey(taskID)).Err()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/images/generations/"+taskID+"?model=gpt-image-1", nil)
	c.Set("key_hash", "hash-1")
	c.Set("image_count", 1)

	// First poll: no dispatch context stored yet (simulates a lost/expired
	// context). Must bill nothing and must not set the billed guard.
	svc.billAsyncMediaTask(c, taskID, "success", 10)
	svc.usageBuffer.mu.Lock()
	afterMissing := len(svc.usageBuffer.records)
	svc.usageBuffer.mu.Unlock()
	assert.Equal(t, 0, afterMissing, "missing-context poll must bill nothing")

	billedGuard, _ := rdb.Get(context.Background(), mediaTaskBilledKey(taskID)).Result()
	assert.Empty(t, billedGuard, "missing-context poll must not consume the idempotency guard")

	// Context now becomes available; a later poll must still settle once.
	svc.storeMediaTaskBilling(c, taskID, "upkey-1", "gpt-image-1", "openai", 0.08, false)
	svc.billAsyncMediaTask(c, taskID, "success", 10)
	svc.billAsyncMediaTask(c, taskID, "success", 10)

	svc.usageBuffer.mu.Lock()
	records := len(svc.usageBuffer.records)
	svc.usageBuffer.mu.Unlock()
	assert.Equal(t, 1, records, "task must still be billed exactly once after context recovers")
}

func TestBillAsyncMediaTask_WithoutDispatchContextBillsNothing(t *testing.T) {
	rdb := mediaRedisClient(t)
	defer rdb.Close()

	svc := mediaTestService(t, -1, "user-1")
	svc.SetRedisClient(rdb)
	svc.usageBuffer = NewUsageBuffer(billing.NewBilling(), zap.NewNop(), 100, time.Hour)

	taskID := "task-test-no-context"
	_ = rdb.Del(context.Background(), mediaTaskBillingKey(taskID), mediaTaskBilledKey(taskID)).Err()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/video/generations/"+taskID+"?model=veo-3", nil)

	svc.billAsyncMediaTask(c, taskID, "success", 10)

	svc.usageBuffer.mu.Lock()
	defer svc.usageBuffer.mu.Unlock()
	assert.Empty(t, svc.usageBuffer.records, "task without dispatch billing context must not be billed")
}

func TestHandleGetAudioGeneration_RejectsMalformedTaskID(t *testing.T) {
	svc := mediaTestService(t, -1, "user-1")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/audio/generations/..%2Fevil?model=tts-async-1", nil)
	c.Params = gin.Params{{Key: "task_id", Value: "../evil"}}

	svc.handleGetAudioGeneration(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"].(map[string]interface{})["message"], "task_id")
}

func TestHandleGetAudioGeneration_RequiresModel(t *testing.T) {
	svc := mediaTestService(t, -1, "user-1")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/audio/generations/task_abc123", nil)
	c.Params = gin.Params{{Key: "task_id", Value: "task_abc123"}}

	svc.handleGetAudioGeneration(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errObj := resp["error"].(map[string]interface{})
	assert.Equal(t, "MISSING_MODEL", errObj["code"])
}

// TestAudioGenerations_AsyncDispatchAndPoll exercises the full async audio
// flow against a fake upstream: dispatch returns a wrapped task envelope and
// defers billing; the poll endpoint bills exactly once at terminal success.
func TestAudioGenerations_AsyncDispatchAndPoll(t *testing.T) {
	rdb := mediaRedisClient(t)
	defer rdb.Close()

	gin.SetMode(gin.TestMode)

	const taskID = "task-audio-e2e"
	ctx := context.Background()
	_ = rdb.Del(ctx, mediaTaskBillingKey(taskID), mediaTaskBilledKey(taskID)).Err()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/audio/generations":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{"task_id": taskID},
			})
		case r.Method == "GET" && r.URL.Path == "/audio/generations/"+taskID:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     taskID,
				"status": "success",
				"data":   map[string]interface{}{"audio_url": "https://example.com/x.mp3"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Auth:   config.AuthConfig{EncryptionKey: "test-encryption-key-32-bytes-long!!"},
		Server: config.ServerConfig{Mode: "test"},
	}
	logger := zap.NewNop()

	kp := keypool.NewManager(cfg, logger)
	defer kp.Close()
	require.NoError(t, kp.AddKey(&keypool.Key{
		KeyStatic: keypool.KeyStatic{
			ID:       "upkey-audio-1",
			Provider: "openai",
			Key:      "sk-upstream-test",
			Priority: "normal",
			Models:   []string{"tts-async-1"},
			BaseURL:  upstream.URL,
		},
	}))

	svc := NewHandlerService(cfg, logger)
	svc.SetKeyPoolManager(kp)
	svc.SetRedisClient(rdb)
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   logger,
		Billing:  billing.NewBilling(),
		Simulate: SimulateConfig{Enabled: false},
	})
	svc.usageBuffer = NewUsageBuffer(billing.NewBilling(), logger, 100, time.Hour)

	setAuth := func(c *gin.Context) {
		c.Set(string(keyIDKey), "key-1")
		c.Set(string(userIDKey), "user-1")
		c.Set("key_hash", "hash-audio-e2e")
		c.Set("access_mode", "general")
		c.Set("key_type", "general")
	}

	// Dispatch: upstream returns a task envelope, proxy wraps it and defers billing.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/audio/generations",
		bytes.NewReader([]byte(`{"model":"tts-async-1","prompt":"hello"}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c)

	svc.handleAudioGenerations(c)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var dispatchResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &dispatchResp))
	assert.Equal(t, taskID, dispatchResp["id"], "dispatch must expose the upstream task id")

	svc.usageBuffer.mu.Lock()
	assert.Empty(t, svc.usageBuffer.records, "async dispatch must not bill immediately")
	svc.usageBuffer.mu.Unlock()

	// Poll twice at terminal success: billed exactly once.
	poll := func() *httptest.ResponseRecorder {
		wr := httptest.NewRecorder()
		pc, _ := gin.CreateTestContext(wr)
		pc.Request, _ = http.NewRequest("GET", "/v1/audio/generations/"+taskID+"?model=tts-async-1", nil)
		pc.Params = gin.Params{{Key: "task_id", Value: taskID}}
		setAuth(pc)
		svc.handleGetAudioGeneration(pc)
		return wr
	}

	w1 := poll()
	require.Equal(t, http.StatusOK, w1.Code, "body: %s", w1.Body.String())
	assert.Contains(t, w1.Body.String(), `"status":"success"`)
	w2 := poll()
	require.Equal(t, http.StatusOK, w2.Code)

	svc.usageBuffer.mu.Lock()
	defer svc.usageBuffer.mu.Unlock()
	require.Len(t, svc.usageBuffer.records, 1, "terminal success must bill exactly once across polls")
	record := svc.usageBuffer.records[0]
	assert.Equal(t, "hash-audio-e2e", record.APIKeyHash)
	assert.Equal(t, "tts-async-1", record.Model)
}

func TestBillAsyncMediaTask_ChargeOnDispatchFailureKeepsCharge(t *testing.T) {
	rdb := mediaRedisClient(t)
	defer rdb.Close()

	svc := mediaTestService(t, -1, "user-1")
	svc.SetRedisClient(rdb)
	svc.usageBuffer = NewUsageBuffer(billing.NewBilling(), zap.NewNop(), 100, time.Hour)

	taskID := "task-test-norefund"
	_ = rdb.Del(context.Background(), mediaTaskBillingKey(taskID), mediaTaskBilledKey(taskID)).Err()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/video/generations/"+taskID+"?model=kling-v3", nil)
	c.Set("key_hash", "hash-norefund")

	svc.storeMediaTaskBilling(c, taskID, "upkey-1", "kling-v3", "tokenrouter", 0.56, true)

	// TokenRouter-style providers keep the dispatch charge even when the task
	// fails, so a terminal failure poll must NOT emit a refund. Repeated
	// polls must also not produce any record (idempotent settle).
	svc.billAsyncMediaTask(c, taskID, "failed", 10)
	svc.billAsyncMediaTask(c, taskID, "failed", 10)

	svc.usageBuffer.mu.Lock()
	defer svc.usageBuffer.mu.Unlock()
	assert.Empty(t, svc.usageBuffer.records, "charge-on-dispatch task keeps the dispatch charge on terminal failure; no refund")
}

func TestBillAsyncMediaTask_ChargeOnDispatchSuccessDoesNotRebill(t *testing.T) {
	rdb := mediaRedisClient(t)
	defer rdb.Close()

	svc := mediaTestService(t, -1, "user-1")
	svc.SetRedisClient(rdb)
	svc.usageBuffer = NewUsageBuffer(billing.NewBilling(), zap.NewNop(), 100, time.Hour)

	taskID := "task-test-cod-success"
	_ = rdb.Del(context.Background(), mediaTaskBillingKey(taskID), mediaTaskBilledKey(taskID)).Err()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/v1/video/generations/"+taskID+"?model=kling-v3", nil)

	svc.storeMediaTaskBilling(c, taskID, "upkey-1", "kling-v3", "tokenrouter", 0.56, true)
	svc.billAsyncMediaTask(c, taskID, "success", 10)

	svc.usageBuffer.mu.Lock()
	defer svc.usageBuffer.mu.Unlock()
	assert.Empty(t, svc.usageBuffer.records, "charge-on-dispatch task billed at dispatch must not bill again on success")
}

// TestVideoGenerations_ChargeOnDispatchFlow exercises dispatch-time billing
// against a fake upstream that bills at task creation (TokenRouter
// semantics): dispatch bills immediately, and a terminal failure poll
// keeps the charge (upstream never refunds task-level failures).
func TestVideoGenerations_ChargeOnDispatchFlow(t *testing.T) {
	rdb := mediaRedisClient(t)
	defer rdb.Close()

	gin.SetMode(gin.TestMode)

	const taskID = "task-cod-e2e"
	ctx := context.Background()
	_ = rdb.Del(ctx, mediaTaskBillingKey(taskID), mediaTaskBilledKey(taskID)).Err()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/video/generations":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{"task_id": taskID},
			})
		case r.Method == "GET" && r.URL.Path == "/video/generations/"+taskID:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     taskID,
				"status": "failed",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Auth:   config.AuthConfig{EncryptionKey: "test-encryption-key-32-bytes-long!!"},
		Server: config.ServerConfig{Mode: "test"},
		Billing: config.BillingConfig{
			AsyncChargeOnDispatchProviders: []string{"tokenrouter"},
		},
	}
	logger := zap.NewNop()

	kp := keypool.NewManager(cfg, logger)
	defer kp.Close()
	require.NoError(t, kp.AddKey(&keypool.Key{
		KeyStatic: keypool.KeyStatic{
			ID:       "upkey-cod-1",
			Provider: "tokenrouter",
			Key:      "sk-upstream-test",
			Priority: "normal",
			Models:   []string{"kling-v3"},
			BaseURL:  upstream.URL,
		},
	}))

	svc := NewHandlerService(cfg, logger)
	svc.SetKeyPoolManager(kp)
	svc.SetRedisClient(rdb)
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   logger,
		Billing:  billing.NewBilling(),
		Simulate: SimulateConfig{Enabled: false},
	})
	svc.usageBuffer = NewUsageBuffer(billing.NewBilling(), logger, 100, time.Hour)

	setAuth := func(c *gin.Context) {
		c.Set(string(keyIDKey), "key-1")
		c.Set(string(userIDKey), "user-1")
		c.Set("key_hash", "hash-cod-e2e")
		c.Set("access_mode", "general")
		c.Set("key_type", "general")
	}

	// Dispatch: charge-on-dispatch provider bills immediately.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/video/generations",
		bytes.NewReader([]byte(`{"model":"kling-v3","prompt":"hello"}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c)

	svc.handleVideoGenerations(c)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var dispatchResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &dispatchResp))
	assert.Equal(t, taskID, dispatchResp["id"])

	svc.usageBuffer.mu.Lock()
	require.Len(t, svc.usageBuffer.records, 1, "charge-on-dispatch must bill at dispatch time")
	assert.Equal(t, "kling-v3", svc.usageBuffer.records[0].Model)
	svc.usageBuffer.mu.Unlock()

	// Poll at terminal failure twice: no refund, no extra charge — the
	// dispatch charge stands, matching upstream cost semantics.
	poll := func() {
		wr := httptest.NewRecorder()
		pc, _ := gin.CreateTestContext(wr)
		pc.Request, _ = http.NewRequest("GET", "/v1/video/generations/"+taskID+"?model=kling-v3", nil)
		pc.Params = gin.Params{{Key: "task_id", Value: taskID}}
		setAuth(pc)
		svc.handleGetVideoGeneration(pc)
	}
	poll()
	poll()

	svc.usageBuffer.mu.Lock()
	defer svc.usageBuffer.mu.Unlock()
	require.Len(t, svc.usageBuffer.records, 1, "dispatch charge only; terminal failure must not refund or re-bill")
}
