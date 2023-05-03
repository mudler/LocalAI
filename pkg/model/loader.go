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

	"github.com/rs/zerolog/log"

	rwkv "github.com/donomii/go-rwkv.cpp"
	gpt2 "github.com/go-skynet/go-gpt2.cpp"
	gptj "github.com/go-skynet/go-gpt4all-j.cpp"
	llama "github.com/go-skynet/go-llama.cpp"
)

type ModelLoader struct {
	ModelPath string
	mu        sync.Mutex

	models            map[string]*llama.LLama
	gptmodels         map[string]*gptj.GPTJ
	gpt2models        map[string]*gpt2.GPT2
	gptstablelmmodels map[string]*gpt2.StableLM
	rwkv              map[string]*rwkv.RwkvState
	promptsTemplates  map[string]*template.Template
}

func NewModelLoader(modelPath string) *ModelLoader {
	return &ModelLoader{
		ModelPath:         modelPath,
		gpt2models:        make(map[string]*gpt2.GPT2),
		gptmodels:         make(map[string]*gptj.GPTJ),
		gptstablelmmodels: make(map[string]*gpt2.StableLM),
		models:            make(map[string]*llama.LLama),
		rwkv:              make(map[string]*rwkv.RwkvState),
		promptsTemplates:  make(map[string]*template.Template),
	}
}

func (ml *ModelLoader) ExistsInModelPath(s string) bool {
	_, err := os.Stat(filepath.Join(ml.ModelPath, s))
	return err == nil
}

func (ml *ModelLoader) ListModels() ([]string, error) {
	files, err := ioutil.ReadDir(ml.ModelPath)
	if err != nil {
		return []string{}, err
	}

	models := []string{}
	for _, file := range files {
		// Skip templates, YAML and .keep files
		if strings.HasSuffix(file.Name(), ".tmpl") || strings.HasSuffix(file.Name(), ".keep") || strings.HasSuffix(file.Name(), ".yaml") || strings.HasSuffix(file.Name(), ".yml") {
			continue
		}

		models = append(models, file.Name())
	}

	return models, nil
}

func (ml *ModelLoader) TemplatePrefix(modelName string, in interface{}) (string, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	m, ok := ml.promptsTemplates[modelName]
	if !ok {
		modelFile := filepath.Join(ml.ModelPath, modelName)
		if err := ml.loadTemplateIfExists(modelName, modelFile); err != nil {
			return "", err
		}

		t, exists := ml.promptsTemplates[modelName]
		if exists {
			m = t
		}

	}
	if m == nil {
		return "", nil
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
	// skip any error here - we run anyway if a template does not exist
	modelTemplateFile := fmt.Sprintf("%s.tmpl", modelName)

	if !ml.ExistsInModelPath(modelTemplateFile) {
		return nil
	}

	dat, err := os.ReadFile(filepath.Join(ml.ModelPath, modelTemplateFile))
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

func (ml *ModelLoader) LoadStableLMModel(modelName string) (*gpt2.StableLM, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Check if we already have a loaded model
	if !ml.ExistsInModelPath(modelName) {
		return nil, fmt.Errorf("model does not exist")
	}

	if m, ok := ml.gptstablelmmodels[modelName]; ok {
		log.Debug().Msgf("Model already loaded in memory: %s", modelName)
		return m, nil
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.ModelPath, modelName)
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
		return m, nil
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.ModelPath, modelName)
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
		return m, nil
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.ModelPath, modelName)
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
	return model, err
}

func (ml *ModelLoader) LoadRWKV(modelName, tokenFile string, threads uint32) (*rwkv.RwkvState, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	log.Debug().Msgf("Loading model name: %s", modelName)

	// Check if we already have a loaded model
	if !ml.ExistsInModelPath(modelName) {
		return nil, fmt.Errorf("model does not exist")
	}

	if m, ok := ml.rwkv[modelName]; ok {
		log.Debug().Msgf("Model already loaded in memory: %s", modelName)
		return m, nil
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.ModelPath, modelName)
	tokenPath := filepath.Join(ml.ModelPath, tokenFile)
	log.Debug().Msgf("Loading model in memory from file: %s", modelFile)

	model := rwkv.LoadFiles(modelFile, tokenPath, threads)
	if model == nil {
		return nil, fmt.Errorf("could not load model")
	}

	ml.rwkv[modelName] = model
	return model, nil
}

func (ml *ModelLoader) LoadLLaMAModel(modelName string, opts ...llama.ModelOption) (*llama.LLama, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	log.Debug().Msgf("Loading model name: %s", modelName)

	// Check if we already have a loaded model
	if !ml.ExistsInModelPath(modelName) {
		return nil, fmt.Errorf("model does not exist")
	}

	if m, ok := ml.models[modelName]; ok {
		log.Debug().Msgf("Model already loaded in memory: %s", modelName)
		return m, nil
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.ModelPath, modelName)
	log.Debug().Msgf("Loading model in memory from file: %s", modelFile)

	model, err := llama.New(modelFile, opts...)
	if err != nil {
		return nil, err
	}

	// If there is a prompt template, load it
	if err := ml.loadTemplateIfExists(modelName, modelFile); err != nil {
		return nil, err
	}

	ml.models[modelName] = model
	return model, err
}
