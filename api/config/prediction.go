package api_config

type PredictionOptions struct {

	// Also part of the OpenAI official spec
	Model string `json:"model" yaml:"model"`

	// Also part of the OpenAI official spec
	Language string `json:"language"`

	// Also part of the OpenAI official spec. use it for returning multiple results
	N int `json:"n"`

	// Common options between all the API calls, part of the OpenAI spec
	TopP        float64 `json:"top_p" yaml:"top_p"`
	TopK        int     `json:"top_k" yaml:"top_k"`
	Temperature float64 `json:"temperature" yaml:"temperature"`
	Maxtokens   int     `json:"max_tokens" yaml:"max_tokens"`
	Echo        bool    `json:"echo"`

	// Custom parameters - not present in the OpenAI API
	Batch         int     `json:"batch" yaml:"batch"`
	F16           bool    `json:"f16" yaml:"f16"`
	IgnoreEOS     bool    `json:"ignore_eos" yaml:"ignore_eos"`
	RepeatPenalty float64 `json:"repeat_penalty" yaml:"repeat_penalty"`
	Keep          int     `json:"n_keep" yaml:"n_keep"`

	MirostatETA float64 `json:"mirostat_eta" yaml:"mirostat_eta"`
	MirostatTAU float64 `json:"mirostat_tau" yaml:"mirostat_tau"`
	Mirostat    int     `json:"mirostat" yaml:"mirostat"`

	FrequencyPenalty float64 `json:"frequency_penalty" yaml:"frequency_penalty"`
	TFZ              float64 `json:"tfz" yaml:"tfz"`

	TypicalP float64 `json:"typical_p" yaml:"typical_p"`
	Seed     int     `json:"seed" yaml:"seed"`

	NegativePrompt      string  `json:"negative_prompt" yaml:"negative_prompt"`
	RopeFreqBase        float32 `json:"rope_freq_base" yaml:"rope_freq_base"`
	RopeFreqScale       float32 `json:"rope_freq_scale" yaml:"rope_freq_scale"`
	NegativePromptScale float32 `json:"negative_prompt_scale" yaml:"negative_prompt_scale"`
	// AutoGPTQ
	UseFastTokenizer bool `json:"use_fast_tokenizer" yaml:"use_fast_tokenizer"`

	// Diffusers
	ClipSkip int `json:"clip_skip" yaml:"clip_skip"`

	// RWKV (?)
	Tokenizer string `json:"tokenizer" yaml:"tokenizer"`
}
