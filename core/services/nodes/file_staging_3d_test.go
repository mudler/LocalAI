package nodes

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
)

type capturing3DBackend struct {
	grpc.Backend
	request *pb.Generate3DRequest
}

func (b *capturing3DBackend) Generate3D(_ context.Context, request *pb.Generate3DRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.request = request
	return &pb.Result{Success: true}, nil
}

type failingFetchStager struct {
	fakeFileStager
}

func (f *failingFetchStager) FetchRemote(_ context.Context, _, _, _ string) error {
	return errors.New("transfer failed")
}

var _ = Describe("FileStagingClient 3D output", func() {
	It("returns an error when the generated asset cannot be retrieved", func(ctx SpecContext) {
		backend := &capturing3DBackend{}
		stager := &failingFetchStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		request := &pb.Generate3DRequest{Dst: "/data/generated/asset.glb"}

		result, err := client.Generate3D(ctx, request)

		Expect(result).To(Equal(&pb.Result{Success: true}))
		Expect(err).To(MatchError(ContainSubstring("retrieving generated 3D asset: transfer failed")))
		Expect(backend.request.Dst).To(Equal("/remote/tmp"))
	})
})
