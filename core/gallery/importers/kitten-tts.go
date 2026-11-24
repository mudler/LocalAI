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

var _ Importer = &KittenTTSImporter{}

// KittenTTSImporter recognises KittenML's kitten-tts releases. Detection
// uses the `KittenML` owner or a "kitten-tts" substring in the repo name
// for third-party mirrors. preferences.backend="kitten-tts" overrides.
type KittenTTSImporter struct{}

func (i *KittenTTSImporter) Name() string      { return "kitten-tts" }
func (i *KittenTTSImporter) Modality() string  { return "tts" }
func (i *KittenTTSImporter) AutoDetects() bool { return true }

func (i *KittenTTSImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "kitten-tts" {
		return true
	}

	if details.HuggingFace != nil {
		if strings.EqualFold(details.HuggingFace.Author, "KittenML") {
			return true
		}
		repoName := details.HuggingFace.ModelID
		if idx := strings.Index(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		if strings.Contains(strings.ToLower(repoName), "kitten-tts") {
			return true
		}
	}

	if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		if strings.EqualFold(owner, "KittenML") || strings.Contains(strings.ToLower(repo), "kitten-tts") {
			return true
		}
	}
	return false
}

func (i *KittenTTSImporter) Import(details Details) (gallery.ModelConfig, error) {
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
		Backend:             "kitten-tts",
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
