package reasoning

// TagPair represents a start/end tag pair for reasoning extraction
type TagPair struct {
	Start string `yaml:"start" json:"start"`
	End   string `yaml:"end" json:"end"`
}

type Config struct {
	DisableReasoningTagPrefill *bool     `yaml:"disable_reasoning_tag_prefill,omitempty" json:"disable_reasoning_tag_prefill,omitempty"`
	DisableReasoning           *bool     `yaml:"disable,omitempty" json:"disable,omitempty"`
	StripReasoningOnly         *bool     `yaml:"strip_reasoning_only,omitempty" json:"strip_reasoning_only,omitempty"`
	ThinkingStartTokens        []string  `yaml:"thinking_start_tokens,omitempty" json:"thinking_start_tokens,omitempty"`
	TagPairs                   []TagPair `yaml:"tag_pairs,omitempty" json:"tag_pairs,omitempty"`
	MessagesFormat             string    `yaml:"messages_format,omitempty" json:"messages_format,omitempty"`
}
