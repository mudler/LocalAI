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

var _ Importer = &DepthAnythingImporter{}

// DepthAnythingImporter recognises depth-anything.cpp GGUF weights, the
// C++/ggml port of ByteDance Depth Anything 3. The signal is narrow on
// purpose: depth-anything.cpp names its weights
// "depth-anything-<size>-<quant>.gguf" (e.g. depth-anything-small-f32.gguf,
// depth-anything-large-q4_k.gguf), so we only match a .gguf whose name carries
// a depth-anything token. That keeps us from claiming arbitrary llama-style
// GGUFs (the importer is registered before llama-cpp), and it deliberately
// does NOT match the upstream depth-anything/* PyTorch repos (which ship
// safetensors checkpoints, not runnable GGUFs).
// preferences.backend="depth-anything" forces the importer regardless.
type DepthAnythingImporter struct{}

func (i *DepthAnythingImporter) Name() string      { return "depth-anything" }
func (i *DepthAnythingImporter) Modality() string  { return "image" }
func (i *DepthAnythingImporter) AutoDetects() bool { return true }

func (i *DepthAnythingImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "depth-anything" {
		return true
	}

	// Direct URL or path to a depth-anything GGUF.
	if isDepthAnythingGGUF(filepath.Base(details.URI)) {
		return true
	}

	// HF repo shipping at least one depth-anything GGUF.
	if details.HuggingFace != nil {
		for _, f := range details.HuggingFace.Files {
			if isDepthAnythingGGUF(filepath.Base(f.Path)) {
				return true
			}
		}
	}

	return false
}

func (i *DepthAnythingImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// depth-anything quants stay above 0.998 correlation even at q4_k, so
	// default to the smallest, then fall back up the size ladder; the last
	// file wins if none match (mirrors whisper / llama-cpp). The ladder lists
	// both f16 and f32 since the published GGUFs ship f32 rather than f16.
	preferredQuants, _ := preferencesMap["quantizations"].(string)
	quants := []string{"q4_k", "q5_k", "q6_k", "q8_0", "f16", "f32"}
	if preferredQuants != "" {
		quants = strings.Split(preferredQuants, ",")
	}

	cfg := gallery.ModelConfig{
		Name:        name,
		Description: description,
	}

	modelConfig := config.ModelConfig{
		Name:        name,
		Description: description,
		Backend:     "depth-anything",
	}

	uri := downloader.URI(details.URI)
	directGGUF := isDepthAnythingGGUF(filepath.Base(details.URI))
	switch {
	case uri.LooksLikeURL() && directGGUF:
		// Direct file URL (e.g. .../resolve/main/depth-anything-small-f32.gguf).
		// The exact file is known, no quant pick.
		fileName, err := uri.FilenameFromUrl()
		if err != nil {
			return gallery.ModelConfig{}, err
		}
		target := filepath.Join("depth-anything", "models", name, fileName)
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: target,
		})
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: target},
		}
	case details.HuggingFace != nil:
		// HF repo: collect every depth-anything GGUF, pick the preferred quant,
		// and nest under depth-anything/models/<name>/ so a multi-quant repo
		// doesn't collide on disk.
		var ggufFiles []hfapi.ModelFile
		for _, f := range details.HuggingFace.Files {
			if isDepthAnythingGGUF(filepath.Base(f.Path)) {
				ggufFiles = append(ggufFiles, f)
			}
		}
		if chosen, ok := pickPreferredGGMLFile(ggufFiles, quants); ok {
			target := filepath.Join("depth-anything", "models", name, filepath.Base(chosen.Path))
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
		// Bare URI with no HF metadata (pref-only path): point at the basename
		// so users can tweak the YAML after import.
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

// isDepthAnythingGGUF reports whether name is a depth-anything.cpp GGUF: a
// .gguf file whose name carries a depth-anything token. The .gguf check is
// case-insensitive; the tokens cover the published naming
// (depth-anything-<size>-<quant>.gguf) and its hyphen/underscore variants.
func isDepthAnythingGGUF(name string) bool {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".gguf") {
		return false
	}
	for _, tok := range []string{"depth-anything", "depth_anything", "depthanything"} {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}
