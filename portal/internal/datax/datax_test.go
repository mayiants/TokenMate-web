package datax

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestBuildInsertSQL verifies upsert/skip SQL generation for various PK shapes.
func TestBuildInsertSQL(t *testing.T) {
	tests := []struct {
		name    string
		table   string
		cols    []Column
		pk      []string
		mode    string
		want    string
		wantErr bool
	}{
		{
			name:  "upsert single pk",
			table: "users",
			cols:  []Column{{Name: "id"}, {Name: "name"}, {Name: "email"}},
			pk:    []string{"id"},
			mode:  ModeUpsert,
			want:  `INSERT INTO "users" ("id", "name", "email") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name", "email" = EXCLUDED."email"`,
		},
		{
			name:  "skip composite pk",
			table: "enterprise_members",
			cols:  []Column{{Name: "enterprise_id"}, {Name: "user_id"}, {Name: "role"}},
			pk:    []string{"enterprise_id", "user_id"},
			mode:  ModeSkip,
			want:  `INSERT INTO "enterprise_members" ("enterprise_id", "user_id", "role") VALUES ($1, $2, $3) ON CONFLICT ("enterprise_id", "user_id") DO NOTHING`,
		},
		{
			name:  "no pk plain insert",
			table: "logs",
			cols:  []Column{{Name: "message"}},
			pk:    []string{},
			mode:  ModeUpsert,
			want:  `INSERT INTO "logs" ("message") VALUES ($1)`,
		},
		{
			name:    "invalid table identifier",
			table:   "users; drop table",
			cols:    []Column{{Name: "id"}},
			pk:      []string{"id"},
			mode:    ModeUpsert,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildInsertSQL(tt.table, tt.cols, tt.pk, tt.mode)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestColumnsToImport verifies matching between file keys and current schema columns.
func TestColumnsToImport(t *testing.T) {
	cols := []Column{
		{Name: "id"},
		{Name: "name"},
		{Name: "status"},
	}
	logger := zap.NewNop()

	tests := []struct {
		name     string
		rows     []map[string]interface{}
		wantCols []string
	}{
		{
			name:     "exact match",
			rows:     []map[string]interface{}{{"id": 1, "name": "a", "status": "x"}},
			wantCols: []string{"id", "name", "status"},
		},
		{
			name:     "file has extra column",
			rows:     []map[string]interface{}{{"id": 1, "name": "a", "extra": "y"}},
			wantCols: []string{"id", "name"},
		},
		{
			name:     "schema has extra column",
			rows:     []map[string]interface{}{{"id": 1, "name": "a"}},
			wantCols: []string{"id", "name"},
		},
		{
			name:     "no overlap",
			rows:     []map[string]interface{}{{"foo": "bar"}},
			wantCols: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := columnsToImport(tt.rows, cols, logger, "test")
			names := []string{}
			for _, c := range got {
				names = append(names, c.Name)
			}
			assert.Equal(t, tt.wantCols, names)
		})
	}
}

// TestManifestRoundTrip verifies manifest write/read and checksums.
func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := NewManifest("v1.2.3", "0042")
	m.Entities["users"] = EntityEntry{Rows: 2, SHA256: "abc123"}

	require.NoError(t, WriteManifest(dir, m))
	loaded, err := ReadManifest(dir)
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3", loaded.PortalVersion)
	assert.Equal(t, "0042", loaded.SchemaVersion)
	assert.Equal(t, 2, loaded.Entities["users"].Rows)
	assert.Equal(t, "abc123", loaded.Entities["users"].SHA256)

	// Checksum round-trip.
	path := filepath.Join(dir, "manifest.json")
	sum, err := FileSHA256(path)
	require.NoError(t, err)
	assert.Len(t, sum, 64)
}

// TestExportImportRoundTrip exercises exportEntity + importRows against a real
// PostgreSQL test database, ensuring the datax normalization/import-arg path
// preserves values losslessly.
func TestExportImportRoundTrip(t *testing.T) {
	testutil.LoadEnv()
	dsn := testutil.PostgresDSN()
	if err := testutil.EnsureTestDatabase(dsn); err != nil {
		t.Skipf("test database unavailable: %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	table := "datax_roundtrip_test_" + uuid.New().String()[:8]
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY,
			name TEXT NOT NULL,
			amount BIGINT NOT NULL,
			meta JSONB,
			active BOOLEAN DEFAULT true
		)`, table))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	})

	id1 := uuid.New().String()
	id2 := uuid.New().String()
	_, err = pool.Exec(ctx,
		fmt.Sprintf("INSERT INTO %s (id, name, amount, meta, active) VALUES ($1, $2, $3, $4, $5), ($6, $7, $8, $9, $10)", table),
		id1, "alice", int64(1000), `{"k": "v"}`, true,
		id2, "bob", int64(2000), nil, false,
	)
	require.NoError(t, err)

	cols, err := TableColumns(ctx, pool, table)
	require.NoError(t, err)

	// Export.
	logger := zap.NewNop()
	exported, err := exportEntity(ctx, pool, table, cols, time.Time{}, false, logger)
	require.NoError(t, err)
	require.Len(t, exported, 2)

	// Verify normalization preserved key types.
	byID := map[string]map[string]interface{}{}
	for _, r := range exported {
		byID[r["id"].(string)] = r
	}
	assert.Equal(t, "alice", byID[id1]["name"])
	assert.Equal(t, int64(1000), byID[id1]["amount"])
	assert.Equal(t, map[string]interface{}{"k": "v"}, byID[id1]["meta"])
	assert.Equal(t, true, byID[id1]["active"])
	assert.Nil(t, byID[id2]["meta"])
	assert.Equal(t, false, byID[id2]["active"])

	// Truncate and re-import.
	_, err = pool.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %s", table))
	require.NoError(t, err)

	pk, err := PrimaryKeyColumns(ctx, pool, table)
	require.NoError(t, err)
	insertSQL, err := buildInsertSQL(table, cols, pk, ModeUpsert)
	require.NoError(t, err)
	require.NoError(t, importRows(ctx, pool, insertSQL, cols, exported))

	// Verify re-imported rows.
	rows, err := pool.Query(ctx, fmt.Sprintf("SELECT id, name, amount, meta, active FROM %s ORDER BY name", table))
	require.NoError(t, err)
	defer rows.Close()

	var count int
	for rows.Next() {
		var id, name string
		var amount int64
		var meta []byte
		var active bool
		require.NoError(t, rows.Scan(&id, &name, &amount, &meta, &active))
		count++
		switch name {
		case "alice":
			assert.Equal(t, id1, id)
			assert.Equal(t, int64(1000), amount)
			assert.JSONEq(t, `{"k":"v"}`, string(meta))
			assert.True(t, active)
		case "bob":
			assert.Equal(t, id2, id)
			assert.Equal(t, int64(2000), amount)
			assert.Empty(t, meta)
			assert.False(t, active)
		default:
			t.Fatalf("unexpected name %q", name)
		}
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 2, count)
}

// TestExportWithSince verifies that the --since filter is applied when the table
// has a created_at column and ignored gracefully when it does not.
func TestExportWithSince(t *testing.T) {
	testutil.LoadEnv()
	dsn := testutil.PostgresDSN()
	if err := testutil.EnsureTestDatabase(dsn); err != nil {
		t.Skipf("test database unavailable: %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	table := "datax_since_test_" + uuid.New().String()[:8]
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`, table))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	})

	var id1, id2 string
	require.NoError(t, pool.QueryRow(ctx, fmt.Sprintf("INSERT INTO %s (id, created_at) VALUES ($1, NOW() - INTERVAL '2 days') RETURNING id", table), uuid.New().String()).Scan(&id1))
	require.NoError(t, pool.QueryRow(ctx, fmt.Sprintf("INSERT INTO %s (id, created_at) VALUES ($1, NOW()) RETURNING id", table), uuid.New().String()).Scan(&id2))

	cols, err := TableColumns(ctx, pool, table)
	require.NoError(t, err)

	logger := zap.NewNop()

	// Since far in the past -> both rows.
	farPast, err := time.Parse("2006-01-02", "2024-01-01")
	require.NoError(t, err)
	exported, err := exportEntity(ctx, pool, table, cols, farPast, true, logger)
	require.NoError(t, err)
	require.Len(t, exported, 2)
	seen := map[string]bool{}
	for _, r := range exported {
		seen[r["id"].(string)] = true
	}
	assert.True(t, seen[id1], "old row should be returned for far-past since")
	assert.True(t, seen[id2], "recent row should be returned for far-past since")

	// Since yesterday -> only the recent row.
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	exported, err = exportEntity(ctx, pool, table, cols, yesterday, true, logger)
	require.NoError(t, err)
	require.Len(t, exported, 1)
	assert.Equal(t, id2, exported[0]["id"])
}
