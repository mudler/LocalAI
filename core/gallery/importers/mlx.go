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

var _ Importer = &MLXImporter{}

type MLXImporter struct{}

func (i *MLXImporter) Name() string      { return "mlx" }
func (i *MLXImporter) Modality() string  { return "text" }
func (i *MLXImporter) AutoDetects() bool { return true }

func (i *MLXImporter) Match(details Details) bool {
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
	if ok && b == "mlx" || b == "mlx-vlm" {
		return true
	}

	// All https://huggingface.co/mlx-community/*
	if strings.Contains(details.URI, "mlx-community/") {
		return true
	}

	return false
}

func (i *MLXImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	// Vision-language checkpoints (e.g. gemma-4 E4B) declare the
	// "image-text-to-text" pipeline tag on HuggingFace. The text-only mlx-lm
	// tokenizer does not carry their processor chat template, so routing them
	// through the plain mlx backend yields degenerate looping output
	// (issue #10269). Send them to the mlx-vlm backend, which applies the
	// processor-aware chat template.
	backend := "mlx"
	if details.HuggingFace != nil && details.HuggingFace.PipelineTag == "image-text-to-text" {
		backend = "mlx-vlm"
	}
	// An explicit backend preference always wins.
	b, ok := preferencesMap["backend"].(string)
	if ok {
		backend = b
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		KnownUsecaseStrings: []string{config.UsecaseChat},
		Backend:             backend,
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: LocalModelPath(details.URI),
			},
		},
		TemplateConfig: config.TemplateConfig{
			UseTokenizerTemplate: true,
		},
	}

	// Apply per-model-family inference parameter defaults
	config.ApplyInferenceDefaults(&modelConfig, details.URI)

	// Auto-set tool_parser / reasoning_parser from parser_defaults.json so
	// the generated YAML mirrors what the vllm importer produces. The mlx
	// backends auto-detect parsers from the chat template at runtime and
	// ignore these Options, but surfacing them in the config keeps the two
	// paths consistent and gives users a single place to override.
	if parsers := config.MatchParserDefaults(details.URI); parsers != nil {
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
