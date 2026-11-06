package web

import (
	"testing"
)

// TestEmbedFS verifies that the embedded filesystem is available.
func TestEmbedFS(t *testing.T) {
	_, err := FS.ReadDir("templates")
	if err != nil {
		t.Fatalf("FS.ReadDir(templates) error: %v - embedded filesystem may not be available", err)
	}
}

// TestEmbedFSReadDir verifies we can read from the embedded filesystem.
func TestEmbedFSReadDir(t *testing.T) {
	entries, err := FS.ReadDir("templates")
	if err != nil {
		t.Fatalf("FS.ReadDir(templates) error: %v", err)
	}
	if len(entries) == 0 {
		t.Error("FS.ReadDir(templates) returned no entries")
	}
	t.Logf("Found %d template entries", len(entries))
}

// TestEmbedFSI18n verifies i18n files are embedded.
func TestEmbedFSI18n(t *testing.T) {
	data, err := FS.ReadFile("i18n/zh.json")
	if err != nil {
		t.Fatalf("FS.ReadFile(i18n/zh.json) error: %v", err)
	}
	if len(data) == 0 {
		t.Error("i18n/zh.json is empty")
	}
	t.Logf("i18n/zh.json size: %d bytes", len(data))
}

// TestEmbedFSStatic verifies static files are embedded.
func TestEmbedFSStatic(t *testing.T) {
	entries, err := FS.ReadDir("static")
	if err != nil {
		t.Fatalf("FS.ReadDir(static) error: %v", err)
	}
	if len(entries) == 0 {
		t.Error("FS.ReadDir(static) returned no entries - static files not embedded")
	}
	t.Logf("Found %d static file/dir entries", len(entries))
}
