package crypto

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashKeySHA256 returns SHA-256 hex digest of the key.
func HashKeySHA256(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
