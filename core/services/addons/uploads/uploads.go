// SPDX-License-Identifier: MIT

// Package uploads is the owner's staging area for add-on zip files. A file
// waits here between the upload and the install the administrator confirms.
// It is private to the owner, bound to the user that uploaded it, and
// removed after a short time, after the install and at start.
package uploads

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

var (
	// ErrNotFound: no such file, or it belongs to another user, or it expired.
	ErrNotFound = errors.New("staged file not found")
	// ErrTooLarge: the upload went over the byte limit.
	ErrTooLarge = errors.New("file is over the size limit")
	// ErrBusy: too many files are waiting.
	ErrBusy = errors.New("too many staged files")
	// ErrOffset: a chunk did not follow the bytes received so far.
	ErrOffset = errors.New("chunk out of order")
)

// Defaults of a store.
const (
	DefaultTTL      = time.Hour
	DefaultMaxFiles = 16
)

var idRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidID reports whether s has the shape of a staged file ID.
func ValidID(s string) bool { return idRE.MatchString(s) }

// Entry is a staged file.
type Entry struct {
	ID      string    `json:"id"`
	Owner   string    `json:"owner"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
	Created time.Time `json:"created"`
}

// Store keeps staged files under Root.
type Store struct {
	Root     string
	TTL      time.Duration
	MaxFiles int
	// Now is replaced by tests.
	Now func() time.Time

	mu   sync.Mutex
	open map[string]*upload
}

type upload struct {
	owner, name string
	f           *os.File
	h           hash.Hash
	n           int64
	max         int64
	started     time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return DefaultTTL
}

func (s *Store) maxFiles() int {
	if s.MaxFiles > 0 {
		return s.MaxFiles
	}
	return DefaultMaxFiles
}

func (s *Store) dir() (string, error) {
	if s.Root == "" || !filepath.IsAbs(s.Root) {
		return "", errors.New("uploads: root must be an absolute path")
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(s.Root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("uploads: root must be a private directory")
	}
	return s.Root, nil
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Store) zipPath(id string) string  { return filepath.Join(s.Root, id+".zip") }
func (s *Store) metaPath(id string) string { return filepath.Join(s.Root, id+".json") }

// Begin opens a new upload for owner. max is the byte limit of the file.
func (s *Store) Begin(owner, name string, max int64) (string, error) {
	if owner == "" || max <= 0 {
		return "", ErrNotFound
	}
	if _, err := s.dir(); err != nil {
		return "", err
	}
	s.Sweep()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open == nil {
		s.open = map[string]*upload{}
	}
	staged, _ := filepath.Glob(filepath.Join(s.Root, "*.zip"))
	if len(staged)+len(s.open) >= s.maxFiles() {
		return "", ErrBusy
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(s.zipPath(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- the name is a random ID under the private staging root
	if err != nil {
		return "", err
	}
	s.open[id] = &upload{owner: owner, name: name, f: f, h: sha256.New(), max: max, started: s.now()}
	return id, nil
}

// Append adds the next chunk. offset is the number of bytes received before it.
func (s *Store) Append(id, owner string, offset int64, p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.open[id]
	if !ok || u.owner != owner {
		return ErrNotFound
	}
	if offset != u.n {
		return ErrOffset
	}
	if int64(len(p)) > u.max-u.n {
		s.abortLocked(id)
		return ErrTooLarge
	}
	if _, err := u.f.Write(p); err != nil {
		s.abortLocked(id)
		return err
	}
	_, _ = u.h.Write(p)
	u.n += int64(len(p))
	return nil
}

// Finish closes the upload and makes it a staged file.
func (s *Store) Finish(id, owner string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.open[id]
	if !ok || u.owner != owner {
		return Entry{}, ErrNotFound
	}
	delete(s.open, id)
	e := Entry{ID: id, Owner: owner, Name: u.name, Size: u.n, SHA256: hex.EncodeToString(u.h.Sum(nil)), Created: s.now().UTC()}
	err := u.f.Sync()
	if ce := u.f.Close(); err == nil {
		err = ce
	}
	if err == nil {
		var b []byte
		if b, err = json.Marshal(e); err == nil {
			err = os.WriteFile(s.metaPath(id), b, 0o600)
		}
	}
	if err != nil {
		_ = os.Remove(s.zipPath(id))
		return Entry{}, err
	}
	return e, nil
}

// Abort drops an upload that is not finished. It is safe to call twice.
func (s *Store) Abort(id, owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.open[id]; ok && u.owner == owner {
		s.abortLocked(id)
	}
}

func (s *Store) abortLocked(id string) {
	if u, ok := s.open[id]; ok {
		_ = u.f.Close()
		delete(s.open, id)
	}
	_ = os.Remove(s.zipPath(id))
}

// Put stores the whole of r as a staged file of owner, up to max bytes.
func (s *Store) Put(owner, name string, max int64, r io.Reader) (Entry, error) {
	id, err := s.Begin(owner, name, max)
	if err != nil {
		return Entry{}, err
	}
	buf := make([]byte, 256<<10)
	var off int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if err = s.Append(id, owner, off, buf[:n]); err != nil {
				return Entry{}, err
			}
			off += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			s.Abort(id, owner)
			return Entry{}, rerr
		}
	}
	return s.Finish(id, owner)
}

// Open returns the path and the entry of a staged file of owner.
func (s *Store) Open(id, owner string) (string, Entry, error) {
	if !ValidID(id) || owner == "" {
		return "", Entry{}, ErrNotFound
	}
	e, err := s.read(id)
	if err != nil || e.Owner != owner || s.expired(e) {
		return "", Entry{}, ErrNotFound
	}
	return s.zipPath(id), e, nil
}

func (s *Store) read(id string) (Entry, error) {
	var e Entry
	b, err := os.ReadFile(s.metaPath(id)) // #nosec G304 -- the name is a validated random ID under the private staging root
	if err != nil {
		return e, ErrNotFound
	}
	if json.Unmarshal(b, &e) != nil || e.ID != id {
		return Entry{}, ErrNotFound
	}
	return e, nil
}

func (s *Store) expired(e Entry) bool { return s.now().Sub(e.Created) > s.ttl() }

// Remove deletes a staged file of owner.
func (s *Store) Remove(id, owner string) error {
	if _, _, err := s.Open(id, owner); err != nil {
		return err
	}
	return s.removeFiles(id)
}

func (s *Store) removeFiles(id string) error {
	err := os.Remove(s.zipPath(id))
	_ = os.Remove(s.metaPath(id))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove staged file: %w", err)
	}
	return nil
}

// Sweep removes the files that are older than the time to live and the
// uploads that never finished. With purge set it removes everything that is
// not an open upload, as the owner does at start.
func (s *Store) Sweep() { s.sweep(false) }

// Purge removes every staged file. Call it once at start.
func (s *Store) Purge() { s.sweep(true) }

func (s *Store) sweep(all bool) {
	if _, err := s.dir(); err != nil {
		return
	}
	// The lock covers the whole sweep, so a Finish in progress is never seen
	// half done.
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, u := range s.open {
		if all || s.now().Sub(u.started) > s.ttl() {
			s.abortLocked(id)
		}
	}
	open := map[string]bool{}
	for id := range s.open {
		open[id] = true
	}
	ents, err := os.ReadDir(s.Root)
	if err != nil {
		return
	}
	for _, de := range ents {
		name := de.Name()
		id := name[:len(name)-len(filepath.Ext(name))]
		if !ValidID(id) || open[id] {
			continue
		}
		e, rerr := s.read(id)
		if all || rerr != nil || s.expired(e) {
			_ = s.removeFiles(id)
		}
	}
}
