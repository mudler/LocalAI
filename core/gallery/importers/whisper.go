package importers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/downloader"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
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
	if isGGMLFilename(filepath.Base(details.URI)) {
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

	preferredQuants, _ := preferencesMap["quantizations"].(string)
	quants := []string{"q5_0"}
	if preferredQuants != "" {
		quants = strings.Split(preferredQuants, ",")
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
	directGGML := isGGMLFilename(filepath.Base(details.URI))
	switch {
	case uri.LooksLikeURL() && directGGML:
		// Direct file URL (e.g. .../resolve/main/ggml-base.en.bin). We
		// already know the exact file the user wants — no quant pick.
		fileName, err := uri.FilenameFromUrl()
		if err != nil {
			return gallery.ModelConfig{}, err
		}
		target := filepath.Join("whisper", "models", name, fileName)
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: target,
		})
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: target},
		}
	case details.HuggingFace != nil:
		// HF repo: collect every ggml-*.bin, pick the preferred quant
		// (default q5_0), nest under whisper/models/<name>/ so the same
		// repo can ship multiple quants without colliding on disk.
		var ggmlFiles []hfapi.ModelFile
		for _, f := range details.HuggingFace.Files {
			if isGGMLFilename(filepath.Base(f.Path)) {
				ggmlFiles = append(ggmlFiles, f)
			}
		}
		if chosen, ok := pickPreferredGGMLFile(ggmlFiles, quants); ok {
			target := filepath.Join("whisper", "models", name, filepath.Base(chosen.Path))
			cfg.Files = append(cfg.Files, gallery.File{
				URI:      chosen.URL,
				Filename: target,
				SHA256:   chosen.SHA256,
			})
			modelConfig.PredictionOptions = schema.PredictionOptions{
				BasicModelRequest: schema.BasicModelRequest{Model: target},
			}
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

// isGGMLFilename returns true when name follows whisper.cpp's "ggml-*.bin"
// packaging convention. The .bin check is case-insensitive; the ggml- prefix
// is exact.
func isGGMLFilename(name string) bool {
	return strings.HasPrefix(name, "ggml-") && strings.HasSuffix(strings.ToLower(name), ".bin")
}

// pickPreferredGGMLFile walks prefs in order and returns the first ggml file
// whose basename contains any preference token (case-insensitive match on the
// quant suffix, e.g. "q5_0"). When no preference matches, falls back to the
// last file — mirroring llama-cpp's pickPreferredGroup behaviour so a missing
// quant still yields *something* the user can run.
func pickPreferredGGMLFile(files []hfapi.ModelFile, prefs []string) (hfapi.ModelFile, bool) {
	if len(files) == 0 {
		return hfapi.ModelFile{}, false
	}
	for _, pref := range prefs {
		lower := strings.ToLower(pref)
		for _, f := range files {
			if strings.Contains(strings.ToLower(filepath.Base(f.Path)), lower) {
				return f, true
			}
		}
	}
	return files[len(files)-1], true
}
