package analytics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ErrAnalysisRunning is returned when an analysis run is requested while a
// previous run is still in progress. A full scan over ~90 days of retention
// data takes minutes; overlapping runs would duplicate that load and race on
// the same report file name.
var ErrAnalysisRunning = errors.New("analysis already in progress")

// Scheduler runs the analyzer periodically and exposes the latest report.
type Scheduler struct {
	analyzer  *Analyzer
	holder    *ReportHolder
	interval  time.Duration
	reportDir string // directory to persist JSON reports
	logger    *zap.Logger

	stopCh chan struct{}
	once   sync.Once
	runMu  sync.Mutex // guards runAnalysis: one scan at a time
}

// SchedulerConfig configures the analytics scheduler.
type SchedulerConfig struct {
	RetentionDir string        // path to retention data
	ReportDir    string        // path to store JSON reports
	Interval     time.Duration // how often to run (default: 6h)
	MaxAge       time.Duration // analyze records within this window (default: 30d)
}

// NewScheduler creates a new analytics scheduler.
func NewScheduler(cfg SchedulerConfig, logger *zap.Logger) *Scheduler {
	if cfg.Interval <= 0 {
		cfg.Interval = 6 * time.Hour
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 30 * 24 * time.Hour
	}
	if cfg.ReportDir == "" {
		cfg.ReportDir = filepath.Join(cfg.RetentionDir, ".analytics")
	}

	return &Scheduler{
		analyzer:  NewAnalyzer(cfg.RetentionDir, cfg.MaxAge, logger),
		holder:    &ReportHolder{},
		interval:  cfg.Interval,
		reportDir: cfg.ReportDir,
		logger:    logger,
		stopCh:    make(chan struct{}),
	}
}

// Start begins the periodic analysis loop.
// It runs an initial analysis immediately, then repeats at the configured interval.
func (s *Scheduler) Start() {
	// Ensure report directory exists
	if err := os.MkdirAll(s.reportDir, 0o750); err != nil {
		s.logger.Error("Failed to create analytics report directory", zap.Error(err))
	}

	// Try to load the last persisted report
	s.loadLastReport()

	// Run initial analysis in background (don't block startup)
	go func() {
		// Small delay to let the proxy fully start
		time.Sleep(30 * time.Second)
		// Errors are logged inside runAnalysis.
		_, _ = s.runAnalysis()

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Errors are logged inside runAnalysis.
				_, _ = s.runAnalysis()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop halts the scheduler.
func (s *Scheduler) Stop() {
	s.once.Do(func() {
		close(s.stopCh)
	})
}

// GetReport returns the latest analytics report.
func (s *Scheduler) GetReport() *Report {
	return s.holder.Get()
}

// RunNow triggers an immediate analysis (useful for admin API).
// Returns ErrAnalysisRunning if a previous run is still in progress.
func (s *Scheduler) RunNow() (*Report, error) {
	return s.runAnalysis()
}

func (s *Scheduler) runAnalysis() (*Report, error) {
	// Skip if a previous scan is still running (periodic tick overlapping a
	// slow scan, or a concurrent admin refresh). Without this guard two runs
	// would scan the same ~90 days of gzip data at once and their reports —
	// timestamped to the second — could overwrite each other.
	if !s.runMu.TryLock() {
		s.logger.Warn("Retention analytics skipped: previous run still in progress")
		return nil, ErrAnalysisRunning
	}
	defer s.runMu.Unlock()

	start := time.Now()
	s.logger.Info("Starting retention analytics...")

	report, err := s.analyzer.Analyze()
	if err != nil {
		s.logger.Error("Retention analytics failed", zap.Error(err))
		return nil, err
	}

	s.holder.Set(report)
	elapsed := time.Since(start)

	s.logger.Info("Retention analytics completed",
		zap.Int("records", report.TotalRecords),
		zap.Int("models", report.Summary.UniqueModels),
		zap.Int("users", report.Summary.UniqueUsers),
		zap.Duration("elapsed", elapsed),
	)

	// Persist to disk
	s.persistReport(report)

	return report, nil
}

func (s *Scheduler) persistReport(report *Report) {
	// Write timestamped file (never overwrite history)
	ts := report.GeneratedAt.Format("20060102-150405")
	timestampedFile := filepath.Join(s.reportDir, fmt.Sprintf("report-%s.json", ts))
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		s.logger.Error("Failed to marshal analytics report", zap.Error(err))
		return
	}
	if err := os.WriteFile(timestampedFile, data, 0o640); err != nil {
		s.logger.Error("Failed to write analytics report", zap.Error(err))
		return
	}

	// Also write latest.json as a copy for quick API access and startup loading
	latestFile := filepath.Join(s.reportDir, "latest.json")
	if err := os.WriteFile(latestFile, data, 0o640); err != nil {
		s.logger.Error("Failed to write latest.json", zap.Error(err))
	}

	// Cleanup: remove report files older than 90 days (aligned with retention max_age)
	s.cleanupOldReports()
}

func (s *Scheduler) cleanupOldReports() {
	cutoff := time.Now().Add(-90 * 24 * time.Hour)
	entries, err := os.ReadDir(s.reportDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "latest.json" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(s.reportDir, e.Name()))
		}
	}
}

func (s *Scheduler) loadLastReport() {
	filename := filepath.Join(s.reportDir, "latest.json")
	data, err := os.ReadFile(filename)
	if err != nil {
		return // no previous report
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return
	}
	s.holder.Set(&report)
	s.logger.Info("Loaded previous analytics report",
		zap.Time("generated_at", report.GeneratedAt),
		zap.Int("records", report.TotalRecords),
	)
}
