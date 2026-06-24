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

var _ Importer = &BarkImporter{}

// BarkImporter recognises Suno's Bark TTS models. The `suno` owner hosts a
// handful of Bark variants (bark, bark-small, bark-v2-en, …) sharing the
// "bark" prefix — narrow enough to detect without false positives from
// other suno repos. preferences.backend="bark" overrides detection.
//
// NOTE: suno/bark ships a `speaker_embeddings/v2` subdirectory that hits a
// pre-existing path-doubling bug in pkg/huggingface-api's recursive tree
// listing (item.Path already carries the parent path, but the recursion
// prepends the parent path again → 404). When ModelDetails fetching fails,
// DiscoverModelConfig leaves HuggingFace nil. To keep detection robust,
// matchURIOwnerRepo() falls back to parsing the raw URI for "suno/bark*"
// so the importer still fires end-to-end.
type BarkImporter struct{}

// matchBarkURI tolerates a nil ModelDetails (see note above) by extracting
// the HF owner+repo portion directly from the raw URI.
func matchBarkURI(uri string) bool {
	owner, repo, ok := HFOwnerRepoFromURI(uri)
	if !ok {
		return false
	}
	return strings.EqualFold(owner, "suno") && strings.HasPrefix(strings.ToLower(repo), "bark")
}

func (i *BarkImporter) Name() string      { return "bark" }
func (i *BarkImporter) Modality() string  { return "tts" }
func (i *BarkImporter) AutoDetects() bool { return true }

func (i *BarkImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "bark" {
		return true
	}

	if details.HuggingFace != nil {
		if strings.EqualFold(details.HuggingFace.Author, "suno") {
			repoName := details.HuggingFace.ModelID
			if idx := strings.Index(repoName, "/"); idx >= 0 {
				repoName = repoName[idx+1:]
			}
			if strings.HasPrefix(strings.ToLower(repoName), "bark") {
				return true
			}
		}
	}

	// HF metadata may be absent when the recursive tree listing errors
	// (see type-level note). Fall back to URI parsing.
	return matchBarkURI(details.URI)
}

func (i *BarkImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	model := details.URI
	if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
		model = details.HuggingFace.ModelID
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "bark",
		KnownUsecaseStrings: []string{"tts"},
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
