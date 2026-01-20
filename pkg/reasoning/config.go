package reasoning

type ReasoningConfig struct {
	// ThinkingForcedOpen indicates that the model outputs reasoning without an opening tag.
	// When true, all content from the start is treated as reasoning until a closing tag is found.
	// This is useful for models like GLM-4 that output reasoning without <think> but end with </think>.
	ThinkingForcedOpen bool `yaml:"thinking_forced_open,omitempty" json:"thinking_forced_open,omitempty"`
}
