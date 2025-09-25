package model

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/utils"

	"github.com/rs/zerolog/log"
)

// new idea: what if we declare a struct of these here, and use a loop to check?

// TODO: Split ModelLoader and TemplateLoader? Just to keep things more organized. Left together to share a mutex until I look into that. Would split if we separate directories for .bin/.yaml and .tmpl
type ModelLoader struct {
	ModelPath        string
	mu               sync.Mutex
	singletonLock    sync.Mutex
	singletonMode    bool
	models           map[string]*Model
	wd               *WatchDog
	externalBackends map[string]string
}

func NewModelLoader(system *system.SystemState, singleActiveBackend bool) *ModelLoader {
	nml := &ModelLoader{
		ModelPath:        system.Model.ModelsPath,
		models:           make(map[string]*Model),
		singletonMode:    singleActiveBackend,
		externalBackends: make(map[string]string),
	}

	return nml
}

func (ml *ModelLoader) SetWatchDog(wd *WatchDog) {
	ml.wd = wd
}

func (ml *ModelLoader) ExistsInModelPath(s string) bool {
	return utils.ExistsInPath(ml.ModelPath, s)
}

func (ml *ModelLoader) SetExternalBackend(name, uri string) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.externalBackends[name] = uri
}

func (ml *ModelLoader) DeleteExternalBackend(name string) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	delete(ml.externalBackends, name)
}

func (ml *ModelLoader) GetExternalBackend(name string) string {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	return ml.externalBackends[name]
}

func (ml *ModelLoader) GetAllExternalBackends(o *Options) map[string]string {
	backends := make(map[string]string)
	maps.Copy(backends, ml.externalBackends)
	if o != nil {
		maps.Copy(backends, o.externalBackends)
	}
	return backends
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
	".pt",
	".onnx",
	".md",
	".MD",
	".DS_Store",
	".",
	".safetensors",
	".bin",
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

func (ml *ModelLoader) ListLoadedModels() []*Model {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	models := []*Model{}
	for _, model := range ml.models {
		models = append(models, model)
	}

	return models
}

func (ml *ModelLoader) LoadModel(modelID, modelName string, loader func(string, string, string) (*Model, error)) (*Model, error) {
	// Check if we already have a loaded model
	if model := ml.CheckIsLoaded(modelID); model != nil {
		return model, nil
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.ModelPath, modelName)
	log.Debug().Msgf("Loading model in memory from file: %s", modelFile)

	ml.mu.Lock()
	defer ml.mu.Unlock()
	model, err := loader(modelID, modelName, modelFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load model with internal loader: %s", err)
	}

	if model == nil {
		return nil, fmt.Errorf("loader didn't return a model")
	}

	ml.models[modelID] = model

	return model, nil
}

func (ml *ModelLoader) ShutdownModel(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

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
		process := m.Process()
		if process == nil {
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
