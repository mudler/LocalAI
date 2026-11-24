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

	It("names the engine and the usecases the hand-written entries name", func() {
		// gallery/virtual.yaml supplies no backend, so an entry that omits one
		// names no engine at all and cannot load.
		e := RenderChild(ChildInput{
			Name:     "example-i-mini",
			Repo:     "mudler/Example-APEX-GGUF",
			Template: "virtual.yaml",
			Weights:  []GGUFFile{{Name: "Example-APEX-I-Mini.gguf", SHA256: "a"}},
			BaseTags: []string{"llm", "gguf"},
		})

		Expect(e.Overrides["backend"]).To(Equal("llama-cpp"))
		Expect(e.Overrides["known_usecases"]).To(ContainElement("chat"))
	})

	It("draws the drafter from DraftRepo when the pairing spans two repos", func() {
		// unsloth/Qwen3-4B-GGUF pairs with a drafter published separately by
		// AtomicChat, so a drafter URI built from the weights repo 404s.
		e := RenderChild(ChildInput{
			Name:      "qwen3-4b-dflash",
			Repo:      "unsloth/Qwen3-4B-GGUF",
			DraftRepo: "AtomicChat/Qwen3-4B-DFlash-GGUF",
			Template:  "virtual.yaml",
			Weights:   []GGUFFile{{Name: "Qwen3-4B-Q4_K_M.gguf", SHA256: "a"}},
			SpecType:  "draft-dflash",
			DraftFile: &GGUFFile{Name: "Qwen3-4B-DFlash.Q8_0.gguf", SHA256: "b"},
			BaseTags:  []string{"llm", "gguf"},
		})

		Expect(e.Files[0].URI).To(Equal(
			"https://huggingface.co/unsloth/Qwen3-4B-GGUF/resolve/main/Qwen3-4B-Q4_K_M.gguf"))
		Expect(e.Files[1].URI).To(Equal(
			"https://huggingface.co/AtomicChat/Qwen3-4B-DFlash-GGUF/resolve/main/Qwen3-4B-DFlash.Q8_0.gguf"))
		Expect(e.Files[1].Filename).To(Equal(
			"llama-cpp/models/AtomicChat/Qwen3-4B-DFlash-GGUF/Qwen3-4B-DFlash.Q8_0.gguf"))
		Expect(e.Overrides["draft_model"]).To(Equal(
			"llama-cpp/models/AtomicChat/Qwen3-4B-DFlash-GGUF/Qwen3-4B-DFlash.Q8_0.gguf"))
	})

	It("falls back to the weights repo for the drafter when DraftRepo is empty", func() {
		// The *-APEX-MTP-GGUF repos ship the drafter alongside the weights.
		e := RenderChild(ChildInput{
			Name:      "example-apex-dflash",
			Repo:      "mudler/Example-APEX-GGUF",
			Template:  "virtual.yaml",
			Weights:   []GGUFFile{{Name: "Example-APEX-I-Quality.gguf", SHA256: "a"}},
			SpecType:  "draft-dflash",
			DraftFile: &GGUFFile{Name: "Example-DFlash.Q8_0.gguf", SHA256: "b"},
			BaseTags:  []string{"llm", "gguf"},
		})

		Expect(e.Files[1].URI).To(Equal(
			"https://huggingface.co/mudler/Example-APEX-GGUF/resolve/main/Example-DFlash.Q8_0.gguf"))
		Expect(e.Files[1].Filename).To(Equal(
			"llama-cpp/models/mudler/Example-APEX-GGUF/Example-DFlash.Q8_0.gguf"))
	})
})

var _ = Describe("RenderChild known_usecases", func() {
	It("declares vision alongside chat when the entry carries an mmproj", func() {
		// An explicit known_usecases suppresses the backend-default fallback, so a
		// chat-only multimodal entry disappears from the UI's vision filter.
		e := RenderChild(ChildInput{
			Name:     "example-i-quality",
			Repo:     "mudler/Example-APEX-GGUF",
			Template: "virtual.yaml",
			Weights:  []GGUFFile{{Name: "Example-APEX-I-Quality.gguf", SHA256: "a"}},
			MMProj:   &GGUFFile{Name: "mmproj-F16.gguf", SHA256: "c"},
			BaseTags: []string{"llm", "gguf"},
		})

		Expect(e.Overrides["known_usecases"]).To(ConsistOf("chat", "vision"))
	})

	It("leaves a text-only entry at chat", func() {
		e := RenderChild(ChildInput{
			Name:     "example-i-quality",
			Repo:     "mudler/Example-APEX-GGUF",
			Template: "virtual.yaml",
			Weights:  []GGUFFile{{Name: "Example-APEX-I-Quality.gguf", SHA256: "a"}},
			BaseTags: []string{"llm", "gguf"},
		})

		Expect(e.Overrides["known_usecases"]).To(ConsistOf("chat"))
	})
})

var _ = Describe("localPath", func() {
	It("keeps two repos with the same basename but different owners apart", func() {
		// LiquidAI and unsloth both publish LFM2.5-8B-A1B-GGUF. A path built from
		// the bare repo name gives both the same local file, so installing the
		// second overwrites or skips the first and one of them then serves bytes
		// that do not match its recorded sha256.
		liquid := RenderChild(ChildInput{
			Name:     "lfm2.5-8b-a1b-i-quality",
			Repo:     "LiquidAI/LFM2.5-8B-A1B-GGUF",
			Template: "virtual.yaml",
			Weights:  []GGUFFile{{Name: "LFM2.5-8B-A1B-Q8_0.gguf", SHA256: "33ab3b8c"}},
			BaseTags: []string{"llm", "gguf"},
		})
		unsloth := RenderChild(ChildInput{
			Name:     "lfm2.5-8b-a1b-q8-0",
			Repo:     "unsloth/LFM2.5-8B-A1B-GGUF",
			Template: "virtual.yaml",
			Weights:  []GGUFFile{{Name: "LFM2.5-8B-A1B-Q8_0.gguf", SHA256: "ec11666b"}},
			BaseTags: []string{"llm", "gguf"},
		})

		Expect(liquid.Files[0].Filename).ToNot(Equal(unsloth.Files[0].Filename))
		Expect(unsloth.Files[0].Filename).To(Equal(
			"llama-cpp/models/unsloth/LFM2.5-8B-A1B-GGUF/LFM2.5-8B-A1B-Q8_0.gguf"))
	})

	It("namespaces the mmproj by owner too", func() {
		e := RenderChild(ChildInput{
			Name:     "example-i-quality",
			Repo:     "mudler/Example-APEX-GGUF",
			Template: "virtual.yaml",
			Weights:  []GGUFFile{{Name: "Example-APEX-I-Quality.gguf", SHA256: "a"}},
			MMProj:   &GGUFFile{Name: "mmproj-F16.gguf", SHA256: "c"},
			BaseTags: []string{"llm", "gguf"},
		})

		Expect(e.Overrides["mmproj"]).To(Equal(
			"llama-cpp/mmproj/mudler/Example-APEX-GGUF/mmproj-F16.gguf"))
	})
})

var _ = Describe("MTP builds", func() {
	renderTier := func(repo string) GalleryEntry {
		return RenderChild(ChildInput{
			Name:     "example-i-quality",
			Repo:     repo,
			Template: "virtual.yaml",
			SpecType: SpecTypeForRepo(repo),
			Weights:  []GGUFFile{{Name: "Example-I-Quality.gguf", SHA256: "a"}},
			BaseTags: []string{"llm", "gguf"},
		})
	}

	It("turns MTP on for a build off an APEX-MTP repo", func() {
		// These weights retain the model's own MTP heads, so shipping them with
		// speculation off is a strictly larger download at the same speed,
		// ranked identically to the plain rung at the same tier.
		e := renderTier("mudler/Qwen3.6-35B-A3B-APEX-MTP-GGUF")

		Expect(e.Overrides["options"]).To(ContainElements(
			"spec_type:draft-mtp", "spec_n_max:6", "spec_p_min:0.75"))
		Expect(e.Tags).To(ContainElement("mtp"))
	})

	It("needs no drafter file, because the heads travel with the weights", func() {
		e := renderTier("mudler/Qwen3.6-35B-A3B-APEX-MTP-GGUF")

		Expect(e.Overrides).ToNot(HaveKey("draft_model"))
		Expect(e.Files).To(HaveLen(1))
	})

	It("leaves a build off a plain APEX repo alone", func() {
		e := renderTier("mudler/Qwen3.6-35B-A3B-APEX-GGUF")

		Expect(e.Tags).ToNot(ContainElement("mtp"))
		Expect(e.Overrides["options"]).To(ConsistOf("use_jinja:true"))
	})

	It("leaves an unsloth counterpart rung alone", func() {
		// The counterpart quantizes the plain weights; nothing there carries heads.
		e := renderTier("unsloth/Qwen3.6-35B-A3B-GGUF")

		Expect(e.Tags).ToNot(ContainElement("mtp"))
		Expect(e.Overrides["options"]).To(ConsistOf("use_jinja:true"))
	})
})
