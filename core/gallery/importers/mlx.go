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

func (i *MLXImporter) Match(uri string, request schema.ImportModelRequest) bool {
	preferences, err := request.Preferences.MarshalJSON()
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
	if strings.Contains(uri, "mlx-community/") {
		return true
	}

	return false
}

func (i *MLXImporter) Import(uri string, request schema.ImportModelRequest) (gallery.ModelConfig, error) {
	preferences, err := request.Preferences.MarshalJSON()
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
		name = filepath.Base(uri)
	}

	description, ok := preferencesMap["description"].(string)
	if !ok {
		description = "Imported from " + uri
	}

	backend := "mlx"
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
				Model: uri,
			},
		},
		TemplateConfig: config.TemplateConfig{
			UseTokenizerTemplate: true,
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
