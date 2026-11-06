package keypool

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestModelHealth_InitiallyAvailable(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())
	assert.True(t, mh.IsModelAvailable("key1", "gpt-4o"))
}

func TestModelHealth_DisabledAfterThreshold(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())

	// First two failures — still available
	mh.RecordFailure("key1", "babbage-002", "model not supported")
	assert.True(t, mh.IsModelAvailable("key1", "babbage-002"))

	mh.RecordFailure("key1", "babbage-002", "model not supported")
	assert.True(t, mh.IsModelAvailable("key1", "babbage-002"))

	// Third failure — disabled
	disabled := mh.RecordFailure("key1", "babbage-002", "model not supported")
	assert.True(t, disabled)
	assert.False(t, mh.IsModelAvailable("key1", "babbage-002"))
}

func TestModelHealth_OtherModelsUnaffected(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())

	// Disable babbage-002 on key1
	mh.RecordFailure("key1", "babbage-002", "err")
	mh.RecordFailure("key1", "babbage-002", "err")
	mh.RecordFailure("key1", "babbage-002", "err")

	// Other model on same key is still available
	assert.True(t, mh.IsModelAvailable("key1", "gpt-4o"))

	// Same model on different key is still available
	assert.True(t, mh.IsModelAvailable("key2", "babbage-002"))
}

func TestModelHealth_SuccessResetsFailures(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())

	// Two failures
	mh.RecordFailure("key1", "gpt-4", "err")
	mh.RecordFailure("key1", "gpt-4", "err")

	// Success resets
	mh.RecordSuccess("key1", "gpt-4")

	// Third failure after reset — not yet at threshold
	mh.RecordFailure("key1", "gpt-4", "err")
	assert.True(t, mh.IsModelAvailable("key1", "gpt-4"))
}

func TestModelHealth_SuccessRestoresDisabled(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())

	// Disable the model
	mh.RecordFailure("key1", "gpt-4", "err")
	mh.RecordFailure("key1", "gpt-4", "err")
	mh.RecordFailure("key1", "gpt-4", "err")
	assert.False(t, mh.IsModelAvailable("key1", "gpt-4"))

	// A success restores it
	mh.RecordSuccess("key1", "gpt-4")
	assert.True(t, mh.IsModelAvailable("key1", "gpt-4"))
}

func TestModelHealth_RecoveryAfterTimeout(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())

	// Disable the model
	mh.RecordFailure("key1", "old-model", "err")
	mh.RecordFailure("key1", "old-model", "err")
	mh.RecordFailure("key1", "old-model", "err")
	assert.False(t, mh.IsModelAvailable("key1", "old-model"))

	// Manually set RecoverAt to past
	shard := mh.shardFor("key1")
	shard.mu.Lock()
	entry := shard.entries[modelHealthKey("key1", "old-model")]
	entry.RecoverAt = time.Now().Add(-1 * time.Second)
	shard.mu.Unlock()

	// Should be available again (recovery)
	assert.True(t, mh.IsModelAvailable("key1", "old-model"))
}

func TestModelHealth_DisabledModels(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())

	// Disable two models on key1
	for i := 0; i < 3; i++ {
		mh.RecordFailure("key1", "model-a", "err")
		mh.RecordFailure("key1", "model-b", "err")
	}

	disabled := mh.DisabledModels("key1")
	assert.Len(t, disabled, 2)
	assert.Contains(t, disabled, "model-a")
	assert.Contains(t, disabled, "model-b")

	// key2 has no disabled models
	assert.Empty(t, mh.DisabledModels("key2"))
}

func TestModelHealth_Stats(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())

	// Create 3 entries, disable 2
	mh.RecordFailure("k1", "m1", "err")
	for i := 0; i < 3; i++ {
		mh.RecordFailure("k1", "m2", "err")
		mh.RecordFailure("k1", "m3", "err")
	}

	total, disabled := mh.Stats()
	assert.Equal(t, 3, total)
	assert.Equal(t, 2, disabled)
}

func TestModelHealth_Reset(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())

	for i := 0; i < 3; i++ {
		mh.RecordFailure("key1", "model-x", "err")
	}
	assert.False(t, mh.IsModelAvailable("key1", "model-x"))

	mh.Reset("key1", "model-x")
	assert.True(t, mh.IsModelAvailable("key1", "model-x"))
}

func TestModelHealth_Cleanup(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())

	// Add old entry
	mh.RecordFailure("key1", "old", "err")
	shard := mh.shardFor("key1")
	shard.mu.Lock()
	entry := shard.entries[modelHealthKey("key1", "old")]
	entry.LastFailure = time.Now().Add(-48 * time.Hour)
	shard.mu.Unlock()

	// Add recent entry
	mh.RecordFailure("key1", "recent", "err")

	removed := mh.Cleanup(24 * time.Hour)
	assert.Equal(t, 1, removed)

	// Old entry removed, recent still exists
	total, _ := mh.Stats()
	assert.Equal(t, 1, total)
}

func TestManager_ModelHealth_Integration(t *testing.T) {
	// Test the Manager public API for model health
	mh := NewModelHealth(zap.NewNop())
	require.NotNil(t, mh)

	// Simulate: model fails 3 times → disabled
	mh.RecordFailure("key-abc", "gpt-3.5-turbo-instruct", "model not supported")
	mh.RecordFailure("key-abc", "gpt-3.5-turbo-instruct", "model not supported")
	disabled := mh.RecordFailure("key-abc", "gpt-3.5-turbo-instruct", "model not supported")
	assert.True(t, disabled)

	// Model is not available for routing
	assert.False(t, mh.IsModelAvailable("key-abc", "gpt-3.5-turbo-instruct"))

	// But other models on same key are fine
	assert.True(t, mh.IsModelAvailable("key-abc", "gpt-4o"))
	assert.True(t, mh.IsModelAvailable("key-abc", "claude-opus-4-6"))
}

func TestModelHealth_TransportCancellationNotCounted(t *testing.T) {
	mh := NewModelHealth(zap.NewNop())

	// Client disconnects / transport cancellations must never disable a model:
	// they say nothing about model availability on the key. Regression test for
	// the 2026-10-23 glm-5.3 24h outage on the single-key approach provider.
	cancellations := []string{
		`request failed: Post "https://up.example/v1/chat/completions": context canceled`,
		`request failed: Post "https://up.example/v1/chat/completions": context deadline exceeded`,
		"request failed: write tcp 10.0.0.2:38220->1.2.3.4:8443: use of closed network connection",
	}
	for _, msg := range cancellations {
		for i := 0; i < ModelFailureThreshold+2; i++ {
			assert.False(t, mh.RecordFailure("key1", "glm-5.3", msg), msg)
		}
	}
	assert.True(t, mh.IsModelAvailable("key1", "glm-5.3"))

	// Real upstream rejections still count and disable after the threshold.
	mh.RecordFailure("key1", "glm-5.3", "401 model not supported")
	mh.RecordFailure("key1", "glm-5.3", "401 model not supported")
	assert.True(t, mh.RecordFailure("key1", "glm-5.3", "401 model not supported"))
	assert.False(t, mh.IsModelAvailable("key1", "glm-5.3"))
}

func TestReportKeyFailure_TransportCancellationIgnored(t *testing.T) {
	m := newTestManager(t)
	key := &Key{
		KeyStatic: KeyStatic{
			ID:       "key-cancel",
			Provider: "approach",
			Priority: "normal",
			Models:   []string{"kimi-k3", "glm-5.3"},
			Enabled:  true,
		},
	}
	require.NoError(t, m.AddKey(key))

	cancelErr := `request failed: Post "https://up.example/v1/chat/completions": context canceled`
	closedErr := "request failed: write tcp 10.0.0.2:38220->1.2.3.4:8443: use of closed network connection"

	// Three consecutive cancellations must NOT trigger the 3-failure cooldown.
	for i := 0; i < 3; i++ {
		m.ReportKeyFailure("approach", "key-cancel", "kimi-k3", 500, "upstream_error", "upstream_error", cancelErr)
		m.ReportKeyFailure("approach", "key-cancel", "kimi-k3", 500, "upstream_error", "upstream_error", closedErr)
	}
	assert.True(t, m.IsKeyAvailable(key), "cancellations must not cooldown the key")
	assert.True(t, m.IsModelHealthy("key-cancel", "kimi-k3"))

	// Genuine upstream errors still count: 3 consecutive failures of the same
	// model cool only that (key, model) — the key stays available and sibling
	// models keep serving.
	for i := 0; i < 3; i++ {
		m.ReportKeyFailure("approach", "key-cancel", "kimi-k3", 500, "upstream_error", "upstream_error", "connection refused")
	}
	assert.True(t, m.IsKeyAvailable(key), "key must stay available (model-level cooldown)")
	assert.True(t, m.IsModelOnCooldown("key-cancel", "kimi-k3"), "failing model must be cooled")
	assert.False(t, m.IsModelOnCooldown("key-cancel", "glm-5.3"), "sibling model must not be cooled")
}
