package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashKeySHA256(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		empty bool
	}{
		{"simple string", "hello", false},
		{"empty string", "", false},
		{"long key", "sk-proj-abc123def456ghi789jkl012mno345pqr678stu901vwx234yz", false},
		{"unicode", "密钥测试-key-test-日本語", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := HashKeySHA256(tt.key)
			// Should be 64 hex chars (32 bytes)
			assert.Len(t, hash, 64, "SHA-256 hex digest should be 64 characters")
		})
	}
}

func TestHashKeySHA256_Deterministic(t *testing.T) {
	key := "deterministic-test-key"
	hash1 := HashKeySHA256(key)
	hash2 := HashKeySHA256(key)
	assert.Equal(t, hash1, hash2, "same input should produce same hash")
}

func TestHashKeySHA256_Unique(t *testing.T) {
	hash1 := HashKeySHA256("key-one")
	hash2 := HashKeySHA256("key-two")
	assert.NotEqual(t, hash1, hash2, "different inputs should produce different hashes")
}

func TestHashKeySHA256_KnownValue(t *testing.T) {
	hash := HashKeySHA256("test")
	// SHA-256 of "test" = 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
	assert.Equal(t, "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", hash)
}
