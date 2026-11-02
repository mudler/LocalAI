// SPDX-License-Identifier: MIT
package outputstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/mudler/LocalAI/core/services/storage"
)

// PrivateFilesystem is an os.Root-contained implementation of the existing
// ObjectStore seam. The legacy FilesystemStore follows symlinks and is therefore
// deliberately not accepted for music's standalone security boundary.
type PrivateFilesystem struct{ root *os.Root }

var _ storage.ObjectStore = (*PrivateFilesystem)(nil)

func (*PrivateFilesystem) privateFilesystem() {}
func NewPrivateFilesystem(dir string) (*PrivateFilesystem, error) {
	r, e := privateRoot(dir)
	if e != nil {
		return nil, e
	}
	return &PrivateFilesystem{r}, nil
}
func (f *PrivateFilesystem) Close() error { return f.root.Close() }
func objectLeaf(key string) (string, error) {
	if !strings.HasPrefix(key, "music/") || !validID(strings.TrimPrefix(key, "music/")) {
		return "", ErrDenied
	}
	return strings.TrimPrefix(key, "music/"), nil
}
func (f *PrivateFilesystem) Put(ctx context.Context, key string, r io.Reader) error {
	leaf, e := objectLeaf(key)
	if e != nil {
		return e
	}
	// Every key is server-random and single-use; never truncate an existing leaf.
	out, e := f.root.OpenFile(leaf, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	n, e := io.Copy(out, io.LimitReader(&contextReader{ctx, r}, MaxWAVBytes+1))
	if e == nil && n > MaxWAVBytes {
		e = ErrDenied
	}
	if e == nil {
		e = out.Sync()
	}
	return errors.Join(e, out.Close())
}
func (f *PrivateFilesystem) Get(_ context.Context, key string) (io.ReadCloser, error) {
	leaf, e := objectLeaf(key)
	if e != nil {
		return nil, e
	}
	return openRegular(f.root, leaf, 0, 0)
}
func (f *PrivateFilesystem) Head(ctx context.Context, key string) (*storage.ObjectMeta, error) {
	r, e := f.Get(ctx, key)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	st, e := r.(*os.File).Stat()
	if e != nil {
		return nil, e
	}
	return &storage.ObjectMeta{Key: key, Size: st.Size(), LastModified: st.ModTime()}, nil
}
func (f *PrivateFilesystem) Exists(ctx context.Context, key string) (bool, error) {
	r, e := f.Get(ctx, key)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	return true, r.Close()
}
func (f *PrivateFilesystem) Delete(_ context.Context, key string) error {
	leaf, e := objectLeaf(key)
	if e != nil {
		return e
	}
	e = f.root.Remove(leaf)
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func (f *PrivateFilesystem) List(_ context.Context, prefix string) ([]string, error) {
	if prefix != "music/" {
		return nil, ErrDenied
	}
	dir, e := f.root.Open(".")
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	entries, e := dir.ReadDir(-1)
	if e != nil {
		return nil, e
	}
	var keys []string
	for _, v := range entries {
		if validID(v.Name()) {
			keys = append(keys, "music/"+v.Name())
		}
	}
	return keys, nil
}
func fileIdentity(st os.FileInfo) (uint64, uint64) {
	v, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	return uint64(v.Dev), v.Ino
}
func singleRegular(st os.FileInfo) bool {
	v, ok := st.Sys().(*syscall.Stat_t)
	return ok && st.Mode().IsRegular() && v.Nlink == 1
}
func openRegular(root *os.Root, leaf string, device, inode uint64) (*os.File, error) {
	// Lstat each component as well as the leaf; Root containment alone allows
	// in-root symlinks. Dedicated 0700 roots exclude untrusted concurrent writers.
	parts := strings.Split(leaf, "/")
	for i := range parts {
		st, e := root.Lstat(path.Join(parts[:i+1]...))
		if e != nil {
			return nil, e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, ErrDenied
		}
	}
	before, e := root.Lstat(leaf)
	if e != nil {
		return nil, e
	}
	if !singleRegular(before) {
		return nil, ErrDenied
	}
	f, e := root.Open(leaf)
	if e != nil {
		return nil, e
	}
	after, e := f.Stat()
	if e != nil || !singleRegular(after) || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, ErrDenied
	}
	d, i := fileIdentity(after)
	if inode != 0 && (d != device || i != inode) {
		_ = f.Close()
		return nil, ErrDenied
	}
	return f, nil
}
func (s *Store) openLocal(a artifactRow) (*os.File, error) {
	return openRegular(s.root, a.Leaf, a.Device, a.Inode)
}
func (s *Store) validateArtifact(ctx context.Context, a artifactRow) (string, error) {
	f, e := s.openLocal(a)
	if e != nil {
		return "", e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return "", e
	}
	if st.Size() <= 0 || st.Size() > MaxWAVBytes || st.Size() != a.Size {
		return "", ErrDenied
	}
	if e = s.validate(ctx, f, a.Size); e != nil {
		return "", e
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return "", e
	}
	hash := sha256.New()
	n, e := io.Copy(hash, &contextReader{ctx, io.LimitReader(f, MaxWAVBytes+1)})
	if e != nil {
		return "", e
	}
	if n != a.Size {
		return "", ErrDenied
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
