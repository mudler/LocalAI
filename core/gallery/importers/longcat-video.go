package importers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"gopkg.in/yaml.v3"
)

const (
	longCatOwner      = "meituan-longcat"
	longCatBaseRepo   = "LongCat-Video"
	longCatAvatarRepo = "LongCat-Video-Avatar-1.5"
)

var _ Importer = &LongCatVideoImporter{}

// LongCatVideoImporter is deliberately owner/repository-specific. LongCat
// checkpoints also contain generic Diffusers metadata, so broad file-based
// matching would let this backend claim unrelated video pipelines.
type LongCatVideoImporter struct{}

func (i *LongCatVideoImporter) Name() string      { return "longcat-video" }
func (i *LongCatVideoImporter) Modality() string  { return "video" }
func (i *LongCatVideoImporter) AutoDetects() bool { return true }

func isLongCatRepo(owner, repo string) bool {
	repo = strings.Split(strings.Trim(repo, "/"), "/")[0]
	return strings.EqualFold(owner, longCatOwner) &&
		(strings.EqualFold(repo, longCatBaseRepo) || strings.EqualFold(repo, longCatAvatarRepo))
}

func longCatModelID(details Details) (string, bool) {
	if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
		parts := strings.SplitN(details.HuggingFace.ModelID, "/", 2)
		if len(parts) == 2 && isLongCatRepo(parts[0], parts[1]) {
			repo := strings.Split(strings.Trim(parts[1], "/"), "/")[0]
			return parts[0] + "/" + repo, true
		}
	}
	if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok && isLongCatRepo(owner, repo) {
		repo = strings.Split(strings.Trim(repo, "/"), "/")[0]
		return owner + "/" + repo, true
	}
	return LocalModelPath(details.URI), false
}

func (i *LongCatVideoImporter) Match(details Details) bool {
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
	if backend, ok := preferencesMap["backend"].(string); ok {
		return backend == i.Name()
	}

	_, matched := longCatModelID(details)
	return matched
}

func (i *LongCatVideoImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	model, canonical := longCatModelID(details)
	name, _ := preferencesMap["name"].(string)
	if name == "" {
		if canonical {
			name = strings.ToLower(filepath.Base(model))
		} else {
			name = filepath.Base(strings.TrimSuffix(model, "/"))
		}
	}

	description, _ := preferencesMap["description"].(string)
	if description == "" {
		description = "Imported from " + details.URI
	}

	options := []string{"attention_backend:sdpa"}
	inputModalities := []string{config.ModalityText, config.ModalityImage}
	if strings.EqualFold(filepath.Base(model), longCatAvatarRepo) {
		options = append(options, "use_distill:true")
		inputModalities = append(inputModalities, config.ModalityAudio)
	}

	modelConfig := config.ModelConfig{
		Name:                  name,
		Description:           description,
		Backend:               i.Name(),
		KnownUsecaseStrings:   []string{config.UsecaseVideo},
		KnownInputModalities:  inputModalities,
		KnownOutputModalities: []string{config.ModalityVideo},
		Options:               options,
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
