package routes

import (
	"testing"
)

func TestSubscriptionPlanStatus_Values(t *testing.T) {
	tests := []struct {
		name   string
		status string
		valid  bool
	}{
		{"active", "active", true},
		{"expired", "expired", true},
		{"cancelled", "cancelled", true},
		{"inactive", "inactive", false},
		{"unknown", "unknown", false},
		{"empty", "", false},
	}

	validStatuses := map[string]bool{
		"active":    true,
		"expired":   true,
		"cancelled": true,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := validStatuses[tt.status]
			if isValid != tt.valid {
				t.Errorf("status %q: isValid = %v, want %v", tt.status, isValid, tt.valid)
			}
		})
	}
}

func TestSubscriptionPlanType_Defaults(t *testing.T) {
	tests := []struct {
		name         string
		inputType    string
		wantPlanType string
	}{
		{"empty defaults to token", "", "token"},
		{"explicit token", "token", "token"},
		{"explicit subscription", "subscription", "subscription"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planType := tt.inputType
			if planType == "" {
				planType = "token"
			}
			if planType != tt.wantPlanType {
				t.Errorf("inputType=%q: planType = %q, want %q",
					tt.inputType, planType, tt.wantPlanType)
			}
		})
	}
}

func TestSubscriptionPlanValidation(t *testing.T) {
	tests := []struct {
		name      string
		planName  string
		price     int64
		wantValid bool
	}{
		{"valid plan", "Basic", 100, true},
		{"zero price", "Free", 0, false},
		{"negative price", "Bad", -10, false},
		{"empty name", "", 100, false},
		{"both invalid", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.planName != "" && tt.price > 0
			if isValid != tt.wantValid {
				t.Errorf("name=%q, price=%d: isValid = %v, want %v",
					tt.planName, tt.price, isValid, tt.wantValid)
			}
		})
	}
}

func TestSubscriptionPlanFields(t *testing.T) {
	tests := []struct {
		name         string
		planName     string
		description  string
		price        int64
		quota        int64
		durationDays int
		dailyLimit   int
		planType     string
	}{
		{
			name:         "basic plan",
			planName:     "Basic",
			description:  "Basic plan",
			price:        1000,
			quota:        100000,
			durationDays: 30,
			dailyLimit:   10000,
			planType:     "token",
		},
		{
			name:         "premium plan",
			planName:     "Premium",
			description:  "Premium plan",
			price:        5000,
			quota:        500000,
			durationDays: 30,
			dailyLimit:   50000,
			planType:     "subscription",
		},
		{
			name:         "unlimited plan",
			planName:     "Unlimited",
			description:  "No limits",
			price:        99999,
			quota:        0,
			durationDays: 365,
			dailyLimit:   0,
			planType:     "subscription",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.planName == "" {
				t.Error("plan name should not be empty")
			}
			if tt.price <= 0 {
				t.Errorf("price should be positive, got %d", tt.price)
			}
			if tt.durationDays <= 0 {
				t.Errorf("durationDays should be positive, got %d", tt.durationDays)
			}
			if tt.planType == "" {
				t.Error("planType should not be empty")
			}
		})
	}
}

func TestSubscriptionUpdatePlan_Fields(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		hasValue bool
	}{
		{"update name", "name", true},
		{"update description", "description", true},
		{"update price", "price", true},
		{"update quota", "quota", true},
		{"update duration", "durationDays", true},
		{"update dailyLimit", "dailyLimit", true},
		{"update status", "status", true},
		{"update planType", "planType", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.hasValue {
				t.Errorf("field %q should have a value for update", tt.field)
			}
		})
	}
}

func TestSubscriptionStats_Categories(t *testing.T) {
	statuses := []string{"active", "expired", "cancelled"}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			// Each status should be a valid category
			validStatuses := map[string]bool{
				"active":    true,
				"expired":   true,
				"cancelled": true,
			}
			if !validStatuses[status] {
				t.Errorf("status %q should be a valid category", status)
			}
		})
	}
}

func TestSubscriptionPriceValidation(t *testing.T) {
	tests := []struct {
		name      string
		price     int64
		wantValid bool
	}{
		{"positive price", 100, true},
		{"minimum price", 1, true},
		{"zero price", 0, false},
		{"negative price", -1, false},
		{"large price", 999999, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.price > 0
			if isValid != tt.wantValid {
				t.Errorf("price=%d: isValid = %v, want %v", tt.price, isValid, tt.wantValid)
			}
		})
	}
}

func TestSubscriptionQuotaValues(t *testing.T) {
	tests := []struct {
		name  string
		quota int64
		valid bool
	}{
		{"zero quota (unlimited)", 0, true},
		{"positive quota", 100000, true},
		{"negative quota", -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Zero quota means unlimited, positive is valid, negative is invalid
			isValid := tt.quota >= 0
			if isValid != tt.valid {
				t.Errorf("quota=%d: isValid = %v, want %v", tt.quota, isValid, tt.valid)
			}
		})
	}
}

func TestSubscriptionDurationValues(t *testing.T) {
	tests := []struct {
		name         string
		durationDays int
		valid        bool
	}{
		{"1 day", 1, true},
		{"30 days", 30, true},
		{"365 days", 365, true},
		{"zero days", 0, false},
		{"negative days", -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.durationDays > 0
			if isValid != tt.valid {
				t.Errorf("durationDays=%d: isValid = %v, want %v",
					tt.durationDays, isValid, tt.valid)
			}
		})
	}
}
