package reasoning

type Config struct {
	DisableReasoningTagPrefill *bool `yaml:"disable_reasoning_tag_prefill,omitempty" json:"disable_reasoning_tag_prefill,omitempty"`
	DisableReasoning           *bool `yaml:"disable,omitempty" json:"disable,omitempty"`
}
