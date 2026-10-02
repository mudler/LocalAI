package nodes

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
)

// The miss map counts consecutive failed probes per (node, model, replica). It
// is the only per-node state in this process that grows on model churn rather
// than on fleet size, so a deployment that loads and unloads models for months
// accumulates one integer per tuple it ever probed and never gives one back.
//
// The bound is the pass itself: a streak survives only while the pass can still
// see the row it counts against. Every spec below is one way a row stops being
// visible, and the last two are the two ways a row must NOT be forgotten.
var _ = Describe("HealthMonitor miss-streak bounds", func() {
	const staleThreshold = 30 * time.Second

	var (
		ctx     context.Context
		store   *fakeNodeHealthStore
		factory *fakeBackendClientFactory
		hm      *HealthMonitor
	)

	// deadModel is one model row whose backend answers every probe with a
	// failure, so a single pass leaves a streak of exactly one.
	deadModel := func(nodeID, modelName, addr string) NodeModel {
		factory.setClient(addr, &fakeBackendClient{healthy: false, err: fmt.Errorf("connection refused")})
		return NodeModel{NodeID: nodeID, ModelName: modelName, WorkerLocalAddress: addr}
	}

	BeforeEach(func() {
		ctx = context.Background()
		store = newFakeNodeHealthStore()
		factory = newFakeBackendClientFactory()
		hm = newTestHealthMonitor(store, factory, true, staleThreshold)
		hm.perModelHealthCheck = true
	})

	It("forgets the streak of a model row that is no longer registered", func() {
		// An unload, a scale-down or an LRU eviction removes the row without
		// telling this monitor. Nothing else in the loop ever revisits the key,
		// so the counter is stranded for the life of the process.
		store.addNode(makeTestNode("node-a", "worker-a", "10.0.0.1:50051", StatusHealthy, freshTime()))
		store.setNodeModels("node-a", deadModel("node-a", "m", "10.0.0.1:50053"))

		hm.doCheckAll(ctx)
		Expect(hm.missCount("node-a", "m", 0)).To(Equal(1))

		store.setNodeModels("node-a")
		hm.doCheckAll(ctx)

		Expect(hm.trackedMisses()).To(BeZero())
	})

	It("forgets the streaks of a node whose heartbeat went stale", func() {
		// The pass skips the node entirely from this branch, so every streak it
		// held is unreachable from here on. autoOffline deletes its rows too,
		// which is what makes the counters count against nothing.
		store.addNode(makeTestNode("node-b", "worker-b", "10.0.0.2:50051", StatusHealthy, freshTime()))
		store.setNodeModels("node-b", deadModel("node-b", "m", "10.0.0.2:50053"))

		hm.doCheckAll(ctx)
		Expect(hm.missCount("node-b", "m", 0)).To(Equal(1))

		store.setHeartbeat("node-b", staleTime(staleThreshold))
		hm.doCheckAll(ctx)

		Expect(hm.trackedMisses()).To(BeZero())
	})

	It("forgets the streaks of a node the operator set draining", func() {
		store.addNode(makeTestNode("node-c", "worker-c", "10.0.0.3:50051", StatusHealthy, freshTime()))
		store.setNodeModels("node-c", deadModel("node-c", "m", "10.0.0.3:50053"))

		hm.doCheckAll(ctx)
		Expect(hm.missCount("node-c", "m", 0)).To(Equal(1))

		store.getNode("node-c").Status = StatusDraining
		hm.doCheckAll(ctx)

		Expect(hm.trackedMisses()).To(BeZero())
	})

	It("forgets the streaks of a node whose tunnel departed", func() {
		// The departure branch skips the probes deliberately: there is no route
		// to dial. It also announces the departure, and every other per-node
		// cache is dropped from that announcement. This one is not, because a
		// departure is only one of the four ways a row stops being visible.
		store.addNode(makeTestNode("node-d", "worker-d", "10.0.0.4:50051", StatusHealthy, freshTime()))
		store.setNodeModels("node-d", deadModel("node-d", "m", "10.0.0.4:50053"))

		hm.doCheckAll(ctx)
		Expect(hm.missCount("node-d", "m", 0)).To(Equal(1))

		hm.presence = &stubPresence{answer: cluster.PresenceGone}
		hm.reconnectGrace = time.Minute
		hm.departures = NewDepartureNotifier()
		hm.doCheckAll(ctx)

		Expect(hm.trackedMisses()).To(BeZero())
	})

	It("keeps the streak of a row the pass could not probe", func() {
		// An unreachable worker is not evidence about its backends. The streak
		// is neither advanced nor cleared, so a row that was two misses from
		// removal before the peer link blipped is still two misses from removal
		// after it. A prune that counted only PROBED rows would forgive it.
		store.addNode(makeTestNode("node-e", "worker-e", "10.0.0.5:50051", StatusHealthy, freshTime()))
		store.setNodeModels("node-e", deadModel("node-e", "m", "10.0.0.5:50053"))

		hm.doCheckAll(ctx)
		Expect(hm.missCount("node-e", "m", 0)).To(Equal(1))

		factory.refuseForNode = fmt.Errorf("no tunnel for you")
		hm.doCheckAll(ctx)

		Expect(hm.missCount("node-e", "m", 0)).To(Equal(1))
	})

	It("keeps every streak when the pass could not read the fleet", func() {
		// A pass that failed to list nodes observed nothing at all, and pruning
		// against nothing would clear every streak in the deployment on one
		// database hiccup.
		store.addNode(makeTestNode("node-f", "worker-f", "10.0.0.6:50051", StatusHealthy, freshTime()))
		store.setNodeModels("node-f", deadModel("node-f", "m", "10.0.0.6:50053"))

		hm.doCheckAll(ctx)
		Expect(hm.missCount("node-f", "m", 0)).To(Equal(1))

		hm.registry = &listFailsStore{NodeHealthStore: store, err: fmt.Errorf("connection reset")}
		hm.doCheckAll(ctx)

		Expect(hm.missCount("node-f", "m", 0)).To(Equal(1))
	})
})

// listFailsStore is the fake store with its fleet listing broken, which a real
// registry cannot be made to do on demand.
type listFailsStore struct {
	NodeHealthStore
	err error
}

func (s *listFailsStore) List(context.Context) ([]BackendNode, error) { return nil, s.err }
