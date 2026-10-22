// SPDX-License-Identifier: MIT
package resources

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
	selectionFile = "selections.json"
	selectionLock = "selections.lock"
	// DefaultSelectionTTL is how long an unused selection is kept.
	DefaultSelectionTTL = 90 * 24 * time.Hour
	// DefaultMaxSelectionsPerUser and DefaultMaxSelections bound the store.
	DefaultMaxSelectionsPerUser = 256
	DefaultMaxSelections        = 8192
	// refreshAfter is how old a used selection gets before its use writes it
	// again. Reads do not write more often than this.
	refreshAfter = 24 * time.Hour
)

// ErrSelectionStore means the selection store cannot be used.
var ErrSelectionStore = errors.New("selection store unavailable")

type selectionEntry struct {
	User       string    `json:"user"`
	Conv       string    `json:"conversation"`
	Addon      string    `json:"addon"`
	ResourceID string    `json:"resourceId"`
	UsedAt     time.Time `json:"usedAt"`
}

type selectionDocument struct {
	Version int              `json:"version"`
	Entries []selectionEntry `json:"entries"`
}

// SelectionOptions bound the store. Zero values select the finite defaults;
// a bound can never be disabled.
type SelectionOptions struct {
	TTL        time.Duration
	MaxPerUser int
	MaxTotal   int
	Now        func() time.Time
	// SyncDirectory makes a rename durable. The zero value syncs the
	// directory; tests inject a failing one.
	SyncDirectory func(*os.File) error
}

// SelectionStore keeps which resource a user chose in a conversation, per
// add-on. It stores an opaque resource ID keyed by the authenticated user, the
// conversation and the add-on: the reference is a convenience and never a
// grant, since every use is authorized again by the owner's policy. The
// store is bounded per user and in total, expires unused entries and evicts a
// user's least recently used entry before it refuses a new one. One process
// holds it at a time; frontends reach it through the owner.
type SelectionStore struct {
	dir  string
	opts SelectionOptions
	lock *os.File
	mu   sync.Mutex
	// entries is keyed by user, conversation and add-on.
	entries map[[3]string]selectionEntry
	// failed is set when a change reached disk but could not be made
	// durable: memory then holds it, and no further write is accepted.
	failed bool
	closed bool
}

// OpenSelections opens or creates the store in dir. A corrupt file is never
// reset: it is an error and the owner reports it.
func OpenSelections(dir string, o SelectionOptions) (*SelectionStore, error) {
	if dir == "" {
		return nil, ErrSelectionStore
	}
	if o.TTL <= 0 {
		o.TTL = DefaultSelectionTTL
	}
	if o.MaxPerUser <= 0 || o.MaxPerUser > DefaultMaxSelectionsPerUser {
		o.MaxPerUser = DefaultMaxSelectionsPerUser
	}
	if o.MaxTotal <= 0 || o.MaxTotal > DefaultMaxSelections {
		o.MaxTotal = DefaultMaxSelections
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.SyncDirectory == nil {
		o.SyncDirectory = func(d *os.File) error { return d.Sync() }
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, selectionLock), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, ErrSelectionStore
	}
	s := &SelectionStore{dir: dir, opts: o, lock: lock, entries: map[[3]string]selectionEntry{}}
	matches, _ := filepath.Glob(filepath.Join(dir, ".selections-*"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
	if err = s.load(); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return s, nil
}

func (s *SelectionStore) load() error {
	b, err := os.ReadFile(filepath.Join(s.dir, selectionFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var d selectionDocument
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil || d.Version != 1 {
		return errors.New("addon selection store is corrupt")
	}
	if _, err = dec.Token(); err != io.EOF {
		return errors.New("addon selection store is corrupt")
	}
	if len(d.Entries) > DefaultMaxSelections {
		return errors.New("addon selection store is corrupt")
	}
	for _, e := range d.Entries {
		if e.User == "" || e.Conv == "" || e.Addon == "" || e.ResourceID == "" || len(e.User) > 256 || len(e.Conv) > 128 || len(e.Addon) > 64 || len(e.ResourceID) > 64 {
			return errors.New("addon selection store is corrupt")
		}
		s.entries[[3]string{e.User, e.Conv, e.Addon}] = e
	}
	return nil
}

// Close releases the store. Nothing is written after it returns.
func (s *SelectionStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.lock.Close()
}

func (s *SelectionStore) live(e selectionEntry, now time.Time) bool {
	return now.Sub(e.UsedAt) < s.opts.TTL
}

// Get returns the stored resource ID. An expired entry is absent. A use of
// an entry that is older than a day writes its time again, so a selection in
// use does not expire; that write is best effort.
func (s *SelectionStore) Get(user, conv, addon string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", false, ErrSelectionStore
	}
	e, ok := s.entries[[3]string{user, conv, addon}]
	now := s.opts.Now()
	if !ok || !s.live(e, now) {
		return "", false, nil
	}
	if now.Sub(e.UsedAt) > refreshAfter && !s.failed {
		next := s.clone()
		e.UsedAt = now
		next[[3]string{user, conv, addon}] = e
		_ = s.commit(next)
	}
	return e.ResourceID, true, nil
}

func (s *SelectionStore) clone() map[[3]string]selectionEntry {
	c := make(map[[3]string]selectionEntry, len(s.entries)+1)
	for k, v := range s.entries {
		c[k] = v
	}
	return c
}

// Put stores the selection. A user at the limit loses the entry that was
// used least recently; the store as a whole at its limit first drops expired
// entries and then refuses.
func (s *SelectionStore) Put(user, conv, addon, resourceID string) error {
	if user == "" || conv == "" || addon == "" || resourceID == "" || len(user) > 256 || len(conv) > 128 || len(addon) > 64 || len(resourceID) > 64 {
		return ErrSelectionStore
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.failed {
		return ErrSelectionStore
	}
	now := s.opts.Now()
	next := s.clone()
	for k, e := range next {
		if !s.live(e, now) {
			delete(next, k)
		}
	}
	key := [3]string{user, conv, addon}
	next[key] = selectionEntry{User: user, Conv: conv, Addon: addon, ResourceID: resourceID, UsedAt: now}
	var mine []selectionEntry
	for _, e := range next {
		if e.User == user {
			mine = append(mine, e)
		}
	}
	if len(mine) > s.opts.MaxPerUser {
		sort.Slice(mine, func(i, j int) bool {
			if !mine[i].UsedAt.Equal(mine[j].UsedAt) {
				return mine[i].UsedAt.Before(mine[j].UsedAt)
			}
			return mine[i].Conv+mine[i].Addon < mine[j].Conv+mine[j].Addon
		})
		for _, e := range mine[:len(mine)-s.opts.MaxPerUser] {
			if k := [3]string{e.User, e.Conv, e.Addon}; k != key {
				delete(next, k)
			}
		}
	}
	if len(next) > s.opts.MaxTotal {
		return ErrSelectionStore
	}
	return s.commit(next)
}

// Delete removes one selection. A missing one is not an error.
func (s *SelectionStore) Delete(user, conv, addon string) error {
	return s.drop(func(e selectionEntry) bool { return e.User == user && e.Conv == conv && e.Addon == addon })
}

func (s *SelectionStore) drop(match func(selectionEntry) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.failed {
		return ErrSelectionStore
	}
	next := s.clone()
	changed := false
	for k, e := range next {
		if match(e) {
			delete(next, k)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.commit(next)
}

// Len reports the number of stored entries, expired ones included.
func (s *SelectionStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// commit makes next durable and publishes it. A change that reached the
// directory entry but not stable storage is kept in memory and stops further
// writes until the store is reopened.
func (s *SelectionStore) commit(next map[[3]string]selectionEntry) error {
	doc := selectionDocument{Version: 1, Entries: make([]selectionEntry, 0, len(next))}
	for _, e := range next {
		doc.Entries = append(doc.Entries, e)
	}
	sort.Slice(doc.Entries, func(i, j int) bool {
		a, b := doc.Entries[i], doc.Entries[j]
		return a.User+"\x00"+a.Conv+"\x00"+a.Addon < b.User+"\x00"+b.Conv+"\x00"+b.Addon
	})
	b, err := json.Marshal(doc)
	if err != nil {
		return ErrSelectionStore
	}
	renamed, err := durableWrite(filepath.Join(s.dir, selectionFile), b, s.opts.SyncDirectory)
	if err != nil {
		if renamed {
			s.entries = next
			s.failed = true
		}
		return ErrSelectionStore
	}
	s.entries = next
	return nil
}

func durableWrite(path string, b []byte, syncDir func(*os.File) error) (renamed bool, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".selections-")
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
	return true, syncDir(d)
}
