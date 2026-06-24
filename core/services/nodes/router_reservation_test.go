package nodes

import (
	"context"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// Routing reserves in_flight = 1 at load time so a freshly loaded replica is not
// immediately evicted out from under the request that caused the load. That
// reservation used to be released ONLY by the first inference completing, so a
// route torn down before any inference ran (client disconnect, handler error
// before the backend call) stranded the counter. A stranded counter is not
// cosmetic: every eviction query requires in_flight = 0, so the replica's VRAM
// becomes unreclaimable.
var _ = Describe("SmartRouter routing reservation", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		router   *SmartRouter
		node     *BackendNode
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		router = &SmartRouter{registry: registry}
		node = &BackendNode{Name: "n1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
		// A loaded replica holding the load-time reservation.
		Expect(registry.SetNodeModel(context.Background(), node.ID, "m", 0, "loaded", "10.0.0.1:12345", 1)).To(Succeed())
	})

	inFlight := func() int {
		var m NodeModel
		Expect(db.First(&m, "node_id = ? AND model_name = ?", node.ID, "m").Error).To(Succeed())
		return m.InFlight
	}

	newResult := func() *RouteResult {
		raw := &stubBackend{}
		tracked := NewInFlightTrackingClient(raw, registry, node.ID, "m", 0)
		return router.newRouteResult(node, "m", 0, raw, tracked)
	}

	It("releases the reservation when the route is torn down without any inference", func() {
		result := newResult()
		Expect(inFlight()).To(Equal(1))

		result.Release()

		Expect(inFlight()).To(Equal(0),
			"a route that never ran an inference must still give the reservation back")
	})

	It("releases the reservation exactly once across both paths", func() {
		result := newResult()
		tracked, ok := result.Client.(*InFlightTrackingClient)
		Expect(ok).To(BeTrue())

		// Simulate the first inference completing, then the route being torn
		// down. Only one of these may consume the single reservation.
		tracked.firstOnce.Do(tracked.onFirstComplete)
		result.Release()
		result.Release()

		Expect(inFlight()).To(Equal(0),
			"double release would under-count and let a busy replica look idle")
	})

	It("does not disturb concurrent per-request tracking", func() {
		result := newResult()
		// A second request arrives and is tracked normally.
		Expect(registry.IncrementInFlight(context.Background(), node.ID, "m", 0)).To(Succeed())
		Expect(inFlight()).To(Equal(2))

		result.Release()

		Expect(inFlight()).To(Equal(1),
			"releasing the reservation must leave a genuinely in-flight request counted")
	})
})
