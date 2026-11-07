package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestLogger(t *testing.T) *Logger {
	t.Helper()
	cfg := &config.Config{
		Portal: config.PortalConfig{URL: "http://127.0.0.1:1"},
		// Large batch + long interval: nothing flushes during the test.
		Audit: config.AuditConfig{Enabled: true, BatchSize: 1000, FlushInterval: time.Hour},
	}
	l, err := NewLogger(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { close(l.stopChan); l.flushTicker.Stop() })
	return l
}

func TestLog_TraceIDAndClientFieldsPropagate(t *testing.T) {
	longUA := strings.Repeat("a", 2000)
	tests := []struct {
		name        string
		entry       AuditEntry
		wantTrace   string // empty => any non-empty random UUID
		wantIP      string
		wantUALen   int
		wantRandomT bool
	}{
		{"explicit trace id is kept", AuditEntry{TraceID: "trace-1", ClientIP: "1.2.3.4", UserAgent: "curl/8"}, "trace-1", "1.2.3.4", 6, false},
		{"empty trace id falls back to uuid", AuditEntry{ClientIP: "5.6.7.8"}, "", "5.6.7.8", 0, true},
		{"user agent is truncated", AuditEntry{TraceID: "t3", UserAgent: longUA}, "t3", "", maxUserAgentLen, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLogger(t)
			tt.entry.Timestamp = time.Now()
			l.Log(tt.entry)

			require.Len(t, l.batch, 1)
			got := l.batch[0]
			if tt.wantRandomT {
				assert.Len(t, got.TraceID, 36)
			} else {
				assert.Equal(t, tt.wantTrace, got.TraceID)
			}
			assert.Equal(t, tt.wantIP, got.ClientIP)
			assert.Len(t, got.UserAgent, tt.wantUALen)
			assert.True(t, l.WasLogged(got.TraceID), "Log must mark the trace as logged")

			raw, err := json.Marshal(got)
			require.NoError(t, err)
			var m map[string]any
			require.NoError(t, json.Unmarshal(raw, &m))
			assert.Equal(t, got.TraceID, m["traceId"])
			if tt.wantIP != "" {
				assert.Equal(t, tt.wantIP, m["clientIp"])
			}
			if tt.wantUALen > 0 {
				assert.Contains(t, m, "userAgent")
			}
		})
	}
}

func TestRecordRequest_UsesProvidedTraceID(t *testing.T) {
	l := newTestLogger(t)
	l.RecordRequest("trace-rr", 1, 1, "m", "p", 1, 1, 0, 0, 1, "ok")
	require.Len(t, l.batch, 1)
	assert.Equal(t, "trace-rr", l.batch[0].TraceID)
}

func TestTTLSet(t *testing.T) {
	now := time.Now()
	s := newTTLSet(time.Minute, 3)
	s.now = func() time.Time { return now }

	s.add("a")
	assert.True(t, s.has("a"))
	assert.False(t, s.has("b"))

	now = now.Add(2 * time.Minute)
	assert.False(t, s.has("a"), "entry must expire after TTL")

	// Capacity: once full of live entries the set resets instead of growing.
	for _, k := range []string{"x", "y", "z", "w"} {
		s.add(k)
	}
	assert.LessOrEqual(t, len(s.items), 3)
	assert.True(t, s.has("w"))
}

func TestIPLimiter(t *testing.T) {
	now := time.Now()
	l := newIPLimiter(3, time.Minute, 2)
	l.now = func() time.Time { return now }

	tests := []struct {
		ip   string
		want bool
	}{
		{"1.1.1.1", true}, {"1.1.1.1", true}, {"1.1.1.1", true},
		{"1.1.1.1", false}, // over the per-IP limit
		{"2.2.2.2", true},  // other IPs unaffected
		{"3.3.3.3", false}, // map full of live windows: fail closed
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, l.allow(tt.ip), tt.ip)
	}

	now = now.Add(61 * time.Second)
	assert.True(t, l.allow("1.1.1.1"), "window must reset")
	assert.True(t, l.allow("3.3.3.3"), "stale entries are evicted when full")
}

func TestAllowAnonymousFailure_CountsDrops(t *testing.T) {
	l := newTestLogger(t)
	before := DroppedAnonymousFailures()
	allowed := 0
	for i := 0; i < anonFailuresPerMinute+5; i++ {
		if l.AllowAnonymousFailure("9.9.9.9") {
			allowed++
		}
	}
	assert.Equal(t, anonFailuresPerMinute, allowed)
	assert.Equal(t, int64(5), DroppedAnonymousFailures()-before)
}

func TestLogDefaultsStatusCode(t *testing.T) {
	l := newTestLogger(t)

	l.Log(AuditEntry{TraceID: "ok-1", Model: "m"})
	l.Log(AuditEntry{TraceID: "ok-2", Model: "m", StatusCode: 201})
	l.Log(AuditEntry{TraceID: "err-1", Model: "m", Error: "boom"})
	l.Log(AuditEntry{TraceID: "err-2", Model: "m", Error: "boom", StatusCode: 502})

	l.batchMutex.Lock()
	defer l.batchMutex.Unlock()
	got := map[string]int{}
	for _, r := range l.batch {
		got[r.TraceID] = r.StatusCode
	}
	assert.Equal(t, map[string]int{"ok-1": 200, "ok-2": 201, "err-1": 0, "err-2": 502}, got)
}
