package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// The realtime pipeline can stream each stage (LLM tokens, TTS audio,
// transcription text) and can disable model "thinking" for the LLM. These are
// opt-in per pipeline; everything defaults to off so existing configs keep the
// unary behaviour.
var _ = Describe("Pipeline streaming config", func() {
	It("defaults every streaming + thinking helper to false when unset", func() {
		var p Pipeline
		Expect(p.StreamLLM()).To(BeFalse())
		Expect(p.StreamTTS()).To(BeFalse())
		Expect(p.StreamTranscription()).To(BeFalse())
		Expect(p.ChunkClauses()).To(BeFalse())
		Expect(p.ThinkingDisabled()).To(BeFalse())
	})

	It("parses the nested streaming block and disable_thinking from YAML", func() {
		var c ModelConfig
		err := yaml.Unmarshal([]byte(`
name: gpt-realtime
pipeline:
  llm: my-llm
  tts: my-tts
  transcription: my-stt
  streaming:
    llm: true
    tts: true
    transcription: true
    clause_chunking: true
  disable_thinking: true
`), &c)
		Expect(err).ToNot(HaveOccurred())
		Expect(c.Pipeline.StreamLLM()).To(BeTrue())
		Expect(c.Pipeline.StreamTTS()).To(BeTrue())
		Expect(c.Pipeline.StreamTranscription()).To(BeTrue())
		Expect(c.Pipeline.ChunkClauses()).To(BeTrue())
		Expect(c.Pipeline.ThinkingDisabled()).To(BeTrue())
	})

	It("treats an explicit false in the streaming block as disabled", func() {
		var c ModelConfig
		err := yaml.Unmarshal([]byte(`
name: gpt-realtime
pipeline:
  streaming:
    tts: false
`), &c)
		Expect(err).ToNot(HaveOccurred())
		Expect(c.Pipeline.StreamTTS()).To(BeFalse())
	})
})
