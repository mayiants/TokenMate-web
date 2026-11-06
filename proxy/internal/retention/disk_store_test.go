package retention

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDiskStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewDiskStore(Config{
		Enabled:    true,
		Directory:  dir,
		BatchSize:  1,
		FlushEvery: time.Hour, // manual flush
	}, zap.NewNop())
	require.NoError(t, err)
	defer store.Close()

	rec := &Record{
		TraceID:    "trace-123",
		ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Metadata: Metadata{
			UserID:     "user-1",
			APIKeyHash: "hash-1",
			Provider:   "openai",
			Model:      "gpt-4o",
			StatusCode: 200,
		},
		Request: PayloadPart{
			Method: "POST",
			Path:   "/v1/chat/completions",
			Body:   []byte(`{"model":"gpt-4o"}`),
		},
		Response: PayloadPart{
			Body: []byte(`{"choices":[]}`),
		},
	}

	store.Record(rec)
	store.Flush()

	got, err := store.Get("trace-123")
	require.NoError(t, err)
	assert.Equal(t, "user-1", got.Metadata.UserID)
	assert.Equal(t, "openai", got.Metadata.Provider)
	assert.Equal(t, "trace-123", got.TraceID)
	assert.Equal(t, `{"model":"gpt-4o"}`, string(got.Request.Body))
	assert.Equal(t, `{"choices":[]}`, string(got.Response.Body))
}

func TestDiskStore_GeneratesTraceID(t *testing.T) {
	dir := t.TempDir()
	store, err := NewDiskStore(Config{Enabled: true, Directory: dir, BatchSize: 1}, zap.NewNop())
	require.NoError(t, err)
	defer store.Close()

	rec := &Record{Metadata: Metadata{Model: "gpt-4o"}}
	store.Record(rec)
	store.Flush()

	assert.NotEmpty(t, rec.TraceID)
	assert.NotEmpty(t, rec.SchemaVersion)
	assert.NotEmpty(t, rec.ReceivedAt)

	got, err := store.Get(rec.TraceID)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", got.Metadata.Model)
}

func TestDiskStore_Cleanup(t *testing.T) {
	dir := t.TempDir()
	store, err := NewDiskStore(Config{
		Enabled:    true,
		Directory:  dir,
		MaxAge:     time.Hour,
		BatchSize:  1,
		FlushEvery: time.Hour,
	}, zap.NewNop())
	require.NoError(t, err)
	defer store.Close()

	// Write a file with an old modification time.
	oldDir := filepath.Join(dir, "2024", "01", "01", "00")
	require.NoError(t, os.MkdirAll(oldDir, 0o750))
	oldPath := filepath.Join(oldDir, "old-trace.json.gz")
	require.NoError(t, os.WriteFile(oldPath, []byte("old"), 0o600))
	oldTime := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(oldPath, oldTime, oldTime))

	// Write a fresh file.
	store.Record(&Record{TraceID: "fresh-trace"})
	store.Flush()

	require.NoError(t, store.Cleanup())

	_, err = os.Stat(oldPath)
	assert.True(t, os.IsNotExist(err), "old file should be deleted")

	_, err = store.Get("fresh-trace")
	assert.NoError(t, err)
}
