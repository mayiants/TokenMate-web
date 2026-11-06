package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExtractRegionFromBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{"bedrock-runtime with region", "https://bedrock-runtime.us-east-1.amazonaws.com", "us-east-1"},
		{"bedrock with region", "https://bedrock.eu-west-1.amazonaws.com", "eu-west-1"},
		{"bedrock-runtime ap-northeast", "https://bedrock-runtime.ap-northeast-1.amazonaws.com", "ap-northeast-1"},
		{"bedrock without region (unusual)", "https://bedrock.amazonaws.com", "amazonaws"},
		{"non-bedrock URL", "https://api.openai.com/v1", ""},
		{"empty URL", "", ""},
		{"no protocol", "bedrock-runtime.us-west-2.amazonaws.com", "us-west-2"},
		{"http protocol", "http://bedrock-runtime.us-east-1.amazonaws.com", "us-east-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRegionFromBaseURL(tt.baseURL)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseProviderFilter(t *testing.T) {
	tests := []struct {
		name     string
		filter   string
		expected map[string]bool
	}{
		{"empty string", "", nil},
		{"single provider", "anthropic", map[string]bool{"anthropic": true}},
		{"multiple providers", "anthropic,amazon,meta", map[string]bool{"anthropic": true, "amazon": true, "meta": true}},
		{"with spaces", " anthropic , amazon ", map[string]bool{"anthropic": true, "amazon": true}},
		{"with empty parts", "anthropic,,amazon", map[string]bool{"anthropic": true, "amazon": true}},
		{"only commas", ",,", nil},
		{"whitespace only", "   ", nil},
		{"case preserved (lowered)", "Anthropic,Amazon", map[string]bool{"anthropic": true, "amazon": true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseProviderFilter(tt.filter)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				assert.Equal(t, len(tt.expected), len(result))
				for k, v := range tt.expected {
					assert.Equal(t, v, result[k])
				}
			}
		})
	}
}

func TestNormalizeProviderName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"openai", "openai"},
		{"OpenAI", "openai"},
		{" OPENAI ", "openai"},
		{"alibaba", "qwen"},
		{"ali", "qwen"},
		{"qwen", "qwen"},
		{"stability", "stability"},
		{"Stability AI", "stability ai"},
		{"anthropic", "anthropic"},
		{"Anthropic", "anthropic"},
		{"unknown", "unknown"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeProviderName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPassesFilter(t *testing.T) {
	tests := []struct {
		name         string
		providerName string
		filter       map[string]bool
		expected     bool
	}{
		{"nil filter passes all", "anthropic", nil, true},
		{"empty filter passes all", "anthropic", map[string]bool{}, true},
		{"exact match", "anthropic", map[string]bool{"anthropic": true}, true},
		{"exact match (normalized)", "Anthropic", map[string]bool{"anthropic": true}, true},
		{"no match", "meta", map[string]bool{"anthropic": true, "amazon": true}, false},
		{"substring match", "Stability AI", map[string]bool{"stability": true}, true},
		{"ali alias to qwen", "ali", map[string]bool{"qwen": true}, true},
		{"qwen in filter, provider is alibaba", "alibaba", map[string]bool{"qwen": true}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := passesFilter(tt.providerName, tt.filter)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractProviderFromProfileID(t *testing.T) {
	tests := []struct {
		profileID string
		expected  string
	}{
		{"us.anthropic.claude-sonnet-4-6-v1:0", "anthropic"},
		{"eu.amazon.nova-pro-v1:0", "amazon"},
		{"us.meta.llama-3-1-70b-v1:0", "meta"},
		{"ap-northeast-1.cohere.command-r-v1:0", "cohere"},
		{"single-part", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.profileID, func(t *testing.T) {
			result := extractProviderFromProfileID(tt.profileID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSetDiscoveryTTL(t *testing.T) {
	original := discoveryTTL
	defer func() { discoveryTTL = original }()

	SetDiscoveryTTL(2 * time.Hour)
	assert.Equal(t, 2*time.Hour, discoveryTTL)

	SetDiscoveryTTL(0)
	assert.Equal(t, time.Duration(0), discoveryTTL)
}

func TestInvalidateDiscoveryCache(t *testing.T) {
	// Clear cache first
	discoveryCacheMu.Lock()
	discoveryCache = make(map[string]*discoveryCacheEntry)
	discoveryCacheMu.Unlock()

	// Add some entries
	discoveryCacheMu.Lock()
	discoveryCache["us-east-1:anthropic"] = &discoveryCacheEntry{
		models:    []bedrockModelInfo{{ModelID: "test"}},
		expiresAt: time.Now().Add(time.Hour),
	}
	discoveryCache["eu-west-1:amazon"] = &discoveryCacheEntry{
		models:    []bedrockModelInfo{{ModelID: "test2"}},
		expiresAt: time.Now().Add(time.Hour),
	}
	count := len(discoveryCache)
	discoveryCacheMu.Unlock()
	assert.Equal(t, 2, count)

	// Invalidate all
	invalidateDiscoveryCache("", "")
	discoveryCacheMu.RLock()
	after := len(discoveryCache)
	discoveryCacheMu.RUnlock()
	assert.Equal(t, 0, after)

	// Invalidate with region parameter (no-op now since it clears all)
	discoveryCacheMu.Lock()
	discoveryCache["us-east-1:test"] = &discoveryCacheEntry{expiresAt: time.Now().Add(time.Hour)}
	discoveryCacheMu.Unlock()

	invalidateDiscoveryCache("whatever", "")
	discoveryCacheMu.RLock()
	after2 := len(discoveryCache)
	discoveryCacheMu.RUnlock()
	assert.Equal(t, 0, after2, "should clear all entries")
}
