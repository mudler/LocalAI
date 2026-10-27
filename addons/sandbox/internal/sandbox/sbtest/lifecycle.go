package sbtest

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func testLifecycle(t *testing.T, newBackend NewBackend) {
	t.Run("refresh shows content another writer committed", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		f.mustRun("echo mine > proj/mine.txt")
		if _, err := f.store.Put(ctx(t), Admin, "/proj/fresh.txt", strings.NewReader("fresh\n"), workspace.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := f.sb.Refresh(ctx(t)); err != nil {
			t.Fatal(err)
		}
		if got := f.mustRun("cat proj/fresh.txt proj/mine.txt"); got != "fresh\nmine\n" {
			t.Fatalf("after refresh = %q", got)
		}
		if got, _ := f.read("/proj/mine.txt"); got != "mine\n" {
			t.Fatalf("refresh did not commit first: %q", got)
		}
		f.mustRun("echo again >> proj/mine.txt")
		if _, c := f.checkpoint("after refresh"); len(c) != 0 {
			t.Fatalf("conflicts = %+v", c)
		}
		if got, _ := f.read("/proj/mine.txt"); got != "mine\nagain\n" {
			t.Fatalf("mine.txt = %q", got)
		}
	})
	t.Run("suspend and resume keep a running process and its open files", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		_, err := f.sb.Exec(ctx(t), sandbox.Cmd{Args: []string{"sh", "-c",
			`exec 3<proj/notes.txt 4>>proj/tick.txt; while :; do cat /dev/fd/3 >/dev/null && echo tick >&4; sleep 0.1; done`}})
		if err != nil {
			t.Fatal(err)
		}
		f.mustRun(`while [ ! -s proj/tick.txt ]; do sleep 0.1; done`)
		ref, err := f.sb.Suspend(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.sb.Exec(ctx(t), sandbox.Cmd{Args: []string{"true"}}); !errors.Is(err, sandbox.ErrGone) {
			t.Fatalf("exec after suspend: %v", err)
		}
		sb, err := f.b.Resume(ctx(t), ref, sandbox.Env{Store: f.store, Actor: Agent})
		if err != nil {
			t.Fatal(err)
		}
		f.sb = sb
		if got := f.mustRun(`a=$(wc -l < proj/tick.txt); sleep 1; b=$(wc -l < proj/tick.txt); [ "$b" -gt "$a" ] && echo alive`); got != "alive\n" {
			t.Fatalf("after resume = %q", got)
		}
		if _, c := f.checkpoint("after resume"); len(c) != 0 {
			t.Fatalf("conflicts = %+v", c)
		}
		if got, _ := f.read("/proj/tick.txt"); !strings.HasPrefix(got, "tick\n") {
			t.Fatalf("tick.txt = %q", got)
		}
	})
	t.Run("destroy keeps committed changes", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		f.mustRun("echo kept > proj/kept.txt")
		f.checkpoint("keep")
		f.mustRun("echo lost > proj/lost.txt")
		if err := f.sb.Destroy(ctx(t)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}}); !errors.Is(err, sandbox.ErrGone) {
			t.Fatalf("exec after destroy: %v", err)
		}
		if got, _ := f.read("/proj/kept.txt"); got != "kept\n" {
			t.Fatalf("kept.txt = %q", got)
		}
		if _, ok := f.read("/proj/lost.txt"); ok {
			t.Fatal("destroy committed an uncommitted file")
		}
	})
	t.Run("a process from before a suspend returns ErrGone", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		p, err := f.sb.Exec(ctx(t), sandbox.Cmd{Args: []string{"sleep", "60"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.sb.Suspend(ctx(t)); err != nil {
			t.Fatal(err)
		}
		requireGone(t, p)
	})
	t.Run("a process from before a destroy returns ErrGone", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		p, err := f.sb.Exec(ctx(t), sandbox.Cmd{Args: []string{"sleep", "60"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.sb.Destroy(ctx(t)); err != nil {
			t.Fatal(err)
		}
		requireGone(t, p)
	})
}

// requireGone checks that a process that started before a Suspend or Destroy
// returns ErrGone from Wait, Signal and Resize. Wait has a timeout, so a
// backend that blocks fails the test instead of hanging it.
func requireGone(t *testing.T, p sandbox.Process) {
	t.Helper()
	waited := make(chan error, 1)
	go func() {
		_, err := p.Wait()
		waited <- err
	}()
	select {
	case err := <-waited:
		if !errors.Is(err, sandbox.ErrGone) {
			t.Fatalf("wait = %v, want ErrGone", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("wait of a process from before a suspend or destroy blocked")
	}
	if err := p.Signal(syscall.SIGTERM); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("signal = %v, want ErrGone", err)
	}
	if err := p.Resize(10, 10); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("resize = %v, want ErrGone", err)
	}
}
