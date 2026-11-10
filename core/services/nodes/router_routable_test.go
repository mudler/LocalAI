package nodes

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// After a change of carrier a worker may hold none of the active carrier and be
// unable to follow. A frontend dials a replica by an address, and the backend of
// such a worker listens on its own loopback address only. The router must not
// dial it: the refused connection is a failed request, not an answer about the
// backend.
var _ = Describe("Routing after the carrier changed", func() {
	var (
		reg    *fakeModelRouter
		active cluster.Carrier
		router *SmartRouter
		dual   BackendNode
		tunnel BackendNode
		legacy BackendNode
	)

	BeforeEach(func() {
		dual = BackendNode{ID: "dual", Name: "dual", Address: "10.0.0.1:50051", Status: StatusHealthy,
			Attached: "nats", Follow: "nats,tunnel"}
		tunnel = BackendNode{ID: "tunnel", Name: "tunnel", Status: StatusHealthy,
			Attached: "tunnel", Follow: "tunnel", FollowError: "no routable address: it cannot follow a change to NATS"}
		legacy = BackendNode{ID: "legacy", Name: "legacy", Address: "10.0.0.3:50051", Status: StatusHealthy}
		active = cluster.CarrierNATS
		reg = &fakeModelRouter{}
		router = NewSmartRouter(reg, SmartRouterOptions{
			ClientFactory: &stubClientFactory{client: &stubBackend{healthResult: true}},
			ActiveCarrier: func() cluster.Carrier { return active },
		})
	})

	route := func() (*RouteResult, error) {
		return router.Route(context.Background(), "m", "models/m.gguf", "llama-cpp", "", nil, false)
	}

	DescribeTable("says which nodes can be reached on the active carrier",
		func(n *BackendNode, on cluster.Carrier, want bool) {
			Expect(NodeRoutableOn(n, on)).To(Equal(want))
		},
		Entry("a worker attached to the active carrier", &BackendNode{Attached: "nats", Follow: "nats,tunnel"}, cluster.CarrierNATS, true),
		Entry("a worker that predates carrier switching, on NATS", &BackendNode{}, cluster.CarrierNATS, true),
		Entry("a worker that predates carrier switching, on the tunnel", &BackendNode{}, cluster.CarrierTunnel, false),
		Entry("a worker that is between carriers and can follow", &BackendNode{Follow: "nats,tunnel"}, cluster.CarrierTunnel, true),
		Entry("a tunnel-only worker that could not follow NATS", &BackendNode{Attached: "tunnel", Follow: "tunnel", FollowError: "no address"}, cluster.CarrierNATS, false),
		Entry("a worker that cannot follow and reports why", &BackendNode{Attached: "nats", Follow: "nats,tunnel", FollowError: "tunnel refused"}, cluster.CarrierTunnel, false),
	)

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
		reg.findBySelectorNodes = []BackendNode{tunnel, dual}
		reg.findAndLockErr = errors.New("not loaded")
		reg.findIdleFromSetNode = &dual
		reg.findVRAMFromSetNode = &dual
		_, _ = route()
		Expect(reg.findAndLockCandidates).ToNot(BeEmpty())
		Expect(reg.findAndLockCandidates[0]).To(Equal([]string{"dual"}))
	})

	It("does not load a model onto a node that cannot be reached", func() {
		reg.findBySelectorNodes = []BackendNode{tunnel, dual}
		reg.findAndLockErr = errors.New("not loaded")
		reg.findIdleFromSetNode = &dual
		reg.findVRAMFromSetNode = &dual
		reg.findLeastLoadedFromSetNode = &dual
		_, _ = route()
		Expect(reg.idleFromSetCandidates).ToNot(BeEmpty())
		for _, ids := range reg.idleFromSetCandidates {
			Expect(ids).ToNot(ContainElement("tunnel"))
		}
	})

	It("answers with a clear 'no available nodes' and keeps every row when no node can be reached", func() {
		reg.findBySelectorNodes = []BackendNode{tunnel}
		_, err := route()
		Expect(err).To(MatchError(ErrNoAvailableNodes))
		Expect(err.Error()).To(ContainSubstring("nats"))
		Expect(err.Error()).To(ContainSubstring("can be reached on the nats carrier"))
		Expect(reg.findAndLockCandidates).To(BeEmpty(), "no replica was picked, so none was dialled")
		Expect(reg.removeCalls).To(BeEmpty(), "an unreachable node loses no row")
		Expect(reg.decrementCalls).To(BeEmpty())
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})

	It("follows the active carrier when it changes", func() {
		reg.findBySelectorNodes = []BackendNode{tunnel, legacy}
		active = cluster.CarrierTunnel
		reg.findAndLockErr = errors.New("not loaded")
		reg.findIdleFromSetNode = &tunnel
		_, _ = route()
		Expect(reg.findAndLockCandidates[0]).To(Equal([]string{"tunnel"}), "the legacy worker has no tunnel")
	})

	It("does nothing when no carrier is given, as before", func() {
		router = NewSmartRouter(reg, SmartRouterOptions{ClientFactory: &stubClientFactory{client: &stubBackend{healthResult: true}}})
		reg.findBySelectorNodes = []BackendNode{tunnel}
		reg.findAndLockErr = errors.New("not loaded")
		reg.findIdleNode = &tunnel
		_, _ = route()
		Expect(reg.findAndLockCandidates).ToNot(BeEmpty())
		for _, c := range reg.findAndLockCandidates {
			Expect(c).To(BeNil())
		}
	})
})

var _ grpc.Backend = (*stubBackend)(nil)
var _ = pb.ModelOptions{}
