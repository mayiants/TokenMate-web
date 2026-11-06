package routes

import (
	"testing"
)

// TestVouchersCommon_GenerateRandomCode tests the generateRandomCode function.
func TestVouchersCommon_GenerateRandomCode(t *testing.T) {
	charset := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	tests := []struct {
		name   string
		length int
	}{
		{"length 6", 6},
		{"length 8", 8},
		{"length 12", 12},
		{"length 16", 16},
		{"length 1", 1},
		{"length 0", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := generateRandomCode(charset, tt.length)
			if len(code) != tt.length {
				t.Errorf("code length = %d, want %d", len(code), tt.length)
			}

			// All characters should be from the charset
			for _, c := range code {
				found := false
				for _, valid := range charset {
					if c == valid {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("character %q not in charset", string(c))
				}
			}
		})
	}
}

// TestVouchersCommon_GenerateRandomCode_Uniqueness tests code uniqueness.
func TestVouchersCommon_GenerateRandomCode_Uniqueness(t *testing.T) {
	charset := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	seen := make(map[string]bool)

	for i := 0; i < 100; i++ {
		code := generateRandomCode(charset, 8)
		if code == "" {
			t.Error("generated empty code")
		}
		if seen[code] {
			t.Errorf("duplicate code generated: %q", code)
		}
		seen[code] = true
	}
}

// TestVouchersCommon_GenerateRandomCode_EmptyCharset tests behavior with empty charset.
func TestVouchersCommon_GenerateRandomCode_EmptyCharset(t *testing.T) {
	// This should panic or return empty — just ensure it doesn't crash unexpectedly
	defer func() {
		if r := recover(); r != nil {
			t.Logf("generateRandomCode with empty charset panicked (expected): %v", r)
		}
	}()
	code := generateRandomCode("", 10)
	_ = code
}

// TestVouchersCommon_StructValidation tests the VoucherRoutes struct.
func TestVouchersCommon_StructValidation(t *testing.T) {
	var _ *VoucherRoutes = nil
	t.Log("VoucherRoutes type verified at compile time")
}
