// SPDX-License-Identifier: MIT
// Package workerstatus defines the private, opt-in launcher observation channel.
// It carries evidence, never credentials or process-control instructions.
package workerstatus

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const Schema = 1
const Capability = "localai-worker-status-v1"
const PathEnv = "LOCALAI_LAUNCHER_STATUS_PATH"
const GenerationEnv = "LOCALAI_LAUNCHER_GENERATION"
const MaxSize = 16 * 1024
const PollInterval = 2 * time.Second
const ApprovalInterval = 10 * time.Second
const ApprovalMaxAge = 30 * time.Second

var ErrUnavailable = errors.New("worker status unavailable")

type Snapshot struct {
	PID               int           `json:"pid"`
	Schema            int           `json:"schema_version"`
	Generation        string        `json:"generation"`
	Sequence          uint64        `json:"sequence"`
	Time              time.Time     `json:"time"`
	Registration      string        `json:"registration"`
	RegistrationTime  time.Time     `json:"registration_time"`
	Credentials       bool          `json:"credentials"`
	Authenticated     bool          `json:"nats_authenticated"`
	Connected         bool          `json:"nats_connected"`
	Subscriptions     bool          `json:"lifecycle_subscriptions"`
	Armed             bool          `json:"readiness_armed"`
	LocalReady        bool          `json:"local_ready"`
	Heartbeat         time.Time     `json:"heartbeat_ack"`
	HeartbeatInterval time.Duration `json:"heartbeat_interval_ns"`
	Failure           string        `json:"failure"`
}

func (s Snapshot) valid() bool {
	if s.Schema != Schema || len(s.Generation) == 0 || len(s.Generation) > 128 || s.Sequence == 0 || s.Time.IsZero() || s.HeartbeatInterval <= 0 || s.HeartbeatInterval > time.Duration(1<<63-1)/3 {
		return false
	}
	switch s.Registration {
	case "starting", "pending", "healthy", "error", "unknown":
	default:
		return false
	}
	switch s.Failure {
	case "", "registration", "credentials", "nats", "subscriptions", "readiness", "heartbeat", "stopping":
	default:
		return false
	}
	return true
}

// NewSession creates a distinct 0700 directory even for consecutive starts.
// The caller retains it until owned-process cleanup completes.
func NewSession(parent string) (path, generation string, err error) {
	dir, e := os.MkdirTemp(parent, "worker-run-")
	if e != nil {
		return "", "", ErrUnavailable
	}
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		os.Remove(dir)
		return "", "", ErrUnavailable
	}
	return filepath.Join(dir, "status.json"), hex.EncodeToString(b), nil
}

func directory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Base(path) != "status.json" {
		return nil, ErrUnavailable
	}
	fd, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	f := os.NewFile(uintptr(fd), "worker status directory")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 {
		f.Close()
		return nil, ErrUnavailable
	}
	return f, nil
}
func openSnapshot(dir *os.File) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), "status.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "worker status")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Size > MaxSize {
		f.Close()
		return nil, ErrUnavailable
	}
	return f, nil
}
func Write(path string, s Snapshot) error {
	if !s.valid() {
		return ErrUnavailable
	}
	data, err := json.Marshal(s)
	if err != nil || len(data) > MaxSize {
		return ErrUnavailable
	}
	dir, err := directory(path)
	if err != nil {
		return ErrUnavailable
	}
	defer dir.Close()
	old, err := openSnapshot(dir)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return ErrUnavailable
	}
	if old != nil {
		old.Close()
	}
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return ErrUnavailable
	}
	name := ".status-" + hex.EncodeToString(b)
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return ErrUnavailable
	}
	defer unix.Unlinkat(int(dir.Fd()), name, 0)
	f := os.NewFile(uintptr(fd), "temporary worker status")
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return ErrUnavailable
	}
	if _, err = f.Write(data); err != nil {
		return ErrUnavailable
	}
	if f.Sync() != nil {
		return ErrUnavailable
	}
	if unix.Renameat(int(dir.Fd()), name, int(dir.Fd()), "status.json") != nil {
		return ErrUnavailable
	}
	if dir.Sync() != nil {
		_ = unix.Unlinkat(int(dir.Fd()), "status.json", 0)
		return ErrUnavailable
	}
	return nil
}
func Read(path, generation string) (Snapshot, error) {
	var s Snapshot
	dir, err := directory(path)
	if err != nil {
		return s, ErrUnavailable
	}
	defer dir.Close()
	f, err := openSnapshot(dir)
	if err != nil {
		return s, ErrUnavailable
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil || len(data) > MaxSize {
		return s, ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || d.Decode(new(any)) != io.EOF || !s.valid() || s.Generation != generation {
		return Snapshot{}, ErrUnavailable
	}
	return s, nil
}

// Tracker measures sequence progress using the caller's monotonic clock. Polling
// an unchanged file cannot renew its lease. Never share it across generations.
type Tracker struct {
	Generation string
	// ExpectedPID must come from the owned child, not from the snapshot. The
	// per-launch generation prevents PID reuse across launches.
	ExpectedPID int
	Started     time.Time
	sequence    uint64
	advanced    time.Time
	publication time.Time
	age         time.Duration
	last        Snapshot
}

func (t *Tracker) Classify(s Snapshot, now time.Time) string {
	if !s.valid() || s.Generation != t.Generation || s.Sequence < t.sequence || t.ExpectedPID <= 0 || s.PID != t.ExpectedPID {
		return "status_unavailable"
	}
	if s.Sequence > t.sequence {
		// Wall time is used only at sequence admission. Thereafter use the
		// local monotonic clock; polling must not grant a new evidence lease.
		age := now.Sub(s.Time)
		if age < 0 || age >= 10*time.Second || (!t.publication.IsZero() && s.Time.Before(t.publication)) || s.RegistrationTime.After(s.Time) || s.Heartbeat.After(s.Time) {
			return "status_unavailable"
		}
		t.sequence = s.Sequence
		t.advanced = now
		t.publication = s.Time
		t.age = age
		t.last = s
	}
	if s != t.last || now.Before(t.advanced) || t.age+now.Sub(t.advanced) >= 10*time.Second {
		return "status_unavailable"
	}
	if s.Failure != "" {
		return s.Failure
	}
	approvalCurrent := !s.RegistrationTime.IsZero() && s.Time.Sub(s.RegistrationTime)+t.age+now.Sub(t.advanced) < ApprovalMaxAge
	if s.Registration == "pending" && approvalCurrent {
		return "pending"
	}
	if s.Registration == "starting" && now.Sub(t.Started) < 30*time.Second {
		return "starting"
	}
	if s.Registration != "healthy" || !approvalCurrent {
		return "registration"
	}
	if !s.Credentials {
		return "credentials"
	}
	if !s.Connected {
		return "nats"
	}
	if !s.Authenticated {
		return "credentials"
	}
	if !s.Subscriptions {
		return "subscriptions"
	}
	if !s.Armed || !s.LocalReady {
		return "readiness"
	}
	if s.Heartbeat.IsZero() || s.Heartbeat.After(s.Time) || s.Time.Sub(s.Heartbeat)+t.age+now.Sub(t.advanced) > 3*s.HeartbeatInterval {
		return "heartbeat"
	}
	return "ready"
}

func removeSnapshot(dir *os.File) error { return unix.Unlinkat(int(dir.Fd()), "status.json", 0) }
