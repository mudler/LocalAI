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

var _ Importer = &LocateAnythingImporter{}

// LocateAnythingImporter routes NVIDIA LocateAnything open-vocabulary
// object-detection / visual-grounding repositories to the
// "locate-anything-cpp" backend (a native C++/ggml port). It must be
// registered BEFORE the generic GGUF matchers (LlamaCPPImporter) so its
// GGUF bundles aren't swallowed by the generic .gguf-handling importer,
// and alongside RFDetrImporter since both are detection models that may
// carry tokenizer-adjacent artefacts.
//
// Detection signals:
//   - preferences.backend="locate-anything-cpp" (explicit override);
//   - repo name contains "locate-anything" or "locateanything"
//     (case-insensitive).
type LocateAnythingImporter struct{}

func (i *LocateAnythingImporter) Name() string      { return "locate-anything-cpp" }
func (i *LocateAnythingImporter) Modality() string  { return "detection" }
func (i *LocateAnythingImporter) AutoDetects() bool { return true }

func repoLooksLikeLocateAnything(repo string) bool {
	lower := strings.ToLower(repo)
	return strings.Contains(lower, "locate-anything") ||
		strings.Contains(lower, "locateanything") ||
		strings.Contains(lower, "locate-anything.cpp") ||
		strings.Contains(lower, "locate-anything-cpp")
}

func (i *LocateAnythingImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "locate-anything-cpp" {
		return true
	}

	if details.HuggingFace != nil {
		repoName := details.HuggingFace.ModelID
		if idx := strings.Index(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		if repoLooksLikeLocateAnything(repoName) {
			return true
		}
	}

	// Fallback: hfapi recursion bug may leave HuggingFace nil — decide
	// from the URI owner/repo.
	if _, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		if repoLooksLikeLocateAnything(repo) {
			return true
		}
	}

	return false
}

func (i *LocateAnythingImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// Prefer the canonical HF "owner/repo" identifier so the emitted
	// YAML mirrors gallery locate-anything entries.
	model := details.URI
	if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
		model = details.HuggingFace.ModelID
	} else if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		model = owner + "/" + repo
	}

	// Always the native C++/ggml backend; explicit preferences.backend
	// overrides the default.
	backend := "locate-anything-cpp"
	if b, ok := preferencesMap["backend"].(string); ok && b != "" {
		backend = b
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             backend,
		KnownUsecaseStrings: []string{"detection"},
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
