package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	llama "github.com/go-skynet/go-llama.cpp"
)

type ModelLoader struct {
	modelPath string
	mu        sync.Mutex
	models    map[string]*llama.LLama
}

func NewModelLoader(modelPath string) *ModelLoader {
	return &ModelLoader{modelPath: modelPath, models: make(map[string]*llama.LLama)}
}

func (ml *ModelLoader) LoadModel(s string, opts ...llama.ModelOption) (*llama.LLama, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Check if we already have a loaded model
	modelFile := filepath.Join(ml.modelPath, s)

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
			modelFile = modelBin
		}
	}

	// Load the model and keep it in memory for later use
	model, err := llama.New(modelFile, opts...)
	if err != nil {
		return nil, err
	}

	ml.models[modelFile] = model
	return model, err
}
