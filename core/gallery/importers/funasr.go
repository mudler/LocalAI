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

const (
	funASRSenseVoiceHF         = "FunAudioLLM/SenseVoiceSmall"
	funASRSenseVoiceModelScope = "iic/SenseVoiceSmall"
)

var _ Importer = &FunASRImporter{}

// FunASRImporter auto-detects only the official SenseVoiceSmall repository.
// Other FunAudioLLM and iic models require preferences.backend="funasr".
type FunASRImporter struct{}

func (i *FunASRImporter) Name() string      { return "funasr" }
func (i *FunASRImporter) Modality() string  { return "asr" }
func (i *FunASRImporter) AutoDetects() bool { return true }

func (i *FunASRImporter) Match(details Details) bool {
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

	if backend, ok := preferencesMap["backend"].(string); ok {
		return backend == "funasr"
	}

	return details.HuggingFace != nil &&
		strings.EqualFold(details.HuggingFace.ModelID, funASRSenseVoiceHF)
}

func (i *FunASRImporter) Import(details Details) (gallery.ModelConfig, error) {
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
	if strings.EqualFold(model, funASRSenseVoiceHF) {
		model = funASRSenseVoiceModelScope
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "funasr",
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
