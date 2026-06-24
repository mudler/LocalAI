package openai

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
)

// emitTranscription transcribes a committed utterance, streaming transcript text
// deltas when the pipeline opts in, and returns the final transcript text.
var _ = Describe("emitTranscription", func() {
	It("streams transcription deltas then a completed event when streaming is enabled", func() {
		on := true
		session := &Session{
			InputAudioTranscription: &types.AudioTranscription{},
			ModelConfig: &config.ModelConfig{
				Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{Transcription: &on}},
			},
			ModelInterface: &fakeModel{
				transcribeDeltas: []string{"Hel", "lo", " world"},
				transcribeFinal:  &schema.TranscriptionResult{Text: "Hello world"},
			},
		}
		t := &fakeTransport{}

		transcript, err := emitTranscription(context.Background(), t, session, "item1", "/tmp/x.wav")

		Expect(err).ToNot(HaveOccurred())
		Expect(transcript).To(Equal("Hello world"))
		Expect(t.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionDelta)).To(Equal(3))
		Expect(t.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
	})

	It("emits a single completed event with no deltas in unary mode", func() {
		session := &Session{
			InputAudioTranscription: &types.AudioTranscription{},
			ModelConfig:             &config.ModelConfig{}, // streaming off
			ModelInterface:          &fakeModel{transcribeFinal: &schema.TranscriptionResult{Text: "Hi"}},
		}
		t := &fakeTransport{}

		transcript, err := emitTranscription(context.Background(), t, session, "item1", "/tmp/x.wav")

		Expect(err).ToNot(HaveOccurred())
		Expect(transcript).To(Equal("Hi"))
		Expect(t.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionDelta)).To(Equal(0))
		Expect(t.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
	})
})
