package openai

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
)

// emitSoundDetection classifies a committed utterance and emits a single
// conversation.item.sound_detection event carrying the scored AudioSet tags.
var _ = Describe("emitSoundDetection", func() {
	It("emits a sound_detection event with the classifier's scored tags", func() {
		session := &Session{
			SoundDetectionEnabled: true,
			SoundDetectionTopK:    5,
			ModelInterface: &fakeModel{
				soundDetectionResult: &schema.SoundClassificationResult{
					Model: "ced",
					Detections: []schema.SoundClassification{
						{Index: 3, Label: "Baby cry, infant cry", Score: 0.91},
						{Index: 7, Label: "Speech", Score: 0.42},
					},
				},
			},
		}
		t := &fakeTransport{}

		err := emitSoundDetection(context.Background(), t, session, "item1", "/tmp/x.wav")

		Expect(err).ToNot(HaveOccurred())
		Expect(t.countEvents(types.ServerEventTypeConversationItemSoundDetection)).To(Equal(1))

		ev, ok := t.events[0].(types.ConversationItemSoundDetectionEvent)
		Expect(ok).To(BeTrue())
		Expect(ev.ItemID).To(Equal("item1"))
		Expect(ev.ContentIndex).To(Equal(0))
		Expect(ev.Detections).To(HaveLen(2))
		Expect(ev.Detections[0].Label).To(Equal("Baby cry, infant cry"))
		Expect(ev.Detections[0].Score).To(BeNumerically("~", 0.91, 1e-6))
		Expect(ev.Detections[0].Index).To(Equal(3))
		Expect(ev.Detections[1].Label).To(Equal("Speech"))
	})

	It("emits an event with no detections when the classifier returns none", func() {
		session := &Session{
			SoundDetectionEnabled: true,
			ModelInterface: &fakeModel{
				soundDetectionResult: &schema.SoundClassificationResult{Model: "ced"},
			},
		}
		t := &fakeTransport{}

		err := emitSoundDetection(context.Background(), t, session, "item1", "/tmp/x.wav")

		Expect(err).ToNot(HaveOccurred())
		Expect(t.countEvents(types.ServerEventTypeConversationItemSoundDetection)).To(Equal(1))
		ev, ok := t.events[0].(types.ConversationItemSoundDetectionEvent)
		Expect(ok).To(BeTrue())
		Expect(ev.Detections).To(BeEmpty())
	})

	It("propagates the classifier error and emits no event", func() {
		session := &Session{
			SoundDetectionEnabled: true,
			ModelInterface:        &fakeModel{soundDetectionErr: errors.New("boom")},
		}
		t := &fakeTransport{}

		err := emitSoundDetection(context.Background(), t, session, "item1", "/tmp/x.wav")

		Expect(err).To(HaveOccurred())
		Expect(t.countEvents(types.ServerEventTypeConversationItemSoundDetection)).To(Equal(0))
	})
})

// A sound-detection-only session (no transcription, no LLM) runs through
// commitUtterance WITHOUT the voice/transcription path: it emits the
// sound_detection event and stops - no transcription event, no LLM response.
var _ = Describe("commitUtterance (sound-detection-only session)", func() {
	It("emits sound detection and neither transcribes nor generates a response", func() {
		session := &Session{
			SoundDetectionEnabled:   true,
			SoundDetectionTopK:      5,
			InputAudioTranscription: nil, // sound-only: no transcription stage
			ModelConfig:             &config.ModelConfig{},
			ModelInterface: &fakeModel{
				soundDetectionResult: &schema.SoundClassificationResult{
					Model: "ced",
					Detections: []schema.SoundClassification{
						{Index: 23, Label: "Baby cry, infant cry", Score: 0.87},
					},
				},
			},
		}
		tr := &fakeTransport{}
		utt := make([]byte, 32) // non-empty PCM so commitUtterance proceeds

		commitUtterance(context.Background(), utt, session, &Conversation{}, tr)

		Expect(tr.countEvents(types.ServerEventTypeConversationItemSoundDetection)).To(Equal(1))
		// No transcription happened.
		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(0))
		// No LLM response was generated (sound-only has no LLM stage).
		Expect(tr.countEvents(types.ServerEventTypeResponseDone)).To(Equal(0))
	})
})
