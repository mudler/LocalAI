package openai

import (
	"context"
	"encoding/binary"
	"errors"
	"os"

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

// Server-side windowing (option B): a sound-only session classifies the last
// WindowMs of streamed audio per tick, with no client commit, and keeps the
// input buffer trimmed to one window.
var _ = Describe("classifySoundWindow (server-side windowing)", func() {
	newSoundSession := func() (*Session, *fakeTransport) {
		return &Session{
			SoundDetectionEnabled:  true,
			SoundDetectionTopK:     5,
			SoundDetectionWindowMs: 200, // 200ms @ 16kHz mono16 = 6400 bytes
			SoundDetectionHopMs:    20,
			InputSampleRate:        16000,
			ModelInterface: &fakeModel{
				soundDetectionResult: &schema.SoundClassificationResult{
					Model:      "ced",
					Detections: []schema.SoundClassification{{Index: 23, Label: "Baby cry, infant cry", Score: 0.87}},
				},
			},
		}, &fakeTransport{}
	}

	It("emits a sound_detection event and trims the buffer to one window", func() {
		session, tr := newSoundSession()
		session.InputAudioBuffer = make([]byte, 10000) // > 6400-byte window

		classifySoundWindow(session, tr)

		Expect(tr.countEvents(types.ServerEventTypeConversationItemSoundDetection)).To(Equal(1))
		// buffer trimmed to exactly one window (200ms @ 16kHz mono 16-bit)
		Expect(len(session.InputAudioBuffer)).To(Equal(6400))
	})

	It("does nothing when too little audio is buffered", func() {
		session, tr := newSoundSession()
		session.InputAudioBuffer = make([]byte, 100) // < ~10ms (320 bytes)

		classifySoundWindow(session, tr)

		Expect(tr.countEvents(types.ServerEventTypeConversationItemSoundDetection)).To(Equal(0))
	})
})

var _ = Describe("writeWindowWAV", func() {
	It("writes a mono 16-bit WAV header declaring the given sample rate", func() {
		pcm := make([]byte, 640)
		path, err := writeWindowWAV(pcm, 24000)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = os.Remove(path) }()

		data, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(data)).To(BeNumerically(">=", 44+len(pcm)))
		// SampleRate is a little-endian uint32 at byte offset 24 of a WAV header.
		Expect(binary.LittleEndian.Uint32(data[24:28])).To(Equal(uint32(24000)))
	})
})
