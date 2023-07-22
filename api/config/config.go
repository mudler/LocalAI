package api_config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PredictionOptions `yaml:"parameters"`
	Name              string            `yaml:"name"`
	StopWords         []string          `yaml:"stopwords"`
	Cutstrings        []string          `yaml:"cutstrings"`
	TrimSpace         []string          `yaml:"trimspace"`
	ContextSize       int               `yaml:"context_size"`
	F16               bool              `yaml:"f16"`
	NUMA              bool              `yaml:"numa"`
	Threads           int               `yaml:"threads"`
	Debug             bool              `yaml:"debug"`
	Roles             map[string]string `yaml:"roles"`
	Embeddings        bool              `yaml:"embeddings"`
	Backend           string            `yaml:"backend"`
	TemplateConfig    TemplateConfig    `yaml:"template"`
	MirostatETA       float64           `yaml:"mirostat_eta"`
	MirostatTAU       float64           `yaml:"mirostat_tau"`
	Mirostat          int               `yaml:"mirostat"`
	NGPULayers        int               `yaml:"gpu_layers"`
	MMap              bool              `yaml:"mmap"`
	MMlock            bool              `yaml:"mmlock"`
	LowVRAM           bool              `yaml:"low_vram"`

	TensorSplit           string `yaml:"tensor_split"`
	MainGPU               string `yaml:"main_gpu"`
	ImageGenerationAssets string `yaml:"asset_dir"`

	PromptCachePath string `yaml:"prompt_cache_path"`
	PromptCacheAll  bool   `yaml:"prompt_cache_all"`
	PromptCacheRO   bool   `yaml:"prompt_cache_ro"`

	Grammar string `yaml:"grammar"`

	PromptStrings, InputStrings                []string
	InputToken                                 [][]int
	functionCallString, functionCallNameString string

	FunctionsConfig Functions `yaml:"function"`

	SystemPrompt string `yaml:"system_prompt"`
}

type Functions struct {
	DisableNoAction         bool   `yaml:"disable_no_action"`
	NoActionFunctionName    string `yaml:"no_action_function_name"`
	NoActionDescriptionName string `yaml:"no_action_description_name"`
}

type TemplateConfig struct {
	Chat        string `yaml:"chat"`
	ChatMessage string `yaml:"chat_message"`
	Completion  string `yaml:"completion"`
	Edit        string `yaml:"edit"`
	Functions   string `yaml:"function"`
}

type ConfigLoader struct {
	configs map[string]Config
	sync.Mutex
}

func (c *Config) SetFunctionCallString(s string) {
	c.functionCallString = s
}

func (c *Config) SetFunctionCallNameString(s string) {
	c.functionCallNameString = s
}

func (c *Config) ShouldUseFunctions() bool {
	return ((c.functionCallString != "none" || c.functionCallString == "") || c.ShouldCallSpecificFunction())
}

func (c *Config) ShouldCallSpecificFunction() bool {
	return len(c.functionCallNameString) > 0
}

func (c *Config) FunctionToCall() string {
	return c.functionCallNameString
}

func defaultPredictOptions(modelFile string) PredictionOptions {
	return PredictionOptions{
		TopP:        0.7,
		TopK:        80,
		Maxtokens:   512,
		Temperature: 0.9,
		Model:       modelFile,
	}
}

func DefaultConfig(modelFile string) *Config {
	return &Config{
		PredictionOptions: defaultPredictOptions(modelFile),
	}
}

func NewConfigLoader() *ConfigLoader {
	return &ConfigLoader{
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

func (cm *ConfigLoader) LoadConfigFile(file string) error {
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

func (cm *ConfigLoader) LoadConfig(file string) error {
	cm.Lock()
	defer cm.Unlock()
	c, err := ReadConfig(file)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	cm.configs[c.Name] = *c
	return nil
}

func (cm *ConfigLoader) GetConfig(m string) (Config, bool) {
	cm.Lock()
	defer cm.Unlock()
	v, exists := cm.configs[m]
	return v, exists
}

func (cm *ConfigLoader) ListConfigs() []string {
	cm.Lock()
	defer cm.Unlock()
	var res []string
	for k := range cm.configs {
		res = append(res, k)
	}
	return res
}

func (cm *ConfigLoader) LoadConfigs(path string) error {
	cm.Lock()
	defer cm.Unlock()
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	files := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, info)
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
