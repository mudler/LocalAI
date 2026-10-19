// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync/atomic"
)

// A stable transport address whose worker is replaced, as on a recycled port.
type generationWorker struct {
	pb.UnimplementedBackendServer
	current atomic.Pointer[server]
}

func (w *generationWorker) MultimodalEmbedding(ctx context.Context, r *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
	return w.current.Load().MultimodalEmbedding(ctx, r)
}
func (w *generationWorker) ModelMetadata(ctx context.Context, r *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
	return w.current.Load().ModelMetadata(ctx, r)
}

var _ = Describe("Multimodal routing generation", func() {
	It("publishes worker generation in metadata and request", func() {
		Expect((&pb.ModelMetadataResponse{}).ProtoReflect().Descriptor().Fields().ByName("embedding_generation")).NotTo(BeNil())
		Expect((&pb.MultimodalEmbeddingRequest{}).ProtoReflect().Descriptor().Fields().ByName("embedding_generation")).NotTo(BeNil())
	})
	It("worker rejects same-capability reload before call and accepts refreshed generation", func() {
		calls := 0
		srv := NewBackendServer(&wireModel{call: func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			calls++
			return &pb.MultimodalEmbeddingResponse{}, nil
		}})
		req := wireRequest()
		bindWireGeneration(srv, req)
		old := req.EmbeddingGeneration
		client := wireClient(srv, false)
		_, err := srv.LoadModel(context.Background(), &pb.ModelOptions{Model: "selected"})
		Expect(err).NotTo(HaveOccurred())
		_, err = client.MultimodalEmbedding(context.Background(), req)
		Expect(status.Code(err)).To(Equal(codes.Aborted))
		Expect(calls).To(BeZero())
		meta, err := srv.ModelMetadata(context.Background(), &pb.ModelOptions{Model: "selected"})
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.EmbeddingGeneration).NotTo(Equal(old))
		req.EmbeddingGeneration = meta.EmbeddingGeneration
		_, err = client.MultimodalEmbedding(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(calls).To(Equal(1))
		req.EmbeddingGeneration = ""
		_, err = client.MultimodalEmbedding(context.Background(), req)
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})
	It("worker rejects same-capability reload during inference before publication", func() {
		entered := make(chan struct{})
		release := make(chan struct{})
		srv := NewBackendServer(&wireModel{call: func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			close(entered)
			<-release
			return &pb.MultimodalEmbeddingResponse{}, nil
		}})
		req := wireRequest()
		bindWireGeneration(srv, req)
		client := wireClient(srv, false)
		done := make(chan error, 1)
		go func() { _, err := client.MultimodalEmbedding(context.Background(), req); done <- err }()
		<-entered
		_, err := srv.LoadModel(context.Background(), &pb.ModelOptions{Model: "selected"})
		Expect(err).NotTo(HaveOccurred())
		close(release)
		Expect(status.Code(<-done)).To(Equal(codes.Aborted))
	})
	It("restart at the same address changes generation even with identical capabilities", func() {
		first := NewBackendServer(&wireModel{})
		second := NewBackendServer(&wireModel{})
		a := wireRequest()
		b := wireRequest()
		bindWireGeneration(first, a)
		bindWireGeneration(second, b)
		Expect(a.EmbeddingGeneration).NotTo(Equal(b.EmbeddingGeneration))
		worker := &generationWorker{}
		worker.current.Store(first.(*server))
		client := wireClient(worker, false)
		before, err := client.ModelMetadata(context.Background(), &pb.ModelOptions{Model: "selected"})
		Expect(err).NotTo(HaveOccurred())
		Expect(before.EmbeddingGeneration).To(Equal(a.EmbeddingGeneration))
		worker.current.Store(second.(*server))
		_, err = client.MultimodalEmbedding(context.Background(), a)
		Expect(status.Code(err)).To(Equal(codes.Aborted))
	})
})
