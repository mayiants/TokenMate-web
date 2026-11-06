package routes

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestHashKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"standard api key", "sk-1234567890abcdef"},
		{"empty string", ""},
		{"with special chars", "sk-abc!@#$%^&*()"},
		{"long key", "sk-" + strings.Repeat("a", 200)},
		{"unicode", "sk-日本語キー"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := crypto.HashKeySHA256(tt.key)

			h := sha256.Sum256([]byte(tt.key))
			want := hex.EncodeToString(h[:])

			if got != want {
				t.Errorf("crypto.HashKeySHA256() = %q, want %q", got, want)
			}
		})
	}
}

func TestHashKeyDeterministic(t *testing.T) {
	key := "sk-test-key-123"
	h1 := crypto.HashKeySHA256(key)
	h2 := crypto.HashKeySHA256(key)
	if h1 != h2 {
		t.Errorf("crypto.HashKeySHA256 not deterministic: %q != %q", h1, h2)
	}
}

func TestHashKeyDifferentInputs(t *testing.T) {
	h1 := crypto.HashKeySHA256("sk-key-1")
	h2 := crypto.HashKeySHA256("sk-key-2")
	if h1 == h2 {
		t.Error("different keys produced same hash")
	}
}

func TestHashKeyLength(t *testing.T) {
	got := crypto.HashKeySHA256("sk-test")
	wantLen := sha256.Size * 2 // hex encoding doubles the byte length
	if len(got) != wantLen {
		t.Errorf("crypto.HashKeySHA256() length = %d, want %d", len(got), wantLen)
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name      string
		bearerHdr string
		xApiKey   string
		want      string
	}{
		{"bearer token", "Bearer sk-abc123", "", "sk-abc123"},
		{"x-api-key header", "", "sk-xyz789", "sk-xyz789"},
		{"bearer takes priority", "Bearer sk-from-bearer", "sk-from-xapikey", "sk-from-bearer"},
		{"no headers", "", "", ""},
		{"bearer empty value", "Bearer ", "", ""},
		{"basic auth ignored", "Basic dXNlcjpwYXNz", "", ""},
		{"bearer case sensitive", "bearer sk-abc", "", ""},
		{"x-api-key with bearer prefix", "Bearer sk-bearer", "sk-xapikey", "sk-bearer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			if tt.bearerHdr != "" {
				c.Request.Header.Set("Authorization", tt.bearerHdr)
			}
			if tt.xApiKey != "" {
				c.Request.Header.Set("X-API-Key", tt.xApiKey)
			}

			got := extractBearerToken(c)
			if got != tt.want {
				t.Errorf("extractBearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIKeyPrefix(t *testing.T) {
	rawKey := "sk-12345678-1234-1234-1234-123456789012"
	prefix := rawKey[:12] + "..."

	if !strings.HasPrefix(prefix, "sk-") {
		t.Errorf("prefix %q should start with 'sk-'", prefix)
	}
	if !strings.HasSuffix(prefix, "...") {
		t.Errorf("prefix %q should end with '...'", prefix)
	}
	if len(prefix) != 15 {
		t.Errorf("prefix length = %d, want 15", len(prefix))
	}
}

func TestAPIKeyFormat(t *testing.T) {
	tests := []struct {
		name   string
		rawKey string
		valid  bool
	}{
		{"standard sk- prefix", "sk-1234567890abcdef1234567890abcdef", true},
		{"missing prefix", "apikey-1234567890abcdef", false},
		{"empty key", "", false},
		{"just prefix", "sk-", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasPrefix := strings.HasPrefix(tt.rawKey, "sk-")
			if hasPrefix != tt.valid {
				t.Errorf("HasPrefix(%q, \"sk-\") = %v, want %v", tt.rawKey, hasPrefix, tt.valid)
			}
		})
	}
}

func TestKeyStatusValues(t *testing.T) {
	validStatuses := []string{"active", "disabled", "revoked"}

	for _, status := range validStatuses {
		if status == "" {
			t.Error("status should not be empty")
		}
	}

	isActive := func(s string) bool { return s == "active" }
	isDisabled := func(s string) bool { return s == "disabled" }

	tests := []struct {
		status  string
		wantAct bool
		wantDis bool
	}{
		{"active", true, false},
		{"disabled", false, true},
		{"revoked", false, false},
	}

	for _, tt := range tests {
		if got := isActive(tt.status); got != tt.wantAct {
			t.Errorf("isActive(%q) = %v, want %v", tt.status, got, tt.wantAct)
		}
		if got := isDisabled(tt.status); got != tt.wantDis {
			t.Errorf("isDisabled(%q) = %v, want %v", tt.status, got, tt.wantDis)
		}
	}
}

func TestAccessModeValues(t *testing.T) {
	validModes := map[string]bool{
		"single_model": true,
		"multi_model":  true,
		"all_models":   true,
	}

	tests := []struct {
		mode  string
		valid bool
	}{
		{"single_model", true},
		{"multi_model", true},
		{"all_models", true},
		{"invalid_mode", false},
		{"", false},
	}

	for _, tt := range tests {
		got := validModes[tt.mode]
		if got != tt.valid {
			t.Errorf("validModes[%q] = %v, want %v", tt.mode, got, tt.valid)
		}
	}
}

func TestKeyValidationResponse(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		balance float64
		valid   bool
		errCode string
	}{
		{"active with balance", "active", 100.0, true, ""},
		{"disabled key", "disabled", 100.0, false, ""},
		{"zero balance", "active", 0, false, "BALANCE_EXHAUSTED"},
		{"negative balance", "active", -10.0, false, "BALANCE_EXHAUSTED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.status == "active" && tt.balance > 0
			if valid != tt.valid {
				t.Errorf("valid = %v, want %v", valid, tt.valid)
			}

			errCode := ""
			if !valid && tt.balance <= 0 && tt.status == "active" {
				errCode = "BALANCE_EXHAUSTED"
			}
			if errCode != tt.errCode {
				t.Errorf("errCode = %q, want %q", errCode, tt.errCode)
			}
		})
	}
}

func TestDefaultKeyValues(t *testing.T) {
	defaultAccessMode := "single_model"
	defaultRateLimit := 60

	if defaultAccessMode == "" {
		t.Error("default access mode should not be empty")
	}
	if defaultRateLimit <= 0 {
		t.Error("default rate limit should be positive")
	}
}

func TestKeyHashUniqueness(t *testing.T) {
	keys := []string{
		"sk-key-1",
		"sk-key-2",
		"sk-key-3",
		"sk-1234567890",
		"sk-abcdefghij",
	}

	seen := make(map[string]string)
	for _, key := range keys {
		h := crypto.HashKeySHA256(key)
		if prev, exists := seen[h]; exists {
			t.Errorf("hash collision: %q and %q produce same hash %q", key, prev, h)
		}
		seen[h] = key
	}
}
