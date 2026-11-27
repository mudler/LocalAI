package worker

import (
	"context"
	"encoding/json"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

var _ = Describe("Worker shutdown", func() {
	It("refuses new installs and upgrades once the shutdown started", func() {
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}}
		called := false
		s.installFn = func(workerctl.BackendInstallRequest, bool, func(string, string, string, float64)) (string, error) {
			called = true
			return "127.0.0.1:1", nil
		}
		s.beginShutdown()

		reply := s.serveInstall(context.Background(), workerctl.BackendInstallRequest{Backend: "b", ModelID: "m", OperationID: "op"}, nil)
		Expect(reply.Success).To(BeFalse())
		Expect(reply.ShuttingDown).To(BeTrue())
		Expect(reply.Error).To(Equal(workerctl.ErrWorkerShuttingDownMsg))
		Expect(called).To(BeFalse())
		Expect(s.operations).To(BeEmpty(), "no load operation is started")

		up := s.serveUpgrade(context.Background(), workerctl.BackendUpgradeRequest{Backend: "b"}, nil)
		Expect(up.Success).To(BeFalse())
		Expect(up.Error).To(Equal(workerctl.ErrWorkerShuttingDownMsg))
	})

	It("aborts the loads in progress and waits until their processes exited", func() {
		proc, grandchild := startGroupProcess()
		DeferCleanup(func() {
			if grandAlive(grandchild) {
				_ = syscallKill(grandchild)
			}
		})
		s, _ := newOperationSupervisor(proc)

		start := time.Now()
		aborted, ended := s.abortLoadOperations(10 * time.Second)
		Expect(aborted).To(ConsistOf("gen-1"))
		Expect(ended).To(BeTrue())
		Expect(time.Since(start)).To(BeNumerically("<", 15*time.Second))
		Expect(proc.Done()).To(BeClosed(), "the process exited before abortLoadOperations returned")
		Eventually(func() bool { return grandAlive(grandchild) }, 5*time.Second, 50*time.Millisecond).Should(BeFalse())
		s.mu.Lock()
		defer s.mu.Unlock()
		Expect(s.operations).To(BeEmpty())
		Expect(s.processes).To(BeEmpty())
	})

	It("does not say the loads ended while a process outlives the abort wait", func() {
		proc := startModelStopProcess()
		DeferCleanup(func() { _ = proc.Stop() })
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}}
		// Another stop is ending the process: the abort only waits for it.
		s.processes["model#0"] = &backendProcess{proc: proc, addr: "127.0.0.1:59005", port: 59005, instance: "i1", stopping: true}
		op, ok := s.beginLoad(workerctl.BackendInstallRequest{ModelID: "model", OperationID: "gen-1"})
		Expect(ok).To(BeTrue())
		Expect(s.attachOperation(op)).To(Equal("i1"))
		s.endLoad()
		s.beginShutdown()

		start := time.Now()
		aborted, ended := s.abortLoadOperations(300 * time.Millisecond)
		Expect(aborted).To(ConsistOf("gen-1"))
		Expect(ended).To(BeFalse())
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second), "the abort phase is bounded as a whole")
	})

	It("cancels a load that still downloads and says the loads ended once its install returned", func() {
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}}
		started := make(chan struct{})
		s.installFn = func(workerctl.BackendInstallRequest, bool, func(string, string, string, float64)) (string, error) {
			close(started)
			// A download: it runs until the load context is cancelled.
			<-s.loadContext().Done()
			return "", s.loadContext().Err()
		}
		replies := make(chan workerctl.BackendInstallReply, 1)
		go func() {
			replies <- s.serveInstall(context.Background(), workerctl.BackendInstallRequest{Backend: "b", ModelID: "m", OperationID: "gen-2"}, nil)
		}()
		Eventually(started).Should(BeClosed())

		s.beginShutdown()
		aborted, ended := s.abortLoadOperations(5 * time.Second)
		Expect(aborted).To(ConsistOf("gen-2"))
		Expect(ended).To(BeTrue())
		var reply workerctl.BackendInstallReply
		Expect(replies).To(Receive(&reply), "the install returned before the abort did")
		Expect(reply.Success).To(BeFalse())
		Expect(reply.ShuttingDown).To(BeTrue(), "the frontend learns why, and places the load on another node")
	})

	It("does not say the loads ended while an install still runs", func() {
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}}
		started, release := make(chan struct{}), make(chan struct{})
		DeferCleanup(func() { close(release) })
		s.installFn = func(workerctl.BackendInstallRequest, bool, func(string, string, string, float64)) (string, error) {
			close(started)
			<-release // an install step that does not watch the load context
			return "", context.Canceled
		}
		go s.serveInstall(context.Background(), workerctl.BackendInstallRequest{Backend: "b", ModelID: "m", OperationID: "gen-3"}, nil)
		Eventually(started).Should(BeClosed())

		s.beginShutdown()
		_, ended := s.abortLoadOperations(300 * time.Millisecond)
		Expect(ended).To(BeFalse())
	})

	It("starts no load and no backend process once the shutdown started", func() {
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}}
		s.beginShutdown()

		_, ok := s.beginLoad(workerctl.BackendInstallRequest{ModelID: "m", OperationID: "late"})
		Expect(ok).To(BeFalse(), "the check and the registration are one step")
		Expect(s.operations).To(BeEmpty())

		// An install that passed every check before the shutdown reaches the
		// start of its process after it: the process does not start, so
		// stopAllBackends leaves no process behind.
		_, err := s.startBackend("m#0", "b", "/bin/sleep")
		Expect(err).To(MatchError(errWorkerShuttingDown))
		Expect(s.stopAllBackends(true)).To(BeEmpty())
		Expect(s.processes).To(BeEmpty())
	})

	It("acknowledges a stop of a process that another stop is ending, once it exited", func() {
		proc := startModelStopProcess()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}, stoppingWait: 10 * time.Second}
		bp := &backendProcess{proc: proc, addr: "127.0.0.1:59002", port: 59002, instance: "i1", stopping: true}
		s.processes["model#0"] = bp

		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = proc.Stop()
		}()
		reply := s.stopModelExact(workerctl.ModelStopRequest{ModelName: "model", ProcessKey: "model#0", ExpectedAddress: "127.0.0.1:59002", Force: true})
		Expect(reply.Error).To(BeEmpty())
		Expect(reply.Terminated).To(BeTrue(), "already stopping counts once the process exited")
	})

	It("does not acknowledge a stop of a process that does not exit", func() {
		proc := startModelStopProcess()
		DeferCleanup(func() { _ = proc.Stop() })
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}, stoppingWait: 300 * time.Millisecond}
		s.processes["model#0"] = &backendProcess{proc: proc, addr: "127.0.0.1:59003", port: 59003, instance: "i1", stopping: true}

		reply := s.stopModelExact(workerctl.ModelStopRequest{ModelName: "model", ProcessKey: "model#0", ExpectedAddress: "127.0.0.1:59003", Force: true})
		Expect(reply.Terminated).To(BeFalse())
		Expect(reply.Error).To(ContainSubstring("already stopping"))
	})

	It("waits for a process that another stop is ending for less than the frontend waits for the reply", func() {
		Expect(defaultStoppingWait).To(BeNumerically("<=", workerctl.ModelStopTimeout/2),
			"the reply must reach the frontend before it gives up on the stop")
	})

	It("does not hold up other stops while one waits for a process another stop is ending", func() {
		proc := startModelStopProcess()
		DeferCleanup(func() { _ = proc.Stop() })
		s := newLifecycleTestSupervisor(make(chan os.Signal, 1))
		s.stoppingWait = 5 * time.Second
		s.processes["model#0"] = &backendProcess{proc: proc, addr: "127.0.0.1:59004", port: 59004, instance: "i1", stopping: true}
		bus := newRecordingBus()
		Expect(registerLifecycleForTest(s, bus)).To(Succeed())

		stop := func(key string) []byte {
			data, err := json.Marshal(workerctl.ModelStopRequest{ModelName: "model", ProcessKey: key, ExpectedAddress: "127.0.0.1:59004", Force: true})
			Expect(err).NotTo(HaveOccurred())
			return data
		}
		slow := bus.deliver(messaging.SubjectNodeModelStop("n1"), stop("model#0"))
		other := bus.deliver(messaging.SubjectNodeModelStop("n1"), stop("model#1"))
		Eventually(other, time.Second).Should(Receive(ContainSubstring(`"terminated":true`)),
			"a stop of a process that is gone answers while the first stop still waits")
		Expect(slow).NotTo(Receive())
	})

	It("reads the shutdown windows", func() {
		Expect(parseShutdownWindow("X", "", time.Minute)).To(Equal(time.Minute))
		Expect(parseShutdownWindow("X", "5s", time.Minute)).To(Equal(5 * time.Second))
		Expect(parseShutdownWindow("X", "soon", time.Minute)).To(Equal(time.Minute))
		Expect(parseShutdownWindow("X", "-1s", time.Minute)).To(Equal(time.Minute))
	})
})
