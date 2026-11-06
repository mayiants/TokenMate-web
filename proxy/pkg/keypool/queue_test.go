package keypool

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"github.com/ai-proxy/ai-proxy-go/pkg/relay"
	"github.com/ai-proxy/ai-proxy-go/types"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// mockQueueAdaptor is a minimal relay.Adaptor used to exercise the queue.
type mockQueueAdaptor struct {
	delay      time.Duration
	statusCode int
	body       []byte
	err        error
	block      chan struct{}
	blockedCh  chan struct{} // notified when the adaptor enters the block select

	calls       atomic.Int64
	maxInFlight atomic.Int64
	inFlight    atomic.Int64
}

func (m *mockQueueAdaptor) response() *http.Response {
	return &http.Response{
		StatusCode: m.statusCode,
		Body:       io.NopCloser(bytes.NewReader(m.body)),
	}
}

func (m *mockQueueAdaptor) DoRequestBytesWithContext(ctx context.Context, _ []byte, _ bool) (*http.Response, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if m.block != nil {
		// Signal that we are about to wait on the block channel so tests can
		// synchronize without relying on time.Sleep.
		if m.blockedCh != nil {
			select {
			case m.blockedCh <- struct{}{}:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		select {
		case <-m.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	cur := m.inFlight.Add(1)
	for {
		max := m.maxInFlight.Load()
		if cur <= max || m.maxInFlight.CompareAndSwap(max, cur) {
			break
		}
	}
	defer m.inFlight.Add(-1)

	m.calls.Add(1)
	if m.err != nil {
		return nil, m.err
	}
	return m.response(), nil
}

func (m *mockQueueAdaptor) DoRequestBytes(_ []byte, _ bool) (*http.Response, error) {
	return m.DoRequestBytesWithContext(context.Background(), nil, false)
}

func (m *mockQueueAdaptor) DoRequest(_ interface{}, _ bool) (*http.Response, error) {
	return m.DoRequestBytesWithContext(context.Background(), nil, false)
}

func (m *mockQueueAdaptor) GetHeaders() map[string]string         { return nil }
func (m *mockQueueAdaptor) GetEndpoint(_ string, _ bool) string   { return "" }
func (m *mockQueueAdaptor) GetRequestURL(_ string, _ bool) string { return "" }
func (m *mockQueueAdaptor) ReadResponse(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
func (m *mockQueueAdaptor) GetRelayFormat() types.RelayFormat { return types.RelayFormatOpenAI }
func (m *mockQueueAdaptor) GetProviderName() string           { return "mock" }
func (m *mockQueueAdaptor) ToProvider(_ dto.GeneralOpenAIRequest) interface{} {
	return nil
}
func (m *mockQueueAdaptor) FromProvider(_ interface{}, _ dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	return nil
}
func (m *mockQueueAdaptor) TransformStreamChunk(_ interface{}, _ int) interface{} { return nil }
func (m *mockQueueAdaptor) ParseResponse(_ []byte) (interface{}, error)           { return nil, nil }
func (m *mockQueueAdaptor) ExtractUsage(_ interface{}) *dto.Usage                 { return nil }

var _ relay.Adaptor = (*mockQueueAdaptor)(nil)

func TestProviderQueue_DispatchesJob(t *testing.T) {
	adaptor := &mockQueueAdaptor{statusCode: 200, body: []byte("ok")}
	q := NewProviderQueue("openai", 1, 2, true, nil)
	defer q.Stop()

	res, err := q.Dispatch(context.Background(), ProviderJob{
		Ctx:          context.Background(),
		Adaptor:      adaptor,
		UpstreamBody: []byte(`{}`),
		Provider:     "openai",
	})
	if err != nil {
		t.Fatalf("unexpected dispatch error: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("unexpected result error: %v", res.Err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", res.StatusCode)
	}
	if string(res.Body) != "ok" {
		t.Fatalf("expected body ok, got %s", string(res.Body))
	}
	if adaptor.calls.Load() != 1 {
		t.Fatalf("expected 1 adaptor call, got %d", adaptor.calls.Load())
	}
}

func TestProviderQueue_RespectsWorkerLimit(t *testing.T) {
	adaptor := &mockQueueAdaptor{statusCode: 200, body: []byte("ok"), delay: 100 * time.Millisecond}
	q := NewProviderQueue("openai", 2, 10, true, nil)
	defer q.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = q.Dispatch(context.Background(), ProviderJob{
				Ctx:          context.Background(),
				Adaptor:      adaptor,
				UpstreamBody: []byte(`{}`),
				Provider:     "openai",
			})
		}()
	}
	wg.Wait()

	if max := adaptor.maxInFlight.Load(); max > 2 {
		t.Fatalf("expected max in-flight <= 2, got %d", max)
	}
}

func TestProviderQueue_ContextCancellation(t *testing.T) {
	adaptor := &mockQueueAdaptor{statusCode: 200, body: []byte("ok"), delay: time.Second}
	q := NewProviderQueue("openai", 1, 2, true, nil)
	defer q.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	res, err := q.Dispatch(ctx, ProviderJob{
		Ctx:          ctx,
		Adaptor:      adaptor,
		UpstreamBody: []byte(`{}`),
		Provider:     "openai",
	})
	if err == nil {
		t.Fatalf("expected context cancellation error, got nil")
	}
	if res.Err != nil && res.Err != context.DeadlineExceeded {
		t.Fatalf("expected deadline exceeded, got %v", res.Err)
	}
}

func TestProviderQueue_RejectedWhenFull(t *testing.T) {
	// One worker and one buffer slot. Warm up the worker, then occupy the
	// worker with a blocking job, fill the buffer with a second job, and
	// verify that a third synchronous dispatch is rejected immediately.
	blockCh := make(chan struct{})
	blockedCh := make(chan struct{}, 1)
	blockingAdaptor := &mockQueueAdaptor{
		statusCode: 200,
		body:       []byte("ok"),
		block:      blockCh,
		blockedCh:  blockedCh,
	}
	dummyAdaptor := &mockQueueAdaptor{statusCode: 200, body: []byte("warm")}
	q := NewProviderQueue("openai", 1, 1, false, nil)
	defer q.Stop()

	// Warm-up guarantees the worker goroutine is running and ready to receive.
	if _, err := q.Dispatch(context.Background(), ProviderJob{
		Ctx:          context.Background(),
		Adaptor:      dummyAdaptor,
		UpstreamBody: []byte(`{}`),
		Provider:     "openai",
	}); err != nil {
		t.Fatalf("warm-up dispatch failed: %v", err)
	}

	var wg sync.WaitGroup
	dispatchAsync := func(a relay.Adaptor) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = q.Dispatch(context.Background(), ProviderJob{
				Ctx:          context.Background(),
				Adaptor:      a,
				UpstreamBody: []byte(`{}`),
				Provider:     "openai",
			})
		}()
	}

	// First blocking job occupies the worker.
	dispatchAsync(blockingAdaptor)
	select {
	case <-blockedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for worker to block")
	}

	// Second blocking job fills the one buffer slot.
	dispatchAsync(blockingAdaptor)
	deadline := time.Now().Add(2 * time.Second)
	for q.Len() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for buffer to fill, len=%d", q.Len())
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Worker busy + buffer full => next job rejected immediately.
	_, err := q.Dispatch(context.Background(), ProviderJob{
		Ctx:          context.Background(),
		Adaptor:      blockingAdaptor,
		UpstreamBody: []byte(`{}`),
		Provider:     "openai",
	})
	if err != ErrQueueFull {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}

	close(blockCh)
	wg.Wait()
}

func TestProviderQueue_SyncFallbackWhenFull(t *testing.T) {
	adaptor := &mockQueueAdaptor{statusCode: 200, body: []byte("fallback")}
	q := NewProviderQueue("openai", 0, 0, true, nil)
	defer q.Stop()

	res, err := q.Dispatch(context.Background(), ProviderJob{
		Ctx:          context.Background(),
		Adaptor:      adaptor,
		UpstreamBody: []byte(`{}`),
		Provider:     "openai",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(res.Body) != "fallback" {
		t.Fatalf("expected fallback body, got %s", string(res.Body))
	}
}

func TestManager_QueueEnabledAndDispatch(t *testing.T) {
	cfg := &config.Config{}
	cfg.Queue.Enabled = true
	cfg.Queue.WorkersPerProvider = 1
	cfg.Queue.SizePerProvider = 2
	cfg.Auth.EncryptionKey = "test-encryption-key-32-bytes-long!!"
	m := NewManager(cfg, zap.NewNop())
	defer m.Close()

	if !m.QueueEnabled() {
		t.Fatal("expected queue enabled")
	}

	adaptor := &mockQueueAdaptor{statusCode: 200, body: []byte("manager")}
	res, err := m.Dispatch(context.Background(), "openai", ProviderJob{
		Ctx:          context.Background(),
		Adaptor:      adaptor,
		UpstreamBody: []byte(`{}`),
		Provider:     "openai",
	})
	if err != nil {
		t.Fatalf("unexpected dispatch error: %v", err)
	}
	if string(res.Body) != "manager" {
		t.Fatalf("expected manager body, got %s", string(res.Body))
	}
}

func TestReadProviderResult_ResponseTooLarge(t *testing.T) {
	oversized := make([]byte, maxUpstreamResponseBody+1)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(oversized)),
	}

	res := readProviderResult(resp)

	assert.ErrorIs(t, res.Err, ErrResponseTooLarge)
	assert.Nil(t, res.Body)
	assert.Equal(t, http.StatusOK, res.StatusCode)
}

func TestReadProviderResult_MaxAllowed(t *testing.T) {
	body := make([]byte, maxUpstreamResponseBody)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	res := readProviderResult(resp)

	assert.NoError(t, res.Err)
	assert.Len(t, res.Body, maxUpstreamResponseBody)
	assert.Equal(t, http.StatusOK, res.StatusCode)
}
