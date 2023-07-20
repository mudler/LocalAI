package model

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"

	grammar "github.com/go-skynet/LocalAI/pkg/grammar"
	"github.com/go-skynet/LocalAI/pkg/grpc"
	process "github.com/mudler/go-processmanager"
	"github.com/rs/zerolog/log"
)

// Rather than pass an interface{} to the prompt template:
// These are the definitions of all possible variables LocalAI will currently populate for use in a prompt template file
// Please note: Not all of these are populated on every endpoint - your template should either be tested for each endpoint you map it to, or tolerant of zero values.
type PromptTemplateData struct {
	Input        string
	Instruction  string
	Functions    []grammar.Function
	MessageIndex int
}

// TODO: Ask mudler about FunctionCall stuff being useful at the message level?
type ChatMessageTemplateData struct {
	SystemPrompt string
	Role         string
	RoleName     string
	Content      string
	MessageIndex int
}

type ModelLoader struct {
	ModelPath string
	mu        sync.Mutex
	// TODO: this needs generics
	models               map[string]*grpc.Client
	grpcProcesses        map[string]*process.Process
	promptsTemplates     map[string]*template.Template
	chatMessageTemplates map[string]*template.Template
}

func NewModelLoader(modelPath string) *ModelLoader {
	return &ModelLoader{
		ModelPath:            modelPath,
		models:               make(map[string]*grpc.Client),
		promptsTemplates:     make(map[string]*template.Template),
		chatMessageTemplates: make(map[string]*template.Template),
		grpcProcesses:        make(map[string]*process.Process),
	}
}

func existsInModelPath(modelPath string, s string) bool {
	_, err := os.Stat(filepath.Join(modelPath, s))
	return err == nil
}

func (ml *ModelLoader) ExistsInModelPath(s string) bool {
	return existsInModelPath(ml.ModelPath, s)
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

// TODO: Split ModelLoader and TemplateLoader? Just to keep things more organized. Left together to share a mutex until I look into that.

func evaluateTemplate[T any](templateName string, in T, modelPath string, templateMap *(map[string]*template.Template), mutex *sync.Mutex) (string, error) {
	mutex.Lock()
	defer mutex.Unlock()

	m, ok := (*templateMap)[templateName]
	if !ok {
		// return "", fmt.Errorf("template not loaded: %s", templateName)
		loadErr := loadTemplateIfExists(templateName, modelPath, templateMap)
		if loadErr != nil {
			return "", loadErr
		}
		m = (*templateMap)[templateName] // ok is not important since we check m on the next line
	}
	if m == nil {
		return "", fmt.Errorf("failed loading a template for %s", templateName)
	}

	var buf bytes.Buffer

	if err := m.Execute(&buf, in); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (ml *ModelLoader) TemplatePrefix(templateName string, in PromptTemplateData) (string, error) {
	return evaluateTemplate[PromptTemplateData](templateName, in, ml.ModelPath, &(ml.promptsTemplates), &(ml.mu))
}

func (ml *ModelLoader) TemplateForChatMessage(templateName string, messageData ChatMessageTemplateData) (string, error) {
	return evaluateTemplate[ChatMessageTemplateData](templateName, messageData, ml.ModelPath, &(ml.chatMessageTemplates), &(ml.mu))
}

func loadTemplateIfExists(templateName, modelPath string, templateMap *(map[string]*template.Template)) error {
	// Check if the template was already loaded
	if _, ok := (*templateMap)[templateName]; ok {
		return nil
	}

	// Check if the model path exists
	// skip any error here - we run anyway if a template does not exist
	modelTemplateFile := fmt.Sprintf("%s.tmpl", templateName)

	if !existsInModelPath(modelPath, modelTemplateFile) {
		return nil
	}

	dat, err := os.ReadFile(filepath.Join(modelPath, modelTemplateFile))
	if err != nil {
		return err
	}

	// Parse the template
	tmpl, err := template.New("prompt").Parse(string(dat))
	if err != nil {
		return err
	}
	(*templateMap)[templateName] = tmpl

	return nil
}

func (ml *ModelLoader) LoadModel(modelName string, loader func(string) (*grpc.Client, error)) (*grpc.Client, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Check if we already have a loaded model
	if model := ml.checkIsLoaded(modelName); model != nil {
		log.Debug().Msgf("Model already loaded in memory: %s", modelName)
		return model, nil
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.ModelPath, modelName)
	log.Debug().Msgf("Loading model in memory from file: %s", modelFile)

	model, err := loader(modelFile)
	if err != nil {
		return nil, err
	}

	// If there is a prompt template, load it
	if err := loadTemplateIfExists(modelName, ml.ModelPath, &(ml.promptsTemplates)); err != nil {
		return nil, err
	}

	ml.models[modelName] = model
	return model, nil
}

func (ml *ModelLoader) checkIsLoaded(s string) *grpc.Client {
	if m, ok := ml.models[s]; ok {
		log.Debug().Msgf("Model already loaded in memory: %s", s)

		if !m.HealthCheck(context.Background()) {
			log.Debug().Msgf("GRPC Model not responding: %s", s)
			if !ml.grpcProcesses[s].IsAlive() {
				log.Debug().Msgf("GRPC Process is not responding: %s", s)
				// stop and delete the process, this forces to re-load the model and re-create again the service
				ml.grpcProcesses[s].Stop()
				delete(ml.grpcProcesses, s)
				delete(ml.models, s)
				return nil
			}
		}

		return m
	}

	return nil
}
