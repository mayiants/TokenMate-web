package server

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"github.com/mayiants/ai-proxy-go/pkg/fallbacklog"
	"go.uber.org/zap"
)

const (
	maxRetries     = 3
	baseBackoff    = 500 * time.Millisecond
	maxBackoff     = 10 * time.Second
	flushChanSize  = 10
	replayInterval = 2 * time.Minute
)

// UsageBuffer batches usage records and sends them to Portal periodically.
type UsageBuffer struct {
	records       []*billing.UsageRecord
	mu            sync.Mutex
	maxBatch      int
	flushInterval time.Duration
	billing       *billing.Billing
	logger        *zap.Logger
	failedCount   atomic.Int64
	fallback      *fallbacklog.Writer

	flushCh chan []*billing.UsageRecord
	wg      sync.WaitGroup
	stopCh  chan struct{}
}

func NewUsageBuffer(b *billing.Billing, logger *zap.Logger, maxBatch int, flushInterval time.Duration) *UsageBuffer {
	if maxBatch <= 0 {
		maxBatch = 100
	}
	if flushInterval <= 0 {
		flushInterval = 3 * time.Second
	}
	fbDir := os.Getenv("FALLBACK_LOG_DIR")
	if fbDir == "" {
		fbDir = "/var/log/ai-proxy"
	}
	fb := fallbacklog.NewWriter(fbDir, 7*24*time.Hour)
	return &UsageBuffer{
		records:       make([]*billing.UsageRecord, 0, maxBatch),
		maxBatch:      maxBatch,
		flushInterval: flushInterval,
		billing:       b,
		logger:        logger,
		fallback:      fb,
		flushCh:       make(chan []*billing.UsageRecord, flushChanSize),
		stopCh:        make(chan struct{}),
	}
}

func (b *UsageBuffer) Add(record *billing.UsageRecord) {
	b.mu.Lock()
	b.records = append(b.records, record)
	if len(b.records) >= b.maxBatch {
		batch := b.records
		b.records = make([]*billing.UsageRecord, 0, b.maxBatch)
		b.mu.Unlock()
		// Send to async channel instead of blocking the request goroutine
		select {
		case b.flushCh <- batch:
		default:
			// Channel full — fall back to synchronous send to avoid unbounded blocking
			b.sendBatch(batch)
		}
		return
	}
	b.mu.Unlock()
}

func (b *UsageBuffer) StartFlushLoop(ctx context.Context) {
	b.wg.Add(3)
	go b.flushWorker(ctx)
	go b.batchSender()
	go b.replayLoop(ctx)
	if b.logger != nil {
		b.logger.Info("Usage buffer flush loop started",
			zap.Int("max_batch", b.maxBatch),
			zap.Duration("flush_interval", b.flushInterval),
		)
	}
}

// replayLoop periodically re-sends records that were persisted to the fallback
// NDJSON log during a Portal-unreachable window, closing the "write-only
// fallback = permanently lost billing" gap. Portal dedupes by trace_id, so a
// record that actually landed before the proxy gave up is harmless to resend.
func (b *UsageBuffer) replayLoop(ctx context.Context) {
	defer b.wg.Done()
	if b.fallback == nil || b.billing == nil {
		return
	}
	ticker := time.NewTicker(replayInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.replayFallback()
		case <-ctx.Done():
			return
		case <-b.stopCh:
			return
		}
	}
}

func (b *UsageBuffer) replayFallback() {
	replayed, failed := b.fallback.Replay(func(line []byte) error {
		var rec billing.UsageRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// A corrupt line can never succeed; skip it (return nil) so it does
			// not wedge the whole file forever.
			if b.logger != nil {
				b.logger.Warn("Skipping unparseable fallback record", zap.Error(err))
			}
			return nil
		}
		return b.billing.SendUsageToPortal(context.Background(), &rec)
	})
	if b.logger != nil && (replayed > 0 || failed > 0) {
		b.logger.Info("Replayed fallback usage records",
			zap.Int("replayed", replayed), zap.Int("failed", failed))
	}
}

func (b *UsageBuffer) flushWorker(ctx context.Context) {
	defer b.wg.Done()
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.Flush()
		case <-ctx.Done():
			b.Flush()
			return
		case <-b.stopCh:
			b.Flush()
			return
		}
	}
}

func (b *UsageBuffer) batchSender() {
	defer b.wg.Done()
	for batch := range b.flushCh {
		b.sendBatch(batch)
	}
}

func (b *UsageBuffer) Flush() {
	b.mu.Lock()
	if len(b.records) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.records
	b.records = make([]*billing.UsageRecord, 0, b.maxBatch)
	b.mu.Unlock()

	b.sendBatch(batch)
}

func (b *UsageBuffer) sendBatch(batch []*billing.UsageRecord) {
	if len(batch) == 0 {
		return
	}

	if b.billing == nil {
		b.writeFallback(batch)
		return
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Min(float64(baseBackoff)*math.Pow(2, float64(attempt-1)), float64(maxBackoff)))
			jitter := time.Duration(rand.Int63n(int64(backoff / 4)))
			time.Sleep(backoff + jitter)
		}

		var err error
		if len(batch) > 1 {
			err = b.billing.SendUsageStatsBatchToPortal(context.Background(), batch)
		} else {
			err = b.billing.SendUsageToPortal(context.Background(), batch[0])
		}
		if err == nil {
			return
		}
		lastErr = err
		if b.logger != nil {
			b.logger.Warn("Usage send failed, retrying",
				zap.Int("attempt", attempt+1),
				zap.Int("max_retries", maxRetries),
				zap.Error(err),
			)
		}
	}

	fc := b.failedCount.Add(1)
	if b.logger != nil && fc%100 == 0 {
		b.logger.Warn("Usage send failures accumulating",
			zap.Int64("failed_count", fc),
			zap.Error(lastErr),
		)
	}
	b.writeFallback(batch)
}

func (b *UsageBuffer) writeFallback(batch []*billing.UsageRecord) {
	if b.fallback == nil {
		if b.logger != nil {
			b.logger.Warn("Usage records dropped: fallback writer nil",
				zap.Int("count", len(batch)))
		}
		return
	}
	for _, record := range batch {
		if werr := b.fallback.WriteRecord(record); werr != nil && b.logger != nil {
			b.logger.Warn("Failed to write usage fallback log", zap.Error(werr))
		}
	}
}

func (b *UsageBuffer) StopAndWait(timeout time.Duration) {
	close(b.stopCh)
	close(b.flushCh)
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		if b.logger != nil {
			b.logger.Warn("Usage buffer shutdown timed out",
				zap.Duration("timeout", timeout),
			)
		}
	}
}
