package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
)

// UploadOptions contains metadata for an upload.
type UploadOptions struct {
	ContentType string
	Size        int64
	Metadata    map[string]string
}

// Provider abstracts file storage backends (local filesystem, S3, MinIO, etc.).
type Provider interface {
	// Upload writes reader to storage at the given key.
	// Key is a relative path such as "enterprise-apps/{appID}/{uuid}.pdf".
	Upload(ctx context.Context, key string, reader io.Reader, opts UploadOptions) error

	// Download returns a ReadCloser for the object at key. Caller must close it.
	Download(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the object at key.
	Delete(ctx context.Context, key string) error

	// Exists reports whether the object at key exists.
	Exists(ctx context.Context, key string) (bool, error)

	// PresignURL returns a temporary URL that can be used to access the object.
	// Returns empty string if the provider does not support presigned URLs.
	PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// New creates a Provider from configuration.
func New(cfg config.FileStorageConfig) (Provider, error) {
	switch cfg.Provider {
	case "local", "":
		return NewLocalProvider(cfg.LocalRoot, cfg.PathPrefix)
	case "s3", "minio":
		// S3 provider can be added later without changing callers.
		return nil, fmt.Errorf("storage provider %q is not implemented yet; use 'local' for now", cfg.Provider)
	default:
		return nil, fmt.Errorf("unknown storage provider: %s", cfg.Provider)
	}
}
