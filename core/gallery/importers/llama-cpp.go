package importers

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	"go.yaml.in/yaml/v2"
)

var _ Importer = &LlamaCPPImporter{}

type LlamaCPPImporter struct{}

func (i *LlamaCPPImporter) Match(details Details) bool {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return false
	}
	preferencesMap := make(map[string]any)
	err = json.Unmarshal(preferences, &preferencesMap)
	if err != nil {
		return false
	}

	if preferencesMap["backend"] == "llama-cpp" {
		return true
	}

	if strings.HasSuffix(details.URI, ".gguf") {
		return true
	}

	if details.HuggingFace != nil {
		for _, file := range details.HuggingFace.Files {
			if strings.HasSuffix(file.Path, ".gguf") {
				return true
			}
		}
	}

	return false
}

func (i *LlamaCPPImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	preferedQuantizations, _ := preferencesMap["quantizations"].(string)
	quants := []string{"q4_k_m"}
	if preferedQuantizations != "" {
		quants = strings.Split(preferedQuantizations, ",")
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		KnownUsecaseStrings: []string{"chat"},
		Backend:             "llama-cpp",
		TemplateConfig: config.TemplateConfig{
			UseTokenizerTemplate: true,
		},
		FunctionsConfig: functions.FunctionsConfig{
			GrammarConfig: functions.GrammarConfig{
				NoGrammar: true,
			},
		},
	}

	cfg := gallery.ModelConfig{
		Name:        name,
		Description: description,
	}

	if strings.Contains(details.URI, ".gguf") {
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: filepath.Base(details.URI),
		})
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: filepath.Base(details.URI),
			},
		}
	} else if details.HuggingFace != nil {
		for _, file := range details.HuggingFace.Files {
			if slices.ContainsFunc(quants, func(quant string) bool {
				return strings.Contains(strings.ToLower(file.Path), strings.ToLower(quant))
			}) {
				cfg.Files = append(cfg.Files, gallery.File{
					URI:      file.URL,
					Filename: filepath.Base(file.Path),
					SHA256:   file.SHA256,
				})
			}
		}
		if len(modelConfig.DownloadFiles) > 0 {
			modelConfig.PredictionOptions = schema.PredictionOptions{
				BasicModelRequest: schema.BasicModelRequest{
					Model: modelConfig.DownloadFiles[0].Filename,
				},
			}
		}
	}

	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}

	cfg.ConfigFile = string(data)

	return cfg, nil
}
