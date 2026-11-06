package keypool

import (
	"testing"
)

// TestParseKeyType - Verify key type parsing per V3 spec
func TestParseKeyType(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		wantType string
		wantErr  bool
	}{
		{
			name:     "general key",
			key:      "sk-wgit-a1b2c3d4e5f6",
			wantType: "general",
			wantErr:  false,
		},
		{
			name:     "subscription key (token plan)",
			key:      "sk-wgit-tp-x9y8z7w6v5u4",
			wantType: "subscription",
			wantErr:  false,
		},
		{
			name:     "enterprise key",
			key:      "sk-wgit-en-corp01-xyz789",
			wantType: "enterprise",
			wantErr:  false,
		},
		{
			name:     "invalid prefix",
			key:      "sk-invalid-key",
			wantType: "",
			wantErr:  true,
		},
		{
			name:     "empty key",
			key:      "",
			wantType: "",
			wantErr:  true,
		},
		{
			name:     "just prefix",
			key:      "sk-wgit-",
			wantType: "general",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyType, err := ParseKeyType(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseKeyType() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if keyType != tt.wantType {
				t.Errorf("ParseKeyType() = %v, want %v", keyType, tt.wantType)
			}
		})
	}
}

// TestParseKeyTypeConsistency - Verify key type is consistent
func TestParseKeyTypeConsistency(t *testing.T) {
	// Same key should always return same type
	key := "sk-wgit-tp-test123"
	type1, _ := ParseKeyType(key)
	type2, _ := ParseKeyType(key)
	if type1 != type2 {
		t.Error("ParseKeyType should be deterministic")
	}
}

// TestKeyFormat - Verify key format validation
func TestKeyFormat(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{"valid general", "sk-wgit-a1b2c3d4", true},
		{"valid subscription", "sk-wgit-tp-a1b2c3d4", true},
		{"valid enterprise", "sk-wgit-en-a1b2c3d4", true},
		{"invalid - no sk prefix", "wk-wgit-a1b2c3d4", false},
		{"invalid - wrong prefix", "sk-other-a1b2c3d4", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := IsValidKeyFormat(tt.key)
			if valid != tt.valid {
				t.Errorf("IsValidKeyFormat(%s) = %v, want %v", tt.key, valid, tt.valid)
			}
		})
	}
}

// TestGetKeyPriority - Verify priority mapping per V3 spec
func TestGetKeyPriority(t *testing.T) {
	tests := []struct {
		accessMode string
		wantPri    string
	}{
		{"enterprise", "high"},
		{"subscription", "high"},
		{"token_plan", "high"},
		{"single_model", "high"},
		{"economy", "economy"},
		{"general", "normal"},
		{"", "normal"},
		{"unknown", "normal"},
	}

	for _, tt := range tests {
		t.Run(tt.accessMode, func(t *testing.T) {
			pri := GetKeyPriority(tt.accessMode)
			if pri != tt.wantPri {
				t.Errorf("GetKeyPriority(%s) = %s, want %s", tt.accessMode, pri, tt.wantPri)
			}
		})
	}
}
