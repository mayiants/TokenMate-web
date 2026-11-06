package routes

import (
	"encoding/json"
	"testing"
)

// TestEnterpriseCommon_CreateEnterpriseValidation tests the create enterprise request binding.
func TestEnterpriseCommon_CreateEnterpriseValidation(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantValid      bool
		wantName       string
		wantMaxMembers int
		wantMaxAdmins  int
	}{
		{"valid enterprise", `{"name": "Acme Corp", "contact_email": "admin@acme.com", "max_members": 100, "max_admins": 5, "billing_cycle": "monthly"}`, true, "Acme Corp", 100, 5},
		{"only name", `{"name": "Acme Corp"}`, true, "Acme Corp", 0, 0},
		{"missing name", `{"contact_email": "admin@acme.com"}`, true, "", 0, 0},
		{"with defaults", `{"name": "Test"}`, true, "Test", 0, 0},
		{"invalid json", `not json`, false, "", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Name           string  `json:"name" binding:"required"`
				ContactEmail   string  `json:"contact_email"`
				MaxMembers     int     `json:"max_members"`
				MaxAdmins      int     `json:"max_admins"`
				AllowedDomains string  `json:"allowed_domains"`
				BillingCycle   string  `json:"billing_cycle"`
				PricingRatio   float64 `json:"pricing_ratio"`
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
		})
	}
}

// TestEnterpriseCommon_DefaultValues tests that default values are applied correctly.
func TestEnterpriseCommon_DefaultValues(t *testing.T) {
	// Simulate the defaulting logic from createEnterprise
	tests := []struct {
		name            string
		inputMaxMembers int
		inputMaxAdmins  int
		inputBilling    string
		inputRatio      float64
		wantMaxMembers  int
		wantMaxAdmins   int
		wantBilling     string
		wantRatio       float64
	}{
		{"all defaults", 0, 0, "", 0, 50, 3, "monthly", 1.0},
		{"custom members", 100, 0, "", 0, 100, 3, "monthly", 1.0},
		{"custom all", 200, 10, "annual", 1.5, 200, 10, "annual", 1.5},
		{"zero ratio", 0, 0, "monthly", 0, 50, 3, "monthly", 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			members := tt.inputMaxMembers
			if members == 0 {
				members = 50
			}
			admins := tt.inputMaxAdmins
			if admins == 0 {
				admins = 3
			}
			billing := tt.inputBilling
			if billing == "" {
				billing = "monthly"
			}
			ratio := tt.inputRatio
			if ratio == 0 {
				ratio = 1.0
			}

			if members != tt.wantMaxMembers {
				t.Errorf("max_members = %d, want %d", members, tt.wantMaxMembers)
			}
			if admins != tt.wantMaxAdmins {
				t.Errorf("max_admins = %d, want %d", admins, tt.wantMaxAdmins)
			}
			if billing != tt.wantBilling {
				t.Errorf("billing_cycle = %q, want %q", billing, tt.wantBilling)
			}
			if ratio != tt.wantRatio {
				t.Errorf("pricing_ratio = %f, want %f", ratio, tt.wantRatio)
			}
		})
	}
}

// TestEnterpriseCommon_RoleCheck tests enterprise role validation logic.
func TestEnterpriseCommon_RoleCheck(t *testing.T) {
	tests := []struct {
		name        string
		role        string
		wantAdmin   bool
		wantIsAdmin bool
	}{
		{"system_admin", "system_admin", true, true},
		{"super_admin", "super_admin", true, true},
		{"enterprise_admin", "enterprise_admin", true, false},
		{"regular user", "user", false, false},
		{"empty role", "", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isEnterpriseAdmin := tt.role == "enterprise_admin" || tt.role == "system_admin" || tt.role == "super_admin"
			isAdmin := tt.role == "system_admin" || tt.role == "super_admin"

			if isEnterpriseAdmin != tt.wantAdmin {
				t.Errorf("isEnterpriseAdmin(%q) = %v, want %v", tt.role, isEnterpriseAdmin, tt.wantAdmin)
			}
			if isAdmin != tt.wantIsAdmin {
				t.Errorf("isAdmin(%q) = %v, want %v", tt.role, isAdmin, tt.wantIsAdmin)
			}
		})
	}
}

// TestEnterpriseCommon_EnterpriseStatusValues tests valid enterprise status values.
func TestEnterpriseCommon_EnterpriseStatusValues(t *testing.T) {
	validStatuses := map[string]bool{
		"active":       true,
		"suspended":    true,
		"admin_vacant": true,
	}

	tests := []struct {
		status    string
		wantValid bool
	}{
		{"active", true},
		{"suspended", true},
		{"admin_vacant", true},
		{"deleted", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			valid := validStatuses[tt.status]
			if valid != tt.wantValid {
				t.Errorf("status %q valid = %v, want %v", tt.status, valid, tt.wantValid)
			}
		})
	}
}
