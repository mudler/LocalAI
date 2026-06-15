package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RerankersImporter", func() {
	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.RerankersImporter{}
			Expect(imp.Name()).To(Equal("rerankers"))
			Expect(imp.Modality()).To(Equal("reranker"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("Match", func() {
		It("matches when backend preference is rerankers", func() {
			imp := &importers.RerankersImporter{}
			preferences := json.RawMessage(`{"backend": "rerankers"}`)
			details := importers.Details{
				URI:         "https://example.com/some-model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches cross-encoder owner via HuggingFace details", func() {
			imp := &importers.RerankersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/cross-encoder/ms-marco-MiniLM-L-6-v2",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "cross-encoder/ms-marco-MiniLM-L-6-v2",
					Author:  "cross-encoder",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches when the repo name contains 'reranker' (case-insensitive)", func() {
			imp := &importers.RerankersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/BAAI/bge-reranker-v2-m3",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "BAAI/bge-reranker-v2-m3",
					Author:  "BAAI",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches Alibaba-NLP/gte-reranker repos", func() {
			imp := &importers.RerankersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/Alibaba-NLP/gte-reranker-modernbert-base",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "Alibaba-NLP/gte-reranker-modernbert-base",
					Author:  "Alibaba-NLP",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches via URI fallback when HuggingFace details are missing", func() {
			imp := &importers.RerankersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/BAAI/bge-reranker-v2-m3",
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("does not match unrelated models without reranker signals", func() {
			imp := &importers.RerankersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/meta-llama/Llama-3-8B",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "meta-llama/Llama-3-8B",
					Author:  "meta-llama",
				},
			}

			Expect(imp.Match(details)).To(BeFalse())
		})

		It("returns false for invalid preferences JSON", func() {
			imp := &importers.RerankersImporter{}
			preferences := json.RawMessage(`not valid json`)
			details := importers.Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("produces a YAML with backend rerankers, reranking true, and the repo as the model", func() {
			imp := &importers.RerankersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/BAAI/bge-reranker-v2-m3",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "BAAI/bge-reranker-v2-m3",
					Author:  "BAAI",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: rerankers"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("BAAI/bge-reranker-v2-m3"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("reranking: true"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("respects custom name and description from preferences", func() {
			imp := &importers.RerankersImporter{}
			preferences := json.RawMessage(`{"name": "my-reranker", "description": "Custom"}`)
			details := importers.Details{
				URI:         "https://huggingface.co/BAAI/bge-reranker-v2-m3",
				Preferences: preferences,
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "BAAI/bge-reranker-v2-m3",
					Author:  "BAAI",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-reranker"))
			Expect(modelConfig.Description).To(Equal("Custom"))
		})
	})

	Context("registration order vs TransformersImporter", func() {
		It("routes BAAI/bge-reranker HF URIs to rerankers rather than transformers", func() {
			uri := "https://huggingface.co/BAAI/bge-reranker-v2-m3"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: rerankers"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})
})
