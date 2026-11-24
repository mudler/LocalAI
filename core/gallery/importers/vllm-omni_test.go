package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("VLLMOmniImporter", func() {
	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.VLLMOmniImporter{}
			Expect(imp.Name()).To(Equal("vllm-omni"))
			Expect(imp.Modality()).To(Equal("text"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("Match", func() {
		It("matches when backend preference is vllm-omni", func() {
			imp := &importers.VLLMOmniImporter{}
			preferences := json.RawMessage(`{"backend": "vllm-omni"}`)
			details := importers.Details{
				URI:         "https://example.com/some-model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches Qwen owner with Omni in the repo name via HuggingFace details", func() {
			imp := &importers.VLLMOmniImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/Qwen/Qwen3-Omni-30B-A3B-Instruct",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "Qwen/Qwen3-Omni-30B-A3B-Instruct",
					Author:  "Qwen",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches Qwen2.5-Omni style repos", func() {
			imp := &importers.VLLMOmniImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/Qwen/Qwen2.5-Omni-7B",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "Qwen/Qwen2.5-Omni-7B",
					Author:  "Qwen",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches via URI fallback when HuggingFace details are missing", func() {
			imp := &importers.VLLMOmniImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/Qwen/Qwen3-Omni-30B-A3B-Instruct",
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("does not match a plain Qwen model without Omni in the repo name", func() {
			imp := &importers.VLLMOmniImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/Qwen/Qwen2.5-7B-Instruct",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "Qwen/Qwen2.5-7B-Instruct",
					Author:  "Qwen",
				},
			}

			Expect(imp.Match(details)).To(BeFalse())
		})

		It("does not match unrelated URIs without preference", func() {
			imp := &importers.VLLMOmniImporter{}
			details := importers.Details{
				URI: "https://example.com/generic-model",
			}

			Expect(imp.Match(details)).To(BeFalse())
		})

		It("does not match a non-Qwen repo whose name merely contains the token Omni", func() {
			imp := &importers.VLLMOmniImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/random/OmniX-something",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "random/OmniX-something",
					Author:  "random",
				},
			}

			// We require the "-Omni-" or Qwen+Omni pattern; a raw leading
			// "Omni" token on an unknown owner must not match.
			Expect(imp.Match(details)).To(BeFalse())
		})

		It("returns false for invalid preferences JSON", func() {
			imp := &importers.VLLMOmniImporter{}
			preferences := json.RawMessage(`not valid json`)
			details := importers.Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("produces a YAML with backend vllm-omni and the repo as the model", func() {
			imp := &importers.VLLMOmniImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/Qwen/Qwen3-Omni-30B-A3B-Instruct",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "Qwen/Qwen3-Omni-30B-A3B-Instruct",
					Author:  "Qwen",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: vllm-omni"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("Qwen/Qwen3-Omni-30B-A3B-Instruct"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("includes chat and multimodal in known_usecases", func() {
			imp := &importers.VLLMOmniImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/Qwen/Qwen3-Omni-30B-A3B-Instruct",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "Qwen/Qwen3-Omni-30B-A3B-Instruct",
					Author:  "Qwen",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("known_usecases:"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- chat"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- multimodal"))
		})

		It("derives name from URI basename by default", func() {
			imp := &importers.VLLMOmniImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/Qwen/Qwen3-Omni-30B-A3B-Instruct",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "Qwen/Qwen3-Omni-30B-A3B-Instruct",
					Author:  "Qwen",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("Qwen3-Omni-30B-A3B-Instruct"))
		})

		It("respects custom name and description from preferences", func() {
			imp := &importers.VLLMOmniImporter{}
			preferences := json.RawMessage(`{"name": "my-omni", "description": "Custom"}`)
			details := importers.Details{
				URI:         "https://huggingface.co/Qwen/Qwen3-Omni-30B-A3B-Instruct",
				Preferences: preferences,
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "Qwen/Qwen3-Omni-30B-A3B-Instruct",
					Author:  "Qwen",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-omni"))
			Expect(modelConfig.Description).To(Equal("Custom"))
		})
	})

	Context("registration order vs VLLMImporter", func() {
		It("routes Qwen3-Omni HF URIs to vllm-omni rather than vllm", func() {
			// With the registry ordering placing VLLMOmniImporter ahead of
			// VLLMImporter, a Qwen Omni repo that also happens to carry
			// tokenizer files must still emit backend: vllm-omni.
			uri := "https://huggingface.co/Qwen/Qwen3-Omni-30B-A3B-Instruct"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: vllm-omni"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})
})
