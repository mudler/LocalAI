package nodes

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// The cluster is hybrid: each worker attaches the way its operator chose, and the
// router asks the node for its routes (RoutesOf), never the cluster for a carrier.
// A frontend dials a replica by an address, and the backend of a worker with no
// route listens on its own loopback address only, so the router must not dial it:
// the refused connection is a failed request, not an answer about the backend.
var _ = Describe("Routing by the routes of a node", func() {
	var (
		reg    *fakeModelRouter
		router *SmartRouter
		dual   BackendNode
		tunnel BackendNode
		legacy BackendNode
		p2p    BackendNode
		p2pOff BackendNode
	)

	BeforeEach(func() {
		dual = BackendNode{ID: "dual", Name: "dual", Address: "10.0.0.1:50051", Status: StatusHealthy,
			Attached: "nats", Follow: "nats,tunnel"}
		tunnel = BackendNode{ID: "tunnel", Name: "tunnel", Status: StatusHealthy,
			Attached: "tunnel", Follow: "tunnel", FollowError: "nats: connecting",
			TunnelTransport: TunnelWebSocket}
		legacy = BackendNode{ID: "legacy", Name: "legacy", Address: "10.0.0.3:50051", Status: StatusHealthy}
		p2p = BackendNode{ID: "p2p", Name: "p2p", Status: StatusHealthy, PeerID: "peer-a",
			Attached: "tunnel", Follow: "tunnel", TunnelTransport: TunnelP2P, TunnelPath: "relayed", TunnelLimited: true}
		p2pOff = BackendNode{ID: "p2p-off", Name: "p2p-off", Status: StatusHealthy, PeerID: "peer-b",
			Follow: "tunnel", TunnelTransport: TunnelP2P}
		reg = &fakeModelRouter{}
		router = NewSmartRouter(reg, SmartRouterOptions{
			ClientFactory: &stubClientFactory{client: &stubBackend{healthResult: true}},
		})
	})

	route := func() (*RouteResult, error) {
		return router.Route(context.Background(), "m", "models/m.gguf", "llama-cpp", "", nil, false)
	}

	DescribeTable("lists the routes of a node",
		func(n *BackendNode, want []Route) {
			Expect(RoutesOf(n)).To(Equal(want))
		},
		Entry("a worker on NATS with an address", &BackendNode{Address: "h:1", Attached: "nats", Follow: "nats,tunnel"},
			[]Route{{Kind: RouteDirect}}),
		Entry("a worker that predates carrier switching", &BackendNode{Address: "h:1"}, []Route{{Kind: RouteDirect}}),
		Entry("a worker on NATS with no address", &BackendNode{Attached: "nats", Follow: "nats"}, nil),
		Entry("an agent worker on NATS, which needs no address", &BackendNode{NodeType: NodeTypeAgent, Attached: "nats", Follow: "nats"},
			[]Route{{Kind: RouteDirect}}),
		Entry("an agent worker on the tunnel", &BackendNode{NodeType: NodeTypeAgent, Attached: "tunnel", Follow: "tunnel", TunnelTransport: TunnelWebSocket},
			[]Route{{Kind: RouteTunnel, Transport: TunnelWebSocket}}),
		Entry("a worker that is attached to nothing", &BackendNode{Address: "h:1", Follow: "nats,tunnel"}, nil),
		Entry("a websocket tunnel worker", &BackendNode{Attached: "tunnel", Follow: "tunnel", TunnelTransport: TunnelWebSocket},
			[]Route{{Kind: RouteTunnel, Transport: TunnelWebSocket}}),
		Entry("a P2P worker on a relayed path", &BackendNode{PeerID: "p", Address: "h:1", Attached: "tunnel", Follow: "tunnel",
			TunnelTransport: TunnelP2P, TunnelPath: "relayed", TunnelLimited: true},
			[]Route{{Kind: RouteTunnel, Transport: TunnelP2P, Path: "relayed", Limited: true}}),
		Entry("a P2P worker never has a direct route", &BackendNode{PeerID: "p", Address: "h:1", Attached: "nats,tunnel", Follow: "nats,tunnel",
			TunnelTransport: TunnelP2P, TunnelPath: "direct"},
			[]Route{{Kind: RouteTunnel, Transport: TunnelP2P, Path: "direct"}}),
		Entry("a P2P worker with no live tunnel", &BackendNode{PeerID: "p", Follow: "tunnel"}, nil),
		Entry("a worker that holds both", &BackendNode{Address: "h:1", Attached: "nats,tunnel", Follow: "nats,tunnel", TunnelTransport: TunnelWebSocket},
			[]Route{{Kind: RouteDirect}, {Kind: RouteTunnel, Transport: TunnelWebSocket}}),
	)

	It("schedules a NATS worker, a websocket tunnel worker and a P2P worker at once", func() {
		reg.findBySelectorNodes = []BackendNode{dual, tunnel, p2p}
		reg.findAndLockErr = errors.New("not loaded")
		reg.findIdleNode = &dual
		_, _ = route()
		Expect(reg.findAndLockCandidates).ToNot(BeEmpty())
		for _, c := range reg.findAndLockCandidates {
			Expect(c).To(BeNil(), "no node is skipped, so the set stays 'any healthy node'")
		}
	})

	It("keeps a node with a limited relay schedulable", func() {
		reg.findBySelectorNodes = []BackendNode{p2p, p2pOff}
		reg.findAndLockErr = errors.New("not loaded")
		reg.findIdleFromSetNode = &p2p
		_, _ = route()
		Expect(reg.findAndLockCandidates[0]).To(Equal([]string{"p2p"}))
	})

	It("leaves out a P2P worker that has no live tunnel", func() {
		reg.findBySelectorNodes = []BackendNode{p2pOff, dual, tunnel}
		reg.findAndLockErr = errors.New("not loaded")
		reg.findIdleFromSetNode = &dual
		_, _ = route()
		Expect(reg.findAndLockCandidates[0]).To(ConsistOf("dual", "tunnel"))
	})

	It("leaves the candidates alone when every node can be reached", func() {
		reg.findBySelectorNodes = []BackendNode{dual, legacy}
		reg.findAndLockErr = errors.New("not loaded")
		reg.findIdleNode = &dual
		_, _ = route()
		Expect(reg.findAndLockCandidates).ToNot(BeEmpty())
		for _, c := range reg.findAndLockCandidates {
			Expect(c).To(BeNil(), "no node is skipped, so the set stays 'any healthy node'")
		}
	})

	It("does not look for a loaded replica on a node that cannot be reached", func() {
		reg.findBySelectorNodes = []BackendNode{p2pOff, dual}
		reg.findAndLockErr = errors.New("not loaded")
		reg.findIdleFromSetNode = &dual
		reg.findVRAMFromSetNode = &dual
		_, _ = route()
		Expect(reg.findAndLockCandidates).ToNot(BeEmpty())
		Expect(reg.findAndLockCandidates[0]).To(Equal([]string{"dual"}))
	})

	It("does not load a model onto a node that cannot be reached", func() {
		reg.findBySelectorNodes = []BackendNode{p2pOff, dual}
		reg.findAndLockErr = errors.New("not loaded")
		reg.findIdleFromSetNode = &dual
		reg.findVRAMFromSetNode = &dual
		reg.findLeastLoadedFromSetNode = &dual
		_, _ = route()
		Expect(reg.idleFromSetCandidates).ToNot(BeEmpty())
		for _, ids := range reg.idleFromSetCandidates {
			Expect(ids).ToNot(ContainElement("p2p-off"))
		}
	})

	It("answers with a clear 'no available nodes' and keeps every row when no node can be reached", func() {
		reg.findBySelectorNodes = []BackendNode{p2pOff}
		_, err := route()
		Expect(err).To(MatchError(ErrNoAvailableNodes))
		Expect(err.Error()).To(ContainSubstring("has a usable route"))
		Expect(reg.findAndLockCandidates).To(BeEmpty(), "no replica was picked, so none was dialled")
		Expect(reg.removeCalls).To(BeEmpty(), "an unreachable node loses no row")
		Expect(reg.decrementCalls).To(BeEmpty())
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})

})

var _ grpc.Backend = (*stubBackend)(nil)
var _ = pb.ModelOptions{}
