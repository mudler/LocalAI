// SPDX-License-Identifier: MIT
package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
)

const (
	fileName    = "policy.json"
	lockName    = "policy.lock"
	stateActive = "active"
	statePend   = "pending"
)

// record is the host's binding of an opaque resource ID to the trusted addon
// identity and the immutable owner. Grants never include the owner.
type record struct {
	AddonID string          `json:"addonId"`
	Owner   string          `json:"owner"`
	State   string          `json:"state"`
	Grants  map[string]Role `json:"grants,omitempty"`
}

type document struct {
	Version   int                `json:"version"`
	Mode      Mode               `json:"mode"`
	Resources map[string]*record `json:"resources"`
}

func (d *document) clone() *document {
	c := &document{Version: d.Version, Mode: d.Mode, Resources: make(map[string]*record, len(d.Resources))}
	for id, r := range d.Resources {
		n := *r
		n.Grants = make(map[string]Role, len(r.Grants))
		for u, role := range r.Grants {
			n.Grants[u] = role
		}
		c.Resources[id] = &n
	}
	return c
}

func (d *document) shared() bool {
	for _, r := range d.Resources {
		if len(r.Grants) > 0 {
			return true
		}
	}
	return false
}

// Store is the durable, owner-local policy state. It lives in its own
// directory so it is backed up and migrated apart from replaceable packages.
// One process holds it at a time.
type Store struct {
	dir  string
	mode Mode
	lock *os.File
	mu   sync.Mutex
	doc  *document
}

func lockDir(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, ErrStoreBusy
	}
	return f, nil
}

func load(dir string) (*document, error) {
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return &document{Version: 1, Resources: map[string]*record{}}, nil
	}
	if err != nil {
		return nil, err
	}
	d := &document{}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(d); err != nil || d.Version != 1 || (d.Mode != "" && !d.Mode.valid()) {
		return nil, errors.New("addon policy store is corrupt")
	}
	if d.Resources == nil {
		d.Resources = map[string]*record{}
	}
	for _, r := range d.Resources {
		if r == nil || r.AddonID == "" || r.Owner == "" || (r.State != stateActive && r.State != statePend) {
			return nil, errors.New("addon policy store is corrupt")
		}
	}
	return d, nil
}

func save(dir string, d *document) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return durableWrite(filepath.Join(dir, fileName), b)
}

// OpenStore opens or creates the store in dir for mode. A populated store of
// another mode is refused: use MigrateMode while the host is stopped. A
// corrupt store is never reset.
func OpenStore(dir string, mode Mode) (*Store, error) {
	if !mode.valid() || dir == "" {
		return nil, ErrMigrationRequired
	}
	lock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	d, err := load(dir)
	if err == nil && d.Mode != mode {
		if len(d.Resources) > 0 {
			err = ErrMigrationRequired
		} else {
			d.Mode = mode
			err = save(dir, d)
		}
	}
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	return &Store{dir: dir, mode: mode, lock: lock, doc: d}, nil
}

// Close releases the store.
func (s *Store) Close() error { return s.lock.Close() }

// Mode is the ownership model of this store.
func (s *Store) Mode() Mode { return s.mode }

func (s *Store) get(id string) (record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.doc.Resources[id]
	if !ok {
		return record{}, false
	}
	c := *r
	c.Grants = make(map[string]Role, len(r.Grants))
	for u, role := range r.Grants {
		c.Grants[u] = role
	}
	return c, true
}

// mutate applies fn to a copy and publishes it only when it is durable, so a
// failed write never leaves memory ahead of disk.
func (s *Store) mutate(fn func(*document) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.doc.clone()
	if err := fn(next); err != nil {
		return err
	}
	if err := save(s.dir, next); err != nil {
		return ErrUnavailable
	}
	s.doc = next
	return nil
}

func (s *Store) pending() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, r := range s.doc.Resources {
		if r.State == statePend {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Backup writes a consistent copy of the store to dst, which must not exist.
func (s *Store) Backup(dst string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeNew(dst, s.doc)
}

func writeNew(dst string, d *document) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, bytes.NewReader(b))
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

// MigrateMode changes the ownership model of a populated store. The store must
// be closed everywhere (the lock is taken without waiting), a backup is written
// first to backupDir, and shared data cannot return to local-owner mode. When
// moving to named-user mode every resource gets the named owner; the owner of a
// resource is otherwise immutable.
func MigrateMode(dir string, to Mode, backupDir, namedOwner string) error {
	if !to.valid() || backupDir == "" {
		return ErrMigrationRequired
	}
	lock, err := lockDir(dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	d, err := load(dir)
	if err != nil {
		return err
	}
	if d.Mode == to {
		return nil
	}
	if to == ModeLocalOwner && d.shared() {
		return ErrDowngradeUnsupported
	}
	if to == ModeNamedUser && !validUserID(namedOwner) && len(d.Resources) > 0 {
		return ErrMigrationRequired
	}
	if err = os.MkdirAll(backupDir, 0o700); err != nil {
		return err
	}
	if err = writeNew(filepath.Join(backupDir, fileName), d); err != nil {
		return err
	}
	next := d.clone()
	next.Mode = to
	for _, r := range next.Resources {
		if to == ModeLocalOwner {
			r.Owner = LocalOwnerID
		} else {
			r.Owner = namedOwner
		}
	}
	return save(dir, next)
}

func durableWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".policy-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
