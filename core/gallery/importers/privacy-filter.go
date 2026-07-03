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

var _ Importer = &PrivacyFilterImporter{}

// PrivacyFilterImporter recognises the OpenMed privacy-filter PII/NER model
// family, served by the standalone privacy-filter.cpp ggml engine (the
// openai-privacy-filter architecture). Detection is deliberately narrow: the
// engine can only run a privacy-filter GGUF, so we match a .gguf whose name
// carries the "privacy-filter" token (e.g. privacy-filter-multilingual-f16.gguf)
// or an HF repo that ships one. That keeps us from claiming arbitrary
// llama-style GGUFs (the importer is registered before llama-cpp) and from
// claiming the upstream OpenMed/privacy-filter-* safetensors repos, which carry
// no runnable GGUF. preferences.backend="privacy-filter" forces it regardless.
type PrivacyFilterImporter struct{}

func (i *PrivacyFilterImporter) Name() string { return "privacy-filter" }

// Modality is "text": the filter operates in the text domain and there is no
// dedicated token-classification chip in the import UI, so it groups with the
// other text-domain backends (matching how ds4 — another single-family text
// GGUF — is classified).
func (i *PrivacyFilterImporter) Modality() string  { return "text" }
func (i *PrivacyFilterImporter) AutoDetects() bool { return true }

func (i *PrivacyFilterImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "privacy-filter" {
		return true
	}

	// Direct URL or path to a privacy-filter GGUF.
	if isPrivacyFilterGGUF(filepath.Base(details.URI)) {
		return true
	}

	// HF repo shipping at least one privacy-filter GGUF.
	if details.HuggingFace != nil {
		for _, f := range details.HuggingFace.Files {
			if isPrivacyFilterGGUF(filepath.Base(f.Path)) {
				return true
			}
		}
	}

	// Fallback: hfapi recursion bug may leave HuggingFace nil — match a repo
	// that names itself as the privacy-filter GGUF distribution (both tokens
	// present), e.g. LocalAI-io/privacy-filter-multilingual-GGUF. Requiring
	// "gguf" keeps the safetensors-only source repo out.
	if _, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		lower := strings.ToLower(repo)
		if privacyFilterName(lower) && strings.Contains(lower, "gguf") {
			return true
		}
	}

	return false
}

func (i *PrivacyFilterImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// The token classifier's accuracy is parity-sensitive, so prefer the
	// highest-precision weights first (f16 is what the gallery ships today),
	// then fall back down the quant ladder; the last file wins if none match.
	preferredQuants, _ := preferencesMap["quantizations"].(string)
	quants := []string{"f16", "q8_0", "q6_k", "q5_k", "q4_k"}
	if preferredQuants != "" {
		quants = strings.Split(preferredQuants, ",")
	}

	cfg := gallery.ModelConfig{
		Name:        name,
		Description: description,
	}

	trueV := true
	modelConfig := config.ModelConfig{
		Name:        name,
		Description: description,
		Backend:     "privacy-filter",
		// embeddings:true mirrors the gallery entry — the privacy-filter
		// backend loads in embedding mode to expose per-token logits.
		Embeddings: &trueV,
		// token_classify reserves the model for the PII NER tier; another
		// model opts into redaction by listing this one under pii.detectors.
		KnownUsecaseStrings: []string{"token_classify"},
	}

	uri := downloader.URI(details.URI)
	directGGUF := isPrivacyFilterGGUF(filepath.Base(details.URI))
	switch {
	case uri.LooksLikeURL() && directGGUF:
		// Direct file URL (e.g. .../resolve/main/privacy-filter-multilingual-f16.gguf).
		// The exact file is known, no quant pick.
		fileName, err := uri.FilenameFromUrl()
		if err != nil {
			return gallery.ModelConfig{}, err
		}
		target := filepath.Join("privacy-filter", "models", name, fileName)
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: target,
		})
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: target},
		}
	case details.HuggingFace != nil:
		// HF repo: collect every privacy-filter GGUF, pick the preferred quant,
		// and nest under privacy-filter/models/<name>/ so a multi-quant repo
		// doesn't collide on disk.
		var ggufFiles []hfapi.ModelFile
		for _, f := range details.HuggingFace.Files {
			if isPrivacyFilterGGUF(filepath.Base(f.Path)) {
				ggufFiles = append(ggufFiles, f)
			}
		}
		if chosen, ok := pickPreferredGGMLFile(ggufFiles, quants); ok {
			target := filepath.Join("privacy-filter", "models", name, filepath.Base(chosen.Path))
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

// privacyFilterName reports whether a lower-cased string carries the
// privacy-filter token in either separator form.
func privacyFilterName(lower string) bool {
	return strings.Contains(lower, "privacy-filter") || strings.Contains(lower, "privacy_filter")
}

// isPrivacyFilterGGUF reports whether name is a privacy-filter GGUF: a .gguf
// file whose name carries the privacy-filter token. The .gguf check is
// case-insensitive.
func isPrivacyFilterGGUF(name string) bool {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".gguf") {
		return false
	}
	return privacyFilterName(lower)
}
