package keypool

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveHealthTier(t *testing.T) {
	fresh := newKeyInternal(&Key{KeyStatic: KeyStatic{ID: "k1"}}, "")
	assert.Equal(t, 0, LiveHealthTier(fresh))

	suspect := newKeyInternal(&Key{
		KeyStatic:  KeyStatic{ID: "k2"},
		KeyRuntime: KeyRuntime{Failures: 1},
	}, "")
	assert.Equal(t, 1, LiveHealthTier(suspect))

	failing := newKeyInternal(&Key{
		KeyStatic:  KeyStatic{ID: "k3"},
		KeyRuntime: KeyRuntime{Failures: 3},
	}, "")
	assert.Equal(t, 2, LiveHealthTier(failing))

	recentFail := newKeyInternal(&Key{
		KeyStatic:  KeyStatic{ID: "k4"},
		KeyRuntime: KeyRuntime{LastFailure: time.Now()},
	}, "")
	assert.Equal(t, 1, LiveHealthTier(recentFail))
}

func TestManager_ResetKeyRuntimeState_ClearsUsageWindow(t *testing.T) {
	m := newTestManager(t)
	key := &Key{KeyStatic: KeyStatic{
		ID: "key-1", Provider: "openai", Priority: "normal",
		Models: []string{"gpt-4o"}, Enabled: true,
	}}
	require.NoError(t, m.AddKey(key))
	// AddKey overwrites limits from tier defaults; tighten afterwards.
	key.RPMLimit = 2

	ki := newKeyInternal(key, "sk-test")
	m.RecordUpstreamRequest("openai", "key-1", "gpt-4o")
	m.RecordUpstreamRequest("openai", "key-1", "gpt-4o")
	require.False(t, m.checkUpstreamAvailable(ki, "gpt-4o"), "RPM budget should be exhausted")

	m.ReportKeyRateLimited("openai", "key-1", "gpt-4o", 60)
	require.True(t, m.IsModelOnCooldown("key-1", "gpt-4o"))

	require.True(t, m.ResetKeyRuntimeState("openai", "key-1"))

	assert.Nil(t, m.getUpstreamUsage("openai:key-1"), "usage window should be dropped")
	assert.True(t, m.checkUpstreamAvailable(ki, "gpt-4o"), "key should have a fresh budget after reset")
	assert.False(t, m.IsModelOnCooldown("key-1", "gpt-4o"), "model cooldown should be cleared")
}

func TestManager_DumpSelectionState(t *testing.T) {
	m := newTestManager(t)
	require.NoError(t, m.AddKey(&Key{KeyStatic: KeyStatic{
		ID: "key-1", Provider: "openai", Priority: "high", Tier: "budget",
		Models: []string{"gpt-4o"}, Enabled: true, KeyPreview: "sk-abc...xyz",
	}}))
	require.NoError(t, m.AddKey(&Key{KeyStatic: KeyStatic{
		ID: "key-2", Provider: "tokenrouter", Priority: "normal",
		Models: []string{"glm-5.3"}, Enabled: true,
	}}))

	m.RecordUpstreamRequest("openai", "key-1", "gpt-4o")
	m.RecordUpstreamTokens("openai", "key-1", "gpt-4o", 42, 0)
	m.ReportKeyRateLimited("openai", "key-1", "gpt-4o", 30)

	states := m.DumpSelectionState("")
	require.Len(t, states, 2)
	assert.Equal(t, "openai", states[0].Provider, "providers should be sorted")
	assert.Equal(t, "tokenrouter", states[1].Provider)

	require.Len(t, states[0].Keys, 1)
	ks := states[0].Keys[0]
	assert.Equal(t, "key-1", ks.KeyID)
	assert.Equal(t, "sk-abc...xyz", ks.KeyPreview)
	assert.Equal(t, "budget", ks.Tier)
	assert.Equal(t, 60, ks.RPMLimit, "budget tier default")
	assert.Equal(t, "active", ks.Status)

	require.NotNil(t, ks.Window)
	assert.Equal(t, 1, ks.Window.RPMUsed)
	assert.Equal(t, int64(42), ks.Window.TPMUsed)
	require.Contains(t, ks.Window.Models, "gpt-4o")
	assert.Equal(t, 1, ks.Window.Models["gpt-4o"].RPM)
	assert.Equal(t, int64(42), ks.Window.Models["gpt-4o"].TPM)

	assert.Contains(t, ks.ModelCooldowns, "gpt-4o")
	assert.Equal(t, 1, ks.HealthTier, "recent rate-limit failure marks the key suspect")

	require.Len(t, states[1].Keys, 1)
	assert.Equal(t, 0, states[1].Keys[0].HealthTier, "fresh key is healthy")
	assert.Nil(t, states[1].Keys[0].Window, "no usage recorded yet")

	filtered := m.DumpSelectionState("tokenrouter")
	require.Len(t, filtered, 1)
	assert.Equal(t, "tokenrouter", filtered[0].Provider)

	assert.Empty(t, m.DumpSelectionState("no-such-provider"))
}
