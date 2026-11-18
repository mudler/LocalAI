//go:build linux || darwin || windows

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Unlike the worker ledger, loss of this ledger is a launch failure. A durable
// pending record precedes exec: a crash between exec and recording identity must
// fence recovery, not silently create an untracked duplicate.
type processIdentity struct {
	PID         int    `json:"pid"`
	Boot        string `json:"boot"`
	Start       string `json:"start"`
	Incarnation string `json:"incarnation"`
	Pending     bool   `json:"pending"`
}
type processLedger struct {
	mu      sync.Mutex
	root    string
	lock    *os.File
	entries map[string]processIdentity
	failed  bool
}

func openLedger(root string, timeout time.Duration) (*processLedger, error) {
	if !filepath.IsAbs(root) {
		return nil, ErrRuntime
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	if e := checkStateRoot(root); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(root, "process.lock"), os.O_CREATE|os.O_RDWR|ledgerNoFollow, 0600) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if e != nil {
		return nil, e
	}
	if e = lockLedger(f); e != nil {
		_ = f.Close()
		return nil, e
	}
	l := &processLedger{root: root, lock: f, entries: map[string]processIdentity{}}
	fail := func(e error) (*processLedger, error) { _ = f.Close(); return nil, e }
	// Require exact OS identity and the platform's handle support even when
	// the ledger is empty.
	if _, e = identity(os.Getpid()); e != nil {
		return fail(e)
	}
	if e = requireHandleSupport(); e != nil {
		return fail(e)
	}
	marker, e := f.Stat()
	if e != nil {
		return fail(e)
	}
	b, e := os.ReadFile(filepath.Join(root, "processes.json")) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if os.IsNotExist(e) && marker.Size() != 0 {
		return fail(ErrRuntime)
	}
	if e != nil && !os.IsNotExist(e) {
		return fail(e)
	}
	if e == nil && (json.Unmarshal(b, &l.entries) != nil || l.entries == nil) {
		return fail(ErrRuntime)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for _, entry := range l.entries {
		if e = recoverProcess(ctx, entry); e != nil {
			return fail(e)
		}
	}
	l.entries = map[string]processIdentity{}
	if e = l.flush(); e != nil {
		return fail(e)
	}
	// Once initialized, missing metadata is corruption, not an empty owner.
	if _, e = f.WriteAt([]byte("initialized\n"), 0); e != nil {
		return fail(e)
	}
	if e = f.Sync(); e != nil {
		return fail(e)
	}
	return l, nil
}
func (l *processLedger) flush() error {
	b, e := json.Marshal(l.entries)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(l.root, "processes.tmp"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY|ledgerNoFollow, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = os.Rename(filepath.Join(l.root, "processes.tmp"), filepath.Join(l.root, "processes.json")); e != nil {
		return e
	}
	return syncLedgerDir(l.root)
}
func (l *processLedger) put(id string, p processIdentity) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed {
		return ErrRuntime
	}
	l.entries[id] = p
	if e := l.flush(); e != nil {
		l.failed = true
		return e
	}
	return nil
}
func (l *processLedger) remove(id, inc string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed {
		return ErrRuntime
	}
	if p, ok := l.entries[id]; ok && p.Incarnation == inc {
		delete(l.entries, id)
		if e := l.flush(); e != nil {
			l.failed = true
			return e
		}
	}
	return nil
}
func (l *processLedger) close() error { return l.lock.Close() }
