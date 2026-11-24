package worker

import (
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	process "github.com/mudler/go-processmanager"
)

// The worker owns the backend processes and its answer is not affected by how
// busy those processes are, which is what makes it a sounder liveness source
// than probing the backend's own serving port. These tests pin the reply it
// gives the reconciler.
var _ = Describe("backendSupervisor running models", func() {
	// liveProcess starts a real long-running child so IsAlive() is true.
	liveProcess := func() *process.Process {
		sleepBin, err := exec.LookPath("sleep")
		Expect(err).ToNot(HaveOccurred())
		p := process.New(
			process.WithTemporaryStateDir(),
			process.WithName(sleepBin),
			process.WithArgs("60"),
		)
		Expect(p.Run()).To(Succeed())
		DeferCleanup(func() { _ = p.Stop() })
		Eventually(p.IsAlive, 5*time.Second, 20*time.Millisecond).Should(BeTrue())
		return p
	}

	It("reports each live process as its model and replica index", func() {
		s := &backendSupervisor{
			processes: map[string]*backendProcess{
				"qwen3.6-35B#0": {proc: liveProcess(), addr: "127.0.0.1:50051"},
				"qwen3.6-35B#1": {proc: liveProcess(), addr: "127.0.0.1:50052"},
				"other-model#0": {proc: liveProcess(), addr: "127.0.0.1:50053"},
			},
		}

		running := s.runningModels()

		Expect(running).To(ConsistOf(
			HaveField("ModelID", "qwen3.6-35B"),
			HaveField("ModelID", "qwen3.6-35B"),
			HaveField("ModelID", "other-model"),
		))
		byAddr := map[string]int{}
		for _, r := range running {
			byAddr[r.Address] = r.ReplicaIndex
		}
		Expect(byAddr).To(Equal(map[string]int{
			"127.0.0.1:50051": 0,
			"127.0.0.1:50052": 1,
			"127.0.0.1:50053": 0,
		}))
	})

	It("omits processes that are not alive", func() {
		// A recorded slot whose process died must not be reported as running,
		// otherwise the reconciler would keep a dead replica's row forever.
		s := &backendSupervisor{
			processes: map[string]*backendProcess{
				"dead-model#0": {proc: nil, addr: "127.0.0.1:50051"},
				"live-model#0": {proc: liveProcess(), addr: "127.0.0.1:50052"},
			},
		}

		Expect(s.runningModels()).To(ConsistOf(HaveField("ModelID", "live-model")))
	})

	It("omits processes that are being stopped", func() {
		// Mid-stop the process is still alive, but its row is on its way out;
		// reporting it would resurrect a replica the controller just released.
		s := &backendSupervisor{
			processes: map[string]*backendProcess{
				"going-away#0": {proc: liveProcess(), addr: "127.0.0.1:50051", stopping: true},
			},
		}

		Expect(s.runningModels()).To(BeEmpty())
	})
})

var _ = Describe("parseProcessKey", func() {
	It("splits a modelID#replica key, tolerating '#' inside the model id", func() {
		modelID, replica, ok := parseProcessKey("qwen3.6-35B#2")
		Expect(ok).To(BeTrue())
		Expect(modelID).To(Equal("qwen3.6-35B"))
		Expect(replica).To(Equal(2))

		// Model ids are user-supplied, so the split must be on the LAST '#'.
		modelID, replica, ok = parseProcessKey("weird#name#3")
		Expect(ok).To(BeTrue())
		Expect(modelID).To(Equal("weird#name"))
		Expect(replica).To(Equal(3))
	})

	It("rejects keys without a valid replica suffix", func() {
		_, _, ok := parseProcessKey("no-suffix")
		Expect(ok).To(BeFalse())
		_, _, ok = parseProcessKey("bad#suffix")
		Expect(ok).To(BeFalse())
	})

	It("round-trips with buildProcessKey", func() {
		modelID, replica, ok := parseProcessKey(buildProcessKey("my-model", "some-backend", 4))
		Expect(ok).To(BeTrue())
		Expect(modelID).To(Equal("my-model"))
		Expect(replica).To(Equal(4))
	})
})
