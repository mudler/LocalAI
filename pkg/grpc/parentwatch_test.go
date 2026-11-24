//go:build !windows

package grpc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These env vars drive the helper roles this test binary re-executes itself as
// (see the init() dispatcher). They are only set for the spawned child/
// grandchild processes, never for the normal `go test` invocation.
const (
	envRole   = "LOCALAI_PARENTWATCH_TEST_ROLE"
	envReady  = "LOCALAI_PARENTWATCH_TEST_READY"  // grandchild writes its PID here once the watcher is armed
	envExited = "LOCALAI_PARENTWATCH_TEST_EXITED" // grandchild writes here when it detects reparenting
)

// init dispatches the helper roles when this test binary is re-executed with a
// role set. It runs before the testing/Ginkgo machinery, and is a no-op during
// a normal test run (role unset).
func init() {
	switch os.Getenv(envRole) {
	case "middle":
		runMiddleRole()
	case "grandchild":
		runGrandchildRole()
	}
}

// childEnv returns the current environment with the parentwatch test role set
// to the given value (replacing any inherited role), leaving the ready/exited
// file paths inherited.
func childEnv(role string) []string {
	out := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if len(kv) > len(envRole) && kv[:len(envRole)+1] == envRole+"=" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, envRole+"="+role)
}

// runGrandchildRole arms the REAL watchParentDeath against its current parent
// (the "middle" process), signals readiness, then blocks. When middle exits and
// we are reparented, the watcher fires and we record it before exiting.
func runGrandchildRole() {
	exitedFile := os.Getenv(envExited)
	readyFile := os.Getenv(envReady)

	origPPID := os.Getppid()
	go watchParentDeath(origPPID, 50*time.Millisecond, func() {
		_ = os.WriteFile(exitedFile, []byte("1"), 0o644)
		os.Exit(7)
	})

	// Safety valve: never linger if something goes wrong with the test.
	go func() {
		time.Sleep(30 * time.Second)
		os.Exit(2)
	}()

	// Signal readiness only after the watcher captured origPPID, so middle
	// won't exit before we've recorded it as our original parent.
	_ = os.WriteFile(readyFile, []byte(strconv.Itoa(os.Getpid())), 0o644)

	select {} // block until the watcher terminates us
}

// runMiddleRole spawns the grandchild (which arms the watcher against us),
// waits until it is ready, then exits — orphaning the grandchild so it gets
// reparented, which is what the watcher must detect.
func runMiddleRole() {
	readyFile := os.Getenv(envReady)

	self, err := os.Executable()
	if err != nil {
		os.Exit(3)
	}
	cmd := exec.Command(self)
	cmd.Env = childEnv("grandchild")
	// Own process group, mirroring how real backends are spawned, and discard
	// std streams so the grandchild doesn't keep any parent pipe open.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		os.Exit(4)
	}

	if !waitForFile(readyFile, 10*time.Second) {
		os.Exit(5)
	}
	os.Exit(0) // orphan the grandchild
}

func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// This spec builds a genuine two-level process tree (test -> middle ->
// grandchild), lets the middle process die, and asserts the grandchild's
// watchParentDeath detects the reparenting and self-terminates.
var _ = Describe("watchParentDeath", func() {
	It("detects reparenting and self-terminates the orphaned process", func() {
		if runtime.GOOS == "windows" {
			Skip("parent-death watcher is not supported on windows")
		}

		dir := GinkgoT().TempDir()
		readyFile := filepath.Join(dir, "ready")
		exitedFile := filepath.Join(dir, "exited")

		self, err := os.Executable()
		Expect(err).NotTo(HaveOccurred(), "cannot resolve test executable")

		middle := exec.Command(self)
		middle.Env = append(childEnv("middle"),
			envReady+"="+readyFile,
			envExited+"="+exitedFile,
		)
		// Discard the helpers' output; keep the test log clean.
		middle.Stdout = nil
		middle.Stderr = nil

		Expect(middle.Start()).To(Succeed(), "failed to start middle helper")
		// Wait only for the middle process; the grandchild is intentionally left
		// orphaned. No pipes are shared, so this returns as soon as middle exits.
		Expect(middle.Wait()).To(Succeed(), "middle helper exited with error")

		// The grandchild must have armed the watcher (and thus captured middle as
		// its parent) before middle exited.
		_, err = os.Stat(readyFile)
		Expect(err).NotTo(HaveOccurred(), "grandchild never signaled readiness")

		// Best-effort cleanup in case the watcher somehow doesn't fire.
		DeferCleanup(func() {
			if b, err := os.ReadFile(readyFile); err == nil {
				if pid, err := strconv.Atoi(string(b)); err == nil {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
		})

		// Now that middle is gone, the grandchild has been reparented; the watcher
		// must notice and write the exited marker.
		Expect(waitForFile(exitedFile, 10*time.Second)).To(BeTrue(), "watcher did not detect parent death within timeout")
	})
})
