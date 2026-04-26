package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LlamaCPPImporter", func() {
	var importer *LlamaCPPImporter

	BeforeEach(func() {
		importer = &LlamaCPPImporter{}
	})

	Context("Match", func() {
		It("should match when URI ends with .gguf", func() {
			details := Details{
				URI: "https://example.com/model.gguf",
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should match when backend preference is llama-cpp", func() {
			preferences := json.RawMessage(`{"backend": "llama-cpp"}`)
			details := Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should not match when URI does not end with .gguf and no backend preference", func() {
			details := Details{
				URI: "https://example.com/model.bin",
			}

			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})

		It("should not match when backend preference is different", func() {
			preferences := json.RawMessage(`{"backend": "mlx"}`)
			details := Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})

		It("should return false when JSON preferences are invalid", func() {
			preferences := json.RawMessage(`invalid json`)
			details := Details{
				URI:         "https://example.com/model.gguf",
				Preferences: preferences,
			}

			// Invalid JSON causes Match to return false early
			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("should import model config with default name and description", func() {
			details := Details{
				URI: "https://example.com/my-model.gguf",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-model.gguf"))
			Expect(modelConfig.Description).To(Equal("Imported from https://example.com/my-model.gguf"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"))
			Expect(len(modelConfig.Files)).To(Equal(1), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].URI).To(Equal("https://example.com/my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("should import model config with custom name and description from preferences", func() {
			preferences := json.RawMessage(`{"name": "custom-model", "description": "Custom description"}`)
			details := Details{
				URI:         "https://example.com/my-model.gguf",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("custom-model"))
			Expect(modelConfig.Description).To(Equal("Custom description"))
			Expect(len(modelConfig.Files)).To(Equal(1), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].URI).To(Equal("https://example.com/my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("should handle invalid JSON preferences", func() {
			preferences := json.RawMessage(`invalid json`)
			details := Details{
				URI:         "https://example.com/my-model.gguf",
				Preferences: preferences,
			}

			_, err := importer.Import(details)
			Expect(err).To(HaveOccurred())
		})

		It("should extract filename correctly from URI with path", func() {
			details := importers.Details{
				URI: "https://example.com/path/to/model.gguf",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(len(modelConfig.Files)).To(Equal(1), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].URI).To(Equal("https://example.com/path/to/model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("drop-in backend preferences", func() {
		// baseline: no preference keeps backend: llama-cpp and the file
		// layout that downstream assertions depend on.
		It("emits backend: llama-cpp when no backend preference is set", func() {
			details := Details{
				URI: "https://example.com/my-model.gguf",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("swaps the emitted backend to ik-llama-cpp when preferred", func() {
			preferences := json.RawMessage(`{"backend": "ik-llama-cpp"}`)
			details := Details{
				URI:         "https://example.com/my-model.gguf",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: ik-llama-cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).NotTo(ContainSubstring("backend: llama-cpp\n"), fmt.Sprintf("Model config: %+v", modelConfig))
			// Model path must remain identical to the llama-cpp baseline.
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(len(modelConfig.Files)).To(Equal(1))
			Expect(modelConfig.Files[0].Filename).To(Equal("my-model.gguf"))
		})

		It("swaps the emitted backend to turboquant when preferred", func() {
			preferences := json.RawMessage(`{"backend": "turboquant"}`)
			details := Details{
				URI:         "https://example.com/my-model.gguf",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: turboquant"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).NotTo(ContainSubstring("backend: llama-cpp\n"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(len(modelConfig.Files)).To(Equal(1))
			Expect(modelConfig.Files[0].Filename).To(Equal("my-model.gguf"))
		})

		It("keeps backend: llama-cpp for unknown backend preferences", func() {
			// Unknown backend values must not leak into the emitted YAML —
			// we only honour the two curated drop-in replacements.
			preferences := json.RawMessage(`{"backend": "something-weird"}`)
			details := Details{
				URI:         "https://example.com/my-model.gguf",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("Import from HuggingFace file listing", func() {
		// These tests exercise the HF branch of Import() without touching
		// the network — they construct a fake *hfapi.ModelDetails and
		// assert the emitted gallery entry directly. Historically the HF
		// branch was only covered by live-API integration specs in
		// importers_test.go; anything that happened in between (shard
		// grouping, quant fallback) had no unit-level regression net.

		const repoBase = "https://huggingface.co/acme/example-GGUF/resolve/main/"

		hfFile := func(path, sha string) hfapi.ModelFile {
			return hfapi.ModelFile{
				Path:   path,
				SHA256: sha,
				URL:    repoBase + path,
			}
		}

		withHF := func(preferences string, files ...hfapi.ModelFile) Details {
			d := Details{
				URI: "https://huggingface.co/acme/example-GGUF",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "acme/example-GGUF",
					Files:   files,
				},
			}
			if preferences != "" {
				d.Preferences = json.RawMessage(preferences)
			}
			return d
		}

		It("picks the preferred quant in a single-file repo", func() {
			details := withHF(`{"name":"example","quantizations":"Q4_K_M"}`,
				hfFile("model-Q4_K_M.gguf", "aaa"),
				hfFile("model-Q3_K_M.gguf", "bbb"),
				hfFile("README.md", ""),
			)

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Files).To(HaveLen(1), fmt.Sprintf("%+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("llama-cpp/models/example/model-Q4_K_M.gguf"))
			Expect(modelConfig.Files[0].URI).To(Equal(repoBase + "model-Q4_K_M.gguf"))
			Expect(modelConfig.Files[0].SHA256).To(Equal("aaa"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: llama-cpp/models/example/model-Q4_K_M.gguf"))
		})

		It("falls back to the last group when no preference matches", func() {
			// Default preference is q4_k_m; the repo has only Q8_0 and
			// Q3_K_M. The old implementation would emit exactly the last
			// file seen — this test pins the fallback behaviour so the
			// group-level fallback keeps matching the historical intent.
			details := withHF(`{"name":"example"}`,
				hfFile("model-Q8_0.gguf", "aaa"),
				hfFile("model-Q3_K_M.gguf", "bbb"),
			)

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Files).To(HaveLen(1), fmt.Sprintf("%+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("llama-cpp/models/example/model-Q3_K_M.gguf"))
		})

		It("emits all shards of a multi-part GGUF and points Model at shard 1", func() {
			// Regression for PR #9510: unsloth/Kimi-K2.6-GGUF ships 14
			// Q8_K_XL shards. Default prefs are q4_k_m; none match, so the
			// fallback must take the whole shard group (not just the last
			// shard) and the config's `model:` must point at shard 1 so
			// llama.cpp's split loader can walk the rest.
			files := make([]hfapi.ModelFile, 0, 14)
			// Deliberately add shards out of order to prove sorting works.
			for _, idx := range []int{7, 1, 14, 2, 8, 3, 9, 4, 10, 5, 11, 6, 12, 13} {
				files = append(files, hfFile(
					fmt.Sprintf("Kimi-K2.6-UD-Q8_K_XL-%05d-of-00014.gguf", idx),
					fmt.Sprintf("sha-%02d", idx),
				))
			}

			details := withHF(`{"name":"Kimi-K2.6-GGUF"}`, files...)

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Files).To(HaveLen(14), fmt.Sprintf("%+v", modelConfig))

			// All 14 shards must be present, in order, under the models dir.
			for i := 1; i <= 14; i++ {
				expected := fmt.Sprintf("llama-cpp/models/Kimi-K2.6-GGUF/Kimi-K2.6-UD-Q8_K_XL-%05d-of-00014.gguf", i)
				Expect(modelConfig.Files[i-1].Filename).To(Equal(expected))
				Expect(modelConfig.Files[i-1].SHA256).To(Equal(fmt.Sprintf("sha-%02d", i)))
			}

			// The configured model path must be shard 1 — this is the file
			// llama.cpp's split loader expects to be pointed at.
			Expect(modelConfig.ConfigFile).To(ContainSubstring(
				"model: llama-cpp/models/Kimi-K2.6-GGUF/Kimi-K2.6-UD-Q8_K_XL-00001-of-00014.gguf",
			))
		})

		It("emits all shards of the preferred quant alongside an mmproj", func() {
			// Sharded multimodal model: mmproj is single-file, the text
			// model is split in 3 parts and matches the user preference.
			details := withHF(`{"name":"VL-GGUF","quantizations":"Q4_K_M","mmproj_quantizations":"F16"}`,
				hfFile("mmproj-F16.gguf", "mm"),
				hfFile("model-Q4_K_M-00001-of-00003.gguf", "p1"),
				hfFile("model-Q4_K_M-00002-of-00003.gguf", "p2"),
				hfFile("model-Q4_K_M-00003-of-00003.gguf", "p3"),
			)

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Files).To(HaveLen(4), fmt.Sprintf("%+v", modelConfig))

			// Model shards come first, in order, then the mmproj.
			Expect(modelConfig.Files[0].Filename).To(Equal("llama-cpp/models/VL-GGUF/model-Q4_K_M-00001-of-00003.gguf"))
			Expect(modelConfig.Files[1].Filename).To(Equal("llama-cpp/models/VL-GGUF/model-Q4_K_M-00002-of-00003.gguf"))
			Expect(modelConfig.Files[2].Filename).To(Equal("llama-cpp/models/VL-GGUF/model-Q4_K_M-00003-of-00003.gguf"))
			Expect(modelConfig.Files[3].Filename).To(Equal("llama-cpp/mmproj/VL-GGUF/mmproj-F16.gguf"))

			Expect(modelConfig.ConfigFile).To(ContainSubstring(
				"model: llama-cpp/models/VL-GGUF/model-Q4_K_M-00001-of-00003.gguf",
			))
			Expect(modelConfig.ConfigFile).To(ContainSubstring(
				"mmproj: llama-cpp/mmproj/VL-GGUF/mmproj-F16.gguf",
			))
		})

		It("does not emit duplicate entries when called repeatedly on the same group", func() {
			// Guards appendShardGroup's dedup: if a shard ends up in the
			// Files slice via more than one code path (e.g. a future
			// refactor that processes mmproj and model candidates through
			// the same path), we must not accidentally duplicate downloads.
			details := withHF(`{"name":"dup","quantizations":"Q4_K_M,Q4_K_M"}`,
				hfFile("model-Q4_K_M.gguf", "aaa"),
			)

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Files).To(HaveLen(1), fmt.Sprintf("%+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("llama-cpp/models/dup/model-Q4_K_M.gguf"))
		})

		It("ignores non-gguf files in the repo listing", func() {
			// Real HF repos ship READMEs, tokenizer json, images, etc.
			// Only .gguf entries should surface as downloadable files.
			details := withHF(`{"name":"noise"}`,
				hfFile("README.md", ""),
				hfFile("config.json", ""),
				hfFile("logo.png", ""),
				hfFile("model-Q4_K_M.gguf", "aaa"),
			)

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Files).To(HaveLen(1))
			Expect(modelConfig.Files[0].Filename).To(Equal("llama-cpp/models/noise/model-Q4_K_M.gguf"))
		})

		It("produces no files when the repo contains no .gguf", func() {
			details := withHF(`{"name":"empty"}`,
				hfFile("README.md", ""),
				hfFile("config.json", ""),
			)

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Files).To(BeEmpty())
		})
	})

	Context("AdditionalBackends", func() {
		It("advertises ik-llama-cpp and turboquant as drop-in replacements", func() {
			entries := importer.AdditionalBackends()

			names := make([]string, 0, len(entries))
			byName := map[string]importers.KnownBackendEntry{}
			for _, e := range entries {
				names = append(names, e.Name)
				byName[e.Name] = e
			}
			Expect(names).To(ConsistOf("ik-llama-cpp", "turboquant"))

			ik := byName["ik-llama-cpp"]
			Expect(ik.Modality).To(Equal("text"))
			Expect(ik.Description).NotTo(BeEmpty())

			tq := byName["turboquant"]
			Expect(tq.Modality).To(Equal("text"))
			Expect(tq.Description).NotTo(BeEmpty())
		})
	})
})
