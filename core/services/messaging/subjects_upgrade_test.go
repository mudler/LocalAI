package messaging_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

var _ = Describe("SubjectNodeBackendUpgrade", func() {
	It("returns the per-node upgrade subject", func() {
		Expect(messaging.SubjectNodeBackendUpgrade("abc")).
			To(Equal("nodes.abc.backend.upgrade"))
	})

	It("sanitizes reserved NATS tokens in the node id", func() {
		Expect(messaging.SubjectNodeBackendUpgrade("a.b*c")).
			To(Equal("nodes.a-b-c.backend.upgrade"))
	})
})

var _ = Describe("BackendUpgradeRequest", func() {
	It("carries backend name, galleries JSON, and replica index", func() {
		req := messaging.BackendUpgradeRequest{
			Backend:          "llama-cpp",
			BackendGalleries: `[{"name":"x"}]`,
			ReplicaIndex:     2,
		}
		Expect(req.Backend).To(Equal("llama-cpp"))
		Expect(req.ReplicaIndex).To(BeEquivalentTo(2))
	})
})
