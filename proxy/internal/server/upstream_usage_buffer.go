package server

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"github.com/mayiants/ai-proxy-go/pkg/fallbacklog"
	"go.uber.org/zap"
)

// UpstreamUsageBuffer batches upstream usage records and sends them to Portal periodically.
// Aggregates by (upstreamKeyId, provider, model, date) within the buffer window.
type UpstreamUsageBuffer struct {
	records       map[string]*billing.UpstreamUsageRecord // key = "keyId:model:date"
	mu            sync.Mutex
	maxBatch      int
	flushInterval time.Duration
	billing       *billing.Billing
	logger        *zap.Logger
	failedCount   atomic.Int64
	fallback      *fallbacklog.Writer
}

// NewUpstreamUsageBuffer creates a new upstream usage buffer.
func NewUpstreamUsageBuffer(b *billing.Billing, logger *zap.Logger, maxBatch int, flushInterval time.Duration) *UpstreamUsageBuffer {
	if maxBatch <= 0 {
		maxBatch = 500
	}
	if flushInterval <= 0 {
		flushInterval = 30 * time.Second
	}
	fbDir := os.Getenv("FALLBACK_LOG_DIR")
	if fbDir == "" {
		fbDir = "/var/log/ai-proxy"
	}
	fb := fallbacklog.NewWriter(fbDir, 7*24*time.Hour)
	return &UpstreamUsageBuffer{
		records:       make(map[string]*billing.UpstreamUsageRecord),
		maxBatch:      maxBatch,
		flushInterval: flushInterval,
		billing:       b,
		logger:        logger,
		fallback:      fb,
	}
}

// Add accumulates upstream usage for a single request.
// It aggregates into the existing record for the same (key, model, date).
func (b *UpstreamUsageBuffer) Add(record *billing.UpstreamUsageRecord) {
	if b == nil || record == nil {
		return
	}
	key := fmt.Sprintf("%s:%s:%s", record.UpstreamKeyID, record.Model, record.Date)

	b.mu.Lock()
	existing, ok := b.records[key]
	if !ok {
		b.records[key] = record
		if len(b.records) >= b.maxBatch {
			batch := b.drainLocked()
			b.mu.Unlock()
			b.sendBatch(batch)
			return
		}
		b.mu.Unlock()
		return
	}

	// Aggregate into existing record
	existing.RequestCount += record.RequestCount
	existing.InputTokens += record.InputTokens
	existing.OutputTokens += record.OutputTokens
	existing.CacheReadTokens += record.CacheReadTokens
	existing.CacheCreationTokens += record.CacheCreationTokens
	existing.ErrorCount += record.ErrorCount
	// Weighted average latency approximation
	if existing.RequestCount > 0 {
		totalLatency := int64(existing.LatencyMs)*(int64(existing.RequestCount)-int64(record.RequestCount)) + int64(record.LatencyMs)*int64(record.RequestCount)
		existing.LatencyMs = int(totalLatency / int64(existing.RequestCount))
	}
	b.mu.Unlock()
}

// drainLocked extracts all records and resets the map. Caller must hold lock.
func (b *UpstreamUsageBuffer) drainLocked() []*billing.UpstreamUsageRecord {
	batch := make([]*billing.UpstreamUsageRecord, 0, len(b.records))
	for _, v := range b.records {
		batch = append(batch, v)
	}
	b.records = make(map[string]*billing.UpstreamUsageRecord)
	return batch
}

// StartFlushLoop starts the periodic flush goroutine.
func (b *UpstreamUsageBuffer) StartFlushLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(b.flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				b.Flush()
			case <-ctx.Done():
				b.Flush() // Final flush on shutdown
				return
			}
		}
	}()
	if b.logger != nil {
		b.logger.Info("Upstream usage buffer flush loop started",
			zap.Int("max_batch", b.maxBatch),
			zap.Duration("flush_interval", b.flushInterval),
		)
	}
}

// Flush sends all buffered records to Portal.
func (b *UpstreamUsageBuffer) Flush() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if len(b.records) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.drainLocked()
	b.mu.Unlock()

	b.sendBatch(batch)
}

func (b *UpstreamUsageBuffer) sendBatch(batch []*billing.UpstreamUsageRecord) {
	if len(batch) == 0 || b.billing == nil {
		return
	}

	if err := b.billing.SendUpstreamUsageBatchToPortal(context.Background(), batch); err != nil {
		b.failedCount.Add(1)
		if b.logger != nil {
			b.logger.Warn("Failed to send upstream usage batch to Portal",
				zap.Error(err),
				zap.Int("batch_size", len(batch)),
			)
		}
		// Write to local fallback log for manual reconciliation
		if b.fallback != nil {
			for _, rec := range batch {
				if werr := b.fallback.WriteRecord(rec); werr != nil && b.logger != nil {
					b.logger.Warn("Failed to write upstream fallback log", zap.Error(werr))
				}
			}
		}
		return
	}

	if b.logger != nil {
		b.logger.Debug("Upstream usage batch sent successfully",
			zap.Int("batch_size", len(batch)),
		)
	}
}
