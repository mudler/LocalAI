package openai

import (
	"context"
	"time"

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

	Context("pipeline.diarization", func() {
		labelled := &schema.TranscriptionResult{
			Text: "hi there. hello",
			Segments: []schema.TranscriptionSegment{
				{Id: 0, Text: "hi there.", Start: 0, End: 600 * time.Millisecond, Speaker: "0"},
				{Id: 1, Text: "hello", Start: time.Second, End: 1400 * time.Millisecond, Speaker: "1"},
				{Id: 2, Text: "unlabelled"},
			},
		}

		segmentEvents := func(t *fakeTransport) []types.ConversationItemInputAudioTranscriptionSegmentEvent {
			var out []types.ConversationItemInputAudioTranscriptionSegmentEvent
			for _, e := range t.recordedEvents() {
				if seg, ok := e.(types.ConversationItemInputAudioTranscriptionSegmentEvent); ok {
					out = append(out, seg)
				}
			}
			return out
		}

		It("requests speakers and emits one segment event per labelled segment", func() {
			m := &fakeModel{transcribeFinal: labelled}
			session := &Session{
				InputAudioTranscription: &types.AudioTranscription{},
				ModelConfig:             &config.ModelConfig{Pipeline: config.Pipeline{Diarization: true}},
				ModelInterface:          m,
			}
			t := &fakeTransport{}

			transcript, err := emitTranscription(context.Background(), t, session, "item1", "/tmp/x.wav")

			Expect(err).ToNot(HaveOccurred())
			Expect(transcript).To(Equal("hi there. hello"))
			Expect(m.lastDiarize).To(BeTrue())
			segs := segmentEvents(t)
			Expect(segs).To(HaveLen(2))
			Expect(segs[0].ItemID).To(Equal("item1"))
			Expect(segs[0].Speaker).To(Equal("0"))
			Expect(segs[0].Text).To(Equal("hi there."))
			Expect(segs[1].Speaker).To(Equal("1"))
			Expect(segs[1].Start).To(BeNumerically("~", 1.0, 1e-9))
			Expect(segs[1].End).To(BeNumerically("~", 1.4, 1e-9))
			Expect(t.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
		})

		It("also emits segment events on the streaming transcription path", func() {
			on := true
			m := &fakeModel{transcribeDeltas: []string{"hi"}, transcribeFinal: labelled}
			session := &Session{
				InputAudioTranscription: &types.AudioTranscription{},
				ModelConfig: &config.ModelConfig{Pipeline: config.Pipeline{
					Diarization: true,
					Streaming:   config.PipelineStreaming{Transcription: &on},
				}},
				ModelInterface: m,
			}
			t := &fakeTransport{}

			_, err := emitTranscription(context.Background(), t, session, "item1", "/tmp/x.wav")

			Expect(err).ToNot(HaveOccurred())
			Expect(m.lastDiarize).To(BeTrue())
			Expect(segmentEvents(t)).To(HaveLen(2))
		})

		It("neither asks for speakers nor emits segments when off", func() {
			m := &fakeModel{transcribeFinal: labelled}
			session := &Session{
				InputAudioTranscription: &types.AudioTranscription{},
				ModelConfig:             &config.ModelConfig{},
				ModelInterface:          m,
			}
			t := &fakeTransport{}

			_, err := emitTranscription(context.Background(), t, session, "item1", "/tmp/x.wav")

			Expect(err).ToNot(HaveOccurred())
			Expect(m.lastDiarize).To(BeFalse())
			Expect(segmentEvents(t)).To(BeEmpty())
		})
	})
})
