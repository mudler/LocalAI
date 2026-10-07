package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/mudler/xlog"
)

// processLedger remembers the backend processes this worker started, in a small
// file, so the next worker can find the ones this one left behind.
//
// Backends run in their own process groups. When the worker is killed (SIGKILL,
// the OOM killer) nothing stops them: they keep their port and their GPU
// memory, and a restarted worker has no record of them. The ledger is that
// record. A restarted worker kills every group it lists before it serves.
//
// An entry is the leader's pid and its start time. The start time is what keeps
// a recycled pid from being mistaken for a backend. A nil ledger does nothing.
type processLedger struct {
	path string

	mu      sync.Mutex
	entries map[string]ledgerEntry
}

type ledgerEntry struct {
	Key       string `json:"key"`
	PID       int    `json:"pid"`
	StartTime string `json:"start_time"`
}

func newProcessLedger(path string) *processLedger {
	return &processLedger{path: path, entries: map[string]ledgerEntry{}}
}

// add records a started process. A failure is logged and ignored: losing the
// ledger costs the orphan sweep, never a start.
func (l *processLedger) add(key string, pid int) {
	if l == nil || pid <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[key] = ledgerEntry{Key: key, PID: pid, StartTime: procStartTime(pid)}
	l.flushLocked()
}

// remove forgets a process the worker stopped itself.
func (l *processLedger) remove(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.entries[key]; !ok {
		return
	}
	delete(l.entries, key)
	l.flushLocked()
}

// sweepStale kills the process groups a previous worker recorded and did not
// stop, and returns how many it killed. It runs once, before this worker starts
// any backend, so every entry in the file belongs to a predecessor.
func (l *processLedger) sweepStale() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := os.ReadFile(l.path)
	if err != nil {
		return 0
	}
	var stale []ledgerEntry
	if err := json.Unmarshal(data, &stale); err != nil {
		xlog.Warn("Ignoring an unreadable worker process ledger", "path", l.path, "error", err)
	}
	killed := 0
	for _, e := range stale {
		if e.PID <= 0 {
			continue
		}
		// Without the leader's start time at the time it was recorded, there is no
		// way to tell the process apart from an unrelated one that reused the pid
		// (the start time is only readable on Linux). Such an entry is never
		// killed.
		if e.StartTime == "" {
			continue
		}
		// The leader still exists with another start time: its pid was reused by
		// an unrelated process. A group that lost its leader cannot be reused (the
		// kernel keeps the pid while the group lives), so a missing leader is
		// ours.
		if now := procStartTime(e.PID); now != "" && now != e.StartTime {
			continue
		}
		if err := killProcessGroup(e.PID); err == nil {
			killed++
			xlog.Warn("Killed a backend process group left behind by a previous worker", "processKey", e.Key, "pid", e.PID)
		}
	}
	l.entries = map[string]ledgerEntry{}
	l.flushLocked()
	return killed
}

// corruptStartTimeForTest makes an entry look like a recycled pid.
func (l *processLedger) corruptStartTimeForTest(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	e.StartTime = "not-the-start-time"
	l.entries[key] = e
	l.flushLocked()
}

// forgetStartTimeForTest makes an entry look like one recorded where the start
// time cannot be read.
func (l *processLedger) forgetStartTimeForTest(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	e.StartTime = ""
	l.entries[key] = e
	l.flushLocked()
}

func (l *processLedger) flushLocked() {
	list := make([]ledgerEntry, 0, len(l.entries))
	for _, e := range l.entries {
		list = append(list, e)
	}
	data, err := json.Marshal(list)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(l.path), 0o750)
	}
	if err == nil {
		tmp := l.path + ".tmp"
		if err = os.WriteFile(tmp, data, 0o600); err == nil {
			err = os.Rename(tmp, l.path)
		}
	}
	if err != nil {
		xlog.Warn("Failed to write the worker process ledger", "path", l.path, "error", err)
	}
}

// procStartTime returns a process's start time as the kernel reports it, or ""
// when it cannot be read: the process is gone, or there is no /proc (the sweep
// is Linux only; elsewhere an entry is never killed).
func procStartTime(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
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
