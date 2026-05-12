package openai

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
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

var _ = Describe("defaultMaxHistoryItems", func() {
	It("caps realtime_audio sessions at 6", func() {
		cfg := &config.ModelConfig{KnownUsecases: withUsecases(config.FLAG_REALTIME_AUDIO)}
		Expect(defaultMaxHistoryItems(cfg)).To(Equal(6))
	})
	It("leaves legacy pipelines unlimited", func() {
		cfg := &config.ModelConfig{Pipeline: config.Pipeline{LLM: "llama"}}
		Expect(defaultMaxHistoryItems(cfg)).To(Equal(0))
	})
	It("tolerates nil", func() {
		Expect(defaultMaxHistoryItems(nil)).To(Equal(0))
	})
})

var _ = Describe("trimRealtimeItems", func() {
	user := func(id string) *types.MessageItemUnion {
		return &types.MessageItemUnion{User: &types.MessageItemUser{ID: id}}
	}
	assistant := func(id string) *types.MessageItemUnion {
		return &types.MessageItemUnion{Assistant: &types.MessageItemAssistant{ID: id}}
	}
	fnCall := func(id, callID string) *types.MessageItemUnion {
		return &types.MessageItemUnion{FunctionCall: &types.MessageItemFunctionCall{ID: id, CallID: callID}}
	}
	fnOut := func(id, callID string) *types.MessageItemUnion {
		return &types.MessageItemUnion{FunctionCallOutput: &types.MessageItemFunctionCallOutput{ID: id, CallID: callID}}
	}

	It("returns the input unchanged when cap is zero", func() {
		in := []*types.MessageItemUnion{user("u1"), assistant("a1")}
		Expect(trimRealtimeItems(in, 0)).To(Equal(in))
	})

	It("returns the input unchanged when under the cap", func() {
		in := []*types.MessageItemUnion{user("u1"), assistant("a1")}
		Expect(trimRealtimeItems(in, 4)).To(Equal(in))
	})

	It("keeps the tail when over the cap", func() {
		in := []*types.MessageItemUnion{user("u1"), assistant("a1"), user("u2"), assistant("a2"), user("u3")}
		out := trimRealtimeItems(in, 3)
		Expect(out).To(HaveLen(3))
		Expect(out[0].User.ID).To(Equal("u2"))
		Expect(out[2].User.ID).To(Equal("u3"))
	})

	It("pulls the cut left to keep a function_call paired with its output", func() {
		// 0:user 1:fc 2:fc_out 3:assistant — cap=2 would otherwise start at
		// index 2 (orphan fc_out). Helper must roll back to include 1.
		in := []*types.MessageItemUnion{user("u1"), fnCall("fc1", "c1"), fnOut("fo1", "c1"), assistant("a1")}
		out := trimRealtimeItems(in, 2)
		// Expect at least the fc + fc_out + assistant (3 items, cap was 2)
		// — the rollback prefers correctness over the cap.
		Expect(len(out)).To(BeNumerically(">=", 3))
		Expect(out[0].FunctionCall).NotTo(BeNil())
		Expect(out[1].FunctionCallOutput).NotTo(BeNil())
	})
})
