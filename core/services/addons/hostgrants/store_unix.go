//go:build !windows

// SPDX-License-Identifier: MIT

package hostgrants

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

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
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|noFollowFlag, 0600) // #nosec G304 -- caller supplies a fixed file in the guarded owner root, never a request path; reject symlinks.
	if err != nil {
		return nil, err
	}
	s := &Store{lock: lock, persist: func(d document) (bool, error) { return persistFile(path, d) }}
	fail := func(e error) (*Store, error) { _ = s.Close(); return nil, fmt.Errorf("%w: %w", ErrUnavailable, e) }
	if err = lock.Chmod(0600); err != nil {
		return fail(err)
	}
	if err = lockFile(lock); err != nil {
		return fail(err)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|noFollowFlag, 0) // #nosec G304 -- same trusted owner-root file; NOFOLLOW plus mode/type checks below prevent file substitution.
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
