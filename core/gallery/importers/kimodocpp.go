// SPDX-License-Identifier: MIT
package importers

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"go.yaml.in/yaml/v2"
)

// Pinned published manifests, including the text bundle shared by all motion
// models. Keep the gallery entries and this import inventory in sync.
//
//go:embed kimodo_models.json
var kimodoModelsJSON []byte

type kimodoModel struct {
	Name             string         `json:"name"`
	Label            string         `json:"label"`
	Repository       string         `json:"repository"`
	Model            string         `json:"model"`
	TextQuantization string         `json:"text_quantization"`
	TextModel        string         `json:"text_model"`
	Files            []gallery.File `json:"files"`
}

type KimodoCppImporter struct{}

func (*KimodoCppImporter) Name() string      { return "kimodocpp" }
func (*KimodoCppImporter) Modality() string  { return "3d_animation" }
func (*KimodoCppImporter) AutoDetects() bool { return true }

func kimodoImportModel(uri, quantization string) (kimodoModel, bool) {
	var models []kimodoModel
	if err := json.Unmarshal(kimodoModelsJSON, &models); err != nil {
		return kimodoModel{}, false
	}
	owner, repo, ok := HFOwnerRepoFromURI(uri)
	if !ok {
		return kimodoModel{}, false
	}
	for _, model := range models {
		if strings.EqualFold(owner+"/"+repo, model.Repository) && model.TextQuantization == quantization {
			return model, true
		}
	}
	return kimodoModel{}, false
}

func (*KimodoCppImporter) Match(details Details) bool {
	var preferences struct {
		Backend string `json:"backend"`
	}
	if len(details.Preferences) > 0 {
		if err := json.Unmarshal(details.Preferences, &preferences); err != nil {
			return false
		}
	}
	if preferences.Backend != "" {
		return preferences.Backend == "kimodocpp"
	}
	_, found := kimodoImportModel(details.URI, "q8_0")
	return found
}

func (*KimodoCppImporter) Import(details Details) (gallery.ModelConfig, error) {
	var preferences struct {
		Name             string `json:"name"`
		Description      string `json:"description"`
		TextQuantization string `json:"text_quantization"`
	}
	if len(details.Preferences) > 0 {
		if err := json.Unmarshal(details.Preferences, &preferences); err != nil {
			return gallery.ModelConfig{}, err
		}
	}
	quantization := strings.ToLower(preferences.TextQuantization)
	if quantization == "" {
		quantization = "q8_0"
	}
	switch quantization {
	case "q8_0", "q6_k", "q5_k", "q4_k_m", "q4_k", "bf16":
	default:
		return gallery.ModelConfig{}, fmt.Errorf("kimodocpp: unsupported text_quantization %q", preferences.TextQuantization)
	}
	selected, found := kimodoImportModel(details.URI, quantization)
	if !found {
		return gallery.ModelConfig{}, fmt.Errorf("kimodocpp: choose a published LocalAI-io Kimodo SOMA or G1 GGML motion repository")
	}
	if preferences.Name == "" {
		preferences.Name = selected.Name
	}
	if preferences.Description == "" {
		preferences.Description = "Kimodo " + selected.Label + " text-to-motion with " + strings.ToUpper(quantization) + " text encoder (animated skeleton GLB)"
	}
	modelConfig := config.ModelConfig{
		Name: preferences.Name, Description: preferences.Description, Backend: "kimodocpp",
		KnownUsecaseStrings: []string{"FLAG_3D_ANIMATION"},
		Options:             []string{"text_bundle:" + selected.TextModel, "frames:150", "steps:100", "text_guidance:2"},
		PredictionOptions:   schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: selected.Model}},
	}
	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	return gallery.ModelConfig{Name: preferences.Name, Description: preferences.Description, Files: selected.Files, ConfigFile: string(data)}, nil
}
