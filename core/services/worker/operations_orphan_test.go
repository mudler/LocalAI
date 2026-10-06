package worker

import (
	"net"
	"path/filepath"
	"strconv"
	"time"

	process "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A worker that is killed (SIGKILL, OOM) cannot stop its backends. They keep
// running in their own process groups. These specs use real process groups and
// a fresh ledger, as a restarted worker has.
var _ = Describe("Backends a crashed worker left behind", func() {
	killGroupAtEnd := func(grandchild int) {
		DeferCleanup(func() {
			if grandAlive(grandchild) {
				_ = syscallKill(grandchild)
			}
		})
	}

	It("are killed, group and grandchild, when the next worker starts", func() {
		proc, grandchild := startGroupProcess()
		killGroupAtEnd(grandchild)
		path := filepath.Join(GinkgoT().TempDir(), "processes.json")
		pid, err := strconv.Atoi(proc.CurrentPID())
		Expect(err).ToNot(HaveOccurred())
		newProcessLedger(path).add("model#0", pid)

		// The worker dies here: its in-memory state is gone, only the file is left.
		restarted := newProcessLedger(path)
		Expect(restarted.sweepStale()).To(Equal(1))

		Eventually(func() bool { return grandAlive(grandchild) }, 10*time.Second, 50*time.Millisecond).Should(BeFalse(),
			"an orphaned grandchild keeps GPU memory and a port")
		Eventually(func() bool { return pidAlive(proc.CurrentPID()) }, 10*time.Second, 50*time.Millisecond).Should(BeFalse())
	})

	It("kills a group whose leader already exited but whose grandchild lives on", func() {
		proc, grandchild := startGroupProcess()
		killGroupAtEnd(grandchild)
		path := filepath.Join(GinkgoT().TempDir(), "processes.json")
		pid, _ := strconv.Atoi(proc.CurrentPID())
		newProcessLedger(path).add("model#0", pid)
		// Only the leader dies.
		Expect(syscallKill(pid)).To(Succeed())
		Eventually(func() bool { return pidAlive(proc.CurrentPID()) }, 10*time.Second, 50*time.Millisecond).Should(BeFalse())
		Expect(grandAlive(grandchild)).To(BeTrue())

		Expect(newProcessLedger(path).sweepStale()).To(Equal(1))
		Eventually(func() bool { return grandAlive(grandchild) }, 10*time.Second, 50*time.Millisecond).Should(BeFalse())
	})

	It("are not confused with an unrelated process that reused the pid", func() {
		other := process.New(process.WithTemporaryStateDir(), process.WithName("/bin/sleep"), process.WithArgs("300"))
		Expect(other.Run()).To(Succeed())
		DeferCleanup(func() { _ = other.Stop() })
		path := filepath.Join(GinkgoT().TempDir(), "processes.json")
		pid, _ := strconv.Atoi(other.CurrentPID())
		ledger := newProcessLedger(path)
		ledger.add("model#0", pid)
		// The recorded start time no longer matches: the pid belongs to someone else.
		ledger.corruptStartTimeForTest("model#0")

		Expect(newProcessLedger(path).sweepStale()).To(BeZero())
		Expect(pidAlive(other.CurrentPID())).To(BeTrue())
	})

	It("are not swept once the worker stopped them itself", func() {
		other := process.New(process.WithTemporaryStateDir(), process.WithName("/bin/sleep"), process.WithArgs("300"))
		Expect(other.Run()).To(Succeed())
		DeferCleanup(func() { _ = other.Stop() })
		path := filepath.Join(GinkgoT().TempDir(), "processes.json")
		pid, _ := strconv.Atoi(other.CurrentPID())
		ledger := newProcessLedger(path)
		ledger.add("model#0", pid)
		ledger.remove("model#0")

		Expect(newProcessLedger(path).sweepStale()).To(BeZero())
		Expect(pidAlive(other.CurrentPID())).To(BeTrue())
	})

	It("is a no-op with no ledger file", func() {
		Expect(newProcessLedger(filepath.Join(GinkgoT().TempDir(), "missing.json")).sweepStale()).To(BeZero())
		var none *processLedger
		Expect(none.sweepStale()).To(BeZero())
		none.add("x", 1) // must not panic
		none.remove("x")
	})
})

// A restarted worker can hand out a port an orphan still holds. The readiness
// poll would then connect to the orphan and report a backend that is not its own.
var _ = Describe("Port allocation", func() {
	It("skips a port that something already listens on", func() {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = lis.Close() })
		busy := lis.Addr().(*net.TCPAddr).Port

		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}, nextPort: busy, minPort: busy, maxPort: busy + 20}
		s.mu.Lock()
		port, err := s.allocateFreePort("model#0")
		s.mu.Unlock()

		Expect(err).ToNot(HaveOccurred())
		Expect(port).ToNot(Equal(busy), "an orphan holds that port")
		Expect(quarantinedPortNumbers(s)).To(ContainElement(busy), "the busy port comes back later, not now")
	})

	It("refuses a start when every port in range is busy", func() {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = lis.Close() })
		busy := lis.Addr().(*net.TCPAddr).Port
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}, nextPort: busy, minPort: busy, maxPort: busy}
		s.mu.Lock()
		_, err = s.allocateFreePort("model#0")
		s.mu.Unlock()
		Expect(err).To(MatchError(ErrNoFreePort))
	})
})
