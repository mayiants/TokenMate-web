package server

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/ai-proxy/ai-proxy-go/pkg/billing"
	"go.uber.org/zap"
)

type settleTask struct {
	ctx context.Context
	req billing.SettleRequest
}

// SettleBuffer provides reliable non-fire-and-forget settlement with retry.
// Handlers call AddSettle instead of "go Billing.Settle(...)".
type SettleBuffer struct {
	billing *billing.Billing
	logger  *zap.Logger
	ch      chan settleTask
	wg      sync.WaitGroup
	stopCh  chan struct{}
}

func NewSettleBuffer(b *billing.Billing, logger *zap.Logger, chanSize int) *SettleBuffer {
	if chanSize <= 0 {
		chanSize = 100
	}
	return &SettleBuffer{
		billing: b,
		logger:  logger,
		ch:      make(chan settleTask, chanSize),
		stopCh:  make(chan struct{}),
	}
}

func (s *SettleBuffer) Start(ctx context.Context) {
	s.wg.Add(1)
	go s.worker(ctx)
	if s.logger != nil {
		s.logger.Info("Settle buffer worker started")
	}
}

func (s *SettleBuffer) AddSettle(ctx context.Context, req billing.SettleRequest) {
	if req.PreConsumed == 0 {
		return
	}
	select {
	case s.ch <- settleTask{ctx: ctx, req: req}:
	default:
		// Channel full — fire-and-forget as last resort (same as current behavior)
		if s.logger != nil {
			s.logger.Warn("Settle buffer full, falling back to fire-and-forget",
				zap.String("request_id", req.RequestID),
			)
		}
		go s.billing.Settle(ctx, req)
	}
}

func (s *SettleBuffer) worker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case task := <-s.ch:
			s.processWithRetry(task.ctx, task.req)
		case <-ctx.Done():
			s.drain()
			return
		case <-s.stopCh:
			s.drain()
			return
		}
	}
}

func (s *SettleBuffer) processWithRetry(ctx context.Context, req billing.SettleRequest) {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Min(float64(500*time.Millisecond)*math.Pow(2, float64(attempt-1)), float64(10*time.Second)))
			jitter := time.Duration(rand.Int63n(int64(backoff / 4)))
			time.Sleep(backoff + jitter)
		}

		resp := s.billing.Settle(ctx, req)
		if resp.Success {
			return
		}
		lastErr = fmt.Errorf("settle failed (attempt %d/%d)", attempt+1, maxRetries+1)
		if s.logger != nil {
			s.logger.Warn("Settle failed, retrying",
				zap.Int("attempt", attempt+1),
				zap.String("request_id", req.RequestID),
			)
		}
	}
	if lastErr != nil && s.logger != nil {
		s.logger.Warn("Settle failed after all retries",
			zap.String("request_id", req.RequestID),
			zap.Error(lastErr),
		)
	}
}

func (s *SettleBuffer) drain() {
	for {
		select {
		case task := <-s.ch:
			s.billing.Settle(task.ctx, task.req)
		default:
			return
		}
	}
}

func (s *SettleBuffer) StopAndWait(timeout time.Duration) {
	close(s.stopCh)
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		if s.logger != nil {
			s.logger.Warn("Settle buffer shutdown timed out",
				zap.Duration("timeout", timeout),
			)
		}
	}
}
