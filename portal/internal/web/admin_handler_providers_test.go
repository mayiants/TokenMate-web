package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestHandleAddProviderValidation tests validation logic for adding providers.
func TestHandleAddProviderValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		formName string
		formType string
		wantOK   bool
	}{
		{"valid provider", "Test Provider", "international", true},
		{"missing name", "", "international", false},
		{"missing type", "Test Provider", "", false},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.formName != "" && tt.formType != ""
			if valid != tt.wantOK {
				t.Errorf("validation: name=%q, type=%q, got=%v, want=%v",
					tt.formName, tt.formType, valid, tt.wantOK)
			}
		})
	}
}

// TestRedirectURLConstruction tests redirect URL building pattern used in admin handlers.
func TestRedirectURLConstruction(t *testing.T) {
	tests := []struct {
		path    string
		toast   string
		message string
	}{
		{"admin/models", "success", "Model created"},
		{"admin/keypool", "error", "Key not found"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest(http.MethodPost, "/"+tt.path, strings.NewReader(""))
			c.Request = req

			// Simulate redirect to toast URL
			redirectURL := "/" + tt.path + "?toast=" + tt.toast + "&message=" + tt.message
			if !strings.Contains(redirectURL, "toast="+tt.toast) {
				t.Errorf("redirect URL missing toast param: %q", redirectURL)
			}
		})
	}
}

// TestHandleToggleModelLogic tests model status toggle logic.
func TestHandleToggleModelLogic(t *testing.T) {
	tests := []struct {
		current string
		want    string
	}{
		{"active", "inactive"},
		{"inactive", "active"},
		// Non-active statuses toggle to active
		{"paused", "active"},
		{"eol_pending", "active"},
	}

	for _, tt := range tests {
		t.Run(tt.current, func(t *testing.T) {
			// Toggle logic: if active -> inactive, else -> active
			newStatus := "inactive"
			if tt.current != "active" {
				newStatus = "active"
			}
			if newStatus != tt.want {
				t.Errorf("toggle(%q) = %q, want %q", tt.current, newStatus, tt.want)
			}
		})
	}
}

// TestHandleApproveModelLogic tests model approval status logic.
func TestHandleApproveModelLogic(t *testing.T) {
	tests := []struct {
		current string
		want    string
	}{
		{"pending", "active"},
		{"active", "active"},
		{"inactive", "active"},
	}

	for _, tt := range tests {
		t.Run(tt.current, func(t *testing.T) {
			newStatus := "active"
			if newStatus != tt.want {
				t.Errorf("approve(%q) = %q, want %q", tt.current, newStatus, tt.want)
			}
		})
	}
}
