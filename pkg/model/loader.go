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

	gptj "github.com/go-skynet/go-gpt4all-j.cpp"
	llama "github.com/go-skynet/go-llama.cpp"
)

type ModelLoader struct {
	modelPath        string
	mu               sync.Mutex
	models           map[string]*llama.LLama
	gptmodels        map[string]*gptj.GPTJ
	promptsTemplates map[string]*template.Template
}

func NewModelLoader(modelPath string) *ModelLoader {
	return &ModelLoader{modelPath: modelPath, gptmodels: make(map[string]*gptj.GPTJ), models: make(map[string]*llama.LLama), promptsTemplates: make(map[string]*template.Template)}
}

func (ml *ModelLoader) ListModels() ([]string, error) {
	files, err := ioutil.ReadDir(ml.modelPath)
	if err != nil {
		return []string{}, err
	}

	models := []string{}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".bin") {
			models = append(models, strings.TrimRight(file.Name(), ".bin"))
		}
	}

	return models, nil
}

func (ml *ModelLoader) TemplatePrefix(modelName string, in interface{}) (string, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	m, ok := ml.promptsTemplates[modelName]
	if !ok {
		// try to find a s.bin
		modelBin := fmt.Sprintf("%s.bin", modelName)
		m, ok = ml.promptsTemplates[modelBin]
		if !ok {
			return "", fmt.Errorf("no prompt template available")
		}
	}

	var buf bytes.Buffer

	if err := m.Execute(&buf, in); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (ml *ModelLoader) loadTemplate(modelName, modelFile string) error {
	modelTemplateFile := fmt.Sprintf("%s.tmpl", modelFile)

	// Check if the model path exists
	if _, err := os.Stat(modelTemplateFile); err != nil {
		return nil
	}

	dat, err := os.ReadFile(modelTemplateFile)
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

func (ml *ModelLoader) LoadGPTJModel(modelName string) (*gptj.GPTJ, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Check if we already have a loaded model
	modelFile := filepath.Join(ml.modelPath, modelName)

	if m, ok := ml.gptmodels[modelFile]; ok {
		return m, nil
	}

	// Check if the model path exists
	if _, err := os.Stat(modelFile); os.IsNotExist(err) {
		// try to find a s.bin
		modelBin := fmt.Sprintf("%s.bin", modelFile)
		if _, err := os.Stat(modelBin); os.IsNotExist(err) {
			return nil, err
		} else {
			modelName = fmt.Sprintf("%s.bin", modelName)
			modelFile = modelBin
		}
	}

	// Load the model and keep it in memory for later use
	model, err := gptj.New(modelFile)
	if err != nil {
		return nil, err
	}

	// If there is a prompt template, load it
	if err := ml.loadTemplate(modelName, modelFile); err != nil {
		return nil, err
	}

	ml.gptmodels[modelFile] = model
	return model, err
}

func (ml *ModelLoader) LoadLLaMAModel(modelName string, opts ...llama.ModelOption) (*llama.LLama, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Check if we already have a loaded model
	modelFile := filepath.Join(ml.modelPath, modelName)
	if m, ok := ml.models[modelFile]; ok {
		return m, nil
	}
	// TODO: This needs refactoring, it's really bad to have it in here
	// Check if we have a GPTJ model loaded instead
	if _, ok := ml.gptmodels[modelFile]; ok {
		return nil, fmt.Errorf("this model is a GPTJ one")
	}

	// Check if the model path exists
	if _, err := os.Stat(modelFile); os.IsNotExist(err) {
		// try to find a s.bin
		modelBin := fmt.Sprintf("%s.bin", modelFile)
		if _, err := os.Stat(modelBin); os.IsNotExist(err) {
			return nil, err
		} else {
			modelName = fmt.Sprintf("%s.bin", modelName)
			modelFile = modelBin
		}
	}

	// Load the model and keep it in memory for later use
	model, err := llama.New(modelFile, opts...)
	if err != nil {
		return nil, err
	}

	// If there is a prompt template, load it
	if err := ml.loadTemplate(modelName, modelFile); err != nil {
		return nil, err
	}

	ml.models[modelFile] = model
	return model, err
}
