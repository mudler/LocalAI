// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type diarizationStager struct {
	lifecycleStager
	root string
}

func (s *diarizationStager) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	if _, err := s.lifecycleStager.EnsureRemote(ctx, nodeID, localPath, key); err != nil {
		return "", err
	}
	remotePath := filepath.Join(s.root, key)
	return remotePath, copyFile(localPath, remotePath)
}

type diarizationStagingBackend struct {
	grpc.Backend
	call func(context.Context, *pb.DiarizeRequest, ...ggrpc.CallOption) (*pb.DiarizeResponse, error)
}

func (b *diarizationStagingBackend) Diarize(ctx context.Context, in *pb.DiarizeRequest, opts ...ggrpc.CallOption) (*pb.DiarizeResponse, error) {
	return b.call(ctx, in, opts...)
}

var _ = Describe("FileStagingClient diarization", func() {
	DescribeTable("stages audio and releases it after the backend returns", func(backendErr error) {
		ctx := context.WithValue(context.Background(), struct{}{}, "request-context")
		root := GinkgoT().TempDir()
		source := filepath.Join(root, "frontend", "clip.wav")
		audio := []byte("RIFF\x00\x01\x02\xffWAVEtest audio")
		Expect(os.MkdirAll(filepath.Dir(source), 0750)).To(Succeed())
		Expect(os.WriteFile(source, audio, 0600)).To(Succeed())
		stager := &diarizationStager{root: filepath.Join(root, "worker")}
		request := &pb.DiarizeRequest{
			Dst: source, Threads: 4, Language: "en", NumSpeakers: 2,
			MinSpeakers: 1, MaxSpeakers: 3, ClusteringThreshold: 0.7,
			MinDurationOn: 0.2, MinDurationOff: 0.4, IncludeText: true,
			ModelIdentity: "diarization-model", IncludeSpeakerProfiles: true,
			KnownVoices: []*pb.KnownVoice{{Id: "voice-1", Name: "speaker", Model: "encoder", Embedding: []float32{0.1, 0.2}}},
		}
		original := proto.Clone(request)
		response := &pb.DiarizeResponse{NumSpeakers: 2}
		option := ggrpc.WaitForReady(true)
		calls := 0
		backend := &diarizationStagingBackend{call: func(gotCtx context.Context, in *pb.DiarizeRequest, opts ...ggrpc.CallOption) (*pb.DiarizeResponse, error) {
			calls++
			Expect(gotCtx).To(Equal(ctx))
			Expect(opts).To(Equal([]ggrpc.CallOption{option}))
			Expect(in).NotTo(BeIdenticalTo(request))
			Expect(stager.ensureCalls).To(HaveLen(1))
			staged := stager.ensureCalls[0]
			Expect(staged.nodeID).To(Equal("worker-1"))
			Expect(staged.localPath).To(Equal(source))
			Expect(in.Dst).To(Equal(filepath.Join(stager.root, staged.key)))
			Expect(in.Dst).NotTo(Equal(source))
			contents, err := os.ReadFile(in.Dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(contents).To(Equal(audio))
			expected := proto.Clone(request).(*pb.DiarizeRequest)
			expected.Dst = in.Dst
			Expect(proto.Equal(in, expected)).To(BeTrue())
			Expect(stager.releasedKeys).To(BeEmpty())
			in.KnownVoices[0].Embedding[0] = 99
			return response, backendErr
		}}
		result, err := NewFileStagingClient(backend, stager, "worker-1").Diarize(ctx, request, option)
		if backendErr != nil {
			Expect(err).To(BeIdenticalTo(backendErr))
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(result).To(BeIdenticalTo(response))
		Expect(calls).To(Equal(1))
		Expect(proto.Equal(request, original)).To(BeTrue())
		Expect(stager.releasedKeys).To(Equal([]string{stager.ensureCalls[0].key}))
	}, Entry("success", nil), Entry("backend error", errors.New("diarization failed")))

	It("releases the attempted stage and does not forward when staging fails", func(ctx SpecContext) {
		failure := errors.New("upload failed")
		stager := &lifecycleStager{ensureErr: failure}
		called := false
		backend := &diarizationStagingBackend{call: func(context.Context, *pb.DiarizeRequest, ...ggrpc.CallOption) (*pb.DiarizeResponse, error) {
			called = true
			return &pb.DiarizeResponse{}, nil
		}}
		request := &pb.DiarizeRequest{Dst: filepath.Join(GinkgoT().TempDir(), "clip.wav")}
		original := proto.Clone(request)
		result, err := NewFileStagingClient(backend, stager, "worker-1").Diarize(ctx, request)
		Expect(err).To(MatchError(ContainSubstring("staging audio for diarization")))
		Expect(errors.Is(err, failure)).To(BeTrue())
		Expect(result).To(BeNil())
		Expect(called).To(BeFalse())
		Expect(proto.Equal(request, original)).To(BeTrue())
		Expect(stager.ensureCalls).To(HaveLen(1))
		Expect(stager.releasedKeys).To(Equal([]string{stager.ensureCalls[0].key}))
	})
})
