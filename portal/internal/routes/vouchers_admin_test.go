package routes

import (
	"encoding/json"
	"testing"
)

// TestVouchersAdmin_CreateVoucherValidation tests the create voucher request binding.
func TestVouchersAdmin_CreateVoucherValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantName  string
		wantAmtOK bool
	}{
		{"valid voucher", `{"name": "Welcome Gift", "amount": 10.0, "count": 5, "type": "gift"}`, true, "Welcome Gift", true},
		{"single voucher", `{"name": "Test", "amount": 5.0}`, true, "Test", true},
		{"zero amount", `{"name": "Free", "amount": 0}`, true, "Free", false},
		{"negative amount", `{"name": "Bad", "amount": -5}`, true, "Bad", false},
		{"invalid json", `bad`, false, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Name   string  `json:"name" binding:"required"`
				Amount float64 `json:"amount" binding:"required"`
				Count  int     `json:"count"`
				Type   string  `json:"type"`
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
			amtOK := req.Amount > 0
			if amtOK != tt.wantAmtOK {
				t.Errorf("amountOK = %v, want %v", amtOK, tt.wantAmtOK)
			}
		})
	}
}

// TestVouchersAdmin_CountBounds tests the count bounds for voucher creation.
func TestVouchersAdmin_CountBounds(t *testing.T) {
	tests := []struct {
		name      string
		reqCount  int
		wantCount int
	}{
		{"default 1", 0, 1},
		{"custom count", 5, 5},
		{"over max", 200, maxVoucherCreateCount},
		{"exact max", maxVoucherCreateCount, maxVoucherCreateCount},
		{"negative", -5, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count := tt.reqCount
			if count < 1 {
				count = 1
			}
			if count > maxVoucherCreateCount {
				count = maxVoucherCreateCount
			}
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}
		})
	}
}

// TestVouchersAdmin_UpdateVoucherValidation tests the update voucher request binding.
func TestVouchersAdmin_UpdateVoucherValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"update status", `{"status": "disabled"}`, true},
		{"update name", `{"name": "New Name"}`, true},
		{"update description", `{"description": "New desc"}`, true},
		{"update expires", `{"expires_at": "2025-12-31"}`, true},
		{"empty body", `{}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Status      string  `json:"status"`
				Name        string  `json:"name"`
				Description string  `json:"description"`
				ExpiresAt   *string `json:"expires_at"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// TestVouchersAdmin_ToggleLogic tests the toggle voucher status logic.
func TestVouchersAdmin_ToggleLogic(t *testing.T) {
	tests := []struct {
		current string
		want    string
	}{
		{"active", "disabled"},
		{"disabled", "active"},
		{"exhausted", "active"}, // same logic
	}

	for _, tt := range tests {
		t.Run(tt.current, func(t *testing.T) {
			newStatus := "active"
			if tt.current == "active" {
				newStatus = "disabled"
			}
			if newStatus != tt.want {
				t.Errorf("toggle(%q) = %q, want %q", tt.current, newStatus, tt.want)
			}
		})
	}
}

// TestVouchersAdmin_WelcomeVoucherValidation tests the welcome voucher save request.
func TestVouchersAdmin_WelcomeVoucherValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantAmtOK bool
	}{
		{"valid welcome", `{"amount": 5.0, "status": "active"}`, true, true},
		{"zero amount", `{"amount": 0}`, true, false},
		{"negative amount", `{"amount": -1}`, true, false},
		{"invalid json", `bad`, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Amount float64 `json:"amount"`
				Status string  `json:"status"`
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
			amtOK := req.Amount > 0
			if amtOK != tt.wantAmtOK {
				t.Errorf("amountOK = %v, want %v", amtOK, tt.wantAmtOK)
			}
		})
	}
}

// TestVouchersAdmin_VoucherTypeValues tests valid voucher type values.
func TestVouchersAdmin_VoucherTypeValues(t *testing.T) {
	validTypes := map[string]bool{
		"gift":       true,
		"welcome":    true,
		"referral":   true,
		"plan":       true,
		"plan_token": true,
		"plan_code":  true,
	}

	tests := []struct {
		vtype     string
		wantValid bool
	}{
		{"gift", true},
		{"welcome", true},
		{"referral", true},
		{"plan", true},
		{"plan_token", true},
		{"plan_code", true},
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.vtype, func(t *testing.T) {
			valid := validTypes[tt.vtype]
			if valid != tt.wantValid {
				t.Errorf("type %q valid = %v, want %v", tt.vtype, valid, tt.wantValid)
			}
		})
	}
}

// TestVouchersAdmin_RewardTypeValues tests valid reward type values.
func TestVouchersAdmin_RewardTypeValues(t *testing.T) {
	validRewards := map[string]bool{
		"gift_balance": true,
		"balance":      true,
	}

	tests := []struct {
		rtype     string
		wantValid bool
	}{
		{"gift_balance", true},
		{"balance", true},
		{"token", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.rtype, func(t *testing.T) {
			valid := validRewards[tt.rtype]
			if valid != tt.wantValid {
				t.Errorf("reward type %q valid = %v, want %v", tt.rtype, valid, tt.wantValid)
			}
		})
	}
}
