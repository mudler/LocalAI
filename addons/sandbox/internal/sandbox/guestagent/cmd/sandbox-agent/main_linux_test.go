package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
)

// A failed boot must end in a power-off request after a pause, then hold
// PID 1 with a long sleep. The test pause ends the goroutine on the hold.
func TestHaltPowersOffAfterAPause(t *testing.T) {
	var steps []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		halt(
			func(format string, args ...any) { steps = append(steps, "log: "+format) },
			func(d time.Duration) {
				steps = append(steps, "pause "+d.String())
				if d == time.Hour {
					runtime.Goexit()
				}
			},
			func() { steps = append(steps, "sync") },
			func() error { steps = append(steps, "poweroff"); return errors.New("refused") },
			"boot: %s", "x",
		)
	}()
	<-done
	want := "log: boot: %s|pause 2s|sync|poweroff|pause 1h0m0s"
	if got := strings.Join(steps, "|"); got != want {
		t.Fatalf("steps = %s, want %s", got, want)
	}
}

// nnpChildEnv marks the re-executed test process that holds the children.
const nnpChildEnv = "SANDBOX_AGENT_NNP_CHILD"

// TestNoNewPrivsReachesChildren runs the check in a fresh process, so the
// flag that setNoNewPrivs sets does not change this test process.
//
// The child is built with CGO_ENABLED=0, as the agent is: AllThreadsSyscall
// returns ENOTSUP in a binary that links libc through cgo.
func TestNoNewPrivsReachesChildren(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agent.test")
	build := exec.Command("go", "test", "-c", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the child: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "-test.run=^TestNoNewPrivsChild$", "-test.v")
	cmd.Env = append(os.Environ(), nnpChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestNoNewPrivsChild") {
		t.Fatalf("child did not run its checks:\n%s", out)
	}
}

// TestNoNewPrivsChild spawns 60 real children, concurrently, through the
// spawner, and checks that each one reports NoNewPrivs: 1. The flag is per
// thread, so setNoNewPrivs must reach every thread, not only the caller's.
func TestNoNewPrivsChild(t *testing.T) {
	if os.Getenv(nnpChildEnv) == "" {
		t.Skip("runs only as the child of TestNoNewPrivsReachesChildren")
	}
	if err := setNoNewPrivs(); err != nil {
		t.Fatal(err)
	}
	sp := &guestagent.LinuxSpawner{Env: []string{"PATH=" + os.Getenv("PATH")}, Dir: t.TempDir(), Reap: true}
	bad := make(chan string, 60)
	var wg sync.WaitGroup
	for range 60 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := sp.Spawn(guestagent.ExecRequest{Args: []string{"sh", "-c", "grep NoNewPrivs /proc/$$/status"}})
			if err != nil {
				bad <- err.Error()
				return
			}
			defer p.Close()
			out, _ := io.ReadAll(p.Stdout())
			_, _, _ = p.Wait()
			if !strings.Contains(string(out), "NoNewPrivs:\t1") {
				bad <- strings.TrimSpace(string(out))
			}
		}()
	}
	wg.Wait()
	close(bad)
	for b := range bad {
		t.Errorf("child without no_new_privs: %q", b)
	}
}

// The termination signals must not stop PID 1. The test process would die
// on them without the Notify.
func TestTerminationSignalsAreDrained(t *testing.T) {
	ignoreSignals()
	for _, s := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT} {
		if err := syscall.Kill(os.Getpid(), s); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
}
