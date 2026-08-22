package importers_test

import (
	"encoding/json"
	"strings"

	. "github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DS4Importer", func() {
	var importer *DS4Importer

	BeforeEach(func() {
		importer = &DS4Importer{}
	})

	Context("Match", func() {
		It("matches the canonical HuggingFace repo URI", func() {
			details := Details{
				URI: "huggingface://antirez/deepseek-v4-gguf/DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2.gguf",
			}
			Expect(importer.Match(details)).To(BeTrue())
		})

		It("matches when filename has the DeepSeek-V4-Flash prefix", func() {
			details := Details{
				URI: "https://example.com/mirror/DeepSeek-V4-Flash-Q4KExperts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out-chat-v2.gguf",
			}
			Expect(importer.Match(details)).To(BeTrue())
		})

		It("matches when backend preference is ds4", func() {
			prefs := json.RawMessage(`{"backend": "ds4"}`)
			details := Details{
				URI:         "https://example.com/some-other.gguf",
				Preferences: prefs,
			}
			Expect(importer.Match(details)).To(BeTrue())
		})

		It("does not match arbitrary GGUFs (must fall through to llama-cpp)", func() {
			details := Details{URI: "huggingface://TheBloke/Llama-2-7B-GGUF/llama-2-7b.Q4_K_M.gguf"}
			Expect(importer.Match(details)).To(BeFalse())
		})

		It("does not match non-GGUF assets", func() {
			details := Details{URI: "https://example.com/model.bin"}
			Expect(importer.Match(details)).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("emits backend: ds4 and the standard ds4flash.gguf filename", func() {
			details := Details{
				URI: "huggingface://antirez/deepseek-v4-gguf/DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2.gguf",
			}
			cfg, err := importer.Import(details)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].Filename).To(Equal("ds4flash.gguf"))
			Expect(cfg.Files[0].URI).To(Equal(details.URI))
			Expect(strings.Contains(cfg.ConfigFile, "backend: ds4")).To(BeTrue(),
				"ConfigFile must specify backend: ds4, got: %s", cfg.ConfigFile)
			Expect(strings.Contains(cfg.ConfigFile, "use_tokenizer_template: true")).To(BeTrue())
		})

		It("preserves an explicit GGUF URI with a query string", func() {
			uri := "https://huggingface.co/antirez/deepseek-v4-gguf/resolve/main/DeepSeek-V4-Flash-UD-IQ3_XXS.gguf?download=true"

			cfg, err := importer.Import(Details{URI: uri})

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal(uri))
		})

		It("selects the highest-priority requested quantization from a repository", func() {
			details := Details{
				URI:         "huggingface://antirez/deepseek-v4-gguf",
				Preferences: json.RawMessage(`{"quantizations":"UD-IQ3_XXS,IQ2_XXS"}`),
				HuggingFace: &hfapi.ModelDetails{Files: []hfapi.ModelFile{
					{Path: "DeepSeek-V4-Flash-IQ2_XXS.gguf", URL: "https://huggingface.co/antirez/deepseek-v4-gguf/resolve/main/DeepSeek-V4-Flash-IQ2_XXS.gguf"},
					{Path: "DeepSeek-V4-Flash-UD-IQ3_XXS.gguf", URL: "https://huggingface.co/antirez/deepseek-v4-gguf/resolve/main/DeepSeek-V4-Flash-UD-IQ3_XXS.gguf"},
				}},
			}

			cfg, err := importer.Import(details)

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://huggingface.co/antirez/deepseek-v4-gguf/resolve/main/DeepSeek-V4-Flash-UD-IQ3_XXS.gguf"))
		})

		It("falls back to the last compatible GGUF when requested quantizations do not match", func() {
			details := Details{
				URI:         "huggingface://antirez/deepseek-v4-gguf",
				Preferences: json.RawMessage(`{"quantizations":"F16"}`),
				HuggingFace: &hfapi.ModelDetails{Files: []hfapi.ModelFile{
					{Path: "DeepSeek-V4-Flash-BF16.gguf", URL: "https://huggingface.co/antirez/deepseek-v4-gguf/resolve/main/DeepSeek-V4-Flash-BF16.gguf"},
					{Path: "README.md", URL: "https://huggingface.co/antirez/deepseek-v4-gguf/resolve/main/README.md"},
					{Path: "DeepSeek-V4-Flash-Q8_0.gguf", URL: "https://huggingface.co/antirez/deepseek-v4-gguf/resolve/main/DeepSeek-V4-Flash-Q8_0.gguf"},
				}},
			}

			cfg, err := importer.Import(details)

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://huggingface.co/antirez/deepseek-v4-gguf/resolve/main/DeepSeek-V4-Flash-Q8_0.gguf"))
		})

		It("rejects a repository URI when no HuggingFace file metadata is available", func() {
			_, err := importer.Import(Details{URI: "huggingface://antirez/deepseek-v4-gguf"})

			Expect(err).To(MatchError(ContainSubstring("compatible GGUF")))
		})

		It("rejects repository metadata without a compatible GGUF", func() {
			details := Details{
				URI: "huggingface://antirez/deepseek-v4-gguf",
				HuggingFace: &hfapi.ModelDetails{Files: []hfapi.ModelFile{
					{Path: "README.md", URL: "https://huggingface.co/antirez/deepseek-v4-gguf/resolve/main/README.md"},
					{Path: "unrelated.gguf", URL: "https://huggingface.co/antirez/deepseek-v4-gguf/resolve/main/unrelated.gguf"},
				}},
			}

			cfg, err := importer.Import(details)

			Expect(err).To(MatchError(ContainSubstring("compatible GGUF")))
			Expect(cfg.Files).To(BeEmpty())
		})
	})
})
