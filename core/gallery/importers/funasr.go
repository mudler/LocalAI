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

var _ Importer = &FunASRImporter{}

// FunASRImporter matches FunASR/FunAudioLLM checkpoints, e.g.
// FunAudioLLM/SenseVoiceSmall, FunAudioLLM/Fun-ASR-Nano-2512,
// iic/SenseVoiceSmall, funasr/paraformer-zh. Detection is scoped to
// known FunASR owners (FunAudioLLM, iic, funasr) or repos with
// "funasr" / "sensevoice" / "paraformer" in the name.
// preferences.backend=funasr forces detection.
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "funasr" {
		return true
	}

	if details.HuggingFace == nil {
		return false
	}

	author := strings.ToLower(details.HuggingFace.Author)
	repoName := details.HuggingFace.ModelID
	if idx := strings.Index(repoName, "/"); idx >= 0 {
		repoName = repoName[idx+1:]
	}
	repoLower := strings.ToLower(repoName)

	// Match known FunASR model owners
	if author == "funaudiollm" || author == "iic" || author == "funasr" {
		if strings.Contains(repoLower, "sensevoice") ||
			strings.Contains(repoLower, "paraformer") ||
			strings.Contains(repoLower, "fun-asr") ||
			strings.Contains(repoLower, "funasr") {
			return true
		}
	}

	// Match any repo with funasr/sensevoice/paraformer in name + ASR tag
	if details.HuggingFace.PipelineTag == "automatic-speech-recognition" {
		if strings.Contains(repoLower, "funasr") ||
			strings.Contains(repoLower, "sensevoice") ||
			strings.Contains(repoLower, "paraformer") {
			return true
		}
	}

	return false
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
