package server

import (
	"context"
	"sync"
	"time"

	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"go.uber.org/zap"
)

// ProviderHealthBuffer batches health records and flushes to Portal.
// Aggregation is done on the Portal side; Proxy just batches and sends.
type ProviderHealthBuffer struct {
	records       []*billing.ProviderHealthRecord
	mu            sync.Mutex
	maxBatch      int
	flushInterval time.Duration
	billing       *billing.Billing
	logger        *zap.Logger
}

// NewProviderHealthBuffer creates a new provider health buffer.
func NewProviderHealthBuffer(b *billing.Billing, logger *zap.Logger, maxBatch int, flushInterval time.Duration) *ProviderHealthBuffer {
	if maxBatch <= 0 {
		maxBatch = 500
	}
	if flushInterval <= 0 {
		flushInterval = 30 * time.Second
	}
	return &ProviderHealthBuffer{
		records:       make([]*billing.ProviderHealthRecord, 0, maxBatch),
		maxBatch:      maxBatch,
		flushInterval: flushInterval,
		billing:       b,
		logger:        logger,
	}
}

// Add appends a health record. Triggers flush if maxBatch reached.
func (b *ProviderHealthBuffer) Add(record *billing.ProviderHealthRecord) {
	if b == nil || record == nil {
		return
	}
	b.mu.Lock()
	b.records = append(b.records, record)
	shouldFlush := len(b.records) >= b.maxBatch
	var batch []*billing.ProviderHealthRecord
	if shouldFlush {
		batch = b.drainLocked()
	}
	b.mu.Unlock()

	if shouldFlush && len(batch) > 0 {
		b.sendBatch(batch)
	}
}

// Flush sends all pending records immediately.
func (b *ProviderHealthBuffer) Flush() {
	b.mu.Lock()
	batch := b.drainLocked()
	b.mu.Unlock()
	if len(batch) > 0 {
		b.sendBatch(batch)
	}
}

// StartFlushLoop starts a background ticker that flushes periodically.
func (b *ProviderHealthBuffer) StartFlushLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(b.flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				b.Flush()
				return
			case <-ticker.C:
				b.Flush()
			}
		}
	}()
	if b.logger != nil {
		b.logger.Info("Provider health buffer flush loop started",
			zap.Int("max_batch", b.maxBatch),
			zap.Duration("flush_interval", b.flushInterval),
		)
	}
}

func (b *ProviderHealthBuffer) drainLocked() []*billing.ProviderHealthRecord {
	if len(b.records) == 0 {
		return nil
	}
	batch := b.records
	b.records = make([]*billing.ProviderHealthRecord, 0, b.maxBatch)
	return batch
}

func (b *ProviderHealthBuffer) sendBatch(batch []*billing.ProviderHealthRecord) {
	if b.billing == nil {
		b.logger.Warn("Provider health billing is nil, skipping batch")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := b.billing.SendProviderHealthBatchToPortal(ctx, batch); err != nil {
		b.logger.Warn("Failed to send provider health batch to Portal",
			zap.Int("count", len(batch)),
			zap.Error(err),
		)
	} else {
		b.logger.Info("Sent provider health batch to Portal",
			zap.Int("count", len(batch)),
		)
	}
}
