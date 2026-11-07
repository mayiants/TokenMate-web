package keypool

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/mayiants/ai-proxy-go/pkg/relay"
	"go.uber.org/zap"
)

// ErrQueueFull is returned when a provider queue is at capacity and sync
// fallback is disabled.
var ErrQueueFull = errors.New("provider queue is full")

// ErrResponseTooLarge is returned when an upstream response exceeds the
// configured maximum body size.
var ErrResponseTooLarge = errors.New("upstream response body exceeds maximum allowed size")

// maxUpstreamResponseBody is the default upper bound for upstream response
// bodies read by the provider queue. It protects the proxy from OOM when a
// provider returns an unexpectedly large payload.
const maxUpstreamResponseBody = 50 << 20 // 50 MiB

// ProviderJob carries everything needed for a worker to execute the upstream
// HTTP call. Only non-streaming requests should be queued.
//
// job.Ctx must be the same context passed to Dispatch — the worker uses it
// for the upstream request and checks cancellation before executing.
type ProviderJob struct {
	Ctx          context.Context
	Adaptor      relay.Adaptor
	UpstreamBody []byte
	Provider     string
	Model        string
	KeyID        string
	UserID       string
	KeyHash      string
	IsStream     bool

	resultCh chan ProviderResult
}

// ProviderResult is the raw outcome of the upstream HTTP call.
type ProviderResult struct {
	StatusCode int
	Body       []byte
	Err        error
}

// ProviderQueue is a bounded queue with a fixed worker pool for one provider.
type ProviderQueue struct {
	provider     string
	jobs         chan ProviderJob
	workers      int
	size         int
	syncFallback bool
	stopOnce     sync.Once
	done         chan struct{}
	stopped      bool
	mu           sync.Mutex
	logger       *zap.Logger
}

// NewProviderQueue starts a bounded queue with `workers` goroutines.
func NewProviderQueue(provider string, workers, size int, syncFallback bool, logger *zap.Logger) *ProviderQueue {
	if workers <= 0 {
		workers = 4
	}
	// Normalize 0 and negative sizes to 64. A size of 0 would create an
	// unbuffered channel, causing Dispatch to almost always hit the default
	// path (queue full) even under light load.
	if size <= 0 {
		size = 64
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	q := &ProviderQueue{
		provider:     provider,
		jobs:         make(chan ProviderJob, size),
		workers:      workers,
		size:         size,
		syncFallback: syncFallback,
		done:         make(chan struct{}),
		logger:       logger,
	}

	for i := 0; i < workers; i++ {
		go q.worker()
	}
	return q
}

// Len returns the current number of jobs buffered in the queue.
func (q *ProviderQueue) Len() int {
	return len(q.jobs)
}

// Cap returns the capacity of the queue buffer.
func (q *ProviderQueue) Cap() int {
	return q.size
}

func (q *ProviderQueue) worker() {
	for {
		select {
		case <-q.done:
			return
		case job := <-q.jobs:
			if job.Ctx.Err() != nil {
				q.sendResult(job, ProviderResult{Err: job.Ctx.Err()})
				continue
			}

			resp, err := job.Adaptor.DoRequestBytesWithContext(job.Ctx, job.UpstreamBody, job.IsStream)
			if err != nil {
				q.sendResult(job, ProviderResult{Err: err})
				continue
			}

			q.sendResult(job, readProviderResult(resp))
		}
	}
}

// readProviderResult reads and closes the response body and returns a ProviderResult.
// The body is capped at maxUpstreamResponseBody to avoid unbounded memory
// allocation on unexpectedly large upstream responses.
func readProviderResult(resp *http.Response) ProviderResult {
	limited := io.LimitReader(resp.Body, maxUpstreamResponseBody+1)
	body, readErr := io.ReadAll(limited)
	_ = resp.Body.Close()

	res := ProviderResult{StatusCode: resp.StatusCode, Body: body}
	if int64(len(body)) > maxUpstreamResponseBody {
		res.Err = ErrResponseTooLarge
		res.Body = nil
		return res
	}
	if readErr != nil {
		res.Err = readErr
	}
	return res
}

func (q *ProviderQueue) sendResult(job ProviderJob, res ProviderResult) {
	if job.resultCh == nil {
		return
	}
	select {
	case job.resultCh <- res:
	default:
		q.logger.Warn("provider result channel blocked or nil receiver; dropping result",
			zap.String("provider", q.provider),
			zap.Int("status_code", res.StatusCode),
		)
	}
}

// Dispatch sends the job to a worker if queue space is available. When the
// queue is full, it falls back to synchronous dispatch if syncFallback is true.
func (q *ProviderQueue) Dispatch(ctx context.Context, job ProviderJob) (ProviderResult, error) {
	if q.isStopped() {
		return q.dispatchSync(ctx, job)
	}

	job.resultCh = make(chan ProviderResult, 1)

	select {
	case q.jobs <- job:
		select {
		case res := <-job.resultCh:
			return res, nil
		case <-ctx.Done():
			return ProviderResult{}, ctx.Err()
		case <-q.done:
			// Queue was stopped while waiting for a worker. Fall back to a
			// synchronous dispatch so the caller still gets a result.
			return q.dispatchSync(ctx, job)
		}
	default:
		if q.syncFallback {
			return q.dispatchSync(ctx, job)
		}
		return ProviderResult{}, ErrQueueFull
	}
}

func (q *ProviderQueue) dispatchSync(ctx context.Context, job ProviderJob) (ProviderResult, error) {
	if err := ctx.Err(); err != nil {
		return ProviderResult{}, err
	}

	resp, err := job.Adaptor.DoRequestBytesWithContext(ctx, job.UpstreamBody, job.IsStream)
	if err != nil {
		return ProviderResult{Err: err}, nil
	}

	return readProviderResult(resp), nil
}

// Stop signals the workers to exit. Workers observe the closed done channel
// and return without panicking; the jobs channel is intentionally left open so
// that a concurrent Dispatch can never send on a closed channel (TOCTOU fix).
func (q *ProviderQueue) Stop() {
	q.stopOnce.Do(func() {
		q.mu.Lock()
		q.stopped = true
		close(q.done)
		q.mu.Unlock()
	})
}

func (q *ProviderQueue) isStopped() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.stopped
}
