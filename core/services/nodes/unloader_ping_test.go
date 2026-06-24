package nodes

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/nats-io/nats.go"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// The scheduler's liveness probe asks a worker a question over NATS and treats
// "no responders" as proof the worker is gone. That is only sound if every
// worker in the fleet subscribes to the subject asked.
//
// It originally asked models.running, which arrived in 4.6. A 4.5 worker is
// perfectly alive and serving, answers backend.list, and never subscribes to
// models.running, so the probe condemned it on every scheduling attempt. A
// model pinned to such a node could then never be placed at all.
var _ = Describe("Node liveness probe subject", func() {
	var (
		mc      *scriptedMessagingClient
		adapter *RemoteUnloaderAdapter
	)

	const nodeID = "11111111-2222-3333-4444-555555555555"

	BeforeEach(func() {
		mc = newScriptedMessagingClient()
		adapter = NewRemoteUnloaderAdapter(nil, mc, 3*time.Minute, 15*time.Minute)
	})

	It("treats a worker that answers backend.list as alive", func() {
		// A worker old enough to predate models.running: it answers the
		// long-standing backend.list subject and nothing else.
		mc.scriptReply(messaging.SubjectNodeBackendList(nodeID), messaging.BackendListReply{})
		mc.scriptNoResponders(messaging.SubjectNodeModelsRunning(nodeID))

		Expect(errors.Is(adapter.PingNode(nodeID), nats.ErrNoResponders)).To(BeFalse(),
			"a worker answering backend.list is alive regardless of newer subjects")
	})

	It("still reports a worker that answers nothing as absent", func() {
		mc.scriptNoResponders(messaging.SubjectNodeBackendList(nodeID))
		mc.scriptNoResponders(messaging.SubjectNodeModelsRunning(nodeID))

		Expect(errors.Is(adapter.PingNode(nodeID), nats.ErrNoResponders)).To(BeTrue())
	})
})
