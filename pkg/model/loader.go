package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/templates"

	"github.com/mudler/LocalAI/pkg/utils"

	process "github.com/mudler/go-processmanager"
	"github.com/rs/zerolog/log"
)

// new idea: what if we declare a struct of these here, and use a loop to check?

// TODO: Split ModelLoader and TemplateLoader? Just to keep things more organized. Left together to share a mutex until I look into that. Would split if we seperate directories for .bin/.yaml and .tmpl
type ModelLoader struct {
	ModelPath     string
	mu            sync.Mutex
	models        map[string]*Model
	grpcProcesses map[string]*process.Process
	templates     *templates.TemplateCache
	wd            *WatchDog
}

func NewModelLoader(modelPath string) *ModelLoader {
	nml := &ModelLoader{
		ModelPath:     modelPath,
		models:        make(map[string]*Model),
		templates:     templates.NewTemplateCache(modelPath),
		grpcProcesses: make(map[string]*process.Process),
	}

	return nml
}

func (ml *ModelLoader) SetWatchDog(wd *WatchDog) {
	ml.wd = wd
}

func (ml *ModelLoader) ExistsInModelPath(s string) bool {
	return utils.ExistsInPath(ml.ModelPath, s)
}

var knownFilesToSkip []string = []string{
	"MODEL_CARD",
	"README",
	"README.md",
}

var knownModelsNameSuffixToSkip []string = []string{
	".tmpl",
	".keep",
	".yaml",
	".yml",
	".json",
	".txt",
	".md",
	".MD",
	".DS_Store",
	".",
	".partial",
	".tar.gz",
}

const retryTimeout = time.Duration(2 * time.Minute)

func (ml *ModelLoader) ListFilesInModelPath() ([]string, error) {
	files, err := os.ReadDir(ml.ModelPath)
	if err != nil {
		return []string{}, err
	}

	models := []string{}
FILE:
	for _, file := range files {

		for _, skip := range knownFilesToSkip {
			if strings.EqualFold(file.Name(), skip) {
				continue FILE
			}
		}

		// Skip templates, YAML, .keep, .json, and .DS_Store files
		for _, skip := range knownModelsNameSuffixToSkip {
			if strings.HasSuffix(file.Name(), skip) {
				continue FILE
			}
		}

		// Skip directories
		if file.IsDir() {
			continue
		}

		models = append(models, file.Name())
	}

	return models, nil
}

func (ml *ModelLoader) ListModels() []*Model {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	models := []*Model{}
	for _, model := range ml.models {
		models = append(models, model)
	}

	return models
}

func (ml *ModelLoader) LoadModel(modelName string, loader func(string, string) (*Model, error)) (*Model, error) {
	// Check if we already have a loaded model
	if model := ml.CheckIsLoaded(modelName); model != nil {
		return model, nil
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.ModelPath, modelName)
	log.Debug().Msgf("Loading model in memory from file: %s", modelFile)

	model, err := loader(modelName, modelFile)
	if err != nil {
		return nil, err
	}

	if model == nil {
		return nil, fmt.Errorf("loader didn't return a model")
	}

	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.models[modelName] = model

	return model, nil
}

func (ml *ModelLoader) ShutdownModel(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	_, ok := ml.models[modelName]
	if !ok {
		return fmt.Errorf("model %s not found", modelName)
	}

	retries := 1
	for ml.models[modelName].GRPC(false, ml.wd).IsBusy() {
		log.Debug().Msgf("%s busy. Waiting.", modelName)
		dur := time.Duration(retries*2) * time.Second
		if dur > retryTimeout {
			dur = retryTimeout
		}
		time.Sleep(dur)
		retries++
	}

	return ml.deleteProcess(modelName)
}

func (ml *ModelLoader) CheckIsLoaded(s string) *Model {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	m, ok := ml.models[s]
	if !ok {
		return nil
	}

	log.Debug().Msgf("Model already loaded in memory: %s", s)
	client := m.GRPC(false, ml.wd)

	log.Debug().Msgf("Checking model availability (%s)", s)
	cTimeout, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	alive, err := client.HealthCheck(cTimeout)
	if !alive {
		log.Warn().Msgf("GRPC Model not responding: %s", err.Error())
		log.Warn().Msgf("Deleting the process in order to recreate it")
		process, exists := ml.grpcProcesses[s]
		if !exists {
			log.Error().Msgf("Process not found for '%s' and the model is not responding anymore !", s)
			return m
		}
		if !process.IsAlive() {
			log.Debug().Msgf("GRPC Process is not responding: %s", s)
			// stop and delete the process, this forces to re-load the model and re-create again the service
			err := ml.deleteProcess(s)
			if err != nil {
				log.Error().Err(err).Str("process", s).Msg("error stopping process")
			}
			return nil
		}
	}

	return m
}
