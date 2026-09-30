package voicerecognition_test

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/voicerecognition"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func entry(name, model string, emb ...float32) voicerecognition.Entry {
	return voicerecognition.Entry{
		Metadata:  voicerecognition.Metadata{ID: name, Name: name, Model: model},
		Embedding: emb,
	}
}

var _ = Describe("SpeakerModelFromOptions", func() {
	It("reads the speaker_model option", func() {
		Expect(voicerecognition.SpeakerModelFromOptions([]string{"diarization_model:d.gguf", "speaker_model: voice-detect-wespeaker-resnet34.gguf "})).
			To(Equal("voice-detect-wespeaker-resnet34.gguf"))
	})
	It("is empty without the option", func() {
		Expect(voicerecognition.SpeakerModelFromOptions([]string{"diarization_model:d.gguf"})).To(BeEmpty())
		Expect(voicerecognition.SpeakerModelFromOptions(nil)).To(BeEmpty())
	})
})

var _ = Describe("EncoderTag", func() {
	It("is the lowercased basename", func() {
		Expect(voicerecognition.EncoderTag("models/Voice-Detect-WeSpeaker.GGUF")).To(Equal("voice-detect-wespeaker.gguf"))
		Expect(voicerecognition.EncoderTag("voice-detect-ecapa-tdnn-voxceleb.gguf")).To(Equal("voice-detect-ecapa-tdnn-voxceleb.gguf"))
	})
})

var _ = Describe("SelectKnownVoices", func() {
	const wespeaker = "voice-detect-wespeaker-resnet34.gguf"
	It("keeps the voices made by the speaker model's encoder", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{
			entry("ada", wespeaker, 1, 0),
			entry("ben", "voice-detect-ecapa-tdnn-voxceleb.gguf", 0, 1, 0),
			entry("cy", "Voice-Detect-WeSpeaker-ResNet34.gguf", 0, 1),
		}, "models/"+wespeaker)
		Expect(sel.Voices).To(HaveLen(2))
		Expect(sel.Voices[0].Name).To(Equal("ada"))
		Expect(sel.Voices[1].Name).To(Equal("cy"))
		Expect(sel.OtherEncoder).To(Equal(1))
	})
	It("matches a speaker_model value that has a directory and upper-case letters", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{entry("ada", wespeaker, 1, 0)}, "Some/Dir/Voice-Detect-WeSpeaker-ResNet34.GGUF")
		Expect(sel.Voices).To(HaveLen(1))
	})
	It("sends nothing, and says why, when every voice is from another encoder", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{entry("ben", "voice-detect-ecapa-tdnn-voxceleb.gguf", 0, 1, 0)}, wespeaker)
		Expect(sel.Voices).To(BeEmpty())
		Expect(sel.OtherEncoder).To(Equal(1))
	})
	It("includes an untagged voice only when its size matches the tagged voices", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{
			entry("ada", wespeaker, 1, 0),
			entry("old_same", "", 0, 1),
			entry("old_other", "", 0, 1, 0),
		}, wespeaker)
		Expect(sel.Voices).To(HaveLen(2))
		Expect(sel.Voices[1].Name).To(Equal("old_same"))
		Expect(sel.Untagged).To(Equal(1))
	})
	It("puts tagged voices first even when an untagged one was registered earlier", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{
			entry("old", "", 0, 1),
			entry("ada", wespeaker, 1, 0),
		}, wespeaker)
		Expect(sel.Voices).To(HaveLen(2))
		Expect(sel.Voices[0].Name).To(Equal("ada"))
		Expect(sel.Voices[1].Name).To(Equal("old"))
	})
	It("includes every untagged voice when no voice is tagged for this encoder", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{entry("old", "", 1, 0)}, wespeaker)
		Expect(sel.Voices).To(HaveLen(1))
		Expect(sel.Untagged).To(Equal(1))
	})
	It("skips voices without a name or an embedding", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{entry("", wespeaker, 1, 0), entry("x", wespeaker)}, wespeaker)
		Expect(sel.Voices).To(BeEmpty())
	})
	It("is empty for an empty registry", func() {
		Expect(voicerecognition.SelectKnownVoices(nil, wespeaker).Voices).To(BeEmpty())
	})
	It("handles an empty speaker model path without matching tagged voices", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{entry("ada", wespeaker, 1, 0), entry("old", "", 0, 1)}, "")
		Expect(sel.OtherEncoder).To(Equal(1))
		Expect(sel.Voices).To(HaveLen(1))
		Expect(sel.Voices[0].Name).To(Equal("old"))
	})
	It("does not modify the input entries", func() {
		in := []voicerecognition.Entry{entry("old", "", 0, 1), entry("ada", wespeaker, 1, 0)}
		voicerecognition.SelectKnownVoices(in, wespeaker)
		Expect(in[0].Metadata.Name).To(Equal("old"))
		Expect(in[1].Metadata.Name).To(Equal("ada"))
	})
})

type listRegistry struct {
	voicerecognition.Registry
	entries []voicerecognition.Entry
	err     error
}

func (r listRegistry) List(context.Context) ([]voicerecognition.Entry, error) {
	return r.entries, r.err
}

var _ = Describe("KnownVoicesFor", func() {
	It("selects from the registry listing", func() {
		sel, err := voicerecognition.KnownVoicesFor(context.Background(), listRegistry{entries: []voicerecognition.Entry{entry("ada", "m.gguf", 1)}}, "m.gguf")
		Expect(err).ToNot(HaveOccurred())
		Expect(sel.Voices).To(HaveLen(1))
	})
	It("propagates a List error", func() {
		boom := errors.New("boom")
		_, err := voicerecognition.KnownVoicesFor(context.Background(), listRegistry{err: boom}, "m.gguf")
		Expect(err).To(MatchError(boom))
	})
})
