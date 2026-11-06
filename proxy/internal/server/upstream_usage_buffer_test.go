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

func TestNewUpstreamUsageBuffer_Defaults(t *testing.T) {
	b := NewUpstreamUsageBuffer(nil, noopLogger(), 0, 0)
	assert.NotNil(t, b)
	assert.Equal(t, 500, b.maxBatch, "default maxBatch is 500")
	assert.Equal(t, 30*time.Second, b.flushInterval, "default flushInterval is 30s")
}

func TestNewUpstreamUsageBuffer_CustomValues(t *testing.T) {
	b := NewUpstreamUsageBuffer(nil, noopLogger(), 10, 5*time.Second)
	assert.NotNil(t, b)
	assert.Equal(t, 10, b.maxBatch)
	assert.Equal(t, 5*time.Second, b.flushInterval)
}

func TestUpstreamUsageBuffer_Add_NilBuffer(t *testing.T) {
	var b *UpstreamUsageBuffer
	// Should not panic
	b.Add(&billing.UpstreamUsageRecord{
		UpstreamKeyID: "key-1",
		Model:         "gpt-4o",
	})
}

func TestUpstreamUsageBuffer_Add_NilRecord(t *testing.T) {
	b := NewUpstreamUsageBuffer(nil, noopLogger(), 10, time.Minute)
	b.Add(nil)
	b.Flush()
}

func TestUpstreamUsageBuffer_Add_Aggregation(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("FALLBACK_LOG_DIR", tmpDir)
	defer os.Unsetenv("FALLBACK_LOG_DIR")

	b := NewUpstreamUsageBuffer(nil, noopLogger(), 100, time.Hour)

	// Add first record for key-1:gpt-4o:2026-06-21
	rec1 := &billing.UpstreamUsageRecord{
		UpstreamKeyID: "key-1",
		Provider:      "openai",
		Model:         "gpt-4o",
		Date:          "2026-06-21",
		RequestCount:  1,
		InputTokens:   100,
		OutputTokens:  50,
		LatencyMs:     200,
	}
	b.Add(rec1)

	// Add second record for the SAME key:model:date — should aggregate
	rec2 := &billing.UpstreamUsageRecord{
		UpstreamKeyID: "key-1",
		Provider:      "openai",
		Model:         "gpt-4o",
		Date:          "2026-06-21",
		RequestCount:  1,
		InputTokens:   50,
		OutputTokens:  30,
		LatencyMs:     100,
	}
	b.Add(rec2)

	// Check aggregation
	b.mu.Lock()
	key := fmt.Sprintf("%s:%s:%s", "key-1", "gpt-4o", "2026-06-21")
	agg, ok := b.records[key]
	b.mu.Unlock()

	assert.True(t, ok, "aggregated record should exist")
	assert.Equal(t, 2, agg.RequestCount)
	assert.Equal(t, int64(150), agg.InputTokens)
	assert.Equal(t, int64(80), agg.OutputTokens)

	// Add third record for a DIFFERENT model — should be separate
	rec3 := &billing.UpstreamUsageRecord{
		UpstreamKeyID: "key-1",
		Provider:      "openai",
		Model:         "claude-3",
		Date:          "2026-06-21",
		RequestCount:  1,
		InputTokens:   200,
		OutputTokens:  100,
		LatencyMs:     300,
	}
	b.Add(rec3)

	b.mu.Lock()
	key2 := fmt.Sprintf("%s:%s:%s", "key-1", "claude-3", "2026-06-21")
	recDiff, ok2 := b.records[key2]
	count := len(b.records)
	b.mu.Unlock()

	assert.True(t, ok2, "different model should create separate record")
	assert.Equal(t, 2, count, "buffer should have 2 aggregated records")
	assert.Equal(t, 1, recDiff.RequestCount)
}

func TestUpstreamUsageBuffer_Flush_WithMockServer(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("FALLBACK_LOG_DIR", tmpDir)
	defer os.Unsetenv("FALLBACK_LOG_DIR")

	var requestCount int32
	var receivedRecords []*billing.UpstreamUsageRecord

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/webhook/upstream-usage-batch", r.URL.Path)

		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		recordsRaw, ok := body["records"].([]interface{})
		if ok {
			for _, recRaw := range recordsRaw {
				recJSON, _ := json.Marshal(recRaw)
				var rec billing.UpstreamUsageRecord
				json.Unmarshal(recJSON, &rec)
				receivedRecords = append(receivedRecords, &rec)
			}
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"ok":true}`)
	}))
	defer mockServer.Close()

	billingClient := billing.NewBillingWithPortal(mockServer.URL, "test-secret", 60*time.Second)
	buf := NewUpstreamUsageBuffer(billingClient, noopLogger(), 5, time.Hour)

	buf.Add(&billing.UpstreamUsageRecord{
		UpstreamKeyID: "key-1",
		Provider:      "openai",
		Model:         "gpt-4o",
		Date:          "2026-06-21",
		RequestCount:  1,
		InputTokens:   100,
		OutputTokens:  50,
	})
	buf.Add(&billing.UpstreamUsageRecord{
		UpstreamKeyID: "key-2",
		Provider:      "anthropic",
		Model:         "claude-3",
		Date:          "2026-06-21",
		RequestCount:  1,
		InputTokens:   200,
		OutputTokens:  100,
	})

	buf.Flush()

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount))
	assert.Len(t, receivedRecords, 2)
}

func TestUpstreamUsageBuffer_AutoFlushOnMaxBatch(t *testing.T) {
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
	buf := NewUpstreamUsageBuffer(billingClient, noopLogger(), 3, time.Hour)

	// Add 3 records with different keys (each creates a new entry in the map)
	for i := 0; i < 3; i++ {
		buf.Add(&billing.UpstreamUsageRecord{
			UpstreamKeyID: fmt.Sprintf("key-%d", i),
			Provider:      "openai",
			Model:         "gpt-4o",
			Date:          "2026-06-21",
			RequestCount:  1,
		})
	}

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount), "auto-flush should trigger on max batch")
}

func TestUpstreamUsageBuffer_Flush_EmptyBuffer(t *testing.T) {
	b := NewUpstreamUsageBuffer(nil, noopLogger(), 10, time.Minute)
	b.Flush() // Should not panic
}

func TestUpstreamUsageBuffer_StartFlushLoop(t *testing.T) {
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
	buf := NewUpstreamUsageBuffer(billingClient, noopLogger(), 100, 50*time.Millisecond)

	buf.Add(&billing.UpstreamUsageRecord{
		UpstreamKeyID: "key-loop",
		Provider:      "openai",
		Model:         "gpt-4o",
		Date:          "2026-06-21",
		RequestCount:  1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	buf.StartFlushLoop(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	assert.GreaterOrEqual(t, atomic.LoadInt32(&requestCount), int32(1), "flush loop should have flushed at least once")
}

func TestUpstreamUsageBuffer_NilBilling_SkipsSend(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("FALLBACK_LOG_DIR", tmpDir)
	defer os.Unsetenv("FALLBACK_LOG_DIR")

	buf := NewUpstreamUsageBuffer(nil, noopLogger(), 10, time.Hour)
	buf.Add(&billing.UpstreamUsageRecord{
		UpstreamKeyID: "key-1",
		Model:         "gpt-4o",
		Date:          "2026-06-21",
		RequestCount:  1,
	})

	// Should not panic — billing is nil
	buf.Flush()
}
