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

var _ Importer = &NemoImporter{}

// NemoImporter matches NVIDIA NeMo ASR checkpoints, which always ship as
// single-file ".nemo" archives under NVIDIA-owned HF repositories. Combining
// owner=nvidia with the .nemo extension is narrow enough to avoid picking up
// the unrelated NVIDIA LLM repos that only carry safetensors weights.
// preferences.backend="nemo" overrides detection.
type NemoImporter struct{}

func (i *NemoImporter) Name() string      { return "nemo" }
func (i *NemoImporter) Modality() string  { return "asr" }
func (i *NemoImporter) AutoDetects() bool { return true }

func (i *NemoImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "nemo" {
		return true
	}

	if details.HuggingFace == nil {
		return false
	}
	if !strings.EqualFold(details.HuggingFace.Author, "nvidia") {
		return false
	}
	return HasExtension(details.HuggingFace.Files, ".nemo")
}

func (i *NemoImporter) Import(details Details) (gallery.ModelConfig, error) {
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
		Backend:             "nemo",
		KnownUsecaseStrings: []string{"transcript"},
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
