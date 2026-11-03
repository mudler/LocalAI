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
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

type fsStore struct {
	root string
	// fault is an instance-local test seam at publication boundaries.
	fault func(string) error
}

// NewFS stores immutable blobs in a service-controlled directory. Shared
// filesystems must support coherent atomic rename and directory sync. Two
// writers of the same hash may both rename; the bytes are identical, so the
// last rename wins harmlessly.
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
	st, err := s.Stage(ctx, r, max)
	if err != nil {
		return "", 0, err
	}
	defer st.Discard()
	if err := st.Publish(ctx); err != nil {
		return "", 0, err
	}
	return st.SHA(), st.Size(), nil
}

// Stage reads r into a durable temporary file. The slow part of an upload
// happens here; Publish only renames.
func (s *fsStore) Stage(ctx context.Context, r io.Reader, max int64) (Staged, error) {
	tempDir := filepath.Join(s.root, "tmp")
	if err := s.mkdir(tempDir); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(tempDir, "upload-*")
	if err != nil {
		return nil, err
	}
	st := &fsStaged{s: s, path: f.Name()}
	// Close and Sync are checked below; this cleanup is best-effort.
	defer func() {
		_ = f.Close()
		if err != nil {
			st.Discard()
		}
	}()
	h := sha256.New()
	src := io.Reader(contextReader{ctx: ctx, r: r})
	if max > 0 {
		src = io.LimitReader(src, max+1)
	}
	if err = s.stage("write"); err != nil {
		return nil, err
	}
	if st.size, err = io.Copy(io.MultiWriter(f, h), src); err != nil {
		return nil, err
	}
	if max > 0 && st.size > max {
		err = workspace.ErrTooLarge
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	st.sha = hex.EncodeToString(h.Sum(nil))
	if err = f.Chmod(0o400); err != nil {
		return nil, err
	}
	if err = s.stage("file sync"); err != nil {
		return nil, err
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	return st, nil
}

type fsStaged struct {
	s    *fsStore
	path string
	sha  string
	size int64
}

func (t *fsStaged) SHA() string { return t.sha }
func (t *fsStaged) Size() int64 { return t.size }

// Discard removes the temporary file. It is safe after Publish.
func (t *fsStaged) Discard() { _ = os.Remove(t.path) }

// Publish moves the staged file to its final name. The file is already
// durable, so only the folder that gains the name needs a sync: a folder
// created on the way is synced into its parent by mkdir, and a leftover
// temporary name after a crash is harmless.
func (t *fsStaged) Publish(ctx context.Context) error {
	s := t.s
	if err := ctx.Err(); err != nil {
		return err
	}
	final := shaPath(s.root, t.sha)
	if err := s.mkdir(filepath.Dir(final)); err != nil {
		return err
	}
	if err := s.safe(final); err != nil {
		return err
	}
	if err := s.stage("rename"); err != nil {
		return err
	}
	// Rename replaces an existing blob with identical bytes. Never remove
	// final after a sync failure: another writer can own that name.
	if err := os.Rename(t.path, final); err != nil {
		return err
	}
	return s.syncDir(filepath.Dir(final))
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
func (s *fsStore) Exists(_ context.Context, sha string) (bool, error) {
	if !validSHA(sha) {
		return false, nil
	}
	p := shaPath(s.root, sha)
	if err := s.safe(p); err != nil {
		return false, err
	}
	_, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// LinkTo copies the blob and never links it, despite the name it has in Store.
// The same UID can chmod a read-only hard link and mutate its inode, so a hard
// link is not an isolation boundary for tool files.
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

// volumeMarker is the file at the blob root whose content names the volume.
const volumeMarker = ".volume-id"

// ReadVolumeID returns the identity stored at the blob root, or "" when the
// root has none yet.
func (s *fsStore) ReadVolumeID(_ context.Context) (string, error) {
	b, err := os.ReadFile(filepath.Join(s.root, volumeMarker)) // #nosec G304 -- fixed name under the canonical root.
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

// WriteVolumeID stores id at the blob root. It fails with os.ErrExist when
// the root already has an identity, so two writers cannot both claim it.
func (s *fsStore) WriteVolumeID(_ context.Context, id string) error {
	f, err := os.OpenFile(filepath.Join(s.root, volumeMarker), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400) // #nosec G304 -- fixed name under the canonical root.
	if err != nil {
		return err
	}
	_, err = f.WriteString(id + "\n")
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	return s.syncDir(s.root)
}
