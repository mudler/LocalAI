package nodes

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

var _ = Describe("RemoteUnloaderAdapter.UpgradeBackend", func() {
	It("fires a NATS request to the backend.upgrade subject and returns the reply", func() {
		mc := newScriptedMessagingClient()
		nodeID := "node-x"

		mc.scriptReply(messaging.SubjectNodeBackendUpgrade(nodeID),
			messaging.BackendUpgradeReply{Success: true})

		adapter := NewRemoteUnloaderAdapter(nil, mc)
		reply, err := adapter.UpgradeBackend(nodeID, "llama-cpp", `[{"name":"x"}]`, "", "", "", 0)
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Success).To(BeTrue())
	})

	It("returns the underlying error when the subject has no responders", func() {
		mc := newScriptedMessagingClient() // unscripted subject => fakeNoRespondersErr by harness convention

		adapter := NewRemoteUnloaderAdapter(nil, mc)
		_, err := adapter.UpgradeBackend("missing-node", "llama-cpp", "", "", "", "", 0)
		Expect(err).To(HaveOccurred())
	})
})
