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

var _ Importer = &NeuTTSImporter{}

// NeuTTSImporter recognises Neuphonic's NeuTTS releases. Detection uses
// "neutts" (case-insensitive) substring in the repo name or the
// `neuphonic` owner — covers both the primary "neutts-air" release and
// community mirrors. preferences.backend="neutts" overrides detection.
type NeuTTSImporter struct{}

func (i *NeuTTSImporter) Name() string      { return "neutts" }
func (i *NeuTTSImporter) Modality() string  { return "tts" }
func (i *NeuTTSImporter) AutoDetects() bool { return true }

func (i *NeuTTSImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "neutts" {
		return true
	}

	if details.HuggingFace != nil {
		if strings.EqualFold(details.HuggingFace.Author, "neuphonic") {
			return true
		}
		repoName := details.HuggingFace.ModelID
		if idx := strings.Index(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		if strings.Contains(strings.ToLower(repoName), "neutts") {
			return true
		}
	}

	if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		if strings.EqualFold(owner, "neuphonic") || strings.Contains(strings.ToLower(repo), "neutts") {
			return true
		}
	}
	return false
}

func (i *NeuTTSImporter) Import(details Details) (gallery.ModelConfig, error) {
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
		Backend:             "neutts",
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
