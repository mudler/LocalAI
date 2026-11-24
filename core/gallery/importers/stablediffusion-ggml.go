package importers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"go.yaml.in/yaml/v2"
)

var _ Importer = &StableDiffusionGGMLImporter{}

// sdGGMLArchTokens enumerates the filename/repo substrings that reliably
// indicate a Stable Diffusion / FLUX GGUF checkpoint. Matching is
// case-insensitive so leejet's "stable-diffusion", city96's "FLUX.1-dev-gguf"
// and assorted SDXL/SD3 mirrors all land here instead of being stolen by
// llama-cpp (which otherwise claims every .gguf). This is a heuristic —
// GGUF metadata would be authoritative but requires a parser we do not
// ship in this package.
var sdGGMLArchTokens = []string{
	"flux",
	"sd1.5",
	"sdxl",
	"sd3",
	"stable-diffusion",
	"stable_diffusion",
}

// StableDiffusionGGMLImporter recognises GGUF-packaged Stable Diffusion /
// FLUX checkpoints (leejet/stable-diffusion.cpp outputs, city96's FLUX GGUF
// mirrors, second-state's SD 3.5 dumps, etc). It must be registered BEFORE
// LlamaCPPImporter so llama-cpp does not steal the .gguf match.
// preferences.backend="stablediffusion-ggml" overrides detection.
type StableDiffusionGGMLImporter struct{}

func (i *StableDiffusionGGMLImporter) Name() string      { return "stablediffusion-ggml" }
func (i *StableDiffusionGGMLImporter) Modality() string  { return "image" }
func (i *StableDiffusionGGMLImporter) AutoDetects() bool { return true }

// containsArchToken reports whether s (compared case-insensitively) includes
// any of the known SD/FLUX arch markers.
func containsArchToken(s string) bool {
	lower := strings.ToLower(s)
	for _, tok := range sdGGMLArchTokens {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

func (i *StableDiffusionGGMLImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "stablediffusion-ggml" {
		return true
	}

	// Raw .gguf URI with an arch token in the filename/URI.
	if strings.HasSuffix(strings.ToLower(details.URI), ".gguf") && containsArchToken(details.URI) {
		return true
	}

	// HF repo (when the API succeeded) with at least one .gguf file and
	// either a leejet owner or an arch token in the repo name.
	if details.HuggingFace != nil {
		if hasGGUF(details.HuggingFace.Files) {
			if strings.EqualFold(details.HuggingFace.Author, "leejet") {
				return true
			}
			repoName := details.HuggingFace.ModelID
			if idx := strings.Index(repoName, "/"); idx >= 0 {
				repoName = repoName[idx+1:]
			}
			if containsArchToken(repoName) {
				return true
			}
		}
	}

	// Fallback: HF details are nil because of the known hfapi tree-listing
	// bug on repos with nested paths — decide from the URI owner/repo alone.
	if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		if strings.EqualFold(owner, "leejet") || containsArchToken(repo) {
			return true
		}
	}

	return false
}

func (i *StableDiffusionGGMLImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// Default: raw .gguf URL — basename is the model name.
	model := filepath.Base(details.URI)
	cfg := gallery.ModelConfig{
		Name:        name,
		Description: description,
	}

	switch {
	case strings.HasSuffix(strings.ToLower(details.URI), ".gguf"):
		// Raw .gguf URI: mirror llama-cpp's flat layout.
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: filepath.Base(details.URI),
		})
		model = filepath.Base(details.URI)
	case details.HuggingFace != nil && hasGGUF(details.HuggingFace.Files):
		chosen := pickSDGGUF(details.HuggingFace.Files)
		if chosen != nil {
			cfg.Files = append(cfg.Files, gallery.File{
				URI:      chosen.URL,
				Filename: filepath.Base(chosen.Path),
				SHA256:   chosen.SHA256,
			})
			model = filepath.Base(chosen.Path)
		}
	default:
		// Pure preference-driven import with a bare URI — best-effort model
		// name; the operator is expected to top up parameters post-import.
		if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
			model = details.HuggingFace.ModelID
		} else {
			model = details.URI
		}
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "stablediffusion-ggml",
		KnownUsecaseStrings: []string{"FLAG_IMAGE"},
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: model},
		},
	}

	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}

	cfg.ConfigFile = string(data)
	return cfg, nil
}

// hasGGUF reports whether files contains at least one .gguf entry.
func hasGGUF(files []hfapi.ModelFile) bool {
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			return true
		}
	}
	return false
}

// pickSDGGUF selects the best .gguf file for a SD/FLUX repo. Preference
// order: Q4_K, then Q8_0, then the first .gguf in the tree. Quantisation
// naming follows leejet/stable-diffusion.cpp and city96's FLUX mirrors.
func pickSDGGUF(files []hfapi.ModelFile) *hfapi.ModelFile {
	var q4k, q8, first *hfapi.ModelFile
	for idx := range files {
		f := &files[idx]
		if !strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			continue
		}
		if first == nil {
			first = f
		}
		lower := strings.ToLower(filepath.Base(f.Path))
		if q4k == nil && strings.Contains(lower, "q4_k") {
			q4k = f
		}
		if q8 == nil && strings.Contains(lower, "q8_0") {
			q8 = f
		}
	}
	switch {
	case q4k != nil:
		return q4k
	case q8 != nil:
		return q8
	default:
		return first
	}
}
