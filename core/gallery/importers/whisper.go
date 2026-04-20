package importers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/downloader"
	"go.yaml.in/yaml/v2"
)

var _ Importer = &WhisperImporter{}

// WhisperImporter recognises whisper.cpp GGML models. The signals are narrow
// on purpose — whisper.cpp names its weights "ggml-*.bin" (e.g.
// ggml-base.en.bin) — so we match either a direct URL to such a file or an
// HF repo that ships one. preferences.backend="whisper" forces the importer
// regardless of artefacts.
type WhisperImporter struct{}

func (i *WhisperImporter) Name() string      { return "whisper" }
func (i *WhisperImporter) Modality() string  { return "asr" }
func (i *WhisperImporter) AutoDetects() bool { return true }

func (i *WhisperImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "whisper" {
		return true
	}

	// Direct URL or path ending in ggml-*.bin
	base := filepath.Base(details.URI)
	if strings.HasPrefix(base, "ggml-") && strings.HasSuffix(strings.ToLower(base), ".bin") {
		return true
	}

	if details.HuggingFace != nil && HasGGMLFile(details.HuggingFace.Files, "ggml-") {
		return true
	}

	return false
}

func (i *WhisperImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	cfg := gallery.ModelConfig{
		Name:        name,
		Description: description,
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "whisper",
		KnownUsecaseStrings: []string{"transcript"},
	}

	uri := downloader.URI(details.URI)
	switch {
	case uri.LooksLikeURL():
		fileName, err := uri.FilenameFromUrl()
		if err != nil {
			return gallery.ModelConfig{}, err
		}
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: fileName,
		})
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: fileName},
		}
	case details.HuggingFace != nil:
		for _, f := range details.HuggingFace.Files {
			base := filepath.Base(f.Path)
			if !strings.HasPrefix(base, "ggml-") {
				continue
			}
			if !strings.HasSuffix(strings.ToLower(base), ".bin") {
				continue
			}
			cfg.Files = append(cfg.Files, gallery.File{
				URI:      f.URL,
				Filename: base,
				SHA256:   f.SHA256,
			})
			modelConfig.PredictionOptions = schema.PredictionOptions{
				BasicModelRequest: schema.BasicModelRequest{Model: base},
			}
			break
		}
	default:
		// Bare URI with no HF metadata (pref-only path). Point the config at
		// the URI basename so users can tweak the YAML after import.
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: filepath.Base(details.URI)},
		}
	}

	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	cfg.ConfigFile = string(data)

	return cfg, nil
}
