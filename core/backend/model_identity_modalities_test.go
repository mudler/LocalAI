package backend_test

// Functional specs for the model identity every modality request must carry
// (#10952, extending #10970 beyond the four PredictOptions RPCs).
//
// The whole mechanism is only safe because the predict-time identity is the
// SAME expression as the load-time one: ModelOptions passes
// model.WithModel(c.Model), which becomes ModelOptions.Model at LoadModel, and
// each helper below builds its request in the same function from the same
// config value. If a helper ever sends ModelID()/Name or a resolved file path
// instead, every request to a correctly-routed backend gets rejected — so the
// config here deliberately gives Model, Name and ModelID() three different
// values, and these specs assert on the recorded wire value rather than on the
// construction site.

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	ggrpc "google.golang.org/grpc"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	identityModelFile = "qwen/actual-weights.gguf"
	identityModelName = "friendly-name"
)

// recordingBackend captures the ModelIdentity each modality request arrives
// with. It embeds grpc.Backend so only the methods under test need declaring;
// anything else would nil-panic, which is the desired signal if a spec starts
// calling an unexpected RPC.
type recordingBackend struct {
	grpcPkg.Backend

	seen map[string]string

	// SoundGenerationRequest carries both `model` (staged path) and
	// `ModelIdentity`; the spec below asserts they are populated independently.
	soundGenModelField string
}

func newRecordingBackend() *recordingBackend {
	return &recordingBackend{seen: map[string]string{}}
}

// The loader probes liveness before handing the client back, so these two must
// answer even though no spec asserts on them.
func (r *recordingBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (r *recordingBackend) IsBusy() bool                              { return false }

func (r *recordingBackend) record(rpc string, in interface{ GetModelIdentity() string }) {
	r.seen[rpc] = in.GetModelIdentity()
}

func (r *recordingBackend) GenerateImage(_ context.Context, in *pb.GenerateImageRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	r.record("GenerateImage", in)
	return &pb.Result{Success: true}, nil
}

func (r *recordingBackend) SoundGeneration(_ context.Context, in *pb.SoundGenerationRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	r.record("SoundGeneration", in)
	r.soundGenModelField = in.GetModel()
	return &pb.Result{Success: true}, nil
}

func (r *recordingBackend) GenerateVideo(_ context.Context, in *pb.GenerateVideoRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	r.record("GenerateVideo", in)
	return &pb.Result{Success: true}, nil
}

func (r *recordingBackend) Detect(_ context.Context, in *pb.DetectOptions, _ ...ggrpc.CallOption) (*pb.DetectResponse, error) {
	r.record("Detect", in)
	return &pb.DetectResponse{}, nil
}

func (r *recordingBackend) Depth(_ context.Context, in *pb.DepthRequest, _ ...ggrpc.CallOption) (*pb.DepthResponse, error) {
	r.record("Depth", in)
	return &pb.DepthResponse{}, nil
}

func (r *recordingBackend) VAD(_ context.Context, in *pb.VADRequest, _ ...ggrpc.CallOption) (*pb.VADResponse, error) {
	r.record("VAD", in)
	return &pb.VADResponse{}, nil
}

func (r *recordingBackend) Diarize(_ context.Context, in *pb.DiarizeRequest, _ ...ggrpc.CallOption) (*pb.DiarizeResponse, error) {
	r.record("Diarize", in)
	return &pb.DiarizeResponse{}, nil
}

func (r *recordingBackend) SoundDetection(_ context.Context, in *pb.SoundDetectionRequest, _ ...ggrpc.CallOption) (*pb.SoundDetectionResponse, error) {
	r.record("SoundDetection", in)
	return &pb.SoundDetectionResponse{}, nil
}

func (r *recordingBackend) AudioTranscription(_ context.Context, in *pb.TranscriptRequest, _ ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	r.record("AudioTranscription", in)
	return &pb.TranscriptResult{}, nil
}

func (r *recordingBackend) AudioTranscriptionStream(_ context.Context, in *pb.TranscriptRequest, _ func(*pb.TranscriptStreamResponse), _ ...ggrpc.CallOption) error {
	r.record("AudioTranscriptionStream", in)
	return nil
}

func (r *recordingBackend) Rerank(_ context.Context, in *pb.RerankRequest, _ ...ggrpc.CallOption) (*pb.RerankResult, error) {
	r.record("Rerank", in)
	return &pb.RerankResult{Usage: &pb.Usage{}}, nil
}

func (r *recordingBackend) Score(_ context.Context, in *pb.ScoreRequest, _ ...ggrpc.CallOption) (*pb.ScoreResponse, error) {
	r.record("Score", in)
	return &pb.ScoreResponse{}, nil
}

func (r *recordingBackend) TokenClassify(_ context.Context, in *pb.TokenClassifyRequest, _ ...ggrpc.CallOption) (*pb.TokenClassifyResponse, error) {
	r.record("TokenClassify", in)
	return &pb.TokenClassifyResponse{}, nil
}

func (r *recordingBackend) FaceVerify(_ context.Context, in *pb.FaceVerifyRequest, _ ...ggrpc.CallOption) (*pb.FaceVerifyResponse, error) {
	r.record("FaceVerify", in)
	return &pb.FaceVerifyResponse{}, nil
}

func (r *recordingBackend) FaceAnalyze(_ context.Context, in *pb.FaceAnalyzeRequest, _ ...ggrpc.CallOption) (*pb.FaceAnalyzeResponse, error) {
	r.record("FaceAnalyze", in)
	return &pb.FaceAnalyzeResponse{}, nil
}

func (r *recordingBackend) VoiceVerify(_ context.Context, in *pb.VoiceVerifyRequest, _ ...ggrpc.CallOption) (*pb.VoiceVerifyResponse, error) {
	r.record("VoiceVerify", in)
	return &pb.VoiceVerifyResponse{}, nil
}

func (r *recordingBackend) VoiceAnalyze(_ context.Context, in *pb.VoiceAnalyzeRequest, _ ...ggrpc.CallOption) (*pb.VoiceAnalyzeResponse, error) {
	r.record("VoiceAnalyze", in)
	return &pb.VoiceAnalyzeResponse{}, nil
}

func (r *recordingBackend) AudioTransform(_ context.Context, in *pb.AudioTransformRequest, _ ...ggrpc.CallOption) (*pb.AudioTransformResult, error) {
	r.record("AudioTransform", in)
	return &pb.AudioTransformResult{}, nil
}

func (r *recordingBackend) VoiceEmbed(_ context.Context, in *pb.VoiceEmbedRequest, _ ...ggrpc.CallOption) (*pb.VoiceEmbedResponse, error) {
	r.record("VoiceEmbed", in)
	return &pb.VoiceEmbedResponse{Embedding: []float32{1}}, nil
}

// newRecordingLoader wires a ModelLoader whose router hands back the recording
// backend, so every helper below reaches it through the real Load path.
func newRecordingLoader(rec *recordingBackend) *model.ModelLoader {
	loader := model.NewModelLoader(&system.SystemState{})
	loader.SetModelRouter(func(_ context.Context, id string, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
		return model.NewModelWithClient(id, "test://recording", rec), nil
	})
	return loader
}

func identityModelCfg() config.ModelConfig {
	threads := 1
	cfg := config.ModelConfig{
		Name:    identityModelName,
		Backend: "stub-backend",
		Threads: &threads,
	}
	cfg.SetDefaults()
	cfg.Name = identityModelName
	cfg.Model = identityModelFile
	return cfg
}

var _ = Describe("modality requests carry the model identity", func() {
	var (
		rec    *recordingBackend
		loader *model.ModelLoader
		appCfg *config.ApplicationConfig
		cfg    config.ModelConfig
		ctx    context.Context
	)

	BeforeEach(func() {
		rec = newRecordingBackend()
		loader = newRecordingLoader(rec)
		appCfg = config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
		cfg = identityModelCfg()
		ctx = context.Background()
	})

	// Sanity: the three candidate values really are distinct here, so an
	// assertion on ModelConfig.Model cannot pass by coincidence.
	It("uses a config whose Model, Name and ModelID differ", func() {
		Expect(cfg.Model).To(Equal(identityModelFile))
		Expect(cfg.Name).To(Equal(identityModelName))
		Expect(cfg.ModelID()).ToNot(Equal(cfg.Model))
	})

	expectIdentity := func(rpc string) {
		Expect(rec.seen).To(HaveKey(rpc), "%s never reached the backend", rpc)
		Expect(rec.seen[rpc]).To(Equal(identityModelFile),
			"%s must send ModelConfig.Model, the value LoadModel receives", rpc)
	}

	It("ImageGeneration", func() {
		fn, err := backend.ImageGeneration(ctx, 1, 1, 1, 1, "p", "n", "", "", loader, cfg, appCfg, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(fn()).To(Succeed())
		expectIdentity("GenerateImage")
	})

	It("VideoGeneration", func() {
		fn, err := backend.VideoGeneration(backend.VideoGenerationOptions{Prompt: "p"}, loader, cfg, appCfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(fn()).To(Succeed())
		expectIdentity("GenerateVideo")
	})

	It("Detection", func() {
		_, err := backend.Detection(ctx, "src.png", "", nil, nil, 0, loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("Detect")
	})

	It("Depth", func() {
		_, err := backend.Depth(ctx, &pb.DepthRequest{Src: "src.png"}, loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("Depth")
	})

	It("VAD", func() {
		_, err := backend.VAD(&schema.VADRequest{Audio: []float32{0}}, ctx, loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("VAD")
	})

	It("ModelDiarization", func() {
		_, err := backend.ModelDiarization(ctx, backend.DiarizationRequest{Audio: "a.wav"}, loader, cfg, appCfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("Diarize")
	})

	It("ModelSoundDetection", func() {
		_, err := backend.ModelSoundDetection(ctx, backend.SoundDetectionRequest{Audio: "a.wav"}, loader, cfg, appCfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("SoundDetection")
	})

	It("ModelTranscription", func() {
		_, err := backend.ModelTranscription(ctx, "a.wav", "en", false, false, "", loader, cfg, appCfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("AudioTranscription")
	})

	It("ModelTranscriptionStream", func() {
		err := backend.ModelTranscriptionStream(ctx, backend.TranscriptionRequest{Audio: "a.wav"}, loader, cfg, appCfg, func(backend.TranscriptionStreamChunk) {})
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("AudioTranscriptionStream")
	})

	It("Rerank", func() {
		_, err := backend.Rerank(ctx, &pb.RerankRequest{Query: "q", Documents: []string{"d"}}, loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("Rerank")
	})

	It("ModelScore", func() {
		fn, err := backend.ModelScore("p", []string{"c"}, backend.ScoreOptions{}, loader, cfg, appCfg)
		Expect(err).ToNot(HaveOccurred())
		_, err = fn(ctx)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("Score")
	})

	It("ModelTokenClassify", func() {
		fn, err := backend.ModelTokenClassify("text", backend.TokenClassifyOptions{}, loader, cfg, appCfg)
		Expect(err).ToNot(HaveOccurred())
		_, err = fn(ctx)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("TokenClassify")
	})

	It("FaceVerify", func() {
		_, err := backend.FaceVerify(ctx, "img1", "img2", 0, false, loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("FaceVerify")
	})

	It("FaceAnalyze", func() {
		_, err := backend.FaceAnalyze(ctx, "img", nil, false, loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("FaceAnalyze")
	})

	It("VoiceVerify", func() {
		_, err := backend.VoiceVerify(ctx, "a1.wav", "a2.wav", 0, false, loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("VoiceVerify")
	})

	It("VoiceAnalyze", func() {
		_, err := backend.VoiceAnalyze(ctx, "a.wav", nil, loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("VoiceAnalyze")
	})

	// SoundGeneration is the second RPC (with TTS) whose request already had a
	// `model` field before this change. Identity must be a SEPARATE field: the
	// distributed file stager rewrites `model` to a worker-local path, so
	// comparing it would reject valid requests. Both must arrive populated.
	It("SoundGeneration", func() {
		appCfg.GeneratedContentDir = GinkgoT().TempDir()
		_, _, err := backend.SoundGeneration(ctx, "hello", nil, nil, nil, nil, nil, nil, "", "", nil, "", "", "", nil, loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("SoundGeneration")
		Expect(rec.soundGenModelField).To(Equal(identityModelFile),
			"the staged `model` field must still be sent, separately from the identity")
	})

	It("ModelAudioTransform", func() {
		dir := GinkgoT().TempDir()
		appCfg.GeneratedContentDir = dir
		src := filepath.Join(dir, "in.wav")
		Expect(os.WriteFile(src, []byte("RIFF"), 0o600)).To(Succeed())
		_, _, err := backend.ModelAudioTransform(ctx, src, "", backend.AudioTransformOptions{}, loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("AudioTransform")
	})

	It("VoiceEmbed", func() {
		_, err := backend.VoiceEmbed(ctx, "a.wav", loader, appCfg, cfg)
		Expect(err).ToNot(HaveOccurred())
		expectIdentity("VoiceEmbed")
	})
})
