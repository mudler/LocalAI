// SPDX-License-Identifier: MIT
package hostgrants

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Store serializes the owner's grant transactions. The parent directory is
// trusted owner storage; callers must not remove the lock file or share the
// file over a filesystem without flock and atomic rename and fsync semantics.
// The persistence follows the pattern of the host settings store: atomic
// rename, directory fsync, compare-and-set on the revision and operation
// receipts for idempotent replay.
type Store struct {
	mu        sync.Mutex
	doc       document
	lock      *os.File
	closed    bool
	uncertain bool
	persist   func(document) (renamed bool, err error)
}

// Result is the outcome of Update.
type Result struct {
	Snapshot Snapshot
	// Replayed is true when the operation ID had already been applied with
	// the same patch. Snapshot is then the current state, which may be newer
	// than the state the original call produced.
	Replayed bool
}

// Open loads the document at path. A missing file is an empty document at
// revision 0 and is not created until the first Update. A corrupt or
// unreadable file is an error: it is never replaced by a default.
func Open(path string) (*Store, error) {
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
	s := &Store{lock: lock, persist: func(d document) (bool, error) { return persistFile(path, d) }}
	fail := func(e error) (*Store, error) { _ = s.Close(); return nil, fmt.Errorf("%w: %w", ErrUnavailable, e) }
	if err = lock.Chmod(0600); err != nil {
		return fail(err)
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(err)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0) // #nosec G304 -- same trusted owner-root file; NOFOLLOW plus mode/type checks below prevent file substitution.
	if os.IsNotExist(err) {
		s.doc = document{Snapshot: emptySnapshot(), Receipts: map[string]receipt{}}
		return s, nil
	}
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
	var d document
	if len(b) > maxDocumentBytes || strictJSON(b, &d) != nil {
		return fail(ErrInvalid)
	}
	if d.Receipts == nil {
		d.Receipts = map[string]receipt{}
	}
	if d.Grants == nil {
		d.Grants = map[string]AddonGrants{}
	}
	if d.Deny == nil {
		d.Deny = map[string][]string{}
	}
	if d.Hints == nil {
		d.Hints = map[string][]string{}
	}
	if err = validateDocument(d); err != nil {
		return fail(err)
	}
	s.doc = d
	return s, nil
}

func strictJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func (s *Store) usable() error {
	switch {
	case s.closed:
		return ErrUnavailable
	case s.uncertain:
		return ErrUncertain
	}
	return nil
}

// Usable reports whether the store answers: it is neither closed nor in doubt
// about its last write. A caller that must fail closed asks it before it
// trusts View, which hides the failure behind an empty document.
func (s *Store) Usable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usable()
}

// Snapshot returns a copy of the current document.
func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if err := s.usable(); err != nil {
		return Snapshot{}, err
	}
	return s.doc.Snapshot.Clone(), nil
}

// View returns the current document without copying it, for the per-call
// check of the broker. The snapshot is immutable: every Update publishes a new
// one and nothing modifies a published one, so the caller must treat it as
// read-only. A closed or uncertain store yields an empty document at revision
// 0, which grants nothing from the UI and denies nothing; the policy file
// deny rules do not depend on it.
func (s *Store) View() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usable() != nil {
		return emptySnapshot()
	}
	return s.doc.Snapshot
}

// Update applies patch if the document is at expectedRevision. A repeated
// operationID with the same patch returns Replayed without a write, checked
// before the revision; the same ID with another patch is ErrOperationMismatch.
// Once persistence begins it completes even if ctx is cancelled. After
// ErrUncertain the store refuses all calls until it is reopened; the caller
// then replays the same operation ID and patch.
func (s *Store) Update(ctx context.Context, patch Patch, expectedRevision uint64, operationID string) (Result, error) {
	return s.UpdateAudited(ctx, patch, expectedRevision, operationID, nil)
}

// UpdateAudited is Update with a hook that records the change before it is
// written. The hook gets the snapshot the write would publish and runs only for
// a write that is going ahead: not for a replay, a conflict or an invalid
// patch. When it fails nothing is written and the error is ErrAuditFailed, so a
// grant never exists without its audit line. A write that fails after the hook
// leaves an audit line for a change that was not applied; the caller sees the
// error.
func (s *Store) UpdateAudited(ctx context.Context, patch Patch, expectedRevision uint64, operationID string, audit func(after Snapshot) error) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := s.usable(); err != nil {
		return Result{}, err
	}
	if !validOperationID(operationID) {
		return Result{}, invalid("operation ID")
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return Result{}, invalid("patch")
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	if r, ok := s.doc.Receipts[operationID]; ok {
		if r.Body != hash {
			return Result{}, ErrOperationMismatch
		}
		return Result{Snapshot: s.doc.Snapshot.Clone(), Replayed: true}, nil
	}
	if expectedRevision != s.doc.Revision {
		return Result{}, ErrConflict
	}
	next, err := apply(s.doc.Snapshot, patch)
	if err != nil {
		return Result{}, err
	}
	next.Revision = s.doc.Revision + 1
	if audit != nil {
		if err := audit(next.Clone()); err != nil {
			return Result{}, errors.Join(ErrAuditFailed, err)
		}
	}
	nd := document{Snapshot: next, Receipts: make(map[string]receipt, len(s.doc.Receipts)+1)}
	for id, r := range s.doc.Receipts {
		nd.Receipts[id] = r
	}
	nd.Receipts[operationID] = receipt{Body: hash, Revision: next.Revision}
	for len(nd.Receipts) > MaxReceipts {
		var oldestID string
		var oldest uint64
		for id, r := range nd.Receipts {
			if oldestID == "" || r.Revision < oldest {
				oldestID, oldest = id, r.Revision
			}
		}
		delete(nd.Receipts, oldestID)
	}
	renamed, err := s.persist(nd)
	if err != nil {
		if renamed {
			s.uncertain = true
			return Result{}, errors.Join(ErrUncertain, err)
		}
		return Result{}, err
	}
	s.doc = nd
	return Result{Snapshot: next.Clone()}, nil
}

// Close releases the lock. The store refuses all calls afterwards.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
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
		return false, invalid("document too large")
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	defer func() { _ = dir.Close() }() // The explicit Sync below determines commit durability.
	f, err := os.CreateTemp(filepath.Dir(path), ".host-grants-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(f.Name()) }() // Best-effort removal; a successful rename already removed this name.
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
