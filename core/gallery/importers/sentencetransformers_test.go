package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SentenceTransformersImporter", func() {
	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.SentenceTransformersImporter{}
			Expect(imp.Name()).To(Equal("sentencetransformers"))
			Expect(imp.Modality()).To(Equal("embeddings"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("Match", func() {
		It("matches when backend preference is sentencetransformers", func() {
			imp := &importers.SentenceTransformersImporter{}
			preferences := json.RawMessage(`{"backend": "sentencetransformers"}`)
			details := importers.Details{
				URI:         "https://example.com/some-model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches when HF repo ships modules.json", func() {
			imp := &importers.SentenceTransformersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "sentence-transformers/all-MiniLM-L6-v2",
					Author:  "sentence-transformers",
					Files: []hfapi.ModelFile{
						{Path: "modules.json"},
						{Path: "tokenizer.json"},
					},
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches when HF repo ships sentence_bert_config.json", func() {
			imp := &importers.SentenceTransformersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/some/st-model",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "some/st-model",
					Author:  "some",
					Files: []hfapi.ModelFile{
						{Path: "sentence_bert_config.json"},
					},
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches sentence-transformers owner even without marker files", func() {
			imp := &importers.SentenceTransformersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/sentence-transformers/foo",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "sentence-transformers/foo",
					Author:  "sentence-transformers",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches via URI fallback when HuggingFace details are missing", func() {
			imp := &importers.SentenceTransformersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2",
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("does not match unrelated plain transformers models", func() {
			imp := &importers.SentenceTransformersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/meta-llama/Llama-3-8B",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "meta-llama/Llama-3-8B",
					Author:  "meta-llama",
					Files: []hfapi.ModelFile{
						{Path: "tokenizer.json"},
					},
				},
			}

			Expect(imp.Match(details)).To(BeFalse())
		})

		It("returns false for invalid preferences JSON", func() {
			imp := &importers.SentenceTransformersImporter{}
			preferences := json.RawMessage(`not valid json`)
			details := importers.Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("produces a YAML with backend sentencetransformers, embeddings true, and the repo as the model", func() {
			imp := &importers.SentenceTransformersImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "sentence-transformers/all-MiniLM-L6-v2",
					Author:  "sentence-transformers",
					Files: []hfapi.ModelFile{
						{Path: "modules.json"},
					},
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: sentencetransformers"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("sentence-transformers/all-MiniLM-L6-v2"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("embeddings: true"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("respects custom name and description from preferences", func() {
			imp := &importers.SentenceTransformersImporter{}
			preferences := json.RawMessage(`{"name": "my-embed", "description": "Custom"}`)
			details := importers.Details{
				URI:         "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2",
				Preferences: preferences,
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "sentence-transformers/all-MiniLM-L6-v2",
					Author:  "sentence-transformers",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-embed"))
			Expect(modelConfig.Description).To(Equal("Custom"))
		})
	})

	Context("registration order vs TransformersImporter", func() {
		It("routes sentence-transformers HF URIs to sentencetransformers rather than transformers", func() {
			uri := "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: sentencetransformers"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})
})
