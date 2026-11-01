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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
)

// nnpChildEnv marks the re-executed test process that holds the children.
const nnpChildEnv = "SANDBOX_AGENT_NNP_CHILD"

var _ = Describe("agent process", func() {
	// A failed boot must end in a power-off request after a pause, then hold
	// PID 1 with a long sleep. The test pause ends the goroutine on the hold.
	It("halts by powering off after a pause", func() {
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
		Expect(strings.Join(steps, "|")).To(Equal("log: boot: %s|pause 2s|sync|poweroff|pause 1h0m0s"))
	})

	// This spec runs the check in a fresh process, so the flag that
	// setNoNewPrivs sets does not change this test process.
	//
	// The child is built with CGO_ENABLED=0, as the agent is: AllThreadsSyscall
	// returns ENOTSUP in a binary that links libc through cgo.
	It("passes no_new_privs to children", func() {
		bin := filepath.Join(GinkgoT().TempDir(), "agent.test")
		build := exec.Command("go", "test", "-c", "-o", bin, ".")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		out, err := build.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "build the child: %s", out)
		cmd := exec.Command(bin, "-ginkgo.focus=no_new_privs child$", "-ginkgo.v", "-ginkgo.no-color")
		cmd.Env = append(os.Environ(), nnpChildEnv+"=1")
		out, err = cmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "child: %s", out)
		Expect(string(out)).To(ContainSubstring("1 Passed"), "child did not run its checks")
	})

	// The child spawns 60 real children, concurrently, through the spawner,
	// and checks that each one reports NoNewPrivs: 1. The flag is per thread,
	// so setNoNewPrivs must reach every thread, not only the caller's.
	It("passes no_new_privs child", func() {
		if os.Getenv(nnpChildEnv) == "" {
			Skip("runs only as the child of the no_new_privs spec")
		}
		Expect(setNoNewPrivs()).To(Succeed())
		sp := &guestagent.LinuxSpawner{Env: []string{"PATH=" + os.Getenv("PATH")}, Dir: GinkgoT().TempDir(), Reap: true}
		bad := make(chan string, 60)
		var wg sync.WaitGroup
		for range 60 {
			wg.Go(func() {
				p, err := sp.Spawn(guestagent.ExecRequest{Args: []string{"sh", "-c", "grep NoNewPrivs /proc/$$/status"}})
				if err != nil {
					bad <- err.Error()
					return
				}
				defer func() { _ = p.Close() }()
				out, _ := io.ReadAll(p.Stdout())
				_, _, _ = p.Wait()
				if !strings.Contains(string(out), "NoNewPrivs:\t1") {
					bad <- strings.TrimSpace(string(out))
				}
			})
		}
		wg.Wait()
		close(bad)
		for b := range bad {
			Expect(b).To(BeEmpty(), "child without no_new_privs: %q", b)
		}
	})

	// The termination signals must not stop PID 1. The test process would die
	// on them without the Notify. Ginkgo handles these signals itself, so the
	// check runs in a child process that TestMain turns into the check.
	It("drains the termination signals", func() {
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), signalChildEnv+"=1")
		out, err := cmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "child: %s", out)
	})
})

// signalChildEnv marks the re-executed test process that receives the
// termination signals.
const signalChildEnv = "SANDBOX_AGENT_SIGNAL_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(signalChildEnv) != "" {
		ignoreSignals()
		for _, s := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT} {
			if err := syscall.Kill(os.Getpid(), s); err != nil {
				os.Exit(1)
			}
		}
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
