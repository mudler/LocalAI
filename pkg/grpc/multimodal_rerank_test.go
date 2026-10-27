// SPDX-License-Identifier: MIT
package grpc

import (
	"context"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type rerankWireModel struct {
	base.Base
	call func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error)
}

func (m *rerankWireModel) MultimodalRerank(ctx context.Context, in *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
	return m.call(ctx, in)
}
func (m *rerankWireModel) Load(*pb.ModelOptions) error { return nil }
func (m *rerankWireModel) ModelMetadata(*pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
	return &pb.ModelMetadataResponse{RerankProtocolVersion: 1, RerankModalities: []pb.EmbeddingModality{pb.EmbeddingModality_TEXT, pb.EmbeddingModality_IMAGE}}, nil
}

func rerankWireRequest() *pb.MultimodalRerankRequest {
	text := func(s string) *pb.EmbeddingPart { return &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Text{Text: s}} }
	image := &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_IMAGE, Data: []byte{0, 255, 1}, Format: "png"}}}
	return &pb.MultimodalRerankRequest{
		Model: "selected", TopN: 2,
		Query:     []*pb.EmbeddingPart{text("query"), image},
		Documents: []*pb.RerankDocument{{Parts: []*pb.EmbeddingPart{text("first")}}, {Parts: []*pb.EmbeddingPart{text("second"), image}}},
		Budget:    &pb.EmbeddingBudget{RemainingSourceParts: 9},
	}
}

func bindRerankGeneration(c pb.BackendServer, r *pb.MultimodalRerankRequest) {
	_, err := c.LoadModel(context.Background(), &pb.ModelOptions{Model: "selected"})
	Expect(err).NotTo(HaveOccurred())
	m, err := c.ModelMetadata(context.Background(), &pb.ModelOptions{Model: "selected"})
	Expect(err).NotTo(HaveOccurred())
	r.EmbeddingGeneration = m.EmbeddingGeneration
}

var _ = Describe("Multimodal rerank wire", func() {
	It("publishes a dedicated typed RPC and metadata fields", func() {
		method := pb.File_backend_proto.Services().ByName("Backend").Methods().ByName("MultimodalRerank")
		Expect(method).NotTo(BeNil())
		Expect(string(method.Input().Name())).To(Equal("MultimodalRerankRequest"))
		fields := (&pb.ModelMetadataResponse{}).ProtoReflect().Descriptor().Fields()
		Expect(fields.ByName("rerank_protocol_version")).NotTo(BeNil())
		Expect(fields.ByName("rerank_modalities")).NotTo(BeNil())
	})
	It("round trips ordered query and document parts over a real transport", func() {
		request := rerankWireRequest()
		response := &pb.MultimodalRerankResponse{
			Result:      &pb.RerankResult{Results: []*pb.DocumentResult{{Index: 1, RelevanceScore: 0.9}, {Index: 0, RelevanceScore: 0.1}}},
			Consumption: &pb.EmbeddingConsumption{SourceParts: 5},
		}
		srv := NewBackendServer(&rerankWireModel{call: func(_ context.Context, in *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
			if !proto.Equal(in, request) {
				return nil, status.Error(codes.Internal, "wire mismatch")
			}
			return response, nil
		}})
		bindRerankGeneration(srv, request)
		got, err := wireClient(srv, false).MultimodalRerank(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(got, response)).To(BeTrue())
	})
	It("a server without the handler answers UNIMPLEMENTED and never uses the text RPC", func() {
		backend := &identityBackend{}
		s := NewBackendServer(backend)
		_, err := s.MultimodalRerank(context.Background(), rerankWireRequest())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(backend.served).To(BeZero())
	})
	It("refuses a missing identity, a mismatched identity and a stale generation", func() {
		srv := NewBackendServer(&rerankWireModel{call: func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
			return &pb.MultimodalRerankResponse{}, nil
		}})
		request := rerankWireRequest()
		bindRerankGeneration(srv, request)

		noModel := proto.Clone(request).(*pb.MultimodalRerankRequest)
		noModel.Model = ""
		_, err := srv.MultimodalRerank(context.Background(), noModel)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

		other := proto.Clone(request).(*pb.MultimodalRerankRequest)
		other.Model = "someone-else"
		_, err = srv.MultimodalRerank(context.Background(), other)
		Expect(err).To(HaveOccurred())

		stale := proto.Clone(request).(*pb.MultimodalRerankRequest)
		stale.EmbeddingGeneration = "old"
		_, err = srv.MultimodalRerank(context.Background(), stale)
		Expect(status.Code(err)).To(Equal(codes.Aborted))

		missing := proto.Clone(request).(*pb.MultimodalRerankRequest)
		missing.EmbeddingGeneration = ""
		_, err = srv.MultimodalRerank(context.Background(), missing)
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})
	It("rejects a request at the transport ceiling before dispatch", func() {
		request := rerankWireRequest()
		request.Query = append(request.Query, &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_IMAGE, Data: make([]byte, maxGRPCMessageSize)}}})
		_, err := (&Client{address: "invalid"}).MultimodalRerank(context.Background(), request)
		Expect(status.Code(err)).To(Equal(codes.ResourceExhausted))
		_, err = (&Client{address: "invalid"}).MultimodalRerank(context.Background(), nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})
})
