//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
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

func identity(pid int) (processIdentity, error) {
	boot, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return processIdentity{}, e
	}
	b, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if e != nil {
		return processIdentity{}, e
	}
	end := strings.LastIndex(string(b), ")")
	if end < 0 {
		return processIdentity{}, ErrRuntime
	}
	f := strings.Fields(string(b)[end+1:])
	if len(f) < 20 {
		return processIdentity{}, ErrRuntime
	}
	if _, e = strconv.ParseUint(f[19], 10, 64); e != nil {
		return processIdentity{}, ErrRuntime
	}
	return processIdentity{PID: pid, Boot: strings.TrimSpace(string(boot)), Start: f[19]}, nil
}
func openLedger(root string, timeout time.Duration) (*processLedger, error) {
	if !filepath.IsAbs(root) {
		return nil, ErrRuntime
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	resolved, e := filepath.EvalSymlinks(root)
	if e != nil || resolved != root {
		return nil, ErrRuntime
	}
	info, e := os.Stat(root)
	if e != nil || info.Mode().Perm()&0077 != 0 {
		return nil, ErrRuntime
	}
	f, e := os.OpenFile(filepath.Join(root, "process.lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		_ = f.Close()
		return nil, e
	}
	l := &processLedger{root: root, lock: f, entries: map[string]processIdentity{}}
	fail := func(e error) (*processLedger, error) { _ = f.Close(); return nil, e }
	// Require exact OS identity and pidfd support even when the ledger is empty.
	if _, e = identity(os.Getpid()); e != nil {
		return fail(e)
	}
	fd, e := unix.PidfdOpen(os.Getpid(), 0)
	if e != nil {
		return fail(e)
	}
	_ = unix.Close(fd)
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
func recoverProcess(ctx context.Context, p processIdentity) error {
	if p.Pending || p.PID <= 0 || p.Start == "" || p.Boot == "" || p.Incarnation == "" {
		return ErrRuntime
	}
	// Open the handle BEFORE reading identity. If the PID exits/recycles while
	// checking /proc, this handle still refers only to the process we opened.
	fd, e := unix.PidfdOpen(p.PID, 0)
	if errors.Is(e, unix.ESRCH) {
		return nil
	}
	if e != nil {
		return e
	}
	defer func() { _ = unix.Close(fd) }()
	now, e := identity(p.PID)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if now.Boot != p.Boot || now.Start != p.Start {
		return nil
	}
	if e = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); e != nil && !errors.Is(e, unix.ESRCH) {
		return e
	}
	if fd < 0 || fd > math.MaxInt32 {
		return ErrRuntime
	}
	for {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, e := unix.Poll(fds, 10)
		if e != nil && !errors.Is(e, unix.EINTR) {
			return e
		}
		if n > 0 && fds[0].Revents&unix.POLLIN != 0 {
			return nil
		}
		if e = ctx.Err(); e != nil {
			return e
		}
	}
}
func (l *processLedger) flush() error {
	b, e := json.Marshal(l.entries)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(l.root, "processes.tmp"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
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
	d, e := os.Open(l.root)
	if e != nil {
		return e
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
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
