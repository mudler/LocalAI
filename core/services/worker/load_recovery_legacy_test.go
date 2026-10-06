package worker

import (
	"context"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Legacy operation recovery", func() {
	It("reports legacy install work before a process exists", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{}}
		_, err := s.beginLoadOperation(workerctl.BackendInstallRequest{ModelID: "model", Backend: "backend"})
		Expect(err).NotTo(HaveOccurred())
		inv := s.modelsRunning(context.Background(), workerctl.ModelsRunningRequest{})
		Expect(inv.ReportsOperations).To(BeTrue())
		Expect(inv.Operations).To(HaveLen(1))
		Expect(inv.Operations[0].Active).To(BeTrue())
	})
	It("does not admit owned work over an active legacy install", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{}}
		req := workerctl.BackendInstallRequest{ModelID: "model", Backend: "backend"}
		token, err := s.beginLoadOperation(req)
		Expect(err).NotTo(HaveOccurred())
		_ = token
		req.Operation = &workerctl.OperationIdentity{TrackingKey: "model", Generation: "generation", Incarnation: workerIncarnation}
		_, err = s.beginLoadOperation(req)
		Expect(err).To(HaveOccurred())
	})
	It("preserves a reused process revision on legacy completion", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{"model#0": {instance: "original", revision: "revision"}}}
		req := workerctl.BackendInstallRequest{ModelID: "model", Backend: "backend"}
		token, err := s.beginLoadOperation(req)
		Expect(err).NotTo(HaveOccurred())
		_ = token
		s.finishLoadInstall(req, token)
		Expect(s.processes["model#0"].revision).To(Equal("revision"))
	})
	It("ignores old install completion across a new invocation and same-address replacement", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{"model#0": {addr: "localhost:1", instance: "old"}}}
		req := workerctl.BackendInstallRequest{ModelID: "model", Backend: "backend", ConfigRevision: "old"}
		old, err := s.beginLoadOperation(req)
		Expect(err).NotTo(HaveOccurred())
		replacement := &backendProcess{addr: "localhost:1", instance: "new", revision: "new"}
		s.processes["model#0"] = replacement
		Expect(s.finishLoadInstall(req, old)).To(BeEmpty())
		Expect(replacement.revision).To(Equal("new"))
		req.ConfigRevision = "new"
		next, err := s.beginLoadOperation(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.finishLoadInstall(req, old)).To(BeEmpty())
		Expect(next.operation.Active).To(BeTrue())
		Expect(s.finishLoadInstall(req, next)).To(Equal("new"))
	})
	It("keeps legacy and owned staging invocations visible and isolates duplicate completions", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{}}
		req := workerctl.BackendInstallRequest{ModelID: "model", Backend: "backend", Operation: &workerctl.OperationIdentity{TrackingKey: "model", Generation: "g", Incarnation: workerIncarnation}}
		install, err := s.beginLoadOperation(req)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.beginLoadOperation(workerctl.BackendInstallRequest{ModelID: "model", Backend: "backend"})
		Expect(err).To(HaveOccurred())
		s.finishLoadInstall(req, install)
		first, err := s.beginStaging(req.Operation, "model#0")
		Expect(err).NotTo(HaveOccurred())
		legacy, err := s.beginStaging(nil, "")
		Expect(err).NotTo(HaveOccurred())
		inv := s.modelsRunning(context.Background(), workerctl.ModelsRunningRequest{})
		Expect(inv.Operations).To(HaveLen(2))
		s.endStaging(first)
		next, err := s.beginStaging(req.Operation, "model#0")
		Expect(err).NotTo(HaveOccurred())
		s.endStaging(first)
		s.endStaging(legacy)
		s.endStaging(legacy)
		Expect(next.Active).To(BeTrue())
		Expect(s.modelsRunning(context.Background(), workerctl.ModelsRunningRequest{}).Operations).To(HaveLen(1))
		s.endStaging(next)
	})

})
