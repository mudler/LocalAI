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

// AdditionalBackends advertises vllm-cpp (the LocalAI-team C++ port of vLLM)
// as a preference-only drop-in: it consumes the same safetensors model dirs
// this importer detects, so selecting it swaps the emitted backend field while
// reusing the vllm Match/Import pipeline.
func (i *VLLMImporter) AdditionalBackends() []KnownBackendEntry {
	return []KnownBackendEntry{
		{Name: "vllm-cpp", Modality: "text", Description: "C++ port of vLLM by the LocalAI team (safetensors + GGUF, no Python at inference)"},
	}
}

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
	if ok && (b == "vllm" || b == "vllm-cpp") {
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

	if backend == "vllm-cpp" {
		// vllm-cpp applies the model's chat template ENGINE-side (like the
		// vllm python backend, so use_tokenizer_template carries over), but
		// tool/reasoning parsing is the engine's own autoparser pipeline -
		// the vllm-python tool_parser/reasoning_parser options don't apply.
	} else {
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
