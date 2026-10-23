// SPDX-License-Identifier: MIT

// Package workerledger holds the process identity rules shared by a worker,
// which records the backend process groups it owns, and the launcher that
// manages it and must find those groups again after a crash.
//
// A record is the group leader's pid, its start time and the launch generation.
// The start time keeps a recycled pid from being mistaken for a backend.
package workerledger

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
	"time"
)

// Entry is one recorded backend process group.
type Entry struct {
	Key        string `json:"key"`
	PID        int    `json:"pid"`
	StartTime  string `json:"start_time"`
	Generation string `json:"generation,omitempty"`
}

// Ownership states of a recorded group.
type Ownership int

const (
	// Gone: no live process group has the recorded id.
	Gone Ownership = iota
	// Foreign: the recorded pid now leads an unrelated group.
	Foreign
	// Owned: a live group whose identity matches the record.
	Owned
	// Uncertain: a live group that cannot be proven to be ours.
	Uncertain
)

// ReapTimeout bounds how long a verified group is given to disappear after it
// is killed. It is a variable only so tests can shorten it.
var ReapTimeout = 5 * time.Second

// KillGroup is the signal sender; tests replace it to model a kill that fails.
var KillGroup = killProcessGroup

// ProcRoot is where the kernel process files live. It is a variable so tests
// can model a system without it, such as macOS.
var ProcRoot = "/proc"

// ReadStartTime returns a process's start time as the system reports it, or ""
// when it cannot be read (the process is gone, or neither /proc nor ps works).
// Values from /proc and from ps are never compared with each other: one system
// uses one source for the lifetime of a ledger.
var ReadStartTime = portableStartTime

// ReadFile loads the entries of a ledger file. A missing file is an empty
// ledger; an unreadable or corrupt one is an error, because ownership of the
// earlier processes is then unknown.
func ReadFile(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the process ledger: %w", err)
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("the process ledger is unreadable, so ownership of earlier backends is unknown: %w", err)
	}
	return entries, nil
}

// WriteFile replaces path with entries through a synced, owner-only temporary
// file so a crash leaves either the old or the new content.
func WriteFile(path string, entries []Entry) error {
	if entries == nil {
		entries = []Entry{}
	}
	data, err := json.Marshal(entries)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o750)
	}
	if err != nil {
		return err
	}
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

// Classify decides whose a recorded group is. A group id cannot be reused while
// any member lives, so a missing leader with a live group is ours; a live leader
// with a different start time is a recycled pid.
func Classify(e Entry) Ownership {
	if e.PID <= 0 || !GroupAlive(e.PID) {
		return Gone
	}
	if e.StartTime == "" {
		return Uncertain
	}
	now := ReadStartTime(e.PID)
	switch {
	case now == "" && StartTimesReadable():
		return Owned // the leader exited; its group lives on
	case now == "":
		return Uncertain
	case now != e.StartTime:
		return Foreign
	}
	return Owned
}

// Reap kills a verified group and waits until it is empty.
func Reap(e Entry) error {
	if err := KillGroup(e.PID); err != nil && GroupAlive(e.PID) {
		return fmt.Errorf("signalling process group %d: %w", e.PID, err)
	}
	deadline := time.Now().Add(ReapTimeout)
	for GroupAlive(e.PID) {
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d still has live members after %s", e.PID, ReapTimeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil
}

// StartTimesReadable reports whether this system can tell a process's start
// time, through /proc or ps.
func StartTimesReadable() bool { return ReadStartTime(os.Getpid()) != "" }

func portableStartTime(pid int) string {
	if _, err := os.Stat(filepath.Join(ProcRoot, "self", "stat")); err == nil {
		return procStartTime(pid)
	}
	return psStartTime(pid)
}

func procStartTime(pid int) string {
	data, err := os.ReadFile(filepath.Join(ProcRoot, strconv.Itoa(pid), "stat"))
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
