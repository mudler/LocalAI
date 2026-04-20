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

var _ Importer = &FishSpeechImporter{}

// FishSpeechImporter recognises Fish Audio's open-weights TTS releases
// (Fish Speech, S1/S2 series). The `fishaudio` owner is the canonical
// publisher — scoping by owner avoids false positives from generic
// safetensors+tokenizer packaging used elsewhere.
// preferences.backend="fish-speech" overrides detection.
type FishSpeechImporter struct{}

func (i *FishSpeechImporter) Name() string      { return "fish-speech" }
func (i *FishSpeechImporter) Modality() string  { return "tts" }
func (i *FishSpeechImporter) AutoDetects() bool { return true }

func (i *FishSpeechImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "fish-speech" {
		return true
	}

	if details.HuggingFace != nil && strings.EqualFold(details.HuggingFace.Author, "fishaudio") {
		return true
	}
	// URI fallback for parity with other TTS importers when HF metadata
	// fetching fails (see BarkImporter note).
	if owner, _, ok := HFOwnerRepoFromURI(details.URI); ok {
		return strings.EqualFold(owner, "fishaudio")
	}
	return false
}

func (i *FishSpeechImporter) Import(details Details) (gallery.ModelConfig, error) {
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
		Backend:             "fish-speech",
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
