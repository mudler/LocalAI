//go:build linux

package gvisor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The integration tests need a host that can run the backend and a guest
// image:
//
//	SANDBOX_TEST_RUNSC  path to runsc, with its gvisor-bin folder next to it;
//	                    an AppArmor profile for it must be loaded
//	SANDBOX_TEST_IMAGE  the guest image, name@sha256:... or oci-layout:<dir>@sha256:...
//	SANDBOX_TEST_CACHE  unpacked images; default <tmp>/sandbox-gvisor-test-cache
//
// They skip, and say why, when the first two are not set. When both are set
// a backend that cannot start fails the test: CI must not pass by skipping.

var cacheOnce sync.Once

func testCache(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("SANDBOX_TEST_CACHE")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "sandbox-gvisor-test-cache")
	}
	cacheOnce.Do(func() { _ = os.MkdirAll(dir, 0o700) })
	return dir
}

func testBackend(t *testing.T) *Backend {
	t.Helper()
	runsc, img := os.Getenv("SANDBOX_TEST_RUNSC"), os.Getenv("SANDBOX_TEST_IMAGE")
	if runsc == "" || img == "" {
		t.Skip("set SANDBOX_TEST_RUNSC (runsc with gvisor-bin next to it) and SANDBOX_TEST_IMAGE (name@sha256:... or oci-layout:<dir>@sha256:...)")
	}
	b, err := New(Options{StateDir: shortDir(t), CacheDir: testCache(t), Runsc: runsc, DefaultImage: img})
	if err != nil {
		t.Fatalf("this host cannot run the backend: %v", err)
	}
	return b
}

func TestBackendSuite(t *testing.T) {
	sbtest.RunBackendSuite(t, func(t *testing.T) sandbox.Backend { return testBackend(t) })
}

// fixture creates a sandbox on a store with /proj.
func fixture(t *testing.T, b *Backend, spec sandbox.Spec) (sandbox.Sandbox, workspace.Store) {
	t.Helper()
	ctx := context.Background()
	st := sbtest.OpenStore(t)
	if _, err := st.Mkdir(ctx, sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	v, err := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Env.Store, spec.Env.Actor, spec.View = st, sbtest.Admin, v
	if spec.Memory == 0 {
		spec.Memory = 1 << 30
	}
	if spec.Disk == 0 {
		spec.Disk = 1 << 30
	}
	sb, err := b.Create(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sb.Destroy(context.Background()) })
	return sb, st
}

func sh(t *testing.T, sb sandbox.Sandbox, script string) string {
	t.Helper()
	var out, errb bytes.Buffer
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", script}, Stdout: &out, Stderr: &errb})
	if err != nil {
		t.Fatal(err)
	}
	if code, err := p.Wait(); err != nil || code != 0 {
		t.Fatalf("%q: %d %v\n%s%s", script, code, err, out.String(), errb.String())
	}
	return out.String()
}

// TestFailedSuspendSaveKeepsTheSandboxRunning fails the save of the suspended
// state: Suspend returns an error, the restored sandbox answers Exec, and a
// later Suspend and Resume work.
func TestFailedSuspendSaveKeepsTheSandboxRunning(t *testing.T) {
	b := testBackend(t)
	b.aliveEvery = 10 * time.Millisecond // the old instance's watcher ticks many times while the restore runs
	sb, _ := fixture(t, b, sandbox.Spec{Env: sandbox.Env{Ports: map[int]sandbox.PortHandler{8080: sbtest.EchoHandler}}})
	s := sb.(*sbx)
	sh(t, sb, "echo before > /tmp/marker")
	oldPid := s.in.pid
	s.hooks.suspendSave = func() error {
		// The save runs after "runsc checkpoint": the old runsc process is
		// gone, or goes within moments. Wait until it is, so that a watcher of
		// the old instance that still ran would see a dead process and mark the
		// sandbox gone while the restore runs (the restore takes far longer than
		// one tick).
		deadline := time.Now().Add(30 * time.Second)
		for pidAlive(oldPid) && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		if pidAlive(oldPid) {
			return errors.New("the old runsc process did not end")
		}
		return errors.New("injected save failure")
	}
	if _, err := sb.Suspend(context.Background()); err == nil || errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("Suspend = %v, want the injected error and no ErrGone", err)
	}
	if got := sh(t, sb, "cat /tmp/marker"); got != "before\n" {
		t.Fatalf("marker after the failed suspend: %q", got)
	}
	s.hooks.suspendSave = nil
	ref, err := sb.Suspend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rb, err := b.Resume(context.Background(), ref, s.env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rb.Destroy(context.Background()) })
	if got := sh(t, rb, "cat /tmp/marker"); got != "before\n" {
		t.Fatalf("marker after resume: %q", got)
	}
}

// runscProcs lists the command lines of the processes that mention needle and
// look like runsc (the sandbox, the gofer, or the runsc command itself).
func runscProcs(t *testing.T, needle string) []string {
	t.Helper()
	ents, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cmd := strings.ReplaceAll(string(raw), "\x00", " ")
		if strings.Contains(cmd, needle) && strings.Contains(cmd, "runsc") {
			out = append(out, e.Name()+" "+cmd)
		}
	}
	return out
}

// waitNoProcs waits until no runsc process mentions needle.
func waitNoProcs(t *testing.T, needle string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		left := runscProcs(t, needle)
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("processes of %s are left:\n%s", needle, strings.Join(left, "\n"))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A sandbox outlives the process that started it. The next New on the same
// StateDir stops it and removes its folder.
func TestNewStopsASandboxThatTheBackendLeftRunning(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, sandbox.Spec{})
	id := sb.ID()
	if len(runscProcs(t, id)) == 0 {
		t.Fatal("the helper finds no process of the running sandbox")
	}
	// Drop the backend without Destroy, as a killed process would.
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b2, err := New(b.opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b2.Close() })
	waitNoProcs(t, id)
	if _, err := os.Stat(filepath.Join(b.opts.StateDir, id)); !os.IsNotExist(err) {
		t.Fatalf("the folder of the old sandbox is still there: %v", err)
	}
}

// A Create that the caller cancels while runsc starts the sandbox must not
// leave a running sandbox behind.
func TestACancelledCreateLeavesNoRunscProcess(t *testing.T) {
	b := testBackend(t)
	st := sbtest.OpenStore(t)
	if _, err := st.Mkdir(bg(), sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	v, err := st.View(bg(), sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []time.Duration{100 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1200 * time.Millisecond, 2 * time.Second} {
		ctx, cancel := context.WithTimeout(bg(), d)
		sb, err := b.Create(ctx, sandbox.Spec{View: v, Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, Memory: 1 << 30, Disk: 1 << 30})
		cancel()
		if err == nil {
			sb.Destroy(bg())
		}
		waitNoProcs(t, b.opts.StateDir)
		if ents := sessionFolders(b.opts.StateDir); len(ents) != 0 {
			t.Fatalf("timeout %s: folders are left in StateDir: %v", d, ents)
		}
	}
}

// A checkpoint that would stage more file bytes than the stage limit fails
// with nothing committed and a message that names the limit. The sandbox runs
// again, and a later checkpoint with room commits the files.
func TestCheckpointOverTheStageLimitCommitsNothing(t *testing.T) {
	b := testBackend(t)
	b.opts.StageLimit = 1 << 20
	sb, _ := fixture(t, b, sandbox.Spec{})
	sh(t, sb, "head -c 2000000 /dev/zero > proj/big.bin")
	cs, _, err := sb.Checkpoint(bg(), "too big")
	if err == nil || !strings.Contains(err.Error(), "stage limit of 1048576 bytes") || cs.Rev != 0 {
		t.Fatalf("Checkpoint = rev %d, %v", cs.Rev, err)
	}
	if got := sh(t, sb, "echo alive"); got != "alive\n" {
		t.Fatalf("the sandbox does not run after the failed checkpoint: %q", got)
	}
	b.opts.StageLimit = 0
	cs, _, err = sb.Checkpoint(bg(), "now it fits")
	if err != nil || cs.Rev == 0 {
		t.Fatalf("Checkpoint = rev %d, %v", cs.Rev, err)
	}
}
