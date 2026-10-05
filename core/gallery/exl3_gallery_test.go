package gallery_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"gopkg.in/yaml.v3"
)

func remarshal(value any, target any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, target)
}

var _ = Describe("EXL3 gallery entries", func() {
	It("pins the four full repositories and configures the Qwen DFlash companion", func() {
		entries, err := gallery.ReadConfigFile[[]gallery.GalleryModel](filepath.Join("..", "..", "gallery", "index.yaml"))
		Expect(err).ToNot(HaveOccurred())

		byName := make(map[string]gallery.GalleryModel, len(*entries))
		for _, entry := range *entries {
			byName[entry.Name] = entry
		}

		expected := map[string]struct {
			repo     string
			revision string
		}{
			"qwen3.8-27b-exl3-vllm-cpp": {
				repo: "Mia-AiLab/Qwen3.8-27B-EXL3-3.5bpw", revision: "19441ac874c4018295da848e250f23511361cda4",
			},
			"qwen3.8-27b-dflash2-exl3-vllm-cpp": {
				repo: "Mia-AiLab/Qwen3.8-27B-EXL3-3.5bpw", revision: "19441ac874c4018295da848e250f23511361cda4",
			},
			"deepseek-v4-flash-spark-exl3-vllm-cpp": {
				repo: "0xSero/deepseek-v4-flash-0731-spark", revision: "ce5ff0f1efb2e184aafc759d281bfae47d3a359c",
			},
			"deepseek-v4-flash-exl3-3bpw-vllm-cpp": {
				repo: "0xSero/DeepSeek-V4-Flash-0731-EXL3-3.0bpw", revision: "e0bf84ac76a5100e8790c22ad10b70b1e2d06d71",
			},
		}

		for name, want := range expected {
			entry, found := byName[name]
			Expect(found).To(BeTrue(), "missing gallery entry %q", name)
			Expect(entry.Tags).To(ContainElements("vllm-cpp", "exl3", "gpu", "cuda"), name)
			Expect(entry.Overrides).To(HaveKeyWithValue("backend", "vllm-cpp"), name)
			cfg := config.ModelConfig{}
			Expect(remarshal(entry.Overrides, &cfg)).To(Succeed(), name)
			Expect(cfg.Artifacts).ToNot(BeEmpty(), name)
			Expect(cfg.Artifacts[0].Source.Repo).To(Equal(want.repo), name)
			Expect(cfg.Artifacts[0].Source.Revision).To(Equal(want.revision), name)
		}

		plain := byName["qwen3.8-27b-exl3-vllm-cpp"]
		Expect(plain.Tags).ToNot(ContainElement("dflash"))
		dflashTags := 0
		for name := range expected {
			if contains(byName[name].Tags, "dflash") {
				dflashTags++
			}
		}
		Expect(dflashTags).To(Equal(1))

		dflash := byName["qwen3.8-27b-dflash2-exl3-vllm-cpp"]
		Expect(dflash.Tags).To(ContainElement("dflash"))
		Expect(dflash.Variants).To(ConsistOf(gallery.Variant{Model: "qwen3.8-27b-exl3-vllm-cpp"}))
		cfg := config.ModelConfig{}
		Expect(remarshal(dflash.Overrides, &cfg)).To(Succeed())
		Expect(cfg.ContextSize).To(HaveValue(Equal(8192)))
		Expect(cfg.EngineArgs).To(HaveKeyWithValue("num_blocks", 2048))
		Expect(cfg.EngineArgs).To(HaveKeyWithValue("max_num_seqs", 8))
		Expect(cfg.EngineArgs).To(HaveKeyWithValue("max_num_batched_tokens", 16384))
		Expect(cfg.EngineArgs).To(HaveKeyWithValue("enable_prefix_caching", false))
		spec, ok := cfg.EngineArgs["speculative_config"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(spec).To(HaveKeyWithValue("method", "dflash"))
		Expect(spec).To(HaveKeyWithValue("num_speculative_tokens", 7))
		Expect(cfg.Artifacts).To(HaveLen(2))
		Expect(cfg.Artifacts[1].Name).To(Equal("draft_model"))
		Expect(cfg.Artifacts[1].Target).To(Equal("companion"))
		Expect(cfg.Artifacts[1].Source.Repo).To(Equal("Mia-AiLab/Qwen3.8-27B-DFlash2-EXL3-5.0bpw"))
		Expect(cfg.Artifacts[1].Source.Revision).To(Equal("4f0436269bca761b071f05319e8e04a87cc633f9"))
	})
})

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
