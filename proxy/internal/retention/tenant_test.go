package retention

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	entA = "3f2b8c1e-5d6a-4e7f-9a0b-1c2d3e4f5a6b"
	entB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

func newStore(t *testing.T, mut func(*Config)) (*DiskStore, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{Enabled: true, Directory: dir, BatchSize: 1, FlushEvery: time.Hour, MaxAge: 30 * 24 * time.Hour}
	if mut != nil {
		mut(&cfg)
	}
	s, err := NewDiskStore(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func TestResolveTenant(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"canonical uuid", entA, filepath.Join("ent", entA), true},
		{"personal sentinel", "_personal", filepath.Join("ent", "_personal"), true},
		{"upper case is normalised", strings.ToUpper(entA), filepath.Join("ent", entA), true},
		{"empty", "", "", false},
		{"path traversal", "../../x", "", false},
		{"traversal with uuid prefix", entA + "/../..", "", false},
		{"absolute path", "/etc/passwd", "", false},
		{"urn form rejected", "urn:uuid:" + entA, "", false},
		{"braced form rejected", "{" + entA + "}", "", false},
		{"no hyphens rejected", strings.ReplaceAll(entA, "-", ""), "", false},
		{"nul byte", entA + "\x00", "", false},
		{"dot", ".", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveTenant(tt.in)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWriteLayout(t *testing.T) {
	tests := []struct {
		name       string
		meta       Metadata
		wantPrefix string
	}{
		{"enterprise with days", Metadata{EnterpriseID: entA, RetentionDays: 14}, filepath.Join("ent", entA, "14d")},
		{"no enterprise => _personal", Metadata{RetentionDays: 7}, filepath.Join("ent", "_personal", "7d")},
		{"malicious enterprise id => _personal", Metadata{EnterpriseID: "../../x", RetentionDays: 7}, filepath.Join("ent", "_personal", "7d")},
		{"missing days => max age (30d)", Metadata{EnterpriseID: entA}, filepath.Join("ent", entA, "30d")},
		{"days clamped", Metadata{EnterpriseID: entA, RetentionDays: 5000}, filepath.Join("ent", entA, "365d")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := newStore(t, nil)
			s.Record(&Record{TraceID: "tr-1", Metadata: tt.meta})

			now := time.Now().UTC()
			want := filepath.Join(dir, tt.wantPrefix, dateRelDir(now), "tr-1.json.gz")
			if _, err := os.Stat(want); err != nil {
				// Tolerate an hour rollover between write and assertion.
				want = filepath.Join(dir, tt.wantPrefix, dateRelDir(now.Add(-time.Hour)), "tr-1.json.gz")
			}
			_, err := os.Stat(want)
			require.NoError(t, err)

			// Nothing may have been written outside the retention directory.
			_, err = os.Stat(filepath.Join(dir, "..", "x"))
			assert.True(t, os.IsNotExist(err))
		})
	}
}

func TestWriteRejectsUnsafeTraceID(t *testing.T) {
	s, dir := newStore(t, nil)
	for _, id := range []string{"../escape", "a/b", ".."} {
		require.Error(t, s.writeRecord(&Record{TraceID: id}), id)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	assert.Empty(t, files)
}

func TestGetForEnterpriseIsolationAndLegacyFallback(t *testing.T) {
	s, dir := newStore(t, nil)
	s.Record(&Record{TraceID: "tr-a", Metadata: Metadata{EnterpriseID: entA, RetentionDays: 10, UserID: "ua"}})
	s.Record(&Record{TraceID: "tr-b", Metadata: Metadata{EnterpriseID: entB, RetentionDays: 10}})

	// Legacy file in the old layout stays readable through Get().
	writeLegacy(t, dir, "2024/01/01/00", "tr-legacy", time.Now())

	got, err := s.GetForEnterprise(entA, "tr-a")
	require.NoError(t, err)
	assert.Equal(t, "ua", got.Metadata.UserID)

	_, err = s.GetForEnterprise(entA, "tr-b")
	assert.Error(t, err, "must not read another tenant's record")

	_, err = s.GetForEnterprise("../../x", "tr-a")
	assert.Error(t, err)
	_, err = s.GetForEnterprise(entA, "../tr-b")
	assert.Error(t, err)

	for _, id := range []string{"tr-a", "tr-b", "tr-legacy"} {
		_, err := s.Get(id)
		assert.NoError(t, err, id)
	}
	_, err = s.GetForEnterprise(entB, "nope")
	assert.Error(t, err)
}

func writeLegacy(t *testing.T, root, rel, trace string, mtime time.Time) string {
	t.Helper()
	d := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(d, 0o750))
	p := filepath.Join(d, trace+".json.gz")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	require.NoError(t, json.NewEncoder(gw).Encode(Record{TraceID: trace}))
	require.NoError(t, gw.Close())
	require.NoError(t, os.WriteFile(p, buf.Bytes(), 0o600))
	require.NoError(t, os.Chtimes(p, mtime, mtime))
	return p
}

func TestCleanup(t *testing.T) {
	s, dir := newStore(t, func(c *Config) { c.MaxAge = 30 * 24 * time.Hour })
	now := time.Now().UTC()
	old := func(d time.Duration) time.Time { return now.Add(-d) }
	day := 24 * time.Hour
	rel := func(ts time.Time) string { return filepath.ToSlash(dateRelDir(ts)) }

	type file struct {
		path    string
		deleted bool
	}
	var files []file
	add := func(root, bucket string, age time.Duration, trace string, deleted bool) {
		ts := old(age)
		r := rel(ts)
		if bucket != "" {
			r = bucket + "/" + r
		}
		files = append(files, file{writeLegacy(t, filepath.Join(dir, root), r, trace, ts), deleted})
	}

	// Legacy layout uses the global 30d max age.
	add("", "", 40*day, "legacy-old", true)
	add("", "", 5*day, "legacy-new", false)
	// Tenant layout uses its own <N>d.
	add("ent/"+entA, "10d", 11*day, "a-10d-expired", true)
	add("ent/"+entA, "10d", 9*day, "a-10d-live", false)
	add("ent/"+entA, "90d", 60*day, "a-90d-live", false) // would be expired under a 30d rule
	add("ent/"+entA, "90d", 91*day, "a-90d-expired", true)
	add("ent/"+entB, "1d", 3*day, "b-1d-expired", true)
	add("ent/_personal", "30d", 29*day, "p-30d-live", false)

	// Files that are not retention data must survive.
	logPath := filepath.Join(dir, accessLogName)
	require.NoError(t, os.WriteFile(logPath, []byte("x\n"), 0o600))
	require.NoError(t, os.Chtimes(logPath, old(400*day), old(400*day)))
	strayDir := filepath.Join(dir, ".analytics")
	require.NoError(t, os.MkdirAll(strayDir, 0o750))
	stray := filepath.Join(strayDir, "latest.json")
	require.NoError(t, os.WriteFile(stray, []byte("{}"), 0o600))
	require.NoError(t, os.Chtimes(stray, old(400*day), old(400*day)))

	stats, err := s.CleanupWithStats()
	require.NoError(t, err)

	wantDeleted := int64(0)
	for _, f := range files {
		_, err := os.Stat(f.path)
		if f.deleted {
			assert.True(t, os.IsNotExist(err), "should be deleted: %s", f.path)
			wantDeleted++
		} else {
			assert.NoError(t, err, "should be kept: %s", f.path)
		}
	}
	assert.Equal(t, wantDeleted, stats.Files)
	assert.Positive(t, stats.Bytes)

	_, err = os.Stat(logPath)
	assert.NoError(t, err, "access log must never be cleaned")
	_, err = os.Stat(stray)
	assert.NoError(t, err, "non-retention directories must be left alone")

	// Emptied tenant (entB's only file expired) and its directories are pruned.
	_, err = os.Stat(filepath.Join(dir, "ent", entB))
	assert.True(t, os.IsNotExist(err), "empty tenant dir should be removed")
	_, err = os.Stat(filepath.Join(dir, "ent"))
	assert.NoError(t, err, "ent root stays")
	_, err = os.Stat(filepath.Dir(files[0].path))
	assert.True(t, os.IsNotExist(err), "empty hour dir should be removed")
}

func TestCleanup_SkipsFutureSubtreesAndBadNames(t *testing.T) {
	s, dir := newStore(t, nil)
	// A file in a directory that is newer than the cutoff is never stat'ed, so
	// even an ancient mtime cannot get it deleted.
	now := time.Now().UTC()
	p := writeLegacy(t, dir, filepath.ToSlash(dateRelDir(now)), "fresh-dir-old-mtime", now.Add(-1000*24*time.Hour))
	// Unparsable names are ignored rather than guessed at.
	bad := writeLegacy(t, dir, "ent/"+entA+"/notdays/2020/01/01/00", "x", now.Add(-1000*24*time.Hour))
	bad2 := writeLegacy(t, dir, "ent/"+entA+"/0d/2020/01/01/00", "y", now.Add(-1000*24*time.Hour))

	require.NoError(t, s.Cleanup())
	for _, f := range []string{p, bad, bad2} {
		_, err := os.Stat(f)
		assert.NoError(t, err, f)
	}
}

func TestCleanup_NotConcurrent(t *testing.T) {
	s, _ := newStore(t, nil)
	s.cleanupMu.Lock()
	stats, err := s.CleanupWithStats()
	s.cleanupMu.Unlock()
	require.NoError(t, err)
	assert.Zero(t, stats.Files, "a second pass must be skipped while one is running")
}

func TestDiskGuard(t *testing.T) {
	tests := []struct {
		name      string
		maxPct    int
		used      float64
		statErr   error
		wantWrite bool
	}{
		{"below threshold writes", 90, 50, nil, true},
		{"at threshold writes", 90, 90, nil, true},
		{"above threshold skips", 90, 95.5, nil, false},
		{"custom threshold", 50, 60, nil, false},
		{"default threshold applies when 0", 0, 95, nil, false},
		{"guard disabled at 100", 100, 99.9, nil, true},
		{"statfs error fails open", 90, 0, errors.New("boom"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := newStore(t, func(c *Config) { c.MaxDiskPercent = tt.maxPct })
			var seen string
			s.statfs = func(p string) (float64, error) { seen = p; return tt.used, tt.statErr }

			s.Record(&Record{TraceID: "g1"})
			s.Record(&Record{TraceID: "g2"})

			_, err := s.Get("g1")
			if tt.wantWrite {
				assert.NoError(t, err)
				assert.Zero(t, s.DiskSkipped())
			} else {
				assert.Error(t, err)
				assert.Equal(t, int64(2), s.DiskSkipped())
			}
			if tt.maxPct != 100 {
				assert.Equal(t, dir, seen, "guard must measure the retention directory's filesystem")
			}
		})
	}
}

func TestUsedPercent(t *testing.T) {
	assert.InDelta(t, 50, usedPercent(100, 50, 50), 0.001)
	assert.InDelta(t, 55.55, usedPercent(100, 50, 40), 0.01, "reserved blocks count as used, like df")
	assert.Zero(t, usedPercent(0, 0, 0))
}

func TestDiskUsedPercentReal(t *testing.T) {
	pct, err := diskUsedPercent(t.TempDir())
	if err != nil {
		t.Skipf("statfs unsupported: %v", err)
	}
	assert.GreaterOrEqual(t, pct, 0.0)
	assert.LessOrEqual(t, pct, 100.0)
}

func TestListObjects(t *testing.T) {
	s, dir := newStore(t, nil)
	now := time.Now().UTC()
	mk := func(age time.Duration, trace string) {
		ts := now.Add(-age)
		writeLegacy(t, filepath.Join(dir, "ent", entA), "10d/"+filepath.ToSlash(dateRelDir(ts)), trace, ts)
	}
	mk(5*time.Minute, "newest")
	mk(2*time.Hour, "mid")
	mk(50*time.Hour, "older")
	writeLegacy(t, filepath.Join(dir, "ent", entB), "10d/"+filepath.ToSlash(dateRelDir(now)), "other-tenant", now)

	ids := func(objs []ObjectInfo) []string {
		out := []string{}
		for _, o := range objs {
			out = append(out, o.TraceID)
		}
		return out
	}

	tests := []struct {
		name  string
		ent   string
		limit int
		date  string
		want  []string
		err   bool
	}{
		{"newest first", entA, 0, "", []string{"newest", "mid", "older"}, false},
		{"limit caps", entA, 2, "", []string{"newest", "mid"}, false},
		{"date filter", entA, 0, now.Add(-50 * time.Hour).Format("2006-01-02"), nil, false}, // computed below
		{"unknown tenant is empty", "11111111-1111-4111-8111-111111111111", 0, "", []string{}, false},
		{"traversal rejected", "../../x", 0, "", nil, true},
		{"bad date rejected", entA, 0, "yesterday", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs, err := s.ListObjects(tt.ent, tt.limit, tt.date)
			if tt.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.name == "date filter" {
				for _, o := range objs {
					assert.Equal(t, tt.date, o.Time.Format("2006-01-02"))
				}
				assert.Contains(t, ids(objs), "older")
				return
			}
			assert.Equal(t, tt.want, ids(objs))
			for _, o := range objs {
				assert.Equal(t, 10, o.Days)
				assert.Positive(t, o.Size)
			}
		})
	}
}

func TestPurgeEnterprise(t *testing.T) {
	s, dir := newStore(t, nil)
	s.Record(&Record{TraceID: "a1", Metadata: Metadata{EnterpriseID: entA, RetentionDays: 5}})
	s.Record(&Record{TraceID: "a2", Metadata: Metadata{EnterpriseID: entA, RetentionDays: 9}})
	s.Record(&Record{TraceID: "b1", Metadata: Metadata{EnterpriseID: entB, RetentionDays: 5}})

	res, err := s.PurgeEnterprise(entA)
	require.NoError(t, err)
	assert.Equal(t, int64(2), res.DeletedFiles)
	assert.Positive(t, res.DeletedBytes)

	_, err = os.Stat(filepath.Join(dir, "ent", entA))
	assert.True(t, os.IsNotExist(err))
	_, err = s.GetForEnterprise(entB, "b1")
	assert.NoError(t, err, "other tenants untouched")

	res, err = s.PurgeEnterprise(entA)
	require.NoError(t, err)
	assert.Zero(t, res.DeletedFiles, "purging twice is a no-op")

	_, err = s.PurgeEnterprise("../../x")
	assert.Error(t, err)
	_, err = s.PurgeEnterprise("")
	assert.Error(t, err)
}

func TestAppendAccessLog(t *testing.T) {
	s, dir := newStore(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, s.AppendAccessLog(AccessEntry{Action: "read", TraceID: "t", ClientIP: "1.1.1.1", Result: "ok"}))
		}()
	}
	wg.Wait()

	path := filepath.Join(dir, accessLogName)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	raw, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, lines, 5)
	var e AccessEntry
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &e))
	assert.Equal(t, "read", e.Action)
	assert.False(t, e.Time.IsZero())
}

type fakeStore struct {
	mu    sync.Mutex
	calls int
	ch    chan struct{}
}

func (f *fakeStore) Record(*Record)              {}
func (f *fakeStore) Flush()                      {}
func (f *fakeStore) Close() error                { return nil }
func (f *fakeStore) Get(string) (*Record, error) { return nil, errors.New("x") }
func (f *fakeStore) Cleanup() error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	select {
	case f.ch <- struct{}{}:
	default:
	}
	return nil
}

func TestRunCleanupLoop(t *testing.T) {
	f := &fakeStore{ch: make(chan struct{}, 10)}
	ctx, cancel := context.WithCancel(context.Background())
	done := RunCleanupLoop(ctx, f, zap.NewNop(), 10*time.Millisecond, 20*time.Millisecond)

	for i := 0; i < 2; i++ {
		select {
		case <-f.ch:
		case <-time.After(2 * time.Second):
			t.Fatal("cleanup did not run")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not stop on context cancel")
	}
	f.mu.Lock()
	n := f.calls
	f.mu.Unlock()
	time.Sleep(60 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, n, f.calls, "no runs after shutdown")
}

func TestCleanup_CurrentPolicyOverridesDirectoryDays(t *testing.T) {
	s, dir := newStore(t, nil)
	now := time.Now().UTC()
	day := 24 * time.Hour
	put := func(tenant, bucket string, age time.Duration, trace string) string {
		ts := now.Add(-age)
		return writeLegacy(t, filepath.Join(dir, "ent", tenant), bucket+"/"+filepath.ToSlash(dateRelDir(ts)), trace, ts)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	extended := put(entA, "30d", 40*day, "extended")                                        // 30d -> 60d: must survive
	extendedOld := put(entA, "30d", 70*day, "ext-old")                                      // beyond the new 60d: goes
	shortened := put(entB, "90d", 40*day, "shortened")                                      // 90d -> 30d: goes now
	shortKeep := put(entB, "90d", 10*day, "short-keep")                                     // inside the new 30d
	unknown := put("cccccccc-bbbb-4ccc-8ddd-eeeeeeeeeeee", "30d", 40*day, "unknown-tenant") // no policy: dir N
	_ = put("_personal", "30d", 40*day, "personal")                                         // platform 60d: survives

	// No policy fetched yet: directory days decide.
	s.SetPolicySource(func(context.Context) (Policy, error) { return Policy{}, errors.New("portal down") })
	_, err := s.CleanupWithStats()
	require.NoError(t, err)
	assert.False(t, exists(extended), "fallback: 40d-old file in 30d dir expires when portal is unreachable")
	assert.True(t, exists(shortened), "fallback: 90d dir keeps 40d-old file")

	// Recreate what the failed pass removed, then use a working policy.
	extended = put(entA, "30d", 40*day, "extended")
	personal := put("_personal", "30d", 40*day, "personal")
	s.SetPolicySource(func(context.Context) (Policy, error) {
		return Policy{Platform: 60, Enterprises: map[string]int{entA: 60, entB: 30}}, nil
	})
	_, err = s.CleanupWithStats()
	require.NoError(t, err)
	assert.True(t, exists(extended), "30d->60d extension applies to existing files")
	assert.False(t, exists(extendedOld), "older than the new window")
	assert.False(t, exists(shortened), "90d->30d shortening applies immediately")
	assert.True(t, exists(shortKeep))
	assert.False(t, exists(unknown), "unknown tenant falls back to directory days")
	assert.True(t, exists(personal), "_personal follows the platform default")

	// A later fetch failure keeps the last good policy rather than reverting.
	s.SetPolicySource(func(context.Context) (Policy, error) { return Policy{}, errors.New("portal down") })
	extended2 := put(entA, "30d", 45*day, "extended2")
	_, err = s.CleanupWithStats()
	require.NoError(t, err)
	assert.True(t, exists(extended2), "stale policy is kept on fetch failure")
}
