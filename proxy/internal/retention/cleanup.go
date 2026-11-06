package retention

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// CleanupStats summarises one cleanup pass.
type CleanupStats struct {
	Files int64
	Bytes int64
}

// Cleanup enforces retention: legacy files (<dir>/<yyyy>/...) expire after the
// global max age, tenant files (<dir>/ent/<tenant>/<N>d/...) after the tenant's
// current Portal policy (N days from the directory name when none is known).
// Concurrent calls are collapsed: if a pass is already running this returns
// immediately.
func (s *DiskStore) Cleanup() error {
	_, err := s.CleanupWithStats()
	return err
}

// CleanupWithStats is Cleanup plus removal counts.
func (s *DiskStore) CleanupWithStats() (CleanupStats, error) {
	var stats CleanupStats
	if !s.cleanupMu.TryLock() {
		return stats, nil
	}
	defer s.cleanupMu.Unlock()
	return s.cleanupLocked(time.Now().UTC(), stats)
}

func (s *DiskStore) cleanupLocked(now time.Time, stats CleanupStats) (CleanupStats, error) {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return stats, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue // access log, stray files: never retention data
		}
		switch {
		case e.Name() == tenantRoot:
			s.refreshPolicy()
			s.cleanupTenants(filepath.Join(s.directory, tenantRoot), now, &stats)
		case isNumeric(e.Name(), 4):
			// Legacy layout: <yyyy>/<mm>/<dd>/<hh>
			s.pruneYear(filepath.Join(s.directory, e.Name()), now.Add(-s.maxAge), &stats)
		}
	}
	return stats, nil
}

func (s *DiskStore) cleanupTenants(root string, now time.Time, stats *CleanupStats) {
	tenants, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, t := range tenants {
		if !t.IsDir() {
			continue
		}
		tenantDir := filepath.Join(root, t.Name())
		buckets, err := os.ReadDir(tenantDir)
		if err != nil {
			continue
		}
		// The Portal's current window wins over the <N>d directory name, so an
		// admin changing 30d -> 90d (or 90d -> 30d) applies to data already on
		// disk without moving it. The directory N is only the fallback.
		var policyDays int
		var havePolicy bool
		if pol := s.currentPolicy(); pol != nil {
			policyDays, havePolicy = pol.daysFor(t.Name())
		}
		for _, b := range buckets {
			days, ok := parseDaysDir(b.Name())
			if !b.IsDir() || !ok {
				continue
			}
			if havePolicy {
				days = policyDays
			}
			cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
			bucketDir := filepath.Join(tenantDir, b.Name())
			s.cleanupDateTree(bucketDir, cutoff, stats)
			removeIfEmpty(bucketDir)
		}
		removeIfEmpty(tenantDir)
	}
}

// cleanupDateTree prunes the year directories under a tenant bucket.
func (s *DiskStore) cleanupDateTree(root string, cutoff time.Time, stats *CleanupStats) {
	years, _ := os.ReadDir(root)
	for _, y := range years {
		if y.IsDir() && isNumeric(y.Name(), 4) {
			s.pruneYear(filepath.Join(root, y.Name()), cutoff, stats)
		}
	}
}

// pruneYear prunes <yyyy>/<mm>/<dd>/<hh>/*.json.gz. A directory whose period
// starts after cutoff cannot hold expired files (a file's mtime is never
// before the hour it was written in), so whole subtrees are skipped without
// being listed. Only inside older hour directories are files stat'ed.
func (s *DiskStore) pruneYear(dir string, cutoff time.Time, stats *CleanupStats) {
	year, _ := strconv.Atoi(filepath.Base(dir))
	if time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).After(cutoff) {
		return
	}
	months, _ := os.ReadDir(dir)
	for _, m := range months {
		mn, ok := parseNum(m.Name(), 1, 12)
		if !m.IsDir() || !ok {
			continue
		}
		monthDir := filepath.Join(dir, m.Name())
		if time.Date(year, time.Month(mn), 1, 0, 0, 0, 0, time.UTC).After(cutoff) {
			continue
		}
		days, _ := os.ReadDir(monthDir)
		for _, d := range days {
			dn, ok := parseNum(d.Name(), 1, 31)
			if !d.IsDir() || !ok {
				continue
			}
			dayDir := filepath.Join(monthDir, d.Name())
			if time.Date(year, time.Month(mn), dn, 0, 0, 0, 0, time.UTC).After(cutoff) {
				continue
			}
			hours, _ := os.ReadDir(dayDir)
			for _, h := range hours {
				hn, ok := parseNum(h.Name(), 0, 23)
				if !h.IsDir() || !ok {
					continue
				}
				if time.Date(year, time.Month(mn), dn, hn, 0, 0, 0, time.UTC).After(cutoff) {
					continue
				}
				hourDir := filepath.Join(dayDir, h.Name())
				s.pruneFiles(hourDir, cutoff, stats)
				removeIfEmpty(hourDir)
			}
			removeIfEmpty(dayDir)
		}
		removeIfEmpty(monthDir)
	}
	removeIfEmpty(dir)
}

func (s *DiskStore) pruneFiles(dir string, cutoff time.Time, stats *CleanupStats) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		info, err := f.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(dir, f.Name())
		if err := os.Remove(path); err != nil {
			if s.logger != nil {
				s.logger.Warn("Failed to remove expired retention file", zap.String("path", path), zap.Error(err))
			}
			continue
		}
		stats.Files++
		stats.Bytes += info.Size()
	}
}

// removeIfEmpty removes dir only when it has no entries; os.Remove already
// refuses non-empty directories, so a race with a concurrent write is safe.
func removeIfEmpty(dir string) { _ = os.Remove(dir) }

func isNumeric(name string, width int) bool {
	if len(name) != width {
		return false
	}
	_, err := strconv.Atoi(name)
	return err == nil
}

func parseNum(name string, lo, hi int) (int, bool) {
	n, err := strconv.Atoi(name)
	if err != nil || n < lo || n > hi || len(name) > 2 {
		return 0, false
	}
	return n, true
}

func parseDaysDir(name string) (int, bool) {
	if !strings.HasSuffix(name, "d") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(name, "d"))
	if err != nil || n < 1 || n > maxRetentionDays {
		return 0, false
	}
	return n, true
}

// RunCleanupLoop runs store.Cleanup once after initialDelay and then every
// interval until ctx is cancelled. The returned channel closes when the loop
// has exited. Overlap is impossible: the loop is sequential and DiskStore
// additionally guards against concurrent passes.
func RunCleanupLoop(ctx context.Context, store Store, logger *zap.Logger, initialDelay, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(initialDelay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			runCleanupOnce(store, logger)
			timer.Reset(interval)
		}
	}()
	return done
}

func runCleanupOnce(store Store, logger *zap.Logger) {
	start := time.Now()
	var (
		stats CleanupStats
		err   error
	)
	if ds, ok := store.(*DiskStore); ok {
		stats, err = ds.CleanupWithStats()
	} else {
		err = store.Cleanup()
	}
	if logger == nil {
		return
	}
	if err != nil {
		logger.Warn("Retention cleanup failed", zap.Error(err))
		return
	}
	logger.Info("Retention cleanup finished",
		zap.Int64("removed_files", stats.Files),
		zap.Int64("removed_bytes", stats.Bytes),
		zap.Duration("took", time.Since(start)),
	)
}
