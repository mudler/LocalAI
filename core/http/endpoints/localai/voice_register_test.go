package localai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("voiceMetadata", func() {
	It("carries the name, the labels and the encoder that embedded the voice", func() {
		m := voiceMetadata("ada", map[string]string{"team": "a"}, "voice-detect-wespeaker-resnet34.gguf")
		Expect(m.Name).To(Equal("ada"))
		Expect(m.Labels).To(Equal(map[string]string{"team": "a"}))
		Expect(m.Model).To(Equal("voice-detect-wespeaker-resnet34.gguf"))
	})
	It("leaves the tag empty when the backend did not say", func() {
		Expect(voiceMetadata("ada", nil, "").Model).To(BeEmpty())
	})
})
