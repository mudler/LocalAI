package nodes

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
)

type capturingTTSBackend struct {
	grpc.Backend
	ttsRequest       *pb.TTSRequest
	streamTTSRequest *pb.TTSRequest
}

func (b *capturingTTSBackend) TTS(_ context.Context, request *pb.TTSRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.ttsRequest = request
	return &pb.Result{Success: true}, nil
}

func (b *capturingTTSBackend) TTSStream(_ context.Context, request *pb.TTSRequest, _ func(*pb.Reply), _ ...ggrpc.CallOption) error {
	b.streamTTSRequest = request
	return nil
}

var _ = Describe("FileStagingClient TTS references", func() {
	It("stages a reference WAV before non-streaming synthesis", func(ctx SpecContext) {
		backend := &capturingTTSBackend{}
		stager := &fakeFileStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		request := &pb.TTSRequest{Voice: "/data/voice-profiles/profile/reference.wav"}

		_, err := client.TTS(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(stager.ensureCalls).To(HaveLen(1))
		Expect(stager.ensureCalls[0].localPath).To(Equal("/data/voice-profiles/profile/reference.wav"))
		Expect(backend.ttsRequest.Voice).To(HavePrefix("/remote/ephemeral/"))
		Expect(backend.ttsRequest.Voice).To(MatchRegexp(`/inputs/[0-9a-f]{8}/reference\.wav$`))
	})

	It("stages a reference WAV before streaming synthesis", func(ctx SpecContext) {
		backend := &capturingTTSBackend{}
		stager := &fakeFileStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		request := &pb.TTSRequest{Voice: "/data/reference.wav"}

		Expect(client.TTSStream(ctx, request, func(*pb.Reply) {})).To(Succeed())
		Expect(stager.ensureCalls).To(HaveLen(1))
		Expect(backend.streamTTSRequest.Voice).To(HavePrefix("/remote/ephemeral/"))
	})

	It("does not stage a named speaker", func(ctx SpecContext) {
		backend := &capturingTTSBackend{}
		stager := &fakeFileStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")

		_, err := client.TTS(ctx, &pb.TTSRequest{Voice: "serena"})
		Expect(err).NotTo(HaveOccurred())
		Expect(stager.ensureCalls).To(BeEmpty())
		Expect(backend.ttsRequest.Voice).To(Equal("serena"))
	})

	It("stages every multi-reference personality clip", func(ctx SpecContext) {
		backend := &capturingTTSBackend{}
		stager := &fakeFileStager{}
		client := NewFileStagingClient(backend, stager, "worker-1")
		request := &pb.TTSRequest{Params: map[string]string{"multi_reference_cond": `[{"audio":"/data/one.wav","text":"one"},{"audio":"/data/two.wav","text":"two"}]`}}
		_, err := client.TTS(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(stager.ensureCalls).To(HaveLen(2))
		var references []map[string]string
		Expect(json.Unmarshal([]byte(backend.ttsRequest.Params["multi_reference_cond"]), &references)).To(Succeed())
		Expect(references[0]["audio"]).To(HavePrefix("/remote/ephemeral/"))
		Expect(references[1]["audio"]).To(HavePrefix("/remote/ephemeral/"))
	})
})
