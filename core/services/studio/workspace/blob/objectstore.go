package blob

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// objectPrefix is where blobs live inside the object store.
const objectPrefix = "workspace/blobs/"

type objectStore struct {
	os    storage.ObjectStore
	cache string
}

// NewObjectStore returns a Store that keeps blobs in a LocalAI object store
// (S3, or the filesystem fallback). Every frontend of a cluster sees the same
// blobs. A blob is downloaded once into cacheDir, so Open can seek.
func NewObjectStore(o storage.ObjectStore, cacheDir string) Store {
	return &objectStore{os: o, cache: cacheDir}
}

func key(sha string) string { return objectPrefix + sha }

func (s *objectStore) Put(ctx context.Context, r io.Reader, max int64) (string, int64, error) {
	tmp, sha, size, err := ingest(r, max, filepath.Join(s.cache, "tmp"))
	if err != nil {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
		return "", 0, err
	}
	ok, err := s.os.Exists(ctx, key(sha))
	if err == nil && !ok {
		var f *os.File
		if f, err = os.Open(tmp); err == nil { // #nosec G304 -- the path is a file of the local blob cache
			err = s.os.Put(ctx, key(sha), f)
			_ = f.Close()
		}
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	// Keep the bytes this frontend just received, so the next read is local.
	if err := place(tmp, shaPath(s.cache, sha)); err != nil {
		_ = os.Remove(tmp)
	}
	return sha, size, nil
}

func (s *objectStore) Open(ctx context.Context, sha string) (io.ReadSeekCloser, error) {
	if !validSHA(sha) {
		return nil, workspace.ErrNotFound
	}
	local := shaPath(s.cache, sha)
	if f, err := os.Open(local); err == nil { // #nosec G304 -- the path is a file of the local blob cache
		return f, nil
	}
	ok, err := s.os.Exists(ctx, key(sha))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, workspace.ErrNotFound
	}
	body, err := s.os.Get(ctx, key(sha))
	if err != nil {
		return nil, err
	}
	defer body.Close()
	tmp, got, _, err := ingest(body, 0, filepath.Join(s.cache, "tmp"))
	if err != nil {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
		return nil, err
	}
	if got != sha {
		_ = os.Remove(tmp)
		return nil, workspace.ErrNotFound // the object does not hold the bytes its name says
	}
	if err := place(tmp, local); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return os.Open(local) // #nosec G304 -- the path is a file of the local blob cache
}

func (s *objectStore) Exists(ctx context.Context, sha string) (bool, error) {
	if !validSHA(sha) {
		return false, nil
	}
	if _, err := os.Stat(shaPath(s.cache, sha)); err == nil {
		return true, nil
	}
	return s.os.Exists(ctx, key(sha))
}

// LinkTo links the blob from the local cache, downloading it first when this
// frontend has not read it yet.
func (s *objectStore) LinkTo(ctx context.Context, sha string, dir *os.Root, name string) error {
	if !validSHA(sha) {
		return workspace.ErrNotFound
	}
	r, err := s.Open(ctx, sha) // fills the cache
	if err != nil {
		return err
	}
	_ = r.Close()
	return linkOrCopy(shaPath(s.cache, sha), dir, name)
}
