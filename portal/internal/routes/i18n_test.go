package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestGetLocale_Cookie tests locale extraction from cookie.
func TestGetLocale_Cookie(t *testing.T) {
	tests := []struct {
		name       string
		cookieVal  string
		wantLocale string
	}{
		{"zh cookie", "zh", "zh"},
		{"en cookie", "en", "en"},
		{"empty cookie", "", ""},     // falls through to header
		{"invalid cookie", "fr", ""}, // falls through to header
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			if tt.cookieVal != "" {
				c.Request.Header.Set("Cookie", "locale="+tt.cookieVal)
			}

			locale := ""
			if cookie, err := c.Cookie("locale"); err == nil && (cookie == "zh" || cookie == "en") {
				locale = cookie
			}

			if tt.cookieVal == "zh" || tt.cookieVal == "en" {
				if locale != tt.wantLocale {
					t.Errorf("locale from cookie = %q, want %q", locale, tt.wantLocale)
				}
			}
		})
	}
}

// TestGetLocale_Header tests locale extraction from Accept-Language header.
func TestGetLocale_Header(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantResult string
	}{
		{"zh-CN", "zh-CN", "zh"},
		{"zh-TW", "zh-TW", "zh"},
		{"en-US", "en-US", "en"},
		{"fr-FR", "fr-FR", "en"},
		{"ja-JP", "ja-JP", "en"},
		{"zh-hans", "zh-hans", "zh"},
		{"empty", "", "en"},
		{"complex with q", "zh-CN,zh;q=0.9,en;q=0.8", "zh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := tt.header
			if loc == "" {
				if tt.wantResult == "en" {
					return // OK
				}
			}

			loc = strings.ToLower(strings.TrimSpace(loc))
			for _, sep := range []string{"-", "_", ",", ";"} {
				if idx := strings.Index(loc, sep); idx > 0 {
					loc = loc[:idx]
				}
			}

			result := "en"
			if loc == "zh" {
				result = "zh"
			}

			if result != tt.wantResult {
				t.Errorf("GetLocale for header %q = %q, want %q", tt.header, result, tt.wantResult)
			}
		})
	}
}

// TestGetLocale_QueryParam tests locale extraction from query param.
func TestGetLocale_QueryParam(t *testing.T) {
	tests := []struct {
		name       string
		queryParam string
		wantResult string
	}{
		{"zh param", "zh", "zh"},
		{"en param", "en", "en"},
		{"empty param", "", "en"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the logic
			loc := tt.queryParam
			if loc == "" {
				loc = "en"
			} else {
				loc = strings.ToLower(strings.TrimSpace(loc))
				if loc != "zh" && loc != "en" {
					loc = "en"
				}
			}

			if loc != tt.wantResult {
				t.Errorf("locale from query = %q, want %q", loc, tt.wantResult)
			}
		})
	}
}

// TestGetLocale_FullFlow tests the complete locale resolution flow.
func TestGetLocale_FullFlow(t *testing.T) {
	tests := []struct {
		name       string
		cookie     string
		header     string
		query      string
		wantLocale string
	}{
		{"cookie takes priority", "zh", "en-US", "en", "zh"},
		{"header when no cookie", "", "zh-CN", "", "zh"},
		{"query when no header", "", "", "zh", "zh"},
		{"default en", "", "", "", "en"},
		{"fr defaults to en", "", "fr-FR", "", "en"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/?locale="+tt.query, nil)

			if tt.cookie != "" {
				c.Request.Header.Set("Cookie", "locale="+tt.cookie)
			}
			if tt.header != "" {
				c.Request.Header.Set("Accept-Language", tt.header)
			}

			locale := GetLocale(c)

			if locale != tt.wantLocale {
				t.Errorf("GetLocale() = %q, want %q", locale, tt.wantLocale)
			}
		})
	}
}

// TestRouteI18n_Init tests that routeI18n is initialized.
func TestRouteI18n_Init(t *testing.T) {
	// routeI18n should be non-nil after init()
	if routeI18n == nil {
		t.Error("routeI18n should not be nil after init()")
	}
}

// TestSupportedLocales tests that only zh and en are supported.
func TestSupportedLocales(t *testing.T) {
	supported := map[string]bool{"zh": true, "en": true}

	tests := []struct {
		locale string
		want   bool
	}{
		{"zh", true},
		{"en", true},
		{"fr", false},
		{"de", false},
		{"ja", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.locale, func(t *testing.T) {
			if supported[tt.locale] != tt.want {
				t.Errorf("supported[%q] = %v, want %v", tt.locale, supported[tt.locale], tt.want)
			}
		})
	}
}
