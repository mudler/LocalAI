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

var _ Importer = &TransformersImporter{}

type TransformersImporter struct{}

func (i *TransformersImporter) Match(details Details) bool {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return false
	}
	preferencesMap := make(map[string]any)
	err = json.Unmarshal(preferences, &preferencesMap)
	if err != nil {
		return false
	}

	b, ok := preferencesMap["backend"].(string)
	if ok && b == "transformers" {
		return true
	}

	if details.HuggingFace != nil {
		for _, file := range details.HuggingFace.Files {
			if strings.Contains(file.Path, "tokenizer.json") ||
				strings.Contains(file.Path, "tokenizer_config.json") {
				return true
			}
		}
	}

	return false
}

func (i *TransformersImporter) Import(details Details) (gallery.ModelConfig, error) {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	preferencesMap := make(map[string]any)
	err = json.Unmarshal(preferences, &preferencesMap)
	if err != nil {
		return gallery.ModelConfig{}, err
	}

	name, ok := preferencesMap["name"].(string)
	if !ok {
		name = filepath.Base(details.URI)
	}

	description, ok := preferencesMap["description"].(string)
	if !ok {
		description = "Imported from " + details.URI
	}

	backend := "transformers"
	b, ok := preferencesMap["backend"].(string)
	if ok {
		backend = b
	}

	modelType, ok := preferencesMap["type"].(string)
	if !ok {
		modelType = "AutoModelForCausalLM"
	}

	quantization, ok := preferencesMap["quantization"].(string)
	if !ok {
		quantization = ""
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		KnownUsecaseStrings: []string{"chat"},
		Backend:             backend,
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: details.URI,
			},
		},
		TemplateConfig: config.TemplateConfig{
			UseTokenizerTemplate: true,
		},
	}
	modelConfig.ModelType = modelType
	modelConfig.Quantization = quantization

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
