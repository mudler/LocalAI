// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type multimodalTrackedBackend struct {
	grpcpkg.Backend
	err error
}

func (b *multimodalTrackedBackend) MultimodalEmbedding(ctx context.Context, in *pb.MultimodalEmbeddingRequest, _ ...ggrpc.CallOption) (*pb.MultimodalEmbeddingResponse, error) {
	return nil, b.err
}

func (b *multimodalTrackedBackend) MultimodalRerank(ctx context.Context, in *pb.MultimodalRerankRequest, _ ...ggrpc.CallOption) (*pb.MultimodalRerankResponse, error) {
	return nil, b.err
}

var _ = Describe("Multimodal routing", func() {
	It("distributed typed rerank is tracked and preserves worker-owned refusals", func() {
		for _, err := range []error{status.Error(codes.Unimplemented, "old"), status.Error(codes.ResourceExhausted, "budget")} {
			tracker := &fakeInFlightTracker{}
			client := NewInFlightTrackingClient(&multimodalTrackedBackend{err: err}, tracker, "node", "same", 2)
			_, got := client.MultimodalRerank(context.Background(), &pb.MultimodalRerankRequest{})
			Expect(got).To(BeIdenticalTo(err))
			Expect(tracker.increments).To(Equal(1))
			Expect(tracker.decrements).To(Equal(1))
			Expect(tracker.removed).To(BeZero())
		}
	})

	It("distributed failure decrements tracking and preserves worker-owned refusals", func() {
		for _, err := range []error{status.Error(codes.Unimplemented, "old"), context.Canceled, status.Error(codes.ResourceExhausted, "budget")} {
			tracker := &fakeInFlightTracker{}
			client := NewInFlightTrackingClient(&multimodalTrackedBackend{err: err}, tracker, "node", "same", 2)
			_, got := client.MultimodalEmbedding(context.Background(), &pb.MultimodalEmbeddingRequest{})
			Expect(got).To(BeIdenticalTo(err))
			Expect(tracker.increments).To(Equal(1))
			Expect(tracker.decrements).To(Equal(1))
			Expect(tracker.removed).To(BeZero())
		}
	})
	It("distributed model-identity and unload refusals reconcile the selected replica", func() {
		for _, err := range []error{grpcerrors.ModelMismatch("test", "other", "same"), grpcerrors.ModelNotLoaded("test")} {
			tracker := &fakeInFlightTracker{}
			client := NewInFlightTrackingClient(&multimodalTrackedBackend{err: err}, tracker, "node", "same", 2)
			_, got := client.MultimodalEmbedding(context.Background(), &pb.MultimodalEmbeddingRequest{})
			Expect(got).To(BeIdenticalTo(err))
			Expect(tracker.removed).To(Equal(1))
			Expect(tracker.decrements).To(Equal(1))
		}
	})
})
