package worker

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/pkg/workerledger"
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

type ledgerEntry = workerledger.Entry

func newProcessLedger(path string) *processLedger {
	return &processLedger{path: path, entries: map[string]ledgerEntry{}}
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
	start := workerledger.ReadStartTime(pid)
	if start == "" && workerledger.StartTimesReadable() {
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
	switch workerledger.Classify(e) {
	case workerledger.Uncertain:
		return fmt.Errorf("ownership of process group %d (%s) cannot be verified", e.PID, key)
	case workerledger.Owned:
		if err := workerledger.Reap(e); err != nil {
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

	stale, err := workerledger.ReadFile(l.path)
	if err != nil {
		return 0, fmt.Errorf("worker process ledger %s: %w", l.path, err)
	}
	killed := 0
	remaining := map[string]ledgerEntry{}
	var problems []string
	for _, e := range stale {
		switch workerledger.Classify(e) {
		case workerledger.Gone, workerledger.Foreign:
		case workerledger.Uncertain:
			remaining[e.Key] = e
			problems = append(problems, fmt.Sprintf("%s (group %d: ownership cannot be verified)", e.Key, e.PID))
		case workerledger.Owned:
			if err := workerledger.Reap(e); err != nil {
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
	err := workerledger.WriteFile(l.path, list)
	if err != nil {
		xlog.Warn("Failed to write the worker process ledger", "path", l.path, "error", err)
	}
	return err
}
