package storage

import (
	"context"
	"io"
	"time"
)

// ObjectMeta holds metadata about an object in the store.
type ObjectMeta struct {
	Key          string
	Size         int64
	LastModified time.Time
	Metadata     map[string]string // user-defined metadata (e.g. x-amz-meta-* in S3)
}

// ObjectStore is the interface for blob storage (model files, user assets, etc.).
// Two implementations exist: S3 (for distributed/production) and filesystem (fallback/local).
type ObjectStore interface {
	// Put stores data under the given key.
	Put(ctx context.Context, key string, r io.Reader) error

	// Get retrieves data for the given key. Caller must close the returned ReadCloser.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Head returns metadata about the object without downloading its body.
	Head(ctx context.Context, key string) (*ObjectMeta, error)

	// Exists returns true if the key exists in the store.
	Exists(ctx context.Context, key string) (bool, error)

	// Delete removes the object at the given key.
	Delete(ctx context.Context, key string) error

	// List returns keys matching the given prefix.
	List(ctx context.Context, prefix string) ([]string, error)
}
