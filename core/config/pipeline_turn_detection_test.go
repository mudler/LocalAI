package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// pipeline.turn_detection sets the server-side default turn-detection mode
// for realtime sessions. Unset keeps server_vad, so existing configs are
// unaffected; retranscribe is opt-in.
var _ = Describe("Pipeline turn_detection config", func() {
	It("defaults to non-semantic with retranscribe off when unset", func() {
		var p Pipeline
		Expect(p.TurnDetectionSemantic()).To(BeFalse())
		Expect(p.TurnDetectionRetranscribe()).To(BeFalse())
	})

	It("parses the nested turn_detection block from YAML", func() {
		var c ModelConfig
		err := yaml.Unmarshal([]byte(`
name: gpt-realtime
pipeline:
  transcription: parakeet-cpp-realtime_eou_120m-v1
  turn_detection:
    type: semantic_vad
    eagerness: high
    retranscribe: true
`), &c)
		Expect(err).ToNot(HaveOccurred())
		Expect(c.Pipeline.TurnDetectionSemantic()).To(BeTrue())
		Expect(c.Pipeline.TurnDetection.Eagerness).To(Equal("high"))
		Expect(c.Pipeline.TurnDetectionRetranscribe()).To(BeTrue())
	})

	It("treats server_vad and unknown types as non-semantic", func() {
		var p Pipeline
		p.TurnDetection.Type = "server_vad"
		Expect(p.TurnDetectionSemantic()).To(BeFalse())
		p.TurnDetection.Type = "something_else"
		Expect(p.TurnDetectionSemantic()).To(BeFalse())
	})

	It("matches semantic_vad case-insensitively with surrounding space", func() {
		var p Pipeline
		p.TurnDetection.Type = " Semantic_VAD "
		Expect(p.TurnDetectionSemantic()).To(BeTrue())
	})

	It("treats an explicit retranscribe false as off", func() {
		var c ModelConfig
		err := yaml.Unmarshal([]byte(`
pipeline:
  turn_detection:
    type: semantic_vad
    retranscribe: false
`), &c)
		Expect(err).ToNot(HaveOccurred())
		Expect(c.Pipeline.TurnDetectionRetranscribe()).To(BeFalse())
	})
})
