// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"errors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestJSONOwnershipSubprocess(t *testing.T) {
	if dir := os.Getenv("STUDIO_JSON_CONTENDER"); dir != "" {
		s, err := NewJSON(dir)
		if err == nil {
			_ = s.(io.Closer).Close()
			t.Fatal("contender acquired ownership")
		}
		return
	}
	ctx := context.Background()
	dir := t.TempDir()
	s, err := NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.(io.Closer).Close()
	if err := s.CreateChat(ctx, session.ChatRecord{ID: "a", Principal: "p"}); err != nil {
		t.Fatal(err)
	}
	path := s.(*jsonStore).file("a", ".entries")
	data := []byte("{torn")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	stale := s.(*jsonStore).stateFile("a", 99)
	if err := os.WriteFile(stale, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestJSONOwnershipSubprocess$")
	cmd.Env = append(os.Environ(), "STUDIO_JSON_CONTENDER="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("contender: %v %s", err, out)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(data) {
		t.Fatalf("loser mutated data: %q %v", got, err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("loser deleted stale state", err)
	}
}
func TestJSONConstructorFailureReleasesOwnership(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chats")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := NewJSON(dir); err == nil {
		s.(io.Closer).Close()
		t.Fatal("expected construction failure")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s, err := NewJSON(dir)
	if err != nil {
		t.Fatal("failure leaked ownership", err)
	}
	defer s.(io.Closer).Close()
}
func TestJSONRevisionLifetimeAndClose(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.(io.Closer).Close()
	if err := s.CreateChat(ctx, session.ChatRecord{ID: "a", Principal: "p"}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		_, token, err := s.(SelectionRevisionProvider).SelectionSnapshot(ctx, "p", "a")
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	old := snapshot()
	if snapshot() != old {
		t.Fatal("read changed token")
	}
	if err := s.SetAddonsOff(ctx, "p", "a", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if snapshot() == old {
		t.Fatal("persist did not change token")
	}
	if err := s.SetAddonsOff(ctx, "p", "a", nil); err != nil {
		t.Fatal(err)
	}
	if snapshot() == old {
		t.Fatal("ABA reused token")
	}
	old = snapshot()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_, _, err := s.(SelectionRevisionProvider).SelectionSnapshot(ctx, "p", "a")
				if err != nil && !errors.Is(err, os.ErrClosed) {
					t.Errorf("snapshot: %v", err)
				}
			}
		}()
	}
	c := s.(io.Closer)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListChats(ctx, "p", session.ChatCursor{}, 10); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed read: %v", err)
	}
	if err := s.SetAddonsOff(ctx, "p", "a", []string{"b"}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed write: %v", err)
	}
	if _, _, err := s.(SelectionRevisionProvider).SelectionSnapshot(ctx, "p", "a"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed snapshot: %v", err)
	}
	next, err := NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.(io.Closer).Close()
	_, token, err := next.(SelectionRevisionProvider).SelectionSnapshot(ctx, "p", "a")
	if err != nil {
		t.Fatal(err)
	}
	if token == old {
		t.Fatal("restart reused token")
	}
	if _, err := os.Stat(filepath.Join(dir, ".owner.lock")); err != nil {
		t.Fatal("sidecar removed", err)
	}
}
