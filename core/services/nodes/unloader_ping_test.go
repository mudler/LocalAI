package nodes

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The scheduler asks a worker one cheap question before committing work to it,
// and then acts on the answer by DEMOTING the node. That is only sound if the
// question is one every worker in the fleet answers.
//
// It has been the wrong question twice. It first asked models.running, which
// arrived in 4.6, so a 4.5 worker that was alive and serving never answered and
// a model pinned to it could never be placed. It then asked two NATS subjects
// the worker had stopped subscribing to when its control plane moved onto the
// tunnel, so EVERY healthy worker answered "no responders" and was marked
// unhealthy on the scheduling path.
//
// These specs pin both halves of the answer: what counts as the worker
// speaking, and what must never be read as the worker being gone.
var _ = Describe("Node liveness probe over the control plane", func() {
	var (
		workers *scriptedControlWorkers
		adapter *RemoteUnloaderAdapter
	)

	const nodeID = "11111111-2222-3333-4444-555555555555"

	BeforeEach(func() {
		workers = newScriptedControlWorkers()
		adapter = NewRemoteUnloaderAdapter(nil, nil, workers.controlClient(), 3*time.Minute, 15*time.Minute)
	})

	It("treats a worker that answers backend.list as alive", func() {
		workers.scriptReply(controlKey(nodeID, workerctl.PathBackendList), messaging.BackendListReply{})

		Expect(adapter.PingNode(nodeID)).To(Succeed())
	})

	It("treats a worker too old to serve the verb as alive, since it answered", func() {
		// A 404 under the control prefix is the worker saying it is older than
		// this frontend. It is a deployment fact, and a worker that can state
		// one is a worker that is there.
		workers.scriptUnsupported(controlKey(nodeID, workerctl.PathBackendList))

		Expect(adapter.PingNode(nodeID)).To(Succeed())
	})

	It("reports a worker it cannot route to as unroutable, and never as absent", func() {
		workers.scriptUnroutable(nodeID)

		err := adapter.PingNode(nodeID)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
		// The two things a caller must not be able to conclude from it: that
		// the worker spoke, and that it is gone.
		Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
		Expect(errors.Is(err, nats.ErrNoResponders)).To(BeFalse())
	})

	It("refuses without a tunnel dialer rather than reaching for an address", func() {
		plain := NewRemoteUnloaderAdapter(nil, nil, NewControlClient(nil, "tok"), time.Minute, time.Minute)

		err := plain.PingNode(nodeID)
		Expect(err).To(MatchError(ErrNoWorkerDialer))
		Expect(err).To(MatchError(ErrWorkerUnroutable))
	})
})

// The merge gate this task exists to clear, asserted through the SCHEDULING
// path with the real adapter rather than through the router's own double.
//
// router_liveness_test.go drives a test-owned map of dead nodes and never
// touches a transport, so it stayed green for the whole window in which every
// healthy worker read as absent. These specs run pickReachableNode against a
// real RemoteUnloaderAdapter and a worker that answers over its control plane,
// which is the only arrangement that can see the difference.
var _ = Describe("Scheduling onto a worker reached over its control plane", func() {
	var (
		workers *scriptedControlWorkers
		reg     *fakeModelRouter
		router  *SmartRouter
	)

	const nodeID = "22222222-3333-4444-5555-666666666666"

	BeforeEach(func() {
		workers = newScriptedControlWorkers()
		reg = &fakeModelRouter{}
		router = NewSmartRouter(reg, SmartRouterOptions{
			Unloader: NewRemoteUnloaderAdapter(nil, nil, workers.controlClient(), time.Minute, time.Minute),
		})
	})

	selectOnce := func(node *BackendNode) func() *BackendNode {
		handed := false
		return func() *BackendNode {
			if handed {
				return nil
			}
			handed = true
			return node
		}
	}

	It("schedules onto a healthy worker instead of demoting it", func() {
		node := &BackendNode{ID: nodeID, Name: "healthy", Address: "10.0.0.1:50051"}
		workers.scriptReply(controlKey(nodeID, workerctl.PathBackendList), messaging.BackendListReply{})

		Expect(router.pickReachableNode(context.Background(), selectOnce(node))).To(Equal(node))
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})

	It("schedules onto a worker this frontend could not route to, rather than demoting it", func() {
		// A worker re-homing its tunnel between frontend replicas, or one whose
		// owning replica is restarting, is unroutable from here while it is
		// heartbeating and serving. Excluding it costs capacity; demoting it
		// tells every other scheduler the same wrong thing.
		node := &BackendNode{ID: nodeID, Name: "re-homing", Address: "10.0.0.2:50051"}
		workers.scriptUnroutable(nodeID)

		Expect(router.pickReachableNode(context.Background(), selectOnce(node))).To(Equal(node))
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})
})
