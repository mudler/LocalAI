package openai

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/pkg/reasoning"
)

// speechStreamer consumes streamed LLM tokens: it strips reasoning, emits a
// transcript delta per content fragment, and sentence-pipes content into TTS so
// audio starts before the full reply is generated.
var _ = Describe("speechStreamer", func() {
	It("emits a transcript delta per token and speaks each completed sentence", func() {
		on := true
		m := &fakeModel{ttsStreamChunks: [][]byte{{7}}, ttsStreamRate: 24000}
		session := &Session{
			OutputSampleRate: 24000,
			ModelInterface:   m,
			ModelConfig: &config.ModelConfig{
				Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{TTS: &on}},
			},
		}
		t := &fakeTransport{}
		s := newSpeechStreamer(context.Background(), t, session, "resp1", "item1", "", reasoning.Config{})

		for _, tok := range []string{"Hello", " world.", " Bye"} {
			s.onToken(tok)
		}
		content, audio, err := s.finish()

		Expect(err).ToNot(HaveOccurred())
		Expect(content).To(Equal("Hello world. Bye"))
		// One transcript delta per (non-empty) token.
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioTranscriptDelta)).To(Equal(3))
		// Two sentences spoken: "Hello world." mid-stream + "Bye" on flush; one
		// chunk each.
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(2))
		Expect(audio).To(Equal([]byte{7, 7}))
	})

	It("does not synthesize audio when TTS streaming is disabled", func() {
		m := &fakeModel{ttsStreamChunks: [][]byte{{7}}, ttsStreamRate: 24000}
		session := &Session{
			OutputSampleRate: 24000,
			ModelInterface:   m,
			ModelConfig:      &config.ModelConfig{}, // streaming.tts off
		}
		t := &fakeTransport{}
		s := newSpeechStreamer(context.Background(), t, session, "resp1", "item1", "", reasoning.Config{})

		s.onToken("Hello world.")
		content, audio, err := s.finish()

		Expect(err).ToNot(HaveOccurred())
		Expect(content).To(Equal("Hello world."))
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioTranscriptDelta)).To(Equal(1))
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(0))
		Expect(audio).To(BeEmpty())
	})
})
