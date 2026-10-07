package worker

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	process "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	gogrpc "google.golang.org/grpc"
)

// freeHookBackend runs a hook inside Free(), so a spec can act while the
// supervisor is in the middle of an unload.
type freeHookBackend struct {
	pb.UnimplementedBackendServer
	frees  atomic.Int32
	onFree func()
}

func (b *freeHookBackend) Free(context.Context, *pb.HealthMessage) (*pb.Result, error) {
	b.frees.Add(1)
	if b.onFree != nil {
		b.onFree()
	}
	return &pb.Result{Success: true}, nil
}

// startGroupProcess starts a real shell in its own process group. The shell
// starts a grandchild and records its pid, so a spec can tell that the whole
// group is gone and not only the leader.
func startGroupProcess() (*process.Process, int) {
	pidFile := filepath.Join(GinkgoT().TempDir(), "grandchild.pid")
	proc := process.New(process.WithTemporaryStateDir(), process.WithName("/bin/sh"),
		process.WithArgs("-c", "sleep 300 & echo $! > "+pidFile+"; wait"))
	Expect(proc.Run()).To(Succeed())
	var grandchild int
	Eventually(func() int {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return 0
		}
		grandchild, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		return grandchild
	}, 5*time.Second, 20*time.Millisecond).ShouldNot(BeZero())
	return proc, grandchild
}

func newOperationSupervisor(proc *process.Process) (*backendSupervisor, *loadOperation) {
	s := &backendSupervisor{
		cfg:       &Config{},
		processes: map[string]*backendProcess{},
		opKillTTL: time.Second,
		opTick:    100 * time.Millisecond,
		readyFn:   func(string) bool { return false },
	}
	s.processes["model#0"] = &backendProcess{proc: proc, addr: "127.0.0.1:59001", port: 59001, instance: "instance-1"}
	op := s.beginOperation(workerctl.BackendInstallRequest{ModelID: "model", OperationID: "gen-1", DeadlineMs: 600000})
	Expect(s.attachOperation(op)).To(Equal("instance-1"))
	return s, op
}

func runWatchdog(s *backendSupervisor) {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	go s.runOperationWatchdog(ctx)
}

var _ = Describe("Load operation watchdog", func() {
	It("kills the whole process group, grandchildren included, when renewals stop", func() {
		proc, grandchild := startGroupProcess()
		DeferCleanup(func() {
			if grandAlive(grandchild) {
				_ = syscallKill(grandchild)
			}
		})
		s, _ := newOperationSupervisor(proc)
		runWatchdog(s)

		// Renewals keep it alive well past the kill TTL.
		for range 12 {
			time.Sleep(200 * time.Millisecond)
			reply := s.serveOperations(context.Background(), workerctl.OperationRequest{Renew: []string{"gen-1"}})
			Expect(reply.Renewed).To(ConsistOf("gen-1"))
		}
		Expect(grandAlive(grandchild)).To(BeTrue())
		Expect(pidAlive(proc.CurrentPID())).To(BeTrue())

		// The controller goes silent.
		Eventually(func() bool { return grandAlive(grandchild) }, 15*time.Second, 100*time.Millisecond).Should(BeFalse(),
			"the grandchild is in the group and must die with it")
		Eventually(proc.Done(), 15*time.Second, 100*time.Millisecond).Should(BeClosed(), "the leader is reaped once it is killed")
		Eventually(func() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.processes) }, 5*time.Second).Should(BeZero())
	})

	It("kills at the absolute deadline even when renewals keep arriving", func() {
		proc, grandchild := startGroupProcess()
		DeferCleanup(func() {
			if grandAlive(grandchild) {
				_ = syscallKill(grandchild)
			}
		})
		s, op := newOperationSupervisor(proc)
		s.mu.Lock()
		op.deadline = time.Now().Add(700 * time.Millisecond)
		s.mu.Unlock()
		runWatchdog(s)
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			for {
				select {
				case <-stop:
					return
				case <-time.After(100 * time.Millisecond):
					s.serveOperations(context.Background(), workerctl.OperationRequest{Renew: []string{"gen-1"}})
				}
			}
		}()
		Eventually(func() bool { return grandAlive(grandchild) }, 15*time.Second, 100*time.Millisecond).Should(BeFalse())
	})

	It("never kills an anonymous operation for missing renewals, only at its deadline", func() {
		proc, grandchild := startGroupProcess()
		DeferCleanup(func() {
			if grandAlive(grandchild) {
				_ = syscallKill(grandchild)
			}
		})
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{},
			opKillTTL: 200 * time.Millisecond, opTick: 50 * time.Millisecond, readyFn: func(string) bool { return false }}
		s.processes["model#0"] = &backendProcess{proc: proc, addr: "127.0.0.1:59002", port: 59002, instance: "i"}
		// A controller older than operations sends no id and no deadline.
		op := s.beginOperation(workerctl.BackendInstallRequest{ModelID: "model"})
		Expect(op.anonymous).To(BeTrue())
		s.attachOperation(op)
		runWatchdog(s)

		Consistently(func() bool { return grandAlive(grandchild) }, 1500*time.Millisecond, 100*time.Millisecond).Should(BeTrue(),
			"silence is not a reason to kill a controller that cannot renew")

		s.mu.Lock()
		op.deadline = time.Now().Add(300 * time.Millisecond)
		s.mu.Unlock()
		Eventually(func() bool { return grandAlive(grandchild) }, 15*time.Second, 100*time.Millisecond).Should(BeFalse())
	})

	It("does not kill a backend that already serves, when a completion was lost", func() {
		proc, grandchild := startGroupProcess()
		DeferCleanup(func() {
			if grandAlive(grandchild) {
				_ = syscallKill(grandchild)
			}
		})
		s, _ := newOperationSupervisor(proc)
		s.readyFn = func(string) bool { return true }
		runWatchdog(s)

		Eventually(func() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.operations) }, 10*time.Second).Should(BeZero())
		Expect(grandAlive(grandchild)).To(BeTrue(), "a serving model is not a load to kill")
		Expect(s.runningModels()).To(HaveLen(1))
	})

	It("stops watching an operation once it is completed", func() {
		proc, grandchild := startGroupProcess()
		DeferCleanup(func() {
			if grandAlive(grandchild) {
				_ = syscallKill(grandchild)
			}
			_ = proc.Stop()
		})
		s, _ := newOperationSupervisor(proc)
		runWatchdog(s)

		reply := s.serveOperations(context.Background(), workerctl.OperationRequest{Complete: []string{"gen-1"}})
		Expect(reply.Completed).To(ConsistOf("gen-1"))
		Consistently(func() bool { return grandAlive(grandchild) }, 2*time.Second, 100*time.Millisecond).Should(BeTrue())
		Expect(s.runningModels()[0].OperationID).To(BeEmpty())
	})

	It("tells the controller which operations it does not know", func() {
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}}
		reply := s.serveOperations(context.Background(), workerctl.OperationRequest{Renew: []string{"nope"}})
		Expect(reply.Unknown).To(ConsistOf("nope"))
		Expect(reply.Renewed).To(BeEmpty())
	})
})

var _ = Describe("Stopping one operation", func() {
	It("reports the operation and the process instance in the inventory", func() {
		proc := startModelStopProcess()
		DeferCleanup(func() { _ = proc.Stop() })
		s, _ := newOperationSupervisor(proc)
		reply := s.modelsRunning(context.Background(), workerctl.ModelsRunningRequest{})
		Expect(reply.ReportsOperations).To(BeTrue())
		Expect(reply.Models).To(ConsistOf(workerctl.RunningModelInfo{
			ModelID: "model", ReplicaIndex: 0, Address: "127.0.0.1:59001", OperationID: "gen-1", ProcessInstance: "instance-1",
		}))
	})

	It("stops exactly the process of the named operation and forgets the operation", func() {
		proc := startModelStopProcess()
		s, _ := newOperationSupervisor(proc)

		reply := requestModelStop(s, workerctl.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: "127.0.0.1:59001",
			OperationID: "gen-1", ProcessInstance: "instance-1", Force: true})

		Expect(reply.Error).To(BeEmpty())
		Expect(reply.Terminated).To(BeTrue())
		Expect(proc.Done()).To(BeClosed())
		Expect(s.operations).To(BeEmpty())
	})

	It("refuses a stop whose operation is not the one the process runs", func() {
		proc := startModelStopProcess()
		DeferCleanup(func() { _ = proc.Stop() })
		s, _ := newOperationSupervisor(proc)

		reply := requestModelStop(s, workerctl.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: "127.0.0.1:59001",
			OperationID: "other-generation", Force: true})

		Expect(reply.Terminated).To(BeFalse())
		Expect(reply.Error).To(ContainSubstring("does not belong to operation"))
		Expect(pidAlive(proc.CurrentPID())).To(BeTrue())
		Expect(s.operations).To(HaveKey("gen-1"))
	})

	It("refuses to turn a stop of a finished load into an unload of the model", func() {
		proc := startModelStopProcess()
		DeferCleanup(func() { _ = proc.Stop() })
		s, _ := newOperationSupervisor(proc)
		s.serveOperations(context.Background(), workerctl.OperationRequest{Complete: []string{"gen-1"}})

		reply := requestModelStop(s, workerctl.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: "127.0.0.1:59001",
			OperationID: "gen-1", Force: true})

		Expect(reply.Terminated).To(BeFalse(), "a cancel of a load that finished must leave the serving model alone")
		Expect(pidAlive(proc.CurrentPID())).To(BeTrue())
	})

	It("refuses a stop that names another process instance", func() {
		proc := startModelStopProcess()
		DeferCleanup(func() { _ = proc.Stop() })
		s, _ := newOperationSupervisor(proc)

		reply := requestModelStop(s, workerctl.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: "127.0.0.1:59001",
			OperationID: "gen-1", ProcessInstance: "a-replacement", Force: true})

		Expect(reply.Terminated).To(BeFalse())
		Expect(reply.Error).To(ContainSubstring("instance"))
		Expect(pidAlive(proc.CurrentPID())).To(BeTrue())
	})

	It("answers terminated for an operation whose process is already gone", func() {
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}}
		s.beginOperation(workerctl.BackendInstallRequest{ModelID: "model", OperationID: "gen-9"})

		reply := requestModelStop(s, workerctl.ModelStopRequest{ProcessKey: "model#0", OperationID: "gen-9"})

		Expect(reply.Terminated).To(BeTrue())
		Expect(s.operations).To(BeEmpty())
	})
})

var _ = Describe("model.unload", func() {
	It("frees nothing when the request names no running model, and never another model's process", func() {
		backend := &freeHookBackend{}
		addr, port, stopServer := startFreeHookBackend(backend)
		defer stopServer()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{
			"model#0": {addr: addr, port: port, instance: "i"},
		}}

		Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{}).Success).To(BeTrue())
		Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "another-model"}).Success).To(BeTrue())
		Expect(backend.frees.Load()).To(BeZero(), "the worker must not guess which running backend to free")

		// A request that names the model frees that model's own process.
		Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model"}).Success).To(BeTrue())
		Expect(backend.frees.Load()).To(Equal(int32(1)))
	})

	It("frees only the process at the given address and instance", func() {
		backend := &freeHookBackend{}
		addr, port, stopServer := startFreeHookBackend(backend)
		defer stopServer()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{
			"model#0": {addr: addr, port: port, instance: "i"},
		}}

		Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{Address: addr, ProcessInstance: "other"}).Success).To(BeTrue())
		Expect(backend.frees.Load()).To(BeZero())
		Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{Address: addr, ProcessInstance: "i"}).Success).To(BeTrue())
		Expect(backend.frees.Load()).To(Equal(int32(1)))
	})

	It("does not hold the supervisor lock across Free, and notices a replaced process", func() {
		backend := &freeHookBackend{}
		addr, port, stopServer := startFreeHookBackend(backend)
		defer stopServer()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{
			"model#0": {addr: addr, port: port, instance: "i"},
		}}
		// If unloadModel held the lock during Free, taking it here would
		// deadlock and the spec would time out.
		backend.onFree = func() {
			s.mu.Lock()
			s.processes["model#0"] = &backendProcess{addr: addr, port: port, instance: "replacement"}
			s.mu.Unlock()
		}

		reply := s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{Address: addr})

		Expect(reply.Success).To(BeFalse())
		Expect(reply.Error).To(ContainSubstring("replaced"))
	})
})

func grandAlive(pid int) bool { return pidAlive(strconv.Itoa(pid)) }

func syscallKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

func startRegisteredBackend(register func(*gogrpc.Server)) (string, int, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	server := gogrpc.NewServer()
	register(server)
	go func() { _ = server.Serve(lis) }()
	return lis.Addr().String(), lis.Addr().(*net.TCPAddr).Port, server.Stop
}

func startFreeHookBackend(backend *freeHookBackend) (string, int, func()) {
	return startRegisteredBackend(func(server *gogrpc.Server) { pb.RegisterBackendServer(server, backend) })
}
