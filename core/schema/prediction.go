package schema

// @Description PredictionOptions contains prediction parameters for model inference
type PredictionOptions struct {

	// Also part of the OpenAI official spec
	BasicModelRequest `yaml:",inline"`

	// Also part of the OpenAI official spec
	Language string `json:"language,omitempty" yaml:"language,omitempty"`

	// Only for audio transcription
	Translate bool `json:"translate,omitempty" yaml:"translate,omitempty"`

	// Also part of the OpenAI official spec. use it for returning multiple results
	N int `json:"n,omitempty" yaml:"n,omitempty"`

	// Common options between all the API calls, part of the OpenAI spec
	TopP        *float64 `json:"top_p,omitempty" yaml:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty" yaml:"top_k,omitempty"`
	Temperature *float64 `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	Maxtokens   *int     `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	Echo        bool     `json:"echo,omitempty" yaml:"echo,omitempty"`

	// Custom parameters - not present in the OpenAI API
	Batch         int     `json:"batch,omitempty" yaml:"batch,omitempty"`
	IgnoreEOS     bool    `json:"ignore_eos,omitempty" yaml:"ignore_eos,omitempty"`
	RepeatPenalty float64 `json:"repeat_penalty,omitempty" yaml:"repeat_penalty,omitempty"`

	RepeatLastN int `json:"repeat_last_n,omitempty" yaml:"repeat_last_n,omitempty"`

	Keep int `json:"n_keep,omitempty" yaml:"n_keep,omitempty"`

	FrequencyPenalty float64  `json:"frequency_penalty,omitempty" yaml:"frequency_penalty,omitempty"`
	PresencePenalty  float64  `json:"presence_penalty,omitempty" yaml:"presence_penalty,omitempty"`
	TFZ              *float64 `json:"tfz,omitempty" yaml:"tfz,omitempty"`

	TypicalP *float64 `json:"typical_p,omitempty" yaml:"typical_p,omitempty"`
	Seed     *int     `json:"seed,omitempty" yaml:"seed,omitempty"`

	NegativePrompt      string  `json:"negative_prompt,omitempty" yaml:"negative_prompt,omitempty"`
	RopeFreqBase        float32 `json:"rope_freq_base,omitempty" yaml:"rope_freq_base,omitempty"`
	RopeFreqScale       float32 `json:"rope_freq_scale,omitempty" yaml:"rope_freq_scale,omitempty"`
	NegativePromptScale float32 `json:"negative_prompt_scale,omitempty" yaml:"negative_prompt_scale,omitempty"`

	// Diffusers
	ClipSkip int `json:"clip_skip,omitempty" yaml:"clip_skip,omitempty"`

	// RWKV (?)
	Tokenizer string `json:"tokenizer,omitempty" yaml:"tokenizer,omitempty"`
}
