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

var _ Importer = &VLLMOmniImporter{}

// VLLMOmniImporter routes Qwen "Omni" multimodal checkpoints
// (Qwen3-Omni, Qwen2.5-Omni, …) to the vllm-omni backend. It must be
// registered BEFORE VLLMImporter in the registry: Omni repos typically
// also carry tokenizer files that would otherwise be claimed by the
// plain vllm importer.
//
// Detection is intentionally narrow to avoid swallowing unrelated repos
// whose name happens to contain the four-letter token "Omni" — we either
// require the HF owner to be "Qwen", or the repo name to contain the
// "-Omni-" / "Omni-" pattern characteristic of the Qwen Omni naming
// scheme. preferences.backend="vllm-omni" always wins.
type VLLMOmniImporter struct{}

func (i *VLLMOmniImporter) Name() string      { return "vllm-omni" }
func (i *VLLMOmniImporter) Modality() string  { return "text" }
func (i *VLLMOmniImporter) AutoDetects() bool { return true }

// repoLooksLikeQwenOmni captures the Qwen3-Omni / Qwen2.5-Omni naming
// conventions without matching random repos whose name merely starts
// with "Omni".
func repoLooksLikeQwenOmni(repo string) bool {
	lower := strings.ToLower(repo)
	return strings.Contains(lower, "-omni-") || strings.HasPrefix(lower, "omni-")
}

func (i *VLLMOmniImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "vllm-omni" {
		return true
	}

	if details.HuggingFace != nil {
		repoName := details.HuggingFace.ModelID
		if idx := strings.Index(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		if strings.EqualFold(details.HuggingFace.Author, "Qwen") && strings.Contains(strings.ToLower(repoName), "omni") {
			return true
		}
		if repoLooksLikeQwenOmni(repoName) {
			return true
		}
	}

	// Fallback: hfapi recursion bug may leave HuggingFace nil — decide
	// from the URI owner/repo.
	if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		if strings.EqualFold(owner, "Qwen") && strings.Contains(strings.ToLower(repo), "omni") {
			return true
		}
		if repoLooksLikeQwenOmni(repo) {
			return true
		}
	}

	return false
}

func (i *VLLMOmniImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// Prefer the HF canonical "owner/repo" identifier for the model
	// parameter so the emitted YAML mirrors the gallery entries
	// (e.g. Qwen/Qwen3-Omni-30B-A3B-Instruct).
	model := details.URI
	if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
		model = details.HuggingFace.ModelID
	} else if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		model = owner + "/" + repo
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		KnownUsecaseStrings: []string{"chat", "multimodal"},
		Backend:             "vllm-omni",
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: model,
			},
		},
		TemplateConfig: config.TemplateConfig{
			UseTokenizerTemplate: true,
		},
	}

	// Apply per-model-family inference parameter defaults — Qwen Omni
	// checkpoints benefit from the same default set as other Qwen models.
	config.ApplyInferenceDefaults(&modelConfig, details.URI)

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
