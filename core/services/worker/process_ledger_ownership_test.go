package worker

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	process "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Ownership of a backend process group has to stay recorded until every member
// of the group is gone, has to survive a crash, and has to say so loudly when it
// cannot be established. These specs use real process groups.
var _ = Describe("Durable backend process ownership", func() {
	sleeper := func() (*process.Process, int) {
		p := process.New(process.WithTemporaryStateDir(), process.WithName("/bin/sleep"), process.WithArgs("300"))
		Expect(p.Run()).To(Succeed())
		DeferCleanup(func() { _ = p.Stop() })
		pid, err := strconv.Atoi(p.CurrentPID())
		Expect(err).ToNot(HaveOccurred())
		return p, pid
	}

	Describe("recording", func() {
		It("reports a failure to persist the record", func() {
			dir := GinkgoT().TempDir()
			blocker := filepath.Join(dir, "blocker")
			Expect(os.WriteFile(blocker, nil, 0o600)).To(Succeed())
			// The parent of the ledger path is a file: nothing can be written.
			ledger := newProcessLedger(filepath.Join(blocker, "processes.json"))
			_, pid := sleeper()
			Expect(ledger.add("model#0", pid)).ToNot(Succeed())
		})

		It("refuses a process it cannot identify", func() {
			ledger := newProcessLedger(filepath.Join(GinkgoT().TempDir(), "processes.json"))
			Expect(ledger.add("model#0", 0)).ToNot(Succeed())
		})

		It("stores the launch generation next to the process identity", func() {
			path := filepath.Join(GinkgoT().TempDir(), "processes.json")
			_, pid := sleeper()
			ledger := newProcessLedger(path)
			ledger.generation = "launch-7"
			Expect(ledger.add("model#0", pid)).To(Succeed())
			data, err := os.ReadFile(path)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(`"generation":"launch-7"`))
			Expect(string(data)).To(ContainSubstring(`"start_time":"`))
			info, err := os.Stat(path)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		})
	})

	Describe("start time identity", func() {
		It("falls back to ps where /proc does not exist, as on macOS", func() {
			real := procRoot
			procRoot = filepath.Join(GinkgoT().TempDir(), "no-proc")
			DeferCleanup(func() { procRoot = real })
			first := readStartTime(os.Getpid())
			Expect(first).ToNot(BeEmpty())
			Expect(readStartTime(os.Getpid())).To(Equal(first), "stable for one process")
			_, pid := sleeper()
			other := readStartTime(pid)
			Expect(other).ToNot(BeEmpty())
			Expect(readStartTime(1<<30)).To(BeEmpty(), "a pid that does not exist has no start time")
		})
	})

	Describe("release", func() {
		It("keeps ownership and kills the group when the leader exited before a descendant", func() {
			if runtime.GOOS == "windows" {
				Skip("no process groups")
			}
			proc, grandchild := startGroupProcess()
			DeferCleanup(func() {
				if grandAlive(grandchild) {
					_ = syscallKill(grandchild)
				}
			})
			path := filepath.Join(GinkgoT().TempDir(), "processes.json")
			pid, _ := strconv.Atoi(proc.CurrentPID())
			ledger := newProcessLedger(path)
			Expect(ledger.add("model#0", pid)).To(Succeed())
			Expect(syscallKill(pid)).To(Succeed())
			Eventually(func() bool { return leaderGone(pid) }, 20*time.Second, 50*time.Millisecond).Should(BeTrue())
			Expect(grandAlive(grandchild)).To(BeTrue())

			Expect(ledger.release("model#0")).To(Succeed())

			Eventually(func() bool { return grandAlive(grandchild) }, 10*time.Second, 50*time.Millisecond).Should(BeFalse(),
				"forgetting the record while a descendant lives would orphan it")
			data, _ := os.ReadFile(path)
			Expect(string(data)).ToNot(ContainSubstring("model#0"))
		})

		It("retains the record when the group cannot be reaped", func() {
			proc, grandchild := startGroupProcess()
			DeferCleanup(func() {
				if grandAlive(grandchild) {
					_ = syscallKill(grandchild)
				}
				_ = proc.Stop()
			})
			path := filepath.Join(GinkgoT().TempDir(), "processes.json")
			pid, _ := strconv.Atoi(proc.CurrentPID())
			ledger := newProcessLedger(path)
			Expect(ledger.add("model#0", pid)).To(Succeed())

			realKill, realWait := killGroup, reapTimeout
			killGroup = func(int) error { return nil } // the signal never lands
			reapTimeout = 300 * time.Millisecond
			DeferCleanup(func() { killGroup, reapTimeout = realKill, realWait })

			Expect(ledger.release("model#0")).ToNot(Succeed())
			data, _ := os.ReadFile(path)
			Expect(string(data)).To(ContainSubstring("model#0"), "a live group stays owned")
		})

		It("drops a record whose pid now belongs to an unrelated process", func() {
			_, pid := sleeper()
			path := filepath.Join(GinkgoT().TempDir(), "processes.json")
			ledger := newProcessLedger(path)
			Expect(ledger.add("model#0", pid)).To(Succeed())
			ledger.corruptStartTimeForTest("model#0")

			Expect(ledger.release("model#0")).To(Succeed())
			Expect(pidAlive(strconv.Itoa(pid))).To(BeTrue(), "an unrelated process is never signalled")
			data, _ := os.ReadFile(path)
			Expect(string(data)).ToNot(ContainSubstring("model#0"))
		})
	})

	Describe("recovery", func() {
		It("blocks, and kills nothing, when ownership of a live group is uncertain", func() {
			_, pid := sleeper()
			path := filepath.Join(GinkgoT().TempDir(), "processes.json")
			ledger := newProcessLedger(path)
			Expect(ledger.add("model#0", pid)).To(Succeed())
			ledger.forgetStartTimeForTest("model#0")

			killed, err := newProcessLedger(path).sweepStale()
			Expect(err).To(HaveOccurred())
			Expect(killed).To(BeZero())
			Expect(pidAlive(strconv.Itoa(pid))).To(BeTrue())
			data, _ := os.ReadFile(path)
			Expect(string(data)).To(ContainSubstring("model#0"), "an unresolved record is kept for the next start")
		})

		It("blocks when the ledger cannot be parsed and leaves it in place", func() {
			path := filepath.Join(GinkgoT().TempDir(), "processes.json")
			Expect(os.WriteFile(path, []byte("{not json"), 0o600)).To(Succeed())
			_, err := newProcessLedger(path).sweepStale()
			Expect(err).To(HaveOccurred())
			data, _ := os.ReadFile(path)
			Expect(string(data)).To(Equal("{not json"))
		})

		It("blocks, and keeps the record, when a verified group survives the kill", func() {
			proc, grandchild := startGroupProcess()
			DeferCleanup(func() {
				if grandAlive(grandchild) {
					_ = syscallKill(grandchild)
				}
				_ = proc.Stop()
			})
			path := filepath.Join(GinkgoT().TempDir(), "processes.json")
			pid, _ := strconv.Atoi(proc.CurrentPID())
			Expect(newProcessLedger(path).add("model#0", pid)).To(Succeed())

			realKill, realWait := killGroup, reapTimeout
			killGroup = func(int) error { return nil }
			reapTimeout = 300 * time.Millisecond
			DeferCleanup(func() { killGroup, reapTimeout = realKill, realWait })

			killed, err := newProcessLedger(path).sweepStale()
			Expect(err).To(HaveOccurred())
			Expect(killed).To(BeZero())
			data, _ := os.ReadFile(path)
			Expect(string(data)).To(ContainSubstring("model#0"))
		})
	})

	Describe("supervisor start gate", func() {
		It("refuses to run a backend whose ownership cannot be recorded", func() {
			dir := GinkgoT().TempDir()
			blocker := filepath.Join(dir, "blocker")
			Expect(os.WriteFile(blocker, nil, 0o600)).To(Succeed())
			s := &backendSupervisor{cfg: &Config{}, ledger: newProcessLedger(filepath.Join(blocker, "p.json"))}
			_, pid := sleeper()
			Expect(s.recordOwnership("model#0", strconv.Itoa(pid))).ToNot(Succeed())
			Expect(s.recordOwnership("model#0", "not-a-pid")).ToNot(Succeed())
		})

		It("is a no-op without a ledger, as in unit tests", func() {
			s := &backendSupervisor{cfg: &Config{}}
			Expect(s.recordOwnership("model#0", "")).To(Succeed())
		})
	})
})
