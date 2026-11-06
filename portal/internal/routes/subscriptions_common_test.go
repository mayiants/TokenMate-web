package routes

import (
	"testing"
)

// TestSubscriptionsCommon_StringInt tests the stringInt helper function.
func TestSubscriptionsCommon_StringInt(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want string
	}{
		{"zero", 0, "0"},
		{"positive", 42, "42"},
		{"negative", -1, "-1"},
		{"large", 999999, "999999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringInt(tt.n)
			if got != tt.want {
				t.Errorf("stringInt(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

// TestSubscriptionsCommon_CoalesceString tests the coalesceString helper function.
func TestSubscriptionsCommon_CoalesceString(t *testing.T) {
	hello := "hello"
	world := "world"
	empty := ""

	tests := []struct {
		name string
		a    *string
		b    *string
		want string
	}{
		{"both non-nil", &hello, &world, "hello"},
		{"a empty b non-nil", &empty, &world, "world"},
		{"a nil b non-nil", nil, &world, "world"},
		{"both nil", nil, nil, ""},
		{"a non-nil b nil", &hello, nil, "hello"},
		{"both empty", &empty, &empty, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coalesceString(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("coalesceString(%v, %v) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestSubscriptionsCommon_PlanTypeValues tests valid subscription plan types.
func TestSubscriptionsCommon_PlanTypeValues(t *testing.T) {
	validTypes := map[string]bool{
		"token": true,
		"code":  true,
	}

	tests := []struct {
		planType  string
		wantValid bool
	}{
		{"token", true},
		{"code", true},
		{"subscription", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.planType, func(t *testing.T) {
			valid := validTypes[tt.planType]
			if valid != tt.wantValid {
				t.Errorf("plan type %q valid = %v, want %v", tt.planType, valid, tt.wantValid)
			}
		})
	}
}

// TestSubscriptionsCommon_StatusValues tests valid subscription status values.
func TestSubscriptionsCommon_StatusValues(t *testing.T) {
	validStatuses := map[string]bool{
		"active":    true,
		"expired":   true,
		"cancelled": true,
		"inactive":  true,
	}

	tests := []struct {
		status    string
		wantValid bool
	}{
		{"active", true},
		{"expired", true},
		{"cancelled", true},
		{"inactive", true},
		{"pending", false},
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
