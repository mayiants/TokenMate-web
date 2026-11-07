package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const (
	defaultBufferSize    = 1000
	defaultBatchSize     = 100
	defaultFlushInterval = 1 * time.Second
	defaultStopTimeout   = 5 * time.Second
)

// criticalCategories are audited via a synchronous DB write when the async
// buffer is full, because dropping them would create a compliance hole for
// billing / security / key-management operations. Lower-stakes categories
// (e.g. "navigation") are still best-effort and may be dropped under load.
var criticalCategories = map[string]bool{
	"security": true,
	"billing":  true,
	"apikeys":  true,
	"auth":     true,
}

// entry is the internal queued representation of an audit log row.
type entry struct {
	UserID     string
	UserEmail  string
	Category   string
	Action     string
	Level      string
	Message    string
	Details    string
	TargetType string
	TargetID   string
	OldValue   string
	NewValue   string
	IPAddress  string
	UserAgent  string
}

// Logger buffers audit log entries and flushes them to the database in batches.
// It keeps the same method signatures as the original synchronous logger so
// callers do not need to change.
type Logger struct {
	db     *db.DB
	logger *zap.Logger

	buffer     chan entry
	batchSize  int
	flushEvery time.Duration
	stopOnce   sync.Once
	stopCh     chan struct{}
	wg         sync.WaitGroup

	// dropped counts entries that could not be enqueued because the buffer
	// was full and could not be synchronously flushed (non-critical
	// categories, or sync flush failures). Exposed via DroppedCount().
	dropped atomic.Int64
}

// NewLogger creates an asynchronous audit logger that batches writes to the
// database. A background worker is started automatically; call Stop before
// process exit to flush pending entries.
func NewLogger(db *db.DB, logger *zap.Logger) *Logger {
	l := &Logger{
		db:         db,
		logger:     logger,
		buffer:     make(chan entry, defaultBufferSize),
		batchSize:  defaultBatchSize,
		flushEvery: defaultFlushInterval,
		stopCh:     make(chan struct{}),
	}

	if db != nil {
		l.wg.Add(1)
		go l.worker()
	}
	return l
}

// NewNoopLogger returns a logger with no database connection. All logged
// entries are silently dropped.
func NewNoopLogger(logger *zap.Logger) *Logger {
	return &Logger{logger: logger}
}

// Stop gracefully shuts down the audit logger, flushing any buffered entries
// within the given context timeout. It is safe to call multiple times.
func (l *Logger) Stop(ctx context.Context) {
	if l.db == nil {
		return
	}
	l.stopOnce.Do(func() {
		close(l.stopCh)
		// Drain buffered entries before waiting for the worker to finish.
		done := make(chan struct{})
		go func() {
			defer close(done)
			l.wg.Wait()
		}()

		select {
		case <-done:
		case <-ctx.Done():
			if l.logger != nil {
				l.logger.Warn("Audit logger flush timed out", zap.Error(ctx.Err()))
			}
		}
	})
}

func (l *Logger) resolveEmail(ctx context.Context, userID, userEmail string) string {
	if userEmail != "" || userID == "" || l.db == nil {
		return userEmail
	}
	var email string
	_ = l.db.QueryRow(ctx,
		`SELECT COALESCE(email, '') FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&email)
	return email
}

func (l *Logger) enqueue(ctx context.Context, e entry) {
	if l.db == nil {
		return
	}
	// Fill client info from the request context unless the caller supplied it
	// explicitly (LogWithIP). Read here, not at flush time: the entry is
	// written asynchronously after the request context may be gone.
	if e.IPAddress == "" {
		e.IPAddress = ClientIPFromContext(ctx)
	}
	if e.UserAgent == "" {
		e.UserAgent = UserAgentFromContext(ctx)
	}
	e.UserAgent = NormalizeUserAgent(e.UserAgent)
	select {
	case l.buffer <- e:
	default:
		// Buffer full. For compliance-sensitive categories fall back to a
		// synchronous write so billing / key-management events are not
		// silently lost; everything else is dropped but counted.
		if criticalCategories[e.Category] {
			if err := l.syncFlush(ctx, e); err != nil {
				l.dropped.Add(1)
				if l.logger != nil {
					l.logger.Error("Audit sync fallback failed, entry dropped",
						zap.String("category", e.Category),
						zap.String("action", e.Action),
						zap.Error(err),
					)
				}
				return
			}
			if l.logger != nil {
				l.logger.Warn("Audit buffer full, critical entry written synchronously",
					zap.String("category", e.Category),
					zap.String("action", e.Action),
				)
			}
			return
		}
		l.dropped.Add(1)
		if l.logger != nil {
			l.logger.Warn("Audit log buffer full, dropping entry",
				zap.String("category", e.Category),
				zap.String("action", e.Action),
			)
		}
	}
}

// DroppedCount returns the total number of audit entries that were dropped
// because the buffer was full and could not be (synchronously) flushed. It
// never resets within a process lifetime; scrape and diff it for a rate.
func (l *Logger) DroppedCount() int64 {
	return l.dropped.Load()
}

// syncFlush writes a single entry directly to the database, bypassing the
// async buffer. Used as the fallback path for critical audit categories when
// the buffer is full.
func (l *Logger) syncFlush(ctx context.Context, e entry) error {
	if l.db == nil || l.db.Pool == nil {
		return fmt.Errorf("audit db not available")
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := l.flushBatchSingle(flushCtx, e); err != nil {
		return err
	}
	return nil
}

// flushBatchSingle inserts a single entry using CopyFrom. It reuses the same
// column layout as flush() so rows stay consistent.
func (l *Logger) flushBatchSingle(ctx context.Context, e entry) (int64, error) {
	userID := interface{}(e.UserID)
	if e.UserID == "" {
		userID = nil
	}
	// Invalid IPs become NULL: one bad INET value would fail the whole COPY.
	ipAddr := nullableIP(e.IPAddress)
	userAgent := nullableText(e.UserAgent)
	details := interface{}(e.Details)
	if e.Details == "" {
		details = nil
	}
	oldValue := interface{}(e.OldValue)
	if e.OldValue == "" {
		oldValue = nil
	}
	newValue := interface{}(e.NewValue)
	if e.NewValue == "" {
		newValue = nil
	}
	row := []interface{}{
		userID, e.UserEmail, e.Category, e.Action, e.Level, e.Message,
		details, e.TargetType, e.TargetID, oldValue, newValue, ipAddr, userAgent,
	}
	return l.db.Pool.CopyFrom(
		ctx,
		pgx.Identifier{"audit_logs"},
		[]string{
			"user_id", "user_email", "category", "action", "level", "message",
			"details", "target_type", "target_id", "old_value", "new_value", "ip_address", "user_agent",
		},
		pgx.CopyFromRows([][]interface{}{row}),
	)
}

func (l *Logger) Log(ctx context.Context, userID string, userEmail, category, action, level, message string) {
	if l.db == nil {
		return
	}
	l.enqueue(ctx, entry{
		UserID:    userID,
		UserEmail: l.resolveEmail(ctx, userID, userEmail),
		Category:  category,
		Action:    action,
		Level:     level,
		Message:   message,
	})
}

func (l *Logger) LogWithDetails(ctx context.Context, userID string, userEmail, category, action, level, message string, details interface{}) {
	if l.db == nil {
		return
	}
	detailsJSON, _ := json.Marshal(details)
	l.enqueue(ctx, entry{
		UserID:    userID,
		UserEmail: l.resolveEmail(ctx, userID, userEmail),
		Category:  category,
		Action:    action,
		Level:     level,
		Message:   message,
		Details:   string(detailsJSON),
	})
}

// LogStructured records an audit log with target info and change diff
func (l *Logger) LogStructured(ctx context.Context, userID, userEmail, category, action, level, message string,
	targetType, targetID string, oldValue, newValue interface{}) {
	if l.db == nil {
		return
	}
	var oldJSON, newJSON string
	if oldValue != nil {
		b, _ := json.Marshal(oldValue)
		oldJSON = string(b)
	}
	if newValue != nil {
		b, _ := json.Marshal(newValue)
		newJSON = string(b)
	}
	l.enqueue(ctx, entry{
		UserID:     userID,
		UserEmail:  l.resolveEmail(ctx, userID, userEmail),
		Category:   category,
		Action:     action,
		Level:      level,
		Message:    message,
		TargetType: targetType,
		TargetID:   targetID,
		OldValue:   oldJSON,
		NewValue:   newJSON,
	})
}

// LogWithIP records an audit log with the client IP address
func (l *Logger) LogWithIP(ctx context.Context, userID, userEmail, category, action, level, message, ipAddress string) {
	if l.db == nil {
		return
	}
	l.enqueue(ctx, entry{
		UserID:    userID,
		UserEmail: l.resolveEmail(ctx, userID, userEmail),
		Category:  category,
		Action:    action,
		Level:     level,
		Message:   message,
		IPAddress: ipAddress,
	})
}

// worker reads entries from the buffer and flushes them in batches.
func (l *Logger) worker() {
	defer l.wg.Done()

	ticker := time.NewTicker(l.flushEvery)
	defer ticker.Stop()

	batch := make([]entry, 0, l.batchSize)

	flush := func(reason string) {
		if len(batch) == 0 {
			return
		}
		if err := l.flush(batch); err != nil {
			if l.logger != nil {
				l.logger.Error("Audit batch flush failed",
					zap.String("reason", reason),
					zap.Int("count", len(batch)),
					zap.Error(err),
				)
			}
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-l.stopCh:
			// Drain remaining buffered entries.
			for {
				select {
				case e := <-l.buffer:
					batch = append(batch, e)
					if len(batch) >= l.batchSize {
						flush("drain")
					}
				default:
					flush("shutdown")
					return
				}
			}
		case e := <-l.buffer:
			batch = append(batch, e)
			if len(batch) >= l.batchSize {
				flush("size")
			}
		case <-ticker.C:
			flush("interval")
		}
	}
}

// flush inserts a batch of audit entries using pgx CopyFrom for efficiency.
func (l *Logger) flush(batch []entry) error {
	if l.db == nil || l.db.Pool == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows := make([][]interface{}, len(batch))
	for i, e := range batch {
		userID := interface{}(e.UserID)
		if e.UserID == "" {
			userID = nil
		}
		ipAddr := nullableIP(e.IPAddress)
		userAgent := nullableText(e.UserAgent)
		details := interface{}(e.Details)
		if e.Details == "" {
			details = nil
		}
		oldValue := interface{}(e.OldValue)
		if e.OldValue == "" {
			oldValue = nil
		}
		newValue := interface{}(e.NewValue)
		if e.NewValue == "" {
			newValue = nil
		}
		rows[i] = []interface{}{
			userID, e.UserEmail, e.Category, e.Action, e.Level, e.Message,
			details, e.TargetType, e.TargetID, oldValue, newValue, ipAddr, userAgent,
		}
	}

	_, err := l.db.Pool.CopyFrom(
		ctx,
		pgx.Identifier{"audit_logs"},
		[]string{
			"user_id", "user_email", "category", "action", "level", "message",
			"details", "target_type", "target_id", "old_value", "new_value", "ip_address", "user_agent",
		},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copy audit logs: %w", err)
	}
	return nil
}

type AuditLog struct {
	ID         int64     `json:"id"`
	UserID     string    `json:"user_id"`
	UserEmail  string    `json:"user_email"`
	Category   string    `json:"category"`
	Action     string    `json:"action"`
	Level      string    `json:"level"`
	Message    string    `json:"message"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	OldValue   string    `json:"old_value"`
	NewValue   string    `json:"new_value"`
	IPAddress  string    `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
}
