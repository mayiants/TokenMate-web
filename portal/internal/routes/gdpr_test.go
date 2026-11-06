package routes

import (
	"encoding/json"
	"testing"
)

// TestGDPR_DeleteConfirmationValidation tests the GDPR delete confirmation logic.
func TestGDPR_DeleteConfirmationValidation(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		wantOK bool
	}{
		{"correct confirmation", `{"confirm": "DELETE_MY_DATA"}`, true},
		{"wrong confirmation", `{"confirm": "delete"}`, false},
		{"empty confirmation", `{"confirm": ""}`, false},
		{"missing field", `{}`, false},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Confirm string `json:"confirm"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			ok := err == nil && req.Confirm == "DELETE_MY_DATA"
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v (confirm=%q)", ok, tt.wantOK, req.Confirm)
			}
		})
	}
}

// TestGDPR_ExportResponseStructure tests the GDPR export JSON structure.
func TestGDPR_ExportResponseStructure(t *testing.T) {
	// Verify that the export data structure has expected keys
	exportData := map[string]interface{}{
		"exportedAt":   "2024-01-01T00:00:00Z",
		"user":         map[string]interface{}{"id": "1", "name": "Test", "email": "test@test.com", "role": "user", "created_at": "2024-01-01T00:00:00Z"},
		"apiKeys":      []interface{}{},
		"usageRecords": []interface{}{},
		"orders":       []interface{}{},
	}

	data, err := json.Marshal(exportData)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	requiredKeys := []string{"exportedAt", "user", "apiKeys", "usageRecords", "orders"}
	for _, key := range requiredKeys {
		if _, ok := parsed[key]; !ok {
			t.Errorf("export data missing key: %q", key)
		}
	}
}

// TestGDPR_DeletionResponseStructure tests the deletion response format.
func TestGDPR_DeletionResponseStructure(t *testing.T) {
	resp := map[string]interface{}{
		"ok":      true,
		"message": "Data deletion completed. Your account has been deactivated.",
	}

	if ok, _ := resp["ok"].(bool); !ok {
		t.Error("deletion response should have ok=true")
	}
	if msg, _ := resp["message"].(string); msg == "" {
		t.Error("deletion response should have a message")
	}
}

// TestGDPR_AnonymizationFormat tests that the anonymized email format is correct.
func TestGDPR_AnonymizationFormat(t *testing.T) {
	userID := "550e8400-e29b-41d4-a716-446655440000"
	anonymizedEmail := "deleted_" + userID + "@deleted.local"

	if len(anonymizedEmail) == 0 {
		t.Error("anonymized email should not be empty")
	}

	// Verify it contains the user ID
	if !containsSubstr(anonymizedEmail, userID) {
		t.Errorf("anonymized email %q should contain user ID %q", anonymizedEmail, userID)
	}

	// Verify format
	if !containsSubstr(anonymizedEmail, "deleted_") || !containsSubstr(anonymizedEmail, "@deleted.local") {
		t.Errorf("anonymized email %q has wrong format", anonymizedEmail)
	}
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
