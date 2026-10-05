package importers

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/functions"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"go.yaml.in/yaml/v2"
)

var _ Importer = &DS4Importer{}

// DS4Importer detects antirez/ds4 weights - single-model DeepSeek V4 Flash
// inference engine. ds4 only loads the GGUFs published at
// huggingface.co/antirez/deepseek-v4-gguf; auto-detect keys on:
//
//   - the repo name itself ("antirez/deepseek-v4-gguf" anywhere in URI)
//   - the canonical filename pattern "DeepSeek-V4-Flash-*.gguf"
//
// Must register BEFORE LlamaCPPImporter - both match .gguf, but ds4 is
// more specific and first-match-wins.
type DS4Importer struct{}

func (i *DS4Importer) Name() string      { return "ds4" }
func (i *DS4Importer) Modality() string  { return "text" }
func (i *DS4Importer) AutoDetects() bool { return true }

func (i *DS4Importer) Match(details Details) bool {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return false
	}
	preferencesMap := make(map[string]any)
	if len(preferences) > 0 {
		_ = json.Unmarshal(preferences, &preferencesMap)
	}

	if b, ok := preferencesMap["backend"].(string); ok && b == "ds4" {
		return true
	}

	if strings.Contains(details.URI, "antirez/deepseek-v4-gguf") {
		return true
	}

	base := filepath.Base(details.URI)
	if strings.HasPrefix(base, "DeepSeek-V4-Flash-") && strings.HasSuffix(base, ".gguf") {
		return true
	}

	if details.HuggingFace != nil {
		for _, file := range details.HuggingFace.Files {
			fb := filepath.Base(file.Path)
			if strings.HasPrefix(fb, "DeepSeek-V4-Flash-") && strings.HasSuffix(fb, ".gguf") {
				return true
			}
		}
	}

	return false
}

func (i *DS4Importer) Import(details Details) (gallery.ModelConfig, error) {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	preferencesMap := make(map[string]any)
	if len(preferences) > 0 {
		_ = json.Unmarshal(preferences, &preferencesMap)
	}

	name, ok := preferencesMap["name"].(string)
	if !ok {
		name = filepath.Base(details.URI)
		name = strings.TrimSuffix(name, ".gguf")
	}
	description, ok := preferencesMap["description"].(string)
	if !ok {
		description = "DeepSeek V4 Flash - antirez/ds4 backend"
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		KnownUsecaseStrings: []string{config.UsecaseChat},
		Backend:             "ds4",
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: "ds4flash.gguf",
			},
		},
		TemplateConfig: config.TemplateConfig{
			UseTokenizerTemplate: true,
		},
		FunctionsConfig: functions.FunctionsConfig{
			GrammarConfig: functions.GrammarConfig{NoGrammar: true},
			// ds4 emits OpenAI-shape tool_calls in ChatDelta natively via
			// our DSML parser; the Go-side regex fallback should NOT fire.
			AutomaticToolParsingFallback: false,
		},
	}

	cfg := gallery.ModelConfig{
		Name:        name,
		Description: description,
	}

	// The file to fetch: derive from the URI. We standardize the local
	// filename to "ds4flash.gguf" to match ds4's own convention (its CLI
	// defaults to that path), so users can run the model without extra
	// config.
	selectedURI := details.URI
	uriFilename, _ := downloader.URI(details.URI).FilenameFromUrl()
	if !strings.HasSuffix(strings.ToLower(uriFilename), ".gguf") {
		if details.HuggingFace == nil {
			return gallery.ModelConfig{}, fmt.Errorf("cannot select a compatible GGUF from repository %q without HuggingFace file metadata", details.URI)
		}

		quantizations, _ := preferencesMap["quantizations"].(string)
		var compatible []hfapi.ModelFile
		for _, file := range details.HuggingFace.Files {
			base := strings.ToLower(filepath.Base(file.Path))
			if strings.HasPrefix(base, "deepseek-v4-flash-") && strings.HasSuffix(base, ".gguf") {
				compatible = append(compatible, file)
			}
		}
		if len(compatible) == 0 {
			return gallery.ModelConfig{}, fmt.Errorf("no compatible GGUF found in repository %q", details.URI)
		}

		selected := compatible[0].URL
		if quantizations != "" {
			selected = compatible[len(compatible)-1].URL
			matched := false
			for _, quantization := range strings.Split(quantizations, ",") {
				quantization = strings.ToLower(strings.TrimSpace(quantization))
				for _, file := range compatible {
					base := strings.ToLower(filepath.Base(file.Path))
					if quantTokenMatches(base, quantization) {
						selected = file.URL
						matched = true
						break
					}
				}
				if matched {
					break
				}
			}
		}
		selectedURI = selected
	}

	uri := downloader.URI(selectedURI)
	cfg.Files = append(cfg.Files, gallery.File{
		Filename: "ds4flash.gguf",
		URI:      string(uri),
	})

	out, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	cfg.ConfigFile = string(out)
	return cfg, nil
}
