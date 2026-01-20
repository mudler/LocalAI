package reasoning

// options holds the configuration for reasoning extraction
type options struct {
	thinkingForcedOpen bool
}

// Option is a functional option for configuring reasoning extraction
type Option func(*options)

// WithThinkingForcedOpen configures the extractor to treat all content from the start
// as reasoning until a closing tag is found. This is useful for models like GLM-4
// that output reasoning without <think> but end with </think>.
func WithThinkingForcedOpen() Option {
	return func(o *options) {
		o.thinkingForcedOpen = true
	}
}
