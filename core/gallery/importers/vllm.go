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

var _ Importer = &VLLMImporter{}

type VLLMImporter struct{}

func (i *VLLMImporter) Name() string      { return "vllm" }
func (i *VLLMImporter) Modality() string  { return "text" }
func (i *VLLMImporter) AutoDetects() bool { return true }

func (i *VLLMImporter) Match(details Details) bool {
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
	if ok && b == "vllm" {
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

func (i *VLLMImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	backend := "vllm"
	b, ok := preferencesMap["backend"].(string)
	if ok {
		backend = b
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

	// Apply per-model-family inference parameter defaults
	config.ApplyInferenceDefaults(&modelConfig, details.URI)

	// Auto-detect tool_parser and reasoning_parser for known model families.
	// Surfacing them in the generated YAML lets users see and edit the choices.
	parsers := config.MatchParserDefaults(details.URI)
	if parsers != nil {
		if tp, ok := parsers["tool_parser"]; ok {
			modelConfig.Options = append(modelConfig.Options, "tool_parser:"+tp)
		}
		if rp, ok := parsers["reasoning_parser"]; ok {
			modelConfig.Options = append(modelConfig.Options, "reasoning_parser:"+rp)
		}
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
