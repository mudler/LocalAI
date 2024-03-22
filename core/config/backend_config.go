package config

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/downloader"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"

	"github.com/charmbracelet/glamour"
)

type BackendConfig struct {
	schema.PredictionOptions `yaml:"parameters"`
	Name                     string `yaml:"name"`

	F16            *bool             `yaml:"f16"`
	Threads        *int              `yaml:"threads"`
	Debug          *bool             `yaml:"debug"`
	Roles          map[string]string `yaml:"roles"`
	Embeddings     bool              `yaml:"embeddings"`
	Backend        string            `yaml:"backend"`
	TemplateConfig TemplateConfig    `yaml:"template"`

	PromptStrings, InputStrings                []string `yaml:"-"`
	InputToken                                 [][]int  `yaml:"-"`
	functionCallString, functionCallNameString string   `yaml:"-"`

	FunctionsConfig Functions `yaml:"function"`

	FeatureFlag FeatureFlag `yaml:"feature_flags"` // Feature Flag registry. We move fast, and features may break on a per model/backend basis. Registry for (usually temporary) flags that indicate aborting something early.
	// LLM configs (GPT4ALL, Llama.cpp, ...)
	LLMConfig `yaml:",inline"`

	// AutoGPTQ specifics
	AutoGPTQ AutoGPTQ `yaml:"autogptq"`

	// Diffusers
	Diffusers Diffusers `yaml:"diffusers"`
	Step      int       `yaml:"step"`

	// GRPC Options
	GRPC GRPC `yaml:"grpc"`

	// Vall-e-x
	VallE VallE `yaml:"vall-e"`

	// CUDA
	// Explicitly enable CUDA or not (some backends might need it)
	CUDA bool `yaml:"cuda"`

	DownloadFiles []File `yaml:"download_files"`

	Description string `yaml:"description"`
	Usage       string `yaml:"usage"`
}

type File struct {
	Filename string `yaml:"filename" json:"filename"`
	SHA256   string `yaml:"sha256" json:"sha256"`
	URI      string `yaml:"uri" json:"uri"`
}

type VallE struct {
	AudioPath string `yaml:"audio_path"`
}

type FeatureFlag map[string]*bool

func (ff FeatureFlag) Enabled(s string) bool {
	v, exist := ff[s]
	return exist && v != nil && *v
}

type GRPC struct {
	Attempts          int `yaml:"attempts"`
	AttemptsSleepTime int `yaml:"attempts_sleep_time"`
}

type Diffusers struct {
	CUDA             bool    `yaml:"cuda"`
	PipelineType     string  `yaml:"pipeline_type"`
	SchedulerType    string  `yaml:"scheduler_type"`
	EnableParameters string  `yaml:"enable_parameters"` // A list of comma separated parameters to specify
	CFGScale         float32 `yaml:"cfg_scale"`         // Classifier-Free Guidance Scale
	IMG2IMG          bool    `yaml:"img2img"`           // Image to Image Diffuser
	ClipSkip         int     `yaml:"clip_skip"`         // Skip every N frames
	ClipModel        string  `yaml:"clip_model"`        // Clip model to use
	ClipSubFolder    string  `yaml:"clip_subfolder"`    // Subfolder to use for clip model
	ControlNet       string  `yaml:"control_net"`
}

type LLMConfig struct {
	SystemPrompt    string   `yaml:"system_prompt"`
	TensorSplit     string   `yaml:"tensor_split"`
	MainGPU         string   `yaml:"main_gpu"`
	RMSNormEps      float32  `yaml:"rms_norm_eps"`
	NGQA            int32    `yaml:"ngqa"`
	PromptCachePath string   `yaml:"prompt_cache_path"`
	PromptCacheAll  bool     `yaml:"prompt_cache_all"`
	PromptCacheRO   bool     `yaml:"prompt_cache_ro"`
	MirostatETA     *float64 `yaml:"mirostat_eta"`
	MirostatTAU     *float64 `yaml:"mirostat_tau"`
	Mirostat        *int     `yaml:"mirostat"`
	NGPULayers      *int     `yaml:"gpu_layers"`
	MMap            *bool    `yaml:"mmap"`
	MMlock          *bool    `yaml:"mmlock"`
	LowVRAM         *bool    `yaml:"low_vram"`
	Grammar         string   `yaml:"grammar"`
	StopWords       []string `yaml:"stopwords"`
	Cutstrings      []string `yaml:"cutstrings"`
	TrimSpace       []string `yaml:"trimspace"`
	TrimSuffix      []string `yaml:"trimsuffix"`

	ContextSize          *int    `yaml:"context_size"`
	NUMA                 bool    `yaml:"numa"`
	LoraAdapter          string  `yaml:"lora_adapter"`
	LoraBase             string  `yaml:"lora_base"`
	LoraScale            float32 `yaml:"lora_scale"`
	NoMulMatQ            bool    `yaml:"no_mulmatq"`
	DraftModel           string  `yaml:"draft_model"`
	NDraft               int32   `yaml:"n_draft"`
	Quantization         string  `yaml:"quantization"`
	GPUMemoryUtilization float32 `yaml:"gpu_memory_utilization"` // vLLM
	TrustRemoteCode      bool    `yaml:"trust_remote_code"`      // vLLM
	EnforceEager         bool    `yaml:"enforce_eager"`          // vLLM
	SwapSpace            int     `yaml:"swap_space"`             // vLLM
	MaxModelLen          int     `yaml:"max_model_len"`          // vLLM
	MMProj               string  `yaml:"mmproj"`

	RopeScaling string `yaml:"rope_scaling"`
	ModelType   string `yaml:"type"`

	YarnExtFactor  float32 `yaml:"yarn_ext_factor"`
	YarnAttnFactor float32 `yaml:"yarn_attn_factor"`
	YarnBetaFast   float32 `yaml:"yarn_beta_fast"`
	YarnBetaSlow   float32 `yaml:"yarn_beta_slow"`
}

type AutoGPTQ struct {
	ModelBaseName    string `yaml:"model_base_name"`
	Device           string `yaml:"device"`
	Triton           bool   `yaml:"triton"`
	UseFastTokenizer bool   `yaml:"use_fast_tokenizer"`
}

type Functions struct {
	DisableNoAction         bool   `yaml:"disable_no_action"`
	NoActionFunctionName    string `yaml:"no_action_function_name"`
	NoActionDescriptionName string `yaml:"no_action_description_name"`
	ParallelCalls           bool   `yaml:"parallel_calls"`
}

type TemplateConfig struct {
	Chat        string `yaml:"chat"`
	ChatMessage string `yaml:"chat_message"`
	Completion  string `yaml:"completion"`
	Edit        string `yaml:"edit"`
	Functions   string `yaml:"function"`
}

func (c *BackendConfig) SetFunctionCallString(s string) {
	c.functionCallString = s
}

func (c *BackendConfig) SetFunctionCallNameString(s string) {
	c.functionCallNameString = s
}

func (c *BackendConfig) ShouldUseFunctions() bool {
	return ((c.functionCallString != "none" || c.functionCallString == "") || c.ShouldCallSpecificFunction())
}

func (c *BackendConfig) ShouldCallSpecificFunction() bool {
	return len(c.functionCallNameString) > 0
}

func (c *BackendConfig) FunctionToCall() string {
	return c.functionCallNameString
}

func (cfg *BackendConfig) SetDefaults(opts ...ConfigLoaderOption) {
	lo := &LoadOptions{}
	lo.Apply(opts...)

	ctx := lo.ctxSize
	threads := lo.threads
	f16 := lo.f16
	debug := lo.debug
	defaultTopP := 0.7
	defaultTopK := 80
	defaultTemp := 0.9
	defaultMaxTokens := 2048
	defaultMirostat := 2
	defaultMirostatTAU := 5.0
	defaultMirostatETA := 0.1

	// Try to offload all GPU layers (if GPU is found)
	defaultNGPULayers := 99999999

	trueV := true
	falseV := false

	if cfg.Seed == nil {
		//  random number generator seed
		defaultSeed := int(rand.Int31())
		cfg.Seed = &defaultSeed
	}

	if cfg.TopK == nil {
		cfg.TopK = &defaultTopK
	}

	if cfg.MMap == nil {
		// MMap is enabled by default
		cfg.MMap = &trueV
	}

	if cfg.MMlock == nil {
		// MMlock is disabled by default
		cfg.MMlock = &falseV
	}

	if cfg.TopP == nil {
		cfg.TopP = &defaultTopP
	}
	if cfg.Temperature == nil {
		cfg.Temperature = &defaultTemp
	}

	if cfg.Maxtokens == nil {
		cfg.Maxtokens = &defaultMaxTokens
	}

	if cfg.Mirostat == nil {
		cfg.Mirostat = &defaultMirostat
	}

	if cfg.MirostatETA == nil {
		cfg.MirostatETA = &defaultMirostatETA
	}

	if cfg.MirostatTAU == nil {
		cfg.MirostatTAU = &defaultMirostatTAU
	}
	if cfg.NGPULayers == nil {
		cfg.NGPULayers = &defaultNGPULayers
	}

	if cfg.LowVRAM == nil {
		cfg.LowVRAM = &falseV
	}

	// Value passed by the top level are treated as default (no implicit defaults)
	// defaults are set by the user
	if ctx == 0 {
		ctx = 1024
	}

	if cfg.ContextSize == nil {
		cfg.ContextSize = &ctx
	}

	if threads == 0 {
		// Threads can't be 0
		threads = 4
	}

	if cfg.Threads == nil {
		cfg.Threads = &threads
	}

	if cfg.F16 == nil {
		cfg.F16 = &f16
	}

	if cfg.Debug == nil {
		cfg.Debug = &falseV
	}

	if debug {
		cfg.Debug = &trueV
	}
}

////// Config Loader ////////

type BackendConfigLoader struct {
	configs map[string]BackendConfig
	sync.Mutex
}

type LoadOptions struct {
	debug            bool
	threads, ctxSize int
	f16              bool
}

func LoadOptionDebug(debug bool) ConfigLoaderOption {
	return func(o *LoadOptions) {
		o.debug = debug
	}
}

func LoadOptionThreads(threads int) ConfigLoaderOption {
	return func(o *LoadOptions) {
		o.threads = threads
	}
}

func LoadOptionContextSize(ctxSize int) ConfigLoaderOption {
	return func(o *LoadOptions) {
		o.ctxSize = ctxSize
	}
}

func LoadOptionF16(f16 bool) ConfigLoaderOption {
	return func(o *LoadOptions) {
		o.f16 = f16
	}
}

type ConfigLoaderOption func(*LoadOptions)

func (lo *LoadOptions) Apply(options ...ConfigLoaderOption) {
	for _, l := range options {
		l(lo)
	}
}

// Load a config file for a model
func (cl *BackendConfigLoader) LoadBackendConfigFileByName(modelName, modelPath string, opts ...ConfigLoaderOption) (*BackendConfig, error) {

	// Load a config file if present after the model name
	cfg := &BackendConfig{
		PredictionOptions: schema.PredictionOptions{
			Model: modelName,
		},
	}

	cfgExisting, exists := cl.GetBackendConfig(modelName)
	if exists {
		cfg = &cfgExisting
	} else {
		// Try loading a model config file
		modelConfig := filepath.Join(modelPath, modelName+".yaml")
		if _, err := os.Stat(modelConfig); err == nil {
			if err := cl.LoadBackendConfig(
				modelConfig, opts...,
			); err != nil {
				return nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
			}
			cfgExisting, exists = cl.GetBackendConfig(modelName)
			if exists {
				cfg = &cfgExisting
			}
		}
	}

	cfg.SetDefaults(opts...)

	return cfg, nil
}

func NewBackendConfigLoader() *BackendConfigLoader {
	return &BackendConfigLoader{
		configs: make(map[string]BackendConfig),
	}
}
func ReadBackendConfigFile(file string, opts ...ConfigLoaderOption) ([]*BackendConfig, error) {
	c := &[]*BackendConfig{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	for _, cc := range *c {
		cc.SetDefaults(opts...)
	}

	return *c, nil
}

func ReadBackendConfig(file string, opts ...ConfigLoaderOption) (*BackendConfig, error) {
	lo := &LoadOptions{}
	lo.Apply(opts...)

	c := &BackendConfig{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	c.SetDefaults(opts...)
	return c, nil
}

func (cm *BackendConfigLoader) LoadBackendConfigFile(file string, opts ...ConfigLoaderOption) error {
	cm.Lock()
	defer cm.Unlock()
	c, err := ReadBackendConfigFile(file, opts...)
	if err != nil {
		return fmt.Errorf("cannot load config file: %w", err)
	}

	for _, cc := range c {
		cm.configs[cc.Name] = *cc
	}
	return nil
}

func (cl *BackendConfigLoader) LoadBackendConfig(file string, opts ...ConfigLoaderOption) error {
	cl.Lock()
	defer cl.Unlock()
	c, err := ReadBackendConfig(file, opts...)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	cl.configs[c.Name] = *c
	return nil
}

func (cl *BackendConfigLoader) GetBackendConfig(m string) (BackendConfig, bool) {
	cl.Lock()
	defer cl.Unlock()
	v, exists := cl.configs[m]
	return v, exists
}

func (cl *BackendConfigLoader) GetAllBackendConfigs() []BackendConfig {
	cl.Lock()
	defer cl.Unlock()
	var res []BackendConfig
	for _, v := range cl.configs {
		res = append(res, v)
	}
	return res
}

func (cl *BackendConfigLoader) ListBackendConfigs() []string {
	cl.Lock()
	defer cl.Unlock()
	var res []string
	for k := range cl.configs {
		res = append(res, k)
	}
	return res
}

// Preload prepare models if they are not local but url or huggingface repositories
func (cl *BackendConfigLoader) Preload(modelPath string) error {
	cl.Lock()
	defer cl.Unlock()

	status := func(fileName, current, total string, percent float64) {
		utils.DisplayDownloadFunction(fileName, current, total, percent)
	}

	log.Info().Msgf("Preloading models from %s", modelPath)

	renderMode := "dark"
	if os.Getenv("COLOR") != "" {
		renderMode = os.Getenv("COLOR")
	}

	glamText := func(t string) {
		out, err := glamour.Render(t, renderMode)
		if err == nil && os.Getenv("NO_COLOR") == "" {
			fmt.Println(out)
		} else {
			fmt.Println(t)
		}
	}

	for i, config := range cl.configs {

		// Download files and verify their SHA
		for _, file := range config.DownloadFiles {
			log.Debug().Msgf("Checking %q exists and matches SHA", file.Filename)

			if err := utils.VerifyPath(file.Filename, modelPath); err != nil {
				return err
			}
			// Create file path
			filePath := filepath.Join(modelPath, file.Filename)

			if err := downloader.DownloadFile(file.URI, filePath, file.SHA256, status); err != nil {
				return err
			}
		}

		modelURL := config.PredictionOptions.Model
		modelURL = downloader.ConvertURL(modelURL)

		if downloader.LooksLikeURL(modelURL) {
			// md5 of model name
			md5Name := utils.MD5(modelURL)

			// check if file exists
			if _, err := os.Stat(filepath.Join(modelPath, md5Name)); errors.Is(err, os.ErrNotExist) {
				err := downloader.DownloadFile(modelURL, filepath.Join(modelPath, md5Name), "", status)
				if err != nil {
					return err
				}
			}

			cc := cl.configs[i]
			c := &cc
			c.PredictionOptions.Model = md5Name
			cl.configs[i] = *c
		}
		if cl.configs[i].Name != "" {
			glamText(fmt.Sprintf("**Model name**: _%s_", cl.configs[i].Name))
		}
		if cl.configs[i].Description != "" {
			//glamText("**Description**")
			glamText(cl.configs[i].Description)
		}
		if cl.configs[i].Usage != "" {
			//glamText("**Usage**")
			glamText(cl.configs[i].Usage)
		}
	}
	return nil
}

// LoadBackendConfigsFromPath reads all the configurations of the models from a path
// (non-recursive)
func (cm *BackendConfigLoader) LoadBackendConfigsFromPath(path string, opts ...ConfigLoaderOption) error {
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
		if !strings.Contains(file.Name(), ".yaml") && !strings.Contains(file.Name(), ".yml") {
			continue
		}
		c, err := ReadBackendConfig(filepath.Join(path, file.Name()), opts...)
		if err == nil {
			cm.configs[c.Name] = *c
		}
	}

	return nil
}
