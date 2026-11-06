package fallbacklog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultDir       = "/var/log/ai-proxy"
	defaultRetention = 7 * 24 * time.Hour // 7 days
)

// Writer writes failed records to local NDJSON files for manual reconciliation.
type Writer struct {
	dir       string
	mu        sync.Mutex
	retention time.Duration
}

// NewWriter creates a new fallback log writer.
func NewWriter(dir string, retention time.Duration) *Writer {
	if dir == "" {
		dir = defaultDir
	}
	if retention <= 0 {
		retention = defaultRetention
	}
	_ = os.MkdirAll(dir, 0755)
	return &Writer{dir: dir, retention: retention}
}

// WriteRecord marshals the record as JSON and appends it to the current hour's NDJSON file.
func (w *Writer) WriteRecord(record interface{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}

	fname := filepath.Join(w.dir, w.currentFileName())
	f, err := os.OpenFile(fname, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open fallback log: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("write fallback log: %w", err)
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		return fmt.Errorf("write newline: %w", err)
	}
	return nil
}

// WriteRecords writes multiple records to the fallback log.
func (w *Writer) WriteRecords(records []interface{}) error {
	for _, rec := range records {
		if err := w.WriteRecord(rec); err != nil {
			return err
		}
	}
	return nil
}

// currentFileName returns the NDJSON filename for the current UTC hour.
func (w *Writer) currentFileName() string {
	return fmt.Sprintf("usage-fallback-%s.ndjson", time.Now().UTC().Format("20060102-150000")[:11]+"0000")
}

// Replay re-sends previously-persisted fallback records to the given send
// function (which must be idempotent on the receiver side — Portal dedupes by
// trace_id). It only processes COMPLETED files (not the current hour's, which
// the writer may still be appending to). A file is deleted only when every
// record in it replays successfully; on any failure the file is left intact for
// the next attempt (at-least-once delivery). Returns (replayed, failed).
func (w *Writer) Replay(send func(line []byte) error) (replayed int, failed int) {
	w.mu.Lock()
	entries, err := os.ReadDir(w.dir)
	current := w.currentFileName()
	w.mu.Unlock()
	if err != nil {
		return 0, 0
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "usage-fallback-") || !strings.HasSuffix(name, ".ndjson") {
			continue
		}
		if name == current {
			continue // still being written this hour
		}
		path := filepath.Join(w.dir, name)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			continue
		}
		fileOK := true
		for _, line := range bytes.Split(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			if serr := send(line); serr != nil {
				fileOK = false
				failed++
				break // stop this file; retry whole file next cycle
			}
			replayed++
		}
		if fileOK {
			w.mu.Lock()
			_ = os.Remove(path)
			w.mu.Unlock()
		}
	}
	return replayed, failed
}

// Cleanup removes NDJSON files older than the retention period.
func (w *Writer) Cleanup() error {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-w.retention)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(w.dir, entry.Name()))
		}
	}
	return nil
}
