package messaging_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// The surviving node subject. It is written out BY HAND and not derived from
// the builder: an agent worker built from another commit subscribes to this
// literal, and a renamed subject is silence that looks exactly like a worker
// that never started.
var _ = Describe("SubjectNodeBackendStop", func() {
	It("returns the per-node stop subject an agent worker subscribes to", func() {
		Expect(messaging.SubjectNodeBackendStop("abc")).
			To(Equal("nodes.abc.backend.stop"))
	})

	It("sanitizes reserved NATS tokens in the node id", func() {
		Expect(messaging.SubjectNodeBackendStop("a.b*c")).
			To(Equal("nodes.a-b-c.backend.stop"))
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
