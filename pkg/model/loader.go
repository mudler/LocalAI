package model

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/rs/zerolog/log"

	gpt2 "github.com/go-skynet/go-gpt2.cpp"
	gptj "github.com/go-skynet/go-gpt4all-j.cpp"
	llama "github.com/go-skynet/go-llama.cpp"
)

type ModelInfo struct {
	Model       interface{}
	LoadedAt    time.Time
	LastUsedAt  time.Time
	ModelType   string // "llama", "gptj", "gpt2", "stablelm"
}

type ModelLoader struct {
	modelPath string
	mu        sync.Mutex

	models            map[string]*ModelInfo
	gpt2models        map[string]*gpt2.GPT2
	gptmodels         map[string]*gptj.GPTJ
	gptstablelmmodels map[string]*gpt2.StableLM

	promptsTemplates map[string]*template.Template

	// Memory management
	memoryThresholdMB int
	autoFitEnabled    bool
	lastUsedMutex     sync.Mutex
}

func NewModelLoader(modelPath string) *ModelLoader {
	return &ModelLoader{
		modelPath:         modelPath,
		gpt2models:        make(map[string]*gpt2.GPT2),
		gptmodels:         make(map[string]*gptj.GPTJ),
		gptstablelmmodels: make(map[string]*gpt2.StableLM),
		models:            make(map[string]*ModelInfo),
		promptsTemplates:  make(map[string]*template.Template),
		memoryThresholdMB: 0, // 0 means no threshold
		autoFitEnabled:    false,
	}
}

// SetMemoryThreshold sets the memory threshold in MB for auto-fit
func (ml *ModelLoader) SetMemoryThreshold(mb int) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.memoryThresholdMB = mb
}

// SetAutoFit enables or disables auto-fit functionality
func (ml *ModelLoader) SetAutoFit(enabled bool) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.autoFitEnabled = enabled
}

// ExistsInModelPath checks if a file exists in the model path
func (ml *ModelLoader) ExistsInModelPath(s string) bool {
	_, err := os.Stat(filepath.Join(ml.modelPath, s))
	return err == nil
}

// ListModels returns all model files in the model path
func (ml *ModelLoader) ListModels() ([]string, error) {
	files, err := ioutil.ReadDir(ml.modelPath)
	if err != nil {
		return []string{}, err
	}

	models := []string{}
	for _, file := range files {
		// Skip template, YAML and .keep files
		if strings.HasSuffix(file.Name(), ".tmpl") || strings.HasSuffix(file.Name(), ".keep") || strings.HasSuffix(file.Name(), ".yaml") || strings.HasSuffix(file.Name(), ".yml") {
			continue
		}

		models = append(models, file.Name())
	}

	return models, nil
}

// TemplatePrefix returns the template prefix for a model
func (ml *ModelLoader) TemplatePrefix(modelName string, in interface{}) (string, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	m, ok := ml.promptsTemplates[modelName]
	if !ok {
		return "", fmt.Errorf("no prompt template available")
	}

	var buf bytes.Buffer

	if err := m.Execute(&buf, in); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (ml *ModelLoader) loadTemplateIfExists(modelName, modelFile string) error {
	// Check if the template was already loaded
	if _, ok := ml.promptsTemplates[modelName]; ok {
		return nil
	}

	// Check if the model path exists
	// skip any error here - we run anyway if a template is not exist
	modelTemplateFile := fmt.Sprintf("%s.tmpl", modelName)

	if !ml.ExistsInModelPath(modelTemplateFile) {
		return nil
	}

	dat, err := os.ReadFile(filepath.Join(ml.modelPath, modelTemplateFile))
	if err != nil {
		return err
	}

	// Parse the template
	tmpl, err := template.New("prompt").Parse(string(dat))
	if err != nil {
		return err
	}
	ml.promptsTemplates[modelName] = tmpl

	return nil
}

// getEstimatedModelSizeMB estimates the memory usage of a model file in MB
func (ml *ModelLoader) getEstimatedModelSizeMB(modelFile string) int {
	fileInfo, err := os.Stat(filepath.Join(ml.modelPath, modelFile))
	if err != nil {
		return 0
	}
	// Estimate: model size in MB (rough approximation)
	return int(fileInfo.Size() / (1024 * 1024))
}

// getCurrentMemoryUsageMB returns current estimated memory usage in MB
func (ml *ModelLoader) getCurrentMemoryUsageMB() int {
	total := 0
	ml.mu.Lock()
	defer ml.mu.Unlock()
	
	for modelName := range ml.models {
		total += ml.getEstimatedModelSizeMB(modelName)
	}
	for modelName := range ml.gptmodels {
		total += ml.getEstimatedModelSizeMB(modelName)
	}
	for modelName := range ml.gpt2models {
		total += ml.getEstimatedModelSizeMB(modelName)
	}
	for modelName := range ml.gptstablelmmodels {
		total += ml.getEstimatedModelSizeMB(modelName)
	}
	
	return total
}

// unloadLeastRecentlyUsed unloads the model that was least recently used
func (ml *ModelLoader) unloadLeastRecentlyUsed() bool {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	var oldestTime time.Time
	var oldestModel string
	var oldestType string

	// Check LLaMA models
	for name, info := range ml.models {
		if oldestTime.IsZero() || info.LastUsedAt.Before(oldestTime) {
			oldestTime = info.LastUsedAt
			oldestModel = name
			oldestType = "llama"
		}
	}

	// Check GPTJ models
	for name, info := range ml.gptmodels {
		if oldestTime.IsZero() || info.LastUsedAt.Before(oldestTime) {
			oldestTime = info.LastUsedAt
			oldestModel = name
			oldestType = "gptj"
		}
	}

	// Check GPT2 models
	for name, info := range ml.gpt2models {
		if oldestTime.IsZero() || info.LastUsedAt.Before(oldestTime) {
			oldestTime = info.LastUsedAt
			oldestModel = name
			oldestType = "gpt2"
		}
	}

	// Check StableLM models
	for name, info := range ml.gptstablelmmodels {
		if oldestTime.IsZero() || info.LastUsedAt.Before(oldestTime) {
			oldestTime = info.LastUsedAt
			oldestModel = name
			oldestType = "stablelm"
		}
	}

	if oldestModel == "" {
		return false
	}

	log.Info().Msgf("Auto-fit: Unloading least recently used model: %s (type: %s)", oldestModel, oldestType)

	switch oldestType {
	case "llama":
		ml.UnloadModelLLaMA(oldestModel)
	case "gptj":
		ml.UnloadModelGPTJ(oldestModel)
	case "gpt2":
		ml.UnloadModelGPT2(oldestModel)
	case "stablelm":
		ml.UnloadModelStableLM(oldestModel)
	}

	return true
}

// checkAndFreezeMemory checks if we're over the threshold and unloads models if needed
func (ml *ModelLoader) checkAndFreezeMemory() {
	if !ml.autoFitEnabled || ml.memoryThresholdMB <= 0 {
		return
	}

	currentUsage := ml.getCurrentMemoryUsageMB()
	if currentUsage > ml.memoryThresholdMB {
		log.Info().Msgf("Memory threshold exceeded: %dMB > %dMB", currentUsage, ml.memoryThresholdMB)
		
		// Keep unloading until we're under threshold
		for ml.getCurrentMemoryUsageMB() > ml.memoryThresholdMB {
			if !ml.unloadLeastRecentlyUsed() {
				log.Warn().Msg("Could not unload any more models to meet memory threshold")
				break
			}
		}
	}
}

func (ml *ModelLoader) LoadStableLMModel(modelName string) (*gpt2.StableLM, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Check if we already have a loaded model
	if !ml.ExistsInModelPath(modelName) {
		return nil, fmt.Errorf("model does not exist")
	}

	if m, ok := ml.gptstablelmmodels[modelName]; ok {
		log.Debug().Msgf("Model already loaded in memory: %s", modelName)
		// Update last used time
		if info, ok := ml.models[modelName]; ok {
			info.LastUsedAt = time.Now()
		}
		return m, nil
	}

	// Check memory threshold before loading
	ml.checkAndFreezeMemory()

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.modelPath, modelName)
	log.Debug().Msgf("Loading model in memory from file: %s", modelFile)

	model, err := gpt2.NewStableLM(modelFile)
	if err != nil {
		return nil, err
	}

	// If there is a prompt template, load it
	if err := ml.loadTemplateIfExists(modelName, modelFile); err != nil {
		return nil, err
	}

	ml.gptstablelmmodels[modelName] = model
	ml.models[modelName] = &ModelInfo{
		Model:      model,
		LoadedAt:   time.Now(),
		LastUsedAt: time.Now(),
		ModelType:  "stablelm",
	}

	return model, err
}

func (ml *ModelLoader) LoadGPT2Model(modelName string) (*gpt2.GPT2, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Check if we already have a loaded model
	if !ml.ExistsInModelPath(modelName) {
		return nil, fmt.Errorf("model does not exist")
	}

	if m, ok := ml.gpt2models[modelName]; ok {
		log.Debug().Msgf("Model already loaded in memory: %s", modelName)
		// Update last used time
		if info, ok := ml.models[modelName]; ok {
			info.LastUsedAt = time.Now()
		}
		return m, nil
	}

	// Check memory threshold before loading
	ml.checkAndFreezeMemory()

	// TODO: This needs refactoring, it's really bad to have it in here
	// Check if we have a GPTStable model loaded instead - if we do we return an error so the API tries with StableLM
	if _, ok := ml.gptstablelmmodels[modelName]; ok {
		log.Debug().Msgf("Model is GPTStableLM: %s", modelName)
		return nil, fmt.Errorf("this model is a GPTStableLM one")
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.modelPath, modelName)
	log.Debug().Msgf("Loading model in memory from file: %s", modelFile)

	model, err := gpt2.New(modelFile)
	if err != nil {
		return nil, err
	}

	// If there is a prompt template, load it
	if err := ml.loadTemplateIfExists(modelName, modelFile); err != nil {
		return nil, err
	}

	ml.gpt2models[modelName] = model
	ml.models[modelName] = &ModelInfo{
		Model:      model,
		LoadedAt:   time.Now(),
		LastUsedAt: time.Now(),
		ModelType:  "gpt2",
	}

	return model, err
}

func (ml *ModelLoader) LoadGPTJModel(modelName string) (*gptj.GPTJ, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Check if we already have a loaded model
	if !ml.ExistsInModelPath(modelName) {
		return nil, fmt.Errorf("model does not exist")
	}

	if m, ok := ml.gptmodels[modelName]; ok {
		log.Debug().Msgf("Model already loaded in memory: %s", modelName)
		// Update last used time
		if info, ok := ml.models[modelName]; ok {
			info.LastUsedAt = time.Now()
		}
		return m, nil
	}

	// Check memory threshold before loading
	ml.checkAndFreezeMemory()

	// TODO: This needs refactoring, it's really bad to have it in here
	// Check if we have a GPT2 model loaded instead - if we do we return an error so the API tries with GPT2
	if _, ok := ml.gpt2models[modelName]; ok {
		log.Debug().Msgf("Model is GPT2: %s", modelName)
		return nil, fmt.Errorf("this model is a GPT2 one")
	}
	if _, ok := ml.gptstablelmmodels[modelName]; ok {
		log.Debug().Msgf("Model is GPTStableLM: %s", modelName)
		return nil, fmt.Errorf("this model is a GPTStableLM one")
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.modelPath, modelName)
	log.Debug().Msgf("Loading model in memory from file: %s", modelFile)

	model, err := gptj.New(modelFile)
	if err != nil {
		return nil, err
	}

	// If there is a prompt template, load it
	if err := ml.loadTemplateIfExists(modelName, modelFile); err != nil {
		return nil, err
	}

	ml.gptmodels[modelName] = model
	ml.models[modelName] = &ModelInfo{
		Model:      model,
		LoadedAt:   time.Now(),
		LastUsedAt: time.Now(),
		ModelType:  "gptj",
	}

	return model, err
}

func (ml *ModelLoader) LoadLLaMAModel(modelName string, opts ...llama.ModelOption) (*llama.LLama, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	log.Debug().Msgf("Loading model name: %s", modelName)

	// Check if we already have a loaded model
	if !ml.ExistsInModelPath(modelName) {
		return nil, fmt.Errorf("model does not exist")
	}

	if m, ok := ml.models[modelName]; ok && m.ModelType == "llama" {
		log.Debug().Msgf("Model already loaded in memory: %s", modelName)
		// Update last used time
		m.LastUsedAt = time.Now()
		return m.Model.(*llama.LLama), nil
	}

	// Check memory threshold before loading
	ml.checkAndFreezeMemory()

	// TODO: This needs refactoring, it's really bad to have it in here
	// Check if we have a GPTJ model loaded instead - if we do we return an error so the API tries with GPTJ
	if _, ok := ml.gptmodels[modelName]; ok {
		log.Debug().Msgf("Model is GPTJ: %s", modelName)
		return nil, fmt.Errorf("this model is a GPTJ one")
	}
	if _, ok := ml.gpt2models[modelName]; ok {
		log.Debug().Msgf("Model is GPT2: %s", modelName)
		return nil, fmt.Errorf("this model is a GPT2 one")
	}
	if _, ok := ml.gptstablelmmodels[modelName]; ok {
		log.Debug().Msgf("Model is GPTStableLM: %s", modelName)
		return nil, fmt.Errorf("this model is a GPTStableLM one")
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.modelPath, modelName)
	log.Debug().Msgf("Loading model in memory from file: %s", modelFile)

	model, err := llama.New(modelFile, opts...)
	if err != nil {
		return nil, err
	}

	// If there is a prompt template, load it
	if err := ml.loadTemplateIfExists(modelName, modelFile); err != nil {
		return nil, err
	}

	ml.models[modelName] = &ModelInfo{
		Model:      model,
		LoadedAt:   time.Now(),
		LastUsedAt: time.Now(),
		ModelType:  "llama",
	}

	return model, err
}

// UnloadModelLLaMA unloads a LLaMA model from memory
func (ml *ModelLoader) UnloadModelLLaMA(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if _, ok := ml.models[modelName]; !ok {
		return fmt.Errorf("model %s is not loaded", modelName)
	}

	delete(ml.models, modelName)
	log.Info().Msgf("Unloaded LLaMA model: %s", modelName)
	return nil
}

// UnloadModelGPTJ unloads a GPTJ model from memory
func (ml *ModelLoader) UnloadModelGPTJ(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if _, ok := ml.gptmodels[modelName]; !ok {
		return fmt.Errorf("model %s is not loaded", modelName)
	}

	delete(ml.gptmodels, modelName)
	delete(ml.models, modelName)
	log.Info().Msgf("Unloaded GPTJ model: %s", modelName)
	return nil
}

// UnloadModelGPT2 unloads a GPT2 model from memory
func (ml *ModelLoader) UnloadModelGPT2(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if _, ok := ml.gpt2models[modelName]; !ok {
		return fmt.Errorf("model %s is not loaded", modelName)
	}

	delete(ml.gpt2models, modelName)
	delete(ml.models, modelName)
	log.Info().Msgf("Unloaded GPT2 model: %s", modelName)
	return nil
}

// UnloadModelStableLM unloads a StableLM model from memory
func (ml *ModelLoader) UnloadModelStableLM(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if _, ok := ml.gptstablelmmodels[modelName]; !ok {
		return fmt.Errorf("model %s is not loaded", modelName)
	}

	delete(ml.gptstablelmmodels, modelName)
	delete(ml.models, modelName)
	log.Info().Msgf("Unloaded StableLM model: %s", modelName)
	return nil
}

// UnloadModel unloads any loaded model by name
func (ml *ModelLoader) UnloadModel(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if info, ok := ml.models[modelName]; ok {
		switch info.ModelType {
		case "llama":
			delete(ml.models, modelName)
		case "gptj":
			delete(ml.gptmodels, modelName)
			delete(ml.models, modelName)
		case "gpt2":
			delete(ml.gpt2models, modelName)
			delete(ml.models, modelName)
		case "stablelm":
			delete(ml.gptstablelmmodels, modelName)
			delete(ml.models, modelName)
		}
		log.Info().Msgf("Unloaded model: %s", modelName)
		return nil
	}

	return fmt.Errorf("model %s is not loaded", modelName)
}

// ListLoadedModels returns a list of currently loaded models
func (ml *ModelLoader) ListLoadedModels() []string {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	models := make([]string, 0, len(ml.models))
	for name := range ml.models {
		models = append(models, name)
	}
	return models
}

// GetMemoryUsage returns current memory usage in MB
func (ml *ModelLoader) GetMemoryUsage() int {
	return ml.getCurrentMemoryUsageMB()
}

// GetMemoryThreshold returns the current memory threshold
func (ml *ModelLoader) GetMemoryThreshold() int {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	return ml.memoryThresholdMB
}
