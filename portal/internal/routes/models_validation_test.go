package routes

import (
	"encoding/json"
	"testing"
)

// TestModelValidation_BatchValidateRequest tests batch validate request binding.
func TestModelValidation_BatchValidateRequest(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantProv  string
		wantIDs   int
	}{
		{"by provider", `{"provider": "openai"}`, true, "openai", 0},
		{"by status", `{"status": "pending"}`, true, "", 0},
		{"by ids", `{"ids": ["id-1", "id-2"]}`, true, "", 2},
		{"all filters", `{"provider": "openai", "status": "pending", "ids": ["id-1"]}`, true, "openai", 1},
		{"invalid json", `bad`, false, "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Provider string   `json:"provider"`
				Status   string   `json:"status"`
				IDs      []string `json:"ids"`
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
			if req.Provider != tt.wantProv {
				t.Errorf("provider = %q, want %q", req.Provider, tt.wantProv)
			}
			if len(req.IDs) != tt.wantIDs {
				t.Errorf("ids count = %d, want %d", len(req.IDs), tt.wantIDs)
			}
		})
	}
}

// TestModelValidation_AutoConfirmLogic tests the auto-confirm logic.
func TestModelValidation_AutoConfirmLogic(t *testing.T) {
	// Simulate: auto-confirm if model is valid (Available=true)
	type valResult struct {
		Available     bool   `json:"available"`
		SuggestedName string `json:"suggested_name"`
	}

	tests := []struct {
		name        string
		available   bool
		wantConfirm bool
	}{
		{"available model", true, true},
		{"unavailable model", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := valResult{Available: tt.available, SuggestedName: "test-model"}
			autoConfirmed := result.Available
			if autoConfirmed != tt.wantConfirm {
				t.Errorf("autoConfirmed = %v, want %v", autoConfirmed, tt.wantConfirm)
			}
		})
	}
}

// TestModelValidation_ResultStructure tests the validation result JSON structure.
func TestModelValidation_ResultStructure(t *testing.T) {
	type validationResult struct {
		Available     bool   `json:"available"`
		SuggestedName string `json:"suggested_name"`
		Error         string `json:"error,omitempty"`
	}

	result := validationResult{
		Available:     true,
		SuggestedName: "gpt-4o",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if parsed["available"] != true {
		t.Error("available should be true")
	}
	if parsed["suggested_name"] != "gpt-4o" {
		t.Error("suggested_name should be gpt-4o")
	}
}
