package nodes

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A replica row carries the address of the backend process it names, and that
// address is how the frontend says WHICH process on a worker it means. A row
// without one names nothing, and the warm path has to decline it rather than
// route with an empty target.
//
// This is defensive: installBackendOnNode now guarantees a non-empty address
// before any row is written, so the only rows that can look like this are ones
// an older frontend wrote. It is specced anyway because the branch touches an
// in-flight reservation, and a reservation that is taken and not returned pins
// the replica against every eviction query for the life of the row.
var _ = Describe("SmartRouter warm path with an unnamed replica", func() {
	var (
		registry *fakeModelRouterForSmartRouter
		clients  *fakeBackendClientFactory
		router   *SmartRouter
		node     *BackendNode
	)

	BeforeEach(func() {
		node = &BackendNode{ID: "node-1", Name: "node-1", Status: StatusHealthy}
		registry = newFakeModelRouterForSmartRouter()
		registry.node = node
		clients = newFakeBackendClientFactory()
		router = NewSmartRouter(registry, SmartRouterOptions{ClientFactory: clients})
	})

	warm := func() *RouteResult {
		return router.tryWarmPath(context.Background(), &routeAttempt{trackingKey: "m", modelName: "m"})
	}

	Context("when the row names no backend process", func() {
		BeforeEach(func() {
			registry.nodeModel = &NodeModel{NodeID: node.ID, ModelName: "m", ReplicaIndex: 0}
		})

		It("declines the warm path so the caller cold-loads", func() {
			Expect(warm()).To(BeNil())
		})

		It("never asks for a client, so the empty target reaches no dialler", func() {
			// The failure this prevents: an empty target opens a stream the
			// worker refuses as an invalid request. That refusal is an answer
			// FROM the worker, so it is the one failure on the whole path that
			// is real evidence about a backend, and this row is not entitled to
			// produce evidence about anything.
			Expect(warm()).To(BeNil())
			Expect(clients.nodesSeen()).To(BeEmpty())
			Expect(clients.addressesSeen()).To(BeEmpty())
		})

		It("returns the reservation FindAndLockNodeWithModel took", func() {
			// Held rather than returned, the row's in_flight never reaches 0
			// and no eviction query can ever select it, so the replica slot and
			// its VRAM are pinned for the life of the row.
			Expect(warm()).To(BeNil())
			registry.mu.Lock()
			defer registry.mu.Unlock()
			Expect(registry.decrementCalled).To(HaveKeyWithValue("node-1:m", 1))
		})

		It("leaves the row in place", func() {
			// Deliberately unlike the sibling !alive branch, which removes the
			// row. A dead backend has been observed dead; this row has been
			// observed to be unreadable, which says nothing about whether a
			// process is running on that worker. It is also the last record
			// that one may be: the acknowledged stop path matches on
			// ExpectedAddress, so a stop for an empty one is refused by the
			// worker, and deleting the row here would free the replica slot for
			// a second copy of the same model while the first one, if it
			// exists, keeps its VRAM. The cost of keeping it is one lock and
			// decrement per request before the cold load, and a row an operator
			// can see; the cost of removing it is an orphan nothing points at.
			Expect(warm()).To(BeNil())
			Expect(registry.removedModels()).To(BeEmpty())
		})
	})

	It("routes normally once the row names one", func() {
		// The control. Without it every assertion above would also pass on a
		// warm path that declined everything.
		registry.nodeModel = &NodeModel{NodeID: node.ID, ModelName: "m", ReplicaIndex: 0, WorkerLocalAddress: "127.0.0.1:50052"}
		Expect(warm()).ToNot(BeNil())
		Expect(clients.addressesSeen()).To(ContainElement("127.0.0.1:50052"))
		registry.mu.Lock()
		defer registry.mu.Unlock()
		Expect(registry.decrementCalled).ToNot(HaveKey("node-1:m"))
	})
})
