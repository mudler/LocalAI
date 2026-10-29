package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

type fsStore struct{ root string }

// NewFS returns a Store that keeps blobs as read-only files under dir, in
// folders named by the first four hex digits of the hash.
func NewFS(dir string) (Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &fsStore{root: dir}, nil
}

func (s *fsStore) Put(_ context.Context, r io.Reader, max int64) (string, int64, error) {
	tmp, sha, size, err := ingest(r, max, filepath.Join(s.root, "tmp"))
	if err != nil {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
		return "", 0, err
	}
	if err := place(tmp, shaPath(s.root, sha)); err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	return sha, size, nil
}

func (s *fsStore) Open(_ context.Context, sha string) (io.ReadSeekCloser, error) {
	if !validSHA(sha) {
		return nil, workspace.ErrNotFound
	}
	f, err := os.Open(shaPath(s.root, sha))
	if errors.Is(err, os.ErrNotExist) {
		return nil, workspace.ErrNotFound
	}
	return f, err
}

func (s *fsStore) Exists(_ context.Context, sha string) (bool, error) {
	if !validSHA(sha) {
		return false, nil
	}
	_, err := os.Stat(shaPath(s.root, sha))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (s *fsStore) LinkTo(_ context.Context, sha string, dir *os.Root, name string) error {
	if !validSHA(sha) {
		return workspace.ErrNotFound
	}
	err := linkOrCopy(shaPath(s.root, sha), dir, name)
	if errors.Is(err, os.ErrNotExist) {
		if _, serr := os.Stat(shaPath(s.root, sha)); serr != nil {
			return workspace.ErrNotFound
		}
	}
	return err
}
