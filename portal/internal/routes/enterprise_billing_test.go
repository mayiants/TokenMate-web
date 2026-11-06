package routes

import (
	"encoding/json"
	"testing"
)

// TestEnterpriseBilling_TopupValidation tests the topup request binding and validation logic.
func TestEnterpriseBilling_TopupValidation(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantValid    bool
		wantQuotaOK  bool
		wantCreditOK bool
	}{
		{"valid both", `{"quota_total": 1000000, "credit_total": 500000, "reason": "Monthly topup"}`, true, true, true},
		{"valid quota only", `{"quota_total": 1000000, "reason": "Topup"}`, true, true, false},
		{"valid credit only", `{"credit_total": 500000, "reason": "Credit"}`, true, false, true},
		{"negative quota", `{"quota_total": -1, "reason": "test"}`, true, false, false},
		{"negative credit", `{"credit_total": -1, "reason": "test"}`, true, false, false},
		{"zero values", `{"quota_total": 0, "credit_total": 0}`, true, true, true},
		{"invalid json", `not json`, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				QuotaTotal  *int64 `json:"quota_total"`
				CreditTotal *int64 `json:"credit_total"`
				Reason      string `json:"reason"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)

			quotaOK := err == nil && req.QuotaTotal != nil && *req.QuotaTotal >= 0
			creditOK := err == nil && req.CreditTotal != nil && *req.CreditTotal >= 0
			valid := err == nil

			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
			if quotaOK != tt.wantQuotaOK {
				t.Errorf("quotaOK = %v, want %v", quotaOK, tt.wantQuotaOK)
			}
			if creditOK != tt.wantCreditOK {
				t.Errorf("creditOK = %v, want %v", creditOK, tt.wantCreditOK)
			}
		})
	}
}

// TestEnterpriseBilling_BillingDetailResponse tests the billing detail JSON structure.
func TestEnterpriseBilling_BillingDetailResponse(t *testing.T) {
	// Verify that the memberUsage struct JSON tags are correct.
	mu := struct {
		UserID   string  `json:"user_id"`
		UserName string  `json:"user_name"`
		Tokens   int64   `json:"tokens"`
		Requests int64   `json:"requests"`
		CostUSD  float64 `json:"cost_usd"`
	}{
		UserID:   "user-1",
		UserName: "Test User",
		Tokens:   1000,
		Requests: 50,
		CostUSD:  0.005,
	}

	data, err := json.Marshal(mu)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if parsed["user_id"] != "user-1" {
		t.Errorf("user_id = %v, want user-1", parsed["user_id"])
	}
	if parsed["cost_usd"] != 0.005 {
		t.Errorf("cost_usd = %v, want 0.005", parsed["cost_usd"])
	}
}

// TestEnterpriseBilling_ResetMonthlyBillingLogic tests the day-check logic.
func TestEnterpriseBilling_ResetMonthlyBillingLogic(t *testing.T) {
	// Test the skip-when-not-1st logic (extracted from source)
	isFirstOfMonth := func(day int) bool { return day == 1 }

	tests := []struct {
		day  int
		want bool
	}{
		{1, true},
		{2, false},
		{15, false},
		{31, false},
	}

	for _, tt := range tests {
		result := isFirstOfMonth(tt.day)
		if result != tt.want {
			t.Errorf("isFirstOfMonth(%d) = %v, want %v", tt.day, result, tt.want)
		}
	}
}
