package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// An alias backed by a text-only model can name a vision_alias: requests that
// carry images are served by that model, everything else stays on the target.
var _ = Describe("Alias vision_alias", func() {
	const textOnly = `name: text-llm
backend: llama-cpp
parameters:
  model: text-llm.gguf
`
	const vision = `name: vision-llm
backend: llama-cpp
mmproj: vision-mmproj.gguf
parameters:
  model: vision-llm.gguf
`
	const assistant = `name: assistant
alias: text-llm
vision_alias: vision-llm
profile:
  system_prompt: You are the everyday assistant.
`
	const plain = `name: plain
alias: text-llm
`
	const onVision = `name: on-vision
alias: vision-llm
vision_alias: text-llm
`
	const chained = `name: chained
alias: text-llm
vision_alias: plain
`

	var cl *config.ModelConfigLoader

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		for name, body := range map[string]string{
			"text-llm": textOnly, "vision-llm": vision, "assistant": assistant,
			"plain": plain, "on-vision": onVision, "chained": chained,
		} {
			Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0644)).To(Succeed())
		}
		cl = config.NewModelConfigLoader(dir)
		Expect(cl.LoadModelConfigsFromPath(dir)).To(Succeed())
	})

	resolve := func(name string, hasImages bool) (*config.ModelConfig, error) {
		cfg, ok := cl.GetModelConfig(name)
		Expect(ok).To(BeTrue(), name)
		resolved, wasAlias, err := cl.ResolveAliasForRequest(&cfg, hasImages)
		Expect(wasAlias).To(BeTrue())
		return resolved, err
	}

	It("serves an image request on the vision_alias model with the alias's profile", func() {
		resolved, err := resolve("assistant", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Name).To(Equal("vision-llm"))
		Expect(resolved.VisionSupported()).To(BeTrue())
		Expect(resolved.SystemPrompt).To(Equal("You are the everyday assistant."))
	})

	It("keeps a text request on the alias target", func() {
		resolved, err := resolve("assistant", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Name).To(Equal("text-llm"))
		Expect(resolved.SystemPrompt).To(Equal("You are the everyday assistant."))
	})

	It("keeps an image request on a target that accepts images itself", func() {
		resolved, err := resolve("on-vision", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Name).To(Equal("vision-llm"))
	})

	It("leaves an alias without vision_alias unchanged", func() {
		resolved, err := resolve("plain", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Name).To(Equal("text-llm"))
	})

	It("refuses a vision_alias that is itself an alias", func() {
		_, err := resolve("chained", true)
		Expect(err).To(MatchError(ContainSubstring("is itself an alias")))

		cfg, _ := cl.GetModelConfig("chained")
		Expect(cl.ValidateAliasTarget(&cfg)).To(MatchError(ContainSubstring("is itself an alias")))
		ok, _ := cl.GetModelConfig("assistant")
		Expect(cl.ValidateAliasTarget(&ok)).To(Succeed())
	})

	It("rejects vision_alias on a config that is not an alias", func() {
		cfg := config.ModelConfig{Name: "solo", Backend: "llama-cpp", VisionAlias: "vision-llm"}
		valid, err := cfg.Validate()
		Expect(valid).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring("vision_alias is only valid on an alias")))
	})
})
