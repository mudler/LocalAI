package config

import (
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/functions"
)

const (
	RAND_SEED = -1
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

	FunctionsConfig functions.FunctionsConfig `yaml:"function"`

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
	TensorParallelSize   int     `yaml:"tensor_parallel_size"`   // vLLM
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

type TemplateConfig struct {
	Chat                 string `yaml:"chat"`
	ChatMessage          string `yaml:"chat_message"`
	Completion           string `yaml:"completion"`
	Edit                 string `yaml:"edit"`
	Functions            string `yaml:"function"`
	UseTokenizerTemplate bool   `yaml:"use_tokenizer_template"`
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
	if c.functionCallNameString != "" &&
		c.functionCallNameString != "none" && c.functionCallNameString != "auto" {
		return c.functionCallNameString
	}

	return c.functionCallString
}

func (cfg *BackendConfig) SetDefaults(opts ...ConfigLoaderOption) {
	lo := &LoadOptions{}
	lo.Apply(opts...)

	ctx := lo.ctxSize
	threads := lo.threads
	f16 := lo.f16
	debug := lo.debug
	// https://github.com/ggerganov/llama.cpp/blob/75cd4c77292034ecec587ecb401366f57338f7c0/common/sampling.h#L22
	defaultTopP := 0.95
	defaultTopK := 40
	defaultTemp := 0.9
	defaultMirostat := 2
	defaultMirostatTAU := 5.0
	defaultMirostatETA := 0.1
	defaultTypicalP := 1.0
	defaultTFZ := 1.0
	defaultZero := 0

	// Try to offload all GPU layers (if GPU is found)
	defaultHigh := 99999999

	trueV := true
	falseV := false

	if cfg.Seed == nil {
		//  random number generator seed
		defaultSeed := RAND_SEED
		cfg.Seed = &defaultSeed
	}

	if cfg.TopK == nil {
		cfg.TopK = &defaultTopK
	}

	if cfg.TypicalP == nil {
		cfg.TypicalP = &defaultTypicalP
	}

	if cfg.TFZ == nil {
		cfg.TFZ = &defaultTFZ
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
		cfg.Maxtokens = &defaultZero
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
		cfg.NGPULayers = &defaultHigh
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
