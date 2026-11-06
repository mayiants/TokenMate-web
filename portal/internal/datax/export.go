package datax

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// ExportOptions configures a data export run.
type ExportOptions struct {
	OutDir        string   // destination directory (created if missing)
	Entities      []string // subset to export; empty = all core entities
	Since         string   // optional YYYY-MM-DD lower bound on created_at
	PortalVersion string   // recorded in the manifest; defaults to PORTAL_VERSION/"dev"
}

// Export dumps each requested core entity that exists in the database to
// <OutDir>/<table>.json and writes a manifest.json. Tables missing from the
// database are skipped with a warning so the tool works across schema versions.
func Export(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger, opts ExportOptions) (*Manifest, error) {
	if opts.OutDir == "" {
		return nil, fmt.Errorf("export: --out directory is required")
	}

	ordered, unknown := ResolveEntities(opts.Entities)
	for _, n := range unknown {
		logger.Warn("export: unknown entity ignored", zap.String("entity", n))
	}

	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	var since time.Time
	if opts.Since != "" {
		t, err := time.Parse("2006-01-02", opts.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid --since %q (want YYYY-MM-DD): %w", opts.Since, err)
		}
		since = t.UTC()
	}

	portalVersion := opts.PortalVersion
	if portalVersion == "" {
		portalVersion = os.Getenv("PORTAL_VERSION")
	}
	if portalVersion == "" {
		portalVersion = "dev"
	}

	manifest := NewManifest(portalVersion, detectSchemaVersion(ctx, pool))

	for _, table := range ordered {
		exists, err := TableExists(ctx, pool, table)
		if err != nil {
			return nil, err
		}
		if !exists {
			logger.Warn("export: table not present in this schema, skipping", zap.String("table", table))
			continue
		}

		cols, err := TableColumns(ctx, pool, table)
		if err != nil {
			return nil, err
		}
		if len(cols) == 0 {
			logger.Warn("export: table has no columns, skipping", zap.String("table", table))
			continue
		}

		rows, err := exportEntity(ctx, pool, table, cols, since, opts.Since != "", logger)
		if err != nil {
			return nil, err
		}

		path := filepath.Join(opts.OutDir, table+".json")
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", table, err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}

		sum, err := FileSHA256(path)
		if err != nil {
			return nil, fmt.Errorf("checksum %s: %w", path, err)
		}
		manifest.Entities[table] = EntityEntry{Rows: len(rows), SHA256: sum}

		logger.Info("export: wrote entity",
			zap.String("table", table),
			zap.Int("rows", len(rows)),
			zap.String("file", path))
	}

	if err := WriteManifest(opts.OutDir, manifest); err != nil {
		return nil, err
	}
	logger.Info("export: complete",
		zap.String("dir", opts.OutDir),
		zap.Int("entities", len(manifest.Entities)))

	return manifest, nil
}

func exportEntity(ctx context.Context, pool *pgxpool.Pool, table string, cols []Column, since time.Time, sinceSet bool, logger *zap.Logger) ([]map[string]interface{}, error) {
	colNames := make([]string, len(cols))
	for i, c := range cols {
		colNames[i] = c.Name
	}

	query := fmt.Sprintf("SELECT %s FROM %s", joinQuoted(colNames), quoteIdent(table))

	var args []interface{}
	if sinceSet {
		if HasColumn(cols, "created_at") {
			query += ` WHERE "created_at" > $1`
			args = append(args, since)
		} else {
			logger.Warn("export: --since ignored (table has no created_at column)", zap.String("table", table))
		}
	}

	if pk, err := PrimaryKeyColumns(ctx, pool, table); err == nil && len(pk) > 0 {
		query += " ORDER BY " + joinQuoted(pk)
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", table, err)
	}
	defer rows.Close()

	out := []map[string]interface{}{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("read %s row: %w", table, err)
		}
		row := make(map[string]interface{}, len(cols))
		for i, v := range vals {
			row[cols[i].Name] = NormalizeExportValue(v, cols[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", table, err)
	}
	return out, nil
}

// detectSchemaVersion returns the highest applied migration version, or "" when
// versioned migrations are not in use.
func detectSchemaVersion(ctx context.Context, pool *pgxpool.Pool) string {
	var version string
	err := pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), '') FROM schema_migrations`).Scan(&version)
	if err != nil {
		return ""
	}
	return version
}
