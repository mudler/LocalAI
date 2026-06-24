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

var _ Importer = &PiperImporter{}

// PiperImporter recognises Piper TTS voices. Piper ships each voice as a pair
// of "<voice>.onnx" + "<voice>.onnx.json" files (e.g.
// en_US-amy-medium.onnx + en_US-amy-medium.onnx.json) — the JSON sidecar is
// what disambiguates these from generic ONNX exports used by other backends
// (Moonshine, sentence-transformers, etc). preferences.backend="piper"
// overrides detection.
type PiperImporter struct{}

func (i *PiperImporter) Name() string      { return "piper" }
func (i *PiperImporter) Modality() string  { return "tts" }
func (i *PiperImporter) AutoDetects() bool { return true }

func (i *PiperImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "piper" {
		return true
	}

	if details.HuggingFace == nil {
		return false
	}
	return HasONNXConfigPair(details.HuggingFace.Files)
}

func (i *PiperImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// Default to the HF repo path so users can resolve the voice at runtime.
	// If the repo ships onnx pairs, surface the first voice file name so the
	// config is ready-to-run for single-voice repositories.
	model := details.URI
	if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
		model = details.HuggingFace.ModelID
	}
	if details.HuggingFace != nil {
		for _, f := range details.HuggingFace.Files {
			base := filepath.Base(f.Path)
			if strings.HasSuffix(strings.ToLower(base), ".onnx") {
				model = base
				break
			}
		}
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "piper",
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
