package web

import (
	"testing"
)

// TestRandomString tests the random alphanumeric string generator.
func TestRandomString(t *testing.T) {
	tests := []struct {
		length int
	}{
		{8},
		{16},
		{32},
		{1},
		{64},
	}

	for _, tt := range tests {
		t.Run("length", func(t *testing.T) {
			s := randomString(tt.length)
			if len(s) != tt.length {
				t.Errorf("randomString(%d) length = %d, want %d", tt.length, len(s), tt.length)
			}
			// All characters should be alphanumeric
			for _, c := range s {
				if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
					t.Errorf("randomString(%d) contains invalid character: %c", tt.length, c)
				}
			}
		})
	}
}

// TestRandomStringUniqueness verifies strings are unique.
func TestRandomStringUniqueness(t *testing.T) {
	s1 := randomString(32)
	s2 := randomString(32)
	if s1 == s2 {
		t.Error("randomString(32) produced identical strings")
	}
}
