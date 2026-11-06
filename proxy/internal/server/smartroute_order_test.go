package server

import (
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/pkg/keypool"
)

func mkKey(id string, failures int, lastFailure, lastSuccess, lastUsed time.Time) *keypool.KeyInternal {
	ki := &keypool.KeyInternal{Key: &keypool.Key{
		KeyStatic:  keypool.KeyStatic{ID: id},
		KeyRuntime: keypool.KeyRuntime{Failures: failures, LastFailure: lastFailure},
	}}
	if !lastSuccess.IsZero() {
		ki.SetLastSuccess(lastSuccess)
	}
	if !lastUsed.IsZero() {
		ki.SetLastUsed(lastUsed)
	}
	return ki
}

// A brand new key (never succeeded) must NOT be starved behind keys that have
// succeeded — it participates in round-robin by effective-LRU time (lastUsed).
func TestLessBySuccessThenLRU_NewKeyNotStarved(t *testing.T) {
	now := time.Now()
	// succeeded key: last active 1 min ago. new key: just entered pool, never
	// used/succeeded (lastUsed ~ now would make it newest = sorts last, which is
	// fine). Make the new key's lastUsed OLDER so it should be tried first.
	succeeded := mkKey("ok", 0, time.Time{}, now.Add(-1*time.Minute), now.Add(-1*time.Minute))
	newKey := mkKey("new", 0, time.Time{}, time.Time{}, now.Add(-5*time.Minute))
	// newKey is least-recently-active → should sort FIRST (gets a chance).
	if !lessBySuccessThenLRU(newKey, succeeded) {
		t.Fatal("a new key with older effective-LRU time must be selectable, not starved")
	}
}

func TestLessBySuccessThenLRU_RoundRobin(t *testing.T) {
	now := time.Now()
	older := mkKey("older", 0, time.Time{}, now.Add(-10*time.Minute), now.Add(-10*time.Minute))
	newer := mkKey("newer", 0, time.Time{}, now.Add(-1*time.Minute), now.Add(-1*time.Minute))
	if !lessBySuccessThenLRU(older, newer) {
		t.Fatal("least-recently-active key should sort first (round-robin)")
	}
}

// effectiveLRUTime uses max(lastSuccess, lastUsed): a key that succeeded recently
// but was 'used' long ago still counts as recently active.
func TestEffectiveLRUTime_MaxOfSuccessAndUsed(t *testing.T) {
	now := time.Now()
	k := mkKey("k", 0, time.Time{}, now.Add(-1*time.Minute), now.Add(-30*time.Minute))
	if got := effectiveLRUTime(k); got.Before(now.Add(-2 * time.Minute)) {
		t.Fatalf("effectiveLRUTime should use lastSuccess (recent), got %v", got)
	}
}

func TestLiveHealthTier_Levels(t *testing.T) {
	now := time.Now()
	healthy := mkKey("h", 0, time.Time{}, now.Add(-1*time.Minute), now.Add(-1*time.Minute))
	suspect := mkKey("s", 1, now, now.Add(-5*time.Minute), now)     // 1 failure
	failing := mkKey("f", 3, now, now.Add(-10*time.Minute), now)    // 3+ failures
	recentFail := mkKey("rf", 0, now, now.Add(-1*time.Minute), now) // last outcome failure, <2min
	if liveHealthTier(healthy) != 0 {
		t.Fatal("healthy key should be tier 0")
	}
	if liveHealthTier(suspect) != 1 {
		t.Fatal("1-failure key should be tier 1 (suspect)")
	}
	if liveHealthTier(failing) != 2 {
		t.Fatal("3+ failures key should be tier 2 (failing)")
	}
	if liveHealthTier(recentFail) != 1 {
		t.Fatal("recent last-outcome-failure should be tier 1 (suspect)")
	}
}

func TestLessByHealthThenSuccess_HealthyBeatsFailing(t *testing.T) {
	now := time.Now()
	healthy := mkKey("healthy", 0, time.Time{}, now.Add(-9*time.Minute), now.Add(-9*time.Minute))
	failing := mkKey("failing", 2, now, time.Time{}, now)
	if !lessByHealthThenSuccess(healthy, failing) {
		t.Fatal("healthy key must sort ahead of a failing key")
	}
	if lessByHealthThenSuccess(failing, healthy) {
		t.Fatal("failing key must NOT sort ahead of a healthy key")
	}
}

// A never-successful but HEALTHY new key beats a FAILING key (health tier wins).
func TestLessByHealthThenSuccess_NewHealthyBeatsFailing(t *testing.T) {
	now := time.Now()
	newHealthy := mkKey("new", 0, time.Time{}, time.Time{}, now.Add(-1*time.Minute))
	failing := mkKey("bad", 3, now, time.Time{}, now)
	if !lessByHealthThenSuccess(newHealthy, failing) {
		t.Fatal("a fresh healthy key must sort ahead of a failing key")
	}
}
