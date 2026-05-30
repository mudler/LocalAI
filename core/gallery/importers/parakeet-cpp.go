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

var _ Importer = &ParakeetCppImporter{}

// ParakeetCppImporter recognises parakeet.cpp GGUF weights, the C++/ggml port
// of NVIDIA NeMo Parakeet. The signal is narrow on purpose: parakeet.cpp names
// its weights "<arch>-<size>-<quant>.gguf" (e.g. tdt_ctc-110m-f16.gguf,
// rnnt-0.6b-q4_k.gguf, realtime_eou_120m-v1-q8_0.gguf), so we only match a
// .gguf whose name carries a parakeet architecture token. That keeps us from
// claiming arbitrary llama-style GGUFs (the importer is registered before
// llama-cpp), and it deliberately does NOT match the upstream nvidia/parakeet-*
// NeMo repos (which ship .nemo checkpoints, not runnable GGUFs).
// preferences.backend="parakeet-cpp" forces the importer regardless.
type ParakeetCppImporter struct{}

func (i *ParakeetCppImporter) Name() string      { return "parakeet-cpp" }
func (i *ParakeetCppImporter) Modality() string  { return "asr" }
func (i *ParakeetCppImporter) AutoDetects() bool { return true }

func (i *ParakeetCppImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "parakeet-cpp" {
		return true
	}

	// Direct URL or path to a parakeet GGUF.
	if isParakeetGGUF(filepath.Base(details.URI)) {
		return true
	}

	// HF repo shipping at least one parakeet GGUF.
	if details.HuggingFace != nil {
		for _, f := range details.HuggingFace.Files {
			if isParakeetGGUF(filepath.Base(f.Path)) {
				return true
			}
		}
	}

	return false
}

func (i *ParakeetCppImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// parakeet quants are near-lossless even at Q4_K (WER 0.0 vs NeMo on 110m),
	// so default to the smallest, then fall back up the size ladder; the last
	// file wins if none match (mirrors whisper / llama-cpp).
	preferredQuants, _ := preferencesMap["quantizations"].(string)
	quants := []string{"q4_k", "q5_k", "q6_k", "q8_0", "f16"}
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
		Backend:             "parakeet-cpp",
		KnownUsecaseStrings: []string{"transcript"},
	}

	uri := downloader.URI(details.URI)
	directGGUF := isParakeetGGUF(filepath.Base(details.URI))
	switch {
	case uri.LooksLikeURL() && directGGUF:
		// Direct file URL (e.g. .../resolve/main/tdt_ctc-110m-f16.gguf). The
		// exact file is known, no quant pick.
		fileName, err := uri.FilenameFromUrl()
		if err != nil {
			return gallery.ModelConfig{}, err
		}
		target := filepath.Join("parakeet-cpp", "models", name, fileName)
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: target,
		})
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: target},
		}
	case details.HuggingFace != nil:
		// HF repo: collect every parakeet GGUF, pick the preferred quant, and
		// nest under parakeet-cpp/models/<name>/ so a multi-quant repo doesn't
		// collide on disk.
		var ggufFiles []hfapi.ModelFile
		for _, f := range details.HuggingFace.Files {
			if isParakeetGGUF(filepath.Base(f.Path)) {
				ggufFiles = append(ggufFiles, f)
			}
		}
		if chosen, ok := pickPreferredGGMLFile(ggufFiles, quants); ok {
			target := filepath.Join("parakeet-cpp", "models", name, filepath.Base(chosen.Path))
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

// isParakeetGGUF reports whether name is a parakeet.cpp GGUF: a .gguf file
// whose name carries a parakeet architecture token. The .gguf check is
// case-insensitive; the tokens cover the published naming
// (<arch>-<size>-<quant>.gguf) plus a generic "parakeet" fallback.
func isParakeetGGUF(name string) bool {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".gguf") {
		return false
	}
	for _, tok := range []string{"tdt_ctc", "tdt-", "tdt_", "rnnt", "ctc-", "ctc_", "realtime_eou", "parakeet"} {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}
