package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WhisperImporter", func() {
	Context("detection from HuggingFace", func() {
		It("matches ggerganov/whisper.cpp by ggml-*.bin file", func() {
			// Live HF API call; the repo lists ggml-base.en.bin among others.
			uri := "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: whisper"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("transcript"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=whisper for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "whisper"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: whisper"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	// Real-world repo that ships *multiple* ggml-*.bin quantizations
	// (ggml-model-q4_0.bin, ggml-model-q5_0.bin, ggml-model-q8_0.bin).
	// We assert the importer (a) follows the HF metadata branch — not the
	// URL branch — when given the repo URL, (b) lays files out under
	// whisper/models/<name>/ like llama-cpp does, and (c) honours the
	// quantizations preference, defaulting to q5_0.
	Context("real-world multi-quant repo: LocalAI-io/whisper-large-v3-it-yodas-only-ggml", func() {
		const (
			uri  = "https://huggingface.co/LocalAI-io/whisper-large-v3-it-yodas-only-ggml"
			name = "whisper-large-v3-it-yodas-only-ggml"
		)

		It("defaults to q5_0 and nests the file under whisper/models/<name>/", func() {
			modelConfig, err := importers.DiscoverModelConfig(uri, json.RawMessage(`{}`))

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: whisper"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("transcript"))

			Expect(modelConfig.Files).To(HaveLen(1), fmt.Sprintf("Model config: %+v", modelConfig))

			expectedPath := "whisper/models/" + name + "/ggml-model-q5_0.bin"
			Expect(modelConfig.Files[0].Filename).To(Equal(expectedPath))
			Expect(modelConfig.Files[0].URI).To(Equal(uri + "/resolve/main/ggml-model-q5_0.bin"))
			Expect(modelConfig.Files[0].SHA256).ToNot(BeEmpty(), "HF metadata should provide a sha256")
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: " + expectedPath))
		})

		It("honours preferences.quantizations=q4_0 to pick ggml-model-q4_0.bin", func() {
			modelConfig, err := importers.DiscoverModelConfig(uri, json.RawMessage(`{"quantizations":"q4_0"}`))

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Files).To(HaveLen(1))

			expectedPath := "whisper/models/" + name + "/ggml-model-q4_0.bin"
			Expect(modelConfig.Files[0].Filename).To(Equal(expectedPath))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: " + expectedPath))
		})
	})

	Context("Import from HuggingFace file listing (offline)", func() {
		// Mirror of llama-cpp_test.go's offline HF context: build a fake
		// *hfapi.ModelDetails and assert the emitted gallery entry without
		// touching the network.
		const repoBase = "https://huggingface.co/acme/example-ggml/resolve/main/"

		hfFile := func(path, sha string) hfapi.ModelFile {
			return hfapi.ModelFile{
				Path:   path,
				SHA256: sha,
				URL:    repoBase + path,
			}
		}

		withHF := func(preferences string, files ...hfapi.ModelFile) importers.Details {
			d := importers.Details{
				URI: "https://huggingface.co/acme/example-ggml",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "acme/example-ggml",
					Files:   files,
				},
			}
			if preferences != "" {
				d.Preferences = json.RawMessage(preferences)
			}
			return d
		}

		It("falls back to the last ggml file when no preference matches", func() {
			imp := &importers.WhisperImporter{}
			details := withHF(`{"name":"example"}`,
				hfFile("ggml-model-q4_0.bin", "aaa"),
				hfFile("ggml-model-q8_0.bin", "ccc"),
				hfFile("README.md", ""),
			)

			modelConfig, err := imp.Import(details)
			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Files).To(HaveLen(1))
			// Default pref is q5_0; repo has only q4_0 and q8_0 — fallback
			// is the last ggml entry, mirroring llama-cpp's behaviour.
			Expect(modelConfig.Files[0].Filename).To(Equal("whisper/models/example/ggml-model-q8_0.bin"))
			Expect(modelConfig.Files[0].SHA256).To(Equal("ccc"))
		})

		It("ignores non-ggml files in the repo listing", func() {
			imp := &importers.WhisperImporter{}
			details := withHF(`{"name":"noise","quantizations":"q5_0"}`,
				hfFile("README.md", ""),
				hfFile("config.json", ""),
				hfFile("ggml-model-q5_0.bin", "bbb"),
			)

			modelConfig, err := imp.Import(details)
			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Files).To(HaveLen(1))
			Expect(modelConfig.Files[0].Filename).To(Equal("whisper/models/noise/ggml-model-q5_0.bin"))
		})
	})

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.WhisperImporter{}
			Expect(imp.Name()).To(Equal("whisper"))
			Expect(imp.Modality()).To(Equal("asr"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})
})
