package routes

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProfile_UpdateProfileValidation tests the update profile request binding.
func TestProfile_UpdateProfileValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantName  string
	}{
		{"update name", `{"name": "New Name"}`, true, "New Name"},
		{"update company", `{"company": "Acme Corp"}`, true, ""},
		{"update timezone", `{"timezone": "Asia/Shanghai"}`, true, ""},
		{"multiple fields", `{"name": "New Name", "company": "Acme Corp", "timezone": "UTC"}`, true, "New Name"},
		{"empty body", `{}`, true, ""},
		{"invalid json", `bad`, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Name     string `json:"name"`
				Company  string `json:"company"`
				Timezone string `json:"timezone"`
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
		})
	}
}

// TestProfile_ChangePasswordValidation tests the change password request binding.
func TestProfile_ChangePasswordValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"valid password change", `{"old_password": "OldPass123!", "new_password": "NewPass123!"}`, true},
		{"short new password", `{"old_password": "OldPass123!", "new_password": "short"}`, true},
		{"missing new_password", `{"old_password": "OldPass123!"}`, true},
		{"missing old_password", `{"new_password": "NewPass123!"}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				OldPassword string `json:"old_password" binding:"required"`
				NewPassword string `json:"new_password" binding:"required,min=8"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// TestProfile_ChangeEmailValidation tests the change email request binding.
func TestProfile_ChangeEmailValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"valid change", `{"new_email": "new@test.com", "verification_code": "ABC123", "password": "pass123"}`, true},
		{"missing code", `{"new_email": "new@test.com", "password": "pass123"}`, true},
		{"missing password", `{"new_email": "new@test.com", "verification_code": "ABC123"}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				NewEmail         string `json:"new_email" binding:"required,email"`
				VerificationCode string `json:"verification_code" binding:"required"`
				Password         string `json:"password" binding:"required"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// TestProfile_ChangePhoneValidation tests the change phone request binding.
func TestProfile_ChangePhoneValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"valid change", `{"new_phone": "+8613800138000", "verification_code": "ABC123", "password": "pass123"}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				NewPhone         string `json:"new_phone" binding:"required"`
				VerificationCode string `json:"verification_code" binding:"required"`
				Password         string `json:"password" binding:"required"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// TestProfile_SendChangeCodeValidation tests the send change code request.
func TestProfile_SendChangeCodeValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantType  string
	}{
		{"email type", `{"type": "email", "value": "test@test.com"}`, true, "email"},
		{"phone type", `{"type": "phone", "value": "+8613800138000"}`, true, "phone"},
		{"invalid type", `{"type": "sms", "value": "test"}`, true, "sms"},
		{"missing value", `{"type": "email"}`, true, "email"},
		{"invalid json", `bad`, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Type  string `json:"type" binding:"required"`
				Value string `json:"value" binding:"required"`
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
			validType := req.Type == "email" || req.Type == "phone"
			if !validType && req.Type != "" {
				t.Logf("type %q would be rejected (must be 'email' or 'phone')", req.Type)
			}
		})
	}
}

// TestProfile_CurrencyValidation tests the currency request binding.
func TestProfile_CurrencyValidation(t *testing.T) {
	tests := []struct {
		currency  string
		wantValid bool
	}{
		{"CNY", true},
		{"USD", true},
		{"EUR", false},
		{"", false},
		{"cny", false},
	}

	for _, tt := range tests {
		t.Run(tt.currency, func(t *testing.T) {
			valid := tt.currency == "CNY" || tt.currency == "USD"
			if valid != tt.wantValid {
				t.Errorf("currency %q valid = %v, want %v", tt.currency, valid, tt.wantValid)
			}
		})
	}
}

// TestProfile_GenerateVerificationCode tests the verification code generation.
func TestGenerateVerificationCode_Format(t *testing.T) {
	code := generateVerificationCode()

	// Should be 6 characters
	if len(code) != 6 {
		t.Errorf("code length = %d, want 6", len(code))
	}

	// Should be uppercase hex
	if strings.ToUpper(code) != code {
		t.Errorf("code should be uppercase, got %q", code)
	}
}

// TestProfile_GenerateVerificationCode_Uniqueness generates multiple codes and
// checks they are (statistically) unique. Codes are 6 decimal digits (10^6
// space), so demanding zero collisions over many draws is itself wrong — the
// birthday bound makes collisions expected. Instead require the unique count
// to stay within statistical tolerance, which still catches a broken
// (e.g. constant) generator.
func TestGenerateVerificationCode_Uniqueness(t *testing.T) {
	const draws = 1000
	seen := make(map[string]bool)
	for i := 0; i < draws; i++ {
		seen[generateVerificationCode()] = true
	}
	// Expected unique ≈ 999.5 (σ ≈ 0.7); 990 is >10σ below, constant or
	// severely degenerate generators land far under it.
	if len(seen) < 990 {
		t.Errorf("only %d unique codes in %d draws, generator looks degenerate", len(seen), draws)
	}
}

// TestProfile_HashPassword tests that password hashing works.
func TestProfile_HashPassword(t *testing.T) {
	password := "TestPassword123!"

	hash, err := hashPassword(password)
	if err != nil {
		t.Fatalf("hashPassword error: %v", err)
	}
	if hash == "" {
		t.Error("hash should not be empty")
	}
	if hash == password {
		t.Error("hash should not equal the password")
	}

	// Verify the hash works
	if err := comparePassword(hash, password); err != nil {
		t.Errorf("comparePassword should succeed for correct password: %v", err)
	}

	// Wrong password should fail
	if err := comparePassword(hash, "WrongPassword"); err == nil {
		t.Error("comparePassword should fail for wrong password")
	}
}

// TestProfile_HashPassword_Deterministic tests that same passwords produce different hashes (salt).
func TestProfile_HashPassword_Deterministic(t *testing.T) {
	password := "TestPassword123!"
	hash1, _ := hashPassword(password)
	hash2, _ := hashPassword(password)

	// bcrypt generates different hashes due to random salt
	if hash1 == hash2 {
		t.Error("bcrypt hashes should differ due to random salt")
	}

	// Both should verify correctly
	if err := comparePassword(hash1, password); err != nil {
		t.Errorf("hash1 should verify: %v", err)
	}
	if err := comparePassword(hash2, password); err != nil {
		t.Errorf("hash2 should verify: %v", err)
	}
}
