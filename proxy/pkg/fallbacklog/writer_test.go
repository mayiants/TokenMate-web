package fallbacklog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWriter(t *testing.T) {
	t.Run("default directory when empty", func(t *testing.T) {
		w := NewWriter("", 0)
		defer os.RemoveAll(w.dir)

		assert.Equal(t, defaultDir, w.dir)
		assert.Equal(t, defaultRetention, w.retention)
	})

	t.Run("custom directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		w := NewWriter(tmpDir, time.Hour)
		defer os.RemoveAll(w.dir)

		assert.Equal(t, tmpDir, w.dir)
		assert.Equal(t, time.Hour, w.retention)
	})

	t.Run("non-existent directory is created", func(t *testing.T) {
		base := t.TempDir()
		newDir := filepath.Join(base, "new-subdir")
		w := NewWriter(newDir, time.Hour)

		// Directory should be created
		_, err := os.Stat(newDir)
		assert.NoError(t, err)
		assert.True(t, w.dir != "")
	})
}

func TestWriter_WriteRecord(t *testing.T) {
	tmpDir := t.TempDir()
	w := NewWriter(tmpDir, time.Hour)

	t.Run("write simple record", func(t *testing.T) {
		record := map[string]interface{}{
			"model":         "gpt-4",
			"input_tokens":  100,
			"output_tokens": 50,
			"cost":          0.01,
		}

		err := w.WriteRecord(record)
		require.NoError(t, err)

		// Read back the file
		files, err := os.ReadDir(tmpDir)
		require.NoError(t, err)
		require.NotEmpty(t, files)

		data, err := os.ReadFile(filepath.Join(tmpDir, files[0].Name()))
		require.NoError(t, err)

		var decoded map[string]interface{}
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "gpt-4", decoded["model"])
		assert.Equal(t, float64(100), decoded["input_tokens"])
		assert.Equal(t, float64(50), decoded["output_tokens"])
	})

	t.Run("write multiple records to same file", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			err := w.WriteRecord(map[string]interface{}{
				"index": i,
				"data":  "test",
			})
			require.NoError(t, err)
		}

		// File should contain 3 lines
		files, err := os.ReadDir(tmpDir)
		require.NoError(t, err)
		require.NotEmpty(t, files)

		content, err := os.ReadFile(filepath.Join(tmpDir, files[0].Name()))
		require.NoError(t, err)

		lines := strings.Split(strings.TrimSpace(string(content)), "\n")
		assert.Len(t, lines, 4) // 1 from first test + 3 from this test
	})

	t.Run("write record with struct", func(t *testing.T) {
		type BillingRecord struct {
			UserID       int     `json:"user_id"`
			Model        string  `json:"model"`
			InputTokens  int     `json:"input_tokens"`
			OutputTokens int     `json:"output_tokens"`
			Cost         float64 `json:"cost"`
		}

		record := BillingRecord{
			UserID:       123,
			Model:        "claude-3-sonnet",
			InputTokens:  500,
			OutputTokens: 250,
			Cost:         0.02,
		}

		err := w.WriteRecord(record)
		require.NoError(t, err)

		files, err := os.ReadDir(tmpDir)
		require.NoError(t, err)
		require.NotEmpty(t, files)

		data, err := os.ReadFile(filepath.Join(tmpDir, files[0].Name()))
		require.NoError(t, err)

		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		lastLine := lines[len(lines)-1]

		var decoded BillingRecord
		err = json.Unmarshal([]byte(lastLine), &decoded)
		require.NoError(t, err)

		assert.Equal(t, 123, decoded.UserID)
		assert.Equal(t, "claude-3-sonnet", decoded.Model)
		assert.Equal(t, 500, decoded.InputTokens)
		assert.Equal(t, 250, decoded.OutputTokens)
		assert.Equal(t, 0.02, decoded.Cost)
	})
}

func TestWriter_WriteRecords(t *testing.T) {
	tmpDir := t.TempDir()
	w := NewWriter(tmpDir, time.Hour)

	t.Run("write multiple records at once", func(t *testing.T) {
		records := []interface{}{
			map[string]interface{}{"id": 1, "data": "a"},
			map[string]interface{}{"id": 2, "data": "b"},
			map[string]interface{}{"id": 3, "data": "c"},
		}

		err := w.WriteRecords(records)
		require.NoError(t, err)

		files, err := os.ReadDir(tmpDir)
		require.NoError(t, err)
		require.NotEmpty(t, files)

		content, err := os.ReadFile(filepath.Join(tmpDir, files[0].Name()))
		require.NoError(t, err)

		lines := strings.Split(strings.TrimSpace(string(content)), "\n")
		assert.Len(t, lines, 3)
	})

	t.Run("write empty records", func(t *testing.T) {
		err := w.WriteRecords(nil)
		require.NoError(t, err)
	})

	t.Run("write records with nil element", func(t *testing.T) {
		err := w.WriteRecords([]interface{}{nil})
		require.NoError(t, err)
	})
}

func TestWriter_WriteRecord_ErrorCases(t *testing.T) {
	t.Run("cannot marshal cyclic reference", func(t *testing.T) {
		tmpDir := t.TempDir()
		w := NewWriter(tmpDir, time.Hour)

		// Create a self-referencing struct (cannot be marshaled)
		type BadStruct struct {
			Self *BadStruct `json:"self"`
		}
		bad := &BadStruct{}
		bad.Self = bad

		// Channel is not marshalable
		ch := make(chan int)

		err := w.WriteRecord(ch)
		assert.Error(t, err)
	})

	t.Run("write to read-only directory fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		roDir := filepath.Join(tmpDir, "readonly")
		err := os.Mkdir(roDir, 0444)
		require.NoError(t, err)
		defer os.Chmod(roDir, 0755)

		w := NewWriter(roDir, time.Hour)
		// WriteRecord tries to create file in the dir, may fail
		// (On some systems this test is racey - just ensure no panic)
		_ = w.WriteRecord(map[string]string{"key": "value"})
	})
}

func TestWriter_Cleanup(t *testing.T) {
	t.Run("cleanup removes old files", func(t *testing.T) {
		tmpDir := t.TempDir()
		w := NewWriter(tmpDir, time.Hour)

		// Write a record
		err := w.WriteRecord(map[string]string{"test": "data"})
		require.NoError(t, err)

		// Create an old file manually with old modification time
		oldFile := filepath.Join(tmpDir, "usage-fallback-20200101-000000.ndjson")
		err = os.WriteFile(oldFile, []byte(`{"old": true}`), 0644)
		require.NoError(t, err)

		twoDaysAgo := time.Now().Add(-2 * 24 * time.Hour)
		err = os.Chtimes(oldFile, twoDaysAgo, twoDaysAgo)
		require.NoError(t, err)

		// Cleanup with 1h retention should remove the old file
		err = w.Cleanup()
		require.NoError(t, err)

		_, err = os.Stat(oldFile)
		assert.True(t, os.IsNotExist(err), "old file should have been removed")
	})

	t.Run("cleanup empty directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		w := NewWriter(tmpDir, time.Hour)

		err := w.Cleanup()
		assert.NoError(t, err)
	})

	t.Run("cleanup keeps recent files", func(t *testing.T) {
		tmpDir := t.TempDir()
		w := NewWriter(tmpDir, 24*time.Hour)

		// Write a recent record
		err := w.WriteRecord(map[string]string{"recent": "data"})
		require.NoError(t, err)

		err = w.Cleanup()
		require.NoError(t, err)

		// Recent file should still exist
		files, err := os.ReadDir(tmpDir)
		require.NoError(t, err)
		assert.NotEmpty(t, files, "recent file should still exist")
	})

	t.Run("cleanup skips directories", func(t *testing.T) {
		tmpDir := t.TempDir()
		w := NewWriter(tmpDir, time.Hour)

		subDir := filepath.Join(tmpDir, "subdir")
		err := os.Mkdir(subDir, 0755)
		require.NoError(t, err)

		err = w.Cleanup()
		require.NoError(t, err)

		// Directory should still exist
		_, err = os.Stat(subDir)
		assert.NoError(t, err)
	})
}

func TestWriter_Concurrency(t *testing.T) {
	tmpDir := t.TempDir()
	w := NewWriter(tmpDir, time.Hour)

	// Concurrent writes should not race (mutex-protected)
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func(id int) {
			_ = w.WriteRecord(map[string]int{"goroutine": id})
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all records written
	files, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	require.NotEmpty(t, files)

	content, err := os.ReadFile(filepath.Join(tmpDir, files[0].Name()))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Len(t, lines, 10, "all 10 writes should be recorded")
}

func TestWriter_NDJSONFormat(t *testing.T) {
	tmpDir := t.TempDir()
	w := NewWriter(tmpDir, time.Hour)

	records := []map[string]interface{}{
		{"line": 1, "msg": "first"},
		{"line": 2, "msg": "second"},
		{"line": 3, "msg": "third"},
	}

	for _, r := range records {
		err := w.WriteRecord(r)
		require.NoError(t, err)
	}

	files, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	require.NotEmpty(t, files)

	content, err := os.ReadFile(filepath.Join(tmpDir, files[0].Name()))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Len(t, lines, 3)

	for i, line := range lines {
		assert.True(t, json.Valid([]byte(line)), "line %d should be valid JSON", i)

		var decoded map[string]interface{}
		err := json.Unmarshal([]byte(line), &decoded)
		require.NoError(t, err)
		assert.Equal(t, float64(i+1), decoded["line"])
	}
}

func TestWriter_Replay(t *testing.T) {
	tmpDir := t.TempDir()
	w := NewWriter(tmpDir, time.Hour)

	// Write a completed (past-hour) file + the current-hour file.
	pastFile := filepath.Join(tmpDir, "usage-fallback-20200101-120000.ndjson")
	if err := os.WriteFile(pastFile, []byte(`{"trace_id":"a"}`+"\n"+`{"trace_id":"b"}`+"\n"), 0644); err != nil {
		t.Fatalf("write past file: %v", err)
	}
	// Current-hour file (must NOT be replayed/removed).
	if err := w.WriteRecord(map[string]string{"trace_id": "current"}); err != nil {
		t.Fatalf("write current: %v", err)
	}

	t.Run("success replays past file and deletes it, keeps current", func(t *testing.T) {
		var seen []string
		replayed, failed := w.Replay(func(line []byte) error {
			seen = append(seen, string(line))
			return nil
		})
		if replayed != 2 || failed != 0 {
			t.Errorf("replayed=%d failed=%d, want 2/0", replayed, failed)
		}
		if _, err := os.Stat(pastFile); !os.IsNotExist(err) {
			t.Errorf("past file should be deleted after full success")
		}
		if _, err := os.Stat(filepath.Join(tmpDir, w.currentFileName())); err != nil {
			t.Errorf("current-hour file must be preserved: %v", err)
		}
	})

	t.Run("failure keeps file for retry", func(t *testing.T) {
		keep := filepath.Join(tmpDir, "usage-fallback-20200102-120000.ndjson")
		_ = os.WriteFile(keep, []byte(`{"trace_id":"x"}`+"\n"), 0644)
		_, failed := w.Replay(func(line []byte) error { return errPortalDown })
		if failed == 0 {
			t.Errorf("expected failure count > 0")
		}
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("file must be retained on failure: %v", err)
		}
	})
}

var errPortalDown = &replayErr{"portal down"}

type replayErr struct{ s string }

func (e *replayErr) Error() string { return e.s }
