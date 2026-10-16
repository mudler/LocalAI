package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"
)

// processLedger is the durable record of the backend process groups this
// worker owns, kept in a small file so a later worker, or the launcher that
// manages this one, can find the groups a crashed worker left behind.
//
// Backends run in their own process groups. When the worker is killed (SIGKILL,
// the OOM killer) nothing stops them: they keep their port and their GPU
// memory. The ledger is the record that survives.
//
// An entry is the group leader's pid, its start time and the launch generation
// that started it. The start time keeps a recycled pid from being mistaken for a
// backend. Ownership is released only once the whole group is gone, not when the
// leader exits: descendants can outlive their leader. A nil ledger does nothing.
type processLedger struct {
	path string
	// generation is the launch generation stored with each new record. Empty
	// for a worker that no launcher manages.
	generation string

	mu      sync.Mutex
	entries map[string]ledgerEntry
}

type ledgerEntry struct {
	Key        string `json:"key"`
	PID        int    `json:"pid"`
	StartTime  string `json:"start_time"`
	Generation string `json:"generation,omitempty"`
}

func newProcessLedger(path string) *processLedger {
	return &processLedger{path: path, entries: map[string]ledgerEntry{}}
}

// Ownership states of a recorded group.
type groupOwnership int

const (
	// ownershipGone: no live process group has the recorded id.
	ownershipGone groupOwnership = iota
	// ownershipForeign: the recorded pid now leads an unrelated group.
	ownershipForeign
	// ownershipOwned: a live group whose identity matches the record.
	ownershipOwned
	// ownershipUncertain: a live group that cannot be proven to be ours.
	ownershipUncertain
)

// reapTimeout bounds how long a verified group is given to disappear after it
// is killed. It is a variable only so tests can shorten it.
var reapTimeout = 5 * time.Second

// killGroup is the signal sender; tests replace it to model a kill that fails.
var killGroup = killProcessGroup

// classify decides whose a recorded group is. A group id cannot be reused while
// any member lives, so a missing leader with a live group is ours; a live leader
// with a different start time is a recycled pid.
func classify(e ledgerEntry) groupOwnership {
	if e.PID <= 0 || !groupAlive(e.PID) {
		return ownershipGone
	}
	if e.StartTime == "" {
		return ownershipUncertain
	}
	now := readStartTime(e.PID)
	switch {
	case now == "" && startTimesReadable():
		return ownershipOwned // the leader exited; its group lives on
	case now == "":
		return ownershipUncertain
	case now != e.StartTime:
		return ownershipForeign
	}
	return ownershipOwned
}

// reap kills a verified group and waits until it is empty.
func reap(e ledgerEntry) error {
	if err := killGroup(e.PID); err != nil && groupAlive(e.PID) {
		return fmt.Errorf("signalling process group %d: %w", e.PID, err)
	}
	deadline := time.Now().Add(reapTimeout)
	for groupAlive(e.PID) {
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d still has live members after %s", e.PID, reapTimeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil
}

// add records a started process before the worker relies on it. An error means
// ownership is not durable, and the caller must not run the backend.
func (l *processLedger) add(key string, pid int) error {
	if l == nil {
		return nil
	}
	if pid <= 0 {
		return fmt.Errorf("recording ownership of %s: no process id", key)
	}
	start := readStartTime(pid)
	if start == "" && startTimesReadable() {
		return fmt.Errorf("recording ownership of %s: cannot read the start time of process %d", key, pid)
	}
	generation := l.generation
	if len(generation) > 128 {
		generation = generation[:128]
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	previous, had := l.entries[key]
	l.entries[key] = ledgerEntry{Key: key, PID: pid, StartTime: start, Generation: generation}
	if err := l.flushLocked(); err != nil {
		if had {
			l.entries[key] = previous
		} else {
			delete(l.entries, key)
		}
		return fmt.Errorf("recording ownership of %s: %w", key, err)
	}
	return nil
}

// release gives up ownership of a process the worker stopped. The record is
// dropped only when the group is verified empty: a group that still has members
// is killed first, and stays recorded if it cannot be emptied or identified.
func (l *processLedger) release(key string) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return nil
	}
	switch classify(e) {
	case ownershipUncertain:
		return fmt.Errorf("ownership of process group %d (%s) cannot be verified", e.PID, key)
	case ownershipOwned:
		if err := reap(e); err != nil {
			return fmt.Errorf("releasing %s: %w", key, err)
		}
	}
	delete(l.entries, key)
	return l.flushLocked()
}

// sweepStale cleans up the groups a previous worker recorded and did not
// release. It runs once, before this worker starts any backend. It returns how
// many groups it killed and an error when any record could not be resolved
// (unverifiable identity, a group that survived, an unreadable ledger). Such
// records are kept and nothing unverified is signalled; the caller must not
// start backends.
func (l *processLedger) sweepStale() (int, error) {
	if l == nil {
		return 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading the worker process ledger %s: %w", l.path, err)
	}
	var stale []ledgerEntry
	if err := json.Unmarshal(data, &stale); err != nil {
		return 0, fmt.Errorf("the worker process ledger %s is unreadable, so ownership of earlier backends is unknown: %w", l.path, err)
	}
	killed := 0
	remaining := map[string]ledgerEntry{}
	var problems []string
	for _, e := range stale {
		switch classify(e) {
		case ownershipGone, ownershipForeign:
		case ownershipUncertain:
			remaining[e.Key] = e
			problems = append(problems, fmt.Sprintf("%s (group %d: ownership cannot be verified)", e.Key, e.PID))
		case ownershipOwned:
			if err := reap(e); err != nil {
				remaining[e.Key] = e
				problems = append(problems, fmt.Sprintf("%s (%v)", e.Key, err))
				continue
			}
			killed++
			xlog.Warn("Killed a backend process group left behind by a previous worker", "processKey", e.Key, "pid", e.PID)
		}
	}
	l.entries = remaining
	if err := l.flushLocked(); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return killed, fmt.Errorf("backend ownership is unresolved: %s", strings.Join(problems, "; "))
	}
	return killed, nil
}

// corruptStartTimeForTest makes an entry look like a recycled pid.
func (l *processLedger) corruptStartTimeForTest(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	e.StartTime = "not-the-start-time"
	l.entries[key] = e
	_ = l.flushLocked()
}

// forgetStartTimeForTest makes an entry look like one recorded where the start
// time cannot be read.
func (l *processLedger) forgetStartTimeForTest(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	e.StartTime = ""
	l.entries[key] = e
	_ = l.flushLocked()
}

func (l *processLedger) flushLocked() error {
	list := make([]ledgerEntry, 0, len(l.entries))
	for _, e := range l.entries {
		list = append(list, e)
	}
	data, err := json.Marshal(list)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(l.path), 0o750)
	}
	if err == nil {
		err = writeFileAtomic(l.path, data)
	}
	if err != nil {
		xlog.Warn("Failed to write the worker process ledger", "path", l.path, "error", err)
	}
	return err
}

// writeFileAtomic replaces path with data through a synced, owner-only
// temporary file so a crash leaves either the old or the new content.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// startTimesReadable reports whether this system can tell a process's start
// time, through /proc or ps.
func startTimesReadable() bool { return readStartTime(os.Getpid()) != "" }

// procRoot is where the kernel process files live. It is a variable so tests
// can model a system without it, such as macOS.
var procRoot = "/proc"

// readStartTime returns a process's start time as the system reports it, or ""
// when it cannot be read (the process is gone, or neither /proc nor ps works).
// Values from /proc and from ps are never compared with each other: one system
// uses one source for the lifetime of a ledger.
var readStartTime = portableStartTime

func portableStartTime(pid int) string {
	if _, err := os.Stat(filepath.Join(procRoot, "self", "stat")); err == nil {
		return procStartTime(pid)
	}
	return psStartTime(pid)
}

func procStartTime(pid int) string {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return ""
	}
	// The command name is in parentheses and may hold spaces; the fields after
	// the last ')' are space separated and start at field 3. Field 22 is the
	// start time.
	s := string(data)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return ""
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}

// psStartTime asks ps for the process start time (one second granularity),
// which is how macOS and the BSDs expose it.
func psStartTime(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	// A fixed locale keeps the text identical for every process that reads it.
	cmd.Env = []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return "ps:" + strings.Join(fields, " ")
}
