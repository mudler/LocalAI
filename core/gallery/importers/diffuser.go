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

var _ Importer = &DiffuserImporter{}

type DiffuserImporter struct{}

func (i *DiffuserImporter) Match(details Details) bool {
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
	if ok && b == "diffusers" {
		return true
	}

	if details.HuggingFace != nil {
		for _, file := range details.HuggingFace.Files {
			if strings.Contains(file.Path, "model_index.json") ||
				strings.Contains(file.Path, "scheduler/scheduler_config.json") {
				return true
			}
		}
	}

	return false
}

func (i *DiffuserImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	backend := "diffusers"
	b, ok := preferencesMap["backend"].(string)
	if ok {
		backend = b
	}

	pipelineType, ok := preferencesMap["pipeline_type"].(string)
	if !ok {
		pipelineType = "StableDiffusionPipeline"
	}

	schedulerType, ok := preferencesMap["scheduler_type"].(string)
	if !ok {
		schedulerType = ""
	}

	enableParameters, ok := preferencesMap["enable_parameters"].(string)
	if !ok {
		enableParameters = "negative_prompt,num_inference_steps"
	}

	cuda := false
	if cudaVal, ok := preferencesMap["cuda"].(bool); ok {
		cuda = cudaVal
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		KnownUsecaseStrings: []string{"image"},
		Backend:             backend,
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: details.URI,
			},
		},
		Diffusers: config.Diffusers{
			PipelineType:     pipelineType,
			SchedulerType:    schedulerType,
			EnableParameters: enableParameters,
			CUDA:             cuda,
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
