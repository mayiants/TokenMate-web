package circuitbreaker

import (
	"fmt"
	"sync"
	"time"
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

type CircuitBreaker struct {
	mu            sync.Mutex
	state         State
	failures      int
	successes     int
	lastFailure   time.Time
	threshold     int
	resetTimeout  time.Duration
	halfOpenMax   int
	halfOpenCount int
	// probeFailures counts consecutive HalfOpen→Open re-trips since the last
	// Closed state. A high count means the half-open probe keeps failing —
	// the classic transport-wedge signature where the provider never recovers
	// without operator intervention.
	probeFailures int
}

type Config struct {
	Threshold    int
	ResetTimeout time.Duration
	HalfOpenMax  int
}

func NewCircuitBreaker(cfg Config) *CircuitBreaker {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 5
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 30 * time.Second
	}
	if cfg.HalfOpenMax <= 0 {
		cfg.HalfOpenMax = 1
	}
	return &CircuitBreaker{
		state:        Closed,
		threshold:    cfg.Threshold,
		resetTimeout: cfg.ResetTimeout,
		halfOpenMax:  cfg.HalfOpenMax,
	}
}

func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case Closed:
		return true
	case Open:
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = HalfOpen
			cb.halfOpenCount = 0
			return true
		}
		return false
	case HalfOpen:
		if cb.halfOpenCount < cb.halfOpenMax {
			cb.halfOpenCount++
			return true
		}
		return false
	default:
		return false
	}
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case HalfOpen:
		// In half-open, a single success proves the provider recovered — close the circuit.
		cb.state = Closed
		cb.failures = 0
		cb.successes = 0
		cb.probeFailures = 0
	case Closed:
		cb.failures = 0
	}
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()

	switch cb.state {
	case Closed:
		if cb.failures >= cb.threshold {
			cb.state = Open
		}
	case HalfOpen:
		cb.state = Open
		cb.successes = 0
		cb.halfOpenCount = 0
		cb.probeFailures++
	}
}

func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// ProbeFailures returns how many consecutive half-open probes have failed
// since the breaker last closed. Exported for observability (admin dump).
func (cb *CircuitBreaker) ProbeFailures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.probeFailures
}

// reset force-closes the breaker. Used by the admin reset endpoint; the
// next requests will re-trip it immediately if the provider is still down.
func (cb *CircuitBreaker) reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = Closed
	cb.failures = 0
	cb.successes = 0
	cb.halfOpenCount = 0
	cb.probeFailures = 0
}

type ProviderBreakers struct {
	mu     sync.Map
	config Config
	// onTransition, when set, is invoked on every state change with the
	// provider name and the from/to states. Wired to the server logger so
	// breaker transitions are visible in logs for the first time.
	onTransition func(provider string, from, to State)
}

func NewProviderBreakers(cfg Config) *ProviderBreakers {
	return &ProviderBreakers{config: cfg}
}

// SetTransitionHook installs a callback fired on breaker state transitions.
// The callback receives the provider, the state before the call, and the
// state after the call; it is invoked only when the state actually changed.
func (pb *ProviderBreakers) SetTransitionHook(fn func(provider string, from, to State)) {
	pb.onTransition = fn
}

func (pb *ProviderBreakers) Get(provider string) *CircuitBreaker {
	if v, ok := pb.mu.Load(provider); ok {
		return v.(*CircuitBreaker)
	}
	cb := NewCircuitBreaker(pb.config)
	actual, _ := pb.mu.LoadOrStore(provider, cb)
	return actual.(*CircuitBreaker)
}

func (pb *ProviderBreakers) Allow(provider string) bool {
	return pb.Get(provider).Allow()
}

func (pb *ProviderBreakers) RecordSuccess(provider string) {
	cb := pb.Get(provider)
	from := cb.State()
	cb.RecordSuccess()
	pb.fireTransition(provider, from, cb.State())
}

func (pb *ProviderBreakers) RecordFailure(provider string) {
	cb := pb.Get(provider)
	from := cb.State()
	cb.RecordFailure()
	pb.fireTransition(provider, from, cb.State())
}

func (pb *ProviderBreakers) fireTransition(provider string, from, to State) {
	if pb.onTransition == nil || from == to {
		return
	}
	pb.onTransition(provider, from, to)
}

// Reset force-closes the named provider's breaker.
func (pb *ProviderBreakers) Reset(provider string) {
	pb.Get(provider).reset()
}

// BreakerInfo is the observability snapshot of one provider's breaker.
type BreakerInfo struct {
	State         string `json:"state"`
	ProbeFailures int    `json:"probe_failures"`
}

// Status returns per-provider breaker state plus consecutive probe-failure
// counts, for the admin selection-state dump.
func (pb *ProviderBreakers) Status() map[string]BreakerInfo {
	status := map[string]BreakerInfo{}
	pb.mu.Range(func(key, value interface{}) bool {
		cb := value.(*CircuitBreaker)
		status[fmt.Sprintf("%s", key)] = BreakerInfo{
			State:         cb.State().String(),
			ProbeFailures: cb.ProbeFailures(),
		}
		return true
	})
	return status
}
