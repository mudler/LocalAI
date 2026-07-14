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

var _ Importer = &CoquiImporter{}

// CoquiImporter recognises Coqui AI's open-weight TTS releases (XTTS-v2,
// YourTTS, the Tortoise port, etc). Detection is owner-scoped to `coqui`
// — their HF org is the authoritative publisher for models that run on
// the Coqui TTS Python runtime. preferences.backend="coqui" overrides.
type CoquiImporter struct{}

func (i *CoquiImporter) Name() string      { return "coqui" }
func (i *CoquiImporter) Modality() string  { return "tts" }
func (i *CoquiImporter) AutoDetects() bool { return true }

func (i *CoquiImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "coqui" {
		return true
	}

	if details.HuggingFace != nil && strings.EqualFold(details.HuggingFace.Author, "coqui") {
		return true
	}

	if owner, _, ok := HFOwnerRepoFromURI(details.URI); ok {
		return strings.EqualFold(owner, "coqui")
	}
	return false
}

func (i *CoquiImporter) Import(details Details) (gallery.ModelConfig, error) {
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
		Backend:             "coqui",
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
