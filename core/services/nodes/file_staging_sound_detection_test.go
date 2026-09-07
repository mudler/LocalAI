package nodes

import (
	"context"
	"errors"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

type soundStagingBackend struct {
	grpc.Backend
	request *pb.SoundDetectionRequest
	err     error
}

func (b *soundStagingBackend) SoundDetection(_ context.Context, in *pb.SoundDetectionRequest, _ ...ggrpc.CallOption) (*pb.SoundDetectionResponse, error) {
	b.request = in
	return &pb.SoundDetectionResponse{}, b.err
}

type soundStagingFailure struct{ FileStager }

func (s *soundStagingFailure) EnsureRemote(context.Context, string, string, string) (string, error) {
	return "", errors.New("upload failed")
}

var _ = Describe("FileStagingClient sound detection", func() {
	It("stages audio on the worker without changing the caller's request", func(ctx SpecContext) {
		backend := &soundStagingBackend{}
		stager := &fakeFileStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		request := &pb.SoundDetectionRequest{Src: "/tmp/realtime-sound-window-test.wav", ModelIdentity: "ced.gguf", TopK: 5, Threshold: 0.25}
		_, err := client.SoundDetection(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(stager.ensureCalls).To(HaveLen(1))
		Expect(stager.ensureCalls[0].nodeID).To(Equal("worker-1"))
		Expect(stager.ensureCalls[0].localPath).To(Equal(request.Src))
		Expect(backend.request.Src).To(Equal("/remote/" + stager.ensureCalls[0].key))
		Expect(backend.request.ModelIdentity).To(Equal(request.ModelIdentity))
		Expect(backend.request.TopK).To(Equal(request.TopK))
		Expect(backend.request.Threshold).To(Equal(request.Threshold))
		Expect(request.Src).To(Equal("/tmp/realtime-sound-window-test.wav"))
	})
	It("does not call the backend when staging fails", func(ctx SpecContext) {
		backend := &soundStagingBackend{}
		client := NewFileStagingClient(backend, &soundStagingFailure{}, "worker-1")
		_, err := client.SoundDetection(ctx, &pb.SoundDetectionRequest{Src: "/tmp/clip.wav"})
		Expect(err).To(MatchError(ContainSubstring("upload failed")))
		Expect(backend.request).To(BeNil())
	})
	It("passes through requests without a file and preserves backend errors", func(ctx SpecContext) {
		failure := errors.New("classifier failed")
		backend := &soundStagingBackend{err: failure}
		stager := &fakeFileStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		_, err := client.SoundDetection(ctx, &pb.SoundDetectionRequest{})
		Expect(err).To(MatchError(failure))
		Expect(stager.ensureCalls).To(BeEmpty())
	})
})
