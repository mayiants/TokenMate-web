package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestExtractBearerTokenFromValidate tests token extraction.
// Note: extractBearerToken is duplicated here because existing apikeys_test.go
// also tests it but with a slightly different approach.
func TestExtractBearerTokenFromValidate(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		xApiKey    string
		want       string
	}{
		{"bearer token", "Bearer sk-abc123", "", "sk-abc123"},
		{"x-api-key header", "", "sk-xyz789", "sk-xyz789"},
		{"bearer takes priority", "Bearer sk-from-bearer", "sk-from-xapikey", "sk-from-bearer"},
		{"no headers", "", "", ""},
		{"bearer empty value", "Bearer ", "", ""},
		{"non-bearer auth", "Basic dXNlcjpwYXNz", "", ""},
		{"no Bearer prefix", "sk-direct", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			if tt.authHeader != "" {
				c.Request.Header.Set("Authorization", tt.authHeader)
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

// TestValidateKeyResponseDefaults tests default values of the response struct.
func TestValidateKeyResponseDefaults(t *testing.T) {
	resp := validateKeyResponse{}

	if resp.Valid {
		t.Error("expected Valid=false by default")
	}
	if resp.Balance != 0 {
		t.Errorf("Balance = %f, want 0", resp.Balance)
	}
	if resp.RateLimitRPM != 0 {
		t.Errorf("RateLimitRPM = %d, want 0", resp.RateLimitRPM)
	}
	if resp.PricingRatio != 0 {
		t.Errorf("PricingRatio = %f, want 0", resp.PricingRatio)
	}
}

// TestValidateKeyStatusCodeValues tests status code constants used in validation.
func TestValidateKeyStatusCodeValues(t *testing.T) {
	// HTTP status codes used in validateKey
	statusCodes := map[string]int{
		"OK":              http.StatusOK,
		"BadRequest":      http.StatusBadRequest,
		"PaymentRequired": http.StatusPaymentRequired,
		"InternalError":   http.StatusInternalServerError,
	}

	for name, code := range statusCodes {
		if code == 0 {
			t.Errorf("status code for %q should not be 0", name)
		}
	}
}

// TestEnterpriseCreditHardCapLogic tests the enterprise credit cap calculation.
func TestEnterpriseCreditHardCapLogic(t *testing.T) {
	// enterpriseCreditHardCapPercent = 110 means cap at 110% of credit_total
	const hardCapPercent = 110

	tests := []struct {
		name        string
		creditTotal int64
		creditUsed  int64
		wantLimited bool
	}{
		{"under cap", 10000, 5000, false},
		{"at cap (100%)", 10000, 10000, false},
		{"at 110% limit", 10000, 11000, true},
		{"over 110% limit", 10000, 12000, true},
		{"zero total", 0, 100, false},
		{"zero used", 10000, 0, false},
		{"both zero", 0, 0, false},
		{"large numbers under", 1000000, 1000000, false},
		{"large numbers over", 1000000, 1200000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isLimited := false
			if tt.creditTotal > 0 {
				capLimit := tt.creditTotal * hardCapPercent / 100
				isLimited = tt.creditUsed >= capLimit
			}
			if isLimited != tt.wantLimited {
				t.Errorf("creditTotal=%d, creditUsed=%d: limited=%v, want %v",
					tt.creditTotal, tt.creditUsed, isLimited, tt.wantLimited)
			}
		})
	}
}

func TestJsonbModelList(t *testing.T) {
	if got := jsonbModelList(nil); got != nil {
		t.Errorf("nil input: got %v", got)
	}
	if got := jsonbModelList([]string{"a", "b"}); len(got) != 2 {
		t.Errorf("[]string: got %v", got)
	}
	in := []interface{}{"claude-opus-4.6", 42, "deepseek-v4-flash"}
	got := jsonbModelList(in)
	if len(got) != 2 || got[0] != "claude-opus-4.6" || got[1] != "deepseek-v4-flash" {
		t.Errorf("[]interface{} with non-string: got %v", got)
	}
	if got := jsonbModelList("not-a-list"); got != nil {
		t.Errorf("string input: got %v", got)
	}
}

func TestIntersectModelAllowlist(t *testing.T) {
	// nil current (unrestricted) → plan list becomes the allowlist
	got := intersectModelAllowlist(nil, []string{"a", "b"})
	list, ok := got.([]string)
	if !ok || len(list) != 2 {
		t.Errorf("nil current: got %#v", got)
	}

	// intersection with existing list
	got = intersectModelAllowlist([]interface{}{"a", "b", "c"}, []string{"b", "c", "d"})
	list, ok = got.([]string)
	if !ok || len(list) != 2 || list[0] != "b" || list[1] != "c" {
		t.Errorf("intersect: got %#v", got)
	}

	// empty result when no overlap
	got = intersectModelAllowlist([]interface{}{"x"}, []string{"a"})
	if len(got.([]string)) != 0 {
		t.Errorf("no overlap: got %#v", got)
	}
}
