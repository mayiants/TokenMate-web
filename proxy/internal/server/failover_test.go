package server

import (
	"testing"

	"github.com/ai-proxy/ai-proxy-go/internal/config"
)

func baseCfg() config.FailoverConfig {
	return config.FailoverConfig{
		Enabled:               true,
		MaxAttempts:           3,
		RetryStatuses:         []int{429, 500, 502, 503, 504, 524},
		RetryOnTransportError: true,
		SlowUpstreamStatus:    524,
		SlowUpstreamMaxSwitch: 2,
	}
}

func TestFailover_DisabledGlobally(t *testing.T) {
	cfg := baseCfg()
	cfg.Enabled = false
	f := newFailoverState(cfg, "glm-5.3")
	f.recordAttempt("approach", "k1")
	if f.shouldRetry(502, false, false) {
		t.Fatal("disabled globally must not retry")
	}
}

func TestFailover_PerModelDisabled(t *testing.T) {
	cfg := baseCfg()
	cfg.DisabledModels = []string{"glm-5.3"}
	f := newFailoverState(cfg, "glm-5.3")
	f.recordAttempt("approach", "k1")
	if f.shouldRetry(502, false, false) {
		t.Fatal("per-model-disabled must not retry")
	}
	// A different model still retries.
	f2 := newFailoverState(cfg, "kimi-k3")
	f2.recordAttempt("approach", "k1")
	if !f2.shouldRetry(502, false, false) {
		t.Fatal("non-disabled model should retry on 502")
	}
}

func TestFailover_MaxAttempts(t *testing.T) {
	f := newFailoverState(baseCfg(), "kimi-k3")
	// attempt 1
	f.recordAttempt("approach", "k1")
	if !f.shouldRetry(502, false, false) {
		t.Fatal("after attempt 1 should allow retry")
	}
	// attempt 2
	f.recordAttempt("tokenrouter", "k2")
	if !f.shouldRetry(502, false, false) {
		t.Fatal("after attempt 2 should allow retry")
	}
	// attempt 3 == max → stop
	f.recordAttempt("tokenrouter", "k3")
	if f.shouldRetry(502, false, false) {
		t.Fatal("after max attempts must stop")
	}
}

func TestFailover_NonRetryableStatus(t *testing.T) {
	f := newFailoverState(baseCfg(), "kimi-k3")
	f.recordAttempt("approach", "k1")
	if f.shouldRetry(400, false, false) {
		t.Fatal("400 is non-retryable")
	}
	if f.shouldRetry(404, false, false) {
		t.Fatal("404 is non-retryable")
	}
}

func TestFailover_FirstByteWritten(t *testing.T) {
	f := newFailoverState(baseCfg(), "kimi-k3")
	f.recordAttempt("approach", "k1")
	if f.shouldRetry(502, false, true) {
		t.Fatal("must never retry after first byte written")
	}
}

func TestFailover_TransportError(t *testing.T) {
	f := newFailoverState(baseCfg(), "kimi-k3")
	f.recordAttempt("approach", "k1")
	if !f.shouldRetry(0, true, false) {
		t.Fatal("transport error should retry when enabled")
	}
	cfg := baseCfg()
	cfg.RetryOnTransportError = false
	f2 := newFailoverState(cfg, "kimi-k3")
	f2.recordAttempt("approach", "k1")
	if f2.shouldRetry(0, true, false) {
		t.Fatal("transport retry disabled")
	}
}

func TestFailover_SlowUpstream524CrossProviderCap(t *testing.T) {
	cfg := baseCfg()
	cfg.MaxAttempts = 10 // make attempts non-binding; the 524 switch cap binds
	f := newFailoverState(cfg, "glm-5.3")
	// attempt 1 on approach, 524 → allowed (0 switches so far)
	f.recordAttempt("approach", "k1")
	if !f.shouldRetry(524, false, false) {
		t.Fatal("524 first switch should be allowed")
	}
	// switch to TR (1 switch), 524 again → allowed (1 < 2)
	f.recordAttempt("tokenrouter", "k2")
	if !f.shouldRetry(524, false, false) {
		t.Fatal("524 second switch should be allowed")
	}
	// switch to provider3 (2 switches), 524 again → NOT allowed (2 == cap)
	f.recordAttempt("provider3", "k3")
	if f.shouldRetry(524, false, false) {
		t.Fatal("524 third switch exceeds SlowUpstreamMaxSwitch=2")
	}
}

func TestFailover_ExcludedTracking(t *testing.T) {
	f := newFailoverState(baseCfg(), "kimi-k3")
	f.recordAttempt("approach", "k1")
	f.recordAttempt("tokenrouter", "k2")
	if !f.excluded["approach:k1"] || !f.excluded["tokenrouter:k2"] {
		t.Fatal("excluded set must track all tried (provider,key)")
	}
	if f.providerSwitch != 1 {
		t.Fatalf("providerSwitch = %d, want 1", f.providerSwitch)
	}
}
