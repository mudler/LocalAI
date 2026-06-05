package openai

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/pkg/reasoning"
)

// transcriptStreamer turns streamed LLM tokens into incremental transcript
// deltas, stripping reasoning. Audio is synthesized once from the full message
// by the caller, so there is no per-sentence segmentation.
var _ = Describe("transcriptStreamer", func() {
	It("emits one transcript delta per content token", func() {
		t := &fakeTransport{}
		s := newTranscriptStreamer(context.Background(), t, "resp1", "item1", "", reasoning.Config{})

		for _, tok := range []string{"Hello", " world.", " Bye"} {
			s.onToken(tok)
		}

		Expect(s.content()).To(Equal("Hello world. Bye"))
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioTranscriptDelta)).To(Equal(3))
		Expect(t.transcriptDeltaText()).To(Equal("Hello world. Bye"))
	})

	It("strips leaked reasoning even when reasoning is disabled (disable_thinking safety net)", func() {
		// disable_thinking maps to DisableReasoning=true (enable_thinking=false to
		// the backend). If the model emits thinking anyway, the transcript must
		// still not leak it: stripping always runs for spoken output.
		disable := true
		t := &fakeTransport{}
		s := newTranscriptStreamer(context.Background(), t, "resp1", "item1", "",
			reasoning.Config{DisableReasoning: &disable})

		s.onToken("<think>secret plan</think>")
		s.onToken("The answer is 42.")

		Expect(s.content()).To(Equal("The answer is 42."))
		Expect(s.content()).ToNot(ContainSubstring("secret plan"))
		Expect(t.transcriptDeltaText()).ToNot(ContainSubstring("secret plan"))
	})
})

// streamLLMResponse drives a full streamed realtime turn: live transcript
// deltas while the LLM generates, then the whole message is synthesized once.
var _ = Describe("streamLLMResponse", func() {
	It("streams transcript deltas then synthesizes the whole message once", func() {
		on := true
		m := &fakeModel{
			predictTokens:   []string{"Hello", " world.", " How are you?"},
			predictResp:     backend.LLMResponse{Response: "Hello world. How are you?"},
			ttsStreamChunks: [][]byte{{9}},
			ttsStreamRate:   24000,
		}
		session := &Session{
			OutputSampleRate: 24000,
			ModelInterface:   m,
			ModelConfig: &config.ModelConfig{
				Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{LLM: &on, TTS: &on}},
			},
		}
		conv := &Conversation{}
		t := &fakeTransport{}
		llmCfg := &config.ModelConfig{}

		handled := streamLLMResponse(context.Background(), session, conv, t, "resp1", nil, nil, llmCfg)

		Expect(handled).To(BeTrue())
		// One live transcript delta per streamed token.
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioTranscriptDelta)).To(Equal(3))
		// The whole message is synthesized ONCE (not per sentence): a single
		// emitSpeech replays the one TTS stream chunk.
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(1))
		Expect(t.transcriptDeltaText()).To(Equal("Hello world. How are you?"))
	})
})
