package datax

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Import mode constants.
const (
	ModeUpsert = "upsert"
	ModeSkip   = "skip"
)

// ImportOptions configures a data import run.
type ImportOptions struct {
	InDir    string   // directory produced by Export (must contain manifest.json)
	Mode     string   // "upsert" (default) or "skip"
	DryRun   bool     // report counts only; execute nothing
	Entities []string // subset to import; empty = all core entities present
}

// ImportSummary is the per-entity count of rows processed (or that would be
// processed in dry-run mode).
type ImportSummary map[string]int

// Import loads <InDir>/manifest.json and imports each requested entity (in
// whitelist order) from <InDir>/<table>.json into the current database schema.
// Columns are matched by name: extra keys in a file are ignored (with a
// warning) and columns absent from a file are omitted so database defaults
// apply. With DryRun set, no rows are written.
func Import(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger, opts ImportOptions) (ImportSummary, error) {
	if opts.InDir == "" {
		return nil, fmt.Errorf("import: --in directory is required")
	}

	mode := opts.Mode
	if mode == "" {
		mode = ModeUpsert
	}
	if mode != ModeUpsert && mode != ModeSkip {
		return nil, fmt.Errorf("import: invalid --mode %q (want upsert|skip)", mode)
	}

	manifest, err := ReadManifest(opts.InDir)
	if err != nil {
		return nil, err
	}
	logger.Info("import: loaded manifest",
		zap.String("portal_version", manifest.PortalVersion),
		zap.String("schema_version", manifest.SchemaVersion),
		zap.String("exported_at", manifest.ExportedAt),
		zap.Int("entities", len(manifest.Entities)))

	ordered, unknown := ResolveEntities(opts.Entities)
	for _, n := range unknown {
		logger.Warn("import: unknown entity ignored", zap.String("entity", n))
	}

	summary := ImportSummary{}

	for _, table := range ordered {
		path := filepath.Join(opts.InDir, table+".json")
		if _, err := os.Stat(path); err != nil {
			// Not exported (or filtered out at export time) — nothing to do.
			continue
		}

		if entry, ok := manifest.Entities[table]; ok && entry.SHA256 != "" {
			if sum, err := FileSHA256(path); err == nil && sum != entry.SHA256 {
				logger.Warn("import: checksum mismatch (file may have been edited)",
					zap.String("table", table))
			}
		}

		fileRows, err := readEntityFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", table, err)
		}
		if len(fileRows) == 0 {
			logger.Info("import: entity file is empty", zap.String("table", table))
			summary[table] = 0
			continue
		}

		exists, err := TableExists(ctx, pool, table)
		if err != nil {
			return nil, err
		}
		if !exists {
			logger.Warn("import: target table not present in this schema, skipping",
				zap.String("table", table))
			continue
		}

		cols, err := TableColumns(ctx, pool, table)
		if err != nil {
			return nil, err
		}
		pk, err := PrimaryKeyColumns(ctx, pool, table)
		if err != nil {
			return nil, err
		}

		includeCols := columnsToImport(fileRows, cols, logger, table)

		if opts.DryRun {
			logger.Info("import: dry-run",
				zap.String("table", table),
				zap.Int("rows", len(fileRows)),
				zap.String("mode", mode),
				zap.Int("columns", len(includeCols)))
			summary[table] = len(fileRows)
			continue
		}

		if len(includeCols) == 0 {
			logger.Warn("import: no matching columns between file and schema, skipping",
				zap.String("table", table))
			summary[table] = 0
			continue
		}

		insertSQL, err := buildInsertSQL(table, includeCols, pk, mode)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", table, err)
		}

		if err := importRows(ctx, pool, insertSQL, includeCols, fileRows); err != nil {
			return nil, fmt.Errorf("%s: %w", table, err)
		}

		logger.Info("import: applied entity",
			zap.String("table", table),
			zap.Int("rows", len(fileRows)),
			zap.String("mode", mode))
		summary[table] = len(fileRows)
	}

	logger.Info("import: complete",
		zap.Bool("dry_run", opts.DryRun),
		zap.Int("entities", len(summary)))
	return summary, nil
}

// columnsToImport returns the current-schema columns that are present in the
// file rows (in schema order), warning once about file keys that do not exist
// in the current schema.
func columnsToImport(fileRows []map[string]interface{}, cols []Column, logger *zap.Logger, table string) []Column {
	colByName := make(map[string]Column, len(cols))
	for _, c := range cols {
		colByName[c.Name] = c
	}

	fileKeys := map[string]bool{}
	for _, row := range fileRows {
		for k := range row {
			fileKeys[k] = true
		}
	}

	var extra []string
	for k := range fileKeys {
		if _, ok := colByName[k]; !ok {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		logger.Warn("import: file contains columns absent from current schema; ignoring",
			zap.String("table", table),
			zap.Strings("columns", extra))
	}

	include := []Column{}
	for _, c := range cols {
		if fileKeys[c.Name] {
			include = append(include, c)
		}
	}
	return include
}

func buildInsertSQL(table string, cols []Column, pk []string, mode string) (string, error) {
	if err := validateIdentifier(table); err != nil {
		return "", err
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		if err := validateIdentifier(c.Name); err != nil {
			return "", err
		}
		names[i] = c.Name
	}
	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quoteIdent(table), joinQuoted(names), strings.Join(placeholders, ", "))

	if len(pk) == 0 {
		// No primary key: cannot conflict-handle; plain insert.
		return sql, nil
	}
	for _, p := range pk {
		if err := validateIdentifier(p); err != nil {
			return "", err
		}
	}
	conflictTarget := joinQuoted(pk)

	if mode == ModeSkip {
		return sql + fmt.Sprintf(" ON CONFLICT (%s) DO NOTHING", conflictTarget), nil
	}

	// upsert: update every non-PK column from the insert list.
	pkSet := make(map[string]bool, len(pk))
	for _, p := range pk {
		pkSet[p] = true
	}
	var sets []string
	for _, n := range names {
		if pkSet[n] {
			continue
		}
		sets = append(sets, fmt.Sprintf("%s = EXCLUDED.%s", quoteIdent(n), quoteIdent(n)))
	}
	if len(sets) == 0 {
		return sql + fmt.Sprintf(" ON CONFLICT (%s) DO NOTHING", conflictTarget), nil
	}
	return sql + fmt.Sprintf(" ON CONFLICT (%s) DO UPDATE SET %s", conflictTarget, strings.Join(sets, ", ")), nil
}

func importRows(ctx context.Context, pool *pgxpool.Pool, insertSQL string, cols []Column, rows []map[string]interface{}) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Disable user-defined triggers for the load transaction (the same pattern
	// pg_restore uses). Some schemas keep derived rows in sync via triggers —
	// e.g. trg_sync_enterprise_membership auto-creates an enterprise_members row
	// when a user with an enterprise_id is inserted — which would then collide
	// with the explicit enterprise_members rows we load afterwards. The dump is
	// internally consistent and we import in foreign-key order, so suppressing
	// triggers during load yields a faithful restore. SET LOCAL reverts
	// automatically at commit/rollback.
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`); err != nil {
		return fmt.Errorf("disable triggers: %w", err)
	}

	for _, row := range rows {
		args := make([]interface{}, len(cols))
		for i, c := range cols {
			args[i] = ImportArg(row[c.Name], c) // absent key -> nil -> NULL
		}
		if _, err := tx.Exec(ctx, insertSQL, args...); err != nil {
			return fmt.Errorf("exec insert: %w", err)
		}
	}

	return tx.Commit(ctx)
}

func readEntityFile(path string) ([]map[string]interface{}, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	dec.UseNumber() // keep integers exact until we know the column type
	var rows []map[string]interface{}
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return rows, nil
}
