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
	Name       string         `json:"name"`
	Label      string         `json:"label"`
	Repository string         `json:"repository"`
	Model      string         `json:"model"`
	Files      []gallery.File `json:"files"`
}

type KimodoCppImporter struct{}

func (*KimodoCppImporter) Name() string      { return "kimodocpp" }
func (*KimodoCppImporter) Modality() string  { return "3d_animation" }
func (*KimodoCppImporter) AutoDetects() bool { return true }

func kimodoImportModel(uri string) (kimodoModel, bool) {
	var models []kimodoModel
	if err := json.Unmarshal(kimodoModelsJSON, &models); err != nil {
		return kimodoModel{}, false
	}
	owner, repo, ok := HFOwnerRepoFromURI(uri)
	if !ok {
		return kimodoModel{}, false
	}
	for _, model := range models {
		if strings.EqualFold(owner+"/"+repo, model.Repository) {
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
	_, found := kimodoImportModel(details.URI)
	return found
}

func (*KimodoCppImporter) Import(details Details) (gallery.ModelConfig, error) {
	selected, found := kimodoImportModel(details.URI)
	if !found {
		return gallery.ModelConfig{}, fmt.Errorf("kimodocpp: choose a published LocalAI-io Kimodo SOMA or G1 GGML motion repository")
	}
	var preferences struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if len(details.Preferences) > 0 {
		if err := json.Unmarshal(details.Preferences, &preferences); err != nil {
			return gallery.ModelConfig{}, err
		}
	}
	if preferences.Name == "" {
		preferences.Name = selected.Name
	}
	if preferences.Description == "" {
		preferences.Description = "Kimodo " + selected.Label + " text-to-motion (animated skeleton GLB)"
	}
	modelConfig := config.ModelConfig{
		Name: preferences.Name, Description: preferences.Description, Backend: "kimodocpp",
		KnownUsecaseStrings: []string{"FLAG_3D_ANIMATION"},
		Options:             []string{"text_bundle:kimodo/text", "frames:150", "steps:100", "text_guidance:2"},
		PredictionOptions:   schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: selected.Model}},
	}
	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	return gallery.ModelConfig{Name: preferences.Name, Description: preferences.Description, Files: selected.Files, ConfigFile: string(data)}, nil
}
