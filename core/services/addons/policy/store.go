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
	"time"
)

const (
	fileName    = "policy.json"
	lockName    = "policy.lock"
	stateActive = "active"
	statePend   = "pending"
	// stateDeleting is a resource its owner asked to delete. It is
	// inaccessible to everyone, takes no new admission and is removed only
	// after the addon confirmed that its backing is gone. A crash leaves the
	// record, and the owner resumes or aborts it at startup.
	stateDeleting = "deleting"
)

// record is the host's binding of an opaque resource ID to the trusted addon
// identity and the immutable owner. Grants never include the owner.
type record struct {
	AddonID string          `json:"addonId"`
	Owner   string          `json:"owner"`
	State   string          `json:"state"`
	Grants  map[string]Role `json:"grants,omitempty"`
	// Epoch counts the lowerings of authority on this resource. Every
	// admission carries the epoch it was admitted under, so a drain can tell
	// work that predates a lowering from work admitted after the grant became
	// durable again.
	Epoch uint64 `json:"epoch,omitempty"`
	// Delete is present exactly while the resource is deleting.
	Delete *deleteMark `json:"delete,omitempty"`
	// CreateRef binds a resource made by the UI creation path to the request
	// that made it, so a repeated operation ID with another request is refused.
	CreateRef string `json:"createRef,omitempty"`
	// Drain names users whose grant was lowered or removed and whose admitted
	// work and pending proposals are not yet known to be stopped. The mark is
	// durable with the change itself and cleared only after the drain
	// completed, so a retry of the same change can never be acknowledged
	// early. It holds the epoch of the lowering, not a flag, so a retry stops
	// and waits for the older work only.
	Drain map[string]drainMark `json:"drain,omitempty"`
}

// deleteMark is the durable state of a deletion in progress.
type deleteMark struct {
	// Op is the operation identifier of the person's delete request.
	Op string `json:"op"`
	// By is the owner who asked for the deletion.
	By string `json:"by"`
	// Confirmed is set once the preconditions held with no admitted work
	// left. Only a confirmed deletion is resumed after a crash; an
	// unconfirmed one is returned to the active state.
	Confirmed bool `json:"confirmed,omitempty"`
}

// drainMark is the cutoff of an owed drain: admitted work of the user with an
// epoch below the value was admitted under authority that was lowered. Writes
// and reads have separate cutoffs because lowering a role to viewer leaves
// reads alone while a removal stops both. Zero means nothing is owed for it.
type drainMark struct {
	Writes uint64 `json:"writes,omitempty"`
	Reads  uint64 `json:"reads,omitempty"`
}

func (r *record) copy() *record {
	n := *r
	n.Grants = make(map[string]Role, len(r.Grants))
	for u, role := range r.Grants {
		n.Grants[u] = role
	}
	if r.Delete != nil {
		d := *r.Delete
		n.Delete = &d
	}
	n.Drain = nil
	if len(r.Drain) > 0 {
		n.Drain = make(map[string]drainMark, len(r.Drain))
		for u, m := range r.Drain {
			n.Drain[u] = m
		}
	}
	return &n
}

// tombstone is what stays of a resource whose deletion finished: enough for
// its owner to learn that a deletion that was reported as unknown did happen.
// It names no data and grants nothing. The newest MaxTombstones are kept.
type tombstone struct {
	AddonID string    `json:"addonId"`
	Owner   string    `json:"owner"`
	Op      string    `json:"op"`
	At      time.Time `json:"at"`
}

// MaxTombstones bounds the finished deletions the store remembers.
const MaxTombstones = 1024

// revocation is a lowering of one person's authority on a resource: the
// highest role the person may hold after it, empty for a removal. A restore
// from a backup older than the lowering applies it again, so restoring never
// gives back access that was taken away.
type revocation struct {
	AddonID    string    `json:"addonId"`
	ResourceID string    `json:"resourceId"`
	UserID     string    `json:"userId"`
	Ceiling    Role      `json:"ceiling,omitempty"`
	At         time.Time `json:"at"`
}

// MaxRevocations is a safety bound on the lowerings the store keeps. The log
// is compacted as it grows: a lowering that a later and stricter or equal one
// of the same person on the same resource covers is dropped, so a person has
// at most three entries and the bound is reached only by many distinct
// people and resources. When it is reached, the oldest entries go and the
// floor of the log moves up to the time of the newest one lost: a restore
// from a backup older than the floor cannot know what was lowered, so it
// restores no access at all.
const MaxRevocations = 4096

// revocationCap is MaxRevocations; a test lowers it.
var revocationCap = MaxRevocations

func (r revocation) rank() int {
	switch r.Ceiling {
	case RoleViewer:
		return 1
	case RoleEditor:
		return 2
	}
	return 0
}

// addRevocation records a lowering. It keeps the log exact for every restore
// time: a dropped entry is always covered by a later entry that is at least as
// strict, so any restore that would have applied the first applies the second.
func (d *document) addRevocation(v revocation) {
	kept := d.Revoked[:0:0]
	for _, o := range d.Revoked {
		if o.AddonID == v.AddonID && o.ResourceID == v.ResourceID && o.UserID == v.UserID && !o.At.After(v.At) && v.rank() <= o.rank() {
			continue
		}
		kept = append(kept, o)
	}
	d.Revoked = append(kept, v)
	if over := len(d.Revoked) - revocationCap; over > 0 {
		for _, o := range d.Revoked[:over] {
			if o.At.After(d.RevokedFloor) {
				d.RevokedFloor = o.At
			}
		}
		d.Revoked = append([]revocation(nil), d.Revoked[over:]...)
	}
}

type document struct {
	Version   int                   `json:"version"`
	Mode      Mode                  `json:"mode"`
	Resources map[string]*record    `json:"resources"`
	Deleted   map[string]*tombstone `json:"deleted,omitempty"`
	Revoked   []revocation          `json:"revoked,omitempty"`
	// RevokedFloor is the time up to which lowerings may have been dropped
	// from Revoked for lack of room. A restore from a backup made before it
	// cannot tell what was lowered since.
	RevokedFloor time.Time `json:"revokedFloor,omitempty"`
}

func (d *document) clone() *document {
	c := &document{Version: d.Version, Mode: d.Mode, Resources: make(map[string]*record, len(d.Resources))}
	for id, r := range d.Resources {
		c.Resources[id] = r.copy()
	}
	if len(d.Deleted) > 0 {
		c.Deleted = make(map[string]*tombstone, len(d.Deleted))
		for id, t := range d.Deleted {
			n := *t
			c.Deleted[id] = &n
		}
	}
	c.Revoked = append([]revocation(nil), d.Revoked...)
	c.RevokedFloor = d.RevokedFloor
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
	// failed is set when a change was renamed into place but could not be made
	// durable. Memory then already holds the change, and no further write is
	// accepted until the store is reopened and reloaded from disk.
	failed bool
}

func lockDir(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
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

func load(dir string) (*document, error) {
	b, err := os.ReadFile(filepath.Join(dir, fileName)) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
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
	// Anything after the document, even a second valid one, is corruption.
	if _, err = dec.Token(); err != io.EOF {
		return nil, errors.New("addon policy store is corrupt")
	}
	if d.Resources == nil {
		d.Resources = map[string]*record{}
	}
	if len(d.Deleted) > MaxTombstones {
		return nil, errors.New("addon policy store is corrupt")
	}
	if len(d.Revoked) > MaxRevocations {
		return nil, errors.New("addon policy store is corrupt")
	}
	for _, v := range d.Revoked {
		if v.AddonID == "" || v.ResourceID == "" || v.UserID == "" || (v.Ceiling != "" && v.Ceiling != RoleViewer && v.Ceiling != RoleEditor) {
			return nil, errors.New("addon policy store is corrupt")
		}
	}
	if len(d.Revoked) >= MaxRevocations && d.RevokedFloor.IsZero() {
		// A store written before the floor existed was cut at this size:
		// anything older than its oldest entry may be gone.
		for _, v := range d.Revoked {
			if d.RevokedFloor.IsZero() || v.At.Before(d.RevokedFloor) {
				d.RevokedFloor = v.At
			}
		}
	}
	for id, t := range d.Deleted {
		if id == "" || t == nil || t.AddonID == "" || t.Owner == "" || t.Op == "" {
			return nil, errors.New("addon policy store is corrupt")
		}
	}
	for _, r := range d.Resources {
		if r == nil || r.AddonID == "" || r.Owner == "" || (r.State != stateActive && r.State != statePend && r.State != stateDeleting) || (r.State == stateDeleting) != (r.Delete != nil) {
			return nil, errors.New("addon policy store is corrupt")
		}
		for u, role := range r.Grants {
			if u == "" || u == r.Owner || (role != RoleViewer && role != RoleEditor) {
				return nil, errors.New("addon policy store is corrupt")
			}
		}
		for u, m := range r.Drain {
			if u == "" || (m.Writes == 0 && m.Reads == 0) || m.Writes > r.Epoch || m.Reads > r.Epoch {
				return nil, errors.New("addon policy store is corrupt")
			}
		}
	}
	return d, nil
}

// save reports renamed when the new file is already in place, so a failure
// after that point is known to leave disk ahead of the caller's memory.
func save(dir string, d *document) (renamed bool, err error) {
	b, err := json.Marshal(d)
	if err != nil {
		return false, err
	}
	return durableWrite(filepath.Join(dir, fileName), b)
}

// cleanTemps removes partial files that a crash left behind. The caller holds
// the directory lock, so no live writer owns them.
func cleanTemps(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, ".policy-*"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
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
	cleanTemps(dir)
	d, err := load(dir)
	if err == nil && d.Mode != mode {
		if len(d.Resources) > 0 {
			err = ErrMigrationRequired
		} else {
			d.Mode = mode
			_, err = save(dir, d)
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
	return *r.copy(), true
}

// mutate applies fn to a copy and publishes it only when it is durable, so a
// failed write never leaves memory ahead of disk.
func (s *Store) mutate(fn func(*document) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return ErrUnavailable
	}
	next := s.doc.clone()
	if err := fn(next); err != nil {
		return err
	}
	renamed, err := save(s.dir, next)
	if err != nil {
		if renamed {
			// The file on disk already holds this change. Memory must not stay
			// behind it (a revocation would look undone), so it rolls forward
			// and the store refuses further writes.
			s.doc = next
			s.failed = true
		}
		return ErrUnavailable
	}
	s.doc = next
	return nil
}

func (s *Store) hasDeleting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.doc.Resources {
		if r.State == stateDeleting {
			return true
		}
	}
	return false
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
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
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
	defer func() { _ = lock.Close() }()
	cleanTemps(dir)
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
	for _, t := range next.Deleted {
		if to == ModeLocalOwner {
			t.Owner = LocalOwnerID
		} else {
			t.Owner = namedOwner
		}
	}
	_, err = save(dir, next)
	return err
}

// syncDirectory makes a rename durable. It is a variable so tests can fail it.
var syncDirectory = func(d *os.File) error { return d.Sync() }

func durableWrite(path string, b []byte) (renamed bool, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".policy-")
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
