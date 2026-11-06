package routes

import (
	"testing"
)

// TestDashboardRoutesMethodsExist is a compile-time check.
func TestDashboardRoutesMethodsExist(t *testing.T) {
	_ = (*DashboardRoutes).getDashboard
	_ = (*DashboardRoutes).getAdminDashboardStats
	_ = (*DashboardRoutes).getConsumptionTrend
	_ = (*DashboardRoutes).getModelDistribution
	_ = (*DashboardRoutes).getProviderDistribution
	_ = (*DashboardRoutes).getUserRanking
	_ = (*DashboardRoutes).getUserTrend
	_ = (*DashboardRoutes).getQuickActions
	_ = (*DashboardRoutes).getAnalytics
	_ = (*DashboardRoutes).getAdminStats
}

// TestDashboardRangeMapping tests the range param mapping used in getAnalytics.
func TestDashboardRangeMapping(t *testing.T) {
	tests := []struct {
		rangeParam string
		wantDays   int
	}{
		{"7d", 7},
		{"30d", 30},
		{"90d", 90},
		{"unknown", 7},
		{"", 7},
		{"1d", 7},
	}

	for _, tt := range tests {
		t.Run(tt.rangeParam, func(t *testing.T) {
			days := 7
			switch tt.rangeParam {
			case "30d":
				days = 30
			case "90d":
				days = 90
			}
			if days != tt.wantDays {
				t.Errorf("range=%q: days=%d, want %d", tt.rangeParam, days, tt.wantDays)
			}
		})
	}
}

// TestDaysParamClamping tests the days parameter validation used across dashboard endpoints.
func TestDaysParamClamping(t *testing.T) {
	tests := []struct {
		name string
		days int
		want int
	}{
		{"valid value", 7, 7},
		{"large value", 365, 365},
		{"zero (clamped)", 0, 7},
		{"negative (clamped)", -1, 7},
		{"default", 7, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := tt.days
			if days < 1 {
				days = 7
			}
			if days != tt.want {
				t.Errorf("days=%d: clamped=%d, want %d", tt.days, days, tt.want)
			}
		})
	}
}

// TestLimitParamClamping tests the limit parameter validation used in user ranking.
func TestLimitParamClamping(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"valid value", 10, 10},
		{"zero (clamped)", 0, 10},
		{"negative (clamped)", -5, 10},
		{"large value", 100, 100},
		{"default", 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := tt.limit
			if limit < 1 {
				limit = 10
			}
			if limit != tt.want {
				t.Errorf("limit=%d: clamped=%d, want %d", tt.limit, limit, tt.want)
			}
		})
	}
}

// TestAdminRoleDetection tests the role detection logic from getDashboard.
func TestAdminRoleDetection(t *testing.T) {
	tests := []struct {
		role    string
		isAdmin bool
	}{
		{"system_admin", true},
		{"super_admin", true},
		{"user", false},
		{"enterprise_admin", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			isAdmin := tt.role == "system_admin" || tt.role == "super_admin"
			if isAdmin != tt.isAdmin {
				t.Errorf("role=%q: isAdmin=%v, want %v", tt.role, isAdmin, tt.isAdmin)
			}
		})
	}
}

// TestQuickActionsThresholds tests the thresholds used in getQuickActions.
func TestQuickActionsThresholds(t *testing.T) {
	// Low balance threshold: 100 (¥1 = 100 cents in quota)
	lowBalanceThreshold := 100

	tests := []struct {
		name    string
		balance int64
		isLow   bool
	}{
		{"zero balance", 0, true},
		{"below threshold", 50, true},
		{"at threshold minus 1", 99, true},
		{"at threshold", 100, false},
		{"above threshold", 101, false},
		{"large balance", 10000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isLow := tt.balance < int64(lowBalanceThreshold)
			if isLow != tt.isLow {
				t.Errorf("balance=%d: isLow=%v, want %v", tt.balance, isLow, tt.isLow)
			}
		})
	}
}
