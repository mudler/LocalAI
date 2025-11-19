package config

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/cogito"
	"gopkg.in/yaml.v3"
)

const (
	RAND_SEED = -1
)

// @Description TTS configuration
type TTSConfig struct {

	// Voice wav path or id
	Voice string `yaml:"voice,omitempty" json:"voice,omitempty"`

	AudioPath string `yaml:"audio_path,omitempty" json:"audio_path,omitempty"`
}

// @Description ModelConfig represents a model configuration
type ModelConfig struct {
	modelConfigFile          string `yaml:"-" json:"-"`
	schema.PredictionOptions `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	Name                     string `yaml:"name,omitempty" json:"name,omitempty"`

	F16                 *bool                `yaml:"f16,omitempty" json:"f16,omitempty"`
	Threads             *int                 `yaml:"threads,omitempty" json:"threads,omitempty"`
	Debug               *bool                `yaml:"debug,omitempty" json:"debug,omitempty"`
	Roles               map[string]string    `yaml:"roles,omitempty" json:"roles,omitempty"`
	Embeddings          *bool                `yaml:"embeddings,omitempty" json:"embeddings,omitempty"`
	Backend             string               `yaml:"backend,omitempty" json:"backend,omitempty"`
	TemplateConfig      TemplateConfig       `yaml:"template,omitempty" json:"template,omitempty"`
	KnownUsecaseStrings []string             `yaml:"known_usecases,omitempty" json:"known_usecases,omitempty"`
	KnownUsecases       *ModelConfigUsecases `yaml:"-" json:"-"`
	Pipeline            Pipeline             `yaml:"pipeline,omitempty" json:"pipeline,omitempty"`

	PromptStrings, InputStrings                []string               `yaml:"-" json:"-"`
	InputToken                                 [][]int                `yaml:"-" json:"-"`
	functionCallString, functionCallNameString string                 `yaml:"-" json:"-"`
	ResponseFormat                             string                 `yaml:"-" json:"-"`
	ResponseFormatMap                          map[string]interface{} `yaml:"-" json:"-"`

	FunctionsConfig functions.FunctionsConfig `yaml:"function,omitempty" json:"function,omitempty"`

	FeatureFlag FeatureFlag `yaml:"feature_flags,omitempty" json:"feature_flags,omitempty"` // Feature Flag registry. We move fast, and features may break on a per model/backend basis. Registry for (usually temporary) flags that indicate aborting something early.
	// LLM configs (GPT4ALL, Llama.cpp, ...)
	LLMConfig `yaml:",inline" json:",inline"`

	// Diffusers
	Diffusers Diffusers `yaml:"diffusers,omitempty" json:"diffusers,omitempty"`
	Step      int       `yaml:"step,omitempty" json:"step,omitempty"`

	// GRPC Options
	GRPC GRPC `yaml:"grpc,omitempty" json:"grpc,omitempty"`

	// TTS specifics
	TTSConfig `yaml:"tts,omitempty" json:"tts,omitempty"`

	// CUDA
	// Explicitly enable CUDA or not (some backends might need it)
	CUDA bool `yaml:"cuda,omitempty" json:"cuda,omitempty"`

	DownloadFiles []File `yaml:"download_files,omitempty" json:"download_files,omitempty"`

	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Usage       string `yaml:"usage,omitempty" json:"usage,omitempty"`

	Options   []string `yaml:"options,omitempty" json:"options,omitempty"`
	Overrides []string `yaml:"overrides,omitempty" json:"overrides,omitempty"`

	MCP   MCPConfig   `yaml:"mcp,omitempty" json:"mcp,omitempty"`
	Agent AgentConfig `yaml:"agent,omitempty" json:"agent,omitempty"`
}

// @Description MCP configuration
type MCPConfig struct {
	Servers string `yaml:"remote,omitempty" json:"remote,omitempty"`
	Stdio   string `yaml:"stdio,omitempty" json:"stdio,omitempty"`
}

// @Description Agent configuration
type AgentConfig struct {
	MaxAttempts           int  `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty"`
	MaxIterations         int  `yaml:"max_iterations,omitempty" json:"max_iterations,omitempty"`
	EnableReasoning       bool `yaml:"enable_reasoning,omitempty" json:"enable_reasoning,omitempty"`
	EnablePlanning        bool `yaml:"enable_planning,omitempty" json:"enable_planning,omitempty"`
	EnableMCPPrompts      bool `yaml:"enable_mcp_prompts,omitempty" json:"enable_mcp_prompts,omitempty"`
	EnablePlanReEvaluator bool `yaml:"enable_plan_re_evaluator,omitempty" json:"enable_plan_re_evaluator,omitempty"`
}

func (c *MCPConfig) MCPConfigFromYAML() (MCPGenericConfig[MCPRemoteServers], MCPGenericConfig[MCPSTDIOServers], error) {
	var remote MCPGenericConfig[MCPRemoteServers]
	var stdio MCPGenericConfig[MCPSTDIOServers]

	if err := yaml.Unmarshal([]byte(c.Servers), &remote); err != nil {
		return remote, stdio, err
	}

	if err := yaml.Unmarshal([]byte(c.Stdio), &stdio); err != nil {
		return remote, stdio, err
	}
	return remote, stdio, nil
}

// @Description MCP generic configuration
type MCPGenericConfig[T any] struct {
	Servers T `yaml:"mcpServers,omitempty" json:"mcpServers,omitempty"`
}
type MCPRemoteServers map[string]MCPRemoteServer
type MCPSTDIOServers map[string]MCPSTDIOServer

// @Description MCP remote server configuration
type MCPRemoteServer struct {
	URL   string `json:"url,omitempty"`
	Token string `json:"token,omitempty"`
}

// @Description MCP STDIO server configuration
type MCPSTDIOServer struct {
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Command string            `json:"command,omitempty"`
}

// @Description Pipeline defines other models to use for audio-to-audio
type Pipeline struct {
	TTS           string `yaml:"tts,omitempty" json:"tts,omitempty"`
	LLM           string `yaml:"llm,omitempty" json:"llm,omitempty"`
	Transcription string `yaml:"transcription,omitempty" json:"transcription,omitempty"`
	VAD           string `yaml:"vad,omitempty" json:"vad,omitempty"`
}

// @Description File configuration for model downloads
type File struct {
	Filename string         `yaml:"filename,omitempty" json:"filename,omitempty"`
	SHA256   string         `yaml:"sha256,omitempty" json:"sha256,omitempty"`
	URI      downloader.URI `yaml:"uri,omitempty" json:"uri,omitempty"`
}

type FeatureFlag map[string]*bool

func (ff FeatureFlag) Enabled(s string) bool {
	if v, exists := ff[s]; exists && v != nil {
		return *v
	}
	return false
}

// @Description GRPC configuration
type GRPC struct {
	Attempts          int `yaml:"attempts,omitempty" json:"attempts,omitempty"`
	AttemptsSleepTime int `yaml:"attempts_sleep_time,omitempty" json:"attempts_sleep_time,omitempty"`
}

// @Description Diffusers configuration
type Diffusers struct {
	CUDA             bool   `yaml:"cuda,omitempty" json:"cuda,omitempty"`
	PipelineType     string `yaml:"pipeline_type,omitempty" json:"pipeline_type,omitempty"`
	SchedulerType    string `yaml:"scheduler_type,omitempty" json:"scheduler_type,omitempty"`
	EnableParameters string `yaml:"enable_parameters,omitempty" json:"enable_parameters,omitempty"` // A list of comma separated parameters to specify
	IMG2IMG          bool   `yaml:"img2img,omitempty" json:"img2img,omitempty"`                     // Image to Image Diffuser
	ClipSkip         int    `yaml:"clip_skip,omitempty" json:"clip_skip,omitempty"`                 // Skip every N frames
	ClipModel        string `yaml:"clip_model,omitempty" json:"clip_model,omitempty"`               // Clip model to use
	ClipSubFolder    string `yaml:"clip_subfolder,omitempty" json:"clip_subfolder,omitempty"`       // Subfolder to use for clip model
	ControlNet       string `yaml:"control_net,omitempty" json:"control_net,omitempty"`
}

// @Description LLMConfig is a struct that holds the configuration that are generic for most of the LLM backends.
type LLMConfig struct {
	SystemPrompt    string   `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
	TensorSplit     string   `yaml:"tensor_split,omitempty" json:"tensor_split,omitempty"`
	MainGPU         string   `yaml:"main_gpu,omitempty" json:"main_gpu,omitempty"`
	RMSNormEps      float32  `yaml:"rms_norm_eps,omitempty" json:"rms_norm_eps,omitempty"`
	NGQA            int32    `yaml:"ngqa,omitempty" json:"ngqa,omitempty"`
	PromptCachePath string   `yaml:"prompt_cache_path,omitempty" json:"prompt_cache_path,omitempty"`
	PromptCacheAll  bool     `yaml:"prompt_cache_all,omitempty" json:"prompt_cache_all,omitempty"`
	PromptCacheRO   bool     `yaml:"prompt_cache_ro,omitempty" json:"prompt_cache_ro,omitempty"`
	MirostatETA     *float64 `yaml:"mirostat_eta,omitempty" json:"mirostat_eta,omitempty"`
	MirostatTAU     *float64 `yaml:"mirostat_tau,omitempty" json:"mirostat_tau,omitempty"`
	Mirostat        *int     `yaml:"mirostat,omitempty" json:"mirostat,omitempty"`
	NGPULayers      *int     `yaml:"gpu_layers,omitempty" json:"gpu_layers,omitempty"`
	MMap            *bool    `yaml:"mmap,omitempty" json:"mmap,omitempty"`
	MMlock          *bool    `yaml:"mmlock,omitempty" json:"mmlock,omitempty"`
	LowVRAM         *bool    `yaml:"low_vram,omitempty" json:"low_vram,omitempty"`
	Reranking       *bool    `yaml:"reranking,omitempty" json:"reranking,omitempty"`
	Grammar         string   `yaml:"grammar,omitempty" json:"grammar,omitempty"`
	StopWords       []string `yaml:"stopwords,omitempty" json:"stopwords,omitempty"`
	Cutstrings      []string `yaml:"cutstrings,omitempty" json:"cutstrings,omitempty"`
	ExtractRegex    []string `yaml:"extract_regex,omitempty" json:"extract_regex,omitempty"`
	TrimSpace       []string `yaml:"trimspace,omitempty" json:"trimspace,omitempty"`
	TrimSuffix      []string `yaml:"trimsuffix,omitempty" json:"trimsuffix,omitempty"`

	ContextSize          *int             `yaml:"context_size,omitempty" json:"context_size,omitempty"`
	NUMA                 bool             `yaml:"numa,omitempty" json:"numa,omitempty"`
	LoraAdapter          string           `yaml:"lora_adapter,omitempty" json:"lora_adapter,omitempty"`
	LoraBase             string           `yaml:"lora_base,omitempty" json:"lora_base,omitempty"`
	LoraAdapters         []string         `yaml:"lora_adapters,omitempty" json:"lora_adapters,omitempty"`
	LoraScales           []float32        `yaml:"lora_scales,omitempty" json:"lora_scales,omitempty"`
	LoraScale            float32          `yaml:"lora_scale,omitempty" json:"lora_scale,omitempty"`
	NoMulMatQ            bool             `yaml:"no_mulmatq,omitempty" json:"no_mulmatq,omitempty"`
	DraftModel           string           `yaml:"draft_model,omitempty" json:"draft_model,omitempty"`
	NDraft               int32            `yaml:"n_draft,omitempty" json:"n_draft,omitempty"`
	Quantization         string           `yaml:"quantization,omitempty" json:"quantization,omitempty"`
	LoadFormat           string           `yaml:"load_format,omitempty" json:"load_format,omitempty"`
	GPUMemoryUtilization float32          `yaml:"gpu_memory_utilization,omitempty" json:"gpu_memory_utilization,omitempty"` // vLLM
	TrustRemoteCode      bool             `yaml:"trust_remote_code,omitempty" json:"trust_remote_code,omitempty"`           // vLLM
	EnforceEager         bool             `yaml:"enforce_eager,omitempty" json:"enforce_eager,omitempty"`                   // vLLM
	SwapSpace            int              `yaml:"swap_space,omitempty" json:"swap_space,omitempty"`                         // vLLM
	MaxModelLen          int              `yaml:"max_model_len,omitempty" json:"max_model_len,omitempty"`                   // vLLM
	TensorParallelSize   int              `yaml:"tensor_parallel_size,omitempty" json:"tensor_parallel_size,omitempty"`     // vLLM
	DisableLogStatus     bool             `yaml:"disable_log_stats,omitempty" json:"disable_log_stats,omitempty"`           // vLLM
	DType                string           `yaml:"dtype,omitempty" json:"dtype,omitempty"`                                   // vLLM
	LimitMMPerPrompt     LimitMMPerPrompt `yaml:"limit_mm_per_prompt,omitempty" json:"limit_mm_per_prompt,omitempty"`       // vLLM
	MMProj               string           `yaml:"mmproj,omitempty" json:"mmproj,omitempty"`

	FlashAttention *string `yaml:"flash_attention,omitempty" json:"flash_attention,omitempty"`
	NoKVOffloading bool    `yaml:"no_kv_offloading,omitempty" json:"no_kv_offloading,omitempty"`
	CacheTypeK     string  `yaml:"cache_type_k,omitempty" json:"cache_type_k,omitempty"`
	CacheTypeV     string  `yaml:"cache_type_v,omitempty" json:"cache_type_v,omitempty"`

	RopeScaling string `yaml:"rope_scaling,omitempty" json:"rope_scaling,omitempty"`
	ModelType   string `yaml:"type,omitempty" json:"type,omitempty"`

	YarnExtFactor  float32 `yaml:"yarn_ext_factor,omitempty" json:"yarn_ext_factor,omitempty"`
	YarnAttnFactor float32 `yaml:"yarn_attn_factor,omitempty" json:"yarn_attn_factor,omitempty"`
	YarnBetaFast   float32 `yaml:"yarn_beta_fast,omitempty" json:"yarn_beta_fast,omitempty"`
	YarnBetaSlow   float32 `yaml:"yarn_beta_slow,omitempty" json:"yarn_beta_slow,omitempty"`

	CFGScale float32 `yaml:"cfg_scale,omitempty" json:"cfg_scale,omitempty"` // Classifier-Free Guidance Scale
}

// @Description LimitMMPerPrompt is a struct that holds the configuration for the limit-mm-per-prompt config in vLLM
type LimitMMPerPrompt struct {
	LimitImagePerPrompt int `yaml:"image,omitempty" json:"image,omitempty"`
	LimitVideoPerPrompt int `yaml:"video,omitempty" json:"video,omitempty"`
	LimitAudioPerPrompt int `yaml:"audio,omitempty" json:"audio,omitempty"`
}

// @Description TemplateConfig is a struct that holds the configuration of the templating system
type TemplateConfig struct {
	// Chat is the template used in the chat completion endpoint
	Chat string `yaml:"chat,omitempty" json:"chat,omitempty"`

	// ChatMessage is the template used for chat messages
	ChatMessage string `yaml:"chat_message,omitempty" json:"chat_message,omitempty"`

	// Completion is the template used for completion requests
	Completion string `yaml:"completion,omitempty" json:"completion,omitempty"`

	// Edit is the template used for edit completion requests
	Edit string `yaml:"edit,omitempty" json:"edit,omitempty"`

	// Functions is the template used when tools are present in the client requests
	Functions string `yaml:"function,omitempty" json:"function,omitempty"`

	// UseTokenizerTemplate is a flag that indicates if the tokenizer template should be used.
	// Note: this is mostly consumed for backends such as vllm and transformers
	// that can use the tokenizers specified in the JSON config files of the models
	UseTokenizerTemplate bool `yaml:"use_tokenizer_template,omitempty" json:"use_tokenizer_template,omitempty"`

	// JoinChatMessagesByCharacter is a string that will be used to join chat messages together.
	// It defaults to \n
	JoinChatMessagesByCharacter *string `yaml:"join_chat_messages_by_character,omitempty" json:"join_chat_messages_by_character,omitempty"`

	Multimodal string `yaml:"multimodal,omitempty" json:"multimodal,omitempty"`

	ReplyPrefix string `yaml:"reply_prefix,omitempty" json:"reply_prefix,omitempty"`
}

func (c *ModelConfig) syncKnownUsecasesFromString() {
	c.KnownUsecases = GetUsecasesFromYAML(c.KnownUsecaseStrings)
	// Make sure the usecases are valid, we rewrite with what we identified
	c.KnownUsecaseStrings = []string{}
	for k, usecase := range GetAllModelConfigUsecases() {
		if c.HasUsecases(usecase) {
			c.KnownUsecaseStrings = append(c.KnownUsecaseStrings, k)
		}
	}
}

func (c *ModelConfig) UnmarshalYAML(value *yaml.Node) error {
	type BCAlias ModelConfig
	var aux BCAlias
	if err := value.Decode(&aux); err != nil {
		return err
	}
	*c = ModelConfig(aux)

	c.syncKnownUsecasesFromString()
	return nil
}

func (c *ModelConfig) SetFunctionCallString(s string) {
	c.functionCallString = s
}

func (c *ModelConfig) SetFunctionCallNameString(s string) {
	c.functionCallNameString = s
}

func (c *ModelConfig) ShouldUseFunctions() bool {
	return ((c.functionCallString != "none" || c.functionCallString == "") || c.ShouldCallSpecificFunction())
}

func (c *ModelConfig) ShouldCallSpecificFunction() bool {
	return len(c.functionCallNameString) > 0
}

// MMProjFileName returns the filename of the MMProj file
// If the MMProj is a URL, it will return the MD5 of the URL which is the filename
func (c *ModelConfig) MMProjFileName() string {
	uri := downloader.URI(c.MMProj)
	if uri.LooksLikeURL() {
		f, _ := uri.FilenameFromUrl()
		return f
	}

	return c.MMProj
}

func (c *ModelConfig) IsMMProjURL() bool {
	uri := downloader.URI(c.MMProj)
	return uri.LooksLikeURL()
}

func (c *ModelConfig) IsModelURL() bool {
	uri := downloader.URI(c.Model)
	return uri.LooksLikeURL()
}

// ModelFileName returns the filename of the model
// If the model is a URL, it will return the MD5 of the URL which is the filename
func (c *ModelConfig) ModelFileName() string {
	uri := downloader.URI(c.Model)
	if uri.LooksLikeURL() {
		f, _ := uri.FilenameFromUrl()
		return f
	}

	return c.Model
}

func (c *ModelConfig) FunctionToCall() string {
	if c.functionCallNameString != "" &&
		c.functionCallNameString != "none" && c.functionCallNameString != "auto" {
		return c.functionCallNameString
	}

	return c.functionCallString
}

func (cfg *ModelConfig) SetDefaults(opts ...ConfigLoaderOption) {
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
	// https://github.com/mudler/LocalAI/issues/2780
	defaultMirostat := 0
	defaultMirostatTAU := 5.0
	defaultMirostatETA := 0.1
	defaultTypicalP := 1.0
	defaultTFZ := 1.0
	defaultZero := 0

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

		// Only exception is for Intel GPUs
		if os.Getenv("XPU") != "" {
			cfg.MMap = &falseV
		} else {
			cfg.MMap = &trueV
		}
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

	if cfg.LowVRAM == nil {
		cfg.LowVRAM = &falseV
	}

	if cfg.Embeddings == nil {
		cfg.Embeddings = &falseV
	}

	if cfg.Reranking == nil {
		cfg.Reranking = &falseV
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

	guessDefaultsFromFile(cfg, lo.modelPath, ctx)
	cfg.syncKnownUsecasesFromString()
}

func (c *ModelConfig) Validate() (bool, error) {
	downloadedFileNames := []string{}
	for _, f := range c.DownloadFiles {
		downloadedFileNames = append(downloadedFileNames, f.Filename)
	}
	validationTargets := []string{c.Backend, c.Model, c.MMProj}
	validationTargets = append(validationTargets, downloadedFileNames...)
	// Simple validation to make sure the model can be correctly loaded
	for _, n := range validationTargets {
		if n == "" {
			continue
		}
		if strings.HasPrefix(n, string(os.PathSeparator)) ||
			strings.Contains(n, "..") {
			return false, fmt.Errorf("invalid file path: %s", n)
		}
	}

	if c.Backend != "" {
		// a regex that checks that is a string name with no special characters, except '-' and '_'
		re := regexp.MustCompile(`^[a-zA-Z0-9-_]+$`)
		if !re.MatchString(c.Backend) {
			return false, fmt.Errorf("invalid backend name: %s", c.Backend)
		}
		return true, nil
	}

	return true, nil
}

func (c *ModelConfig) HasTemplate() bool {
	return c.TemplateConfig.Completion != "" || c.TemplateConfig.Edit != "" || c.TemplateConfig.Chat != "" || c.TemplateConfig.ChatMessage != "" || c.TemplateConfig.UseTokenizerTemplate
}

func (c *ModelConfig) GetModelConfigFile() string {
	return c.modelConfigFile
}

type ModelConfigUsecases int

const (
	FLAG_ANY              ModelConfigUsecases = 0b000000000000
	FLAG_CHAT             ModelConfigUsecases = 0b000000000001
	FLAG_COMPLETION       ModelConfigUsecases = 0b000000000010
	FLAG_EDIT             ModelConfigUsecases = 0b000000000100
	FLAG_EMBEDDINGS       ModelConfigUsecases = 0b000000001000
	FLAG_RERANK           ModelConfigUsecases = 0b000000010000
	FLAG_IMAGE            ModelConfigUsecases = 0b000000100000
	FLAG_TRANSCRIPT       ModelConfigUsecases = 0b000001000000
	FLAG_TTS              ModelConfigUsecases = 0b000010000000
	FLAG_SOUND_GENERATION ModelConfigUsecases = 0b000100000000
	FLAG_TOKENIZE         ModelConfigUsecases = 0b001000000000
	FLAG_VAD              ModelConfigUsecases = 0b010000000000
	FLAG_VIDEO            ModelConfigUsecases = 0b100000000000
	FLAG_DETECTION        ModelConfigUsecases = 0b1000000000000

	// Common Subsets
	FLAG_LLM ModelConfigUsecases = FLAG_CHAT | FLAG_COMPLETION | FLAG_EDIT
)

func GetAllModelConfigUsecases() map[string]ModelConfigUsecases {
	return map[string]ModelConfigUsecases{
		// Note: FLAG_ANY is intentionally excluded from this map
		// because it's 0 and would always match in HasUsecases checks
		"FLAG_CHAT":             FLAG_CHAT,
		"FLAG_COMPLETION":       FLAG_COMPLETION,
		"FLAG_EDIT":             FLAG_EDIT,
		"FLAG_EMBEDDINGS":       FLAG_EMBEDDINGS,
		"FLAG_RERANK":           FLAG_RERANK,
		"FLAG_IMAGE":            FLAG_IMAGE,
		"FLAG_TRANSCRIPT":       FLAG_TRANSCRIPT,
		"FLAG_TTS":              FLAG_TTS,
		"FLAG_SOUND_GENERATION": FLAG_SOUND_GENERATION,
		"FLAG_TOKENIZE":         FLAG_TOKENIZE,
		"FLAG_VAD":              FLAG_VAD,
		"FLAG_LLM":              FLAG_LLM,
		"FLAG_VIDEO":            FLAG_VIDEO,
		"FLAG_DETECTION":        FLAG_DETECTION,
	}
}

func stringToFlag(s string) string {
	return "FLAG_" + strings.ToUpper(s)
}

func GetUsecasesFromYAML(input []string) *ModelConfigUsecases {
	if len(input) == 0 {
		return nil
	}
	result := FLAG_ANY
	flags := GetAllModelConfigUsecases()
	for _, str := range input {
		flag, exists := flags[stringToFlag(str)]
		if exists {
			result |= flag
		}
	}
	return &result
}

// HasUsecases examines a ModelConfig and determines which endpoints have a chance of success.
func (c *ModelConfig) HasUsecases(u ModelConfigUsecases) bool {
	if (c.KnownUsecases != nil) && ((u & *c.KnownUsecases) == u) {
		return true
	}
	return c.GuessUsecases(u)
}

// GuessUsecases is a **heuristic based** function, as the backend in question may not be loaded yet, and the config may not record what it's useful at.
// In its current state, this function should ideally check for properties of the config like templates, rather than the direct backend name checks for the lower half.
// This avoids the maintenance burden of updating this list for each new backend - but unfortunately, that's the best option for some services currently.
func (c *ModelConfig) GuessUsecases(u ModelConfigUsecases) bool {
	if (u & FLAG_CHAT) == FLAG_CHAT {
		if c.TemplateConfig.Chat == "" && c.TemplateConfig.ChatMessage == "" && !c.TemplateConfig.UseTokenizerTemplate {
			return false
		}
	}
	if (u & FLAG_COMPLETION) == FLAG_COMPLETION {
		if c.TemplateConfig.Completion == "" {
			return false
		}
	}
	if (u & FLAG_EDIT) == FLAG_EDIT {
		if c.TemplateConfig.Edit == "" {
			return false
		}
	}
	if (u & FLAG_EMBEDDINGS) == FLAG_EMBEDDINGS {
		if c.Embeddings == nil || !*c.Embeddings {
			return false
		}
	}
	if (u & FLAG_IMAGE) == FLAG_IMAGE {
		imageBackends := []string{"diffusers", "stablediffusion", "stablediffusion-ggml"}
		if !slices.Contains(imageBackends, c.Backend) {
			return false
		}

		if c.Backend == "diffusers" && c.Diffusers.PipelineType == "" {
			return false
		}

	}
	if (u & FLAG_VIDEO) == FLAG_VIDEO {
		videoBackends := []string{"diffusers", "stablediffusion"}
		if !slices.Contains(videoBackends, c.Backend) {
			return false
		}

		if c.Backend == "diffusers" && c.Diffusers.PipelineType == "" {
			return false
		}

	}
	if (u & FLAG_RERANK) == FLAG_RERANK {
		if c.Backend != "rerankers" {
			return false
		}
	}
	if (u & FLAG_TRANSCRIPT) == FLAG_TRANSCRIPT {
		if c.Backend != "whisper" {
			return false
		}
	}
	if (u & FLAG_TTS) == FLAG_TTS {
		ttsBackends := []string{"bark-cpp", "piper", "transformers-musicgen", "kokoro"}
		if !slices.Contains(ttsBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_DETECTION) == FLAG_DETECTION {
		if c.Backend != "rfdetr" {
			return false
		}
	}

	if (u & FLAG_SOUND_GENERATION) == FLAG_SOUND_GENERATION {
		if c.Backend != "transformers-musicgen" {
			return false
		}
	}

	if (u & FLAG_TOKENIZE) == FLAG_TOKENIZE {
		tokenizeCapableBackends := []string{"llama.cpp", "rwkv"}
		if !slices.Contains(tokenizeCapableBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_VAD) == FLAG_VAD {
		if c.Backend != "silero-vad" {
			return false
		}
	}

	return true
}

// BuildCogitoOptions generates cogito options from the model configuration
// It accepts a context, MCP sessions, and optional callback functions for status, reasoning, tool calls, and tool results
func (c *ModelConfig) BuildCogitoOptions() []cogito.Option {
	cogitoOpts := []cogito.Option{
		cogito.WithIterations(3),  // default to 3 iterations
		cogito.WithMaxAttempts(3), // default to 3 attempts
		cogito.WithForceReasoning(),
	}

	// Apply agent configuration options
	if c.Agent.EnableReasoning {
		cogitoOpts = append(cogitoOpts, cogito.EnableToolReasoner)
	}

	if c.Agent.EnablePlanning {
		cogitoOpts = append(cogitoOpts, cogito.EnableAutoPlan)
	}

	if c.Agent.EnableMCPPrompts {
		cogitoOpts = append(cogitoOpts, cogito.EnableMCPPrompts)
	}

	if c.Agent.EnablePlanReEvaluator {
		cogitoOpts = append(cogitoOpts, cogito.EnableAutoPlanReEvaluator)
	}

	if c.Agent.MaxIterations != 0 {
		cogitoOpts = append(cogitoOpts, cogito.WithIterations(c.Agent.MaxIterations))
	}

	if c.Agent.MaxAttempts != 0 {
		cogitoOpts = append(cogitoOpts, cogito.WithMaxAttempts(c.Agent.MaxAttempts))
	}

	return cogitoOpts
}
