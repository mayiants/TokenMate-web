package keypool

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/hkdf"
)

// redisKeySnapshot is a serializable snapshot of Key + ActualKey for Redis persistence
type redisKeySnapshot struct {
	KeyStatic
	KeyRuntime
	EncryptedActualKey string `json:"eak,omitempty"` // AES-256-GCM encrypted actual key
}

// redisPoolSnapshot is a serializable snapshot of ProviderPool for Redis persistence
type redisPoolSnapshot struct {
	Provider      string              `json:"provider"`
	Keys          []*redisKeySnapshot `json:"keys"`
	TotalKeys     int                 `json:"total_keys"`
	ActiveKeys    int                 `json:"active_keys"`
	TotalRequests int64               `json:"total_requests"`
	TotalTokens   int64               `json:"total_tokens"`
}

// persistToRedis saves all keys to Redis using serializable snapshots.
// ActualKey values are AES-256-GCM encrypted before storage.
// Each provider is stored under its own Redis key to avoid a single monolithic blob.
func (m *Manager) persistToRedis(ctx context.Context) error {
	if m.redis == nil {
		return nil
	}

	m.poolsMu.RLock()
	providers := make([]string, 0, len(m.pools))
	for k := range m.pools {
		providers = append(providers, k)
	}
	m.poolsMu.RUnlock()

	snaps := make(map[string]*redisPoolSnapshot, len(providers))
	for _, provider := range providers {
		lock := m.getLock(provider)
		lock.RLock()
		pool, ok := m.getPool(provider)
		if !ok {
			lock.RUnlock()
			continue
		}

		snap := &redisPoolSnapshot{
			Provider:      pool.Provider,
			TotalKeys:     pool.TotalKeys,
			ActiveKeys:    pool.ActiveKeys,
			TotalRequests: pool.TotalRequests,
			TotalTokens:   pool.TotalTokens,
			Keys:          make([]*redisKeySnapshot, 0, len(pool.internalKeys)),
		}
		for _, ik := range pool.internalKeys {
			rks := &redisKeySnapshot{
				KeyStatic:  ik.KeyStatic,
				KeyRuntime: ik.KeyRuntime,
			}
			// Encrypt actual key if encryption key is available
			if m.encryptionKey != "" && ik.ActualKey != "" {
				enc, err := EncryptKey(ik.ActualKey, m.encryptionKey)
				if err != nil {
					lock.RUnlock()
					return fmt.Errorf("encrypt key %s: %w", ik.KeyPreview, err)
				}
				rks.EncryptedActualKey = enc
			} else if ik.ActualKey != "" {
				// No encryption key configured — refuse to persist plaintext.
				lock.RUnlock()
				return fmt.Errorf("cannot persist key %s: encryption key is not configured", ik.KeyPreview)
			}
			snap.Keys = append(snap.Keys, rks)
		}
		lock.RUnlock()
		snaps[provider] = snap
	}

	providerListJSON, err := json.Marshal(providers)
	if err != nil {
		return fmt.Errorf("marshal provider list: %w", err)
	}

	metaJSON, err := json.Marshal(map[string]string{
		"config_version": m.GetConfigVersion(),
	})
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}

	// Write all provider snapshots and the provider list atomically via MULTI/EXEC.
	_, err = m.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, m.redisKey+":providers", providerListJSON, 0)
		pipe.Set(ctx, m.redisKey+":meta", metaJSON, 0)
		for provider, snap := range snaps {
			jsonData, err := json.Marshal(snap)
			if err != nil {
				return fmt.Errorf("marshal provider %s: %w", provider, err)
			}
			pipe.Set(ctx, m.redisKey+":provider:"+provider, jsonData, 0)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("pipeline write to redis: %w", err)
	}
	return nil
}

// loadFromRedis loads keys from Redis and reconstructs ProviderPool with KeyInternal.
// Supports both the new per-provider format and the legacy monolithic blob format.
func (m *Manager) loadFromRedis(ctx context.Context) error {
	if m.redis == nil {
		return nil
	}

	providerListData, err := m.redis.Get(ctx, m.redisKey+":providers").Bytes()
	if err != nil {
		if err == redis.Nil {
			// Fallback to legacy single-blob format
			return m.loadFromRedisLegacyBlob(ctx)
		}
		return fmt.Errorf("get provider list from redis: %w", err)
	}

	if metaData, err := m.redis.Get(ctx, m.redisKey+":meta").Bytes(); err == nil {
		var meta struct {
			ConfigVersion string `json:"config_version"`
		}
		if json.Unmarshal(metaData, &meta) == nil {
			m.setConfigVersion(meta.ConfigVersion)
		}
	}

	var providers []string
	if err := json.Unmarshal(providerListData, &providers); err != nil {
		return fmt.Errorf("unmarshal provider list: %w", err)
	}

	m.poolsMu.Lock()
	for _, provider := range providers {
		data, err := m.redis.Get(ctx, m.redisKey+":provider:"+provider).Bytes()
		if err != nil {
			if err == redis.Nil {
				m.logger.Warn("Provider snapshot missing in Redis", zap.String("provider", provider))
				continue
			}
			m.logger.Warn("Failed to load provider snapshot from Redis", zap.String("provider", provider), zap.Error(err))
			continue
		}

		var snap redisPoolSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			m.logger.Warn("Failed to unmarshal provider snapshot", zap.String("provider", provider), zap.Error(err))
			continue
		}
		m.loadPoolSnapshot(&snap)
	}
	m.poolsMu.Unlock()

	return nil
}

// loadFromRedisLegacyBlob loads keys from the old single-blob Redis format.
// Kept for backward compatibility during upgrades.
func (m *Manager) loadFromRedisLegacyBlob(ctx context.Context) error {
	data, err := m.redis.Get(ctx, m.redisKey).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil // No cached data
		}
		return fmt.Errorf("get legacy blob from redis: %w", err)
	}

	var snapshots map[string]*redisPoolSnapshot
	if err := json.Unmarshal(data, &snapshots); err != nil {
		return fmt.Errorf("unmarshal legacy keys: %w", err)
	}

	m.poolsMu.Lock()
	for _, snap := range snapshots {
		m.loadPoolSnapshot(snap)
	}
	m.poolsMu.Unlock()

	m.logger.Info("Loaded keys from legacy Redis blob format")
	return nil
}

// loadPoolSnapshot loads a single provider snapshot into m.pools.
// Caller must hold m.poolsMu write lock.
func (m *Manager) loadPoolSnapshot(snap *redisPoolSnapshot) {
	pool := &ProviderPool{
		Provider:      snap.Provider,
		Keys:          make([]*Key, 0, len(snap.Keys)),
		internalKeys:  make([]*KeyInternal, 0, len(snap.Keys)),
		TotalKeys:     snap.TotalKeys,
		ActiveKeys:    snap.ActiveKeys,
		TotalRequests: snap.TotalRequests,
		TotalTokens:   snap.TotalTokens,
	}
	for _, rks := range snap.Keys {
		key := &Key{
			KeyStatic:  rks.KeyStatic,
			KeyRuntime: rks.KeyRuntime,
		}
		pool.Keys = append(pool.Keys, key)

		// Decrypt actual key
		actualKey := rks.EncryptedActualKey
		if actualKey != "" {
			if m.encryptionKey == "" {
				m.logger.Warn("Cannot decrypt actual key from Redis: encryption key not configured, skipping",
					zap.String("key_id", key.ID))
				continue
			}
			dec, err := DecryptKey(actualKey, m.encryptionKey)
			if err == nil {
				actualKey = dec
			} else {
				m.logger.Warn("Failed to decrypt actual key from Redis, skipping",
					zap.String("key_id", key.ID),
					zap.Error(err))
				continue
			}
		}
		pool.internalKeys = append(pool.internalKeys, newKeyInternal(key, actualKey))
	}
	m.pools[snap.Provider] = pool
	// Ensure lock exists
	m.getLock(snap.Provider)
}

// deriveAESKey derives a 32-byte AES key from the configured encryptionKey using HKDF-SHA256.
func deriveAESKey(encryptionKey string) []byte {
	hkdfReader := hkdf.New(sha256.New, []byte(encryptionKey), nil, []byte("ai-proxy-keypool-v1"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		// HKDF cannot fail with valid parameters in practice; fall back to SHA-256 hash.
		h := sha256.Sum256([]byte(encryptionKey))
		copy(key, h[:])
	}
	return key
}

// deriveAESKeyLegacy derives a key using the old padding/truncation method.
// Kept for backward compatibility when decrypting previously persisted Redis data.
func deriveAESKeyLegacy(encryptionKey string) []byte {
	key := []byte(encryptionKey)
	if len(key) < 32 {
		padded := make([]byte, 32)
		copy(padded, key)
		key = padded
	} else if len(key) > 32 {
		key = key[:32]
	}
	return key
}

// encryptAESGCM encrypts plaintext with the provided AES key.
func encryptAESGCM(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// decryptAESGCM decrypts ciphertext with the provided AES key.
func decryptAESGCM(encrypted string, key []byte) (string, error) {
	ciphertext, err := hex.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("decode hex: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}

// EncryptKey encrypts a key using AES-256-GCM with HKDF-SHA256 key derivation.
func EncryptKey(plaintext, encryptionKey string) (string, error) {
	key := deriveAESKey(encryptionKey)
	return encryptAESGCM(plaintext, key)
}

// DecryptKey decrypts a key using AES-256-GCM.
// Tries HKDF-SHA256 first, then falls back to the legacy padding/truncation method
// for data encrypted before the HKDF migration.
func DecryptKey(encrypted, encryptionKey string) (string, error) {
	plaintext, err := decryptAESGCM(encrypted, deriveAESKey(encryptionKey))
	if err == nil {
		return plaintext, nil
	}
	// Fallback to legacy key derivation for backward compatibility.
	return decryptAESGCM(encrypted, deriveAESKeyLegacy(encryptionKey))
}
