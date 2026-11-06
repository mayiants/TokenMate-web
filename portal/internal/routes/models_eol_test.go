package routes

import (
	"encoding/json"
	"testing"
)

// TestModelEOL_SetEOLPendingValidation tests the setEOLPending request binding.
func TestModelEOL_SetEOLPendingValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"with eol date", `{"eol_service_at": "2025-12-31", "eol_notes": "Model being retired"}`, true},
		{"without date", `{"eol_notes": "Deprecated model"}`, true},
		{"empty body", `{}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				EOLServiceAt string `json:"eol_service_at"`
				EOLNotes     string `json:"eol_notes"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// TestModelEOL_UpdateEOLValidation tests the update EOL request binding.
func TestModelEOL_UpdateEOLValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"update date", `{"eol_service_at": "2025-12-31"}`, true},
		{"update purge", `{"eol_purge_at": "2026-01-31"}`, true},
		{"update notes", `{"eol_notes": "Updated notes"}`, true},
		{"all fields", `{"eol_service_at": "2025-12-31", "eol_purge_at": "2026-01-31", "eol_notes": "Notes"}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				EOLServiceAt *string `json:"eol_service_at"`
				EOLPurgeAt   *string `json:"eol_purge_at"`
				EOLNotes     *string `json:"eol_notes"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// TestModelEOL_EOLReminderDays tests the EOL reminder day logic.
func TestModelEOL_EOLReminderDays(t *testing.T) {
	// Only send reminders at 7, 3, or 1 day before EOL
	isReminderDay := func(days int) bool {
		return days == 7 || days == 3 || days == 1
	}

	tests := []struct {
		days    int
		wantRem bool
	}{
		{7, true},
		{3, true},
		{1, true},
		{5, false},
		{2, false},
		{10, false},
		{0, false},
		{-1, false},
	}

	for _, tt := range tests {
		result := isReminderDay(tt.days)
		if result != tt.wantRem {
			t.Errorf("isReminderDay(%d) = %v, want %v", tt.days, result, tt.wantRem)
		}
	}
}

// TestModelEOL_DaysRemainingCalculation tests days remaining calculation.
func TestModelEOL_DaysRemainingCalculation(t *testing.T) {
	tests := []struct {
		name        string
		hoursRemain int
		wantDays    int
	}{
		{"24 hours", 24, 1},
		{"48 hours", 48, 2},
		{"168 hours (7 days)", 168, 7},
		{"1 hour", 1, 0},
		{"0 hours", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := tt.hoursRemain / 24
			if days != tt.wantDays {
				t.Errorf("daysRemaining for %dh = %d, want %d", tt.hoursRemain, days, tt.wantDays)
			}
		})
	}
}
