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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

func testCache() string {
	dir := os.Getenv("SANDBOX_TEST_CACHE")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "sandbox-gvisor-test-cache")
	}
	cacheOnce.Do(func() { _ = os.MkdirAll(dir, 0o700) })
	return dir
}

func testBackend() *Backend {
	runsc, img := os.Getenv("SANDBOX_TEST_RUNSC"), os.Getenv("SANDBOX_TEST_IMAGE")
	if runsc == "" || img == "" {
		Skip("set SANDBOX_TEST_RUNSC (runsc with gvisor-bin next to it) and SANDBOX_TEST_IMAGE (name@sha256:... or oci-layout:<dir>@sha256:...)")
	}
	b, err := New(Options{StateDir: shortDir(), CacheDir: testCache(), Runsc: runsc, DefaultImage: img})
	Expect(err).ToNot(HaveOccurred(), "this host cannot run the backend")
	return b
}

var _ = Describe("backend conformance suite", func() {
	sbtest.RunBackendSuite(func() sandbox.Backend { return testBackend() })
})

// fixture creates a sandbox on a store with /proj.
func fixture(b *Backend, spec sandbox.Spec) (sandbox.Sandbox, workspace.Store) {
	ctx := context.Background()
	st := sbtest.OpenStore()
	_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
	Expect(err).ToNot(HaveOccurred())
	v, err := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	Expect(err).ToNot(HaveOccurred())
	spec.Env.Store, spec.Env.Actor, spec.View = st, sbtest.Admin, v
	if spec.Memory == 0 {
		spec.Memory = 1 << 30
	}
	if spec.Disk == 0 {
		spec.Disk = 1 << 30
	}
	sb, err := b.Create(ctx, spec)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = sb.Destroy(context.Background()) })
	return sb, st
}

func sh(sb sandbox.Sandbox, script string) string {
	var out, errb bytes.Buffer
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", script}, Stdout: &out, Stderr: &errb})
	Expect(err).ToNot(HaveOccurred())
	code, err := p.Wait()
	Expect(err).ToNot(HaveOccurred(), "%q: %s%s", script, out.String(), errb.String())
	Expect(code).To(BeZero(), "%q: %s%s", script, out.String(), errb.String())
	return out.String()
}

// runscProcs lists the command lines of the processes that mention needle and
// look like runsc (the sandbox, the gofer, or the runsc command itself).
func runscProcs(needle string) []string {
	ents, err := os.ReadDir("/proc")
	Expect(err).ToNot(HaveOccurred())
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
func waitNoProcs(needle string) {
	Eventually(func() []string { return runscProcs(needle) }, 15*time.Second, 100*time.Millisecond).Should(BeEmpty(), "processes of %s are left", needle)
}

var _ = Describe("gVisor backend integration", func() {
	// Fails the save of the suspended state: Suspend returns an error, the
	// restored sandbox answers Exec, and a later Suspend and Resume work.
	It("keeps the sandbox running after a failed suspend save", func() {
		b := testBackend()
		b.aliveEvery = 10 * time.Millisecond // the old instance's watcher ticks many times while the restore runs
		sb, _ := fixture(b, sandbox.Spec{Env: sandbox.Env{Ports: map[int]sandbox.PortHandler{8080: sbtest.EchoHandler}}})
		s := sb.(*sbx)
		sh(sb, "echo before > /tmp/marker")
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
		_, err := sb.Suspend(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sandbox.ErrGone)).To(BeFalse(), "Suspend = %v, want the injected error and no ErrGone", err)
		Expect(sh(sb, "cat /tmp/marker")).To(Equal("before\n"), "marker after the failed suspend")
		s.hooks.suspendSave = nil
		ref, err := sb.Suspend(context.Background())
		Expect(err).ToNot(HaveOccurred())
		rb, err := b.Resume(context.Background(), ref, s.env)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = rb.Destroy(context.Background()) })
		Expect(sh(rb, "cat /tmp/marker")).To(Equal("before\n"), "marker after resume")
	})

	// A sandbox outlives the process that started it. The next New on the same
	// StateDir stops it and removes its folder.
	It("stops a sandbox that the backend left running on the next New", func() {
		b := testBackend()
		sb, _ := fixture(b, sandbox.Spec{})
		id := sb.ID()
		Expect(runscProcs(id)).ToNot(BeEmpty(), "the helper finds no process of the running sandbox")
		// Drop the backend without Destroy, as a killed process would.
		Expect(b.Close()).To(Succeed())
		b2, err := New(b.opts)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = b2.Close() })
		waitNoProcs(id)
		_, err = os.Stat(filepath.Join(b.opts.StateDir, id))
		Expect(os.IsNotExist(err)).To(BeTrue(), "the folder of the old sandbox is still there: %v", err)
	})

	// A Create that the caller cancels while runsc starts the sandbox must not
	// leave a running sandbox behind.
	It("leaves no runsc process after a cancelled Create", func() {
		b := testBackend()
		st := sbtest.OpenStore()
		_, err := st.Mkdir(bg(), sbtest.Admin, "/proj")
		Expect(err).ToNot(HaveOccurred())
		v, err := st.View(bg(), sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
		Expect(err).ToNot(HaveOccurred())
		for _, d := range []time.Duration{100 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1200 * time.Millisecond, 2 * time.Second} {
			ctx, cancel := context.WithTimeout(bg(), d)
			sb, err := b.Create(ctx, sandbox.Spec{View: v, Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, Memory: 1 << 30, Disk: 1 << 30})
			cancel()
			if err == nil {
				_ = sb.Destroy(bg())
			}
			waitNoProcs(b.opts.StateDir)
			Expect(sessionFolders(b.opts.StateDir)).To(BeEmpty(), "timeout %s: folders are left in StateDir", d)
		}
	})

	// A checkpoint that would stage more file bytes than the stage limit fails
	// with nothing committed and a message that names the limit. The sandbox runs
	// again, and a later checkpoint with room commits the files.
	It("commits nothing for a checkpoint over the stage limit", func() {
		b := testBackend()
		b.opts.StageLimit = 1 << 20
		sb, _ := fixture(b, sandbox.Spec{})
		sh(sb, "head -c 2000000 /dev/zero > proj/big.bin")
		cs, _, err := sb.Checkpoint(bg(), "too big")
		Expect(err).To(HaveOccurred(), "Checkpoint = rev %d", cs.Rev)
		Expect(err.Error()).To(ContainSubstring("stage limit of 1048576 bytes"))
		Expect(cs.Rev).To(BeZero())
		Expect(sh(sb, "echo alive")).To(Equal("alive\n"), "the sandbox does not run after the failed checkpoint")
		b.opts.StageLimit = 0
		cs, _, err = sb.Checkpoint(bg(), "now it fits")
		Expect(err).ToNot(HaveOccurred())
		Expect(cs.Rev).ToNot(BeZero())
	})
})
