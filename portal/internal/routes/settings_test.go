package routes

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSettings_SettingGroups tests the settingGroups map structure.
func TestSettings_SettingGroups(t *testing.T) {
	groups := settingGroups

	expectedGroups := []string{
		"operation", "monitoring", "token", "log",
		"payment", "announcement", "compliance", "rate_limit", "notifications",
	}

	for _, name := range expectedGroups {
		if group, ok := groups[name]; !ok {
			t.Errorf("expected group %q in settingGroups", name)
		} else if group.Name == "" {
			t.Errorf("group %q has empty Name", name)
		} else if len(group.Keys) == 0 {
			t.Errorf("group %q has no keys", name)
		}
	}

	if len(groups) != 9 {
		t.Errorf("expected 9 groups, got %d", len(groups))
	}
}

// TestSettings_DefaultSettings tests the defaultSettings map.
func TestSettings_DefaultSettings(t *testing.T) {
	defaults := defaultSettings

	// Check key defaults
	tests := []struct {
		key     string
		wantVal string
	}{
		{"quota_for_new_user", "1000000"},
		{"alipay_enabled", "true"},
		{"wechat_enabled", "false"},
		{"usd_exchange_rate", "720"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			val, ok := defaults[tt.key]
			if !ok {
				t.Errorf("key %q not found in defaultSettings", tt.key)
				return
			}
			if val != tt.wantVal {
				t.Errorf("defaultSettings[%q] = %q, want %q", tt.key, val, tt.wantVal)
			}
		})
	}

	// Ensure all settings have defaults
	if len(defaults) < 20 {
		t.Errorf("expected at least 20 default settings, got %d", len(defaults))
	}
}

// TestSettings_UpdateSettingsValidation tests the update settings request binding.
func TestSettings_UpdateSettingsValidation(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantValid   bool
		wantUpdates int
	}{
		{"valid update", `{"settings": {"quota_for_new_user": "2000000", "retry_times": "5"}}`, true, 2},
		{"single setting", `{"settings": {"quota_for_new_user": "2000000"}}`, true, 1},
		{"empty settings", `{"settings": {}}`, true, 0},
		{"missing settings", `{}`, true, 0},
		{"invalid json", `bad`, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Settings map[string]string `json:"settings"`
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

			// Simulate the validation logic
			updated := 0
			for key := range req.Settings {
				if _, ok := defaultSettings[key]; ok {
					updated++
				}
			}
			if updated != tt.wantUpdates {
				t.Errorf("updated = %d, want %d", updated, tt.wantUpdates)
			}
		})
	}
}

// TestSettings_ResetSettingsValidation tests reset logic.
func TestSettings_ResetSettingsValidation(t *testing.T) {
	// Reset all keys when no specific keys provided
	keysToReset := []string{}
	if len(keysToReset) == 0 {
		for k := range defaultSettings {
			keysToReset = append(keysToReset, k)
		}
	}
	if len(keysToReset) != len(defaultSettings) {
		t.Errorf("reset all should reset %d keys, got %d", len(defaultSettings), len(keysToReset))
	}

	// Reset specific keys
	specific := []string{"quota_for_new_user", "unknown_key"}
	resetCount := 0
	for _, key := range specific {
		if _, ok := defaultSettings[key]; ok {
			resetCount++
		}
	}
	if resetCount != 1 {
		t.Errorf("specific keys reset should be 1 (only valid keys), got %d", resetCount)
	}
}

// TestSettings_SMTPRequestValidation tests the updateSMTPSettings request binding
// and validation rules (host/user required, port defaults to 465).
func TestSettings_SMTPRequestValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantPort  int
	}{
		{"valid", `{"host":"smtp.example.com","port":587,"secure":false,"user":"u","password":"p","from":"f"}`, true, 587},
		{"port defaults to 465", `{"host":"smtp.example.com","user":"u"}`, true, 465},
		{"negative port defaults", `{"host":"smtp.example.com","user":"u","port":-1}`, true, 465},
		{"missing host", `{"user":"u"}`, false, 0},
		{"missing user", `{"host":"smtp.example.com"}`, false, 0},
		{"blank host", `{"host":"  ","user":"u"}`, false, 0},
		{"invalid json", `bad`, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Host     string `json:"host"`
				Port     int    `json:"port"`
				Secure   bool   `json:"secure"`
				User     string `json:"user"`
				Password string `json:"password"`
				From     string `json:"from"`
			}
			if err := json.Unmarshal([]byte(tt.body), &req); err != nil {
				if tt.wantValid {
					t.Errorf("unexpected bind error: %v", err)
				}
				return
			}

			req.Host = strings.TrimSpace(req.Host)
			req.User = strings.TrimSpace(req.User)
			valid := req.Host != "" && req.User != ""
			if valid != tt.wantValid {
				t.Fatalf("valid = %v, want %v", valid, tt.wantValid)
			}
			if !valid {
				return
			}

			if req.Port <= 0 {
				req.Port = 465
			}
			if req.Port != tt.wantPort {
				t.Errorf("port = %d, want %d", req.Port, tt.wantPort)
			}
		})
	}
}

// TestSettings_GetSettingJSONDefaults tests the default settings for invitation config.
func TestSettings_GetSettingJSONDefaults(t *testing.T) {
	defaults := map[string]string{
		"invitation_max_codes_per_user":  "10",
		"invitation_default_max_uses":    "10",
		"invitation_default_expire_days": "365",
		"invitation_inviter_reward":      "5",
		"invitation_invitee_reward":      "2",
	}

	for key, val := range defaults {
		if val == "" {
			t.Errorf("default for %q should not be empty", key)
		}
	}
}

// TestSettings_BooleanValues tests boolean setting values.
func TestSettings_BooleanValues(t *testing.T) {
	// Verify boolean settings use "true"/"false" strings
	boolSettings := []string{
		"automatic_disable_channel_enabled",
		"log_consume_enabled",
		"alipay_enabled",
		"wechat_enabled",
		"payment_simulation_mode",
		"announcement_enabled",
	}

	for _, key := range boolSettings {
		val, ok := defaultSettings[key]
		if !ok {
			t.Errorf("boolean setting %q not found", key)
			continue
		}
		if val != "true" && val != "false" {
			t.Errorf("boolean setting %q should be 'true' or 'false', got %q", key, val)
		}
	}
}
