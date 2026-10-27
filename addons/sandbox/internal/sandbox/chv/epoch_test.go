//go:build linux

package chv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// TestReleasedHandleCannotCheckpoint checks the ownership epoch: after a
// Resume through another handle, the handle that Suspend released neither
// checkpoints nor writes state.json.
func TestReleasedHandleCannotCheckpoint(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	sb1, st := fixture(t, b, "")
	ref := suspendForTest(t, sb1)
	sb2, err := b.Resume(ctx, ref, resumeEnvFor(st))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sb2.Destroy(context.Background()) })
	sh(t, sb2, "echo two > /workspace/proj/two.txt")
	statePath := filepath.Join(ref.Path, "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sb1.Checkpoint(ctx, "stale"); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("checkpoint of the released handle: %v, want ErrGone", err)
	}
	if err := sb1.Refresh(ctx); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("refresh of the released handle: %v, want ErrGone", err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("state.json changed:\n%s\n---\n%s", before, after)
	}
	// The live handle still commits its own work.
	if _, _, err := sb2.Checkpoint(ctx, "live"); err != nil {
		t.Fatalf("checkpoint of the live handle: %v", err)
	}
}

// TestStaleDestroyKeepsLaterSnapshot checks that a late Destroy of an old
// handle leaves both a resumed sandbox and a later snapshot of the folder.
func TestStaleDestroyKeepsLaterSnapshot(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	sb1, st := fixture(t, b, "")
	ref := suspendForTest(t, sb1)
	sb2, err := b.Resume(ctx, ref, resumeEnvFor(st))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sb2.Destroy(context.Background()) })
	if err := sb1.Destroy(ctx); err != nil {
		t.Fatalf("destroy of the stale handle: %v", err)
	}
	sh(t, sb2, "echo alive")
	ref2 := suspendForTest(t, sb2)
	if err := sb1.Destroy(ctx); err != nil {
		t.Fatalf("second destroy of the stale handle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ref2.Path, "snapshot")); err != nil {
		t.Fatalf("the stale handle deleted the later snapshot: %v", err)
	}
	sb3, err := b.Resume(ctx, ref2, resumeEnvFor(st))
	if err != nil {
		t.Fatalf("resume of the later snapshot: %v", err)
	}
	t.Cleanup(func() { sb3.Destroy(context.Background()) })
	sh(t, sb3, "echo alive again")
}
