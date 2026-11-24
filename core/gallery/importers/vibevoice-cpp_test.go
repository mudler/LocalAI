package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("VibeVoiceCppImporter", func() {
	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.VibeVoiceCppImporter{}
			Expect(imp.Name()).To(Equal("vibevoice-cpp"))
			Expect(imp.Modality()).To(Equal("tts"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=vibevoice-cpp for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "vibevoice-cpp"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: vibevoice-cpp"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("tokenizer=tokenizer.gguf"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("tts"))
		})

		It("emits an ASR skeleton when usecase=asr is requested with no HF metadata", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "vibevoice-cpp", "usecase": "asr"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: vibevoice-cpp"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("type=asr"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("transcript"))
		})
	})

	// Live HF call against the canonical bundle. Marked broad: it shouldn't
	// be brittle to upstream adding more quants/voices — we only assert that
	// the realtime TTS path was picked and the tokenizer was bundled.
	Context("detection from HuggingFace: mudler/vibevoice.cpp-models", func() {
		const uri = "https://huggingface.co/mudler/vibevoice.cpp-models"

		It("routes to vibevoice-cpp, picks the realtime TTS GGUF and bundles tokenizer + voice prompt", func() {
			modelConfig, err := importers.DiscoverModelConfig(uri, json.RawMessage(`{}`))

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: vibevoice-cpp"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("tts"))

			// Primary model must be the realtime variant (TTS default).
			Expect(modelConfig.ConfigFile).To(ContainSubstring("vibevoice-realtime-"))

			// Tokenizer is mandatory and must show up both as a downloaded
			// file and as a tokenizer= option entry. The path is rooted
			// under vibevoice-cpp/<name>/ so multiple imports don't collide.
			var sawTokenizerFile, sawModelFile, sawVoiceFile bool
			for _, f := range modelConfig.Files {
				if f.Filename == "" {
					continue
				}
				if filepathBase(f.Filename) == "tokenizer.gguf" {
					sawTokenizerFile = true
				}
				if startsWith(filepathBase(f.Filename), "vibevoice-realtime-") {
					sawModelFile = true
				}
				if startsWith(filepathBase(f.Filename), "voice-") {
					sawVoiceFile = true
				}
			}
			Expect(sawTokenizerFile).To(BeTrue(), fmt.Sprintf("expected tokenizer.gguf in Files, got: %+v", modelConfig.Files))
			Expect(sawModelFile).To(BeTrue(), fmt.Sprintf("expected a vibevoice-realtime-*.gguf in Files, got: %+v", modelConfig.Files))
			Expect(sawVoiceFile).To(BeTrue(), fmt.Sprintf("expected a voice-*.gguf in Files, got: %+v", modelConfig.Files))

			Expect(modelConfig.ConfigFile).To(ContainSubstring("tokenizer=vibevoice-cpp/"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("voice=vibevoice-cpp/"))
		})

		It("routes to ASR + diarization when preferences.usecase=asr", func() {
			modelConfig, err := importers.DiscoverModelConfig(uri, json.RawMessage(`{"usecase":"asr"}`))

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: vibevoice-cpp"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("transcript"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("type=asr"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("vibevoice-asr-"))
			// ASR must NOT bundle a voice prompt — the backend ignores it
			// for transcription and we don't want gratuitous downloads.
			Expect(modelConfig.ConfigFile).ToNot(ContainSubstring("voice="))
		})
	})

	// Offline fixtures — assert the end-to-end shape of what the importer
	// emits without depending on HF availability or upstream file lists.
	Context("Import from HuggingFace file listing (offline)", func() {
		const repoBase = "https://huggingface.co/mudler/vibevoice.cpp-models/resolve/main/"

		hfFile := func(path, sha string) hfapi.ModelFile {
			return hfapi.ModelFile{
				Path:   path,
				SHA256: sha,
				URL:    repoBase + path,
			}
		}

		withHF := func(preferences string, files ...hfapi.ModelFile) importers.Details {
			d := importers.Details{
				URI: "https://huggingface.co/mudler/vibevoice.cpp-models",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "mudler/vibevoice.cpp-models",
					Files:   files,
				},
			}
			if preferences != "" {
				d.Preferences = json.RawMessage(preferences)
			}
			return d
		}

		It("defaults to TTS realtime + tokenizer + first voice, nested under vibevoice-cpp/<name>/", func() {
			imp := &importers.VibeVoiceCppImporter{}
			details := withHF(`{"name":"vibe"}`,
				hfFile("vibevoice-realtime-0.5B-q8_0.gguf", "aaa"),
				hfFile("vibevoice-asr-q4_k.gguf", "bbb"),
				hfFile("tokenizer.gguf", "ccc"),
				hfFile("voice-en-Carter_man.gguf", "ddd"),
				hfFile("voice-en-Emma.gguf", "eee"),
				hfFile("README.md", ""),
			)

			modelConfig, err := imp.Import(details)
			Expect(err).ToNot(HaveOccurred())

			Expect(modelConfig.Files).To(HaveLen(3))
			byName := map[string]string{}
			for _, f := range modelConfig.Files {
				byName[filepathBase(f.Filename)] = f.Filename
			}
			Expect(byName).To(HaveKey("vibevoice-realtime-0.5B-q8_0.gguf"))
			Expect(byName).To(HaveKey("tokenizer.gguf"))
			Expect(byName).To(HaveKey("voice-en-Carter_man.gguf"))
			Expect(byName["tokenizer.gguf"]).To(Equal("vibevoice-cpp/vibe/tokenizer.gguf"))

			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: vibevoice-cpp"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: vibevoice-cpp/vibe/vibevoice-realtime-0.5B-q8_0.gguf"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- tokenizer=vibevoice-cpp/vibe/tokenizer.gguf"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- voice=vibevoice-cpp/vibe/voice-en-Carter_man.gguf"))
			Expect(modelConfig.ConfigFile).ToNot(ContainSubstring("type=asr"))
		})

		It("routes to ASR when preferences.usecase=asr and skips voice prompts", func() {
			imp := &importers.VibeVoiceCppImporter{}
			details := withHF(`{"name":"vibe-asr","usecase":"asr"}`,
				hfFile("vibevoice-realtime-0.5B-q8_0.gguf", "aaa"),
				hfFile("vibevoice-asr-q4_k.gguf", "bbb"),
				hfFile("vibevoice-asr-q8_0.gguf", "fff"),
				hfFile("tokenizer.gguf", "ccc"),
				hfFile("voice-en-Emma.gguf", "ddd"),
			)

			modelConfig, err := imp.Import(details)
			Expect(err).ToNot(HaveOccurred())

			Expect(modelConfig.Files).To(HaveLen(2))
			byName := map[string]string{}
			for _, f := range modelConfig.Files {
				byName[filepathBase(f.Filename)] = f.Filename
			}
			// Default quant order picks q8_0 over q4_k.
			Expect(byName).To(HaveKey("vibevoice-asr-q8_0.gguf"))
			Expect(byName).To(HaveKey("tokenizer.gguf"))

			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: vibevoice-cpp/vibe-asr/vibevoice-asr-q8_0.gguf"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- type=asr"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- tokenizer=vibevoice-cpp/vibe-asr/tokenizer.gguf"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("transcript"))
			Expect(modelConfig.ConfigFile).ToNot(ContainSubstring("voice="))
		})

		It("honours preferences.quantizations to pick a specific quant", func() {
			imp := &importers.VibeVoiceCppImporter{}
			details := withHF(`{"name":"vibe","quantizations":"q4_k"}`,
				hfFile("vibevoice-asr-q4_k.gguf", "aaa"),
				hfFile("vibevoice-asr-q8_0.gguf", "bbb"),
				hfFile("tokenizer.gguf", "ccc"),
			)

			modelConfig, err := imp.Import(details)
			Expect(err).ToNot(HaveOccurred())

			// Repo only ships ASR — auto-routes to asr, picks the requested
			// quant, emits type=asr automatically.
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: vibevoice-cpp/vibe/vibevoice-asr-q4_k.gguf"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- type=asr"))
		})

		It("honours preferences.voice to pick a specific voice prompt", func() {
			imp := &importers.VibeVoiceCppImporter{}
			details := withHF(`{"name":"vibe","voice":"Emma"}`,
				hfFile("vibevoice-realtime-0.5B-q8_0.gguf", "aaa"),
				hfFile("tokenizer.gguf", "bbb"),
				hfFile("voice-en-Carter_man.gguf", "ccc"),
				hfFile("voice-en-Emma.gguf", "ddd"),
			)

			modelConfig, err := imp.Import(details)
			Expect(err).ToNot(HaveOccurred())

			Expect(modelConfig.ConfigFile).To(ContainSubstring("- voice=vibevoice-cpp/vibe/voice-en-Emma.gguf"))
			Expect(modelConfig.ConfigFile).ToNot(ContainSubstring("voice-en-Carter_man"))
		})
	})

	// Make sure we don't regress the existing Python-backend importer for
	// repos that don't carry the C++ port's signal (e.g. microsoft/VibeVoice-1.5B).
	Context("non-cpp vibevoice repos still route to the Python importer", func() {
		It("does not claim microsoft/VibeVoice-1.5B (no GGUF / no .cpp suffix)", func() {
			imp := &importers.VibeVoiceCppImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/microsoft/VibeVoice-1.5B",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "microsoft/VibeVoice-1.5B",
					Files: []hfapi.ModelFile{
						{Path: "config.json"},
						{Path: "model.safetensors"},
					},
				},
				Preferences: json.RawMessage(`{}`),
			}
			Expect(imp.Match(details)).To(BeFalse())
		})
	})
})

// filepathBase / startsWith are tiny helpers so the test file stays
// stdlib-only and doesn't pull in path/filepath + strings just for the
// expected-shape assertions.
func filepathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
