// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"net"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type wireModel struct {
	base.Base
	call func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error)
}

func (m *wireModel) MultimodalEmbedding(ctx context.Context, in *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
	return m.call(ctx, in)
}

func (m *wireModel) Load(*pb.ModelOptions) error { return nil }
func (m *wireModel) ModelMetadata(*pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
	return &pb.ModelMetadataResponse{EmbeddingProtocolVersion: 1, EmbeddingModalities: []pb.EmbeddingModality{pb.EmbeddingModality_TEXT}}, nil
}
func bindWireGeneration(c pb.BackendServer, r *pb.MultimodalEmbeddingRequest) {
	_, err := c.LoadModel(context.Background(), &pb.ModelOptions{Model: "selected"})
	Expect(err).NotTo(HaveOccurred())
	m, err := c.ModelMetadata(context.Background(), &pb.ModelOptions{Model: "selected"})
	Expect(err).NotTo(HaveOccurred())
	r.EmbeddingGeneration = m.EmbeddingGeneration
}

// No MultimodalEmbedding handler is registered: this is the old service surface,
// not a new server whose application merely returns an unsupported error.
func wireClient(s pb.BackendServer, old bool) *Client {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	transport := ggrpc.NewServer()
	desc := pb.Backend_ServiceDesc
	if old {
		desc.Methods = nil
		for _, method := range pb.Backend_ServiceDesc.Methods {
			if method.MethodName != "MultimodalEmbedding" {
				desc.Methods = append(desc.Methods, method)
			}
		}
	}
	transport.RegisterService(&desc, s)
	go func() { _ = transport.Serve(listener) }()
	DeferCleanup(func() { transport.Stop(); _ = listener.Close() })
	return &Client{address: listener.Addr().String(), parallel: true, token: "wire-token"}
}
func wireRequest() *pb.MultimodalEmbeddingRequest {
	return &pb.MultimodalEmbeddingRequest{Model: "selected", Options: &pb.PredictOptions{Embeddings: "legacy must not be used"}, Parts: []*pb.EmbeddingPart{
		{Part: &pb.EmbeddingPart_Text{Text: "before"}},
		{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_IMAGE, Data: []byte{0, 255, 1}, Format: "png"}}},
		{Part: &pb.EmbeddingPart_Text{Text: ""}},
		{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_AUDIO, Data: []byte{2, 0, 254}, Format: "wav"}}},
		{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_VIDEO, Data: []byte{3, 0, 253}, Format: "mp4"}}},
		{Part: &pb.EmbeddingPart_Text{Text: "after"}},
	}, Budget: &pb.EmbeddingBudget{}}
}
func fillWireCounters(m proto.Message) {
	r := m.ProtoReflect()
	for i := 0; i < r.Descriptor().Fields().Len(); i++ {
		f := r.Descriptor().Fields().Get(i)
		Expect(f.Kind()).To(Equal(protoreflect.Uint64Kind))
		r.Set(f, protoreflect.ValueOfUint64(^uint64(0)-uint64(i)))
	}
}

var _ = Describe("Multimodal wire", func() {
	It("publishes a dedicated typed RPC rather than legacy unknown fields", func() {
		method := pb.File_backend_proto.Services().ByName("Backend").Methods().ByName("MultimodalEmbedding")
		Expect(method).NotTo(BeNil())
		Expect(string(method.Input().Name())).To(Equal("MultimodalEmbeddingRequest"))
		Expect(string(method.Output().Name())).To(Equal("MultimodalEmbeddingResponse"))
	})
	It("round trips ordered binary text parts and every uint64 counter", func() {
		request := wireRequest()
		fillWireCounters(request.Budget)
		response := &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: []float32{1, 2}, Tokens: 1, Dim: 2, PromptTokens: 7, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}, Consumption: &pb.EmbeddingConsumption{}}
		fillWireCounters(response.Consumption)
		srv := NewBackendServer(&wireModel{call: func(ctx context.Context, in *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			if !proto.Equal(in, request) {
				return nil, status.Error(codes.Internal, "wire mismatch")
			}
			return response, nil
		}})
		bindWireGeneration(srv, request)
		client := wireClient(srv, false)
		result, err := client.MultimodalEmbedding(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(result, response)).To(BeTrue())
		Expect(request.GetParts()[2].GetPart()).To(BeAssignableToTypeOf(&pb.EmbeddingPart_Text{}))
		fields := (&pb.EmbeddingMedia{}).ProtoReflect().Descriptor().Fields()
		Expect(fields.Len()).To(Equal(3))
		Expect(fields.ByName("data").Kind()).To(Equal(protoreflect.BytesKind))
		Expect(request.Budget.ProtoReflect().Descriptor().Fields().Len()).To(Equal(9))
		Expect(response.Consumption.ProtoReflect().Descriptor().Fields().Len()).To(Equal(9))
	})
	It("old server returns UNIMPLEMENTED without invoking legacy embeddings", func() {
		backend := &identityBackend{}
		client := wireClient(NewBackendServer(backend), true)
		_, err := client.MultimodalEmbedding(context.Background(), wireRequest())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(backend.served).To(BeZero())
	})
	It("new Go default server rejects without invoking legacy embeddings", func() {
		backend := &identityBackend{}
		s := NewBackendServer(backend)
		_, err := s.MultimodalEmbedding(context.Background(), wireRequest())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(backend.served).To(BeZero())
	})
	It("leaves legacy text RPC and result layout unchanged", func() {
		backend := &identityBackend{}
		client := wireClient(NewBackendServer(backend), false)
		result, err := client.Embeddings(context.Background(), &pb.PredictOptions{Embeddings: "hello"})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Embeddings).To(Equal([]float32{1}))
		Expect(result.Layout).To(Equal(pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL))
		Expect(backend.served).To(Equal(1))
	})
	It("preserves absent metadata and distinguishes all five modalities", func() {
		m := &pb.ModelMetadataResponse{}
		Expect(proto.Unmarshal([]byte{}, m)).To(Succeed())
		Expect(m.EmbeddingProtocolVersion).To(BeZero())
		Expect(m.EmbeddingModalities).To(BeEmpty())
		m.EmbeddingProtocolVersion = 1
		m.EmbeddingModalities = []pb.EmbeddingModality{pb.EmbeddingModality_TEXT, pb.EmbeddingModality_IMAGE, pb.EmbeddingModality_AUDIO, pb.EmbeddingModality_SILENT_VIDEO, pb.EmbeddingModality_AUDIOVISUAL_VIDEO}
		b, err := proto.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		n := &pb.ModelMetadataResponse{}
		Expect(proto.Unmarshal(b, n)).To(Succeed())
		Expect(proto.Equal(m, n)).To(BeTrue())
	})
	It("preserves legacy field numbers and dedicated request numbers", func() {
		for _, tc := range []struct {
			message proto.Message
			names   []string
			numbers []int
		}{
			{&pb.ModelMetadataResponse{}, []string{"supports_thinking", "rendered_template", "tool_format", "media_marker", "embedding_protocol_version", "embedding_modalities"}, []int{1, 2, 3, 4, 5, 6}},
			{&pb.EmbeddingResult{}, []string{"embeddings", "tokens", "dim", "prompt_tokens", "layout"}, []int{1, 2, 3, 4, 5}},
			{&pb.PredictOptions{}, []string{"EmbeddingTokens", "Embeddings"}, []int{35, 36}},
			{&pb.MultimodalEmbeddingRequest{}, []string{"model", "options", "parts", "budget"}, []int{1, 2, 3, 4}},
		} {
			for i, n := range tc.names {
				Expect(int(tc.message.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(n)).Number())).To(Equal(tc.numbers[i]))
			}
		}
	})
	It("enforces proto.Size below at and above 50 MiB before dispatch", func() {
		calls := 0
		s := NewBackendServer(&wireModel{call: func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			calls++
			return &pb.MultimodalEmbeddingResponse{}, nil
		}})
		data := make([]byte, maxGRPCMessageSize)
		for _, delta := range []int{-1, 0, 1} {
			r := wireRequest()
			bindWireGeneration(s, r)
			media := r.Parts[1].GetMedia()
			media.Data = data
			n := len(data) + (maxGRPCMessageSize + delta - proto.Size(r))
			media.Data = data[:n]
			Expect(proto.Size(r)).To(Equal(maxGRPCMessageSize + delta))
			_, err := s.MultimodalEmbedding(context.Background(), r)
			if delta < 0 {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(status.Code(err)).To(Equal(codes.ResourceExhausted))
				// An invalid address proves this error originates before transport dispatch.
				_, err = (&Client{address: "invalid"}).MultimodalEmbedding(context.Background(), r)
				Expect(status.Code(err)).To(Equal(codes.ResourceExhausted))
			}
		}
		Expect(calls).To(Equal(1))
	})
	It("preserves bearer metadata call options busy accounting and cancellation", func() {
		entered := make(chan metadata.MD, 1)
		srv := NewBackendServer(&wireModel{call: func(ctx context.Context, in *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			entered <- md
			_ = ggrpc.SetHeader(ctx, metadata.Pairs("wire-header", "yes")) // the header is only a probe for the wire test
			<-ctx.Done()
			return nil, status.FromContextError(ctx.Err()).Err()
		}})
		request := wireRequest()
		bindWireGeneration(srv, request)
		client := wireClient(srv, false)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		var headers metadata.MD
		go func() { _, err := client.MultimodalEmbedding(ctx, request, ggrpc.Header(&headers)); done <- err }()
		var md metadata.MD
		Eventually(entered, time.Second*5).Should(Receive(&md))
		Expect(md.Get("authorization")).To(Equal([]string{"Bearer wire-token"}))
		Expect(client.IsBusy()).To(BeTrue())
		cancel()
		var err error
		Eventually(done, time.Second*5).Should(Receive(&err))
		Expect(status.Code(err)).To(Equal(codes.Canceled))
		Expect(client.IsBusy()).To(BeFalse())
	})
	It("enforces selected identity on embedded and transported routes", func() {
		backend := &identityBackend{}
		s := NewBackendServer(backend)
		_, err := s.LoadModel(context.Background(), &pb.ModelOptions{Model: "other"})
		Expect(err).NotTo(HaveOccurred())
		for _, c := range []Backend{&embedBackend{s: s.(*server)}, wireClient(s, false)} {
			_, err = c.MultimodalEmbedding(context.Background(), wireRequest())
			Expect(grpcerrors.IsModelMismatch(err)).To(BeTrue())
		}
		r := wireRequest()
		r.Options.ModelIdentity = "conflict"
		_, err = s.MultimodalEmbedding(context.Background(), r)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})
})
