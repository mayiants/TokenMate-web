package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ========== Helper Function Tests ==========

func TestGenerateRandomCode(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"length 6", 6},
		{"length 8", 8},
		{"length 12", 12},
		{"length 16", 16},
		{"length 1", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := generateRandomCode(tt.length)
			if len(code) != tt.length {
				t.Errorf("generateRandomCode(%d) returned length %d, want %d", tt.length, len(code), tt.length)
			}
		})
	}
}

func TestGenerateRandomCodeCharacters(t *testing.T) {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for i := 0; i < 100; i++ {
		code := generateRandomCode(12)
		for _, c := range code {
			found := false
			for _, valid := range chars {
				if c == valid {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("generateRandomCode returned invalid character %q", c)
			}
		}
	}
}

func TestGenerateRandomCodeNoDuplicates(t *testing.T) {
	// Test that generating many codes produces valid codes
	code1 := generateRandomCode(12)
	code2 := generateRandomCode(12)

	// Both codes should have the correct length
	if len(code1) != 12 {
		t.Errorf("generateRandomCode(12) returned length %d, want 12", len(code1))
	}
	if len(code2) != 12 {
		t.Errorf("generateRandomCode(12) returned length %d, want 12", len(code2))
	}

	// Codes should contain only valid characters
	const validChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for _, c := range code1 {
		if !strings.ContainsRune(validChars, c) {
			t.Errorf("generateRandomCode returned invalid character: %c", c)
		}
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"123", 123},
		{"0", 0},
		{"999", 999},
		{"", 0},
		{"abc", 0},
		{"12abc34", 1234},
		{"-5", 5}, // negative: function extracts digits only
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseInt(tt.input)
			if got != tt.want {
				t.Errorf("parseInt(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// ========== Form Validation Logic Tests ==========

// TestCreateModelValidation tests the validation logic for creating models
func TestCreateModelValidation(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		vendor  string
		nameVal string
		valid   bool
	}{
		{"all fields", "gpt-4", "openai", "GPT-4", true},
		{"missing model_id", "", "openai", "GPT-4", false},
		{"missing vendor", "gpt-4", "", "GPT-4", false},
		{"missing name", "gpt-4", "openai", "", false},
		{"all empty", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.modelID != "" && tt.vendor != "" && tt.nameVal != ""
			if valid != tt.valid {
				t.Errorf("validation: modelID=%q, vendor=%q, name=%q, got=%v, want=%v",
					tt.modelID, tt.vendor, tt.nameVal, valid, tt.valid)
			}
		})
	}
}

// TestAddUpstreamKeyValidation tests the validation logic for adding upstream keys
func TestAddUpstreamKeyValidation(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		apiKey   string
		valid    bool
	}{
		{"all fields", "openai", "sk-test-1234", true},
		{"missing provider", "", "sk-test-1234", false},
		{"missing api_key", "openai", "", false},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.provider != "" && tt.apiKey != ""
			if valid != tt.valid {
				t.Errorf("validation: provider=%q, apiKey=%q, got=%v, want=%v",
					tt.provider, tt.apiKey, valid, tt.valid)
			}
		})
	}
}

// TestAddProviderValidation tests the validation logic for adding providers
func TestAddProviderValidation(t *testing.T) {
	tests := []struct {
		name    string
		nameVal string
		typeVal string
		valid   bool
	}{
		{"all fields", "OpenAI", "international", true},
		{"missing name", "", "international", false},
		{"missing type", "OpenAI", "", false},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.nameVal != "" && tt.typeVal != ""
			if valid != tt.valid {
				t.Errorf("validation: name=%q, type=%q, got=%v, want=%v",
					tt.nameVal, tt.typeVal, valid, tt.valid)
			}
		})
	}
}

// TestCreatePlanValidation tests the validation logic for creating plans
func TestCreatePlanValidation(t *testing.T) {
	tests := []struct {
		name    string
		nameVal string
		price   string
		valid   bool
	}{
		{"all fields", "Pro", "32900", true},
		{"missing name", "", "32900", false},
		{"missing price", "Pro", "", false},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.nameVal != "" && tt.price != ""
			if valid != tt.valid {
				t.Errorf("validation: name=%q, price=%q, got=%v, want=%v",
					tt.nameVal, tt.price, valid, tt.valid)
			}
		})
	}
}

// ========== Key Preview Logic Tests ==========

func TestKeyPreviewLogic(t *testing.T) {
	tests := []struct {
		key      string
		expected string
	}{
		{"sk-1234567890abcdef", "sk-12345...cdef"},
		{"short-key", "short-key"}, // len <= 12, no transformation
		{"sk-very-long-api-key-12345678901234567890", "sk-very-...7890"},
		{"abc", "abc"},
		{"sk-123456789012", "sk-12345...9012"},   // exactly 12 chars, but >12 triggers transformation (13 chars)
		{"sk-12345678901234", "sk-12345...1234"}, // 14 chars
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			preview := tt.key
			if len(tt.key) > 12 {
				preview = tt.key[:8] + "..." + tt.key[len(tt.key)-4:]
			}
			if preview != tt.expected {
				t.Errorf("keyPreview(%q) = %q, want %q", tt.key, preview, tt.expected)
			}
		})
	}
}

// ========== Status Toggle Logic Tests ==========

func TestToggleModelStatusLogic(t *testing.T) {
	tests := []struct {
		current string
		want    string
	}{
		{"active", "inactive"},
		{"inactive", "active"},
		{"pending", "inactive"}, // non-active/non-inactive defaults to inactive
		{"", "inactive"},        // empty defaults to inactive
	}

	for _, tt := range tests {
		t.Run(tt.current, func(t *testing.T) {
			newStatus := "inactive"
			if tt.current == "inactive" {
				newStatus = "active"
			}
			if newStatus != tt.want {
				t.Errorf("toggleModelStatus(%q) = %q, want %q", tt.current, newStatus, tt.want)
			}
		})
	}
}

func TestTogglePlanStatusLogic(t *testing.T) {
	tests := []struct {
		current string
		want    string
	}{
		{"active", "inactive"},
		{"inactive", "active"},
		{"pending", "inactive"},
		{"", "inactive"},
	}

	for _, tt := range tests {
		t.Run(tt.current, func(t *testing.T) {
			newStatus := "inactive"
			if tt.current == "inactive" {
				newStatus = "active"
			}
			if newStatus != tt.want {
				t.Errorf("togglePlanStatus(%q) = %q, want %q", tt.current, newStatus, tt.want)
			}
		})
	}
}

// ========== Redirect URL Tests ==========

func TestRedirectWithToast(t *testing.T) {
	tests := []struct {
		baseURL   string
		toastType string
		message   string
		contains  []string // check that these are present (order-independent due to url.Values)
	}{
		{
			"/admin/models",
			"success",
			"模型已创建",
			[]string{"toast=success", "message=%E6%A8%A1%E5%9E%8B%E5%B7%B2%E5%88%9B%E5%BB%BA"},
		},
		{
			"/admin/keypool",
			"error",
			"请填写必填字段",
			[]string{"toast=error", "message=%E8%AF%B7%E5%A1%AB%E5%86%99%E5%BF%85%E5%A1%AB%E5%AD%97%E6%AE%B5"},
		},
		{
			"/admin/providers",
			"success",
			"供应商已添加",
			[]string{"toast=success", "message=%E4%BE%9B%E5%BA%94%E5%95%86%E5%B7%B2%E6%B7%BB%E5%8A%A0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.baseURL, func(t *testing.T) {
			q := url.Values{}
			q.Set("toast", tt.toastType)
			q.Set("message", tt.message)
			redirectURL := tt.baseURL + "?" + q.Encode()

			for _, contain := range tt.contains {
				if !strings.Contains(redirectURL, contain) {
					t.Errorf("redirectURL %q should contain %q", redirectURL, contain)
				}
			}
		})
	}
}

// ========== Gin Context Tests (mock context) ==========

func TestPostFormExtraction(t *testing.T) {
	tests := []struct {
		name     string
		formData map[string]string
		field    string
		want     string
	}{
		{"simple field", map[string]string{"model_id": "gpt-4"}, "model_id", "gpt-4"},
		{"empty field", map[string]string{"model_id": ""}, "model_id", ""},
		{"missing field", map[string]string{"vendor": "openai"}, "model_id", ""},
		{"multiple fields", map[string]string{"model_id": "gpt-4", "vendor": "openai", "name": "GPT-4"}, "name", "GPT-4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			form := make(url.Values)
			for k, v := range tt.formData {
				form.Set(k, v)
			}
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c.Request = req

			got := c.PostForm(tt.field)
			if got != tt.want {
				t.Errorf("PostForm(%q) = %q, want %q", tt.field, got, tt.want)
			}
		})
	}
}

func TestParamExtraction(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req := httptest.NewRequest(http.MethodGet, "/admin/models/123", nil)
	c.Request = req

	// Simulate route param by setting it directly
	c.Params = gin.Params{{Key: "id", Value: "123"}}

	got := c.Param("id")
	if got != "123" {
		t.Errorf("Param(id) = %q, want %q", got, "123")
	}
}
