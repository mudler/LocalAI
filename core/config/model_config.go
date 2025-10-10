package config

import (
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/functions"
	"gopkg.in/yaml.v3"
)

const (
	RAND_SEED = -1
)

type TTSConfig struct {

	// Voice wav path or id
	Voice string `yaml:"voice" json:"voice"`

	AudioPath string `yaml:"audio_path" json:"audio_path"`
}

// ModelConfig represents a model configuration
type ModelConfig struct {
	modelConfigFile          string `yaml:"-" json:"-"`
	schema.PredictionOptions `yaml:"parameters" json:"parameters"`
	Name                     string `yaml:"name" json:"name"`

	F16                 *bool                `yaml:"f16" json:"f16"`
	Threads             *int                 `yaml:"threads" json:"threads"`
	Debug               *bool                `yaml:"debug" json:"debug"`
	Roles               map[string]string    `yaml:"roles" json:"roles"`
	Embeddings          *bool                `yaml:"embeddings" json:"embeddings"`
	Backend             string               `yaml:"backend" json:"backend"`
	TemplateConfig      TemplateConfig       `yaml:"template" json:"template"`
	KnownUsecaseStrings []string             `yaml:"known_usecases" json:"known_usecases"`
	KnownUsecases       *ModelConfigUsecases `yaml:"-" json:"-"`
	Pipeline            Pipeline             `yaml:"pipeline" json:"pipeline"`

	PromptStrings, InputStrings                []string               `yaml:"-" json:"-"`
	InputToken                                 [][]int                `yaml:"-" json:"-"`
	functionCallString, functionCallNameString string                 `yaml:"-" json:"-"`
	ResponseFormat                             string                 `yaml:"-" json:"-"`
	ResponseFormatMap                          map[string]interface{} `yaml:"-" json:"-"`

	FunctionsConfig functions.FunctionsConfig `yaml:"function" json:"function"`

	FeatureFlag FeatureFlag `yaml:"feature_flags" json:"feature_flags"` // Feature Flag registry. We move fast, and features may break on a per model/backend basis. Registry for (usually temporary) flags that indicate aborting something early.
	// LLM configs (GPT4ALL, Llama.cpp, ...)
	LLMConfig `yaml:",inline" json:",inline"`

	// Diffusers
	Diffusers Diffusers `yaml:"diffusers" json:"diffusers"`
	Step      int       `yaml:"step" json:"step"`

	// GRPC Options
	GRPC GRPC `yaml:"grpc" json:"grpc"`

	// TTS specifics
	TTSConfig `yaml:"tts" json:"tts"`

	// CUDA
	// Explicitly enable CUDA or not (some backends might need it)
	CUDA bool `yaml:"cuda" json:"cuda"`

	DownloadFiles []File `yaml:"download_files" json:"download_files"`

	Description string `yaml:"description" json:"description"`
	Usage       string `yaml:"usage" json:"usage"`

	Options   []string `yaml:"options" json:"options"`
	Overrides []string `yaml:"overrides" json:"overrides"`

	MCP   MCPConfig   `yaml:"mcp" json:"mcp"`
	Agent AgentConfig `yaml:"agent" json:"agent"`
}

type MCPConfig struct {
	Servers string `yaml:"remote" json:"remote"`
	Stdio   string `yaml:"stdio" json:"stdio"`
}

type AgentConfig struct {
	MaxAttempts        int  `yaml:"max_attempts" json:"max_attempts"`
	MaxIterations      int  `yaml:"max_iterations" json:"max_iterations"`
	EnableReasoning    bool `yaml:"enable_reasoning" json:"enable_reasoning"`
	EnableReEvaluation bool `yaml:"enable_re_evaluation" json:"enable_re_evaluation"`
}

func (c *MCPConfig) MCPConfigFromYAML() (MCPGenericConfig[MCPRemoteServers], MCPGenericConfig[MCPSTDIOServers]) {
	var remote MCPGenericConfig[MCPRemoteServers]
	var stdio MCPGenericConfig[MCPSTDIOServers]

	if err := yaml.Unmarshal([]byte(c.Servers), &remote); err != nil {
		return remote, stdio
	}

	if err := yaml.Unmarshal([]byte(c.Stdio), &stdio); err != nil {
		return remote, stdio
	}

	return remote, stdio
}

type MCPGenericConfig[T any] struct {
	Servers T `yaml:"mcpServers" json:"mcpServers"`
}
type MCPRemoteServers map[string]MCPRemoteServer
type MCPSTDIOServers map[string]MCPSTDIOServer

type MCPRemoteServer struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

type MCPSTDIOServer struct {
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Command string            `json:"command"`
}

// Pipeline defines other models to use for audio-to-audio
type Pipeline struct {
	TTS           string `yaml:"tts" json:"tts"`
	LLM           string `yaml:"llm" json:"llm"`
	Transcription string `yaml:"transcription" json:"transcription"`
	VAD           string `yaml:"vad" json:"vad"`
}

type File struct {
	Filename string         `yaml:"filename" json:"filename"`
	SHA256   string         `yaml:"sha256" json:"sha256"`
	URI      downloader.URI `yaml:"uri" json:"uri"`
}

type FeatureFlag map[string]*bool

func (ff FeatureFlag) Enabled(s string) bool {
	if v, exists := ff[s]; exists && v != nil {
		return *v
	}
	return false
}

type GRPC struct {
	Attempts          int `yaml:"attempts" json:"attempts"`
	AttemptsSleepTime int `yaml:"attempts_sleep_time" json:"attempts_sleep_time"`
}

type Diffusers struct {
	CUDA             bool   `yaml:"cuda" json:"cuda"`
	PipelineType     string `yaml:"pipeline_type" json:"pipeline_type"`
	SchedulerType    string `yaml:"scheduler_type" json:"scheduler_type"`
	EnableParameters string `yaml:"enable_parameters" json:"enable_parameters"` // A list of comma separated parameters to specify
	IMG2IMG          bool   `yaml:"img2img" json:"img2img"`                     // Image to Image Diffuser
	ClipSkip         int    `yaml:"clip_skip" json:"clip_skip"`                 // Skip every N frames
	ClipModel        string `yaml:"clip_model" json:"clip_model"`               // Clip model to use
	ClipSubFolder    string `yaml:"clip_subfolder" json:"clip_subfolder"`       // Subfolder to use for clip model
	ControlNet       string `yaml:"control_net" json:"control_net"`
}

// LLMConfig is a struct that holds the configuration that are
// generic for most of the LLM backends.
type LLMConfig struct {
	SystemPrompt    string   `yaml:"system_prompt" json:"system_prompt"`
	TensorSplit     string   `yaml:"tensor_split" json:"tensor_split"`
	MainGPU         string   `yaml:"main_gpu" json:"main_gpu"`
	RMSNormEps      float32  `yaml:"rms_norm_eps" json:"rms_norm_eps"`
	NGQA            int32    `yaml:"ngqa" json:"ngqa"`
	PromptCachePath string   `yaml:"prompt_cache_path" json:"prompt_cache_path"`
	PromptCacheAll  bool     `yaml:"prompt_cache_all" json:"prompt_cache_all"`
	PromptCacheRO   bool     `yaml:"prompt_cache_ro" json:"prompt_cache_ro"`
	MirostatETA     *float64 `yaml:"mirostat_eta" json:"mirostat_eta"`
	MirostatTAU     *float64 `yaml:"mirostat_tau" json:"mirostat_tau"`
	Mirostat        *int     `yaml:"mirostat" json:"mirostat"`
	NGPULayers      *int     `yaml:"gpu_layers" json:"gpu_layers"`
	MMap            *bool    `yaml:"mmap" json:"mmap"`
	MMlock          *bool    `yaml:"mmlock" json:"mmlock"`
	LowVRAM         *bool    `yaml:"low_vram" json:"low_vram"`
	Reranking       *bool    `yaml:"reranking" json:"reranking"`
	Grammar         string   `yaml:"grammar" json:"grammar"`
	StopWords       []string `yaml:"stopwords" json:"stopwords"`
	Cutstrings      []string `yaml:"cutstrings" json:"cutstrings"`
	ExtractRegex    []string `yaml:"extract_regex" json:"extract_regex"`
	TrimSpace       []string `yaml:"trimspace" json:"trimspace"`
	TrimSuffix      []string `yaml:"trimsuffix" json:"trimsuffix"`

	ContextSize          *int             `yaml:"context_size" json:"context_size"`
	NUMA                 bool             `yaml:"numa" json:"numa"`
	LoraAdapter          string           `yaml:"lora_adapter" json:"lora_adapter"`
	LoraBase             string           `yaml:"lora_base" json:"lora_base"`
	LoraAdapters         []string         `yaml:"lora_adapters" json:"lora_adapters"`
	LoraScales           []float32        `yaml:"lora_scales" json:"lora_scales"`
	LoraScale            float32          `yaml:"lora_scale" json:"lora_scale"`
	NoMulMatQ            bool             `yaml:"no_mulmatq" json:"no_mulmatq"`
	DraftModel           string           `yaml:"draft_model" json:"draft_model"`
	NDraft               int32            `yaml:"n_draft" json:"n_draft"`
	Quantization         string           `yaml:"quantization" json:"quantization"`
	LoadFormat           string           `yaml:"load_format" json:"load_format"`
	GPUMemoryUtilization float32          `yaml:"gpu_memory_utilization" json:"gpu_memory_utilization"` // vLLM
	TrustRemoteCode      bool             `yaml:"trust_remote_code" json:"trust_remote_code"`           // vLLM
	EnforceEager         bool             `yaml:"enforce_eager" json:"enforce_eager"`                   // vLLM
	SwapSpace            int              `yaml:"swap_space" json:"swap_space"`                         // vLLM
	MaxModelLen          int              `yaml:"max_model_len" json:"max_model_len"`                   // vLLM
	TensorParallelSize   int              `yaml:"tensor_parallel_size" json:"tensor_parallel_size"`     // vLLM
	DisableLogStatus     bool             `yaml:"disable_log_stats" json:"disable_log_stats"`           // vLLM
	DType                string           `yaml:"dtype" json:"dtype"`                                   // vLLM
	LimitMMPerPrompt     LimitMMPerPrompt `yaml:"limit_mm_per_prompt" json:"limit_mm_per_prompt"`       // vLLM
	MMProj               string           `yaml:"mmproj" json:"mmproj"`

	FlashAttention *string `yaml:"flash_attention" json:"flash_attention"`
	NoKVOffloading bool    `yaml:"no_kv_offloading" json:"no_kv_offloading"`
	CacheTypeK     string  `yaml:"cache_type_k" json:"cache_type_k"`
	CacheTypeV     string  `yaml:"cache_type_v" json:"cache_type_v"`

	RopeScaling string `yaml:"rope_scaling" json:"rope_scaling"`
	ModelType   string `yaml:"type" json:"type"`

	YarnExtFactor  float32 `yaml:"yarn_ext_factor" json:"yarn_ext_factor"`
	YarnAttnFactor float32 `yaml:"yarn_attn_factor" json:"yarn_attn_factor"`
	YarnBetaFast   float32 `yaml:"yarn_beta_fast" json:"yarn_beta_fast"`
	YarnBetaSlow   float32 `yaml:"yarn_beta_slow" json:"yarn_beta_slow"`

	CFGScale float32 `yaml:"cfg_scale" json:"cfg_scale"` // Classifier-Free Guidance Scale
}

// LimitMMPerPrompt is a struct that holds the configuration for the limit-mm-per-prompt config in vLLM
type LimitMMPerPrompt struct {
	LimitImagePerPrompt int `yaml:"image" json:"image"`
	LimitVideoPerPrompt int `yaml:"video" json:"video"`
	LimitAudioPerPrompt int `yaml:"audio" json:"audio"`
}

// TemplateConfig is a struct that holds the configuration of the templating system
type TemplateConfig struct {
	// Chat is the template used in the chat completion endpoint
	Chat string `yaml:"chat" json:"chat"`

	// ChatMessage is the template used for chat messages
	ChatMessage string `yaml:"chat_message" json:"chat_message"`

	// Completion is the template used for completion requests
	Completion string `yaml:"completion" json:"completion"`

	// Edit is the template used for edit completion requests
	Edit string `yaml:"edit" json:"edit"`

	// Functions is the template used when tools are present in the client requests
	Functions string `yaml:"function" json:"function"`

	// UseTokenizerTemplate is a flag that indicates if the tokenizer template should be used.
	// Note: this is mostly consumed for backends such as vllm and transformers
	// that can use the tokenizers specified in the JSON config files of the models
	UseTokenizerTemplate bool `yaml:"use_tokenizer_template" json:"use_tokenizer_template"`

	// JoinChatMessagesByCharacter is a string that will be used to join chat messages together.
	// It defaults to \n
	JoinChatMessagesByCharacter *string `yaml:"join_chat_messages_by_character" json:"join_chat_messages_by_character"`

	Multimodal string `yaml:"multimodal" json:"multimodal"`

	JinjaTemplate bool `yaml:"jinja_template" json:"jinja_template"`

	ReplyPrefix string `yaml:"reply_prefix" json:"reply_prefix"`
}

func (c *ModelConfig) UnmarshalYAML(value *yaml.Node) error {
	type BCAlias ModelConfig
	var aux BCAlias
	if err := value.Decode(&aux); err != nil {
		return err
	}
	*c = ModelConfig(aux)

	c.KnownUsecases = GetUsecasesFromYAML(c.KnownUsecaseStrings)
	// Make sure the usecases are valid, we rewrite with what we identified
	c.KnownUsecaseStrings = []string{}
	for k, usecase := range GetAllModelConfigUsecases() {
		if c.HasUsecases(usecase) {
			c.KnownUsecaseStrings = append(c.KnownUsecaseStrings, k)
		}
	}
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
}

func (c *ModelConfig) Validate() bool {
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
			return false
		}
	}

	if c.Backend != "" {
		// a regex that checks that is a string name with no special characters, except '-' and '_'
		re := regexp.MustCompile(`^[a-zA-Z0-9-_]+$`)
		return re.MatchString(c.Backend)
	}

	return true
}

func (c *ModelConfig) HasTemplate() bool {
	return c.TemplateConfig.Completion != "" || c.TemplateConfig.Edit != "" || c.TemplateConfig.Chat != "" || c.TemplateConfig.ChatMessage != ""
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
		"FLAG_ANY":              FLAG_ANY,
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
		if c.TemplateConfig.Chat == "" && c.TemplateConfig.ChatMessage == "" {
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
		ttsBackends := []string{"bark-cpp", "piper", "transformers-musicgen"}
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
