package worker

import (
	"context"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Load recovery targeting", func() {
	It("does not Free an unrelated process when the requested model is absent", func() {
		backend := &modelStopBackend{}
		addr, _, stop := startModelStopBackend(backend)
		defer stop()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{"other#0": {addr: addr}}}
		reply := s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "missing"})
		Expect(reply.Success).To(BeTrue())
		Expect(backend.freeCalls.Load()).To(BeZero())
	})
	It("does not trust an address belonging to another model", func() {
		backend := &modelStopBackend{}
		addr, _, stop := startModelStopBackend(backend)
		defer stop()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{"other#0": {addr: addr}}}
		s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "missing", Address: addr})
		Expect(backend.freeCalls.Load()).To(BeZero())
	})
	It("rejects a stop whose model does not match its process key", func() {
		proc := startModelStopProcess()
		defer proc.Stop()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{"other#0": {proc: proc, addr: "localhost:1"}}}
		reply := s.stopModelExact(workerctl.ModelStopRequest{ModelName: "wanted", ProcessKey: "other#0", ExpectedAddress: "localhost:1", Force: true})
		Expect(reply.Terminated).To(BeFalse())
		Expect(reply.Error).NotTo(BeEmpty())
	})
	It("rejects unverified configuration revisions", func() {
		proc := startModelStopProcess()
		defer proc.Stop()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{"model#0": {proc: proc, addr: "localhost:1"}}}
		reply := s.stopModelExact(workerctl.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: "localhost:1", ConfigRevision: "new", Force: true})
		Expect(reply.Terminated).To(BeFalse())
	})

	It("fences installs by boot and keeps in-progress work in inventory", func() {
		id := &workerctl.OperationIdentity{TrackingKey: "model", Generation: "g1", Incarnation: workerIncarnation}
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}}
		req := workerctl.BackendInstallRequest{ModelID: "model", Backend: "backend", Operation: id}
		token, err := s.beginLoadOperation(req)
		Expect(err).NotTo(HaveOccurred())
		_ = token
		inv := s.modelsRunning(context.Background(), workerctl.ModelsRunningRequest{})
		Expect(inv.ReportsOperations).To(BeTrue())
		Expect(inv.Operations).To(HaveLen(1))
		Expect(inv.Operations[0].Active).To(BeTrue())
		stop := s.stopModelExact(workerctl.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: "localhost:1", Operation: id})
		Expect(stop.Terminated).To(BeFalse())
		s.finishLoadInstall(req, token)
		inv = s.modelsRunning(context.Background(), workerctl.ModelsRunningRequest{})
		Expect(inv.Operations[0].Phase).To(Equal("stage-or-load"))
		_, err = s.beginLoadOperation(req)
		Expect(err).To(HaveOccurred())
		stale := *id
		stale.Incarnation = "old-boot"
		req.ModelID = "other"
		req.Operation = &stale
		_, err = s.beginLoadOperation(req)
		Expect(err).To(HaveOccurred())
	})
	It("fences same-address reuse with process instance and generation", func() {
		proc := startModelStopProcess()
		defer proc.Stop()
		id := &workerctl.OperationIdentity{TrackingKey: "model", Generation: "g2", Incarnation: workerIncarnation}
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{"model#0": {proc: proc, addr: "localhost:1", instance: "new", operation: id}}}
		req := workerctl.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: "localhost:1", ProcessInstance: "old", Force: true}
		Expect(s.stopModelExact(req).Terminated).To(BeFalse())
		req.ProcessInstance = "new"
		old := *id
		old.Generation = "g1"
		req.Operation = &old
		Expect(s.stopModelExact(req).Terminated).To(BeFalse())
	})
	It("unloads every exact replica without touching another model", func() {
		a, b, c := &modelStopBackend{}, &modelStopBackend{}, &modelStopBackend{}
		aa, _, sa := startModelStopBackend(a)
		defer sa()
		ab, _, sb := startModelStopBackend(b)
		defer sb()
		ac, _, sc := startModelStopBackend(c)
		defer sc()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{"model#0": {addr: aa}, "model#1": {addr: ab}, "other#0": {addr: ac}}}
		Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model"}).Success).To(BeTrue())
		Expect(a.freeCalls.Load()).To(Equal(int32(1)))
		Expect(b.freeCalls.Load()).To(Equal(int32(1)))
		Expect(c.freeCalls.Load()).To(BeZero())
	})
})
