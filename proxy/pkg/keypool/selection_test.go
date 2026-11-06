package keypool

import (
	"context"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	cfg := &config.Config{
		Auth: config.AuthConfig{
			EncryptionKey: "this-is-a-32-character-test-key!!",
		},
		Portal: config.PortalConfig{},
		Queue:  config.QueueConfig{},
	}
	return NewManager(cfg, zap.NewNop())
}

func TestNewKeyInternal(t *testing.T) {
	key := &Key{
		KeyStatic: KeyStatic{
			ID:       "key-1",
			Provider: "openai",
			Models:   []string{"gpt-4o", "gpt-4o-mini"},
		},
	}
	ki := newKeyInternal(key, "sk-test")
	require.NotNil(t, ki)
	assert.Equal(t, "key-1", ki.ID)
	assert.Equal(t, "sk-test", ki.ActualKey)
	ok, matched := ki.MatchModel("gpt-4o")
	assert.True(t, ok)
	_ = matched
}

func TestKeyInternal_MatchModel(t *testing.T) {
	ki := newKeyInternal(&Key{
		KeyStatic: KeyStatic{
			Models: []string{
				"claude-opus-4-20260512",
				"claude-sonnet-4",
				"gpt-4o",
			},
		},
	}, "")

	tests := []struct {
		model    string
		expected bool
	}{
		{"gpt-4o", true},
		{"gpt-4o-mini", false},
		{"claude-opus-4-20260512", true},
		// Prefix match with hyphen boundary: "claude-opus-4" → "claude-opus-4-20260512"
		{"claude-opus-4", true},
		{"claude-sonnet-4", true},
	}

	for _, tt := range tests {
		ok, matched := ki.MatchModel(tt.model)
		assert.Equal(t, tt.expected, ok, "model %s matched=%s", tt.model, matched)
	}
}

func TestKeyInternal_QuotaUsed(t *testing.T) {
	ki := newKeyInternal(&Key{KeyStatic: KeyStatic{ID: "key-1"}}, "")
	ki.AddQuotaUsed(100)
	ki.AddQuotaUsed(50)
	assert.Equal(t, int64(150), ki.GetQuotaUsed())
}

func TestManager_AddKeyAndGetKeysForProvider(t *testing.T) {
	m := newTestManager(t)

	key := &Key{
		KeyStatic: KeyStatic{
			ID:       "key-1",
			Provider: "openai",
			Priority: "normal",
			Models:   []string{"gpt-4o"},
			Enabled:  true,
		},
	}
	require.NoError(t, m.AddKey(key))

	high, normal, economy := m.GetKeysForProvider("openai")
	assert.Len(t, high, 0)
	assert.Len(t, normal, 1)
	assert.Len(t, economy, 0)
	assert.Equal(t, "key-1", normal[0].Key.ID)
}

func TestManager_GetKeyByPriority(t *testing.T) {
	m := newTestManager(t)

	for _, id := range []string{"key-high", "key-normal", "key-economy"} {
		priority := "normal"
		switch id {
		case "key-high":
			priority = "high"
		case "key-economy":
			priority = "economy"
		}
		require.NoError(t, m.AddKey(&Key{
			KeyStatic: KeyStatic{
				ID:       id,
				Provider: "openai",
				Priority: priority,
				Models:   []string{"gpt-4o"},
				Enabled:  true,
			},
		}))
	}

	// High priority user should get high or normal key.
	selected := m.GetKeyByPriority(context.Background(), "openai", "high", "gpt-4o", "")
	require.NotNil(t, selected)
	assert.Contains(t, []string{"key-high", "key-normal"}, selected.ID)

	// General user should get normal key.
	selected = m.GetKeyByPriority(context.Background(), "openai", "normal", "gpt-4o", "")
	require.NotNil(t, selected)
	assert.Equal(t, "key-normal", selected.ID)

	// Economy user should get economy key.
	selected = m.GetKeyByPriority(context.Background(), "openai", "economy", "gpt-4o", "")
	require.NotNil(t, selected)
	assert.Equal(t, "key-economy", selected.ID)
}

func TestManager_GetKeyByPriority_PreferredKey(t *testing.T) {
	m := newTestManager(t)

	require.NoError(t, m.AddKey(&Key{
		KeyStatic: KeyStatic{
			ID:       "key-a",
			Provider: "openai",
			Priority: "normal",
			Models:   []string{"gpt-4o"},
			Enabled:  true,
		},
	}))
	require.NoError(t, m.AddKey(&Key{
		KeyStatic: KeyStatic{
			ID:       "key-b",
			Provider: "openai",
			Priority: "normal",
			Models:   []string{"gpt-4o"},
			Enabled:  true,
		},
	}))

	selected := m.GetKeyByPriority(context.Background(), "openai", "normal", "gpt-4o", "key-b")
	require.NotNil(t, selected)
	assert.Equal(t, "key-b", selected.ID)
}

func TestManager_GetKeyByPriority_NoPool(t *testing.T) {
	m := newTestManager(t)
	selected := m.GetKeyByPriority(context.Background(), "unknown", "normal", "gpt-4o", "")
	assert.Nil(t, selected)
}

func TestManager_GetAllKeysForModel(t *testing.T) {
	m := newTestManager(t)

	require.NoError(t, m.AddKey(&Key{
		KeyStatic: KeyStatic{
			ID:       "key-openai",
			Provider: "openai",
			Priority: "normal",
			Models:   []string{"gpt-4o"},
			Enabled:  true,
		},
	}))
	require.NoError(t, m.AddKey(&Key{
		KeyStatic: KeyStatic{
			ID:       "key-anthropic",
			Provider: "anthropic",
			Priority: "high",
			Models:   []string{"claude-sonnet-4"},
			Enabled:  true,
		},
	}))

	high, normal, economy := m.GetAllKeysForModel("gpt-4o", nil, "")
	assert.Len(t, high, 0)
	assert.Len(t, normal, 1)
	assert.Len(t, economy, 0)

	high, normal, economy = m.GetAllKeysForModel("claude-sonnet-4", nil, "")
	assert.Len(t, high, 1)
	assert.Len(t, normal, 0)
	assert.Len(t, economy, 0)
}

func TestManager_GetAllKeysForModel_CatalogGate(t *testing.T) {
	m := newTestManager(t)

	// zimaocloud key advertises the model, but the Portal catalog does NOT
	// activate (zimaocloud, deepseek-v4-flash) — it must not be routable.
	require.NoError(t, m.AddKey(&Key{
		KeyStatic: KeyStatic{
			ID:       "key-zimao",
			Provider: "zimaocloud",
			Priority: "normal",
			Models:   []string{"deepseek-v4-flash"},
			Enabled:  true,
		},
	}))
	// deepseek key advertises nothing (some providers never return a model
	// list) but the catalog activates the pair — it must be routable.
	require.NoError(t, m.AddKey(&Key{
		KeyStatic: KeyStatic{
			ID:       "key-ds",
			Provider: "deepseek",
			Priority: "normal",
			Enabled:  true,
		},
	}))

	m.updateModelNameMap([]ModelNameMapEntry{
		{Provider: "deepseek", APIModelName: "deepseek-v4-flash", ProductID: "deepseek-v4-flash"},
	})

	high, normal, economy := m.GetAllKeysForModel("deepseek-v4-flash", nil, "")
	assert.Len(t, high, 0)
	assert.Len(t, economy, 0)
	require.Len(t, normal, 1)
	assert.Equal(t, "key-ds", normal[0].Key.ID)
	assert.Equal(t, "deepseek-v4-flash", normal[0].UpstreamModelName)

	// GetProvidersForModel follows the same catalog gate.
	assert.Equal(t, []string{"deepseek"}, m.GetProvidersForModel("deepseek-v4-flash"))

	// A model absent from the catalog routes nowhere.
	high, normal, economy = m.GetAllKeysForModel("not-in-catalog", nil, "")
	assert.Len(t, high, 0)
	assert.Len(t, normal, 0)
	assert.Len(t, economy, 0)
	assert.Empty(t, m.GetProvidersForModel("not-in-catalog"))
}

func TestManager_RecordUpstreamRequestAndTokens(t *testing.T) {
	m := newTestManager(t)

	m.RecordUpstreamRequest("openai", "key-1", "gpt-4o")
	m.RecordUpstreamTokens("openai", "key-1", "gpt-4o", 100, 0)
	m.RecordUpstreamTokens("openai", "key-1", "gpt-4o", 50, 0)

	usage := m.getUpstreamUsage("openai:key-1")
	require.NotNil(t, usage)
	assert.Equal(t, 1, usage.rpmUsed)
	assert.Equal(t, int64(150), usage.tpmUsed)
}

func TestManager_RecordUpstreamTokens_AWSCacheReadExclusion(t *testing.T) {
	m := newTestManager(t)

	// AWS providers: cache-read tokens excluded (Bedrock TPM quota does not
	// meter cache reads).
	m.RecordUpstreamTokens("amazon-us-east-1", "aws-1", "claude-haiku-4.5", 10000, 9900)
	usage := m.getUpstreamUsage("amazon-us-east-1:aws-1")
	require.NotNil(t, usage)
	assert.Equal(t, int64(100), usage.tpmUsed)

	// amazon-mantle-* providers share the same Bedrock quota semantics.
	m.RecordUpstreamTokens("amazon-mantle-us-east-1", "aws-2", "claude-haiku-4.5", 10000, 9900)
	usage = m.getUpstreamUsage("amazon-mantle-us-east-1:aws-2")
	require.NotNil(t, usage)
	assert.Equal(t, int64(100), usage.tpmUsed)

	// Other providers keep full-count semantics.
	m.RecordUpstreamTokens("openrouter", "or-1", "anthropic/claude-haiku-4.5", 10000, 9900)
	usage = m.getUpstreamUsage("openrouter:or-1")
	require.NotNil(t, usage)
	assert.Equal(t, int64(10000), usage.tpmUsed)

	// Exclusion never goes negative.
	m.RecordUpstreamTokens("amazon-us-east-1", "aws-3", "claude-haiku-4.5", 10, 50)
	usage = m.getUpstreamUsage("amazon-us-east-1:aws-3")
	require.NotNil(t, usage)
	assert.Equal(t, int64(0), usage.tpmUsed)
}

func TestManager_ReportKeyFailure(t *testing.T) {
	m := newTestManager(t)

	key := &Key{
		KeyStatic: KeyStatic{
			ID:       "key-1",
			Provider: "openai",
			Priority: "normal",
			Models:   []string{"gpt-4o"},
			Enabled:  true,
		},
	}
	require.NoError(t, m.AddKey(key))

	// A single failure should not disable the key.
	m.ReportKeyFailure("openai", "key-1", "gpt-4o", 500, "upstream_error", "upstream_error", "boom")
	assert.True(t, m.IsKeyAvailable(key), "single failure should not disable key")

	// Many failures in quick succession should eventually disable it.
	for i := 0; i < 20; i++ {
		m.ReportKeyFailure("openai", "key-1", "gpt-4o", 500, "upstream_error", "upstream_error", "boom")
	}
	assert.False(t, m.IsKeyAvailable(key), "many failures should disable key")
}

func TestManager_IsKeyAvailable_Cooldown(t *testing.T) {
	m := newTestManager(t)

	key := &Key{
		KeyStatic: KeyStatic{
			ID:       "key-1",
			Provider: "openai",
			Priority: "normal",
			Enabled:  true,
		},
		KeyRuntime: KeyRuntime{
			Status:        "cooldown",
			CooldownUntil: time.Now().Add(time.Hour),
		},
	}
	assert.False(t, m.IsKeyAvailable(key))

	key.CooldownUntil = time.Now().Add(-time.Hour)
	assert.True(t, m.IsKeyAvailable(key))
}

func TestNormalizeModelName(t *testing.T) {
	assert.Equal(t, "gpt-4o", normalizeModelName("gpt.4o"))
	assert.Equal(t, "claude-opus-4-20260512", normalizeModelName("claude.opus.4.20260512"))
}

func TestManager_CheckUpstreamAvailableModelLimits(t *testing.T) {
	m := newTestManager(t)
	ki := newKeyInternal(&Key{KeyStatic: KeyStatic{
		ID:          "key-ml",
		Provider:    "tokenrouter",
		Priority:    "normal",
		Enabled:     true,
		ModelLimits: map[string]ModelLimit{"*": {RPM: 2}, "expensive-model": {RPM: 1}},
	}}, "sk-test")

	// No usage recorded yet: available for any model.
	assert.True(t, m.checkUpstreamAvailable(ki, "model-a"))
	assert.True(t, m.checkUpstreamAvailable(ki, "expensive-model"))

	// Two requests on model-a exhaust the "*" budget for model-a only.
	m.RecordUpstreamRequest("tokenrouter", "key-ml", "model-a")
	m.RecordUpstreamRequest("tokenrouter", "key-ml", "model-a")
	assert.False(t, m.checkUpstreamAvailable(ki, "model-a"))
	assert.True(t, m.checkUpstreamAvailable(ki, "model-b"))

	// Exact model entry overrides "*": expensive-model allows only 1 rpm.
	m.RecordUpstreamRequest("tokenrouter", "key-ml", "expensive-model")
	assert.False(t, m.checkUpstreamAvailable(ki, "expensive-model"))

	// Key without ModelLimits is unaffected by per-model usage.
	kiPlain := newKeyInternal(&Key{KeyStatic: KeyStatic{
		ID: "key-plain", Provider: "tokenrouter", Priority: "normal", Enabled: true,
	}}, "sk-test2")
	assert.True(t, m.checkUpstreamAvailable(kiPlain, "model-a"))
}
