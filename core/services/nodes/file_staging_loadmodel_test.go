package nodes

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
)

// capturingLoadBackend records the ModelOptions handed to LoadModel, standing in
// for the remote gRPC backend the FileStagingClient forwards to.
type capturingLoadBackend struct {
	grpc.Backend
	got *pb.ModelOptions
}

func (b *capturingLoadBackend) LoadModel(_ context.Context, in *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.got = in
	return &pb.Result{Success: true}, nil
}

// FileStagingClient is the concrete type of `client` at router.go's
// client.LoadModel call site (buildClientForAddr wraps the gRPC backend with it
// whenever a file stager is configured, i.e. every distributed load). This pins
// the boundary the companion-option investigation kept returning to: the wrapper
// must forward ModelOptions.Options (the channel a managed companion option like
// base_model rides on) to the backend byte-for-byte. If a companion option is
// present in what the controller sends here but absent at the Python backend,
// this test proves the loss is NOT in this Go handoff.
var _ = Describe("FileStagingClient LoadModel option pass-through", func() {
	It("forwards ModelOptions.Options to the backend unchanged", func(ctx SpecContext) {
		backend := &capturingLoadBackend{}
		client := NewFileStagingClient(backend, &fakeFileStager{}, "worker-1")

		sent := &pb.ModelOptions{
			Model:     "longcat-video-avatar-1.5",
			ModelPath: "/models/longcat-video-avatar-1.5",
			Options: []string{
				"attention_backend:sdpa",
				"use_distill:true",
				"base_model:.artifacts/huggingface/deadbeef/snapshot",
			},
		}

		_, err := client.LoadModel(ctx, sent)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend.got).NotTo(BeNil())
		Expect(backend.got.Options).To(Equal(sent.Options))
		Expect(backend.got.Options).To(ContainElement("base_model:.artifacts/huggingface/deadbeef/snapshot"))
		// The nested staged ModelPath the backend joins the relative option against
		// must survive too.
		Expect(backend.got.ModelPath).To(Equal("/models/longcat-video-avatar-1.5"))
	})
})
