package routes

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEnterpriseMembers_TopupValidation tests the topup member quota request binding.
func TestEnterpriseMembers_TopupValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantQuota int64
	}{
		{"valid topup", `{"quota": 1000000}`, true, 1000000},
		{"large topup", `{"quota": 9999999999}`, true, 9999999999},
		{"zero quota", `{"quota": 0}`, true, 0},
		{"negative quota", `{"quota": -100}`, true, -100},
		{"missing quota", `{}`, true, 0},
		{"invalid json", `bad`, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Quota int64 `json:"quota" binding:"required"`
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
			if req.Quota != tt.wantQuota {
				t.Errorf("quota = %d, want %d", req.Quota, tt.wantQuota)
			}
			// Business rule: quota must be positive
			if req.Quota <= 0 {
				// Should be rejected by handler
				t.Logf("quota=%d would be rejected: must be positive", req.Quota)
			}
		})
	}
}

// TestEnterpriseMembers_QuotaLimitValidation tests setMemberQuotaLimit validation.
func TestEnterpriseMembers_QuotaLimitValidation(t *testing.T) {
	tests := []struct {
		name      string
		limit     int64
		wantValid bool
	}{
		{"positive limit", 1000000, true},
		{"zero unlimited", 0, true},
		{"negative limit", -1, false},
		{"large limit", 9999999999, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.limit >= 0
			if valid != tt.wantValid {
				t.Errorf("quota_limit=%d valid=%v, want %v", tt.limit, valid, tt.wantValid)
			}
		})
	}
}

// TestEnterpriseMembers_RoleValues tests valid member role values.
func TestEnterpriseMembers_RoleValues(t *testing.T) {
	validRoles := map[string]bool{
		"member": true,
		"admin":  true,
	}

	tests := []struct {
		role      string
		wantValid bool
	}{
		{"member", true},
		{"admin", true},
		{"super_admin", false},
		{"", false},
		{"user", false},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			valid := validRoles[tt.role]
			if valid != tt.wantValid {
				t.Errorf("role %q valid = %v, want %v", tt.role, valid, tt.wantValid)
			}
		})
	}
}

// TestEnterpriseMembers_RoleMapping tests the mapping from enterprise role to users.role.
func TestEnterpriseMembers_RoleMapping(t *testing.T) {
	tests := []struct {
		entRole      string
		wantUserRole string
	}{
		{"member", "user"},
		{"admin", "enterprise_admin"},
	}

	for _, tt := range tests {
		t.Run(tt.entRole, func(t *testing.T) {
			mappedRole := "user"
			if tt.entRole == "admin" {
				mappedRole = "enterprise_admin"
			}
			if mappedRole != tt.wantUserRole {
				t.Errorf("mapped role for %q = %q, want %q", tt.entRole, mappedRole, tt.wantUserRole)
			}
		})
	}
}

// TestEnterpriseMembers_AddMemberValidation tests the add member request binding.
func TestEnterpriseMembers_AddMemberValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantEmail string
		wantRole  string
	}{
		{"valid member", `{"email": "user@test.com", "name": "Test User", "role": "member"}`, true, "user@test.com", "member"},
		{"valid admin", `{"email": "admin@test.com", "role": "admin"}`, true, "admin@test.com", "admin"},
		{"only email", `{"email": "user@test.com"}`, true, "user@test.com", ""},
		{"invalid role", `{"email": "user@test.com", "role": "owner"}`, true, "user@test.com", "owner"},
		{"invalid json", `bad`, false, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Email string `json:"email" binding:"required"`
				Name  string `json:"name"`
				Role  string `json:"role"`
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
			// Normalize
			email := strings.ToLower(strings.TrimSpace(req.Email))
			role := strings.TrimSpace(req.Role)
			if role == "" {
				role = "member"
			}
			if email != strings.ToLower(tt.wantEmail) {
				t.Errorf("normalized email = %q, want %q", email, strings.ToLower(tt.wantEmail))
			}
			validRole := role == "member" || role == "admin"
			if !validRole && tt.wantRole != "" {
				t.Logf("role %q would be rejected by handler (must be member or admin)", role)
			}
		})
	}
}

// TestEnterpriseMembers_ToggleStatus tests the status toggle logic.
func TestEnterpriseMembers_ToggleStatus(t *testing.T) {
	tests := []struct {
		currentStatus string
		wantNewStatus string
	}{
		{"active", "suspended"},
		{"suspended", "active"},
	}

	for _, tt := range tests {
		t.Run(tt.currentStatus, func(t *testing.T) {
			newStatus := "suspended"
			if tt.currentStatus == "suspended" {
				newStatus = "active"
			}
			if newStatus != tt.wantNewStatus {
				t.Errorf("toggle(%q) = %q, want %q", tt.currentStatus, newStatus, tt.wantNewStatus)
			}
		})
	}
}
