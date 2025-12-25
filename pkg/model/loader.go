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

	"github.com/mudler/xlog"
)

// new idea: what if we declare a struct of these here, and use a loop to check?

// TODO: Split ModelLoader and TemplateLoader? Just to keep things more organized. Left together to share a mutex until I look into that. Would split if we separate directories for .bin/.yaml and .tmpl
type ModelLoader struct {
	ModelPath                string
	mu                       sync.Mutex
	models                   map[string]*Model
	loading                  map[string]chan struct{} // tracks models currently being loaded
	wd                       *WatchDog
	externalBackends         map[string]string
	lruEvictionMaxRetries    int           // Maximum number of retries when waiting for busy models
	lruEvictionRetryInterval time.Duration // Interval between retries when waiting for busy models
}

// NewModelLoader creates a new ModelLoader instance.
// LRU eviction is now managed through the WatchDog component.
func NewModelLoader(system *system.SystemState) *ModelLoader {
	nml := &ModelLoader{
		ModelPath:                system.Model.ModelsPath,
		models:                   make(map[string]*Model),
		loading:                  make(map[string]chan struct{}),
		externalBackends:         make(map[string]string),
		lruEvictionMaxRetries:    30,              // Default: 30 retries
		lruEvictionRetryInterval: 1 * time.Second, // Default: 1 second
	}

	return nml
}

// GetLoadingCount returns the number of models currently being loaded
func (ml *ModelLoader) GetLoadingCount() int {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	return len(ml.loading)
}

func (ml *ModelLoader) SetWatchDog(wd *WatchDog) {
	ml.wd = wd
}

func (ml *ModelLoader) GetWatchDog() *WatchDog {
	return ml.wd
}

// SetLRUEvictionRetrySettings updates the LRU eviction retry settings
func (ml *ModelLoader) SetLRUEvictionRetrySettings(maxRetries int, retryInterval time.Duration) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.lruEvictionMaxRetries = maxRetries
	ml.lruEvictionRetryInterval = retryInterval
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
	ml.mu.Lock()

	// Check if we already have a loaded model
	if model := ml.checkIsLoaded(modelID); model != nil {
		ml.mu.Unlock()
		return model, nil
	}

	// Check if another goroutine is already loading this model
	if loadingChan, isLoading := ml.loading[modelID]; isLoading {
		ml.mu.Unlock()
		// Wait for the other goroutine to finish loading
		xlog.Debug("Waiting for model to be loaded by another request", "modelID", modelID)
		<-loadingChan
		// Now check if the model is loaded
		ml.mu.Lock()
		model := ml.checkIsLoaded(modelID)
		ml.mu.Unlock()
		if model != nil {
			return model, nil
		}
		// If still not loaded, the other goroutine failed - we'll try again
		return ml.LoadModel(modelID, modelName, loader)
	}

	// Mark this model as loading (create a channel that will be closed when done)
	loadingChan := make(chan struct{})
	ml.loading[modelID] = loadingChan
	ml.mu.Unlock()

	// Ensure we clean up the loading state when done
	defer func() {
		ml.mu.Lock()
		delete(ml.loading, modelID)
		close(loadingChan)
		ml.mu.Unlock()
	}()

	// Load the model (this can take a long time, no lock held)
	modelFile := filepath.Join(ml.ModelPath, modelName)
	xlog.Debug("Loading model in memory from file", "file", modelFile)

	model, err := loader(modelID, modelName, modelFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load model with internal loader: %s", err)
	}

	if model == nil {
		return nil, fmt.Errorf("loader didn't return a model")
	}

	// Add to models map
	ml.mu.Lock()
	ml.models[modelID] = model
	ml.mu.Unlock()

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
	return ml.checkIsLoaded(s)
}

func (ml *ModelLoader) checkIsLoaded(s string) *Model {
	m, ok := ml.models[s]
	if !ok {
		return nil
	}

	xlog.Debug("Model already loaded in memory", "model", s)
	client := m.GRPC(false, ml.wd)

	xlog.Debug("Checking model availability", "model", s)
	cTimeout, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	alive, err := client.HealthCheck(cTimeout)
	if !alive {
		xlog.Warn("GRPC Model not responding", "error", err)
		xlog.Warn("Deleting the process in order to recreate it")
		process := m.Process()
		if process == nil {
			xlog.Error("Process not found and the model is not responding anymore", "model", s)
			return m
		}
		if !process.IsAlive() {
			xlog.Debug("GRPC Process is not responding", "model", s)
			// stop and delete the process, this forces to re-load the model and re-create again the service
			err := ml.deleteProcess(s)
			if err != nil {
				xlog.Error("error stopping process", "error", err, "process", s)
			}
			return nil
		}
	}

	return m
}
