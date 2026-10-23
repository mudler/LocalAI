package voicerecognition_test

import (
	"context"
	"encoding/json"
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
	It("falls back to speaker_component when there is no speaker_model", func() {
		Expect(voicerecognition.SpeakerModelFromOptions([]string{"diar_component:diar", "speaker_component: voice "})).To(Equal("voice"))
	})
	It("prefers speaker_model over speaker_component, in any order", func() {
		Expect(voicerecognition.SpeakerModelFromOptions([]string{"speaker_component:voice", "speaker_model:bundle.gguf"})).To(Equal("bundle.gguf"))
		Expect(voicerecognition.SpeakerModelFromOptions([]string{"speaker_model:bundle.gguf", "speaker_component:voice"})).To(Equal("bundle.gguf"))
	})
	It("ignores empty values", func() {
		Expect(voicerecognition.SpeakerModelFromOptions([]string{"speaker_model:", "speaker_component:voice"})).To(Equal("voice"))
		Expect(voicerecognition.SpeakerModelFromOptions([]string{"speaker_model:", "speaker_component:"})).To(BeEmpty())
	})
})

var _ = Describe("SpeakerEncoderFromOptions", func() {
	It("has a file for speaker_model and none for a component", func() {
		Expect(voicerecognition.SpeakerEncoderFromOptions([]string{"speaker_model:enc.gguf"})).
			To(Equal(voicerecognition.SpeakerEncoder{Ref: "enc.gguf", File: "enc.gguf"}))
		Expect(voicerecognition.SpeakerEncoderFromOptions([]string{"speaker_component:voice"})).
			To(Equal(voicerecognition.SpeakerEncoder{Ref: "voice"}))
	})
	It("reads the speaker_tag alias in lowercase", func() {
		Expect(voicerecognition.SpeakerEncoderFromOptions([]string{"speaker_component:voice", "speaker_tag: Voice-Detect-WeSpeaker.gguf"}).Tags).
			To(Equal([]string{"voice-detect-wespeaker.gguf"}))
		Expect(voicerecognition.SpeakerEncoderFromOptions([]string{"speaker_component:voice"}).Tags).To(BeEmpty())
	})
	It("names no encoder for a speaker_tag alone", func() {
		Expect(voicerecognition.SpeakerEncoderFromOptions([]string{"speaker_tag:x.gguf"}).Ref).To(BeEmpty())
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
	It("defers untagged dimensions to the loaded backend, not registry tags", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{
			entry("ada", wespeaker, 1, 0),
			entry("old_same", "", 0, 1),
			entry("old_other", "", 0, 1, 0),
		}, wespeaker)
		Expect(sel.Voices).To(HaveLen(3))
		Expect(sel.Voices[1].Name).To(Equal("old_other"))
		Expect(sel.Voices[2].Name).To(Equal("old_same"))
		Expect(sel.Untagged).To(Equal(2))
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
	It("includes untagged voices of different sizes when no voice is tagged for this encoder", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{entry("old", "", 1, 0), entry("older", "", 1, 0, 0)}, wespeaker)
		Expect(sel.Voices).To(HaveLen(2)) // the backend skips the ones whose size does not match
		Expect(sel.Untagged).To(Equal(2))
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

var _ = Describe("encoder fingerprint of selected voices", func() {
	const hash = "sha256:aaaa"
	It("passes the family and takes the weights from a hash tag", func() {
		e := entry("ada", hash, 1, 0)
		e.Metadata.EncoderFamily = "voicedetect:ecapa_tdnn:ecapa:192"
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{e}, "spk.gguf")
		Expect(sel.Voices).To(HaveLen(1))
		Expect(sel.Voices[0].Family).To(Equal("voicedetect:ecapa_tdnn:ecapa:192"))
		Expect(sel.Voices[0].Weights).To(Equal(hash))
	})
	It("forwards an audio-registered voice by its family and weights, whatever its file name", func() {
		e := entry("ada", "voice-detect-ecapa.gguf", 1, 0)
		e.Metadata.EncoderFamily = "voicedetect:ecapa_tdnn:ecapa:192"
		e.Metadata.EncoderWeights = hash
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{e}, "other-encoder.gguf")
		Expect(sel.OtherEncoder).To(BeZero())
		Expect(sel.Voices).To(HaveLen(1))
		Expect(sel.Voices[0].Model).To(Equal("voice-detect-ecapa.gguf"))
		Expect(sel.Voices[0].Family).To(Equal("voicedetect:ecapa_tdnn:ecapa:192"))
		Expect(sel.Voices[0].Weights).To(Equal(hash))
	})
	It("keeps an old audio voice without a family on the file-name filter", func() {
		old := entry("ada", "voice-detect-ecapa.gguf", 1, 0)
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{old}, "other-encoder.gguf")
		Expect(sel.Voices).To(BeEmpty())
		Expect(sel.OtherEncoder).To(Equal(1))
	})
	It("leaves a file-name tagged voice unfingerprinted", func() {
		sel := voicerecognition.SelectKnownVoices([]voicerecognition.Entry{entry("ada", "spk.gguf", 1, 0)}, "spk.gguf")
		Expect(sel.Voices[0].Family).To(BeEmpty())
		Expect(sel.Voices[0].Weights).To(BeEmpty())
	})
	It("loads a stored voice that has no family, and keeps the family when there is one", func() {
		var old, fresh voicerecognition.Metadata
		Expect(json.Unmarshal([]byte(`{"id":"1","name":"ada","registered_at":"2026-01-01T00:00:00Z","model":"spk.gguf"}`), &old)).To(Succeed())
		Expect(old.EncoderFamily).To(BeEmpty())
		raw, err := json.Marshal(voicerecognition.Metadata{ID: "2", Name: "ben", Model: hash, EncoderFamily: "f"})
		Expect(err).ToNot(HaveOccurred())
		Expect(json.Unmarshal(raw, &fresh)).To(Succeed())
		Expect(fresh.EncoderFamily).To(Equal("f"))
		raw, _ = json.Marshal(old)
		Expect(string(raw)).ToNot(ContainSubstring("encoder_family"))
		Expect(string(raw)).ToNot(ContainSubstring("encoder_weights"))
	})
})

var _ = Describe("SelectKnownVoices for a bundle component", func() {
	const hash = "sha256:72040372aa"
	mixed := func() []voicerecognition.Entry {
		return []voicerecognition.Entry{
			entry("hashed", hash, 1, 0),
			entry("legacy-wespeaker", "voice-detect-wespeaker-resnet34.gguf", 1, 0),
			entry("legacy-ecapa", "voice-detect-ecapa-tdnn-voxceleb.gguf", 1, 0),
			entry("old", "", 1, 0),
		}
	}
	ids := func(sel voicerecognition.KnownVoiceSelection) []string {
		var out []string
		for _, v := range sel.Voices {
			out = append(out, v.ID)
		}
		return out
	}

	It("sends hash-tagged and untagged voices, and refuses tag-only voices, without a tag alias", func() {
		sel := voicerecognition.SelectKnownVoices(mixed(), "")
		Expect(ids(sel)).To(Equal([]string{"hashed", "old"}))
		Expect(sel.OtherEncoder).To(Equal(2))
	})
	It("matches a tag-only voice through the alias and no other", func() {
		sel := voicerecognition.SelectKnownVoices(mixed(), "", "Voice-Detect-WeSpeaker-ResNet34.gguf")
		Expect(ids(sel)).To(Equal([]string{"hashed", "legacy-wespeaker", "old"}))
		Expect(sel.OtherEncoder).To(Equal(1))
	})
	It("adds the alias to the file tag of a speaker_model", func() {
		sel := voicerecognition.SelectKnownVoices(mixed(), "bundle.gguf", "voice-detect-ecapa-tdnn-voxceleb.gguf")
		Expect(ids(sel)).To(Equal([]string{"hashed", "legacy-ecapa", "old"}))
	})
	It("ignores a blank alias", func() {
		Expect(ids(voicerecognition.SelectKnownVoices(mixed(), "", " ", ""))).To(Equal([]string{"hashed", "old"}))
	})
})

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
