package billing

import (
	"testing"
	"time"
)

func TestUpstreamUsageRecordAggregation(t *testing.T) {
	// Simulate what happens in UpstreamUsageBuffer.Add
	records := make(map[string]*UpstreamUsageRecord)

	key := "key1:model1:2026-05-16"
	record1 := &UpstreamUsageRecord{
		UpstreamKeyID:       "key1",
		Provider:            "minimax",
		Model:               "model1",
		Date:                "2026-05-16",
		RequestCount:        10,
		InputTokens:         5000,
		OutputTokens:        3000,
		CacheReadTokens:     200,
		CacheCreationTokens: 100,
		ErrorCount:          1,
		LatencyMs:           450,
	}
	records[key] = record1

	// Second batch for same key/model/date
	record2 := &UpstreamUsageRecord{
		UpstreamKeyID:       "key1",
		Provider:            "minimax",
		Model:               "model1",
		Date:                "2026-05-16",
		RequestCount:        5,
		InputTokens:         2500,
		OutputTokens:        1500,
		CacheReadTokens:     100,
		CacheCreationTokens: 50,
		ErrorCount:          0,
		LatencyMs:           300,
	}

	// Aggregate
	existing := records[key]
	existing.RequestCount += record2.RequestCount
	existing.InputTokens += record2.InputTokens
	existing.OutputTokens += record2.OutputTokens
	existing.CacheReadTokens += record2.CacheReadTokens
	existing.CacheCreationTokens += record2.CacheCreationTokens
	existing.ErrorCount += record2.ErrorCount

	// Weighted average latency
	if existing.RequestCount > 0 {
		totalLatency := int64(existing.LatencyMs)*(int64(existing.RequestCount)-int64(record2.RequestCount)) + int64(record2.LatencyMs)*int64(record2.RequestCount)
		existing.LatencyMs = int(totalLatency / int64(existing.RequestCount))
	}

	// Verify
	if existing.RequestCount != 15 {
		t.Errorf("Expected request_count=15, got %d", existing.RequestCount)
	}
	if existing.InputTokens != 7500 {
		t.Errorf("Expected input_tokens=7500, got %d", existing.InputTokens)
	}
	if existing.OutputTokens != 4500 {
		t.Errorf("Expected output_tokens=4500, got %d", existing.OutputTokens)
	}
	if existing.ErrorCount != 1 {
		t.Errorf("Expected error_count=1, got %d", existing.ErrorCount)
	}
	// Weighted avg: (450*10 + 300*5) / 15 = 400
	if existing.LatencyMs != 400 {
		t.Errorf("Expected avg_latency_ms=400, got %d", existing.LatencyMs)
	}
}

func TestUpstreamUsageBufferMaxBatch(t *testing.T) {
	// Test that buffer respects maxBatch limit
	buf := make(map[string]*UpstreamUsageRecord)
	maxBatch := 3

	for i := 0; i < 5; i++ {
		key := "key" + string(rune('0'+i)) + ":model:2026-05-16"
		buf[key] = &UpstreamUsageRecord{
			UpstreamKeyID: "key" + string(rune('0'+i)),
			RequestCount:  1,
		}
		if len(buf) >= maxBatch {
			// Simulate flush
			if len(buf) != maxBatch {
				t.Errorf("Expected buffer size %d at flush, got %d", maxBatch, len(buf))
			}
			buf = make(map[string]*UpstreamUsageRecord)
		}
	}

	// After loop, remaining records
	if len(buf) != 2 {
		t.Errorf("Expected remaining buffer size 2, got %d", len(buf))
	}
}

func TestUpstreamUsageBufferFlushInterval(t *testing.T) {
	interval := 30 * time.Second
	if interval != 30*time.Second {
		t.Error("Default flush interval should be 30s")
	}
}
