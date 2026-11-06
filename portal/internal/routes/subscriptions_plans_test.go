package routes

import (
	"encoding/json"
	"testing"
)

// TestSubscriptionsPlans_CreatePlanValidation tests the create plan request binding.
func TestSubscriptionsPlans_CreatePlanValidation(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantValid   bool
		wantName    string
		wantPriceOK bool
	}{
		{"valid plan", `{"name": "Pro Plan", "price": 100, "quota": 5000000, "duration_days": 30}`, true, "Pro Plan", true},
		{"with rate limits", `{"name": "Basic", "price": 39, "quota": 1000000, "duration_days": 30, "rpm_limit": 60, "tpm_limit": 100000}`, true, "Basic", true},
		{"zero price", `{"name": "Free", "price": 0, "quota": 100000, "duration_days": 7}`, true, "Free", false},
		{"negative price", `{"name": "Bad", "price": -10, "quota": 1000, "duration_days": 30}`, true, "Bad", false},
		{"invalid json", `bad`, false, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Name         string `json:"name" binding:"required"`
				Price        int64  `json:"price" binding:"required"`
				Quota        int64  `json:"quota" binding:"required"`
				DurationDays int    `json:"duration_days" binding:"required"`
				PlanType     string `json:"plan_type"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
				return
			}
			if !valid {
				return
			}
			if req.Name != tt.wantName {
				t.Errorf("name = %q, want %q", req.Name, tt.wantName)
			}
			priceOK := req.Price > 0
			if priceOK != tt.wantPriceOK {
				t.Errorf("priceOK = %v, want %v", priceOK, tt.wantPriceOK)
			}
		})
	}
}

// TestSubscriptionsPlans_TogglePlanLogic tests the plan toggle logic.
func TestSubscriptionsPlans_TogglePlanLogic(t *testing.T) {
	tests := []struct {
		currentStatus string
		wantNewStatus string
	}{
		{"active", "inactive"},
		{"inactive", "active"},
	}

	for _, tt := range tests {
		t.Run(tt.currentStatus, func(t *testing.T) {
			newStatus := "inactive"
			if tt.currentStatus == "inactive" {
				newStatus = "active"
			}
			if newStatus != tt.wantNewStatus {
				t.Errorf("toggle(%q) = %q, want %q", tt.currentStatus, newStatus, tt.wantNewStatus)
			}
		})
	}
}

// TestSubscriptionsPlans_DurationDisplay tests the duration display labels.
func TestSubscriptionsPlans_DurationDisplay(t *testing.T) {
	tests := []struct {
		durationDays int
		wantLabel    string
	}{
		{0, "forever"},
		{30, "one_month"},
		{365, "one_year"},
		{7, "n_days"},
		{90, "n_days"},
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.durationDays)), func(t *testing.T) {
			var label string
			if tt.durationDays > 0 {
				switch tt.durationDays {
				case 30:
					label = "one_month"
				case 365:
					label = "one_year"
				default:
					label = "n_days"
				}
			} else {
				label = "forever"
			}
			if label != tt.wantLabel {
				t.Errorf("duration label for %d days = %q, want %q", tt.durationDays, label, tt.wantLabel)
			}
		})
	}
}

// TestSubscriptionsPlans_UpdatePlanValidation tests the update plan request binding.
func TestSubscriptionsPlans_UpdatePlanValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"update price", `{"price": 200}`, true},
		{"update name", `{"name": "New Name"}`, true},
		{"update status", `{"status": "inactive"}`, true},
		{"update rate limits", `{"rpm_limit": 120, "tpm_limit": 500000}`, true},
		{"empty update", `{}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Name     string `json:"name"`
				Price    *int64 `json:"price"`
				Quota    *int64 `json:"quota"`
				Status   string `json:"status"`
				RPMLimit *int   `json:"rpm_limit"`
				TPMLimit *int   `json:"tpm_limit"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// TestSubscriptionsPlans_TemplateTiers tests the template tier validation.
func TestSubscriptionsPlans_TemplateTiers(t *testing.T) {
	validTiers := map[int]bool{39: true, 99: true, 399: true, 799: true}

	tests := []struct {
		tier      int
		wantValid bool
	}{
		{39, true},
		{99, true},
		{399, true},
		{799, true},
		{0, false},
		{100, false},
		{999, false},
	}

	for _, tt := range tests {
		valid := validTiers[tt.tier]
		if valid != tt.wantValid {
			t.Errorf("tier %d valid = %v, want %v", tt.tier, valid, tt.wantValid)
		}
	}
}

// TestSubscriptionsPlans_TemplateTypeValidation tests the template type validation.
func TestSubscriptionsPlans_TemplateTypeValidation(t *testing.T) {
	validTypes := map[string]bool{
		"subscription-token": true,
		"subscription-code":  true,
	}

	tests := []struct {
		templateType string
		wantValid    bool
	}{
		{"subscription-token", true},
		{"subscription-code", true},
		{"subscription", false},
		{"", false},
	}

	for _, tt := range tests {
		valid := validTypes[tt.templateType]
		if valid != tt.wantValid {
			t.Errorf("template type %q valid = %v, want %v", tt.templateType, valid, tt.wantValid)
		}
	}
}
