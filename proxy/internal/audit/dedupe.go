package audit

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/mayiants/ai-proxy-go/pkg/metrics"
	"go.uber.org/zap"
)

const (
	// loggedTraceTTL only needs to outlive a single request; handlers log
	// right before the response completes and the failure middleware checks
	// immediately after.
	loggedTraceTTL = 2 * time.Minute
	loggedTraceMax = 100000

	// anonFailuresPerMinute bounds failure audits without a resolved key per
	// client IP.
	anonFailuresPerMinute = 20
	anonLimiterMaxIPs     = 50000

	maxUserAgentLen = 512
)

var droppedAnonFailures atomic.Int64

// DroppedAnonymousFailures returns how many anonymous failure audits were
// dropped by the per-IP limiter since process start.
func DroppedAnonymousFailures() int64 { return droppedAnonFailures.Load() }

func truncateUA(ua string) string {
	if len(ua) > maxUserAgentLen {
		return ua[:maxUserAgentLen]
	}
	return ua
}

// MarkLogged records that an audit row for traceID has been emitted.
func (l *Logger) MarkLogged(traceID string) {
	if traceID == "" || l.logged == nil {
		return
	}
	l.logged.add(traceID)
}

// WasLogged reports whether an audit row for traceID was recently emitted.
func (l *Logger) WasLogged(traceID string) bool {
	if traceID == "" || l.logged == nil {
		return false
	}
	return l.logged.has(traceID)
}

// AllowAnonymousFailure reports whether another failure audit without a
// resolved key may be emitted for clientIP. Rejections are counted.
func (l *Logger) AllowAnonymousFailure(clientIP string) bool {
	if l.anonLimiter == nil || l.anonLimiter.allow(clientIP) {
		return true
	}
	metrics.GetPrometheusRegistry().AuditAnonDroppedTotal.Inc()
	if n := droppedAnonFailures.Add(1); n == 1 || n%1000 == 0 {
		l.logger.Warn("Anonymous failure audits rate-limited",
			zap.Int64("dropped_total", n),
		)
	}
	return false
}

// ttlSet is a bounded set whose entries expire after ttl.
type ttlSet struct {
	mu    sync.Mutex
	ttl   time.Duration
	max   int
	items map[string]time.Time
	now   func() time.Time
}

func newTTLSet(ttl time.Duration, max int) *ttlSet {
	return &ttlSet{ttl: ttl, max: max, items: make(map[string]time.Time), now: time.Now}
}

func (s *ttlSet) add(k string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if len(s.items) >= s.max {
		s.sweepLocked(now)
		if len(s.items) >= s.max {
			// Still full of live entries: dropping the set is cheaper than
			// tracking LRU order, and the worst case is one duplicate audit row.
			s.items = make(map[string]time.Time)
		}
	}
	s.items[k] = now.Add(s.ttl)
}

func (s *ttlSet) has(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.items[k]
	if !ok {
		return false
	}
	if !s.now().Before(exp) {
		delete(s.items, k)
		return false
	}
	return true
}

func (s *ttlSet) sweepLocked(now time.Time) {
	for k, exp := range s.items {
		if !now.Before(exp) {
			delete(s.items, k)
		}
	}
}

// ipLimiter is a fixed-window counter per key.
type ipLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	maxKeys int
	items   map[string]*ipWindow
	now     func() time.Time
}

type ipWindow struct {
	start time.Time
	count int
}

func newIPLimiter(limit int, window time.Duration, maxKeys int) *ipLimiter {
	return &ipLimiter{limit: limit, window: window, maxKeys: maxKeys, items: make(map[string]*ipWindow), now: time.Now}
}

func (l *ipLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w, ok := l.items[key]
	if !ok || now.Sub(w.start) >= l.window {
		if !ok && len(l.items) >= l.maxKeys {
			for k, v := range l.items {
				if now.Sub(v.start) >= l.window {
					delete(l.items, k)
				}
			}
			if len(l.items) >= l.maxKeys {
				// Many distinct IPs inside one window: fail closed so a
				// distributed scan cannot bypass the limit by exhausting the map.
				return false
			}
		}
		l.items[key] = &ipWindow{start: now, count: 1}
		return true
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	return true
}
