package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/downloader"
	"github.com/go-skynet/LocalAI/pkg/functions"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

const (
	RAND_SEED = -1
)

type TTSConfig struct {

	// Voice wav path or id
	Voice string `yaml:"voice"`

	// Vall-e-x
	VallE VallE `yaml:"vall-e"`
}

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

	PromptStrings, InputStrings                []string               `yaml:"-"`
	InputToken                                 [][]int                `yaml:"-"`
	functionCallString, functionCallNameString string                 `yaml:"-"`
	ResponseFormat                             string                 `yaml:"-"`
	ResponseFormatMap                          map[string]interface{} `yaml:"-"`

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

	// TTS specifics
	TTSConfig `yaml:"tts"`

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

// LLMConfig is a struct that holds the configuration that are
// generic for most of the LLM backends.
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

	FlashAttention bool `yaml:"flash_attention"`
	NoKVOffloading bool `yaml:"no_kv_offloading"`

	RopeScaling string `yaml:"rope_scaling"`
	ModelType   string `yaml:"type"`

	YarnExtFactor  float32 `yaml:"yarn_ext_factor"`
	YarnAttnFactor float32 `yaml:"yarn_attn_factor"`
	YarnBetaFast   float32 `yaml:"yarn_beta_fast"`
	YarnBetaSlow   float32 `yaml:"yarn_beta_slow"`
}

// AutoGPTQ is a struct that holds the configuration specific to the AutoGPTQ backend
type AutoGPTQ struct {
	ModelBaseName    string `yaml:"model_base_name"`
	Device           string `yaml:"device"`
	Triton           bool   `yaml:"triton"`
	UseFastTokenizer bool   `yaml:"use_fast_tokenizer"`
}

// TemplateConfig is a struct that holds the configuration of the templating system
type TemplateConfig struct {
	// Chat is the template used in the chat completion endpoint
	Chat string `yaml:"chat"`

	// ChatMessage is the template used for chat messages
	ChatMessage string `yaml:"chat_message"`

	// Completion is the template used for completion requests
	Completion string `yaml:"completion"`

	// Edit is the template used for edit completion requests
	Edit string `yaml:"edit"`

	// Functions is the template used when tools are present in the client requests
	Functions string `yaml:"function"`

	// UseTokenizerTemplate is a flag that indicates if the tokenizer template should be used.
	// Note: this is mostly consumed for backends such as vllm and transformers
	// that can use the tokenizers specified in the JSON config files of the models
	UseTokenizerTemplate bool `yaml:"use_tokenizer_template"`

	// JoinChatMessagesByCharacter is a string that will be used to join chat messages together.
	// It defaults to \n
	JoinChatMessagesByCharacter *string `yaml:"join_chat_messages_by_character"`
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

// MMProjFileName returns the filename of the MMProj file
// If the MMProj is a URL, it will return the MD5 of the URL which is the filename
func (c *BackendConfig) MMProjFileName() string {
	modelURL := downloader.ConvertURL(c.MMProj)
	if downloader.LooksLikeURL(modelURL) {
		return utils.MD5(modelURL)
	}

	return c.MMProj
}

func (c *BackendConfig) IsMMProjURL() bool {
	return downloader.LooksLikeURL(downloader.ConvertURL(c.MMProj))
}

func (c *BackendConfig) IsModelURL() bool {
	return downloader.LooksLikeURL(downloader.ConvertURL(c.Model))
}

// ModelFileName returns the filename of the model
// If the model is a URL, it will return the MD5 of the URL which is the filename
func (c *BackendConfig) ModelFileName() string {
	modelURL := downloader.ConvertURL(c.Model)
	if downloader.LooksLikeURL(modelURL) {
		return utils.MD5(modelURL)
	}

	return c.Model
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

	guessDefaultsFromFile(cfg, lo.modelPath)
}

func (config *BackendConfig) UpdateFromOpenAIRequest(input *schema.OpenAIRequest) {
	if input.Echo {
		config.Echo = input.Echo
	}
	if input.TopK != nil {
		config.TopK = input.TopK
	}
	if input.TopP != nil {
		config.TopP = input.TopP
	}

	if input.Backend != "" {
		config.Backend = input.Backend
	}

	if input.ClipSkip != 0 {
		config.Diffusers.ClipSkip = input.ClipSkip
	}

	if input.ModelBaseName != "" {
		config.AutoGPTQ.ModelBaseName = input.ModelBaseName
	}

	if input.NegativePromptScale != 0 {
		config.NegativePromptScale = input.NegativePromptScale
	}

	if input.UseFastTokenizer {
		config.UseFastTokenizer = input.UseFastTokenizer
	}

	if input.NegativePrompt != "" {
		config.NegativePrompt = input.NegativePrompt
	}

	if input.RopeFreqBase != 0 {
		config.RopeFreqBase = input.RopeFreqBase
	}

	if input.RopeFreqScale != 0 {
		config.RopeFreqScale = input.RopeFreqScale
	}

	if input.Grammar != "" {
		config.Grammar = input.Grammar
	}

	if input.Temperature != nil {
		config.Temperature = input.Temperature
	}

	if input.Maxtokens != nil {
		config.Maxtokens = input.Maxtokens
	}

	if input.ResponseFormat != nil {
		switch responseFormat := input.ResponseFormat.(type) {
		case string:
			config.ResponseFormat = responseFormat
		case map[string]interface{}:
			config.ResponseFormatMap = responseFormat
		}
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

	if len(input.Tools) > 0 {
		for _, tool := range input.Tools {
			input.Functions = append(input.Functions, tool.Function)
		}
	}

	if input.ToolsChoice != nil {
		var toolChoice functions.Tool

		switch content := input.ToolsChoice.(type) {
		case string:
			_ = json.Unmarshal([]byte(content), &toolChoice)
		case map[string]interface{}:
			dat, _ := json.Marshal(content)
			_ = json.Unmarshal(dat, &toolChoice)
		}
		input.FunctionCall = map[string]interface{}{
			"name": toolChoice.Function.Name,
		}
	}

	// Decode each request's message content
	index := 0
	for i, m := range input.Messages {
		switch content := m.Content.(type) {
		case string:
			input.Messages[i].StringContent = content
		case []interface{}:
			dat, _ := json.Marshal(content)
			c := []schema.Content{}
			json.Unmarshal(dat, &c)
			for _, pp := range c {
				if pp.Type == "text" {
					input.Messages[i].StringContent = pp.Text
				} else if pp.Type == "image_url" {
					// Detect if pp.ImageURL is an URL, if it is download the image and encode it in base64:
					base64, err := utils.GetImageURLAsBase64(pp.ImageURL.URL)
					if err == nil {
						input.Messages[i].StringImages = append(input.Messages[i].StringImages, base64) // TODO: make sure that we only return base64 stuff
						// set a placeholder for each image
						input.Messages[i].StringContent = fmt.Sprintf("[img-%d]", index) + input.Messages[i].StringContent
						index++
					} else {
						log.Error().Err(err).Msg("Failed encoding image")
					}
				}
			}
		}
	}

	if input.RepeatPenalty != 0 {
		config.RepeatPenalty = input.RepeatPenalty
	}

	if input.FrequencyPenalty != 0 {
		config.FrequencyPenalty = input.FrequencyPenalty
	}

	if input.PresencePenalty != 0 {
		config.PresencePenalty = input.PresencePenalty
	}

	if input.Keep != 0 {
		config.Keep = input.Keep
	}

	if input.Batch != 0 {
		config.Batch = input.Batch
	}

	if input.IgnoreEOS {
		config.IgnoreEOS = input.IgnoreEOS
	}

	if input.Seed != nil {
		config.Seed = input.Seed
	}

	if input.TypicalP != nil {
		config.TypicalP = input.TypicalP
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

	// Can be either a string or an object
	switch fnc := input.FunctionCall.(type) {
	case string:
		if fnc != "" {
			config.SetFunctionCallString(fnc)
		}
	case map[string]interface{}:
		var name string
		n, exists := fnc["name"]
		if exists {
			nn, e := n.(string)
			if e {
				name = nn
			}
		}
		config.SetFunctionCallNameString(name)
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

func (c *BackendConfig) Validate() bool {
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

	if c.Name == "" {
		return false
	}

	if c.Backend != "" {
		// a regex that checks that is a string name with no special characters, except '-' and '_'
		re := regexp.MustCompile(`^[a-zA-Z0-9-_]+$`)
		return re.MatchString(c.Backend)
	}

	return true
}

func (c *BackendConfig) HasTemplate() bool {
	return c.TemplateConfig.Completion != "" || c.TemplateConfig.Edit != "" || c.TemplateConfig.Chat != "" || c.TemplateConfig.ChatMessage != ""
}
