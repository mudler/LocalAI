package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
)

// A composed pipeline needs more than one snapshot: LongCat-Video-Avatar-1.5
// loads its own transformer but takes tokenizer, text encoder and VAE from the
// LongCat-Video base repo. The config expresses that as one target: model
// artifact followed by named target: companion artifacts. Exactly one primary,
// and it must be first, because ModelFileName() and every load path resolve the
// load target from Artifacts[0].
var _ = Describe("multi-artifact config validation", func() {
	parse := func(doc string) config.ModelConfig {
		var c config.ModelConfig
		Expect(yaml.Unmarshal([]byte(doc), &c)).To(Succeed())
		return c
	}

	It("accepts a primary followed by a named companion", func() {
		c := parse(`
backend: longcat-video
artifacts:
  - name: model
    target: model
    source: {type: huggingface, repo: meituan-longcat/LongCat-Video-Avatar-1.5}
  - name: base_model
    target: companion
    source: {type: huggingface, repo: meituan-longcat/LongCat-Video}
parameters: {model: meituan-longcat/LongCat-Video-Avatar-1.5}
`)
		valid, err := c.Validate()
		Expect(err).NotTo(HaveOccurred())
		Expect(valid).To(BeTrue())
	})

	It("rejects a config whose artifacts are all companions", func() {
		// The reachable way to end up without a primary. Two primaries cannot
		// coexist by construction: both would have to be named "model", so the
		// duplicate-name rule catches that case first (covered below).
		c := parse(`
backend: longcat-video
artifacts:
  - name: base_model
    target: companion
    source: {type: huggingface, repo: owner/base}
parameters: {model: owner/main}
`)
		valid, err := c.Validate()
		Expect(valid).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring("exactly one")))
	})

	It("rejects a second artifact claiming the primary target", func() {
		c := parse(`
backend: longcat-video
artifacts:
  - name: model
    target: model
    source: {type: huggingface, repo: owner/one}
  - name: second
    target: model
    source: {type: huggingface, repo: owner/two}
parameters: {model: owner/one}
`)
		valid, err := c.Validate()
		Expect(valid).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring("primary artifact name")))
	})

	It("rejects a companion declared before the primary", func() {
		// Artifacts[0] is the load target everywhere; a companion in that slot
		// would silently point the backend at the wrong snapshot.
		c := parse(`
backend: longcat-video
artifacts:
  - name: base_model
    target: companion
    source: {type: huggingface, repo: owner/base}
  - name: model
    target: model
    source: {type: huggingface, repo: owner/main}
parameters: {model: owner/main}
`)
		valid, err := c.Validate()
		Expect(valid).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring("first")))
	})

	It("rejects companions sharing a name", func() {
		c := parse(`
backend: longcat-video
artifacts:
  - name: model
    target: model
    source: {type: huggingface, repo: owner/main}
  - name: base_model
    target: companion
    source: {type: huggingface, repo: owner/base}
  - name: base_model
    target: companion
    source: {type: huggingface, repo: owner/other}
parameters: {model: owner/main}
`)
		valid, err := c.Validate()
		Expect(valid).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring("duplicate")))
	})
})
