package openai

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("liveVoiceOptions", func() {
	ada := voicerecognition.Entry{
		Metadata:  voicerecognition.Metadata{Name: "Ada", Model: "enc.gguf"},
		Embedding: []float32{1, 0},
	}
	cfgWith := func(opts ...string) *config.ModelConfig { return &config.ModelConfig{Options: opts} }

	It("opens the session with the registered voices", func() {
		opts := liveVoiceOptions(context.Background(), fakeVoiceRegistry{entries: []voicerecognition.Entry{ada}}, cfgWith("speaker_model:enc.gguf"))
		Expect(opts).To(HaveLen(1))
	})
	It("adds nothing without a speaker_model, a registry, or when the registry fails", func() {
		reg := fakeVoiceRegistry{entries: []voicerecognition.Entry{ada}}
		Expect(liveVoiceOptions(context.Background(), reg, cfgWith("x:y"))).To(BeEmpty())
		Expect(liveVoiceOptions(context.Background(), nil, cfgWith("speaker_model:enc.gguf"))).To(BeEmpty())
		Expect(liveVoiceOptions(context.Background(), fakeVoiceRegistry{err: errors.New("boom")}, cfgWith("speaker_model:enc.gguf"))).To(BeEmpty())
	})
})

var _ = Describe("transcription segment event", func() {
	It("carries speaker_name only when set", func() {
		named, _ := json.Marshal(types.ConversationItemInputAudioTranscriptionSegmentEvent{Speaker: "0", SpeakerName: "Ada"})
		Expect(string(named)).To(ContainSubstring(`"speaker_name":"Ada"`))
		plain, _ := json.Marshal(types.ConversationItemInputAudioTranscriptionSegmentEvent{Speaker: "0"})
		Expect(string(plain)).ToNot(ContainSubstring("speaker_name"))
	})
})
