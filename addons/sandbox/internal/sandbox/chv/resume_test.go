//go:build linux

package chv

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func suspendForTest(t *testing.T, sb sandbox.Sandbox) sandbox.SnapshotRef {
	t.Helper()
	ref, err := sb.Suspend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func resumeEnvFor(st workspace.Store) sandbox.Env {
	return sandbox.Env{Store: st, Actor: sbtest.Admin}
}

func nopPort(_ context.Context, c net.Conn) { c.Close() }

// procsIn lists the host processes whose command line names dir.
func procsIn(t *testing.T, dir string) []string {
	t.Helper()
	paths, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	var out []string
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte(dir)) {
			out = append(out, p)
		}
	}
	return out
}

func TestResumeRefusesOtherActorPortsAndLiveSandbox(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, "")
	ref := suspendForTest(t, sb)
	ctx := context.Background()
	if _, err := b.Resume(ctx, ref, sandbox.Env{Store: st, Actor: workspace.Actor{Principal: "user:other"}}); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("other actor: %v, want ErrInvalid", err)
	}
	if _, err := b.Resume(ctx, ref, sandbox.Env{Store: st, Actor: sbtest.Admin, Ports: map[int]sandbox.PortHandler{7000: nopPort}}); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("other port set: %v, want ErrInvalid", err)
	}
	live, err := b.Resume(ctx, ref, resumeEnvFor(st))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { live.Destroy(context.Background()) })
	if _, err := b.Resume(ctx, ref, resumeEnvFor(st)); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("resume while live: %v, want ErrInvalid", err)
	}
	sh(t, live, "echo alive")
	if _, err := os.Stat(filepath.Join(ref.Path, "console.prev.log")); err != nil {
		t.Fatalf("the old console log was not kept: %v", err)
	}
}

func TestConcurrentResumeOfOneRefRunsOnce(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, "")
	ref := suspendForTest(t, sb)
	ctx := context.Background()
	type result struct {
		sb  sandbox.Sandbox
		err error
	}
	out := make([]result, 2)
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i].sb, out[i].err = b.Resume(ctx, ref, resumeEnvFor(st))
		}()
	}
	wg.Wait()
	var live sandbox.Sandbox
	ok := 0
	for _, r := range out {
		switch {
		case r.err == nil:
			ok++
			live = r.sb
			t.Cleanup(func() { r.sb.Destroy(context.Background()) })
		case !errors.Is(r.err, workspace.ErrInvalid):
			t.Fatalf("a refused resume failed with %v, want ErrInvalid", r.err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d resumes succeeded, want exactly 1", ok)
	}
	sh(t, live, "echo alive")
}

func TestDestroyOfSuspendedHandleKeepsResumedSandbox(t *testing.T) {
	b := testBackend(t)
	old, st := fixture(t, b, "")
	ref := suspendForTest(t, old)
	ctx := context.Background()
	live, err := b.Resume(ctx, ref, resumeEnvFor(st))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { live.Destroy(context.Background()) })
	if err := old.Destroy(ctx); err != nil {
		t.Fatalf("destroy of the stale handle: %v", err)
	}
	sh(t, live, "echo alive")
	if _, err := os.Stat(ref.Path); err != nil {
		t.Fatalf("the stale handle deleted the resumed sandbox: %v", err)
	}
	if err := live.Destroy(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ref.Path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the folder is still there after destroy: %v", err)
	}
	if p := procsIn(t, ref.Path); len(p) != 0 {
		t.Fatalf("processes left after destroy: %v", p)
	}
}

func TestSuspendSaveFailureKeepsSandboxRunning(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, "")
	s := sb.(*sbx)
	s.hooks.suspendSave = func() error { return errors.New("injected save failure") }
	ref, err := sb.Suspend(context.Background())
	if err == nil {
		t.Fatal("suspend with a failed save succeeded")
	}
	if ref != (sandbox.SnapshotRef{}) {
		t.Fatalf("a failed suspend returned a ref: %+v", ref)
	}
	s.hooks.suspendSave = nil
	sh(t, sb, "echo still running")
	suspendForTest(t, sb)
}
