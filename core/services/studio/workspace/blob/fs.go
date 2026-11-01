package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

type fsStore struct {
	root string
	// fault is an instance-local test seam at publication boundaries.
	fault func(string) error
}

// NewFS stores immutable blobs in a service-controlled directory. Shared
// filesystems must support coherent exclusive creation, rename and directory sync.
func NewFS(dir string) (Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// Preserve ordinary platform aliases (for example macOS /var). The
	// operator must keep the canonical backing directory service-controlled.
	if err = os.MkdirAll(abs, 0o700); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &fsStore{root: canonical}, nil
}

// safe refuses symlink components inside the canonical blob root.
// The tree must remain service-controlled; arbitrary host writers are unsupported.
func (s *fsStore) safe(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("blob: symlink component is not allowed")
		}
		if p == s.root || filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func (s *fsStore) stage(name string) error {
	if s.fault != nil {
		return s.fault(name)
	}
	return nil
}
func (s *fsStore) syncDir(path string) error {
	if err := s.stage("directory sync"); err != nil {
		return err
	}
	f, err := os.Open(path) // #nosec G304 -- internal callers pass only canonical-root directories or hash shards in the service-controlled tree.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
func (s *fsStore) mkdir(path string) error {
	if err := s.safe(path); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := s.mkdir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return s.syncDir(filepath.Dir(path))
}
func (s *fsStore) Put(ctx context.Context, r io.Reader, max int64) (string, int64, error) {
	tempDir := filepath.Join(s.root, "tmp")
	if err := s.mkdir(tempDir); err != nil {
		return "", 0, err
	}
	f, err := os.CreateTemp(tempDir, "upload-*")
	if err != nil {
		return "", 0, err
	}
	// Publication checks Sync and Close below; deferred cleanup is best-effort,
	// including after a successful rename has removed the temporary name.
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	h := sha256.New()
	src := io.Reader(contextReader{ctx: ctx, r: r})
	if max > 0 {
		src = io.LimitReader(src, max+1)
	}
	if err = s.stage("write"); err != nil {
		return "", 0, err
	}
	n, err := io.Copy(io.MultiWriter(f, h), src)
	if err != nil {
		return "", 0, err
	}
	if max > 0 && n > max {
		return "", 0, workspace.ErrTooLarge
	}
	if err = ctx.Err(); err != nil {
		return "", 0, err
	}
	sha := hex.EncodeToString(h.Sum(nil))
	final := shaPath(s.root, sha)
	if err = s.mkdir(filepath.Dir(final)); err != nil {
		return "", 0, err
	}
	if err = s.safe(final); err != nil {
		return "", 0, err
	}
	if err = f.Chmod(0o400); err != nil {
		return "", 0, err
	}
	if err = s.stage("file sync"); err != nil {
		return "", 0, err
	}
	if err = f.Sync(); err != nil {
		return "", 0, err
	}
	if err = f.Close(); err != nil {
		return "", 0, err
	}
	if err = s.stage("rename"); err != nil {
		return "", 0, err
	}
	if err = os.Rename(f.Name(), final); err != nil {
		return "", 0, err
	}
	// Never remove final after a sync failure: another writer can own that name.
	for p := filepath.Dir(final); ; p = filepath.Dir(p) {
		if err = s.syncDir(p); err != nil {
			return "", 0, err
		}
		if p == s.root {
			break
		}
	}
	if err = s.syncDir(tempDir); err != nil {
		return "", 0, err
	}
	return sha, n, nil
}
func (s *fsStore) Open(_ context.Context, sha string) (io.ReadSeekCloser, error) {
	if !validSHA(sha) {
		return nil, workspace.ErrNotFound
	}
	p := shaPath(s.root, sha)
	if err := s.safe(p); err != nil {
		return nil, err
	}
	f, err := os.Open(p) // #nosec G304 -- validSHA confines p beneath the canonical root; safe rejects in-tree symlinks in the service-controlled tree.
	if errors.Is(err, os.ErrNotExist) {
		return nil, workspace.ErrNotFound
	}
	return f, err
}
func (s *fsStore) Exists(ctx context.Context, sha string) (bool, error) {
	f, err := s.Open(ctx, sha)
	if errors.Is(err, workspace.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, f.Close()
}

// LinkTo deliberately copies. The same UID can chmod a read-only hard link and
// mutate its inode, so a hard link is not an isolation boundary for tool files.
func (s *fsStore) LinkTo(ctx context.Context, sha string, dir *os.Root, name string) error {
	if !filepath.IsLocal(name) {
		return workspace.ErrInvalid
	}
	in, err := s.Open(ctx, sha)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = dir.Remove(name)
	}
	return err
}
func (s *fsStore) Remove(_ context.Context, sha string) error {
	if !validSHA(sha) {
		return nil
	}
	p := shaPath(s.root, sha)
	if err := s.safe(p); err != nil {
		return err
	}
	err := os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.syncDir(filepath.Dir(p))
}

// A Reader blocked inside Read must honor its own request cancellation.
// Between reads we stop promptly, including before publication after EOF.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// IsFilesystem distinguishes authoritative direct FS from an object-store cache.
func (*fsStore) IsFilesystem() bool { return true }
