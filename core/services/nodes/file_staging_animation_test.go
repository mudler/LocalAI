// SPDX-License-Identifier: MIT
package nodes

import (
	"context"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

type capturingAnimationBackend struct {
	grpc.Backend
	request *pb.Animate3DRequest
}

func (b *capturingAnimationBackend) Animate3D(_ context.Context, request *pb.Animate3DRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.request = request
	return &pb.Result{Success: true}, nil
}

var _ = Describe("animation file staging", func() {
	It("stages media inputs but preserves literal text and model identity", func(ctx SpecContext) {
		backend := &capturingAnimationBackend{}
		stager := &lifecycleStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		request := &pb.Animate3DRequest{ModelIdentity: "original-model", Dst: "/tmp/output.glb", Inputs: map[string]*pb.AnimationInput{
			"prompt": {Type: "text", Data: "/a/path/is/still/text"},
			"mesh":   {Type: "mesh", Data: "/tmp/mesh.glb"},
			"video":  {Type: "video", Data: "/tmp/motion.mp4"},
		}}
		_, err := client.Animate3D(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend.request.Inputs["prompt"].Data).To(Equal(request.Inputs["prompt"].Data))
		Expect(backend.request.Inputs["mesh"].Data).To(HavePrefix("/remote/"))
		Expect(backend.request.Inputs["video"].Data).To(HavePrefix("/remote/"))
		Expect(backend.request.ModelIdentity).To(Equal("original-model"))
		Expect(request.Dst).To(Equal("/tmp/output.glb"))
		Expect(request.Inputs["mesh"].Data).To(Equal("/tmp/mesh.glb"))
		Expect(stager.releasedKeys).To(HaveLen(2))
	})
	It("reports output transfer failures", func(ctx SpecContext) {
		client := NewFileStagingClient(&capturingAnimationBackend{}, &failingFetchStager{}, "worker-1")
		_, err := client.Animate3D(ctx, &pb.Animate3DRequest{Dst: "/tmp/animation.glb"})
		Expect(err).To(MatchError(ContainSubstring("retrieving animation: transfer failed")))
	})
})
