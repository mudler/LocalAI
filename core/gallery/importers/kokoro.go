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

var _ Importer = &KokoroImporter{}

// KokoroImporter recognises hexgrad's Kokoro TTS family (Kokoro-82M,
// Kokoro-82M-v1.1-zh, …). The repo name carries "Kokoro" and the weights
// ship as a PyTorch .pth/.pt — pairing the two keeps us from claiming the
// quantised GGUF mirrors (which llama-cpp handles) or the ONNX exports
// (which the pref-only `kokoros` Rust runtime handles).
// preferences.backend="kokoro" overrides detection; preferences.backend
// ="kokoros" deliberately does *not* trigger this importer (see test).
type KokoroImporter struct{}

func (i *KokoroImporter) Name() string      { return "kokoro" }
func (i *KokoroImporter) Modality() string  { return "tts" }
func (i *KokoroImporter) AutoDetects() bool { return true }

func (i *KokoroImporter) Match(details Details) bool {
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

	// Explicit "kokoro" overrides. "kokoros" is intentionally distinct:
	// comparing against the exact string prevents pref-only kokoros
	// requests from hijacking this importer.
	if b, ok := preferencesMap["backend"].(string); ok && b == "kokoro" {
		return true
	}

	if details.HuggingFace == nil {
		return false
	}
	repoName := details.HuggingFace.ModelID
	if idx := strings.Index(repoName, "/"); idx >= 0 {
		repoName = repoName[idx+1:]
	}
	if !strings.Contains(strings.ToLower(repoName), "kokoro") {
		return false
	}
	// Require a PyTorch checkpoint to disambiguate from ONNX-only or
	// GGUF-only mirrors that route to other backends.
	return HasExtension(details.HuggingFace.Files, ".pth") || HasExtension(details.HuggingFace.Files, ".pt")
}

func (i *KokoroImporter) Import(details Details) (gallery.ModelConfig, error) {
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
		Backend:             "kokoro",
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
