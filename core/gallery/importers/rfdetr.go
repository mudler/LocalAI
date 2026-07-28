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

var _ Importer = &RFDetrImporter{}

// RFDetrImporter routes RF-DETR object-detection repositories to the
// "rfdetr" backend. It must be registered BEFORE TransformersImporter
// because RF-DETR checkpoints often ship tokenizer-adjacent artefacts.
//
// Detection signals:
//   - preferences.backend="rfdetr" (explicit override);
//   - repo name contains "rf-detr" or "rfdetr" (case-insensitive).
type RFDetrImporter struct{}

func (i *RFDetrImporter) Name() string      { return "rfdetr" }
func (i *RFDetrImporter) Modality() string  { return "detection" }
func (i *RFDetrImporter) AutoDetects() bool { return true }

func repoLooksLikeRFDetr(repo string) bool {
	lower := strings.ToLower(repo)
	return strings.Contains(lower, "rf-detr") || strings.Contains(lower, "rfdetr")
}

// repoHasGGUF inspects the HuggingFace file list (when available) to decide
// whether the repo ships RF-DETR weights in ggml/GGUF form — the native
// rfdetr-cpp backend's input format. Mudler's rfdetr-cpp-* repos
// (mudler/rfdetr-cpp-nano, mudler/rfdetr-cpp-base, ...) match.
func repoHasGGUF(details Details) bool {
	if details.HuggingFace == nil {
		return false
	}
	for _, f := range details.HuggingFace.Files {
		if strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			return true
		}
	}
	return false
}

func repoLooksLikeRFDetrCpp(repo string) bool {
	lower := strings.ToLower(repo)
	return strings.Contains(lower, "rfdetr-cpp") || strings.Contains(lower, "rf-detr-cpp") ||
		strings.Contains(lower, "rfdetr.cpp") || strings.Contains(lower, "rt-detr.cpp") ||
		strings.Contains(lower, "rf-detr.cpp")
}

func (i *RFDetrImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && (b == "rfdetr" || b == "rfdetr-cpp") {
		return true
	}

	if details.HuggingFace != nil {
		repoName := details.HuggingFace.ModelID
		if idx := strings.Index(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		if repoLooksLikeRFDetr(repoName) {
			return true
		}
	}

	// Fallback: hfapi recursion bug may leave HuggingFace nil — decide
	// from the URI owner/repo.
	if _, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		if repoLooksLikeRFDetr(repo) {
			return true
		}
	}

	return false
}

func (i *RFDetrImporter) Import(details Details) (gallery.ModelConfig, error) {
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
	// YAML mirrors gallery rfdetr entries.
	model := details.URI
	if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
		model = details.HuggingFace.ModelID
	} else if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		model = owner + "/" + repo
	}

	// Route GGUF-bearing repos (mudler/rfdetr-cpp-*) to the native
	// rfdetr-cpp backend; HF transformer repos keep the Python rfdetr
	// backend. Explicit preferences.backend overrides the heuristic.
	backend := "rfdetr"
	if b, ok := preferencesMap["backend"].(string); ok && b != "" {
		backend = b
	} else if repoHasGGUF(details) {
		backend = "rfdetr-cpp"
	} else if details.HuggingFace != nil {
		repoName := details.HuggingFace.ModelID
		if idx := strings.Index(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		if repoLooksLikeRFDetrCpp(repoName) {
			backend = "rfdetr-cpp"
		}
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
