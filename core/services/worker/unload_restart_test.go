package worker

import (
	"context"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

var _ = Describe("model.unload and the backend process", func() {
	unloadSupervisor := func(backend *modelStopBackend) (*backendSupervisor, string, int) {
		addr, port, stopServer := startModelStopBackend(backend)
		DeferCleanup(stopServer)
		proc := startModelStopProcess()
		DeferCleanup(func() {
			if pidAlive(proc.CurrentPID()) {
				_ = proc.Stop()
			}
		})
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{
			"model#0": {proc: proc, addr: addr, port: port, instance: "i"},
		}}
		return s, addr, port
	}

	It("stops a backend that does not implement Free, so the next install starts a fresh process", func() {
		// llama-cpp is such a backend: its second LoadModel on a live process aborts.
		backend := &modelStopBackend{freeErr: status.Error(codes.Unimplemented, "method Free not implemented")}
		s, _, _ := unloadSupervisor(backend)
		old := s.processes["model#0"].proc

		reply := s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model"})

		Expect(reply.Success).To(BeTrue())
		Expect(backend.freeCalls.Load()).To(Equal(int32(1)), "Free is tried once, not again before the stop")
		Expect(old.Done()).To(BeClosed())
		Expect(s.processes).NotTo(HaveKey("model#0"))
		Expect(s.getAddr("model#0")).To(BeEmpty(), "installBackend must not take its already-running path")
	})

	It("keeps a backend that frees its model, so a reload stays cheap", func() {
		backend := &modelStopBackend{}
		s, addr, _ := unloadSupervisor(backend)

		reply := s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model"})

		Expect(reply.Success).To(BeTrue())
		Expect(backend.freeCalls.Load()).To(Equal(int32(1)))
		Expect(s.getAddr("model#0")).To(Equal(addr))
		Expect(pidAlive(s.processes["model#0"].proc.CurrentPID())).To(BeTrue())
	})

	It("stops a backend whose Free fails, because it may still hold its model", func() {
		backend := &modelStopBackend{freeErr: status.Error(codes.Internal, "boom")}
		s, _, _ := unloadSupervisor(backend)
		old := s.processes["model#0"].proc

		Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model"}).Success).To(BeTrue())
		Expect(old.Done()).To(BeClosed())
		Expect(s.getAddr("model#0")).To(BeEmpty(), "installBackend must not take its already-running path")
	})

	Context("logging", func() {
		var logs *lockedBuffer

		BeforeEach(func() {
			logs = &lockedBuffer{}
			handler := slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo})
			xlog.SetLogger(xlog.NewLoggerWithHandler(handler, xlog.LogLevelInfo))
		})

		AfterEach(func() {
			xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text"))
		})

		It("says when the address has no process", func() {
			s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}}

			reply := s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model", Address: "127.0.0.1:1"})

			Expect(reply.Success).To(BeTrue())
			Expect(logs.String()).To(ContainSubstring("has no process in this worker"))
		})

		It("says when the request names an older instance and keeps the live process", func() {
			backend := &modelStopBackend{}
			s, addr, _ := unloadSupervisor(backend)

			reply := s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model", ProcessInstance: "older"})

			Expect(reply.Success).To(BeTrue())
			Expect(backend.freeCalls.Load()).To(BeZero())
			Expect(s.getAddr("model#0")).To(Equal(addr))
			Expect(logs.String()).To(And(ContainSubstring("older process instance"), ContainSubstring("requestedInstance=older")))
		})

		It("says when Free succeeds and the process is kept", func() {
			s, _, _ := unloadSupervisor(&modelStopBackend{})

			Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model"}).Success).To(BeTrue())
			Expect(logs.String()).To(ContainSubstring("Free() succeeded"))
		})

		It("says when Free fails and the process is stopped", func() {
			s, _, _ := unloadSupervisor(&modelStopBackend{freeErr: status.Error(codes.Internal, "boom")})

			Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model"}).Success).To(BeTrue())
			Expect(logs.String()).To(And(
				ContainSubstring("Free() failed"),
				ContainSubstring("Stopping backend process after Free() did not succeed"),
				ContainSubstring("Stopped backend process after model.unload"),
			))
		})
	})
})
