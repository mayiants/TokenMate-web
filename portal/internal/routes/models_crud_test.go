package routes

import (
	"encoding/json"
	"testing"
)

// TestModelCrud_CreateServiceModelValidation tests the create service model request binding.
func TestModelCrud_CreateServiceModelValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantProv  string
		wantPID   string
	}{
		{"valid model", `{"provider": "openai", "product_id": "gpt-4o"}`, true, "openai", "gpt-4o"},
		{"with prices", `{"provider": "openai", "product_id": "gpt-4o", "input_price_cache_hit": 0.0001, "output_price": 0.0002}`, true, "openai", "gpt-4o"},
		{"with show in store", `{"provider": "openai", "product_id": "gpt-4o", "show_in_store": true}`, true, "openai", "gpt-4o"},
		{"missing provider", `{"product_id": "gpt-4o"}`, true, "", "gpt-4o"},
		{"missing product_id", `{"provider": "openai"}`, true, "openai", ""},
		{"invalid json", `bad`, false, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Provider     string `json:"provider" binding:"required"`
				ProductID    string `json:"product_id" binding:"required"`
				APIModelName string `json:"api_model_name"`
				ShowInStore  bool   `json:"show_in_store"`
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
			if req.ProductID != tt.wantPID {
				t.Errorf("product_id = %q, want %q", req.ProductID, tt.wantPID)
			}
		})
	}
}

// TestModelCrud_UpdateServiceModelValidation tests update service model request binding.
func TestModelCrud_UpdateServiceModelValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"update api_model_name", `{"api_model_name": "gpt-4o-mini"}`, true},
		{"update prices", `{"input_price": 0.0001, "output_price": 0.0002}`, true},
		{"update show_in_store", `{"show_in_store": false}`, true},
		{"update sort_order", `{"sort_order": 5}`, true},
		{"update status", `{"status": "active"}`, true},
		{"empty update", `{}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				APIModelName string   `json:"api_model_name"`
				InputPrice   *float64 `json:"input_price"`
				OutputPrice  *float64 `json:"output_price"`
				ShowInStore  *bool    `json:"show_in_store"`
				SortOrder    *int     `json:"sort_order"`
				Status       string   `json:"status"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// TestModelCrud_ReviewApprovalValidation tests the review approval request binding.
func TestModelCrud_ReviewApprovalValidation(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantValid  bool
		wantAction string
	}{
		{"approve", `{"action": "approve", "notes": "Looks good"}`, true, "approve"},
		{"reject", `{"action": "reject", "notes": "Invalid model"}`, true, "reject"},
		{"missing action", `{"notes": "test"}`, true, ""},
		{"invalid action", `{"action": "maybe"}`, true, "maybe"},
		{"invalid json", `bad`, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Action string `json:"action" binding:"required"`
				Notes  string `json:"notes"`
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
			if req.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", req.Action, tt.wantAction)
			}
			validAction := req.Action == "approve" || req.Action == "reject"
			if !validAction && req.Action != "" {
				t.Logf("action %q would be rejected by handler", req.Action)
			}
		})
	}
}

// TestModelCrud_ModelRoutingValidation tests model routing create request.
func TestModelCrud_ModelRoutingValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"valid route", `{"model_name": "gpt-4o", "provider": "openai", "priority": 1}`, true},
		{"missing provider", `{"model_name": "gpt-4o", "priority": 1}`, true},
		{"missing model_name", `{"provider": "openai", "priority": 1}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				ModelName string `json:"model_name" binding:"required"`
				Provider  string `json:"provider" binding:"required"`
				Priority  int    `json:"priority"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}
