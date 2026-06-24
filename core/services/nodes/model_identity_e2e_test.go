package nodes

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// staleRouteBackend stands in for the backend a recycled port now points at:
// a real, healthy, loaded process that simply holds a DIFFERENT model than the
// controller's cached row believes.
type staleRouteBackend struct {
	base.SingleThread

	served int
}

func (b *staleRouteBackend) Load(*pb.ModelOptions) error { return nil }

func (b *staleRouteBackend) Predict(*pb.PredictOptions) (string, error) {
	b.served++
	return "an answer from the WRONG model", nil
}

// This joins the two halves of the #10952 fix that are unit-tested separately
// elsewhere: the backend rejecting a request whose identity does not match what
// it loaded (pkg/grpc), and the router dropping the stale row when it sees that
// rejection (inflight.go). Neither half is worth much without the other, and
// nothing else exercises them against a real gRPC server together.
var _ = Describe("stale distributed route is caught end to end", func() {
	var (
		tracker *fakeInFlightTracker
		llm     *staleRouteBackend
		tracked *InFlightTrackingClient
	)

	BeforeEach(func() {
		tracker = &fakeInFlightTracker{}
		llm = &staleRouteBackend{}

		addr := "test://stale-route"
		grpc.Provide(addr, llm)
		client := grpc.NewClient(addr, true, nil, false)

		// The worker loaded "model-a" on this port.
		_, err := client.LoadModel(context.Background(), &pb.ModelOptions{Model: "model-a.gguf"})
		Expect(err).ToNot(HaveOccurred())

		tracked = NewInFlightTrackingClient(client, tracker, "node-1", "model-b", 0)
	})

	It("rejects the wrong-model request and drops the stale replica row", func() {
		// The controller's cached row for "model-b" points here because the
		// port was recycled. Liveness alone cannot tell, so the identity does.
		_, err := tracked.Predict(context.Background(), &pb.PredictOptions{
			Prompt:        "hello",
			ModelIdentity: "model-b.gguf",
		})

		Expect(err).To(HaveOccurred())
		Expect(grpcerrors.IsModelMismatch(err)).To(BeTrue(), "got %v", err)
		Expect(llm.served).To(Equal(0), "the wrong model must never answer")
		Expect(tracker.removed).To(Equal(1), "the stale row must be dropped so the next request reloads")
	})

	It("serves and keeps the row when the route is correct", func() {
		_, err := tracked.Predict(context.Background(), &pb.PredictOptions{
			Prompt:        "hello",
			ModelIdentity: "model-a.gguf",
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(llm.served).To(Equal(1))
		Expect(tracker.removed).To(Equal(0))
	})

	// Every deployment that has not upgraded its controller sends no identity.
	It("serves and keeps the row when the controller sends no identity", func() {
		_, err := tracked.Predict(context.Background(), &pb.PredictOptions{Prompt: "hello"})

		Expect(err).ToNot(HaveOccurred())
		Expect(llm.served).To(Equal(1))
		Expect(tracker.removed).To(Equal(0))
	})
})
