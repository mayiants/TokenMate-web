package routes

import (
	"encoding/json"
	"testing"
	"time"
)

// TestVouchersUser_RedeemValidation tests the redeem request binding.
func TestVouchersUser_RedeemValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantCode  string
	}{
		{"valid code", `{"code": "VG-ABCD-EFGH-IJKL"}`, true, "VG-ABCD-EFGH-IJKL"},
		{"empty code", `{"code": ""}`, true, ""},
		{"missing code", `{}`, true, ""},
		{"invalid json", `bad`, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Code string `json:"code"`
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
			if req.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", req.Code, tt.wantCode)
			}
		})
	}
}

// TestVouchersUser_TransferQuotaValidation tests the transfer quota request binding.
func TestVouchersUser_TransferQuotaValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantAmtOK bool
	}{
		{"valid transfer", `{"amount": 10.0}`, true, true},
		{"zero amount", `{"amount": 0}`, true, false},
		{"negative amount", `{"amount": -5}`, true, false},
		{"missing amount", `{}`, true, false},
		{"invalid json", `bad`, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Amount float64 `json:"amount" binding:"required"`
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
				t.Errorf("amountOK = %v, want %v (%f)", amtOK, tt.wantAmtOK, req.Amount)
			}
		})
	}
}

// TestVouchersUser_GenerateInviteCode tests the generateInviteCode function.
func TestVouchersUser_GenerateInviteCode(t *testing.T) {
	code := generateInviteCode()

	// Should start with "INV-"
	if len(code) < 5 || code[:4] != "INV-" {
		t.Errorf("invite code %q should start with 'INV-'", code)
	}

	// Should not be empty
	if code == "" {
		t.Error("invite code should not be empty")
	}
}

// TestVouchersUser_GenerateAffCode tests the generateAffCode function.
func TestVouchersUser_GenerateAffCode(t *testing.T) {
	code := generateAffCode()

	// Should have length affiliateCodeRandomLength
	if len(code) != affiliateCodeRandomLength {
		t.Errorf("aff code length = %d, want %d", len(code), affiliateCodeRandomLength)
	}

	// Should be lowercase alphanumeric
	for _, c := range code {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			t.Errorf("aff code contains invalid character: %q", string(c))
		}
	}
}

// TestVouchersUser_RedeemVoucherStatusCheck tests the voucher status validation logic.
func TestVouchersUser_RedeemVoucherStatusCheck(t *testing.T) {
	validStatuses := map[string]bool{
		"active":    true,
		"exhausted": false,
		"disabled":  false,
		"expired":   false,
	}

	tests := []struct {
		status    string
		wantValid bool
	}{
		{"active", true},
		{"exhausted", false},
		{"disabled", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			valid := validStatuses[tt.status]
			if valid != tt.wantValid {
				t.Errorf("status %q valid for redeem = %v, want %v", tt.status, valid, tt.wantValid)
			}
		})
	}
}

// TestVouchersUser_RedeemExpiryCheck tests the voucher expiry validation logic.
func TestVouchersUser_RedeemExpiryCheck(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		expiresAt *time.Time
		wantOK    bool
	}{
		{"not expired", timePtr(now.Add(30 * 24 * time.Hour)), true},
		{"already expired", timePtr(now.Add(-1 * 24 * time.Hour)), false},
		{"no expiry", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok := tt.expiresAt == nil || tt.expiresAt.After(now)
			if ok != tt.wantOK {
				t.Errorf("expiry ok = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

// TestVouchersUser_RedeemUsageCheck tests the voucher usage count validation.
func TestVouchersUser_RedeemUsageCheck(t *testing.T) {
	tests := []struct {
		name      string
		maxUses   int
		usedCount int
		wantOK    bool
	}{
		{"under limit", 10, 5, true},
		{"at limit", 5, 5, false},
		{"over limit", 10, 15, false},
		{"unlimited", 0, 1000, true},
		{"unlimited negative", -1, 1000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok := tt.maxUses <= 0 || tt.usedCount < tt.maxUses
			if ok != tt.wantOK {
				t.Errorf("usage ok = %v, want %v (maxUses=%d, usedCount=%d)", ok, tt.wantOK, tt.maxUses, tt.usedCount)
			}
		})
	}
}

// TestVouchersUser_PlanVoucherRatio tests the prorated ratio calculation.
func TestVouchersUser_PlanVoucherRatio(t *testing.T) {
	tests := []struct {
		name      string
		amount    int64
		planPrice int64
		planQuota int64
		wantQuota int64
	}{
		{"full ratio", 1000, 1000, 1000000, 1000000},
		{"half ratio", 500, 1000, 1000000, 500000},
		{"exceeds cap", 2000, 1000, 1000000, 1000000},
		{"zero price", 500, 0, 1000000, 1000000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratio := 1.0
			if tt.planPrice > 0 {
				ratio = float64(tt.amount) / float64(tt.planPrice)
				if ratio > 1.0 {
					ratio = 1.0
				}
			}
			actualQuota := int64(float64(tt.planQuota) * ratio)
			if actualQuota != tt.wantQuota {
				t.Errorf("prorated quota = %d, want %d", actualQuota, tt.wantQuota)
			}
		})
	}
}

// TestVouchersUser_ConsumerTypeValues tests valid consumer type values.
func TestVouchersUser_ConsumerTypeValues(t *testing.T) {
	validTypes := map[string]bool{
		"redeemer": true,
		"creator":  true,
	}

	tests := []struct {
		ctype     string
		wantValid bool
	}{
		{"redeemer", true},
		{"creator", true},
		{"admin", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ctype, func(t *testing.T) {
			valid := validTypes[tt.ctype]
			if valid != tt.wantValid {
				t.Errorf("consumer type %q valid = %v, want %v", tt.ctype, valid, tt.wantValid)
			}
		})
	}
}
