package nodes

import (
	"context"
	"errors"
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var _ = Describe("The route report of a worker", func() {
	var (
		ctx context.Context
		reg *NodeRegistry
	)

	register := func(name, address string) *BackendNode {
		GinkgoHelper()
		n := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: address, TokenHash: "h"}
		Expect(reg.Register(ctx, n, true)).To(Succeed())
		return n
	}
	read := func(id string) *BackendNode {
		GinkgoHelper()
		n, err := reg.Get(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		return n
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		reg, err = NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
	})

	It("stores the routes a worker holds and was given, which RoutesOf reads", func() {
		n := register("both", "10.0.0.1:50051")
		Expect(reg.SetRouteReport(ctx, n.ID, RouteReport{
			Attached: []string{RouteNATS, RouteTunnelID},
			Follow:   []string{RouteNATS, RouteTunnelID},
			Tunnel:   TunnelRoute{Transport: TunnelWebSocket},
		})).To(Succeed())

		got := read(n.ID)
		Expect(got.Attached).To(Equal("nats,tunnel"))
		Expect(got.Follow).To(Equal("nats,tunnel"))
		Expect(RoutesOf(got)).To(Equal([]Route{{Kind: RouteDirect}, {Kind: RouteTunnel, Transport: TunnelWebSocket}}))
	})

	It("gives a worker that reports it is attached to none no route", func() {
		n := register("between", "")
		Expect(reg.SetRouteReport(ctx, n.ID, RouteReport{Attached: nil, Follow: []string{RouteTunnelID}})).To(Succeed())
		got := read(n.ID)
		Expect(got.Attached).To(BeEmpty())
		Expect(RoutesOf(got)).To(BeEmpty())
	})

	It("carries the reason a worker gives for a route it does not hold", func() {
		n := register("tunnel-only", "")
		Expect(reg.SetRouteReport(ctx, n.ID, RouteReport{
			Attached: []string{RouteTunnelID}, Follow: []string{RouteNATS, RouteTunnelID},
			FollowError: "nats: connecting",
		})).To(Succeed())
		Expect(read(n.ID).FollowError).To(Equal("nats: connecting"))
	})

	It("refuses a report for a node that is not registered", func() {
		Expect(reg.SetRouteReport(ctx, "ghost", RouteReport{Follow: []string{RouteNATS}})).To(MatchError(gorm.ErrRecordNotFound))
	})

	It("keeps only the names of known routes, once each", func() {
		Expect(parseRoutes([]string{" nats", "tunnel", "nats", "carrier-pigeon"})).To(Equal([]string{RouteNATS, RouteTunnelID}))
		Expect(splitRoutes("nats, carrier-pigeon,tunnel")).To(Equal([]string{RouteNATS, RouteTunnelID}))
		Expect(splitRoutes("")).To(BeNil())
	})
})

var _ = Describe("The direct client factory for a worker that holds a tunnel", func() {
	var (
		ctx     context.Context
		known   map[string]bool
		asked   int
		factory BackendClientFactory
	)

	BeforeEach(func() {
		ctx = context.Background()
		known = map[string]bool{"tunnel-only": false, "routable": true}
		asked = 0
		factory = NewGuardedDirectClientFactory("tok", func(nodeID string) (bool, error) {
			asked++
			has, ok := known[nodeID]
			if !ok {
				return false, errors.New("unknown node")
			}
			return has, nil
		})
	})

	health := func(nodeID, address string) (grpc.Backend, bool, error) {
		c := factory.NewClient(nodeID, address, false)
		hc, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		ok, err := c.HealthCheck(hc)
		return c, ok, err
	}

	It("does not dial the loopback address of a worker that registered no address, and reports a failure of the transport", func() {
		c, ok, err := health("tunnel-only", "127.0.0.1:1")
		Expect(ok).To(BeFalse())
		Expect(err).To(HaveOccurred())
		Expect(grpc.TransportFailureOf(c)).To(HaveOccurred(), "a caller that decides whether a backend is dead must not count this")
		Expect(classifyProbeOutcome(ok, err)).ToNot(Equal(ProbeAlive))
	})

	It("dials the loopback address of a worker that registered an address, as before", func() {
		c, ok, _ := health("routable", "127.0.0.1:1")
		Expect(ok).To(BeFalse())
		Expect(grpc.TransportFailureOf(c)).ToNot(HaveOccurred(), "a refused connection on a direct dial is the answer of the host about the backend")
	})

	It("does not ask the registry for an address that is not on loopback", func() {
		_, _, _ = health("tunnel-only", "10.255.255.1:1")
		Expect(asked).To(BeZero())
	})

	It("dials when the registry cannot say", func() {
		c, _, _ := health("unknown", "127.0.0.1:1")
		Expect(grpc.TransportFailureOf(c)).ToNot(HaveOccurred())
	})

	It("asks the registry once for a burst of clients of one worker", func() {
		for range 5 {
			factory.NewClient("tunnel-only", "127.0.0.1:1", false)
		}
		Expect(asked).To(Equal(1))
	})
})

var _ = Describe("the index of a replica on the wire", func() {
	It("keeps a small index and clamps one that does not fit, instead of wrapping it", func() {
		Expect(replicaIndex32(0)).To(Equal(int32(0)))
		Expect(replicaIndex32(7)).To(Equal(int32(7)))
		Expect(replicaIndex32(math.MaxInt32 + 1)).To(Equal(int32(math.MaxInt32)))
		Expect(replicaIndex32(math.MinInt32 - 1)).To(Equal(int32(math.MinInt32)))
	})
})
