// SPDX-License-Identifier: MIT
package hostsettings

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// Store serializes one owner's policy transactions. The parent directory is
// trusted owner storage; callers must not remove the lock file or share this
// file over a filesystem without flock and atomic rename/fsync semantics.
type Store struct {
	mu      sync.Mutex
	state   state
	lock    *os.File
	persist func(document) (renamed bool, err error)
}

// Open loads an existing document, or imports a validated sole legacy owner
// policy if absent. Corruption never triggers migration or default policy.
func Open(path string, migration *Migration) (*Store, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600) // #nosec G304 -- caller supplies a fixed file in the guarded owner root, never a request path; reject symlinks.
	if err != nil {
		return nil, err
	}
	s := &Store{lock: lock, state: unavailable{}}
	fail := func(e error) (*Store, error) { _ = s.Close(); return nil, fmt.Errorf("%w: %w", ErrUnavailable, e) }
	if err = lock.Chmod(0600); err != nil {
		return fail(err)
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(err)
	}
	s.persist = func(d document) (bool, error) { return persistFile(path, d) }
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0) // #nosec G304 -- same trusted owner-root file; NOFOLLOW plus mode/type checks below prevent file substitution.
	var d document
	if os.IsNotExist(err) {
		d, err = migrate(migration)
		if err != nil {
			return fail(err)
		}
		_, err = s.persist(d)
		if err != nil {
			return fail(err)
		}
	} else {
		if err != nil {
			return fail(err)
		}
		info, statErr := f.Stat()
		if statErr != nil {
			_ = f.Close()
			return fail(statErr)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxDocumentBytes {
			_ = f.Close()
			return fail(ErrInvalid)
		}
		b, readErr := io.ReadAll(io.LimitReader(f, maxDocumentBytes+1))
		closeErr := f.Close()
		if err = errors.Join(readErr, closeErr); err != nil {
			return fail(err)
		}
		if len(b) > maxDocumentBytes {
			return fail(ErrInvalid)
		}
		if err = strictJSON(b, &d); err != nil {
			return fail(err)
		}
		if err = validateDocument(d); err != nil {
			return fail(err)
		}
	}
	s.state = ready{d}
	return s, nil
}

func validateSnapshot(s Snapshot) error {
	if s.SchemaVersion != SchemaVersion || s.Revision == 0 || !provenanceValid(s.Migration) {
		return ErrInvalid
	}
	return validate(s.Policy)
}

func validateDocument(d document) error {
	if err := validateSnapshot(d.Snapshot); err != nil {
		return err
	}
	if len(d.Receipts) > MaxReceipts || uint64(len(d.Receipts)) > d.Snapshot.Revision-1 {
		return ErrInvalid
	}
	revisions := map[uint64]bool{}
	for id, r := range d.Receipts {
		hash, err := hex.DecodeString(r.Body)
		if !validOperationID(id) || err != nil || len(hash) != 32 || r.Body != strings.ToLower(r.Body) {
			return ErrInvalid
		}
		if err = validateSnapshot(r.Snapshot); err != nil {
			return err
		}
		rev := r.Snapshot.Revision
		if rev < 2 || rev > d.Snapshot.Revision || revisions[rev] || r.Snapshot.Migration != d.Snapshot.Migration {
			return ErrInvalid
		}
		revisions[rev] = true
		if rev == d.Snapshot.Revision {
			a, _ := json.Marshal(r.Snapshot)
			b, _ := json.Marshal(d.Snapshot)
			if string(a) != string(b) {
				return ErrInvalid
			}
		}
	}
	if d.Snapshot.Revision > 1 && !revisions[d.Snapshot.Revision] {
		return ErrInvalid
	}
	return nil
}

func validOperationID(id string) bool {
	return len(id) > 0 && len(id) <= 128 && strings.TrimSpace(id) == id
}

func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if r, ok := s.state.(ready); ok {
		return clone(r.doc.Snapshot), nil
	}
	return Snapshot{}, ErrUnavailable
}

// Update checks replay before CAS. Once persistence begins it completes even
// if ctx is cancelled: a caller that loses the response must reconcile using
// the same operation ID and body, never generate a fresh ID blindly.
func (s *Store) Update(ctx context.Context, patch Patch, expectedRevision uint64, operationID string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	r := reduce(s.state, update{Patch: patch, Revision: expectedRevision, ID: operationID})
	s.state = r.State
	if r.Persist == nil {
		return r.Reply, r.Err
	}
	renamed, err := s.persist(*r.Persist)
	var completion event = persisted{}
	if err != nil {
		if renamed {
			completion = durabilityUnknown{}
		} else {
			completion = writeFailed{}
		}
	}
	r = reduce(s.state, completion)
	s.state = r.State
	return r.Reply, r.Err
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = unavailable{}
	if s.lock == nil {
		return nil
	}
	err := errors.Join(syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN), s.lock.Close())
	s.lock = nil
	return err
}

// The boolean separates a safely rejected write from an ambiguous commit.
func persistFile(path string, d document) (bool, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return false, err
	}
	if len(b) > maxDocumentBytes {
		return false, ErrInvalid
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	defer func() { _ = dir.Close() }() // The explicit Sync below determines commit durability.
	f, err := os.CreateTemp(filepath.Dir(path), ".host-policy-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(f.Name()) }() // Best-effort removal; successful rename already removed this name.
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return false, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return false, err
	}
	if err = dir.Sync(); err != nil {
		return true, err
	}
	return true, nil
}
