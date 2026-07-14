package importers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"go.yaml.in/yaml/v2"
)

var _ Importer = &VoxCPMImporter{}

// VoxCPMImporter recognises OpenBMB's VoxCPM TTS family (VoxCPM, VoxCPM1.5,
// VoxCPM2, …). The weights ship under a variety of owners (openbmb,
// mlx-community, quantised mirrors) so detection keys off the repo-name
// substring rather than the owner. preferences.backend="voxcpm" overrides.
type VoxCPMImporter struct{}

func (i *VoxCPMImporter) Name() string      { return "voxcpm" }
func (i *VoxCPMImporter) Modality() string  { return "tts" }
func (i *VoxCPMImporter) AutoDetects() bool { return true }

func (i *VoxCPMImporter) Match(details Details) bool {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return false
	}
	preferencesMap := make(map[string]any)
	if len(preferences) > 0 {
		if err := json.Unmarshal(preferences, &preferencesMap); err != nil {
			return false
		}
	}

	if b, ok := preferencesMap["backend"].(string); ok && b == "voxcpm" {
		return true
	}

	if details.HuggingFace != nil {
		repoName := details.HuggingFace.ModelID
		if idx := strings.Index(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		if strings.Contains(strings.ToLower(repoName), "voxcpm") {
			return true
		}
	}

	if _, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		return strings.Contains(strings.ToLower(repo), "voxcpm")
	}
	return false
}

func (i *VoxCPMImporter) Import(details Details) (gallery.ModelConfig, error) {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	preferencesMap := make(map[string]any)
	if len(preferences) > 0 {
		if err := json.Unmarshal(preferences, &preferencesMap); err != nil {
			return gallery.ModelConfig{}, err
		}
	}

	name, ok := preferencesMap["name"].(string)
	if !ok {
		name = filepath.Base(details.URI)
	}

	description, ok := preferencesMap["description"].(string)
	if !ok {
		description = "Imported from " + details.URI
	}

	model := details.URI
	if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
		model = details.HuggingFace.ModelID
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "voxcpm",
		KnownUsecaseStrings: []string{"tts"},
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: model},
		},
	}

	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}

	return gallery.ModelConfig{
		Name:        name,
		Description: description,
		ConfigFile:  string(data),
	}, nil
}
