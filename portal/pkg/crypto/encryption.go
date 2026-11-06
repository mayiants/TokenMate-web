package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

var (
	ErrEmptyKey        = errors.New("encryption key is empty")
	ErrInvalidKey      = errors.New("encryption key must be at least 8 characters")
	ErrCiphertextShort = errors.New("ciphertext too short")
)

// Encrypt encrypts plaintext using AES-256-GCM.
// The passphrase is hashed with SHA-256 (unsalted, single round) to derive the
// 32-byte AES key. This is NOT a proper KDF (no salt, no stretching) and is
// acceptable only for low-risk secrets at rest; for anything more sensitive use
// a salted KDF such as scrypt or Argon2.
// Returns hex-encoded ciphertext with nonce prepended.
func Encrypt(plaintext, encryptionKey string) (string, error) {
	if encryptionKey == "" {
		return "", ErrEmptyKey
	}
	if len(encryptionKey) < 8 {
		return "", ErrInvalidKey
	}

	key := sha256.Sum256([]byte(encryptionKey))

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts hex-encoded ciphertext using AES-256-GCM
// Expects nonce to be prepended to ciphertext
func Decrypt(encrypted, encryptionKey string) (string, error) {
	if encryptionKey == "" {
		return "", ErrEmptyKey
	}
	if len(encryptionKey) < 8 {
		return "", ErrInvalidKey
	}

	key := sha256.Sum256([]byte(encryptionKey))

	ciphertext, err := hex.DecodeString(encrypted)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", ErrCiphertextShort
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// GenerateKey generates a random 32-byte key for encryption
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}
