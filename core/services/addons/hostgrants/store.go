// SPDX-License-Identifier: MIT

package hostgrants

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
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

// strictJSON decodes JSON with no unknown fields and no trailing data.
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
	err := errors.Join(unlockFile(s.lock), s.lock.Close())
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
