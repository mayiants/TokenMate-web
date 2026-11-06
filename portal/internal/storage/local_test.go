package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalProviderUploadDownloadDelete(t *testing.T) {
	root := t.TempDir()
	provider, err := NewLocalProvider(root, "test-prefix")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	ctx := context.Background()
	key := "subdir/hello.txt"
	content := []byte("hello enterprise document storage")

	// Upload
	if err := provider.Upload(ctx, key, bytes.NewReader(content), UploadOptions{ContentType: "text/plain", Size: int64(len(content))}); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// Exists
	exists, err := provider.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Fatal("expected file to exist")
	}

	// Download
	reader, err := provider.Download(ctx, key)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer func() { _ = reader.Close() }()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: got %q, want %q", got, content)
	}

	// Delete
	if err := provider.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	exists, err = provider.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists after delete: %v", err)
	}
	if exists {
		t.Fatal("expected file to be deleted")
	}

	// Verify the path includes prefix.
	expectedPath := filepath.Join(root, "test-prefix", "subdir", "hello.txt")
	if _, err := os.Stat(expectedPath); !os.IsNotExist(err) {
		t.Fatalf("expected path %s to be removed", expectedPath)
	}
}

func TestLocalProviderPathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	provider, err := NewLocalProvider(root, "")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	ctx := context.Background()
	key := "../escape.txt"
	if err := provider.Upload(ctx, key, bytes.NewReader([]byte("bad")), UploadOptions{}); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}
