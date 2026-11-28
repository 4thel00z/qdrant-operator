// Package ports declares the interfaces the use cases depend on; adapters implement them.
package ports

import (
	"context"
	"io"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

// Storage is S3-compatible object storage. Prefix operations match keys under "<prefix>/".
type Storage interface {
	UploadStream(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, key string, body io.Reader) (int64, error)
	PutObject(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, key string, data []byte) error
	GetObject(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, key string) ([]byte, error)
	DeletePrefix(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, prefix string) (int, error)
	AbortUploads(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, prefix string) (int, error)
	PresignedGetURL(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, key string, expires time.Duration) (string, error)
}
