package openai

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// withUsecases returns a *ModelConfigUsecase pointing at the OR of the given flags.
// Helper so each spec keeps its intent obvious.
func withUsecases(flags ...config.ModelConfigUsecase) *config.ModelConfigUsecase {
	var u config.ModelConfigUsecase
	for _, f := range flags {
		u |= f
	}
	return &u
}

var _ = Describe("prepareRealtimeConfig", func() {
	It("rejects a nil config", func() {
		code, msg, ok := prepareRealtimeConfig(nil)
		Expect(ok).To(BeFalse())
		Expect(code).To(Equal("invalid_model"))
		Expect(msg).To(ContainSubstring("not a pipeline model"))
	})

	It("rejects a model with no pipeline slots and no realtime_audio usecase", func() {
		cfg := &config.ModelConfig{Name: "plain-chat"}
		code, msg, ok := prepareRealtimeConfig(cfg)
		Expect(ok).To(BeFalse())
		Expect(code).To(Equal("invalid_model"))
		Expect(msg).To(ContainSubstring("not a pipeline model"))
	})

	It("accepts a model with a fully populated legacy pipeline", func() {
		cfg := &config.ModelConfig{
			Name: "legacy",
			Pipeline: config.Pipeline{
				VAD:           "silero",
				Transcription: "whisper",
				LLM:           "llama",
				TTS:           "piper",
			},
		}
		_, _, ok := prepareRealtimeConfig(cfg)
		Expect(ok).To(BeTrue())
		Expect(cfg.Pipeline.LLM).To(Equal("llama"), "user-supplied pipeline slot must not be overwritten")
	})

	It("accepts a self-contained realtime_audio model and self-pipelines empty slots", func() {
		cfg := &config.ModelConfig{
			Name:          "lfm2.5-audio-realtime",
			KnownUsecases: withUsecases(config.FLAG_REALTIME_AUDIO),
		}
		_, _, ok := prepareRealtimeConfig(cfg)
		Expect(ok).To(BeTrue())
		Expect(cfg.Pipeline.VAD).To(Equal("lfm2.5-audio-realtime"))
		Expect(cfg.Pipeline.Transcription).To(Equal("lfm2.5-audio-realtime"))
		Expect(cfg.Pipeline.LLM).To(Equal("lfm2.5-audio-realtime"))
		Expect(cfg.Pipeline.TTS).To(Equal("lfm2.5-audio-realtime"))
	})

	It("preserves user-pinned pipeline slots on a realtime_audio model", func() {
		// A user might want a dedicated silero-vad and let the realtime_audio
		// model own only STT/LLM/TTS.
		cfg := &config.ModelConfig{
			Name:          "lfm-with-external-vad",
			KnownUsecases: withUsecases(config.FLAG_REALTIME_AUDIO),
			Pipeline: config.Pipeline{
				VAD: "silero-vad",
			},
		}
		_, _, ok := prepareRealtimeConfig(cfg)
		Expect(ok).To(BeTrue())
		Expect(cfg.Pipeline.VAD).To(Equal("silero-vad"))
		Expect(cfg.Pipeline.Transcription).To(Equal("lfm-with-external-vad"))
		Expect(cfg.Pipeline.LLM).To(Equal("lfm-with-external-vad"))
		Expect(cfg.Pipeline.TTS).To(Equal("lfm-with-external-vad"))
	})

	It("accepts a model with at least one legacy pipeline slot set", func() {
		// Pre-existing behaviour: the gate only rejected when ALL four slots
		// were empty. Lock that in so the change doesn't tighten the gate.
		cfg := &config.ModelConfig{
			Name: "partial",
			Pipeline: config.Pipeline{
				LLM: "llama",
			},
		}
		_, _, ok := prepareRealtimeConfig(cfg)
		Expect(ok).To(BeTrue())
	})
})
