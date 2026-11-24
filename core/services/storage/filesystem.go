package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// validateKey ensures the resolved path stays inside the store root.
func (fs *FilesystemStore) validateKey(key string) error {
	p := filepath.Join(fs.root, filepath.FromSlash(key))
	absRoot, err := filepath.Abs(fs.root)
	if err != nil {
		return fmt.Errorf("resolving store root: %w", err)
	}
	absPath, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("resolving key path: %w", err)
	}
	if !strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) && absPath != absRoot {
		return fmt.Errorf("key %q resolves outside storage root", key)
	}
	return nil
}

// FilesystemStore implements ObjectStore backed by a local directory.
type FilesystemStore struct {
	root string
}

// NewFilesystemStore creates a new filesystem-backed ObjectStore rooted at the given directory.
func NewFilesystemStore(root string) (*FilesystemStore, error) {
	if err := os.MkdirAll(root, 0750); err != nil {
		return nil, fmt.Errorf("creating storage root %s: %w", root, err)
	}
	return &FilesystemStore{root: root}, nil
}

func (fs *FilesystemStore) path(key string) string {
	return filepath.Join(fs.root, filepath.FromSlash(key))
}

func (fs *FilesystemStore) Put(_ context.Context, key string, r io.Reader) error {
	if err := fs.validateKey(key); err != nil {
		return err
	}
	p := fs.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0750); err != nil {
		return fmt.Errorf("creating directories for %s: %w", key, err)
	}
	f, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", key, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

func (fs *FilesystemStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if err := fs.validateKey(key); err != nil {
		return nil, err
	}
	f, err := os.Open(fs.path(key))
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", key, err)
	}
	return f, nil
}

func (fs *FilesystemStore) Head(_ context.Context, key string) (*ObjectMeta, error) {
	if err := fs.validateKey(key); err != nil {
		return nil, err
	}
	info, err := os.Stat(fs.path(key))
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", key, err)
	}
	return &ObjectMeta{
		Key:          key,
		Size:         info.Size(),
		LastModified: info.ModTime(),
	}, nil
}

func (fs *FilesystemStore) Exists(_ context.Context, key string) (bool, error) {
	if err := fs.validateKey(key); err != nil {
		return false, err
	}
	_, err := os.Stat(fs.path(key))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (fs *FilesystemStore) Delete(_ context.Context, key string) error {
	if err := fs.validateKey(key); err != nil {
		return err
	}
	err := os.Remove(fs.path(key))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting %s: %w", key, err)
	}
	return nil
}

// Close implements io.Closer. FilesystemStore holds no resources that need
// explicit cleanup, so this is a no-op.
func (fs *FilesystemStore) Close() error { return nil }

func (fs *FilesystemStore) List(_ context.Context, prefix string) ([]string, error) {
	if err := fs.validateKey(prefix); err != nil {
		return nil, err
	}
	var keys []string
	base := fs.path(prefix)

	// If the prefix path doesn't exist, return empty list
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return keys, nil
	}

	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, err := filepath.Rel(fs.root, path)
			if err != nil {
				return err
			}
			keys = append(keys, strings.ReplaceAll(rel, string(filepath.Separator), "/"))
		}
		return nil
	})
	return keys, err
}
