package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RenderChild", func() {
	It("tags an entry that configures draft-dflash", func() {
		e := RenderChild(ChildInput{
			Name:      "qwen3.5-9b-dflash",
			Repo:      "mudler/Example-APEX-GGUF",
			Template:  "virtual.yaml",
			Weights:   []GGUFFile{{Name: "Example-APEX-I-Quality.gguf", SHA256: "a"}},
			SpecType:  "draft-dflash",
			DraftFile: &GGUFFile{Name: "Example-DFlash.Q8_0.gguf", SHA256: "b"},
			BaseTags:  []string{"llm", "gguf"},
		})

		Expect(e.Tags).To(ContainElement("dflash"))
		Expect(e.Tags).ToNot(ContainElement("mtp"))
		Expect(e.Overrides["options"]).To(ContainElement("spec_type:draft-dflash"))
		Expect(e.Overrides["draft_model"]).ToNot(BeNil())
	})

	It("does not tag an MTP-named repo that configures no speculation", func() {
		// mudler/Qwen3.6-35B-A3B-APEX-MTP-GGUF ships MTP-bearing weights. Weights
		// that carry the heads are not an entry that enables them, and tagging it
		// would win the feature axis without being any faster.
		e := RenderChild(ChildInput{
			Name:     "qwen3.6-35b-a3b-apex-mtp-i-quality",
			Repo:     "mudler/Qwen3.6-35B-A3B-APEX-MTP-GGUF",
			Template: "virtual.yaml",
			Weights:  []GGUFFile{{Name: "Qwen3.6-35B-A3B-APEX-MTP-I-Quality.gguf", SHA256: "a"}},
			BaseTags: []string{"llm", "gguf"},
		})

		Expect(e.Tags).ToNot(ContainElement("mtp"))
		Expect(e.Tags).ToNot(ContainElement("dflash"))
		Expect(e.Overrides).ToNot(HaveKey("draft_model"))
	})

	It("lists every shard of a sharded build and points the model at the first", func() {
		e := RenderChild(ChildInput{
			Name:     "step-3.7-flash-ud-q4-k-m",
			Repo:     "unsloth/Step-3.7-Flash-GGUF",
			Template: "virtual.yaml",
			Weights: []GGUFFile{
				{Name: "UD-Q4_K_M/Step-3.7-Flash-UD-Q4_K_M-00001-of-00002.gguf", SHA256: "a"},
				{Name: "UD-Q4_K_M/Step-3.7-Flash-UD-Q4_K_M-00002-of-00002.gguf", SHA256: "b"},
			},
			BaseTags: []string{"llm", "gguf"},
		})

		Expect(e.Files).To(HaveLen(2))
		params, ok := e.Overrides["parameters"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(params["model"]).To(HaveSuffix("00001-of-00002.gguf"))
		Expect(e.Files[0].URI).To(Equal(
			"https://huggingface.co/unsloth/Step-3.7-Flash-GGUF/resolve/main/UD-Q4_K_M/Step-3.7-Flash-UD-Q4_K_M-00001-of-00002.gguf"))
	})

	It("wires mmproj when the repo publishes one", func() {
		e := RenderChild(ChildInput{
			Name:     "example-i-mini",
			Repo:     "mudler/Example-APEX-GGUF",
			Template: "virtual.yaml",
			Weights:  []GGUFFile{{Name: "Example-APEX-I-Mini.gguf", SHA256: "a"}},
			MMProj:   &GGUFFile{Name: "mmproj-F16.gguf", SHA256: "c"},
			BaseTags: []string{"llm", "gguf"},
		})

		Expect(e.Overrides["mmproj"]).ToNot(BeNil())
		Expect(e.Files).To(HaveLen(2))
	})
})
