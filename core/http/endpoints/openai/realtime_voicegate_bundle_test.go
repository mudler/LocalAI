package openai

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
)

// embedBackend plays the parakeet-cpp backend of a bundle: it answers VoiceEmbed
// the way the real backend does, with a vector and the encoder identity.
type embedBackend struct {
	grpcPkg.Backend
	requests []*proto.VoiceEmbedRequest
}

func (b *embedBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *embedBackend) IsBusy() bool                              { return false }
func (b *embedBackend) VoiceEmbed(_ context.Context, req *proto.VoiceEmbedRequest, _ ...grpc.CallOption) (*proto.VoiceEmbedResponse, error) {
	b.requests = append(b.requests, req)
	return &proto.VoiceEmbedResponse{Embedding: []float32{1, 0, 0}, Model: "sha256:abcd"}, nil
}

// One bundle model can name every stage of a realtime pipeline, the speaker
// gate included.
var _ = Describe("realtime pipeline with a bundle model on every stage", func() {
	It("builds the pipeline and resolves speakers through the bundle's VoiceEmbed", func() {
		dir := GinkgoT().TempDir()
		configs := map[string]string{
			"bundle": `name: bundle
backend: parakeet-cpp
parameters:
  model: parakeet-cpp/bundle.gguf
known_usecases: [transcript, vad, sound_classification, speaker_recognition]
options: ["vad:true", "diar_component:diar", "sound_component:ced", "speaker_component:voice"]
`,
			"llm": "name: llm\nbackend: test\nparameters:\n  model: llm.bin\n",
			"tts": "name: tts\nbackend: test\nparameters:\n  model: tts.bin\n",
			"pipe": `name: pipe
pipeline:
  vad: bundle
  transcription: bundle
  sound_detection: bundle
  llm: llm
  tts: tts
  disable_warmup: true
  voice_recognition:
    model: bundle
    mode: identify
    threshold: 0.5
`,
		}
		for name, body := range configs {
			Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o644)).To(Succeed())
		}
		cl := config.NewModelConfigLoader(dir)
		Expect(cl.LoadModelConfigsFromPath(dir)).To(Succeed())
		state, err := system.GetSystemState(system.WithModelPath(dir))
		Expect(err).NotTo(HaveOccurred())
		appConfig := config.NewApplicationConfig(config.WithSystemState(state))
		ml := model.NewModelLoader(state)

		bundleCfg, ok := cl.GetModelConfig("bundle")
		Expect(ok).To(BeTrue())
		Expect(bundleCfg.HasUsecases(config.FLAG_SPEAKER_RECOGNITION)).To(BeTrue())
		fake := &embedBackend{}
		loaded := model.NewModelWithClient(bundleCfg.ModelID(), "in-process", fake)
		loaded.MarkHealthy()
		_, err = ml.LoadModel(bundleCfg.ModelID(), bundleCfg.Model, func(_, _, _ string) (*model.Model, error) { return loaded, nil })
		Expect(err).NotTo(HaveOccurred())

		pipe, err := cl.LoadModelConfigFileByNameDefaultOptions("pipe", appConfig)
		Expect(err).NotTo(HaveOccurred())
		_, err = newModel(&pipe.Pipeline, cl, ml, appConfig, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		registry := &fakeRegistry{matches: []voicerecognition.Match{
			{Distance: 0.1, Metadata: voicerecognition.Metadata{Name: "alice"}},
		}}
		gate, err := newVoiceGate(*pipe.Pipeline.VoiceRecognition, cl, ml, appConfig, registry)
		Expect(err).NotTo(HaveOccurred())

		res, err := gate.Resolve(context.Background(), "utterance.wav")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.speaker.Name).To(Equal("alice"))
		Expect(fake.requests).To(HaveLen(1))
		Expect(fake.requests[0].Audio).To(Equal("utterance.wav"))
	})
})
