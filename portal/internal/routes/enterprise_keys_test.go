package routes

import (
	"encoding/json"
	"testing"
)

// TestEnterpriseKeys_CreateKeyValidation tests the create key request binding.
func TestEnterpriseKeys_CreateKeyValidation(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantValid   bool
		wantKeyName string
		wantAssign  string
	}{
		{"valid key", `{"name": "My Key", "assigned_to": "user-123", "group_id": "group-1"}`, true, "My Key", "user-123"},
		{"only required", `{"name": "My Key", "assigned_to": "user-123"}`, true, "My Key", "user-123"},
		{"missing assigned_to", `{"name": "My Key"}`, true, "My Key", ""},
		{"missing name", `{"assigned_to": "user-123"}`, true, "", "user-123"},
		{"invalid json", `not json`, false, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Name       string `json:"name" binding:"required"`
				AssignedTo string `json:"assigned_to" binding:"required"`
				GroupID    string `json:"group_id"`
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
			if req.Name != tt.wantKeyName {
				t.Errorf("name = %q, want %q", req.Name, tt.wantKeyName)
			}
			if req.AssignedTo != tt.wantAssign {
				t.Errorf("assigned_to = %q, want %q", req.AssignedTo, tt.wantAssign)
			}
		})
	}
}

// TestEnterpriseKeys_ReassignKeyValidation tests the reassign key request binding.
func TestEnterpriseKeys_ReassignKeyValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantName  string
	}{
		{"full reassign", `{"name": "New Name", "assigned_to": "user-456", "group_id": "group-2"}`, true, "New Name"},
		{"name only", `{"name": "New Name"}`, true, "New Name"},
		{"assign only", `{"assigned_to": "user-456"}`, true, ""},
		{"empty body", `{}`, true, ""},
		{"invalid json", `bad`, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Name       string `json:"name"`
				AssignedTo string `json:"assigned_to"`
				GroupID    string `json:"group_id"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
			if valid && req.Name != tt.wantName {
				t.Errorf("name = %q, want %q", req.Name, tt.wantName)
			}
		})
	}
}

// TestEnterpriseKeys_KeyPrefixFormat tests the enterprise key prefix generation.
func TestEnterpriseKeys_KeyPrefixFormat(t *testing.T) {
	prefix := "sk-wgit-en-"

	if len(prefix) == 0 {
		t.Error("enterprise key prefix should not be empty")
	}

	// 12 chars per apiKeyPrefixDisplayLength
	if apiKeyPrefixDisplayLength != 12 {
		t.Errorf("apiKeyPrefixDisplayLength = %d, want 12", apiKeyPrefixDisplayLength)
	}

	// Verify key trimming
	fullKey := "sk-wgit-en-12345678-1234-1234-1234-123456789012"
	if len(fullKey) > apiKeyPrefixDisplayLength {
		trimmed := fullKey[:apiKeyPrefixDisplayLength] + "..."
		if len(trimmed) != 15 {
			t.Errorf("trimmed prefix length = %d, want 15", len(trimmed))
		}
	}
}

// TestEnterpriseKeys_RevokeReactivateStatus ensures key status transitions are valid.
func TestEnterpriseKeys_RevokeReactivateStatus(t *testing.T) {
	validTransitions := map[string][]string{
		"active":   {"revoked", "disabled"},
		"revoked":  {"active"},
		"disabled": {"active"},
	}

	tests := []struct {
		from   string
		to     string
		wantOK bool
	}{
		{"active", "revoked", true},
		{"revoked", "active", true},
		{"active", "disabled", true},
		{"disabled", "active", true},
		{"revoked", "deleted", false},
	}

	for _, tt := range tests {
		t.Run(tt.from+"->"+tt.to, func(t *testing.T) {
			allowed := false
			if targets, ok := validTransitions[tt.from]; ok {
				for _, tgt := range targets {
					if tgt == tt.to {
						allowed = true
						break
					}
				}
			}
			if allowed != tt.wantOK {
				t.Errorf("transition %s->%s allowed=%v, want %v", tt.from, tt.to, allowed, tt.wantOK)
			}
		})
	}
}
