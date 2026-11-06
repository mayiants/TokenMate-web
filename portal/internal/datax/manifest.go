package datax

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Manifest is the manifest.json written next to the per-entity JSON files. It
// records provenance and per-entity row counts and checksums so an import can
// verify the dump before applying it.
type Manifest struct {
	PortalVersion string                 `json:"portal_version"`
	SchemaVersion string                 `json:"schema_version"`
	ExportedAt    string                 `json:"exported_at"`
	Entities      map[string]EntityEntry `json:"entities"`
}

// EntityEntry is the per-entity accounting stored in the manifest.
type EntityEntry struct {
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}

// NewManifest returns an empty manifest stamped for now.
func NewManifest(portalVersion, schemaVersion string) *Manifest {
	return &Manifest{
		PortalVersion: portalVersion,
		SchemaVersion: schemaVersion,
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		Entities:      map[string]EntityEntry{},
	}
}

// WriteManifest serializes m to <dir>/manifest.json (indented).
func WriteManifest(dir string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// ReadManifest loads <dir>/manifest.json.
func ReadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Entities == nil {
		m.Entities = map[string]EntityEntry{}
	}
	return &m, nil
}

// FileSHA256 returns the hex sha256 digest of a file's contents.
func FileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
