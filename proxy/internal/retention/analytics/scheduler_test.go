package analytics

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// TestRunNow_SkipsWhenRunning verifies that a refresh triggered while a scan
// is still in flight is rejected instead of starting a second overlapping
// scan of the same retention data.
func TestRunNow_SkipsWhenRunning(t *testing.T) {
	dir := t.TempDir()
	sched := NewScheduler(SchedulerConfig{
		RetentionDir: filepath.Join(dir, "retention"),
		ReportDir:    filepath.Join(dir, "reports"),
	}, zap.NewNop())

	// Simulate an in-flight scan by holding the run lock.
	if !sched.runMu.TryLock() {
		t.Fatal("expected to acquire run lock")
	}
	defer sched.runMu.Unlock()

	_, err := sched.RunNow()
	assert.True(t, errors.Is(err, ErrAnalysisRunning), "expected ErrAnalysisRunning, got %v", err)
}
