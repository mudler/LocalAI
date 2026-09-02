package nodes

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// stubPresence answers the scheduler's presence question without a database.
//
// It exists to NAME each of the four values, so a widening of the exclusion is
// attributed to the value that caused it. It cannot see a carrier change, which
// is why the real-registry specs at the bottom of this file exist beside it:
// the previous version of this suite drove a test-owned map of "dead nodes" and
// stayed green for the whole window in which every healthy worker read as
// absent.
type stubPresence struct {
	answer    cluster.Presence
	answerFor map[string]cluster.Presence
	err       error

	nodes  []string
	graces []time.Duration
}

func (s *stubPresence) Presence(_ context.Context, nodeID string, grace time.Duration) (cluster.Presence, error) {
	s.nodes = append(s.nodes, nodeID)
	s.graces = append(s.graces, grace)
	if s.err != nil {
		// PresenceUnknown alongside the error, the way the real registry
		// answers: a value nobody may act on rather than one that reads as a
		// verdict.
		return cluster.PresenceUnknown, s.err
	}
	if p, ok := s.answerFor[nodeID]; ok {
		return p, nil
	}
	return s.answer, nil
}

// selectorReturning hands back each node in turn, mimicking a scheduler that
// re-picks after the previous choice was demoted.
func selectorReturning(nodes ...*BackendNode) func() *BackendNode {
	i := 0
	return func() *BackendNode {
		if i >= len(nodes) {
			return nil
		}
		n := nodes[i]
		i++
		return n
	}
}

var _ = Describe("Scheduling and node presence", func() {
	var (
		reg      *fakeModelRouter
		presence *stubPresence
		router   *SmartRouter
		ctx      context.Context
	)

	// The grace every spec measures against. It is the operator's trade, so it
	// is also what the scheduler must hand the registry: a scheduler that asked
	// with a constant of its own would ignore --worker-reconnect-grace entirely
	// while every spec below that only checks a Presence VALUE stayed green.
	const grace = 60 * time.Second

	newNode := func(id string) *BackendNode {
		return &BackendNode{ID: id, Name: id, Address: id + ":50051"}
	}

	BeforeEach(func() {
		ctx = context.Background()
		reg = &fakeModelRouter{}
		presence = &stubPresence{answer: cluster.PresenceConnected}
		router = NewSmartRouter(reg, SmartRouterOptions{Presence: presence, ReconnectGrace: grace})
	})

	DescribeTable("decides whether a node may be given work",
		func(p cluster.Presence, eligible bool) {
			presence.answer = p
			Expect(router.nodeMayTakeWork(ctx, newNode("node-1"))).To(Equal(eligible))
		},
		Entry("connected: eligible", cluster.PresenceConnected, true),
		// The catastrophe case. A worker re-homing between replicas, or a
		// replica that just died holding it, must NOT be excluded: excluding it
		// costs capacity, and marking it unhealthy is what turns a rolling
		// frontend restart into a fleet-wide eviction.
		Entry("reconnecting: still eligible", cluster.PresenceReconnecting, true),
		// A node with no connection row has never dialled or its departure aged
		// out. Presence cannot say which, so scheduling does not exclude it and
		// the install that follows reports its own failure.
		Entry("unknown: still eligible", cluster.PresenceUnknown, true),
		Entry("gone: not eligible", cluster.PresenceGone, false),
	)

	It("asks with the operator's configured grace rather than a constant of its own", func() {
		router.nodeMayTakeWork(ctx, newNode("node-1"))

		Expect(presence.graces).To(Equal([]time.Duration{grace}))
	})

	It("does not mark a reconnecting node unhealthy", func() {
		node := newNode("re-homing")
		presence.answer = cluster.PresenceReconnecting

		Expect(router.pickReachableNode(ctx, selectorReturning(node))).To(Equal(node))
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})

	It("marks a gone node unhealthy exactly once and reschedules", func() {
		gone, live := newNode("gone-node"), newNode("live-node")
		presence.answerFor = map[string]cluster.Presence{
			gone.ID: cluster.PresenceGone,
			live.ID: cluster.PresenceConnected,
		}

		picked := router.pickReachableNode(ctx, selectorReturning(gone, live))

		Expect(picked).To(Equal(live))
		Expect(reg.markedUnhealthy).To(Equal([]string{gone.ID}))
		Expect(presence.nodes).To(Equal([]string{gone.ID, live.ID}))
	})

	It("treats a presence query FAILURE as eligible, never as absence", func() {
		// A database hiccup must not evict the fleet. The error carries
		// PresenceUnknown, so a read that acted on the value while ignoring the
		// error would still be eligible here; what separates the two is that
		// nothing may be concluded, which the demotion assertion covers.
		node := newNode("only-node")
		presence.err = errors.New("connection reset")

		Expect(router.nodeMayTakeWork(ctx, node)).To(BeTrue())
		Expect(router.pickReachableNode(ctx, selectorReturning(node))).To(Equal(node))
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})

	It("treats every node as eligible when no presence reader is configured", func() {
		// A single-node deployment has no cluster registry to ask, and nothing
		// there has a tunnel to lose.
		plain := NewSmartRouter(reg, SmartRouterOptions{})
		node := newNode("only-node")

		Expect(plain.pickReachableNode(ctx, selectorReturning(node))).To(Equal(node))
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})

	It("takes the first eligible node without asking about the rest", func() {
		first, second := newNode("first"), newNode("second")

		Expect(router.pickReachableNode(ctx, selectorReturning(first, second))).To(Equal(first))
		Expect(presence.nodes).To(Equal([]string{"first"}))
	})

	It("gives up rather than spinning when every node is gone", func() {
		presence.answer = cluster.PresenceGone
		a, b, c, d := newNode("a"), newNode("b"), newNode("c"), newNode("d")

		picked := router.pickReachableNode(ctx, selectorReturning(a, b, c, d))

		Expect(picked).To(BeNil())
		Expect(len(presence.nodes)).To(BeNumerically("<=", maxNodeLivenessRetries))
	})

	It("stops when the demotion itself fails, so it cannot loop on one node", func() {
		presence.answer = cluster.PresenceGone
		dead := newNode("dead-node")
		reg.markUnhealthyErr = errors.New("database is down")

		picked := router.pickReachableNode(ctx, selectorReturning(dead, dead, dead))

		Expect(picked).To(BeNil())
		Expect(presence.nodes).To(Equal([]string{"dead-node"}))
	})
})

// The same decision, against the registry that answers it in production, on the
// database clock.
//
// The stub above can only prove that the scheduler acts correctly on a value it
// was handed. These prove that the value it is handed is the one the deployment
// holds: that Presence is actually consulted, that the operator's grace reaches
// it, and that a departure inside the grace and a departure past it are told
// apart by the DATABASE rather than by this process.
var _ = Describe("Scheduling against the cluster registry that answers presence", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		clusterR *cluster.Registry
		reg      *fakeModelRouter
		router   *SmartRouter
	)

	const (
		grace    = 60 * time.Second
		instance = "inst-a"
	)

	// ageDeparture pushes a worker's departure into the past ON THE DATABASE
	// CLOCK. A Go-side time.Now().Add(-d) would age the row against this
	// process's clock and then compare it against the database's, which is the
	// skew the whole window is written to be immune to.
	ageDeparture := func(nodeID string, by time.Duration) {
		GinkgoHelper()
		Expect(db.WithContext(ctx).Exec(
			`UPDATE node_connections SET disconnected_at = now() - make_interval(secs => ?) WHERE node_id = ?`,
			by.Seconds(), nodeID).Error).To(Succeed())
	}

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, instance, "10.0.0.1:8080", "v1")).To(Succeed())
		reg = &fakeModelRouter{}
		router = NewSmartRouter(reg, SmartRouterOptions{Presence: clusterR, ReconnectGrace: grace})
	})

	node := func(id string) *BackendNode {
		return &BackendNode{ID: id, Name: id, Address: id + ":50051"}
	}

	It("schedules onto a worker whose tunnel a live replica holds", func() {
		_, err := clusterR.Claim(ctx, "worker-connected", instance)
		Expect(err).ToNot(HaveOccurred())

		n := node("worker-connected")
		Expect(router.pickReachableNode(ctx, selectorReturning(n))).To(Equal(n))
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})

	It("schedules onto a worker whose tunnel was lost inside the grace, without demoting it", func() {
		// The rolling-restart case in its real form: the row records a
		// departure, and the worker is at this moment re-dialling the load
		// balancer.
		epoch, err := clusterR.Claim(ctx, "worker-rehoming", instance)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterR.Release(ctx, "worker-rehoming", instance, epoch)).To(Succeed())
		ageDeparture("worker-rehoming", grace/2)

		n := node("worker-rehoming")
		Expect(router.pickReachableNode(ctx, selectorReturning(n))).To(Equal(n))
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})

	It("excludes and demotes a worker whose departure has outlived the grace", func() {
		epoch, err := clusterR.Claim(ctx, "worker-gone", instance)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterR.Release(ctx, "worker-gone", instance, epoch)).To(Succeed())
		// Just past the edge, not an order of magnitude past it: a window only
		// ever aged ten times its own width is a window nothing pins.
		ageDeparture("worker-gone", grace+5*time.Second)

		_, err = clusterR.Claim(ctx, "worker-live", instance)
		Expect(err).ToNot(HaveOccurred())
		gone, live := node("worker-gone"), node("worker-live")

		Expect(router.pickReachableNode(ctx, selectorReturning(gone, live))).To(Equal(live))
		Expect(reg.markedUnhealthy).To(Equal([]string{"worker-gone"}))
	})

	It("schedules onto a worker that has never dialled a tunnel", func() {
		// No connection row at all. The registry cannot tell a worker still
		// starting up from one whose departure aged out of retention, so it
		// answers unknown and the scheduler places work; the install that
		// follows reports its own failure if the worker really is not there.
		n := node("worker-never-seen")
		Expect(router.pickReachableNode(ctx, selectorReturning(n))).To(Equal(n))
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})
})
