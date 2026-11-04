package openai

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A chained pipeline whose four slots all name one llama-cpp model (the shape
// of the lfm3-audio-2b-pipeline gallery entries) must work with and without a
// VAD slot. Without one, nothing detects turns, so the client commits them.
var _ = Describe("chained pipeline on one llama-cpp model", func() {
	const selfModel = "name: lfm3-chain\n" +
		"backend: llama-cpp\n" +
		"parameters:\n  model: lfm3.gguf\n" +
		"known_usecases: [chat, transcript, tts]\n" +
		"pipeline:\n" +
		"  transcription: lfm3-chain\n  llm: lfm3-chain\n  tts: lfm3-chain\n  disable_warmup: true\n"

	type env struct {
		loader    *config.ModelConfigLoader
		models    *model.ModelLoader
		appConfig *config.ApplicationConfig
	}
	setup := func(body string) (*env, *config.ModelConfig) {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "lfm3-chain.yaml"), []byte(body), 0o644)).To(Succeed())
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		state, err := system.GetSystemState(system.WithModelPath(dir))
		Expect(err).NotTo(HaveOccurred())
		e := &env{loader: loader, models: model.NewModelLoader(state), appConfig: config.NewApplicationConfig(config.WithSystemState(state))}
		cfg, err := loader.LoadModelConfigFileByNameDefaultOptions("lfm3-chain", e.appConfig)
		Expect(err).NotTo(HaveOccurred())
		return e, cfg
	}
	withVAD := func(body string) string {
		return body + "  vad: lfm3-vad\n"
	}

	It("is accepted as a pipeline model although it is neither realtime_audio nor any-to-any", func() {
		_, cfg := setup(selfModel)
		code, msg, ok := prepareRealtimeConfig(cfg)
		Expect(ok).To(BeTrue(), code+": "+msg)
		Expect(cfg.Pipeline.IsAnyToAny()).To(BeFalse())
		Expect(cfg.Pipeline.LLM).To(Equal("lfm3-chain"))
	})

	It("builds the chained model without a VAD and answers VAD calls with Unimplemented", func() {
		e, cfg := setup(selfModel)
		m, err := newModel(&cfg.Pipeline, e.loader, e.models, e.appConfig, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		w, ok := m.(*wrappedModel)
		Expect(ok).To(BeTrue(), "a chained pipeline must not take the any-to-any path")
		Expect(w.VADConfig).To(BeNil())
		Expect(w.TranscriptionConfig.Name).To(Equal("lfm3-chain"))
		Expect(w.LLMConfig.Name).To(Equal("lfm3-chain"))
		Expect(w.TTSConfig.Name).To(Equal("lfm3-chain"))
		_, err = m.VAD(context.Background(), &schema.VADRequest{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no VAD model"))
	})

	It("starts without turn detection when no VAD is named and with server_vad when one is", func() {
		_, noVAD := setup(selfModel)
		Expect(defaultSessionTurnDetection(noVAD)).To(BeNil())

		_, vad := setup(withVAD(selfModel))
		td := defaultSessionTurnDetection(vad)
		Expect(td).NotTo(BeNil())
		Expect(td.ServerVad).NotTo(BeNil())
	})

	It("rejects server_vad on a session update when the pipeline has no VAD", func() {
		e, cfg := setup(selfModel)
		m, err := newModel(&cfg.Pipeline, e.loader, e.models, e.appConfig, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		session := &Session{Model: "lfm3-chain", ModelConfig: cfg, ModelInterface: m}
		err = updateSession(session, &types.SessionUnion{Realtime: &types.RealtimeSession{
			Audio: &types.RealtimeSessionAudio{Input: &types.SessionAudioInput{
				TurnDetectionSet: true,
				TurnDetection:    &types.TurnDetectionUnion{ServerVad: &types.ServerVad{}},
			}},
		}}, e.loader, e.models, e.appConfig, nil, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("VAD"))
		Expect(session.TurnDetection).To(BeNil())

		// Turning detection off stays allowed.
		Expect(updateSession(session, &types.SessionUnion{Realtime: &types.RealtimeSession{
			Audio: &types.RealtimeSessionAudio{Input: &types.SessionAudioInput{TurnDetectionSet: true}},
		}}, e.loader, e.models, e.appConfig, nil, nil, nil)).To(Succeed())
	})
})
