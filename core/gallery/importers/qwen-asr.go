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

var _ Importer = &QwenASRImporter{}

// QwenASRImporter matches Qwen's dedicated ASR checkpoints, e.g.
// Qwen/Qwen3-ASR-1.7B. Detection is scoped to the Qwen owner with an "ASR"
// substring in the repo name — narrow enough to avoid other Qwen Audio
// variants that run on different backends (Qwen-Audio, Qwen2-Audio, Qwen3-
// Omni, Qwen TTS). preferences.backend=qwen-asr forces detection.
type QwenASRImporter struct{}

func (i *QwenASRImporter) Name() string      { return "qwen-asr" }
func (i *QwenASRImporter) Modality() string  { return "asr" }
func (i *QwenASRImporter) AutoDetects() bool { return true }

func (i *QwenASRImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "qwen-asr" {
		return true
	}

	if details.HuggingFace == nil {
		return false
	}
	if !strings.EqualFold(details.HuggingFace.Author, "Qwen") {
		return false
	}
	// Extract the repo-name portion so we don't accidentally match when
	// "asr" only appears as a substring in the owner field.
	repoName := details.HuggingFace.ModelID
	if idx := strings.Index(repoName, "/"); idx >= 0 {
		repoName = repoName[idx+1:]
	}
	return strings.Contains(strings.ToLower(repoName), "asr")
}

func (i *QwenASRImporter) Import(details Details) (gallery.ModelConfig, error) {
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
		Backend:             "qwen-asr",
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
