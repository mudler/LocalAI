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
	"strings"
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

// record is the host's binding of an opaque resource ID to a resource kind and
// the immutable owner. Grants never include the owner. For an add-on kind the
// trusted add-on identity is also kept in AddonID; other kinds leave it empty.
type record struct {
	// Kind names what the resource is. It selects the role table, see
	// RegisterKind. An add-on resource has the kind "addon:<addonID>".
	Kind    string          `json:"kind"`
	AddonID string          `json:"addonId,omitempty"`
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
	// Local is the service's own name of a resource of a kind that is not an
	// add-on, such as the ID of a connector binding or the name of a
	// collection. One owner has at most one active record of a kind under a
	// name. See EnsureRef.
	Local string `json:"local,omitempty"`
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
	Kind    string    `json:"kind"`
	AddonID string    `json:"addonId,omitempty"`
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
	Kind string `json:"kind"`
	// AddonID is read only from a log written before kinds existed. Load moves
	// it into Kind and clears it.
	AddonID    string    `json:"addonId,omitempty"`
	ResourceID string    `json:"resourceId"`
	UserID     string    `json:"userId"`
	Ceiling    Role      `json:"ceiling,omitempty"`
	At         time.Time `json:"at"`
}

// MaxRevocations is a safety bound on the lowerings each log of the store
// keeps. There are two logs: one of the add-on resources, which a restore of
// an add-on reads, and one of the other kinds, so that churn on shared agents,
// bindings or collections can never push an add-on lowering out or move the
// floor of the add-on log. Each log is compacted as it grows: a lowering that a later and stricter or equal one
// of the same person on the same resource covers is dropped, so a person has
// at most three entries and the bound is reached only by many distinct
// people and resources. When it is reached, the oldest entries go and the
// floor of the log moves up to the time of the newest one lost: a restore
// from a backup older than the floor cannot know what was lowered, so it
// restores no access at all.
const MaxRevocations = 4096

// revocationCap is MaxRevocations; a test lowers it.
var revocationCap = MaxRevocations

// covers says whether the lowering r is at least as strict as o: a removal,
// the same ceiling, or viewer after editor. The roles of other kinds are not
// ordered, so only the same ceiling or a removal covers one of them.
func (r revocation) covers(o revocation) bool {
	return r.Ceiling == "" || r.Ceiling == o.Ceiling || (r.Ceiling == RoleViewer && o.Ceiling == RoleEditor)
}

// addRevocation records a lowering. It keeps the log exact for every restore
// time: a dropped entry is always covered by a later entry that is at least as
// strict, so any restore that would have applied the first applies the second.
func (d *document) addRevocation(v revocation) {
	if shareKind(v.Kind) {
		d.RevokedKinds, d.RevokedKindsFloor = appendRevocation(d.RevokedKinds, d.RevokedKindsFloor, v)
		return
	}
	d.Revoked, d.RevokedFloor = appendRevocation(d.Revoked, d.RevokedFloor, v)
}

// appendRevocation adds v to one log and returns the log and its floor.
func appendRevocation(log []revocation, floor time.Time, v revocation) ([]revocation, time.Time) {
	kept := log[:0:0]
	for _, o := range log {
		if o.Kind == v.Kind && o.ResourceID == v.ResourceID && o.UserID == v.UserID && !o.At.After(v.At) && v.covers(o) {
			continue
		}
		kept = append(kept, o)
	}
	log = append(kept, v)
	if over := len(log) - revocationCap; over > 0 {
		for _, o := range log[:over] {
			if o.At.After(floor) {
				floor = o.At
			}
		}
		log = append([]revocation(nil), log[over:]...)
	}
	return log, floor
}

// splitRevocations moves the lowerings of the kinds that are not add-ons
// out of the add-on log, where a store written before the two logs kept
// them. The floor of the old log stays on the add-on log: it may have been
// raised by any kind, and keeping it there can only make a restore stricter.
func (d *document) splitRevocations() {
	kept := d.Revoked[:0:0]
	for _, v := range d.Revoked {
		if shareKind(v.Kind) {
			d.RevokedKinds = append(d.RevokedKinds, v)
			d.migrated = true
			continue
		}
		kept = append(kept, v)
	}
	d.Revoked = kept
	sort.SliceStable(d.RevokedKinds, func(i, j int) bool { return d.RevokedKinds[i].At.Before(d.RevokedKinds[j].At) })
}

type document struct {
	Version   int                   `json:"version"`
	Mode      Mode                  `json:"mode"`
	Resources map[string]*record    `json:"resources"`
	Deleted   map[string]*tombstone `json:"deleted,omitempty"`
	// Revoked is the log of the lowerings of add-on resources.
	Revoked []revocation `json:"revoked,omitempty"`
	// RevokedFloor is the time up to which lowerings may have been dropped
	// from Revoked for lack of room. A restore from a backup made before it
	// cannot tell what was lowered since.
	RevokedFloor time.Time `json:"revokedFloor,omitempty"`
	// RevokedKinds and RevokedKindsFloor are the same for the kinds that are
	// not add-ons. No lowering of those kinds can move RevokedFloor.
	RevokedKinds      []revocation `json:"revokedKinds,omitempty"`
	RevokedKindsFloor time.Time    `json:"revokedKindsFloor,omitempty"`
	// migrated is set by load when it filled in kinds a store written before
	// kinds existed did not have, so OpenStore writes the new form back.
	migrated bool
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
	c.RevokedKinds = append([]revocation(nil), d.RevokedKinds...)
	c.RevokedKindsFloor = d.RevokedKindsFloor
	c.migrated = d.migrated
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
	if len(d.Revoked) > MaxRevocations || len(d.RevokedKinds) > MaxRevocations {
		return nil, errors.New("addon policy store is corrupt")
	}
	if !d.migrateKinds() {
		return nil, errors.New("addon policy store is corrupt")
	}
	for _, log := range [][]revocation{d.Revoked, d.RevokedKinds} {
		for _, v := range log {
			if v.Kind == "" || !wellFormedKind(v.Kind) || v.ResourceID == "" || v.UserID == "" || (v.Ceiling != "" && !grantRole(v.Ceiling)) {
				return nil, errors.New("addon policy store is corrupt")
			}
		}
	}
	// The log of the other kinds is written by this version only, which
	// never puts an add-on lowering there.
	for _, v := range d.RevokedKinds {
		if !shareKind(v.Kind) || v.AddonID != "" {
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
	d.splitRevocations()
	if len(d.RevokedKinds) > MaxRevocations {
		return nil, errors.New("addon policy store is corrupt")
	}
	for id, t := range d.Deleted {
		if id == "" || t == nil || t.Kind == "" || !kindMatchesAddon(t.Kind, t.AddonID) || t.Owner == "" || t.Op == "" {
			return nil, errors.New("addon policy store is corrupt")
		}
	}
	for _, r := range d.Resources {
		if r == nil || r.Kind == "" || r.Owner == "" || !kindMatchesAddon(r.Kind, r.AddonID) || (r.Local != "" && (r.AddonID != "" || !validLocal(r.Local))) || (r.State != stateActive && r.State != statePend && r.State != stateDeleting) || (r.State == stateDeleting) != (r.Delete != nil) {
			return nil, errors.New("addon policy store is corrupt")
		}
		for u, role := range r.Grants {
			if u == "" || u == r.Owner || !storedRole(r.Kind, role) {
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

// migrateKinds gives what a store written before kinds existed the kind of an
// add-on resource. Every such entry names its add-on, and the add-on kind is
// derived from it, so nothing a person was granted or lost changes. An entry
// that has neither is left as it is and fails the check in load. It reports
// false when a revocation names an add-on that its kind does not: that entry
// is corrupt, and clearing the field would hide it.
func (d *document) migrateKinds() bool {
	for _, r := range d.Resources {
		if r != nil && r.Kind == "" && r.AddonID != "" {
			r.Kind = addonKind(r.AddonID)
			d.migrated = true
		}
	}
	for _, t := range d.Deleted {
		if t != nil && t.Kind == "" && t.AddonID != "" {
			t.Kind = addonKind(t.AddonID)
			d.migrated = true
		}
	}
	for i := range d.Revoked {
		v := &d.Revoked[i]
		if v.Kind == "" && v.AddonID != "" {
			v.Kind = addonKind(v.AddonID)
			d.migrated = true
		}
		// The kind is the identity; keeping the old field too would give the
		// entry two sources of truth.
		if v.AddonID != "" {
			if v.Kind != addonKind(v.AddonID) {
				return false
			}
			v.AddonID = ""
			d.migrated = true
		}
	}
	return true
}

// storedRole says whether a grant of the role can be on a record of the kind.
// An add-on grants viewer and editor only. Whether another kind grants the role
// is known only once it registers, so the store accepts every grant role and
// the check denies a role the kind does not grant.
func storedRole(kind string, role Role) bool {
	if _, addon := addonOf(kind); addon {
		return role == RoleViewer || role == RoleEditor
	}
	return grantRole(role)
}

// wellFormedKind says whether a stored kind is usable: the add-on family needs
// an add-on ID after the prefix.
func wellFormedKind(kind string) bool {
	if strings.HasPrefix(kind, addonKindPrefix) {
		_, ok := addonOf(kind)
		return ok
	}
	return kind != ""
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
			d.migrated = true
		}
	}
	if err == nil && d.migrated {
		_, err = save(dir, d)
		d.migrated = false
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
