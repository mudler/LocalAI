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

var _ Importer = &RerankersImporter{}

// RerankersImporter routes cross-encoder / reranker repositories to the
// "rerankers" backend. It must be registered BEFORE SentenceTransformers
// and Transformers because reranker repos typically ship tokenizer files
// (and sometimes modules.json) that would otherwise be claimed by those
// generic importers.
//
// Detection signals:
//   - preferences.backend="rerankers" (explicit override);
//   - HF owner == "cross-encoder" (the canonical sentence-transformers
//     cross-encoder organisation);
//   - repo name contains "reranker" (case-insensitive) — catches BAAI
//     bge-reranker variants, Alibaba-NLP/gte-reranker-*, etc.
type RerankersImporter struct{}

func (i *RerankersImporter) Name() string      { return "rerankers" }
func (i *RerankersImporter) Modality() string  { return "reranker" }
func (i *RerankersImporter) AutoDetects() bool { return true }

func repoLooksLikeReranker(repo string) bool {
	return strings.Contains(strings.ToLower(repo), "reranker")
}

func (i *RerankersImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "rerankers" {
		return true
	}

	if details.HuggingFace != nil {
		if strings.EqualFold(details.HuggingFace.Author, "cross-encoder") {
			return true
		}
		repoName := details.HuggingFace.ModelID
		if idx := strings.Index(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		if repoLooksLikeReranker(repoName) {
			return true
		}
	}

	// Fallback: hfapi recursion bug may leave HuggingFace nil — decide
	// from the URI owner/repo.
	if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		if strings.EqualFold(owner, "cross-encoder") {
			return true
		}
		if repoLooksLikeReranker(repo) {
			return true
		}
	}

	return false
}

func (i *RerankersImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// Prefer the canonical HF "owner/repo" identifier so emitted YAML
	// mirrors the gallery rerankers entries.
	model := details.URI
	if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
		model = details.HuggingFace.ModelID
	} else if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		model = owner + "/" + repo
	}

	trueV := true
	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "rerankers",
		KnownUsecaseStrings: []string{"rerank"},
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: model},
		},
	}
	// Reranking is a field of the embedded config.LLMConfig; set it after
	// the literal so the intent stays obvious.
	modelConfig.Reranking = &trueV

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
