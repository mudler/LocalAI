package grpc

import (
	"context"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// modalityBackend answers every non-PredictOptions inference RPC successfully
// and counts the calls that reached it. `served` is therefore the signal for
// "the identity guard let this through" — the same role it plays in
// model_identity_test.go for the four PredictOptions RPCs.
type modalityBackend struct {
	base.SingleThread

	loaded string
	served int
}

func (b *modalityBackend) Load(opts *pb.ModelOptions) error {
	b.loaded = opts.Model
	return nil
}

func (b *modalityBackend) GenerateImage(*pb.GenerateImageRequest) error { b.served++; return nil }
func (b *modalityBackend) GenerateVideo(*pb.GenerateVideoRequest) error { b.served++; return nil }
func (b *modalityBackend) TTS(*pb.TTSRequest) error                     { b.served++; return nil }

func (b *modalityBackend) TTSStream(_ *pb.TTSRequest, ch chan []byte) error {
	b.served++
	ch <- []byte{0}
	close(ch)
	return nil
}

func (b *modalityBackend) SoundGeneration(*pb.SoundGenerationRequest) error { b.served++; return nil }

func (b *modalityBackend) AudioTranscription(context.Context, *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	b.served++
	return pb.TranscriptResult{Text: "ok"}, nil
}

func (b *modalityBackend) AudioTranscriptionStream(_ context.Context, _ *pb.TranscriptRequest, ch chan *pb.TranscriptStreamResponse) error {
	b.served++
	ch <- &pb.TranscriptStreamResponse{Delta: "ok"}
	close(ch)
	return nil
}

func (b *modalityBackend) Detect(*pb.DetectOptions) (pb.DetectResponse, error) {
	b.served++
	return pb.DetectResponse{}, nil
}

func (b *modalityBackend) Depth(*pb.DepthRequest) (pb.DepthResponse, error) {
	b.served++
	return pb.DepthResponse{}, nil
}

func (b *modalityBackend) FaceVerify(*pb.FaceVerifyRequest) (pb.FaceVerifyResponse, error) {
	b.served++
	return pb.FaceVerifyResponse{}, nil
}

func (b *modalityBackend) FaceAnalyze(*pb.FaceAnalyzeRequest) (pb.FaceAnalyzeResponse, error) {
	b.served++
	return pb.FaceAnalyzeResponse{}, nil
}

func (b *modalityBackend) VoiceVerify(*pb.VoiceVerifyRequest) (pb.VoiceVerifyResponse, error) {
	b.served++
	return pb.VoiceVerifyResponse{}, nil
}

func (b *modalityBackend) VoiceAnalyze(*pb.VoiceAnalyzeRequest) (pb.VoiceAnalyzeResponse, error) {
	b.served++
	return pb.VoiceAnalyzeResponse{}, nil
}

func (b *modalityBackend) VoiceEmbed(*pb.VoiceEmbedRequest) (pb.VoiceEmbedResponse, error) {
	b.served++
	return pb.VoiceEmbedResponse{}, nil
}

func (b *modalityBackend) VAD(*pb.VADRequest) (pb.VADResponse, error) {
	b.served++
	return pb.VADResponse{}, nil
}

func (b *modalityBackend) Diarize(*pb.DiarizeRequest) (pb.DiarizeResponse, error) {
	b.served++
	return pb.DiarizeResponse{}, nil
}

func (b *modalityBackend) SoundDetection(context.Context, *pb.SoundDetectionRequest) (*pb.SoundDetectionResponse, error) {
	b.served++
	return &pb.SoundDetectionResponse{}, nil
}

func (b *modalityBackend) AudioTransform(*pb.AudioTransformRequest) (*pb.AudioTransformResult, error) {
	b.served++
	return &pb.AudioTransformResult{}, nil
}

var _ AIModel = (*modalityBackend)(nil)

// callAllModalities exercises every remaining inference RPC that the generic Go
// server implements, sending `identity` in each request's ModelIdentity field.
// One call per RPC, so the returned error count is also the RPC count.
//
// Rerank, Score and TokenClassify are absent on purpose: the generic Go server
// does not implement them (they fall through to UnimplementedBackendServer), so
// only the C++ and Python backends can enforce them.
func callAllModalities(c Backend, identity string) map[string]error {
	ctx := context.Background()
	errs := map[string]error{}

	_, errs["GenerateImage"] = c.GenerateImage(ctx, &pb.GenerateImageRequest{ModelIdentity: identity})
	_, errs["GenerateVideo"] = c.GenerateVideo(ctx, &pb.GenerateVideoRequest{ModelIdentity: identity})
	_, errs["TTS"] = c.TTS(ctx, &pb.TTSRequest{ModelIdentity: identity})
	errs["TTSStream"] = c.TTSStream(ctx, &pb.TTSRequest{ModelIdentity: identity}, func(*pb.Reply) {})
	_, errs["SoundGeneration"] = c.SoundGeneration(ctx, &pb.SoundGenerationRequest{ModelIdentity: identity})
	_, errs["AudioTranscription"] = c.AudioTranscription(ctx, &pb.TranscriptRequest{ModelIdentity: identity})
	errs["AudioTranscriptionStream"] = c.AudioTranscriptionStream(ctx, &pb.TranscriptRequest{ModelIdentity: identity}, func(*pb.TranscriptStreamResponse) {})
	_, errs["Detect"] = c.Detect(ctx, &pb.DetectOptions{ModelIdentity: identity})
	_, errs["Depth"] = c.Depth(ctx, &pb.DepthRequest{ModelIdentity: identity})
	_, errs["FaceVerify"] = c.FaceVerify(ctx, &pb.FaceVerifyRequest{ModelIdentity: identity})
	_, errs["FaceAnalyze"] = c.FaceAnalyze(ctx, &pb.FaceAnalyzeRequest{ModelIdentity: identity})
	_, errs["VoiceVerify"] = c.VoiceVerify(ctx, &pb.VoiceVerifyRequest{ModelIdentity: identity})
	_, errs["VoiceAnalyze"] = c.VoiceAnalyze(ctx, &pb.VoiceAnalyzeRequest{ModelIdentity: identity})
	_, errs["VoiceEmbed"] = c.VoiceEmbed(ctx, &pb.VoiceEmbedRequest{ModelIdentity: identity})
	_, errs["VAD"] = c.VAD(ctx, &pb.VADRequest{ModelIdentity: identity})
	_, errs["Diarize"] = c.Diarize(ctx, &pb.DiarizeRequest{ModelIdentity: identity})
	_, errs["SoundDetection"] = c.SoundDetection(ctx, &pb.SoundDetectionRequest{ModelIdentity: identity})
	_, errs["AudioTransform"] = c.AudioTransform(ctx, &pb.AudioTransformRequest{ModelIdentity: identity})

	return errs
}

// #10970 protected only the four PredictOptions RPCs. Every other modality
// shares the exposure — the controller's cached row names a host:port, the port
// can be recycled by another model's backend, and a liveness-only probe cannot
// tell the difference — so each one must reject a request naming another model
// rather than answer from whatever it holds.
var _ = Describe("modality RPC model identity guard", func() {
	newServed := func(addr, loadedModel string) (Backend, *modalityBackend) {
		b := &modalityBackend{}
		Provide(addr, b)
		c := NewClient(addr, true, nil, false)
		_, err := c.LoadModel(context.Background(), &pb.ModelOptions{Model: loadedModel})
		Expect(err).ToNot(HaveOccurred())
		Expect(b.loaded).To(Equal(loadedModel))
		return c, b
	}

	It("rejects every modality RPC when the identity names another model", func() {
		c, b := newServed("test://modality-mismatch", "a.gguf")

		for rpc, err := range callAllModalities(c, "b.gguf") {
			Expect(err).To(HaveOccurred(), "%s must reject a wrong-model request", rpc)
			Expect(grpcerrors.IsModelMismatch(err)).To(BeTrue(), "%s: want a mismatch error, got %v", rpc, err)
			// The router reacts differently to the two signals, so a mismatch
			// must never be mistaken for a not-loaded.
			Expect(grpcerrors.IsModelNotLoaded(err)).To(BeFalse(), "%s", rpc)
		}
		Expect(b.served).To(Equal(0), "no request may reach the model on a mismatch")
	})

	It("serves when the identity matches the loaded model", func() {
		c, b := newServed("test://modality-match", "a.gguf")

		errs := callAllModalities(c, "a.gguf")
		for rpc, err := range errs {
			Expect(err).ToNot(HaveOccurred(), "%s", rpc)
		}
		Expect(b.served).To(Equal(len(errs)))
	})

	// Compatibility, old controller -> new backend. Every existing deployment
	// sends no identity, and tests/e2e-backends/backend_test.go drives real
	// backends with bare request structs at many call sites. Tightening this
	// breaks all of them, so it must fail here first.
	It("serves when the request carries no identity", func() {
		c, b := newServed("test://modality-empty-request", "a.gguf")

		errs := callAllModalities(c, "")
		for rpc, err := range errs {
			Expect(err).ToNot(HaveOccurred(), "%s", rpc)
		}
		Expect(b.served).To(Equal(len(errs)))
	})

	// The backend side of the same rule: a model loaded without an identity
	// (an old controller did the load) cannot judge anything, so it must serve.
	It("serves when the backend has no recorded identity", func() {
		c, b := newServed("test://modality-empty-loaded", "")

		errs := callAllModalities(c, "b.gguf")
		for rpc, err := range errs {
			Expect(err).ToNot(HaveOccurred(), "%s", rpc)
		}
		Expect(b.served).To(Equal(len(errs)))
	})

	// AudioEncode/AudioDecode are deliberately NOT guarded: the opus codec
	// backend they target is loaded with a literal model.WithModel("opus") and
	// has no ModelConfig, so there is no value with the structural guarantee
	// the rest of this mechanism depends on. Sending an identity that merely
	// looks right would be convention, not construction — the exact
	// false-rejection risk #10970 refused to take. Pinned here so a future
	// "completeness" pass has to make that decision deliberately.
	It("leaves the codec RPCs unguarded", func() {
		c, _ := newServed("test://modality-codec", "a.gguf")

		// Whatever these answer, it must never be a model mismatch: no
		// identity is carried, so nothing is compared.
		_, err := c.AudioEncode(context.Background(), &pb.AudioEncodeRequest{})
		Expect(grpcerrors.IsModelMismatch(err)).To(BeFalse())
		_, err = c.AudioDecode(context.Background(), &pb.AudioDecodeRequest{})
		Expect(grpcerrors.IsModelMismatch(err)).To(BeFalse())
	})
})
