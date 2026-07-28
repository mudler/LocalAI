package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RFDetrImporter", func() {
	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.RFDetrImporter{}
			Expect(imp.Name()).To(Equal("rfdetr"))
			Expect(imp.Modality()).To(Equal("detection"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("Match", func() {
		It("matches when backend preference is rfdetr", func() {
			imp := &importers.RFDetrImporter{}
			preferences := json.RawMessage(`{"backend": "rfdetr"}`)
			details := importers.Details{
				URI:         "https://example.com/some-model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches when the repo name contains 'rf-detr' (case-insensitive)", func() {
			imp := &importers.RFDetrImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/roboflow/rf-detr-base",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "roboflow/rf-detr-base",
					Author:  "roboflow",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches when the repo name contains 'rfdetr' (case-insensitive)", func() {
			imp := &importers.RFDetrImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/some/rfdetr-whatever",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "some/rfdetr-whatever",
					Author:  "some",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches via URI fallback when HuggingFace details are missing", func() {
			imp := &importers.RFDetrImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/roboflow/rf-detr-base",
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("does not match unrelated repos without rfdetr signals", func() {
			imp := &importers.RFDetrImporter{}
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
			imp := &importers.RFDetrImporter{}
			preferences := json.RawMessage(`not valid json`)
			details := importers.Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("produces a YAML with backend rfdetr and the repo as the model", func() {
			imp := &importers.RFDetrImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/roboflow/rf-detr-base",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "roboflow/rf-detr-base",
					Author:  "roboflow",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: rfdetr"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("roboflow/rf-detr-base"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("respects custom name and description from preferences", func() {
			imp := &importers.RFDetrImporter{}
			preferences := json.RawMessage(`{"name": "my-detr", "description": "Custom"}`)
			details := importers.Details{
				URI:         "https://huggingface.co/roboflow/rf-detr-base",
				Preferences: preferences,
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "roboflow/rf-detr-base",
					Author:  "roboflow",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-detr"))
			Expect(modelConfig.Description).To(Equal("Custom"))
		})
	})

	// Table-driven coverage of the GGUF auto-routing path between the
	// Python rfdetr backend (HF transformer repos) and the native
	// rfdetr-cpp backend (GGUF repos like mudler/rfdetr-cpp-*).
	//
	// Cases are kept offline-deterministic by injecting Details directly
	// rather than going through DiscoverModelConfig (which would hit live HF).
	// The live HF cross-check lives in its own Context below.
	Context("GGUF auto-routing (offline)", func() {
		hfFile := func(path string) hfapi.ModelFile {
			return hfapi.ModelFile{Path: path}
		}

		type tc struct {
			name           string
			uri            string
			modelID        string
			files          []hfapi.ModelFile
			prefs          string
			expectBackend  string // expected `backend:` line content
			rejectBackends []string
		}

		entries := []tc{
			{
				name:          "GGUF repo with rfdetr-cpp prefix routes to rfdetr-cpp",
				uri:           "https://huggingface.co/mudler/rfdetr-cpp-nano",
				modelID:       "mudler/rfdetr-cpp-nano",
				files:         []hfapi.ModelFile{hfFile("rfdetr-nano-q8_0.gguf"), hfFile("README.md")},
				prefs:         "",
				expectBackend: "backend: rfdetr-cpp",
			},
			{
				name:          "GGUF presence alone routes to rfdetr-cpp even when repo name lacks -cpp",
				uri:           "https://huggingface.co/some/rf-detr-ggml",
				modelID:       "some/rf-detr-ggml",
				files:         []hfapi.ModelFile{hfFile("rfdetr-base-f16.gguf")},
				prefs:         "",
				expectBackend: "backend: rfdetr-cpp",
			},
			{
				name:           "transformer repo without GGUF stays on the Python rfdetr backend",
				uri:            "https://huggingface.co/roboflow/rf-detr-base",
				modelID:        "roboflow/rf-detr-base",
				files:          []hfapi.ModelFile{hfFile("config.json"), hfFile("pytorch_model.bin")},
				prefs:          "",
				expectBackend:  "backend: rfdetr\n",
				rejectBackends: []string{"backend: rfdetr-cpp"},
			},
			{
				name:           "explicit preferences.backend=rfdetr overrides GGUF auto-detect",
				uri:            "https://huggingface.co/mudler/rfdetr-cpp-nano",
				modelID:        "mudler/rfdetr-cpp-nano",
				files:          []hfapi.ModelFile{hfFile("rfdetr-nano-q8_0.gguf")},
				prefs:          `{"backend": "rfdetr"}`,
				expectBackend:  "backend: rfdetr\n",
				rejectBackends: []string{"backend: rfdetr-cpp"},
			},
			{
				name:          "explicit preferences.backend=rfdetr-cpp wins on non-GGUF transformer repo",
				uri:           "https://huggingface.co/roboflow/rf-detr-base",
				modelID:       "roboflow/rf-detr-base",
				files:         []hfapi.ModelFile{hfFile("config.json")},
				prefs:         `{"backend": "rfdetr-cpp"}`,
				expectBackend: "backend: rfdetr-cpp",
			},
			{
				name:          "repo name with rfdetr.cpp pattern routes to rfdetr-cpp even without HF file list",
				uri:           "https://huggingface.co/some/rfdetr.cpp-bundle",
				modelID:       "some/rfdetr.cpp-bundle",
				files:         nil,
				prefs:         "",
				expectBackend: "backend: rfdetr-cpp",
			},
		}

		for _, e := range entries {
			e := e // capture for closure
			It(e.name, func() {
				imp := &importers.RFDetrImporter{}
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

				// Match must always be true for these fixtures — they're
				// either preference-driven or have an rfdetr/rf-detr token.
				Expect(imp.Match(details)).To(BeTrue(), fmt.Sprintf("Match should fire for %+v", details))

				modelConfig, err := imp.Import(details)
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Import error: %v", err))
				Expect(modelConfig.ConfigFile).To(ContainSubstring(e.expectBackend),
					fmt.Sprintf("Model config: %+v", modelConfig))
				for _, rej := range e.rejectBackends {
					Expect(modelConfig.ConfigFile).ToNot(ContainSubstring(rej),
						fmt.Sprintf("did not expect %q in: %+v", rej, modelConfig))
				}
			})
		}
	})

	// Live HF cross-check: the canonical native GGUF repo for the
	// rfdetr-cpp backend. Marked broad — we only assert the routing
	// decision, not file lists (upstream may add quants over time).
	Context("detection from HuggingFace: mudler/rfdetr-cpp-nano", func() {
		It("auto-routes to the native rfdetr-cpp backend without preferences", func() {
			uri := "https://huggingface.co/mudler/rfdetr-cpp-nano"
			modelConfig, err := importers.DiscoverModelConfig(uri, json.RawMessage(`{}`))

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: rfdetr-cpp"),
				fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("mudler/rfdetr-cpp-nano"))
		})
	})
})
