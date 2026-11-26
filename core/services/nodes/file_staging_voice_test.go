// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// voiceStagingBackend stands in for a backend on another machine: it only
// accepts paths under the worker root, so a frontend temp path fails the way
// a remote worker fails to open it.
type voiceStagingBackend struct {
	grpc.Backend
	workerRoot string
	opened     []string
}

func (b *voiceStagingBackend) open(path string) error {
	if !strings.HasPrefix(path, b.workerRoot+string(filepath.Separator)) {
		return errors.New("Error opening '" + path + "': System error.")
	}
	if _, err := os.ReadFile(path); err != nil {
		return err
	}
	b.opened = append(b.opened, path)
	return nil
}

func (b *voiceStagingBackend) VoiceEmbed(_ context.Context, in *pb.VoiceEmbedRequest, _ ...ggrpc.CallOption) (*pb.VoiceEmbedResponse, error) {
	if err := b.open(in.Audio); err != nil {
		return nil, err
	}
	return &pb.VoiceEmbedResponse{Embedding: []float32{0.1, 0.2}, Model: in.ModelIdentity}, nil
}

func (b *voiceStagingBackend) VoiceAnalyze(_ context.Context, in *pb.VoiceAnalyzeRequest, _ ...ggrpc.CallOption) (*pb.VoiceAnalyzeResponse, error) {
	if err := b.open(in.Audio); err != nil {
		return nil, err
	}
	return &pb.VoiceAnalyzeResponse{Segments: []*pb.VoiceAnalysis{{DominantGender: "x"}}}, nil
}

func (b *voiceStagingBackend) VoiceVerify(_ context.Context, in *pb.VoiceVerifyRequest, _ ...ggrpc.CallOption) (*pb.VoiceVerifyResponse, error) {
	if err := b.open(in.Audio1); err != nil {
		return nil, err
	}
	if err := b.open(in.Audio2); err != nil {
		return nil, err
	}
	return &pb.VoiceVerifyResponse{Verified: true}, nil
}

var _ = Describe("FileStagingClient voice recognition", func() {
	var (
		root    string
		clip1   string
		clip2   string
		stager  *diarizationStager
		backend *voiceStagingBackend
		client  *FileStagingClient
	)

	BeforeEach(func() {
		root = GinkgoT().TempDir()
		clip1 = filepath.Join(root, "frontend", "localai-voice-1.wav")
		clip2 = filepath.Join(root, "frontend", "localai-voice-2.wav")
		Expect(os.MkdirAll(filepath.Dir(clip1), 0750)).To(Succeed())
		Expect(os.WriteFile(clip1, []byte("RIFF one"), 0600)).To(Succeed())
		Expect(os.WriteFile(clip2, []byte("RIFF two"), 0600)).To(Succeed())
		stager = &diarizationStager{root: filepath.Join(root, "worker")}
		backend = &voiceStagingBackend{workerRoot: stager.root}
		client = NewFileStagingClient(backend, stager, "worker-1")
	})

	expectStagedAndReleased := func(n int) {
		Expect(stager.ensureCalls).To(HaveLen(n))
		Expect(backend.opened).To(HaveLen(n))
		keys := make([]string, 0, n)
		for i, c := range stager.ensureCalls {
			Expect(c.nodeID).To(Equal("worker-1"))
			Expect(backend.opened[i]).To(Equal(filepath.Join(stager.root, c.key)))
			keys = append(keys, c.key)
		}
		Expect(stager.releasedKeys).To(ConsistOf(keys))
	}

	It("stages the clip for VoiceEmbed", func(ctx SpecContext) {
		req := &pb.VoiceEmbedRequest{Audio: clip1, ModelIdentity: "encoder"}
		original := proto.Clone(req)
		res, err := client.VoiceEmbed(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Embedding).To(HaveLen(2))
		Expect(res.Model).To(Equal("encoder"))
		Expect(proto.Equal(req, original)).To(BeTrue())
		Expect(stager.ensureCalls[0].localPath).To(Equal(clip1))
		expectStagedAndReleased(1)
	})

	It("stages the clip for VoiceAnalyze", func(ctx SpecContext) {
		req := &pb.VoiceAnalyzeRequest{Audio: clip1, Actions: []string{"gender"}}
		original := proto.Clone(req)
		res, err := client.VoiceAnalyze(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Segments).To(HaveLen(1))
		Expect(proto.Equal(req, original)).To(BeTrue())
		expectStagedAndReleased(1)
	})

	It("stages both clips for VoiceVerify", func(ctx SpecContext) {
		req := &pb.VoiceVerifyRequest{Audio1: clip1, Audio2: clip2, Threshold: 0.3}
		original := proto.Clone(req)
		res, err := client.VoiceVerify(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Verified).To(BeTrue())
		Expect(proto.Equal(req, original)).To(BeTrue())
		Expect(stager.ensureCalls[0].localPath).To(Equal(clip1))
		Expect(stager.ensureCalls[1].localPath).To(Equal(clip2))
		expectStagedAndReleased(2)
	})

	It("does not call the backend when staging fails", func(ctx SpecContext) {
		failure := errors.New("upload failed")
		failing := &lifecycleStager{ensureErr: failure}
		c := NewFileStagingClient(backend, failing, "worker-1")

		_, err := c.VoiceEmbed(ctx, &pb.VoiceEmbedRequest{Audio: clip1})
		Expect(err).To(MatchError(ContainSubstring("staging audio for voice embed")))
		Expect(errors.Is(err, failure)).To(BeTrue())

		_, err = c.VoiceAnalyze(ctx, &pb.VoiceAnalyzeRequest{Audio: clip1})
		Expect(err).To(MatchError(ContainSubstring("staging audio for voice analyze")))

		_, err = c.VoiceVerify(ctx, &pb.VoiceVerifyRequest{Audio1: clip1, Audio2: clip2})
		Expect(err).To(MatchError(ContainSubstring("staging first audio for voice verify")))

		Expect(backend.opened).To(BeEmpty())
		Expect(failing.releasedKeys).To(HaveLen(3))
	})
})
