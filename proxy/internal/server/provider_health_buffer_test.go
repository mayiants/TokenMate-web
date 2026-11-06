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

	"github.com/ai-proxy/ai-proxy-go/pkg/billing"
	"github.com/stretchr/testify/assert"
)

func TestNewProviderHealthBuffer_Defaults(t *testing.T) {
	b := NewProviderHealthBuffer(nil, noopLogger(), 0, 0)
	assert.NotNil(t, b)
	assert.Equal(t, 500, b.maxBatch, "default maxBatch is 500")
	assert.Equal(t, 30*time.Second, b.flushInterval, "default flushInterval is 30s")
}

func TestNewProviderHealthBuffer_CustomValues(t *testing.T) {
	b := NewProviderHealthBuffer(nil, noopLogger(), 10, 5*time.Second)
	assert.NotNil(t, b)
	assert.Equal(t, 10, b.maxBatch)
	assert.Equal(t, 5*time.Second, b.flushInterval)
}

func TestProviderHealthBuffer_Add_NilBuffer(t *testing.T) {
	var b *ProviderHealthBuffer
	// Should not panic
	b.Add(&billing.ProviderHealthRecord{
		Provider: "openai",
		Model:    "gpt-4o",
	})
}

func TestProviderHealthBuffer_Add_NilRecord(t *testing.T) {
	b := NewProviderHealthBuffer(nil, noopLogger(), 10, time.Minute)
	// Should not panic
	b.Add(nil)
	b.Flush() // Flush with empty buffer
}

func TestProviderHealthBuffer_Add_Flush_WithMockServer(t *testing.T) {
	// Set fallback log dir to temp directory
	tmpDir := t.TempDir()
	os.Setenv("FALLBACK_LOG_DIR", tmpDir)
	defer os.Unsetenv("FALLBACK_LOG_DIR")

	var requestCount int32
	var receivedRecords []*billing.ProviderHealthRecord

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/webhook/provider-health-batch", r.URL.Path)

		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		recordsRaw, ok := body["records"].([]interface{})
		if ok {
			for _, recRaw := range recordsRaw {
				recJSON, _ := json.Marshal(recRaw)
				var rec billing.ProviderHealthRecord
				json.Unmarshal(recJSON, &rec)
				receivedRecords = append(receivedRecords, &rec)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"ok":true}`)
	}))
	defer mockServer.Close()

	billingClient := billing.NewBillingWithPortal(mockServer.URL, "test-secret", 60*time.Second)
	logger := noopLogger()
	buf := NewProviderHealthBuffer(billingClient, logger, 5, time.Hour)

	// Add records
	buf.Add(&billing.ProviderHealthRecord{
		Provider:   "openai",
		Model:      "gpt-4o",
		TraceID:    "trace-1",
		StatusCode: 200,
		LatencyMs:  150,
	})
	buf.Add(&billing.ProviderHealthRecord{
		Provider:   "anthropic",
		Model:      "claude-3",
		TraceID:    "trace-2",
		StatusCode: 200,
		LatencyMs:  300,
	})

	// Flush
	buf.Flush()

	// Give time for the HTTP request to complete
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount))
	assert.Len(t, receivedRecords, 2)
}

func TestProviderHealthBuffer_AutoFlushOnMaxBatch(t *testing.T) {
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
	buf := NewProviderHealthBuffer(billingClient, noopLogger(), 3, time.Hour)

	// Add 3 records — should trigger auto-flush
	for i := 0; i < 3; i++ {
		buf.Add(&billing.ProviderHealthRecord{
			Provider:   "openai",
			Model:      "gpt-4o",
			TraceID:    fmt.Sprintf("trace-%d", i),
			StatusCode: 200,
		})
	}

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount), "auto-flush should trigger on max batch")
}

func TestProviderHealthBuffer_Flush_EmptyBuffer(t *testing.T) {
	b := NewProviderHealthBuffer(nil, noopLogger(), 10, time.Minute)
	// Should not panic
	b.Flush()
}

func TestProviderHealthBuffer_StartFlushLoop(t *testing.T) {
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
	buf := NewProviderHealthBuffer(billingClient, noopLogger(), 100, 50*time.Millisecond)

	buf.Add(&billing.ProviderHealthRecord{
		Provider:   "openai",
		Model:      "gpt-4o",
		TraceID:    "trace-loop",
		StatusCode: 200,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	buf.StartFlushLoop(ctx)

	// Wait for the ticker to fire
	time.Sleep(200 * time.Millisecond)

	// Cancel context to trigger final flush
	cancel()
	time.Sleep(100 * time.Millisecond)

	assert.GreaterOrEqual(t, atomic.LoadInt32(&requestCount), int32(1), "flush loop should have flushed at least once")
}

func TestProviderHealthBuffer_NilBilling_DoesNotSend(t *testing.T) {
	buf := NewProviderHealthBuffer(nil, noopLogger(), 5, time.Hour)
	buf.Add(&billing.ProviderHealthRecord{
		Provider:   "openai",
		Model:      "gpt-4o",
		StatusCode: 200,
	})
	buf.Flush() // Should not panic — billing is nil, sendBatch logs warning
}

func TestProviderHealthBuffer_SendBatch_NilBilling(t *testing.T) {
	buf := NewProviderHealthBuffer(nil, noopLogger(), 5, time.Hour)
	// Should not panic
	buf.sendBatch([]*billing.ProviderHealthRecord{
		{Provider: "openai"},
	})
}
