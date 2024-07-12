package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/pkg/templates"

	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/utils"

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
	Functions            []functions.Function
	MessageIndex         int
}

type ChatMessageTemplateData struct {
	SystemPrompt string
	Role         string
	RoleName     string
	FunctionName string
	Content      string
	MessageIndex int
	Function     bool
	FunctionCall interface{}
	LastMessage  bool
}

// new idea: what if we declare a struct of these here, and use a loop to check?

// TODO: Split ModelLoader and TemplateLoader? Just to keep things more organized. Left together to share a mutex until I look into that. Would split if we seperate directories for .bin/.yaml and .tmpl
type ModelLoader struct {
	ModelPath string
	mu        sync.Mutex
	// TODO: this needs generics
	grpcClients   map[string]grpc.Backend
	models        map[string]ModelAddress
	grpcProcesses map[string]*process.Process
	templates     *templates.TemplateCache
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
	".DS_Store",
	".",
	".partial",
	".tar.gz",
}

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

	return ml.stopModel(modelName)
}

func (ml *ModelLoader) stopModel(modelName string) error {
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
				err := ml.deleteProcess(s)
				if err != nil {
					log.Error().Err(err).Str("process", s).Msg("error stopping process")
				}
				return ""
			}
		}

		return m
	}

	return ""
}

const (
	ChatPromptTemplate templates.TemplateType = iota
	ChatMessageTemplate
	CompletionPromptTemplate
	EditPromptTemplate
	FunctionsPromptTemplate
)

func (ml *ModelLoader) EvaluateTemplateForPrompt(templateType templates.TemplateType, templateName string, in PromptTemplateData) (string, error) {
	// TODO: should this check be improved?
	if templateType == ChatMessageTemplate {
		return "", fmt.Errorf("invalid templateType: ChatMessage")
	}
	return ml.templates.EvaluateTemplate(templateType, templateName, in)
}

func (ml *ModelLoader) EvaluateTemplateForChatMessage(templateName string, messageData ChatMessageTemplateData) (string, error) {
	return ml.templates.EvaluateTemplate(ChatMessageTemplate, templateName, messageData)
}
