package keypool

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"go.uber.org/zap"
)

func TestNewManager(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-testing-only-32chars"},
		Portal: config.PortalConfig{
			URL:          "http://localhost:3001",
			SyncSecret:   "test-secret",
			SyncInterval: 60 * time.Second,
		},
	}

	logger, _ := zap.NewDevelopment()
	m := NewManager(cfg, logger)

	if m == nil {
		t.Fatal("NewManager returned nil")
	}

	if m.portalURL != cfg.Portal.URL {
		t.Errorf("portalURL = %s, want %s", m.portalURL, cfg.Portal.URL)
	}
}

func TestGetLock(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-testing-only-32chars"}}
	logger, _ := zap.NewDevelopment()
	m := NewManager(cfg, logger)

	// Get lock for same provider should return same lock
	lock1 := m.getLock("openai")
	lock2 := m.getLock("openai")

	if lock1 != lock2 {
		t.Error("getLock should return same lock for same provider")
	}

	// Get lock for different provider should return different lock
	lock3 := m.getLock("anthropic")

	if lock1 == lock3 {
		t.Error("getLock should return different locks for different providers")
	}
}

func TestAddKey(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-testing-only-32chars"}}
	logger, _ := zap.NewDevelopment()
	m := NewManager(cfg, logger)

	key := &Key{
		KeyStatic: KeyStatic{
			ID:         "test-key-1",
			Provider:   "openai",
			Key:        "sk-test-key",
			KeyPreview: "sk-test-...key",
			Tier:       "standard",
		},
	}

	err := m.AddKey(key)
	if err != nil {
		t.Fatalf("AddKey failed: %v", err)
	}

	// Verify key was added
	retrieved := m.GetKey("openai")
	if retrieved == nil {
		t.Fatal("GetKey returned nil after AddKey")
	}

	if retrieved.ID != key.ID {
		t.Errorf("retrieved key ID = %s, want %s", retrieved.ID, key.ID)
	}
}

func TestGetKeyLRU(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-testing-only-32chars"}}
	logger, _ := zap.NewDevelopment()
	m := NewManager(cfg, logger)

	// Add multiple keys
	for i := 1; i <= 3; i++ {
		key := &Key{
			KeyStatic: KeyStatic{
				ID:         fmt.Sprintf("key-%d", i),
				Provider:   "openai",
				Key:        fmt.Sprintf("sk-key-%d", i),
				KeyPreview: fmt.Sprintf("sk-key-%d...", i),
				Tier:       "standard",
			},
		}
		m.AddKey(key)
		time.Sleep(10 * time.Millisecond) // Ensure different LastUsed times
	}

	// Get key should return least recently used
	k1 := m.GetKey("openai")
	if k1 == nil {
		t.Fatal("GetKey returned nil")
	}

	// First key should be returned (oldest)
	if k1.ID != "key-1" {
		t.Errorf("GetKey returned %s, want key-1", k1.ID)
	}

	// After using key-1, next call should return key-2
	k2 := m.GetKey("openai")
	if k2.ID != "key-2" {
		t.Errorf("GetKey returned %s, want key-2", k2.ID)
	}
}

func TestToggleKey(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-testing-only-32chars"}}
	logger, _ := zap.NewDevelopment()
	m := NewManager(cfg, logger)

	key := &Key{
		KeyStatic: KeyStatic{
			ID:         "test-key",
			Provider:   "openai",
			Key:        "sk-test",
			KeyPreview: "sk-test...",
			Tier:       "standard",
		},
	}
	m.AddKey(key)

	// Disable key
	result := m.ToggleKey("openai", "test-key", false)
	if !result {
		t.Fatal("ToggleKey returned false")
	}

	// Verify key is disabled
	pool, ok := m.getPool("openai")
	if !ok {
		t.Fatal("pool not found")
	}

	lock := m.getLock("openai")
	lock.RLock()
	for _, k := range pool.Keys {
		if k.ID == "test-key" {
			if k.Enabled {
				t.Error("key should be disabled")
			}
			if k.Status != "inactive" {
				t.Errorf("status = %s, want inactive", k.Status)
			}
		}
	}
	lock.RUnlock()

	// Verify GetKey doesn't return disabled key
	retrieved := m.GetKey("openai")
	if retrieved != nil {
		t.Error("GetKey should return nil for disabled key pool")
	}
}

func TestRemoveKey(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-testing-only-32chars"}}
	logger, _ := zap.NewDevelopment()
	m := NewManager(cfg, logger)

	key := &Key{
		KeyStatic: KeyStatic{
			ID:         "test-key",
			Provider:   "openai",
			Key:        "sk-test",
			KeyPreview: "sk-test...",
			Tier:       "standard",
		},
	}
	m.AddKey(key)

	// Remove key
	result := m.RemoveKey("openai", "test-key")
	if !result {
		t.Fatal("RemoveKey returned false")
	}

	// Verify key is removed
	retrieved := m.GetKey("openai")
	if retrieved != nil {
		t.Error("GetKey should return nil after RemoveKey")
	}
}

func TestGetTierLimits(t *testing.T) {
	tests := []struct {
		tier    string
		wantRPM int
		wantTPM int
	}{
		{"premium", 600, 3000000},
		{"budget", 60, 1500000},
		{"standard", 300, 1500000},
		{"unknown", 300, 1500000},
		{"enterprise", 1200, 10000000},
	}

	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			rpm, tpm := getTierLimits(tt.tier)
			if rpm != tt.wantRPM {
				t.Errorf("getTierLimits(%s) rpm = %d, want %d", tt.tier, rpm, tt.wantRPM)
			}
			if tpm != tt.wantTPM {
				t.Errorf("getTierLimits(%s) tpm = %d, want %d", tt.tier, tpm, tt.wantTPM)
			}
		})
	}
}

func TestEncryptDecryptKey(t *testing.T) {
	encryptionKey := "test-encryption-key-32-bytes-long"
	plaintext := "sk-my-secret-api-key-12345"

	// Test encryption
	encrypted, err := EncryptKey(plaintext, encryptionKey)
	if err != nil {
		t.Fatalf("EncryptKey failed: %v", err)
	}

	if encrypted == plaintext {
		t.Error("encrypted text should not equal plaintext")
	}

	// Test decryption
	decrypted, err := DecryptKey(encrypted, encryptionKey)
	if err != nil {
		t.Fatalf("DecryptKey failed: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("decrypted = %s, want %s", decrypted, plaintext)
	}
}

func TestEncryptKeyDifferentOutputs(t *testing.T) {
	encryptionKey := "test-encryption-key-32-bytes-long"
	plaintext := "sk-my-secret-api-key"

	// Same plaintext should produce different ciphertext (due to random nonce)
	encrypted1, _ := EncryptKey(plaintext, encryptionKey)
	encrypted2, _ := EncryptKey(plaintext, encryptionKey)

	if encrypted1 == encrypted2 {
		t.Error("same plaintext should produce different ciphertext due to random nonce")
	}

	// But both should decrypt to same plaintext
	decrypted1, _ := DecryptKey(encrypted1, encryptionKey)
	decrypted2, _ := DecryptKey(encrypted2, encryptionKey)

	if decrypted1 != decrypted2 {
		t.Error("different ciphertexts should decrypt to same plaintext")
	}
}

func TestDecryptKeyWrongKey(t *testing.T) {
	encryptionKey := "test-encryption-key-32-bytes-long"
	wrongKey := "wrong-encryption-key-32-bytes-lon"
	plaintext := "sk-my-secret-api-key"

	encrypted, _ := EncryptKey(plaintext, encryptionKey)

	_, err := DecryptKey(encrypted, wrongKey)
	if err == nil {
		t.Error("DecryptKey should fail with wrong key")
	}
}

func TestConcurrentAccess(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-for-testing-only-32chars"}}
	logger, _ := zap.NewDevelopment()
	m := NewManager(cfg, logger)

	// Add initial keys
	for i := 0; i < 10; i++ {
		key := &Key{
			KeyStatic: KeyStatic{
				ID:         fmt.Sprintf("key-%d", i),
				Provider:   "openai",
				Key:        fmt.Sprintf("sk-key-%d", i),
				KeyPreview: fmt.Sprintf("sk-key-%d...", i),
				Tier:       "standard",
			},
		}
		m.AddKey(key)
	}

	// Concurrent reads and writes
	var wg sync.WaitGroup
	errors := make(chan error, 100)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				// Read
				_ = m.GetKey("openai")

				// Write
				m.UpdateKeyUsage("openai", fmt.Sprintf("key-%d", idx%10), 100, true)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent access error: %v", err)
	}
}
