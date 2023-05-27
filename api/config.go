package api

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type Config struct {
	OpenAIRequest         `yaml:"parameters"`
	Name                  string            `yaml:"name"`
	StopWords             []string          `yaml:"stopwords"`
	Cutstrings            []string          `yaml:"cutstrings"`
	TrimSpace             []string          `yaml:"trimspace"`
	ContextSize           int               `yaml:"context_size"`
	F16                   bool              `yaml:"f16"`
	Threads               int               `yaml:"threads"`
	Debug                 bool              `yaml:"debug"`
	Roles                 map[string]string `yaml:"roles"`
	Embeddings            bool              `yaml:"embeddings"`
	Backend               string            `yaml:"backend"`
	TemplateConfig        TemplateConfig    `yaml:"template"`
	MirostatETA           float64           `yaml:"mirostat_eta"`
	MirostatTAU           float64           `yaml:"mirostat_tau"`
	Mirostat              int               `yaml:"mirostat"`
	NGPULayers            int               `yaml:"gpu_layers"`
	ImageGenerationAssets string            `yaml:"asset_dir"`

	PromptCachePath string `yaml:"prompt_cache_path"`
	PromptCacheAll  bool   `yaml:"prompt_cache_all"`

	PromptStrings, InputStrings []string
	InputToken                  [][]int
}

type TemplateConfig struct {
	Completion string `yaml:"completion"`
	Chat       string `yaml:"chat"`
	Edit       string `yaml:"edit"`
}

type ConfigMerger struct {
	configs map[string]Config
	sync.Mutex
}

func NewConfigMerger() *ConfigMerger {
	return &ConfigMerger{
		configs: make(map[string]Config),
	}
}
func ReadConfigFile(file string) ([]*Config, error) {
	c := &[]*Config{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	return *c, nil
}

func ReadConfig(file string) (*Config, error) {
	c := &Config{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	return c, nil
}

func (cm ConfigMerger) LoadConfigFile(file string) error {
	cm.Lock()
	defer cm.Unlock()
	c, err := ReadConfigFile(file)
	if err != nil {
		return fmt.Errorf("cannot load config file: %w", err)
	}

	for _, cc := range c {
		cm.configs[cc.Name] = *cc
	}
	return nil
}

func (cm ConfigMerger) LoadConfig(file string) error {
	cm.Lock()
	defer cm.Unlock()
	c, err := ReadConfig(file)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	cm.configs[c.Name] = *c
	return nil
}

func (cm ConfigMerger) GetConfig(m string) (Config, bool) {
	cm.Lock()
	defer cm.Unlock()
	v, exists := cm.configs[m]
	return v, exists
}

func (cm ConfigMerger) ListConfigs() []string {
	cm.Lock()
	defer cm.Unlock()
	var res []string
	for k := range cm.configs {
		res = append(res, k)
	}
	return res
}

func (cm ConfigMerger) LoadConfigs(path string) error {
	cm.Lock()
	defer cm.Unlock()
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}

	for _, file := range files {
		// Skip templates, YAML and .keep files
		if !strings.Contains(file.Name(), ".yaml") {
			continue
		}
		c, err := ReadConfig(filepath.Join(path, file.Name()))
		if err == nil {
			cm.configs[c.Name] = *c
		}
	}

	return nil
}

func updateConfig(config *Config, input *OpenAIRequest) {
	if input.Echo {
		config.Echo = input.Echo
	}
	if input.TopK != 0 {
		config.TopK = input.TopK
	}
	if input.TopP != 0 {
		config.TopP = input.TopP
	}

	if input.Temperature != 0 {
		config.Temperature = input.Temperature
	}

	if input.Maxtokens != 0 {
		config.Maxtokens = input.Maxtokens
	}

	switch stop := input.Stop.(type) {
	case string:
		if stop != "" {
			config.StopWords = append(config.StopWords, stop)
		}
	case []interface{}:
		for _, pp := range stop {
			if s, ok := pp.(string); ok {
				config.StopWords = append(config.StopWords, s)
			}
		}
	}

	if input.RepeatPenalty != 0 {
		config.RepeatPenalty = input.RepeatPenalty
	}

	if input.Keep != 0 {
		config.Keep = input.Keep
	}

	if input.Batch != 0 {
		config.Batch = input.Batch
	}

	if input.F16 {
		config.F16 = input.F16
	}

	if input.IgnoreEOS {
		config.IgnoreEOS = input.IgnoreEOS
	}

	if input.Seed != 0 {
		config.Seed = input.Seed
	}

	if input.Mirostat != 0 {
		config.Mirostat = input.Mirostat
	}

	if input.MirostatETA != 0 {
		config.MirostatETA = input.MirostatETA
	}

	if input.MirostatTAU != 0 {
		config.MirostatTAU = input.MirostatTAU
	}

	switch inputs := input.Input.(type) {
	case string:
		if inputs != "" {
			config.InputStrings = append(config.InputStrings, inputs)
		}
	case []interface{}:
		for _, pp := range inputs {
			switch i := pp.(type) {
			case string:
				config.InputStrings = append(config.InputStrings, i)
			case []interface{}:
				tokens := []int{}
				for _, ii := range i {
					tokens = append(tokens, int(ii.(float64)))
				}
				config.InputToken = append(config.InputToken, tokens)
			}
		}
	}

	switch p := input.Prompt.(type) {
	case string:
		config.PromptStrings = append(config.PromptStrings, p)
	case []interface{}:
		for _, pp := range p {
			if s, ok := pp.(string); ok {
				config.PromptStrings = append(config.PromptStrings, s)
			}
		}
	}
}
func readInput(c *fiber.Ctx, loader *model.ModelLoader, randomModel bool) (string, *OpenAIRequest, error) {
	input := new(OpenAIRequest)
	// Get input data from the request body
	if err := c.BodyParser(input); err != nil {
		return "", nil, err
	}

	modelFile := input.Model

	if c.Params("model") != "" {
		modelFile = c.Params("model")
	}

	received, _ := json.Marshal(input)

	log.Debug().Msgf("Request received: %s", string(received))

	// Set model from bearer token, if available
	bearer := strings.TrimLeft(c.Get("authorization"), "Bearer ")
	bearerExists := bearer != "" && loader.ExistsInModelPath(bearer)

	// If no model was specified, take the first available
	if modelFile == "" && !bearerExists && randomModel {
		models, _ := loader.ListModels()
		if len(models) > 0 {
			modelFile = models[0]
			log.Debug().Msgf("No model specified, using: %s", modelFile)
		} else {
			log.Debug().Msgf("No model specified, returning error")
			return "", nil, fmt.Errorf("no model specified")
		}
	}

	// If a model is found in bearer token takes precedence
	if bearerExists {
		log.Debug().Msgf("Using model from bearer token: %s", bearer)
		modelFile = bearer
	}
	return modelFile, input, nil
}

func readConfig(modelFile string, input *OpenAIRequest, cm *ConfigMerger, loader *model.ModelLoader, debug bool, threads, ctx int, f16 bool) (*Config, *OpenAIRequest, error) {
	// Load a config file if present after the model name
	modelConfig := filepath.Join(loader.ModelPath, modelFile+".yaml")
	if _, err := os.Stat(modelConfig); err == nil {
		if err := cm.LoadConfig(modelConfig); err != nil {
			return nil, nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
		}
	}

	var config *Config
	cfg, exists := cm.GetConfig(modelFile)
	if !exists {
		config = &Config{
			OpenAIRequest: defaultRequest(modelFile),
			ContextSize:   ctx,
			Threads:       threads,
			F16:           f16,
			Debug:         debug,
		}
	} else {
		config = &cfg
	}

	// Set the parameters for the language model prediction
	updateConfig(config, input)

	// Don't allow 0 as setting
	if config.Threads == 0 {
		if threads != 0 {
			config.Threads = threads
		} else {
			config.Threads = 4
		}
	}

	// Enforce debug flag if passed from CLI
	if debug {
		config.Debug = true
	}

	return config, input, nil
}
