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

// transformBackend records the request it receives and answers like a
// separation backend: it writes stems next to the worker-side dst.
type transformBackend struct {
	grpc.Backend
	request *pb.AudioTransformRequest
	result  *pb.AudioTransformResult
	err     error
}

func (b *transformBackend) AudioTransform(_ context.Context, in *pb.AudioTransformRequest, _ ...ggrpc.CallOption) (*pb.AudioTransformResult, error) {
	b.request = in
	return b.result, b.err
}

// transformStager is a fakeFileStager that hands out a worker temp path,
// records fetches and can fail an upload or the fetch of one file.
type transformStager struct {
	fakeFileStager
	ensureErr error
	failFetch string
	fetches   []ensureCall // nodeID, remote path, local destination
}

func (s *transformStager) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	if s.ensureErr != nil {
		return "", s.ensureErr
	}
	return s.fakeFileStager.EnsureRemote(ctx, nodeID, localPath, key)
}

func (s *transformStager) AllocRemoteTemp(context.Context, string) (string, error) {
	return "/worker/staging/out-1.tmp", nil
}

func (s *transformStager) FetchRemote(_ context.Context, nodeID, remote, local string) error {
	s.fetches = append(s.fetches, ensureCall{nodeID, remote, local})
	if s.failFetch != "" && remote == s.failFetch {
		return errors.New("fetch failed")
	}
	return nil
}

func separationResult() *pb.AudioTransformResult {
	return &pb.AudioTransformResult{
		Dst: "/worker/staging/out-1.tmp",
		Stems: []*pb.AudioTransformStem{
			{Name: "drums", Dst: "/worker/staging/out-1.drums.tmp"},
			{Name: "vocals", Dst: "/worker/staging/out-1.vocals.tmp"},
		},
	}
}

var _ = Describe("FileStagingClient audio transform", func() {
	It("stages input and reference and rewrites the request paths", func(ctx SpecContext) {
		backend := &transformBackend{result: &pb.AudioTransformResult{}}
		stager := &transformStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		in := &pb.AudioTransformRequest{AudioPath: "/tmp/in.wav", ReferencePath: "/tmp/ref.wav", Dst: "/gen/audio/out.wav", ModelIdentity: "m"}
		_, err := client.AudioTransform(ctx, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(stager.ensureCalls).To(HaveLen(2))
		Expect(stager.ensureCalls[0].localPath).To(Equal("/tmp/in.wav"))
		Expect(stager.ensureCalls[1].localPath).To(Equal("/tmp/ref.wav"))
		Expect(backend.request.AudioPath).To(Equal("/remote/" + stager.ensureCalls[0].key))
		Expect(backend.request.ReferencePath).To(Equal("/remote/" + stager.ensureCalls[1].key))
		Expect(backend.request.Dst).To(Equal("/worker/staging/out-1.tmp"))
		Expect(backend.request.ModelIdentity).To(Equal("m"))
		Expect(in.AudioPath).To(Equal("/tmp/in.wav"))
		Expect(in.Dst).To(Equal("/gen/audio/out.wav"))
	})

	It("retrieves dst and every stem and returns frontend paths", func(ctx SpecContext) {
		backend := &transformBackend{result: separationResult()}
		stager := &transformStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		res, err := client.AudioTransform(ctx, &pb.AudioTransformRequest{AudioPath: "/tmp/in.wav", Dst: "/gen/audio/out.wav"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Dst).To(Equal("/gen/audio/out.wav"))
		Expect(res.Stems).To(HaveLen(2))
		Expect(res.Stems[0].Name).To(Equal("drums"))
		Expect(res.Stems[0].Dst).To(Equal("/gen/audio/out.drums.wav"))
		Expect(res.Stems[1].Dst).To(Equal("/gen/audio/out.vocals.wav"))
		Expect(stager.fetches).To(Equal([]ensureCall{
			{"worker-1", "/worker/staging/out-1.tmp", "/gen/audio/out.wav"},
			{"worker-1", "/worker/staging/out-1.drums.tmp", "/gen/audio/out.drums.wav"},
			{"worker-1", "/worker/staging/out-1.vocals.tmp", "/gen/audio/out.vocals.wav"},
		}))
	})

	It("does not call the backend when the upload fails", func(ctx SpecContext) {
		backend := &transformBackend{result: &pb.AudioTransformResult{}}
		client := NewFileStagingClient(backend, &transformStager{ensureErr: errors.New("upload failed")}, "worker-1")
		_, err := client.AudioTransform(ctx, &pb.AudioTransformRequest{AudioPath: "/tmp/in.wav", Dst: "/gen/audio/out.wav"})
		Expect(err).To(MatchError(ContainSubstring("upload failed")))
		Expect(backend.request).To(BeNil())
	})

	It("returns an error when one stem cannot be retrieved", func(ctx SpecContext) {
		backend := &transformBackend{result: separationResult()}
		stager := &transformStager{failFetch: "/worker/staging/out-1.vocals.tmp"}
		client := NewFileStagingClient(backend, stager, "worker-1")
		_, err := client.AudioTransform(ctx, &pb.AudioTransformRequest{AudioPath: "/tmp/in.wav", Dst: "/gen/audio/out.wav"})
		Expect(err).To(MatchError(ContainSubstring("vocals")))
	})

	It("does not stage an empty reference", func(ctx SpecContext) {
		backend := &transformBackend{result: &pb.AudioTransformResult{}}
		stager := &transformStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		_, err := client.AudioTransform(ctx, &pb.AudioTransformRequest{AudioPath: "/tmp/in.wav", Dst: "/gen/audio/out.wav"})
		Expect(err).NotTo(HaveOccurred())
		Expect(stager.ensureCalls).To(HaveLen(1))
		Expect(backend.request.ReferencePath).To(BeEmpty())
	})

	It("passes backend errors through without retrieving anything", func(ctx SpecContext) {
		failure := errors.New("separation failed")
		backend := &transformBackend{err: failure}
		stager := &transformStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		_, err := client.AudioTransform(ctx, &pb.AudioTransformRequest{AudioPath: "/tmp/in.wav", Dst: "/gen/audio/out.wav"})
		Expect(err).To(MatchError(failure))
		Expect(stager.fetches).To(BeEmpty())
	})
})
