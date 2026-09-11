package messaging_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

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
