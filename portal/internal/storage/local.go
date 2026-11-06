package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalProvider stores files on the local filesystem.
type LocalProvider struct {
	root       string
	pathPrefix string
}

// NewLocalProvider creates a local filesystem storage provider.
func NewLocalProvider(root, pathPrefix string) (*LocalProvider, error) {
	if root == "" {
		root = "./uploads"
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve local storage root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o750); err != nil {
		return nil, fmt.Errorf("create local storage root %s: %w", absRoot, err)
	}
	return &LocalProvider{
		root:       absRoot,
		pathPrefix: pathPrefix,
	}, nil
}

// resolveKey converts a storage key into an absolute filesystem path.
func (p *LocalProvider) resolveKey(key string) (string, error) {
	key = filepath.Clean(key)
	if strings.HasPrefix(key, "..") || strings.Contains(key, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid storage key: %s", key)
	}
	if p.pathPrefix != "" {
		key = filepath.Join(p.pathPrefix, key)
	}
	return filepath.Join(p.root, key), nil
}

// Upload writes the file to disk.
func (p *LocalProvider) Upload(ctx context.Context, key string, reader io.Reader, opts UploadOptions) error {
	path, err := p.resolveKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create upload directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("create file %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	if _, err := io.Copy(file, reader); err != nil {
		// Best-effort cleanup on write failure.
		_ = os.Remove(path)
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

// Download opens the file for reading.
func (p *LocalProvider) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	path, err := p.resolveKey(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", key)
		}
		return nil, fmt.Errorf("open file %s: %w", path, err)
	}
	return file, nil
}

// Delete removes the file.
func (p *LocalProvider) Delete(ctx context.Context, key string) error {
	path, err := p.resolveKey(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove file %s: %w", path, err)
	}
	return nil
}

// Exists reports whether the file exists.
func (p *LocalProvider) Exists(ctx context.Context, key string) (bool, error) {
	path, err := p.resolveKey(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// PresignURL is not supported for local filesystem; returns empty string.
func (p *LocalProvider) PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return "", nil
}
