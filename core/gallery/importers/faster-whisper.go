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

var _ Importer = &FasterWhisperImporter{}

// FasterWhisperImporter recognises CTranslate2-packaged whisper checkpoints
// (the format consumed by the faster-whisper runtime). The classic layout is
// a flat directory with model.bin + config.json and an ASR pipeline_tag.
//
// We disambiguate from vanilla OpenAI whisper repos — which would otherwise
// also hit the tokenizer.json path and get routed to transformers — by
// requiring either the Systran owner (the upstream distributor) or the
// string "faster-whisper" in the repo name. preferences.backend=
// faster-whisper overrides detection.
type FasterWhisperImporter struct{}

func (i *FasterWhisperImporter) Name() string      { return "faster-whisper" }
func (i *FasterWhisperImporter) Modality() string  { return "asr" }
func (i *FasterWhisperImporter) AutoDetects() bool { return true }

func (i *FasterWhisperImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "faster-whisper" {
		return true
	}

	if details.HuggingFace == nil {
		return false
	}

	if !HasFile(details.HuggingFace.Files, "model.bin") {
		return false
	}
	if !HasFile(details.HuggingFace.Files, "config.json") {
		return false
	}
	if details.HuggingFace.PipelineTag != "automatic-speech-recognition" {
		return false
	}

	// Narrow to the faster-whisper distribution: Systran owner OR
	// "faster-whisper" in the repo name. Without this guard, any vanilla
	// whisper repo on HF would also match the file pair and ASR tag.
	if strings.EqualFold(details.HuggingFace.Author, "Systran") {
		return true
	}
	return strings.Contains(strings.ToLower(details.HuggingFace.ModelID), "faster-whisper")
}

func (i *FasterWhisperImporter) Import(details Details) (gallery.ModelConfig, error) {
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
		Backend:             "faster-whisper",
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
