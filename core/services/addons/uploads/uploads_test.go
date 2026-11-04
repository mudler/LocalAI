// SPDX-License-Identifier: MIT
package uploads

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "uploads")
	return &Store{Root: root, Now: func() time.Time { return now }, MaxFiles: 2}, &now
}

func TestPutOpenAndOwner(t *testing.T) {
	s, _ := newStore(t)
	e, err := s.Put("alice", "x.zip", 1<<20, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Size != 5 || len(e.SHA256) != 64 || !ValidID(e.ID) {
		t.Fatalf("entry %+v", e)
	}
	p, got, err := s.Open(e.ID, "alice")
	if err != nil || got.ID != e.ID {
		t.Fatalf("open: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "hello" {
		t.Fatalf("content %q", b)
	}
	if _, _, err = s.Open(e.ID, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user must not open it: %v", err)
	}
	if _, _, err = s.Open("../etc/passwd", "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a path is not an ID: %v", err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if err = s.Remove(e.ID, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatal("another user removed it")
	}
	if err = s.Remove(e.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Open(e.ID, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatal("still there")
	}
}

func TestSizeLimitStopsTheUpload(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.Put("alice", "x.zip", 10, bytes.NewReader(make([]byte, 11)))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
	ents, _ := os.ReadDir(s.Root)
	if len(ents) != 0 {
		t.Fatalf("left files: %v", ents)
	}
}

func TestChunksMustFollowEachOther(t *testing.T) {
	s, _ := newStore(t)
	id, err := s.Begin("alice", "x.zip", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(id, "alice", 0, []byte("ab")); err != nil {
		t.Fatal(err)
	}
	if err = s.Append(id, "alice", 5, []byte("cd")); !errors.Is(err, ErrOffset) {
		t.Fatalf("gap: %v", err)
	}
	if err = s.Append(id, "bob", 2, []byte("cd")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user: %v", err)
	}
	if _, err = s.Finish(id, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user finish: %v", err)
	}
	e, err := s.Finish(id, "alice")
	if err != nil || e.Size != 2 {
		t.Fatalf("finish: %v %+v", err, e)
	}
}

func TestExpiryBusyAndPurge(t *testing.T) {
	s, now := newStore(t)
	a, _ := s.Put("alice", "a.zip", 100, strings.NewReader("a"))
	if _, err := s.Put("alice", "b.zip", 100, strings.NewReader("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("alice", "c.zip", 100, strings.NewReader("c")); !errors.Is(err, ErrBusy) {
		t.Fatalf("limit: %v", err)
	}
	*now = now.Add(2 * time.Hour)
	if _, _, err := s.Open(a.ID, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatal("an expired file opens")
	}
	if _, err := s.Put("alice", "c.zip", 100, strings.NewReader("c")); err != nil {
		t.Fatalf("sweep did not free room: %v", err)
	}
	s.Purge()
	ents, _ := os.ReadDir(s.Root)
	if len(ents) != 0 {
		t.Fatalf("purge left %v", ents)
	}
}

func TestRootMustBePrivate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{Root: root}
	if _, err := s.Put("alice", "x.zip", 10, strings.NewReader("x")); err == nil {
		t.Fatal("a world-readable root was accepted")
	}
}
