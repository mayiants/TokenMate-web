package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"github.com/stretchr/testify/assert"
)

func TestNewUsageBuffer_Defaults(t *testing.T) {
	b := NewUsageBuffer(nil, noopLogger(), 0, 0)
	assert.NotNil(t, b)
	assert.Equal(t, 100, b.maxBatch, "default maxBatch is 100")
	assert.Equal(t, 3*time.Second, b.flushInterval, "default flushInterval is 3s")
}

func TestNewUsageBuffer_CustomValues(t *testing.T) {
	b := NewUsageBuffer(nil, noopLogger(), 10, 5*time.Second)
	assert.NotNil(t, b)
	assert.Equal(t, 10, b.maxBatch)
	assert.Equal(t, 5*time.Second, b.flushInterval)
}

func TestUsageBuffer_Add_Flush_WithMockServer(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("FALLBACK_LOG_DIR", tmpDir)
	defer os.Unsetenv("FALLBACK_LOG_DIR")

	var requestCount int32
	var receivedRecords []*billing.UsageRecord

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)

		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		recordsRaw, ok := body["records"].([]interface{})
		if ok {
			for _, recRaw := range recordsRaw {
				recJSON, _ := json.Marshal(recRaw)
				var rec billing.UsageRecord
				json.Unmarshal(recJSON, &rec)
				receivedRecords = append(receivedRecords, &rec)
			}
		} else {
			// Single record send (to /api/webhook/usage)
			var rec billing.UsageRecord
			bodyJSON, _ := json.Marshal(body)
			json.Unmarshal(bodyJSON, &rec)
			if rec.TraceID != "" {
				receivedRecords = append(receivedRecords, &rec)
			}
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"ok":true}`)
	}))
	defer mockServer.Close()

	billingClient := billing.NewBillingWithPortal(mockServer.URL, "test-secret", 60*time.Second)
	buf := NewUsageBuffer(billingClient, noopLogger(), 100, time.Hour)

	// Add 2 records — batch send path
	buf.Add(&billing.UsageRecord{TraceID: "trace-1", Model: "gpt-4o", Provider: "openai"})
	buf.Add(&billing.UsageRecord{TraceID: "trace-2", Model: "claude-3", Provider: "anthropic"})

	buf.Flush()

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount))
	assert.Len(t, receivedRecords, 2)
}

func TestUsageBuffer_Add_SingleRecord_MockServer(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("FALLBACK_LOG_DIR", tmpDir)
	defer os.Unsetenv("FALLBACK_LOG_DIR")

	var requestCount int32
	var lastPath string

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		lastPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"ok":true}`)
	}))
	defer mockServer.Close()

	billingClient := billing.NewBillingWithPortal(mockServer.URL, "test-secret", 60*time.Second)
	buf := NewUsageBuffer(billingClient, noopLogger(), 100, time.Hour)

	buf.Add(&billing.UsageRecord{TraceID: "trace-single", Model: "gpt-4o", Provider: "openai"})
	buf.Flush()

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount))
	// Single record goes to /api/webhook/usage
	assert.Equal(t, "/api/webhook/usage", lastPath)
}

func TestUsageBuffer_AutoFlushOnMaxBatch(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("FALLBACK_LOG_DIR", tmpDir)
	defer os.Unsetenv("FALLBACK_LOG_DIR")

	var requestCount int32

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	billingClient := billing.NewBillingWithPortal(mockServer.URL, "test-secret", 60*time.Second)
	buf := NewUsageBuffer(billingClient, noopLogger(), 3, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buf.StartFlushLoop(ctx)

	for i := 0; i < 3; i++ {
		buf.Add(&billing.UsageRecord{
			TraceID:  fmt.Sprintf("trace-%d", i),
			Model:    "gpt-4o",
			Provider: "openai",
		})
	}

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount), "auto-flush should trigger on max batch")
}

func TestUsageBuffer_Add_NilRecord(t *testing.T) {
	// Nil records are just appended — handling nil records in sendBatch/client
	b := NewUsageBuffer(nil, noopLogger(), 10, time.Minute)
	b.Add(nil)
	b.Flush() // Should not panic — billing is nil, skips send
}

func TestUsageBuffer_Flush_EmptyBuffer(t *testing.T) {
	b := NewUsageBuffer(nil, noopLogger(), 10, time.Minute)
	b.Flush() // Should not panic
}

func TestUsageBuffer_StartFlushLoop(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("FALLBACK_LOG_DIR", tmpDir)
	defer os.Unsetenv("FALLBACK_LOG_DIR")

	var requestCount int32

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	billingClient := billing.NewBillingWithPortal(mockServer.URL, "test-secret", 60*time.Second)
	buf := NewUsageBuffer(billingClient, noopLogger(), 100, 50*time.Millisecond)

	buf.Add(&billing.UsageRecord{TraceID: "trace-loop", Model: "gpt-4o", Provider: "openai"})

	ctx, cancel := context.WithCancel(context.Background())
	buf.StartFlushLoop(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	assert.GreaterOrEqual(t, atomic.LoadInt32(&requestCount), int32(1), "flush loop should have flushed at least once")
}

func TestUsageBuffer_NilBilling_SkipsSend(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("FALLBACK_LOG_DIR", tmpDir)
	defer os.Unsetenv("FALLBACK_LOG_DIR")

	buf := NewUsageBuffer(nil, noopLogger(), 10, time.Hour)
	buf.Add(&billing.UsageRecord{TraceID: "trace-1", Model: "gpt-4o", Provider: "openai"})
	buf.Flush()
	// Should not panic
}
