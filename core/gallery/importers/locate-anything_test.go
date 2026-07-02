package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LocateAnythingImporter", func() {
	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.LocateAnythingImporter{}
			Expect(imp.Name()).To(Equal("locate-anything-cpp"))
			Expect(imp.Modality()).To(Equal("detection"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("Match", func() {
		It("matches when backend preference is locate-anything-cpp", func() {
			imp := &importers.LocateAnythingImporter{}
			preferences := json.RawMessage(`{"backend": "locate-anything-cpp"}`)
			details := importers.Details{
				URI:         "https://example.com/some-model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches when the repo name contains 'locate-anything' (case-insensitive)", func() {
			imp := &importers.LocateAnythingImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/mudler/locate-anything-cpp-3b",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "mudler/Locate-Anything-CPP-3B",
					Author:  "mudler",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches when the repo name contains 'locateanything' (case-insensitive)", func() {
			imp := &importers.LocateAnythingImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/nvidia/LocateAnything-3B",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "nvidia/LocateAnything-3B",
					Author:  "nvidia",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches via URI fallback when HuggingFace details are missing", func() {
			imp := &importers.LocateAnythingImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/nvidia/LocateAnything-3B",
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("does not match unrelated repos without locate-anything signals", func() {
			imp := &importers.LocateAnythingImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/meta-llama/Llama-3-8B",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "meta-llama/Llama-3-8B",
					Author:  "meta-llama",
				},
			}

			Expect(imp.Match(details)).To(BeFalse())
		})

		It("does not match an rfdetr repo", func() {
			imp := &importers.LocateAnythingImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/mudler/rfdetr-cpp-nano",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "mudler/rfdetr-cpp-nano",
					Author:  "mudler",
				},
			}

			Expect(imp.Match(details)).To(BeFalse())
		})

		It("returns false for invalid preferences JSON", func() {
			imp := &importers.LocateAnythingImporter{}
			preferences := json.RawMessage(`not valid json`)
			details := importers.Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("produces a YAML with backend locate-anything-cpp and the repo as the model", func() {
			imp := &importers.LocateAnythingImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/nvidia/LocateAnything-3B",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "nvidia/LocateAnything-3B",
					Author:  "nvidia",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: locate-anything-cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("nvidia/LocateAnything-3B"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("detection"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("respects custom name and description from preferences", func() {
			imp := &importers.LocateAnythingImporter{}
			preferences := json.RawMessage(`{"name": "my-locate", "description": "Custom"}`)
			details := importers.Details{
				URI:         "https://huggingface.co/nvidia/LocateAnything-3B",
				Preferences: preferences,
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "nvidia/LocateAnything-3B",
					Author:  "nvidia",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-locate"))
			Expect(modelConfig.Description).To(Equal("Custom"))
		})
	})

	// Table-driven coverage of the backend routing: locate-anything repos
	// always route to the native locate-anything-cpp backend, with an
	// explicit preferences.backend override honoured.
	//
	// Cases are kept offline-deterministic by injecting Details directly
	// rather than going through DiscoverModelConfig (which would hit live HF).
	Context("backend routing (offline)", func() {
		hfFile := func(path string) hfapi.ModelFile {
			return hfapi.ModelFile{Path: path}
		}

		type tc struct {
			name          string
			uri           string
			modelID       string
			files         []hfapi.ModelFile
			prefs         string
			expectBackend string // expected `backend:` line content
		}

		entries := []tc{
			{
				name:          "canonical NVIDIA repo routes to locate-anything-cpp",
				uri:           "https://huggingface.co/nvidia/LocateAnything-3B",
				modelID:       "nvidia/LocateAnything-3B",
				files:         []hfapi.ModelFile{hfFile("locate-anything-3b-q8_0.gguf"), hfFile("README.md")},
				prefs:         "",
				expectBackend: "backend: locate-anything-cpp",
			},
			{
				name:          "GGUF bundle with locate-anything name routes to locate-anything-cpp",
				uri:           "https://huggingface.co/mudler/locate-anything.cpp-3b",
				modelID:       "mudler/locate-anything.cpp-3b",
				files:         []hfapi.ModelFile{hfFile("model-f16.gguf")},
				prefs:         "",
				expectBackend: "backend: locate-anything-cpp",
			},
			{
				name:          "explicit preferences.backend override is honoured",
				uri:           "https://huggingface.co/nvidia/LocateAnything-3B",
				modelID:       "nvidia/LocateAnything-3B",
				files:         nil,
				prefs:         `{"backend": "locate-anything-cpp"}`,
				expectBackend: "backend: locate-anything-cpp",
			},
		}

		for _, e := range entries {
			e := e // capture for closure
			It(e.name, func() {
				imp := &importers.LocateAnythingImporter{}
				details := importers.Details{
					URI: e.uri,
					HuggingFace: &hfapi.ModelDetails{
						ModelID: e.modelID,
						Files:   e.files,
					},
				}
				if e.prefs != "" {
					details.Preferences = json.RawMessage(e.prefs)
				}

				Expect(imp.Match(details)).To(BeTrue(), fmt.Sprintf("Match should fire for %+v", details))

				modelConfig, err := imp.Import(details)
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Import error: %v", err))
				Expect(modelConfig.ConfigFile).To(ContainSubstring(e.expectBackend),
					fmt.Sprintf("Model config: %+v", modelConfig))
			})
		}
	})
})
