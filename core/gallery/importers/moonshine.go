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

var _ Importer = &MoonshineImporter{}

// MoonshineImporter recognises the UsefulSensors Moonshine ASR models, which
// ship as ONNX artefacts under HF repositories owned by "UsefulSensors".
// Detection combines the owner and a .onnx file presence check so we don't
// accidentally match other UsefulSensors projects that might not host ASR
// weights. preferences.backend="moonshine" overrides detection.
type MoonshineImporter struct{}

func (i *MoonshineImporter) Name() string      { return "moonshine" }
func (i *MoonshineImporter) Modality() string  { return "asr" }
func (i *MoonshineImporter) AutoDetects() bool { return true }

func (i *MoonshineImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "moonshine" {
		return true
	}

	if details.HuggingFace == nil {
		return false
	}
	if !strings.EqualFold(details.HuggingFace.Author, "UsefulSensors") {
		return false
	}
	// Accept either on-disk .onnx (the canonical Moonshine packaging) or an
	// ASR pipeline_tag on the metadata. The latter covers the transformers/
	// safetensors-only sibling repos (moonshine-tiny, moonshine-base, …)
	// that still route to the moonshine runtime.
	if HasONNX(details.HuggingFace.Files) {
		return true
	}
	return details.HuggingFace.PipelineTag == "automatic-speech-recognition"
}

func (i *MoonshineImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// Prefer the canonical HF repo path ("owner/repo") so downstream
	// runtime tooling can resolve the model regardless of how the user
	// spelled the URI (hf://, https://huggingface.co/, etc.).
	model := details.URI
	if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
		model = details.HuggingFace.ModelID
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "moonshine",
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
