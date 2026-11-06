package crypto

import (
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	key := "this-is-a-32-byte-encryption-key"
	plaintext := "sk-openai-api-key-1234567890abcdef"

	encrypted, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if encrypted == plaintext {
		t.Error("Encrypted text should not equal plaintext")
	}

	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("Decrypted = %v, want %v", decrypted, plaintext)
	}
}

func TestEncryptDifferentOutputs(t *testing.T) {
	key := "this-is-a-32-byte-encryption-key"
	plaintext := "same-plaintext"

	enc1, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt 1 failed: %v", err)
	}

	enc2, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt 2 failed: %v", err)
	}

	// Different nonces should produce different ciphertext
	if enc1 == enc2 {
		t.Error("Same plaintext should produce different ciphertext due to random nonce")
	}

	// Both should decrypt to same plaintext
	dec1, _ := Decrypt(enc1, key)
	dec2, _ := Decrypt(enc2, key)
	if dec1 != dec2 {
		t.Error("Both should decrypt to same plaintext")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := "this-is-the-correct-32byte-key!!"
	key2 := "this-is-the-wrong-32byte-key!!!!"
	plaintext := "secret-data"

	encrypted, _ := Encrypt(plaintext, key1)

	_, err := Decrypt(encrypted, key2)
	if err == nil {
		t.Error("Decrypt with wrong key should fail")
	}
}

func TestEmptyKey(t *testing.T) {
	_, err := Encrypt("test", "")
	if err != ErrEmptyKey {
		t.Errorf("Encrypt with empty key should return ErrEmptyKey, got %v", err)
	}

	_, err = Decrypt("test", "")
	if err != ErrEmptyKey {
		t.Errorf("Decrypt with empty key should return ErrEmptyKey, got %v", err)
	}
}

func TestShortKeyPadding(t *testing.T) {
	shortKey := "short"

	_, err := Encrypt("test-data", shortKey)
	if err != ErrInvalidKey {
		t.Errorf("Encrypt with short key should return ErrInvalidKey, got %v", err)
	}

	_, err = Decrypt("test-data", shortKey)
	if err != ErrInvalidKey {
		t.Errorf("Decrypt with short key should return ErrInvalidKey, got %v", err)
	}
}

func TestGenerateKey(t *testing.T) {
	key1, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	key2, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	if key1 == key2 {
		t.Error("Generated keys should be unique")
	}

	// Should be 64 hex characters (32 bytes)
	if len(key1) != 64 {
		t.Errorf("Generated key length = %d, want 64", len(key1))
	}
}

func TestEncryptDecryptRealAPIKeys(t *testing.T) {
	testKeys := []string{
		"sk-proj-abc123def456ghi789jkl012mno345pqr678stu901vwx234yz",
		"sk-ant-api03-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		"sk-wgit-tp-test123456789",
		"sk-wgit-en-corp01-xyz789",
	}

	key := "this-is-the-master-32byte-key!!!"
	for _, plaintext := range testKeys {
		encrypted, err := Encrypt(plaintext, key)
		if err != nil {
			t.Fatalf("Encrypt failed for %s: %v", plaintext[:10], err)
		}

		decrypted, err := Decrypt(encrypted, key)
		if err != nil {
			t.Fatalf("Decrypt failed for %s: %v", plaintext[:10], err)
		}

		if decrypted != plaintext {
			t.Errorf("Decrypted mismatch for key %s", plaintext[:10])
		}
	}
}
