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

var _ Importer = &ACEStepImporter{}

// ACEStepImporter recognises ACE-Step music generation checkpoints
// (ACE-Step/ACE-Step-v1-3.5B, ACE-Step/Ace-Step1.5, community finetunes).
// Detection matches on "ace-step" in the repo name — case-insensitive —
// so quantised mirrors still route here. The backend itself is
// sound-generation / TTS-adjacent; the Modality() method returns "image"
// purely to slot into the UI dropdown's image/video tab where it lives
// with other generative media importers. preferences.backend="ace-step"
// overrides detection.
type ACEStepImporter struct{}

func (i *ACEStepImporter) Name() string      { return "ace-step" }
func (i *ACEStepImporter) Modality() string  { return "image" }
func (i *ACEStepImporter) AutoDetects() bool { return true }

func (i *ACEStepImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "ace-step" {
		return true
	}

	if details.HuggingFace != nil {
		repoName := details.HuggingFace.ModelID
		if idx := strings.Index(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		if strings.Contains(strings.ToLower(repoName), "ace-step") {
			return true
		}
		if strings.EqualFold(details.HuggingFace.Author, "ACE-Step") {
			return true
		}
	}

	// Fallback: hfapi recursion bug may leave HuggingFace nil — decide
	// from the URI owner/repo.
	if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		if strings.EqualFold(owner, "ACE-Step") {
			return true
		}
		if strings.Contains(strings.ToLower(repo), "ace-step") {
			return true
		}
	}

	return false
}

func (i *ACEStepImporter) Import(details Details) (gallery.ModelConfig, error) {
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
	} else if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		model = owner + "/" + repo
	}

	modelConfig := config.ModelConfig{
		Name:        name,
		Description: description,
		Backend:     "ace-step",
		// Mirrors gallery/index.yaml's ace-step-turbo entry which flags
		// both sound_generation and tts — ACE-Step is a music/sound model,
		// the UI groups it under image/video simply because there is no
		// first-class music tab yet.
		KnownUsecaseStrings: []string{"sound_generation", "tts"},
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
