package retention

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mayiants/ai-proxy-go/pkg/metrics"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const schemaVersion = "1.0"

// DiskStore writes retention records to local disk as gzip-compressed JSON.
// It is a minimal, S3-free implementation that lets operators verify the
// capture pipeline before committing to object-storage costs.
type DiskStore struct {
	directory  string
	maxAge     time.Duration
	batchSize  int
	flushEvery time.Duration
	logger     *zap.Logger

	maxDiskPercent int
	// statfs is injectable so the disk guard can be tested without filling a disk.
	statfs       func(path string) (float64, error)
	diskSkipped  atomic.Int64
	lastDiskWarn atomic.Int64 // unix nanos of the last guard warning

	// cleanupMu serialises Cleanup runs and tenant purges.
	cleanupMu sync.Mutex
	accessMu  sync.Mutex

	// Current Portal retention policy (see policy.go); nil until first fetch.
	policyMu  sync.Mutex
	policySrc PolicySource
	policy    *Policy

	mu      sync.Mutex
	buffer  []*Record
	ticker  *time.Ticker
	stopCh  chan struct{}
	stopped bool
}

// Config configures the local-disk retention store.
type Config struct {
	Enabled           bool
	Directory         string
	MaxAge            time.Duration
	BatchSize         int
	FlushEvery        time.Duration
	CaptureAllHeaders bool
	// MaxDiskPercent: skip writes when the retention filesystem is fuller than
	// this. 0 selects the default (90); values >= 100 disable the guard.
	MaxDiskPercent int
}

// NewDiskStore creates a local-disk retention store.
func NewDiskStore(cfg Config, logger *zap.Logger) (*DiskStore, error) {
	if cfg.Directory == "" {
		cfg.Directory = "./retention"
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1 // minimal default: flush immediately
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = 3 * time.Second
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 90 * 24 * time.Hour // 90 days
	}

	if cfg.MaxDiskPercent == 0 {
		cfg.MaxDiskPercent = defaultMaxDiskPercent
	}

	if err := os.MkdirAll(cfg.Directory, 0o750); err != nil {
		return nil, fmt.Errorf("create retention directory %q: %w", cfg.Directory, err)
	}

	s := &DiskStore{
		directory:      cfg.Directory,
		maxAge:         cfg.MaxAge,
		batchSize:      cfg.BatchSize,
		flushEvery:     cfg.FlushEvery,
		maxDiskPercent: cfg.MaxDiskPercent,
		logger:         logger,
		statfs:         diskUsedPercent,
		buffer:         make([]*Record, 0, cfg.BatchSize),
		stopCh:         make(chan struct{}),
	}

	s.ticker = time.NewTicker(cfg.FlushEvery)
	go s.loop()
	return s, nil
}

// Record enqueues a retention record. The write happens asynchronously unless
// the batch is full, in which case it flushes synchronously.
func (s *DiskStore) Record(r *Record) {
	if r == nil {
		return
	}
	if r.TraceID == "" {
		r.TraceID = uuid.New().String()
	}
	if r.SchemaVersion == "" {
		r.SchemaVersion = schemaVersion
	}
	if r.ReceivedAt == "" {
		r.ReceivedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	s.mu.Lock()
	s.buffer = append(s.buffer, r)
	shouldFlush := len(s.buffer) >= s.batchSize
	s.mu.Unlock()

	if shouldFlush {
		s.Flush()
	}
}

// Flush writes all buffered records to disk.
func (s *DiskStore) Flush() {
	s.mu.Lock()
	if len(s.buffer) == 0 {
		s.mu.Unlock()
		return
	}
	batch := s.buffer
	s.buffer = make([]*Record, 0, s.batchSize)
	s.mu.Unlock()

	for _, r := range batch {
		if err := s.writeRecord(r); err != nil && s.logger != nil {
			s.logger.Warn("Failed to write retention record",
				zap.String("trace_id", r.TraceID),
				zap.Error(err),
			)
		}
	}
}

// Close stops the background flush loop and flushes remaining records.
func (s *DiskStore) Close() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	s.mu.Unlock()

	close(s.stopCh)
	s.ticker.Stop()
	s.Flush()
	return nil
}

// Get reads a stored record by trace ID. It searches the whole directory tree
// (legacy and tenant layouts) and is the slow fallback; prefer GetForEnterprise
// when the tenant is known.
func (s *DiskStore) Get(traceID string) (*Record, error) {
	return s.findRecord(s.directory, traceID)
}

// GetForEnterprise searches only that tenant's subtree. enterpriseID must be a
// canonical UUID or "_personal".
func (s *DiskStore) GetForEnterprise(enterpriseID, traceID string) (*Record, error) {
	rel, ok := ResolveTenant(enterpriseID)
	if !ok {
		return nil, fmt.Errorf("invalid enterprise_id")
	}
	return s.findRecord(filepath.Join(s.directory, rel), traceID)
}

func (s *DiskStore) findRecord(root, traceID string) (*Record, error) {
	if traceID == "" {
		return nil, fmt.Errorf("trace_id is required")
	}
	if !safeFileComponent(traceID) {
		return nil, fmt.Errorf("invalid trace_id")
	}

	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(path) == traceID+".json.gz" {
			found = path
			return io.EOF // stop walking
		}
		return nil
	})
	if err != nil && err != io.EOF {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("record not found: %s", traceID)
		}
		return nil, err
	}
	if found == "" {
		return nil, fmt.Errorf("record not found: %s", traceID)
	}
	return s.readRecord(found)
}

func (s *DiskStore) loop() {
	for {
		select {
		case <-s.ticker.C:
			s.Flush()
		case <-s.stopCh:
			return
		}
	}
}

func (s *DiskStore) writeRecord(r *Record) error {
	// The trace id becomes a file name; refuse anything that could escape the
	// directory instead of trusting upstream callers.
	if !safeFileComponent(r.TraceID) {
		return fmt.Errorf("unsafe trace_id %q", r.TraceID)
	}
	if s.diskGuardTripped() {
		return nil
	}

	// Layout: <directory>/ent/<enterprise_id|_personal>/<days>d/<yyyy>/<mm>/<dd>/<hh>/<trace_id>.json.gz
	// The tenant and retention days are encoded in the path so Cleanup can
	// enforce per-tenant expiry without opening any file.
	now := time.Now().UTC()
	dir := filepath.Join(s.directory, tenantRelDir(r.Metadata.EnterpriseID), s.daysDirName(r.Metadata.RetentionDays), dateRelDir(now))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}

	path := filepath.Join(dir, r.TraceID+".json.gz")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	gw := gzip.NewWriter(f)
	enc := json.NewEncoder(gw)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		_ = gw.Close()
		return fmt.Errorf("encode record: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	return nil
}

// diskGuardTripped reports whether the retention filesystem is fuller than the
// configured threshold. A statfs failure fails open: losing retention data
// because of a monitoring error is worse than a possibly-full disk, which the
// OS will report on its own.
func (s *DiskStore) diskGuardTripped() bool {
	if s.maxDiskPercent <= 0 || s.maxDiskPercent >= 100 || s.statfs == nil {
		return false
	}
	pct, err := s.statfs(s.directory)
	if err != nil || pct <= float64(s.maxDiskPercent) {
		return false
	}
	s.diskSkipped.Add(1)
	metrics.GetPrometheusRegistry().RetentionDiskSkippedTotal.Inc()
	// Rate-limit the warning to one per minute; at full speed this path would
	// otherwise log once per request.
	nowNs := time.Now().UnixNano()
	last := s.lastDiskWarn.Load()
	if (last == 0 || nowNs-last >= int64(time.Minute)) && s.lastDiskWarn.CompareAndSwap(last, nowNs) && s.logger != nil {
		s.logger.Warn("Retention disk usage above threshold; skipping writes",
			zap.Float64("used_percent", pct),
			zap.Int("max_disk_percent", s.maxDiskPercent),
			zap.Int64("skipped_total", s.diskSkipped.Load()),
		)
	}
	return true
}

// DiskSkipped returns the number of writes skipped by the disk guard.
func (s *DiskStore) DiskSkipped() int64 { return s.diskSkipped.Load() }

func (s *DiskStore) readRecord(path string) (*Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()

	var r Record
	if err := json.NewDecoder(gr).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// IsEnabled reports whether the supplied config turns retention on.
func IsEnabled(cfg Config) bool {
	return cfg.Enabled
}

// Store is the interface used by handlers to retain request/response pairs.
type Store interface {
	Record(*Record)
	Flush()
	Close() error
	Get(traceID string) (*Record, error)
	Cleanup() error
}

// NoopStore is a disabled store that does nothing.
type NoopStore struct{}

func (NoopStore) Record(*Record) {}
func (NoopStore) Flush()         {}
func (NoopStore) Close() error   { return nil }
func (NoopStore) Get(string) (*Record, error) {
	return nil, fmt.Errorf("retention is disabled")
}
func (NoopStore) Cleanup() error { return nil }

// Compile-time interface checks.
var _ Store = (*DiskStore)(nil)
var _ Store = (*NoopStore)(nil)

// BackgroundContext is used by the cleanup job.
var BackgroundContext = context.Background()
