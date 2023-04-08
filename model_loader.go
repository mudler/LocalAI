package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"text/template"

	llama "github.com/go-skynet/go-llama.cpp"
)

type ModelLoader struct {
	modelPath        string
	mu               sync.Mutex
	models           map[string]*llama.LLama
	promptsTemplates map[string]*template.Template
}

func NewModelLoader(modelPath string) *ModelLoader {
	return &ModelLoader{modelPath: modelPath, models: make(map[string]*llama.LLama), promptsTemplates: make(map[string]*template.Template)}
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

func (ml *ModelLoader) LoadModel(modelName string, opts ...llama.ModelOption) (*llama.LLama, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Check if we already have a loaded model
	modelFile := filepath.Join(ml.modelPath, modelName)

	if m, ok := ml.models[modelFile]; ok {
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
	model, err := llama.New(modelFile, opts...)
	if err != nil {
		return nil, err
	}

	// If there is a prompt template, load it

	modelTemplateFile := fmt.Sprintf("%s.tmpl", modelFile)
	// Check if the model path exists
	if _, err := os.Stat(modelTemplateFile); err == nil {
		dat, err := os.ReadFile(modelTemplateFile)
		if err != nil {
			return nil, err
		}

		// Parse the template
		tmpl, err := template.New("prompt").Parse(string(dat))
		if err != nil {
			return nil, err
		}
		ml.promptsTemplates[modelName] = tmpl
	}

	ml.models[modelFile] = model
	return model, err
}
