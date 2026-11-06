package keypool

import (
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ModelHealth tracks per-model availability on a per-key basis.
// When a specific model repeatedly fails on a key (e.g. upstream returns 401
// "model not supported"), the model is temporarily disabled for that key without
// affecting other models on the same key.
type ModelHealth struct {
	shards [modelHealthShardCount]modelHealthShard
	logger *zap.Logger
}

type modelHealthShard struct {
	mu      sync.RWMutex
	entries map[string]*ModelHealthEntry
}

const modelHealthShardCount = 32

// ModelHealthEntry tracks failure state for a single key+model combination.
type ModelHealthEntry struct {
	Failures    int
	LastFailure time.Time
	LastSuccess time.Time
	Disabled    bool
	DisabledAt  time.Time
	RecoverAt   time.Time
	LastError   string
}

const (
	// ModelFailureThreshold is the number of consecutive failures before a model
	// is disabled on a key.
	ModelFailureThreshold = 3

	// ModelRecoveryInterval is how long a model stays disabled before it is
	// automatically re-enabled for probing.
	ModelRecoveryInterval = 24 * time.Hour

	// ModelShortRecoveryInterval is used for transient-looking errors where we
	// want to retry sooner.
	ModelShortRecoveryInterval = 1 * time.Hour
)

// NewModelHealth creates a new model health tracker.
func NewModelHealth(logger *zap.Logger) *ModelHealth {
	if logger == nil {
		logger = zap.NewNop()
	}
	mh := &ModelHealth{logger: logger}
	for i := range mh.shards {
		mh.shards[i].entries = make(map[string]*ModelHealthEntry)
	}
	return mh
}

func modelHealthKey(keyID, model string) string {
	return keyID + ":" + model
}

func (mh *ModelHealth) shardFor(keyID string) *modelHealthShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(keyID))
	return &mh.shards[h.Sum32()%modelHealthShardCount]
}

// IsModelAvailable checks whether a model is currently usable on the given key.
// Returns true if there is no health entry or if the model is not disabled.
// If the recovery time has passed, the model is re-enabled (allowing a probe).
func (mh *ModelHealth) IsModelAvailable(keyID, model string) bool {
	shard := mh.shardFor(keyID)
	key := modelHealthKey(keyID, model)

	// Snapshot the fields we need under the read lock. Reading entry.Disabled /
	// entry.RecoverAt after RUnlock() races with RecordFailure/RecordSuccess,
	// which write the same fields under the write lock — go test -race flags
	// this on every request since IsModelAvailable is on the hot path.
	shard.mu.RLock()
	entry, ok := shard.entries[key]
	var disabled bool
	var recoverAt time.Time
	if ok && entry != nil {
		disabled = entry.Disabled
		recoverAt = entry.RecoverAt
	}
	shard.mu.RUnlock()

	if !ok || entry == nil {
		return true
	}

	if !disabled {
		return true
	}

	// Recovery check: if past RecoverAt, re-enable for probing. Re-fetch the
	// entry under the write lock because a concurrent RecordFailure may have
	// pushed RecoverAt forward or re-disabled it since our snapshot.
	if time.Now().After(recoverAt) {
		shard.mu.Lock()
		entry, ok := shard.entries[key]
		if ok && entry != nil && entry.Disabled && time.Now().After(entry.RecoverAt) {
			entry.Disabled = false
			entry.Failures = 0
			shard.mu.Unlock()
			mh.logger.Info("Model health: re-enabled for probing",
				zap.String("key_id", keyID),
				zap.String("model", model),
			)
			return true
		}
		shard.mu.Unlock()
		// Entry changed concurrently; re-evaluate against the latest state.
		return !ok || entry == nil || !entry.Disabled
	}

	return false
}

// RecordSuccess records a successful request for a key+model combination.
// Clears any accumulated failures.
func (mh *ModelHealth) RecordSuccess(keyID, model string) {
	shard := mh.shardFor(keyID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	entry, ok := shard.entries[modelHealthKey(keyID, model)]
	if !ok {
		// No entry = no failures recorded, nothing to do
		return
	}

	// Clear failure state on success
	entry.Failures = 0
	entry.LastSuccess = time.Now()
	if entry.Disabled {
		entry.Disabled = false
		mh.logger.Info("Model health: restored after success",
			zap.String("key_id", keyID),
			zap.String("model", model),
		)
	}
}

// isTransportCancellation reports whether errorMsg is a client-side or
// transport-level cancellation rather than an upstream rejection. Such
// failures say nothing about whether the model is available on the key —
// the client may simply have disconnected while a slow (e.g. long-reasoning)
// response was still generating. Counting them toward ModelFailureThreshold
// caused a 24h total outage of glm-5.3 on a single-key provider after three
// client timeouts (2026-10-23). Key/provider-level health still records them.
func isTransportCancellation(errorMsg string) bool {
	return strings.Contains(errorMsg, "context canceled") ||
		strings.Contains(errorMsg, "context deadline exceeded") ||
		strings.Contains(errorMsg, "use of closed network connection")
}

// RecordFailure records a model-level failure for a key+model combination.
// After ModelFailureThreshold consecutive failures, the model is disabled.
// Returns true if the model was just disabled by this call.
func (mh *ModelHealth) RecordFailure(keyID, model, errorMsg string) bool {
	if isTransportCancellation(errorMsg) {
		return false
	}

	shard := mh.shardFor(keyID)
	now := time.Now()

	shard.mu.Lock()
	defer shard.mu.Unlock()

	entry, ok := shard.entries[modelHealthKey(keyID, model)]
	if !ok {
		entry = &ModelHealthEntry{}
		shard.entries[modelHealthKey(keyID, model)] = entry
	}

	entry.Failures++
	entry.LastFailure = now
	entry.LastError = errorMsg

	if entry.Failures >= ModelFailureThreshold && !entry.Disabled {
		entry.Disabled = true
		entry.DisabledAt = now
		entry.RecoverAt = now.Add(ModelRecoveryInterval)
		mh.logger.Warn("Model health: model disabled on key",
			zap.String("key_id", keyID),
			zap.String("model", model),
			zap.Int("failures", entry.Failures),
			zap.String("last_error", truncateStr(errorMsg, 100)),
			zap.Time("recover_at", entry.RecoverAt),
		)
		return true
	}

	return false
}

// Reset clears health state for a specific key+model combination.
func (mh *ModelHealth) Reset(keyID, model string) {
	shard := mh.shardFor(keyID)
	shard.mu.Lock()
	delete(shard.entries, modelHealthKey(keyID, model))
	shard.mu.Unlock()
}

// ResetKey clears health state for every model entry belonging to one key.
// All entries for a key live on the same shard (sharding is by keyID), so a
// single shard sweep with a prefix match is sufficient.
func (mh *ModelHealth) ResetKey(keyID string) {
	shard := mh.shardFor(keyID)
	prefix := keyID + ":"
	shard.mu.Lock()
	for k := range shard.entries {
		if strings.HasPrefix(k, prefix) {
			delete(shard.entries, k)
		}
	}
	shard.mu.Unlock()
}

// ResetAll clears all model health entries.
func (mh *ModelHealth) ResetAll() {
	for i := range mh.shards {
		mh.shards[i].mu.Lock()
		mh.shards[i].entries = make(map[string]*ModelHealthEntry)
		mh.shards[i].mu.Unlock()
	}
}

// DisabledModels returns a list of currently disabled model entries for a given key.
// Because sharding is by keyID, all entries for the key live on the same shard.
func (mh *ModelHealth) DisabledModels(keyID string) []string {
	shard := mh.shardFor(keyID)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	var disabled []string
	prefix := keyID + ":"
	for k, entry := range shard.entries {
		if entry.Disabled && len(k) > len(prefix) && k[:len(prefix)] == prefix {
			model := k[len(prefix):]
			disabled = append(disabled, model)
		}
	}
	return disabled
}

// Stats returns summary statistics.
func (mh *ModelHealth) Stats() (total, disabled int) {
	for i := range mh.shards {
		mh.shards[i].mu.RLock()
		total += len(mh.shards[i].entries)
		for _, entry := range mh.shards[i].entries {
			if entry.Disabled {
				disabled++
			}
		}
		mh.shards[i].mu.RUnlock()
	}
	return
}

// Cleanup removes entries that have been recovered (not disabled) and have no
// recent failures, as well as disabled entries whose RecoverAt is more than
// maxAge in the past (they've had ample time to recover).
// Call this periodically to prevent unbounded growth.
func (mh *ModelHealth) Cleanup(maxAge time.Duration) int {
	removed := 0
	cutoff := time.Now().Add(-maxAge)
	for i := range mh.shards {
		mh.shards[i].mu.Lock()
		for k, entry := range mh.shards[i].entries {
			if (!entry.Disabled && entry.LastFailure.Before(cutoff) && entry.LastSuccess.Before(cutoff)) ||
				(entry.Disabled && entry.RecoverAt.Before(cutoff)) {
				delete(mh.shards[i].entries, k)
				removed++
			}
		}
		mh.shards[i].mu.Unlock()
	}
	return removed
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
