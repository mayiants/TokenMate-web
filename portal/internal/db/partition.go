package db

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// partitionLookbackMonths is how many historical months to ensure.
const partitionLookbackMonths = 12

// partitionLookaheadMonths is how many future months to ensure.
const partitionLookaheadMonths = 3

// EnsureUsagePartitions creates monthly range partitions for usage_records so
// the default partition does not grow unbounded. It creates partitions for the
// last partitionLookbackMonths and the next partitionLookaheadMonths.
// Calling it repeatedly is safe because each partition is created with
// IF NOT EXISTS.
func (db *DB) EnsureUsagePartitions(ctx context.Context, logger *zap.Logger) error {
	now := time.Now().UTC()
	start := now.AddDate(0, -partitionLookbackMonths, 0)
	end := now.AddDate(0, partitionLookaheadMonths+1, 0)
	return db.createMonthlyPartitions(ctx, start, end)
}

func (db *DB) createMonthlyPartitions(ctx context.Context, start, end time.Time) error {
	for t := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC); !t.After(end); t = t.AddDate(0, 1, 0) {
		next := t.AddDate(0, 1, 0)
		name := fmt.Sprintf("usage_records_%s", t.Format("2006_01"))
		from := t.Format("2006-01-02")
		to := next.Format("2006-01-02")
		sql := fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s PARTITION OF usage_records FOR VALUES FROM ('%s') TO ('%s')",
			name, from, to,
		)
		if _, err := db.Pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("create partition %s (%s to %s): %w", name, from, to, err)
		}
	}
	return nil
}

// DropOldUsagePartitions drops monthly partitions of usage_records that are
// older than retentionMonths. If retentionMonths <= 0 or the environment
// variable USAGE_RECORDS_RETENTION_MONTHS is not set, this function is a no-op
// so historical data is not destroyed by default.
func (db *DB) DropOldUsagePartitions(ctx context.Context, logger *zap.Logger) error {
	retentionMonths := 0
	if v := os.Getenv("USAGE_RECORDS_RETENTION_MONTHS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			retentionMonths = parsed
		}
	}
	if retentionMonths <= 0 {
		return nil
	}

	cutoff := time.Now().UTC().AddDate(0, -retentionMonths, 0)
	cutoff = time.Date(cutoff.Year(), cutoff.Month(), 1, 0, 0, 0, 0, time.UTC)

	rows, err := db.Pool.Query(ctx,
		`SELECT inhrelid::regclass::text
		 FROM pg_inherits
		 WHERE inhparent = 'usage_records'::regclass`)
	if err != nil {
		return fmt.Errorf("list usage_records partitions: %w", err)
	}
	defer rows.Close()

	var dropped int
	for rows.Next() {
		var partitionName string
		if err := rows.Scan(&partitionName); err != nil {
			return fmt.Errorf("scan partition name: %w", err)
		}
		// Partition names are formatted as usage_records_YYYY_MM.
		var year, month int
		if _, err := fmt.Sscanf(partitionName, "usage_records_%d_%d", &year, &month); err != nil {
			continue
		}
		partitionTime := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		if partitionTime.Before(cutoff) {
			sql := fmt.Sprintf("DROP TABLE IF EXISTS %s", partitionName)
			if _, err := db.Pool.Exec(ctx, sql); err != nil {
				return fmt.Errorf("drop partition %s: %w", partitionName, err)
			}
			dropped++
			logger.Info("Dropped old usage_records partition", zap.String("partition", partitionName))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate partitions: %w", err)
	}
	if dropped > 0 {
		logger.Info("Old usage_records partition cleanup completed", zap.Int("dropped", dropped))
	}
	return nil
}

// StartUsagePartitionMaintenance ensures partitions immediately and then runs
// a daily background job to create upcoming partitions. The goroutine exits
// when ctx is cancelled.
func (db *DB) StartUsagePartitionMaintenance(ctx context.Context, logger *zap.Logger) {
	if err := db.EnsureUsagePartitions(ctx, logger); err != nil {
		logger.Error("Failed to ensure usage_records partitions", zap.Error(err))
	}

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := db.EnsureUsagePartitions(context.Background(), logger); err != nil {
					logger.Error("Failed to maintain usage_records partitions", zap.Error(err))
				}
				if err := db.DropOldUsagePartitions(context.Background(), logger); err != nil {
					logger.Error("Failed to drop old usage_records partitions", zap.Error(err))
				}
			}
		}
	}()
}
