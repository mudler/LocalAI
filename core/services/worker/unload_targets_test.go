package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// model.unload frees GPU memory of one model. Freeing another model's
// process empties it while the control plane still counts it as loaded, so
// the target must come from the model name and never from map order.
var _ = Describe("backendSupervisor unload targets", func() {
	newSupervisor := func() *backendSupervisor {
		return &backendSupervisor{
			processes: map[string]*backendProcess{
				"chat#0":     {addr: "127.0.0.1:50051"},
				"chat#1":     {addr: "127.0.0.1:50052"},
				"parakeet#0": {addr: "127.0.0.1:50053"},
			},
		}
	}

	It("targets every replica of the named model and nothing else", func() {
		s := newSupervisor()
		for range 50 { // map iteration order is random; the answer must not be
			Expect(s.unloadTargets(workerctl.ModelUnloadRequest{ModelName: "chat"})).
				To(ConsistOf("127.0.0.1:50051", "127.0.0.1:50052"))
		}
	})

	It("targets the process named by an exact key", func() {
		Expect(newSupervisor().unloadTargets(workerctl.ModelUnloadRequest{ModelName: "parakeet#0"})).
			To(ConsistOf("127.0.0.1:50053"))
	})

	It("prefers the address in the request", func() {
		Expect(newSupervisor().unloadTargets(workerctl.ModelUnloadRequest{ModelName: "chat", Address: "10.0.0.1:1"})).
			To(ConsistOf("10.0.0.1:1"))
	})

	It("frees nothing for a model that is not running", func() {
		s := newSupervisor()
		Expect(s.unloadTargets(workerctl.ModelUnloadRequest{ModelName: "missing"})).To(BeEmpty())
		Expect(s.unloadTargets(workerctl.ModelUnloadRequest{})).To(BeEmpty())
	})
})
