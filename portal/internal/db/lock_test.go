package db

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdvisoryLock_Singleton(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	lockID := int64(999999001)

	// Acquire the lock in a separate goroutine and hold it briefly.
	lock, acquired, err := db.TryAdvisoryLock(ctx, lockID)
	require.NoError(t, err)
	require.True(t, acquired, "first acquisition should succeed")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Give the main goroutine time to verify contention.
		time.Sleep(100 * time.Millisecond)
		require.NoError(t, lock.Release(ctx))
	}()

	// A second acquisition on the same lock ID should fail while held.
	_, acquired2, err2 := db.TryAdvisoryLock(ctx, lockID)
	require.NoError(t, err2)
	assert.False(t, acquired2, "second acquisition should fail while lock is held")

	wg.Wait()

	// After the lock is released, a new acquisition should succeed.
	lock3, acquired3, err3 := db.TryAdvisoryLock(ctx, lockID)
	require.NoError(t, err3)
	require.True(t, acquired3, "re-acquisition after release should succeed")
	require.NoError(t, lock3.Release(ctx))
}
