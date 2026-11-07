package keypool

import (
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"go.uber.org/zap"
)

func newTestManagerWithKey(t *testing.T) *Manager {
	t.Helper()
	cfg := &config.Config{Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-testing-only-32chars"}}
	logger, _ := zap.NewDevelopment()
	m := NewManager(cfg, logger)
	if err := m.AddKey(&Key{KeyStatic: KeyStatic{
		ID: "k1", Provider: "approach", Key: "sk-x", KeyPreview: "sk-x...", Tier: "standard",
	}}); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	return m
}

func keyFailures(m *Manager, provider, id string) int {
	pool, ok := m.getPool(provider)
	if !ok {
		return -1
	}
	for _, k := range pool.Keys {
		if k.ID == id {
			return k.Failures
		}
	}
	return -1
}

func TestReportKeySuccess_ResetsFailures(t *testing.T) {
	m := newTestManagerWithKey(t)
	// Accumulate two non-fatal failures (below the 3-strike cooldown threshold).
	m.ReportKeyFailure("approach", "k1", "glm-5.3", 500, "upstream_error", "upstream_error", "boom")
	m.ReportKeyFailure("approach", "k1", "glm-5.3", 500, "upstream_error", "upstream_error", "boom")
	if f := keyFailures(m, "approach", "k1"); f != 2 {
		t.Fatalf("expected Failures=2 before success, got %d", f)
	}
	// A success must reset the counter so sparse errors never accumulate to 3.
	m.ReportKeySuccess("approach", "k1", "glm-5.3")
	if f := keyFailures(m, "approach", "k1"); f != 0 {
		t.Fatalf("expected Failures=0 after success, got %d", f)
	}
}

func TestReportKeyRateLimited_HonorsRetryAfter(t *testing.T) {
	m := newTestManagerWithKey(t)
	// Rate limit does not touch the key failure counter...
	m.ReportKeyRateLimited("approach", "k1", "glm-5.3", 120)
	if f := keyFailures(m, "approach", "k1"); f != 0 {
		t.Fatalf("rate limit must not increment Failures, got %d", f)
	}
	// ...and cools only this (key, model).
	if !m.IsModelOnCooldown("k1", "glm-5.3") {
		t.Fatal("expected (k1, glm-5.3) on cooldown after rate limit")
	}
	if m.IsModelOnCooldown("k1", "kimi-k3") {
		t.Fatal("rate limit on glm-5.3 must not cool kimi-k3")
	}
}

func TestReportKeyRateLimited_CapsAbsurdRetryAfter(t *testing.T) {
	m := newTestManagerWithKey(t)
	// An absurd Retry-After must be capped at 10 minutes, not frozen for hours.
	m.ReportKeyRateLimited("approach", "k1", "glm-5.3", 99999)
	m.modelCooldownsMu.RLock()
	until := m.modelCooldowns["k1"]["glm-5.3"]
	m.modelCooldownsMu.RUnlock()
	if d := time.Until(until); d > 11*time.Minute {
		t.Fatalf("cooldown should be capped near 10m, got %s", d)
	}
}
