package schema

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// LogprobsValue represents the logprobs parameter which is a boolean.
// According to OpenAI API: true means return log probabilities, false/null means don't return them.
// The actual number of top logprobs per token is controlled by top_logprobs (0-5).
type LogprobsValue struct {
	Enabled bool // true if logprobs should be returned
}

// UnmarshalJSON implements json.Unmarshaler to handle boolean
func (l *LogprobsValue) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as boolean
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		l.Enabled = b
		return nil
	}

	// If it's null, set to false
	var n *bool
	if err := json.Unmarshal(data, &n); err == nil {
		l.Enabled = false
		return nil
	}

	// Try as integer for backward compatibility (treat > 0 as true)
	var i int
	if err := json.Unmarshal(data, &i); err == nil {
		l.Enabled = i > 0
		return nil
	}

	return json.Unmarshal(data, &l.Enabled)
}

// MarshalJSON implements json.Marshaler
func (l LogprobsValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(l.Enabled)
}

// UnmarshalYAML implements yaml.Unmarshaler to handle boolean
func (l *LogprobsValue) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		switch value.Tag {
		case "!!bool":
			var b bool
			if err := value.Decode(&b); err != nil {
				return err
			}
			l.Enabled = b
			return nil
		case "!!int":
			// For backward compatibility, treat integer > 0 as true
			var i int
			if err := value.Decode(&i); err != nil {
				return err
			}
			l.Enabled = i > 0
			return nil
		case "!!null":
			l.Enabled = false
			return nil
		}
	}
	return value.Decode(&l.Enabled)
}

// IsEnabled returns true if logprobs should be returned
func (l *LogprobsValue) IsEnabled() bool {
	return l.Enabled
}

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

	// OpenAI API logprobs parameters
	// logprobs: boolean - if true, returns log probabilities of each output token
	// top_logprobs: integer 0-20 - number of most likely tokens to return at each token position
	Logprobs    LogprobsValue      `json:"logprobs,omitempty" yaml:"logprobs,omitempty"`         // Whether to return log probabilities (true/false)
	TopLogprobs *int               `json:"top_logprobs,omitempty" yaml:"top_logprobs,omitempty"` // Number of top logprobs per token (0-20)
	LogitBias   map[string]float64 `json:"logit_bias,omitempty" yaml:"logit_bias,omitempty"`     // Map of token IDs to bias values (-100 to 100)

	NegativePrompt      string  `json:"negative_prompt,omitempty" yaml:"negative_prompt,omitempty"`
	RopeFreqBase        float32 `json:"rope_freq_base,omitempty" yaml:"rope_freq_base,omitempty"`
	RopeFreqScale       float32 `json:"rope_freq_scale,omitempty" yaml:"rope_freq_scale,omitempty"`
	NegativePromptScale float32 `json:"negative_prompt_scale,omitempty" yaml:"negative_prompt_scale,omitempty"`

	// Diffusers
	ClipSkip int `json:"clip_skip,omitempty" yaml:"clip_skip,omitempty"`

	// RWKV (?)
	Tokenizer string `json:"tokenizer,omitempty" yaml:"tokenizer,omitempty"`
}
