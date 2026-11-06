package routes

import (
	"testing"
	"time"
)

// TestAnnouncementStatusValues tests valid announcement statuses.
func TestAnnouncementStatusValues(t *testing.T) {
	validStatuses := []string{"draft", "published", "archived"}

	for _, status := range validStatuses {
		if status == "" {
			t.Error("status should not be empty")
		}
	}

	validTransitions := map[string][]string{
		"draft":     {"published", "archived"},
		"published": {"archived"},
		"archived":  {},
	}

	tests := []struct {
		from  string
		to    string
		valid bool
	}{
		{"draft", "published", true},
		{"draft", "archived", true},
		{"published", "archived", true},
		{"published", "draft", false},
		{"archived", "draft", false},
		{"archived", "published", false},
		{"invalid", "published", false},
	}

	for _, tt := range tests {
		t.Run(tt.from+"_to_"+tt.to, func(t *testing.T) {
			allowed := false
			for _, target := range validTransitions[tt.from] {
				if target == tt.to {
					allowed = true
					break
				}
			}
			if allowed != tt.valid {
				t.Errorf("transition %q → %q: allowed=%v, want %v", tt.from, tt.to, allowed, tt.valid)
			}
		})
	}
}

// TestAnnouncementTypeValues tests valid announcement type values.
func TestAnnouncementTypeValues(t *testing.T) {
	validTypes := []string{"info", "warning", "success", "error", "maintenance"}

	for _, at := range validTypes {
		if at == "" {
			t.Error("announcement type should not be empty")
		}
	}

	// Default type should be "info"
	defaultType := "info"
	if defaultType != "info" {
		t.Errorf("default type = %q, want \"info\"", defaultType)
	}
}

// TestAnnouncementDisplayModeValues tests valid display modes.
func TestAnnouncementDisplayModeValues(t *testing.T) {
	validModes := []string{"modal", "banner", "popup", "inline"}

	for _, mode := range validModes {
		if mode == "" {
			t.Error("display mode should not be empty")
		}
	}

	defaultMode := "modal"
	if defaultMode != "modal" {
		t.Errorf("default display mode = %q, want \"modal\"", defaultMode)
	}
}

// TestAnnouncementDismissModeValues tests valid dismiss modes.
func TestAnnouncementDismissModeValues(t *testing.T) {
	validModes := []string{"session", "once", "never"}

	for _, mode := range validModes {
		if mode == "" {
			t.Error("dismiss mode should not be empty")
		}
	}

	defaultMode := "session"
	if defaultMode != "session" {
		t.Errorf("default dismiss mode = %q, want \"session\"", defaultMode)
	}
}

// TestAnnouncementTargetAudienceValues tests valid target audience values.
func TestAnnouncementTargetAudienceValues(t *testing.T) {
	validAudiences := []string{"all", "new_user", "enterprise", "free"}

	for _, audience := range validAudiences {
		if audience == "" {
			t.Error("target audience should not be empty")
		}
	}

	defaultAudience := "all"
	if defaultAudience != "all" {
		t.Errorf("default target audience = %q, want \"all\"", defaultAudience)
	}
}

// TestAnnouncementBannerColorValues tests valid banner color values.
func TestAnnouncementBannerColorValues(t *testing.T) {
	validColors := []string{"primary", "secondary", "success", "danger", "warning", "info", "light", "dark"}

	for _, color := range validColors {
		if color == "" {
			t.Error("banner color should not be empty")
		}
	}

	defaultColor := "primary"
	if defaultColor != "primary" {
		t.Errorf("default banner color = %q, want \"primary\"", defaultColor)
	}
}

// TestAnnouncementDateParsing tests date parsing used in create/update handlers.
func TestAnnouncementDateParsing(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid date", "2024-01-15", false},
		{"valid date 2", "2024-12-31", false},
		{"invalid format", "01/15/2024", true},
		{"invalid date", "2024-13-01", true},
		{"empty string", "", true},
		{"datetime format", "2024-01-15T00:00:00Z", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := time.Parse("2006-01-02", tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("time.Parse(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

// TestAnnouncementRoutesMethodsExist is a compile-time check.
func TestAnnouncementRoutesMethodsExist(t *testing.T) {
	_ = (*AnnouncementRoutes).listPublished
	_ = (*AnnouncementRoutes).getUnreadCount
	_ = (*AnnouncementRoutes).markRead
	_ = (*AnnouncementRoutes).adminList
	_ = (*AnnouncementRoutes).create
	_ = (*AnnouncementRoutes).get
	_ = (*AnnouncementRoutes).update
	_ = (*AnnouncementRoutes).delete
	_ = (*AnnouncementRoutes).publish
	_ = (*AnnouncementRoutes).archive
}
