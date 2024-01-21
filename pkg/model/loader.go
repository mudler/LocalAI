package model

import (
	"bytes"
	"context"
	"fmt"
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
	SystemPrompt         string
	SuppressSystemPrompt bool // used by chat specifically to indicate that SystemPrompt above should be _ignored_
	Input                string
	Instruction          string
	Functions            []grammar.Function
	MessageIndex         int
}

// TODO: Ask mudler about FunctionCall stuff being useful at the message level?
type ChatMessageTemplateData struct {
	SystemPrompt string
	Role         string
	RoleName     string
	Content      string
	MessageIndex int
}

// Keep this in sync with config.TemplateConfig. Is there a more idiomatic way to accomplish this in go?
// Technically, order doesn't _really_ matter, but the count must stay in sync, see tests/integration/reflect_test.go
type TemplateType int

const (
	ChatPromptTemplate TemplateType = iota
	ChatMessageTemplate
	CompletionPromptTemplate
	EditPromptTemplate
	FunctionsPromptTemplate

	// The following TemplateType is **NOT** a valid value and MUST be last. It exists to make the sanity integration tests simpler!
	IntegrationTestTemplate
)

// new idea: what if we declare a struct of these here, and use a loop to check?

// TODO: Split ModelLoader and TemplateLoader? Just to keep things more organized. Left together to share a mutex until I look into that. Would split if we seperate directories for .bin/.yaml and .tmpl
type ModelLoader struct {
	ModelPath string
	mu        sync.Mutex
	// TODO: this needs generics
	grpcClients   map[string]grpc.Backend
	models        map[string]ModelAddress
	grpcProcesses map[string]*process.Process
	templates     map[TemplateType]map[string]*template.Template
	wd            *WatchDog
}

type ModelAddress string

func (m ModelAddress) GRPC(parallel bool, wd *WatchDog) grpc.Backend {
	enableWD := false
	if wd != nil {
		enableWD = true
	}
	return grpc.NewClient(string(m), parallel, wd, enableWD)
}

func NewModelLoader(modelPath string) *ModelLoader {
	nml := &ModelLoader{
		ModelPath:     modelPath,
		grpcClients:   make(map[string]grpc.Backend),
		models:        make(map[string]ModelAddress),
		templates:     make(map[TemplateType]map[string]*template.Template),
		grpcProcesses: make(map[string]*process.Process),
	}

	nml.initializeTemplateMap()
	return nml
}

func (ml *ModelLoader) SetWatchDog(wd *WatchDog) {
	ml.wd = wd
}

func (ml *ModelLoader) ExistsInModelPath(s string) bool {
	return existsInPath(ml.ModelPath, s)
}

func (ml *ModelLoader) ListModels() ([]string, error) {
	files, err := os.ReadDir(ml.ModelPath)
	if err != nil {
		return []string{}, err
	}

	models := []string{}
	for _, file := range files {
		// Skip templates, YAML, .keep, .json, and .DS_Store files - TODO: as this list grows, is there a more efficient method?
		if strings.HasSuffix(file.Name(), ".tmpl") || strings.HasSuffix(file.Name(), ".keep") || strings.HasSuffix(file.Name(), ".yaml") || strings.HasSuffix(file.Name(), ".yml") || strings.HasSuffix(file.Name(), ".json") || strings.HasSuffix(file.Name(), ".DS_Store") {
			continue
		}

		models = append(models, file.Name())
	}

	return models, nil
}

func (ml *ModelLoader) LoadModel(modelName string, loader func(string, string) (ModelAddress, error)) (ModelAddress, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Check if we already have a loaded model
	if model := ml.CheckIsLoaded(modelName); model != "" {
		return model, nil
	}

	// Load the model and keep it in memory for later use
	modelFile := filepath.Join(ml.ModelPath, modelName)
	log.Debug().Msgf("Loading model in memory from file: %s", modelFile)

	model, err := loader(modelName, modelFile)
	if err != nil {
		return "", err
	}

	// TODO: Add a helper method to iterate all prompt templates associated with a config if and only if it's YAML?
	// Minor perf loss here until this is fixed, but we initialize on first request

	// // If there is a prompt template, load it
	// if err := ml.loadTemplateIfExists(modelName); err != nil {
	// 	return nil, err
	// }

	ml.models[modelName] = model
	return model, nil
}

func (ml *ModelLoader) ShutdownModel(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	return ml.StopModel(modelName)
}

func (ml *ModelLoader) StopModel(modelName string) error {
	defer ml.deleteProcess(modelName)
	if _, ok := ml.models[modelName]; !ok {
		return fmt.Errorf("model %s not found", modelName)
	}
	return nil
	//return ml.deleteProcess(modelName)
}

func (ml *ModelLoader) CheckIsLoaded(s string) ModelAddress {
	var client grpc.Backend
	if m, ok := ml.models[s]; ok {
		log.Debug().Msgf("Model already loaded in memory: %s", s)
		if c, ok := ml.grpcClients[s]; ok {
			client = c
		} else {
			client = m.GRPC(false, ml.wd)
		}
		alive, err := client.HealthCheck(context.Background())
		if !alive {
			log.Warn().Msgf("GRPC Model not responding: %s", err.Error())
			log.Warn().Msgf("Deleting the process in order to recreate it")
			if !ml.grpcProcesses[s].IsAlive() {
				log.Debug().Msgf("GRPC Process is not responding: %s", s)
				// stop and delete the process, this forces to re-load the model and re-create again the service
				ml.deleteProcess(s)
				return ""
			}
		}

		return m
	}

	return ""
}

func (ml *ModelLoader) EvaluateTemplateForPrompt(templateType TemplateType, templateName string, in PromptTemplateData) (string, error) {
	// TODO: should this check be improved?
	if templateType == ChatMessageTemplate {
		return "", fmt.Errorf("invalid templateType: ChatMessage")
	}
	return ml.evaluateTemplate(templateType, templateName, in)
}

func (ml *ModelLoader) EvaluateTemplateForChatMessage(templateName string, messageData ChatMessageTemplateData) (string, error) {
	return ml.evaluateTemplate(ChatMessageTemplate, templateName, messageData)
}

func existsInPath(path string, s string) bool {
	_, err := os.Stat(filepath.Join(path, s))
	return err == nil
}

func (ml *ModelLoader) initializeTemplateMap() {
	// This also seems somewhat clunky as we reference the Test / End of valid data value slug, but it works?
	for tt := TemplateType(0); tt < IntegrationTestTemplate; tt++ {
		ml.templates[tt] = make(map[string]*template.Template)
	}
}

func (ml *ModelLoader) evaluateTemplate(templateType TemplateType, templateName string, in interface{}) (string, error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	m, ok := ml.templates[templateType][templateName]
	if !ok {
		// return "", fmt.Errorf("template not loaded: %s", templateName)
		loadErr := ml.loadTemplateIfExists(templateType, templateName)
		if loadErr != nil {
			return "", loadErr
		}
		m = ml.templates[templateType][templateName] // ok is not important since we check m on the next line, and wealready checked
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

func (ml *ModelLoader) loadTemplateIfExists(templateType TemplateType, templateName string) error {
	// Check if the template was already loaded
	if _, ok := ml.templates[templateType][templateName]; ok {
		return nil
	}

	// Check if the model path exists
	// skip any error here - we run anyway if a template does not exist
	modelTemplateFile := fmt.Sprintf("%s.tmpl", templateName)

	dat := ""
	if ml.ExistsInModelPath(modelTemplateFile) {
		d, err := os.ReadFile(filepath.Join(ml.ModelPath, modelTemplateFile))
		if err != nil {
			return err
		}
		dat = string(d)
	} else {
		dat = templateName
	}

	// Parse the template
	tmpl, err := template.New("prompt").Parse(dat)
	if err != nil {
		return err
	}
	ml.templates[templateType][templateName] = tmpl

	return nil
}
