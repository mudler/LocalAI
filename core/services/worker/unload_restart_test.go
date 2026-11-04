package worker

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

	It("keeps a backend whose Free fails for another reason", func() {
		backend := &modelStopBackend{freeErr: status.Error(codes.Internal, "boom")}
		s, addr, _ := unloadSupervisor(backend)

		Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model"}).Success).To(BeTrue())
		Expect(s.getAddr("model#0")).To(Equal(addr))
	})
})
