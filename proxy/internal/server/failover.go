package server

import (
	"strings"

	"github.com/ai-proxy/ai-proxy-go/internal/config"
)

// failoverState tracks per-request retry/failover progress. See
// docs/upstream-retry-failover-design-202610251430.md.
type failoverState struct {
	cfg            config.FailoverConfig
	model          string // logical model (for per-model disable check)
	attempt        int    // number of attempts recorded so far (0 before first)
	excluded       map[string]bool
	providerSwitch int // number of cross-provider switches performed
	lastProvider   string
}

func newFailoverState(cfg config.FailoverConfig, model string) *failoverState {
	return &failoverState{cfg: cfg, model: model, excluded: make(map[string]bool)}
}

// enabledForModel reports whether failover is active for this request's model.
func (f *failoverState) enabledForModel() bool {
	if f == nil || !f.cfg.Enabled {
		return false
	}
	for _, m := range f.cfg.DisabledModels {
		if strings.EqualFold(strings.TrimSpace(m), f.model) {
			return false
		}
	}
	return true
}

// maxAttempts returns the effective attempt cap (>=1).
func (f *failoverState) maxAttempts() int {
	if f.cfg.MaxAttempts < 1 {
		return 1
	}
	return f.cfg.MaxAttempts
}

// isRetryableStatus reports whether an HTTP status is in the retry list.
func (f *failoverState) isRetryableStatus(status int) bool {
	for _, s := range f.cfg.RetryStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// recordAttempt marks the (provider, keyID) just used as consumed and tracks
// cross-provider switches. Call after each attempt, before deciding to retry.
func (f *failoverState) recordAttempt(provider, keyID string) {
	if f.excluded == nil {
		f.excluded = make(map[string]bool)
	}
	f.excluded[provider+":"+keyID] = true
	if f.lastProvider != "" && f.lastProvider != provider {
		f.providerSwitch++
	}
	f.lastProvider = provider
	f.attempt++
}

// shouldRetry decides whether another attempt is allowed given the outcome of
// the attempt just recorded. status is the upstream HTTP status (0 for a
// transport error); transportErr indicates an EOF/reset/unreachable class
// error; wroteFirstByte must be false (idempotency: never retry after any
// client byte has been flushed).
//
// Slow-upstream status (default 524) is allowed to switch to a *different*
// provider up to SlowUpstreamMaxSwitch times. Because the failed key is in the
// exclusion set, the resolver naturally moves to the next candidate; if only
// the same provider remains the resolver returns none and the loop ends.
func (f *failoverState) shouldRetry(status int, transportErr bool, wroteFirstByte bool) bool {
	if !f.enabledForModel() {
		return false
	}
	if wroteFirstByte {
		return false
	}
	if f.attempt >= f.maxAttempts() {
		return false
	}
	if status != 0 && status == f.slowStatus() {
		return f.providerSwitch < f.cfg.SlowUpstreamMaxSwitch
	}
	if transportErr {
		return f.cfg.RetryOnTransportError
	}
	if status != 0 {
		return f.isRetryableStatus(status)
	}
	return false
}

func (f *failoverState) slowStatus() int {
	if f.cfg.SlowUpstreamStatus == 0 {
		return 524
	}
	return f.cfg.SlowUpstreamStatus
}
