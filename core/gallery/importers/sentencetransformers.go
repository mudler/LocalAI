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

var _ Importer = &SentenceTransformersImporter{}

// SentenceTransformersImporter routes sentence-transformers embedding
// repositories to the "sentencetransformers" backend. It MUST be
// registered BEFORE TransformersImporter — ST repos ship tokenizer.json
// which would otherwise be claimed by the transformers importer.
//
// Detection signals:
//   - preferences.backend="sentencetransformers" (explicit override);
//   - repo ships "modules.json" (the ST pipeline manifest);
//   - repo ships "sentence_bert_config.json" (legacy ST marker);
//   - HF owner == "sentence-transformers".
type SentenceTransformersImporter struct{}

func (i *SentenceTransformersImporter) Name() string      { return "sentencetransformers" }
func (i *SentenceTransformersImporter) Modality() string  { return "embeddings" }
func (i *SentenceTransformersImporter) AutoDetects() bool { return true }

func (i *SentenceTransformersImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "sentencetransformers" {
		return true
	}

	if details.HuggingFace != nil {
		if HasFile(details.HuggingFace.Files, "modules.json") {
			return true
		}
		if HasFile(details.HuggingFace.Files, "sentence_bert_config.json") {
			return true
		}
		if strings.EqualFold(details.HuggingFace.Author, "sentence-transformers") {
			return true
		}
	}

	// Fallback: hfapi recursion bug may leave HuggingFace nil — decide
	// from the URI owner.
	if owner, _, ok := HFOwnerRepoFromURI(details.URI); ok {
		if strings.EqualFold(owner, "sentence-transformers") {
			return true
		}
	}

	return false
}

func (i *SentenceTransformersImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// Prefer the canonical HF "owner/repo" identifier so the emitted YAML
	// mirrors the gallery sentencetransformers entries.
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
		Backend:             "sentencetransformers",
		KnownUsecaseStrings: []string{"embeddings"},
		Embeddings:          &trueV,
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
