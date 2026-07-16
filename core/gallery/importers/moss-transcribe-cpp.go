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

var _ Importer = &MossTranscribeCppImporter{}

// MossTranscribeCppImporter recognises moss-transcribe.cpp GGUF weights, the
// C++/ggml port of OpenMOSS MOSS-Transcribe-Diarize. The signal is narrow on
// purpose: moss-transcribe.cpp names its weights "moss-transcribe[-<quant>].gguf"
// (e.g. moss-transcribe-q5_k.gguf, moss-transcribe-q8_0.gguf), so we only match a
// .gguf whose name carries the "moss-transcribe" token. That keeps us from
// claiming arbitrary llama-style GGUFs (the importer is registered before
// llama-cpp). preferences.backend="moss-transcribe-cpp" forces the importer
// regardless.
type MossTranscribeCppImporter struct{}

func (i *MossTranscribeCppImporter) Name() string      { return "moss-transcribe-cpp" }
func (i *MossTranscribeCppImporter) Modality() string  { return "asr" }
func (i *MossTranscribeCppImporter) AutoDetects() bool { return true }

func (i *MossTranscribeCppImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "moss-transcribe-cpp" {
		return true
	}

	// Direct URL or path to a moss-transcribe GGUF.
	if isMossTranscribeGGUF(filepath.Base(details.URI)) {
		return true
	}

	// HF repo shipping at least one moss-transcribe GGUF.
	if details.HuggingFace != nil {
		for _, f := range details.HuggingFace.Files {
			if isMossTranscribeGGUF(filepath.Base(f.Path)) {
				return true
			}
		}
	}

	return false
}

func (i *MossTranscribeCppImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// MOSS quants are byte-exact against the reference even at Q5_K (and ~1/6 the
	// size), so default to q5_k, then fall back up the size ladder; the last file
	// wins if none match (mirrors parakeet-cpp / whisper).
	preferredQuants, _ := preferencesMap["quantizations"].(string)
	quants := []string{"q5_k", "q4_k", "q6_k", "q8_0", "f16"}
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
		Backend:             "moss-transcribe-cpp",
		KnownUsecaseStrings: []string{"transcript"},
	}

	uri := downloader.URI(details.URI)
	directGGUF := isMossTranscribeGGUF(filepath.Base(details.URI))
	switch {
	case uri.LooksLikeURL() && directGGUF:
		// Direct file URL (e.g. .../resolve/main/moss-transcribe-q5_k.gguf). The
		// exact file is known, no quant pick.
		fileName, err := uri.FilenameFromUrl()
		if err != nil {
			return gallery.ModelConfig{}, err
		}
		target := filepath.Join("moss-transcribe-cpp", "models", name, fileName)
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: target,
		})
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: target},
		}
	case details.HuggingFace != nil:
		// HF repo: collect every moss-transcribe GGUF, pick the preferred quant,
		// and nest under moss-transcribe-cpp/models/<name>/ so a multi-quant repo
		// doesn't collide on disk.
		var ggufFiles []hfapi.ModelFile
		for _, f := range details.HuggingFace.Files {
			if isMossTranscribeGGUF(filepath.Base(f.Path)) {
				ggufFiles = append(ggufFiles, f)
			}
		}
		if chosen, ok := pickPreferredGGMLFile(ggufFiles, quants); ok {
			target := filepath.Join("moss-transcribe-cpp", "models", name, filepath.Base(chosen.Path))
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
		// Bare URI with no HF metadata (pref-only path): point at the basename so
		// users can tweak the YAML after import.
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

// isMossTranscribeGGUF reports whether name is a moss-transcribe.cpp GGUF: a
// .gguf file whose name carries the "moss-transcribe" token (hyphen or
// underscore form). The .gguf check is case-insensitive.
func isMossTranscribeGGUF(name string) bool {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".gguf") {
		return false
	}
	for _, tok := range []string{"moss-transcribe", "moss_transcribe"} {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}
