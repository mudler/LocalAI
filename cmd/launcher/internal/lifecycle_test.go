// SPDX-License-Identifier: MIT
package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/pkg/workerledger"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs run real processes and real process groups: the lifecycle
// controller exists to prove that nothing of an earlier role is still alive.
var _ = Describe("Serialized launcher lifecycle", func() {
	var (
		dir    string
		ledger string
		lc     *lifecycle
	)

	scripts := 0
	script := func(body string) launchSpec {
		// A new file per call: rewriting a script a shell is reading corrupts it.
		scripts++
		path := filepath.Join(dir, fmt.Sprintf("runtime-%d.sh", scripts))
		Expect(os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700)).To(Succeed())
		return launchSpec{Binary: path, Dir: dir, LedgerPath: ledger}
	}
	// The marker proves the trap is installed: a signal sent earlier would take
	// the shell's default action and the spec would test nothing.
	const polite = `trap 'exit 0' TERM; : > "$(dirname "$0")/ready"; while :; do sleep 0.05; done`
	const stubborn = `trap '' TERM; : > "$(dirname "$0")/ready"; while :; do sleep 0.05; done`
	start := func(spec launchSpec) uint64 {
		Expect(os.Remove(filepath.Join(dir, "ready"))).To(Or(Succeed(), MatchError(os.ErrNotExist)))
		gen, err := lc.Start(spec)
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() error { _, err := os.Stat(filepath.Join(dir, "ready")); return err }, 5*time.Second, 10*time.Millisecond).Should(Succeed())
		return gen
	}

	// backend starts a process in its own group, as a worker does, and records it.
	backend := func(key string) int {
		cmd := exec.Command("/bin/sleep", "300")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		Expect(cmd.Start()).To(Succeed())
		go func() { _ = cmd.Wait() }()
		DeferCleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
		entries, err := workerledger.ReadFile(ledger)
		Expect(err).ToNot(HaveOccurred())
		entries = append(entries, workerledger.Entry{Key: key, PID: cmd.Process.Pid, StartTime: workerledger.ReadStartTime(cmd.Process.Pid)})
		Expect(workerledger.WriteFile(ledger, entries)).To(Succeed())
		return cmd.Process.Pid
	}
	alive := func(pid int) func() bool { return func() bool { return workerledger.GroupAlive(pid) } }

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		ledger = filepath.Join(dir, "worker-processes.json")
		lc = newLifecycle()
		lc.termGrace = 400 * time.Millisecond
		lc.killGrace = 2 * time.Second
		realReap := workerledger.ReapTimeout
		workerledger.ReapTimeout = time.Second
		DeferCleanup(func() { workerledger.ReapTimeout = realReap })
		DeferCleanup(func() { _ = lc.Stop() })
	})

	Describe("starting", func() {
		It("runs one child and refuses a second start", func() {
			gen, err := lc.Start(script(polite))
			Expect(err).ToNot(HaveOccurred())
			Expect(gen).ToNot(BeZero())
			Expect(lc.State()).To(Equal(lifecycleRunning))
			_, err = lc.Start(script(polite))
			Expect(errors.Is(err, errLifecycleBusy)).To(BeTrue())
		})

		It("lets only one of several concurrent starts through", func() {
			spec := script(polite)
			var wg sync.WaitGroup
			var mu sync.Mutex
			succeeded := 0
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := lc.Start(spec); err == nil {
						mu.Lock()
						succeeded++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			Expect(succeeded).To(Equal(1))
		})

		It("kills a recorded backend group of an earlier run before it starts", func() {
			pid := backend("model#0")
			_, err := lc.Start(script(polite))
			Expect(err).ToNot(HaveOccurred())
			Expect(alive(pid)()).To(BeFalse())
		})

		It("leaves a recycled process id alone", func() {
			pid := backend("model#0")
			entries, err := workerledger.ReadFile(ledger)
			Expect(err).ToNot(HaveOccurred())
			entries[0].StartTime = "recycled"
			Expect(workerledger.WriteFile(ledger, entries)).To(Succeed())
			_, err = lc.Start(script(polite))
			Expect(err).ToNot(HaveOccurred())
			Expect(alive(pid)()).To(BeTrue(), "an unrelated group is never signalled")
		})

		It("refuses to start over a record it cannot resolve and signals nothing", func() {
			pid := backend("model#0")
			real := workerledger.ReadStartTime
			workerledger.ReadStartTime = func(int) string { return "" }
			DeferCleanup(func() { workerledger.ReadStartTime = real })
			// A system that cannot read start times cannot prove ownership.
			_, err := lc.Start(script(polite))
			Expect(err).To(HaveOccurred())
			Expect(lc.State()).To(Equal(lifecycleStopped))
			Expect(alive(pid)()).To(BeTrue())
		})

		It("refuses to start over a corrupt ledger", func() {
			Expect(os.WriteFile(ledger, []byte("{not json"), 0o600)).To(Succeed())
			_, err := lc.Start(script(polite))
			Expect(err).To(HaveOccurred())
			Expect(lc.State()).To(Equal(lifecycleStopped))
		})

		It("reports a binary that cannot be executed and stays stopped", func() {
			_, err := lc.Start(launchSpec{Binary: filepath.Join(dir, "missing"), Dir: dir, LedgerPath: ledger})
			Expect(err).To(HaveOccurred())
			Expect(lc.State()).To(Equal(lifecycleStopped))
			_, err = lc.Start(script(polite))
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("stopping", func() {
		It("asks for a graceful exit first", func() {
			start(script(polite))
			began := time.Now()
			Expect(lc.Stop()).To(Succeed())
			Expect(time.Since(began)).To(BeNumerically("<", lc.termGrace), "a child that honours SIGTERM is not killed")
			Expect(lc.State()).To(Equal(lifecycleStopped))
		})

		It("force-kills a child that ignores SIGTERM after the grace period", func() {
			start(script(stubborn))
			pid := lc.PID()
			began := time.Now()
			Expect(lc.Stop()).To(Succeed())
			Expect(time.Since(began)).To(BeNumerically(">=", lc.termGrace))
			Expect(alive(pid)()).To(BeFalse(), "the whole runtime group is gone")
			Expect(lc.State()).To(Equal(lifecycleStopped))
		})

		It("kills backend groups that outlive the runtime", func() {
			start(script(polite))
			first, second := backend("a#0"), backend("b#0")
			Expect(lc.Stop()).To(Succeed())
			Expect(alive(first)()).To(BeFalse())
			Expect(alive(second)()).To(BeFalse())
			entries, err := workerledger.ReadFile(ledger)
			Expect(err).ToNot(HaveOccurred())
			Expect(entries).To(BeEmpty(), "released ownership is dropped from the record")
		})

		It("kills a backend whose leader already exited while a member lives", func() {
			start(script(polite))
			// The leader shell exits at once; its sleeping child keeps the group.
			cmd := exec.Command("/bin/sh", "-c", "sleep 300 & exit 0")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			Expect(cmd.Start()).To(Succeed())
			pid := cmd.Process.Pid
			start := workerledger.ReadStartTime(pid)
			DeferCleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
			Expect(workerledger.WriteFile(ledger, []workerledger.Entry{{Key: "m#0", PID: pid, StartTime: start}})).To(Succeed())
			_ = cmd.Wait()
			Expect(alive(pid)()).To(BeTrue())
			Expect(lc.Stop()).To(Succeed())
			Expect(alive(pid)()).To(BeFalse())
		})

		It("rejects start and a second stop while stopping", func() {
			start(script(stubborn))
			done := make(chan error, 1)
			go func() { done <- lc.Stop() }()
			Eventually(lc.State).Should(Equal(lifecycleStopping))
			_, err := lc.Start(script(polite))
			Expect(errors.Is(err, errLifecycleBusy)).To(BeTrue())
			Expect(errors.Is(lc.Stop(), errLifecycleBusy)).To(BeTrue())
			Eventually(done, 5*time.Second).Should(Receive(BeNil()))
		})

		It("reports that nothing runs when nothing runs", func() {
			Expect(errors.Is(lc.Stop(), errNotRunning)).To(BeTrue())
		})

		It("is incomplete, blocks every start and keeps the record when a group cannot be emptied", func() {
			start(script(polite))
			pid := backend("m#0")
			realKill := workerledger.KillGroup
			workerledger.KillGroup = func(int) error { return nil } // the signal never lands
			workerledger.ReapTimeout = 200 * time.Millisecond
			err := lc.Stop()
			Expect(errors.Is(err, errShutdownIncomplete)).To(BeTrue())
			Expect(lc.State()).To(Equal(lifecycleIncomplete))
			_, err = lc.Start(script(polite))
			Expect(errors.Is(err, errShutdownIncomplete)).To(BeTrue(), "no replacement while ownership is unresolved")
			entries, rerr := workerledger.ReadFile(ledger)
			Expect(rerr).ToNot(HaveOccurred())
			Expect(entries).To(HaveLen(1))

			// A retry with working signals finishes the job.
			workerledger.KillGroup = realKill
			Expect(lc.Stop()).To(Succeed())
			Expect(lc.State()).To(Equal(lifecycleStopped))
			Expect(alive(pid)()).To(BeFalse())
		})
	})

	Describe("unexpected exit and stale callbacks", func() {
		It("cleans up owned groups and records the exit when the runtime dies by itself", func() {
			exits := make(chan uint64, 4)
			spec := script(`sleep 0.2; exit 3`)
			spec.OnExit = func(gen uint64, err error) { exits <- gen }
			gen, err := lc.Start(spec)
			Expect(err).ToNot(HaveOccurred())
			pid := backend("m#0")
			Eventually(exits, 5*time.Second).Should(Receive(Equal(gen)))
			Eventually(lc.State, 5*time.Second).Should(Equal(lifecycleStopped))
			Expect(alive(pid)()).To(BeFalse(), "a parent exit is cleanup work, not proof that descendants stopped")
			g, exitErr := lc.LastExit()
			Expect(g).To(Equal(gen))
			Expect(exitErr).To(HaveOccurred())
		})

		It("ignores the exit of an older generation", func() {
			var mu sync.Mutex
			var seen []uint64
			record := func(gen uint64, _ error) { mu.Lock(); seen = append(seen, gen); mu.Unlock() }
			first := script(polite)
			first.OnExit = record
			g1, err := lc.Start(first)
			Expect(err).ToNot(HaveOccurred())
			Expect(lc.Stop()).To(Succeed())
			second := script(polite)
			second.OnExit = record
			g2, err := lc.Start(second)
			Expect(err).ToNot(HaveOccurred())
			Expect(g2).To(BeNumerically(">", g1))
			Expect(lc.Running(g1)).To(BeFalse(), "a callback tagged with the old generation is stale")
			Expect(lc.Running(g2)).To(BeTrue())
			mu.Lock()
			defer mu.Unlock()
			Expect(seen).To(ConsistOf(g1), "an expected stop is reported once, and never as the new generation")
		})
	})

	Describe("per-user lock", func() {
		It("lets one launcher own the role at a time", func() {
			first, err := acquireUserLock(dir)
			Expect(err).ToNot(HaveOccurred())
			_, err = acquireUserLock(dir)
			Expect(errors.Is(err, errLauncherLocked)).To(BeTrue())
			Expect(first.Release()).To(Succeed())
			again, err := acquireUserLock(dir)
			Expect(err).ToNot(HaveOccurred())
			Expect(again.Release()).To(Succeed())
		})

		It("is owner-only and refuses a symbolic link", func() {
			lock, err := acquireUserLock(dir)
			Expect(err).ToNot(HaveOccurred())
			info, err := os.Stat(filepath.Join(dir, "launcher.lock"))
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
			Expect(lock.Release()).To(Succeed())
			Expect(os.Remove(filepath.Join(dir, "launcher.lock"))).To(Succeed())
			Expect(os.Symlink(filepath.Join(dir, "elsewhere"), filepath.Join(dir, "launcher.lock"))).To(Succeed())
			_, err = acquireUserLock(dir)
			Expect(err).To(HaveOccurred())
		})

		It("is not inherited by the runtime child", func() {
			// If the child kept the descriptor, a crashed launcher would leave a
			// lock that a surviving runtime holds, or release it too early.
			lock, err := acquireUserLock(dir)
			Expect(err).ToNot(HaveOccurred())
			child := exec.Command("/bin/sleep", "300")
			Expect(child.Start()).To(Succeed())
			DeferCleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
			Expect(lock.Release()).To(Succeed())
			again, err := acquireUserLock(dir)
			Expect(err).ToNot(HaveOccurred(), "the live child must not hold the lock")
			Expect(again.Release()).To(Succeed())
		})
	})
})
