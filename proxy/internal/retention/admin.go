package retention

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListLimit = 100
	maxListLimit     = 1000
)

// TenantStore is the optional per-enterprise capability of a Store. Handlers
// type-assert for it so simple stores (and test doubles) need not implement it.
type TenantStore interface {
	GetForEnterprise(enterpriseID, traceID string) (*Record, error)
	ListObjects(enterpriseID string, limit int, date string) ([]ObjectInfo, error)
	PurgeEnterprise(enterpriseID string) (PurgeResult, error)
	AppendAccessLog(AccessEntry) error
}

// ObjectInfo describes one stored record without reading its payload.
type ObjectInfo struct {
	TraceID string    `json:"trace_id"`
	Size    int64     `json:"size"`
	Time    time.Time `json:"time"`
	Days    int       `json:"retention_days"`
}

// PurgeResult reports what PurgeEnterprise removed.
type PurgeResult struct {
	DeletedFiles int64 `json:"deleted_files"`
	DeletedBytes int64 `json:"deleted_bytes"`
}

// AccessEntry is one line of the append-only admin access log.
type AccessEntry struct {
	Time         time.Time `json:"time"`
	Action       string    `json:"action"`
	TraceID      string    `json:"trace_id,omitempty"`
	EnterpriseID string    `json:"enterprise_id,omitempty"`
	ClientIP     string    `json:"client_ip,omitempty"`
	Result       string    `json:"result"`
}

// AppendAccessLog appends one JSON line to <dir>/retention-access.log (0600).
// It is append-only by construction: the file is only ever opened O_APPEND.
func (s *DiskStore) AppendAccessLog(e AccessEntry) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.directory, accessLogName), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(append(line, '\n'))
	return err
}

// ListObjects lists a tenant's records newest first, capped at limit
// (default 100, max 1000). date, if set, is YYYY-MM-DD (UTC).
func (s *DiskStore) ListObjects(enterpriseID string, limit int, date string) ([]ObjectInfo, error) {
	rel, ok := ResolveTenant(enterpriseID)
	if !ok {
		return nil, fmt.Errorf("invalid enterprise_id")
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	var day time.Time
	if date != "" {
		var err error
		if day, err = time.Parse("2006-01-02", date); err != nil {
			return nil, fmt.Errorf("invalid date, want YYYY-MM-DD")
		}
	}

	type hourDir struct {
		path  string
		start time.Time
		days  int
	}
	var hours []hourDir

	tenantDir := filepath.Join(s.directory, rel)
	buckets, err := os.ReadDir(tenantDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ObjectInfo{}, nil
		}
		return nil, err
	}
	for _, b := range buckets {
		days, ok := parseDaysDir(b.Name())
		if !b.IsDir() || !ok {
			continue
		}
		bucket := filepath.Join(tenantDir, b.Name())
		for _, y := range readDirs(bucket) {
			yn, ok := parseNum4(y)
			if !ok || (!day.IsZero() && yn != day.Year()) {
				continue
			}
			for _, m := range readDirs(filepath.Join(bucket, y)) {
				mn, ok := parseNum(m, 1, 12)
				if !ok || (!day.IsZero() && mn != int(day.Month())) {
					continue
				}
				for _, d := range readDirs(filepath.Join(bucket, y, m)) {
					dn, ok := parseNum(d, 1, 31)
					if !ok || (!day.IsZero() && dn != day.Day()) {
						continue
					}
					for _, h := range readDirs(filepath.Join(bucket, y, m, d)) {
						hn, ok := parseNum(h, 0, 23)
						if !ok {
							continue
						}
						hours = append(hours, hourDir{
							path:  filepath.Join(bucket, y, m, d, h),
							start: time.Date(yn, time.Month(mn), dn, hn, 0, 0, 0, time.UTC),
							days:  days,
						})
					}
				}
			}
		}
	}
	// Newest hour first so we can stop as soon as the cap is met: every
	// remaining hour is older than everything already collected.
	sort.Slice(hours, func(i, j int) bool { return hours[i].start.After(hours[j].start) })

	var out []ObjectInfo
	for _, h := range hours {
		files, err := os.ReadDir(h.path)
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if f.IsDir() || !strings.HasSuffix(name, ".json.gz") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			out = append(out, ObjectInfo{
				TraceID: strings.TrimSuffix(name, ".json.gz"),
				Size:    info.Size(),
				Time:    info.ModTime().UTC(),
				Days:    h.days,
			})
		}
		if len(out) >= limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []ObjectInfo{}
	}
	return out, nil
}

// PurgeEnterprise deletes a tenant's entire subtree. It takes the cleanup lock
// so it cannot interleave with a scheduled pass.
func (s *DiskStore) PurgeEnterprise(enterpriseID string) (PurgeResult, error) {
	var res PurgeResult
	rel, ok := ResolveTenant(enterpriseID)
	if !ok {
		return res, fmt.Errorf("invalid enterprise_id")
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()

	root := filepath.Join(s.directory, rel)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, err
	}
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			res.DeletedFiles++
			res.DeletedBytes += info.Size()
		}
		return nil
	})
	if err := os.RemoveAll(root); err != nil {
		return res, err
	}
	return res, nil
}

func readDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func parseNum4(name string) (int, bool) {
	if !isNumeric(name, 4) {
		return 0, false
	}
	n, err := strconv.Atoi(name)
	return n, err == nil
}

var _ TenantStore = (*DiskStore)(nil)
