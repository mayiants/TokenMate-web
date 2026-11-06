# Plan: Provider-level queue + worker pool (ai-proxy-go)

> **Status:** COMPLETE (implemented and configurable via `queue.*` config keys)

## Scope
Add an opt-in, per-provider bounded queue + worker pool for **non-streaming** upstream dispatch. Streaming remains in the handler goroutine because it requires holding the client response writer open and flushing chunks incrementally.

## Goal
Reduce per-request goroutine memory/CPU on the server semaphore path by moving the actual upstream HTTP call for chat completions (and the Anthropic→OpenAI non-stream path) to a dedicated worker pool per provider, while keeping pre-consume, settle, metrics, retention, and client response handling in the handler goroutine.

## Design

### 1. Configuration
Add to `internal/config/config.go` under a new `QueueConfig` (or extend `RoutingConfig`):

- `provider_queue_enabled` (bool, default `false`)
- `provider_queue_workers_per_provider` (int, default `4`)
- `provider_queue_size_per_provider` (int, default `64`)
- `provider_queue_sync_fallback` (bool, default `true`) — dispatch synchronously when the queue is full to avoid rejecting requests.

### 2. Core data structures (`pkg/keypool/queue.go`, new file)

```go
type providerJob struct {
    ctx            context.Context
    adaptor        relay.Adaptor
    upstreamBody   []byte
    provider       string
    model          string
    keyID          string
    userID         string
    keyHash        string
    isStream       bool // must be false for queued path
    resultCh       chan providerResult
}

type providerResult struct {
    statusCode int
    body       []byte
    err        error // transport error
}

type ProviderQueue struct {
    jobs     chan providerJob
    workers  int
    mu       sync.Mutex
    stopped  bool
}
```

Add to `Manager`:

```go
queuesMu     sync.RWMutex
queues       map[string]*ProviderQueue
queueWorkers int
queueSize    int
queueEnabled bool
```

### 3. Worker behavior
Each provider queue has `N` long-running goroutines. A worker:

1. Receives a `providerJob` from the channel.
2. Checks `job.ctx.Done()` before and after dispatch; if cancelled, returns an error result.
3. Calls a new context-aware dispatch method `adaptor.DoRequestBytesWithContext(job.ctx, job.upstreamBody, false)`. If the adaptor does not yet support contexts, we add it to `pkg/relay/base/adaptor.go` and override it in `pkg/relay/channel/aws/aws.go` and `pkg/relay/channel/gemini/gemini.go`.
4. Reads the response body.
5. Sends a `providerResult` back on `job.resultCh`.

The worker does **not** parse usage, update key-pool state, or settle. It only performs the upstream I/O.

### 4. Handler integration
In `internal/server/handlers_chat.go` and `internal/server/handlers_responses.go`:

- After pre-consume and building the adaptor/upstream body, if `isStream` is false and `keyPoolManager.QueueEnabled()` is true:
  - Build a `providerJob` with a buffered `resultCh` (size 1).
  - Call `keyPoolManager.Dispatch(ctx, provider, job)`.
    - If the queue is full and `sync_fallback` is enabled, fall back to inline `adaptor.DoRequestBytes`.
    - If the queue is full and fallback is disabled, return `503 Service Unavailable`.
  - Wait on `resultCh` with `select { case <-ctx.Done(): ... case res := <-resultCh: ... }`.
- On `providerResult`:
  - If `res.err != nil`, follow the existing transport-error path.
  - If `res.statusCode >= 400`, follow the existing upstream-error path.
  - Otherwise continue with parse/transform/usage/settle exactly as today.

Streaming handlers are **not changed**; they keep inline dispatch.

### 5. Context propagation
- Add `DoRequestBytesWithContext(ctx context.Context, body []byte, stream bool) (*http.Response, error)` to the `relay.Adaptor` interface.
- Base implementation uses `http.NewRequestWithContext(ctx, ...)` with `base.SharedHTTPClient()`.
- AWS and Gemini overrides updated to respect the passed context.
- This lets cancelled/disconnected clients abort the upstream request instead of consuming quota.

### 6. Lifecycle side effects stay in the handler
The handler keeps responsibility for:
- Pre-consume / local balance cache deduction.
- `keyPoolManager.RecordUpstreamRequest`, `RecordModelSuccess`, `ReportKeyFailure`, `ReportModelFailure`.
- `recordUpstreamUsageAsync`, `recordProviderHealthAsync`, `recordUsageAsyncFromContext`.
- `providerBreakers.RecordSuccess/RecordFailure`.
- Affinity cache updates.
- Budget controller recording.
- Retention capture.
- Settle (local or portal).
- Writing the client response.
- Setting context values consumed by `MetricsMiddleware` (`provider`, `model`, `tokens_consumed`, etc.).

### 7. Shutdown
- `Manager.Stop()` (new or existing) signals all queue workers to drain and stop.
- Each worker exits when the jobs channel is closed.

### 8. Tests
Add `pkg/keypool/queue_test.go`:
- `TestProviderQueue_DispatchesJob`: basic queued dispatch with a mock adaptor.
- `TestProviderQueue_RespectsWorkerLimit`: slow upstream + many jobs shows concurrency capped at `workers`.
- `TestProviderQueue_ContextCancellation`: cancelled context returns error without hitting upstream.
- `TestProviderQueue_FallbackWhenFull`: queue-full path falls back to synchronous dispatch.

Add/adapt handler-level tests in `internal/server/handlers_test.go` if a test helper for queued dispatch exists.

## Compatibility assessment

| Concern | How the plan addresses it |
|---|---|
| Pre-consume/settle ordering | Handler pre-consumes before enqueue, worker only does I/O, handler settles after result. |
| Local balance cache races | Same as today; each request serially pre-consumes/settles in its handler. |
| Client timeout/cancellation | Context propagated into upstream request; worker returns on cancellation. |
| Streaming | Explicitly excluded from queue. |
| Retries/failover | Not changed; worker dispatches the already-selected provider/key once. |
| Metrics middleware | Handler still sets all context flags before returning. |
| Retention | Handler receives raw body from worker and captures retention as before. |
| Server semaphore | Queue workers are separate goroutines; queue size + workers cap upstream concurrency per provider. |
| Adaptor interface change | Add `DoRequestBytesWithContext`; default/canonical adaptors updated, custom AWS/Gemini overrides updated. |

## Recommended default
Keep `provider_queue_enabled=false` by default. This is a behavior-changing optimization; operators can enable it after load testing.
