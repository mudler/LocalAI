// SPDX-License-Identifier: MIT

// Package artifacts holds the private, temporary file results of addons.
//
// A file result is not a shared file. The owner copies the bytes from the
// addon's registered runtime endpoint into a private staged area, records who
// asked for them and from which resources they were produced, and serves them
// only to that principal while that principal can still read every source
// resource. Nothing here uses the model, media or upload file routes.
package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/storage"
)

// Artifact is the public view of a stored artifact.
type Artifact = gateway.Artifact

var (
	// ErrNotFound is the one answer for an unknown identifier, for an
	// artifact of another principal and for an expired one, so an identifier
	// cannot be probed.
	ErrNotFound = fmt.Errorf("addon artifact not available: %w", gateway.ErrForbidden)
	// ErrNotReady means the transfer has not completed. Nothing incomplete is
	// ever downloadable.
	ErrNotReady = fmt.Errorf("addon artifact is not complete: %w", gateway.ErrForbidden)
	// ErrQuota means a byte, count or per-user limit would be exceeded. It is a
	// denial of the request, not an outage.
	ErrQuota = fmt.Errorf("addon artifact quota reached: %w", gateway.ErrForbidden)
	// ErrTooLarge means the declared output is larger than one artifact may be.
	ErrTooLarge = fmt.Errorf("addon artifact is too large: %w", gateway.ErrForbidden)
	// ErrBusy means the concurrent transfer or download bound is reached. The
	// call can be repeated later.
	ErrBusy = fmt.Errorf("addon artifact store is busy: %w", gateway.ErrUnavailable)
	// ErrClosed means the store is shut down.
	ErrClosed = fmt.Errorf("addon artifact store is closed: %w", gateway.ErrUnavailable)
	// ErrCorrupt means the metadata or the bytes at rest are not what was
	// recorded. A corrupt metadata file is never reset.
	ErrCorrupt = errors.New("addon artifact store is corrupt")
	// ErrStoreBusy means another process holds the store.
	ErrStoreBusy = errors.New("addon artifact store is in use")

	errSizeMismatch = errors.New("output size differs from its declaration")
	errExpired      = fmt.Errorf("addon artifact expired: %w", gateway.ErrForbidden)
)

const (
	metaFileName = "artifacts.json"
	lockName     = "artifacts.lock"
	metaDir      = "meta"
	blobDir      = "blobs"
	stagingDir   = "staging"
)

// idPattern is the shape of a host-issued identifier. It is also the only shape
// that can name a file in the staged area, so no addon text or request text can
// pick a path.
var idPattern = regexp.MustCompile(`^a[0-9a-f]{32}$`)

// record is the durable form. The addon output handle is never stored: it is
// valid for the operation that produced it and only while the transfer runs.
// Binding is a digest of the principal, operation and handle, which lets a
// repeated request find its artifact without keeping the handle.
type record struct {
	ID          string                     `json:"id"`
	Principal   string                     `json:"principal"`
	Mode        string                     `json:"mode"`
	Sources     []gateway.ArtifactResource `json:"sources"`
	AddonID     string                     `json:"addonId"`
	Generation  uint64                     `json:"generation"`
	OperationID string                     `json:"operationId"`
	Binding     string                     `json:"binding"`
	Name        string                     `json:"name"`
	MediaType   string                     `json:"mediaType"`
	Size        int64                      `json:"size"`
	State       gateway.ArtifactState      `json:"state"`
	Reason      string                     `json:"reason,omitempty"`
	CreatedAt   time.Time                  `json:"createdAt"`
	ExpiresAt   time.Time                  `json:"expiresAt"`
}

func (r *record) copy() *record {
	n := *r
	n.Sources = append([]gateway.ArtifactResource(nil), r.Sources...)
	return &n
}

func (r *record) view() Artifact {
	return Artifact{ID: r.ID, Principal: r.Principal, Sources: append([]gateway.ArtifactResource(nil), r.Sources...), AddonID: r.AddonID,
		Generation: r.Generation, OperationID: r.OperationID, Name: r.Name, MediaType: r.MediaType, Size: r.Size, State: r.State,
		Reason: r.Reason, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}
}

// holdsBytes reports the states that count against the byte quotas.
func (r *record) holdsBytes() bool {
	return r.State == gateway.ArtifactTransferring || r.State == gateway.ArtifactComplete
}

type document struct {
	Version   int                `json:"version"`
	Artifacts map[string]*record `json:"artifacts"`
}

func (d *document) clone() *document {
	c := &document{Version: d.Version, Artifacts: make(map[string]*record, len(d.Artifacts))}
	for id, r := range d.Artifacts {
		c.Artifacts[id] = r.copy()
	}
	return c
}

func validState(s gateway.ArtifactState) bool {
	switch s {
	case gateway.ArtifactTransferring, gateway.ArtifactComplete, gateway.ArtifactFailed, gateway.ArtifactCancelled:
		return true
	}
	return false
}

// validate is the structural check on load. A record that fails it is
// corruption: the file is refused and never rewritten.
func (r *record) validate(id string) bool {
	if r == nil || r.ID != id || !idPattern.MatchString(id) || r.Principal == "" || r.AddonID == "" || r.Generation == 0 || r.OperationID == "" ||
		len(r.Binding) != 64 || r.Name == "" || r.MediaType == "" || r.Size < 0 || !validState(r.State) || len(r.Sources) == 0 ||
		r.CreatedAt.IsZero() || !r.ExpiresAt.After(r.CreatedAt) {
		return false
	}
	for _, s := range r.Sources {
		if s.AddonID == "" || s.ResourceID == "" {
			return false
		}
	}
	return true
}

// metaStore is the durable metadata: copy, write a temporary file, fsync,
// rename, fsync the directory, validate on load, never reset, and stop writing
// after a failed write or after Close.
type metaStore struct {
	dir  string
	lock *os.File
	mu   sync.Mutex
	doc  *document
	// failed is set when a change reached the disk but could not be made
	// durable. Memory then holds it and no further write is accepted.
	failed bool
	closed bool
}

func lockDir(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- directory mode: the owner needs the search bit
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, ErrStoreBusy
	}
	return f, nil
}

func loadMeta(dir string) (*document, error) {
	b, err := os.ReadFile(filepath.Join(dir, metaFileName)) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if errors.Is(err, os.ErrNotExist) {
		return &document{Version: 1, Artifacts: map[string]*record{}}, nil
	}
	if err != nil {
		return nil, err
	}
	d := &document{}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(d); err != nil || d.Version != 1 {
		return nil, ErrCorrupt
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, ErrCorrupt
	}
	if d.Artifacts == nil {
		d.Artifacts = map[string]*record{}
	}
	for id, r := range d.Artifacts {
		if !r.validate(id) {
			return nil, ErrCorrupt
		}
	}
	return d, nil
}

// syncDirectory makes a rename durable. It is a variable so tests can fail it.
var syncDirectory = func(d *os.File) error { return d.Sync() }

func durableWrite(path string, b []byte) (renamed bool, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".meta-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return false, err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return true, err
	}
	defer func() { _ = d.Close() }()
	return true, syncDirectory(d)
}

func openMeta(dir string) (*metaStore, error) {
	lock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	// A crash can leave a temporary file; the directory lock means no live
	// writer owns it.
	if matches, _ := filepath.Glob(filepath.Join(dir, ".meta-*")); len(matches) > 0 {
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
	d, err := loadMeta(dir)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	return &metaStore{dir: dir, lock: lock, doc: d}, nil
}

func (s *metaStore) get(id string) (record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.doc.Artifacts[id]
	if !ok {
		return record{}, false
	}
	return *r.copy(), true
}

func (s *metaStore) all() []record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]record, 0, len(s.doc.Artifacts))
	for _, r := range s.doc.Artifacts {
		out = append(out, *r.copy())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// mutate applies fn to a copy and publishes it only when it is durable.
func (s *metaStore) mutate(fn func(*document) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || s.closed {
		return gateway.ErrUnavailable
	}
	next := s.doc.clone()
	if err := fn(next); err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil {
		return gateway.ErrUnavailable
	}
	renamed, err := durableWrite(filepath.Join(s.dir, metaFileName), b)
	if err != nil {
		if renamed {
			s.doc = next
			s.failed = true
		}
		return gateway.ErrUnavailable
	}
	s.doc = next
	return nil
}

// Close releases the store. Nothing is written after it returns.
func (s *metaStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.lock.Close()
}

// stagedStore is the private staged filesystem adapter behind the ObjectStore
// boundary. Unlike the shared filesystem store it never exposes a path: a key
// must be a host-issued identifier, a write goes to a partial file that is
// renamed into place only when the reader ended cleanly, and every file is
// owner-only. Whether the object is allowed to be read is not decided here; the
// metadata and the policy decide that.
type stagedStore struct {
	blobs, staging string
}

var _ storage.ObjectStore = (*stagedStore)(nil)

func openStaged(root string) (*stagedStore, error) {
	s := &stagedStore{blobs: filepath.Join(root, blobDir), staging: filepath.Join(root, stagingDir)}
	for _, d := range []string{s.blobs, s.staging} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(d, 0o700); err != nil { // #nosec G302 -- directory mode: the owner needs the search bit
			return nil, err
		}
	}
	return s, nil
}

func (s *stagedStore) blob(key string) (string, error) {
	if !idPattern.MatchString(key) {
		return "", ErrNotFound
	}
	return filepath.Join(s.blobs, key), nil
}

func (s *stagedStore) part(key string) string { return filepath.Join(s.staging, key+".part") }

// copyBufferSize is the size of the only buffer a transfer or a download holds.
const copyBufferSize = 32 << 10

// Put streams r into a partial file and publishes it as key only when r ended
// without error. A reader that fails, is cancelled, runs short or runs long
// leaves nothing at key and no partial file.
func (s *stagedStore) Put(ctx context.Context, key string, r io.Reader) (err error) {
	dst, err := s.blob(key)
	if err != nil {
		return err
	}
	part := s.part(key)
	f, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(part)
		}
	}()
	buf := make([]byte, copyBufferSize)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, err = f.Write(buf[:n]); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(part)
		return err
	}
	if err = os.Rename(part, dst); err != nil {
		_ = os.Remove(part)
		return err
	}
	d, derr := os.Open(s.blobs)
	if derr != nil {
		return derr
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// Get opens the object. The caller closes it.
func (s *stagedStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := s.blob(key)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, ErrCorrupt
	}
	return f, nil
}

// Head returns the size and modification time of the object.
func (s *stagedStore) Head(_ context.Context, key string) (*storage.ObjectMeta, error) {
	p, err := s.blob(key)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrCorrupt
	}
	return &storage.ObjectMeta{Key: key, Size: info.Size(), LastModified: info.ModTime()}, nil
}

// Exists reports whether the object is present.
func (s *stagedStore) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Head(ctx, key)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Delete removes the object and any partial file of it. An absent object is
// not an error.
func (s *stagedStore) Delete(_ context.Context, key string) error {
	p, err := s.blob(key)
	if err != nil {
		return err
	}
	var errs []error
	for _, path := range []string{p, s.part(key)} {
		if e := os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}

// List returns the keys that start with prefix, in order.
func (s *stagedStore) List(_ context.Context, prefix string) ([]string, error) {
	entries, err := os.ReadDir(s.blobs)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, e := range entries {
		if idPattern.MatchString(e.Name()) && strings.HasPrefix(e.Name(), prefix) {
			keys = append(keys, e.Name())
		}
	}
	return keys, nil
}
