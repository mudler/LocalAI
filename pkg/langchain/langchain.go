package langchain

type PredictOptions struct {
	Model string `json:"model"`
	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens int `json:"max_tokens"`
	// Temperature is the temperature for sampling, between 0 and 1.
	Temperature float64 `json:"temperature"`
	// StopWords is a list of words to stop on.
	StopWords []string `json:"stop_words"`
}

type PredictOption func(p *PredictOptions)

var DefaultOptions = PredictOptions{
	Model:       "gpt2",
	MaxTokens:   200,
	Temperature: 0.96,
	StopWords:   nil,
}

type Predict struct {
	Completion string
}

func SetModel(model string) PredictOption {
	return func(o *PredictOptions) {
		o.Model = model
	}
}

func SetTemperature(temperature float64) PredictOption {
	return func(o *PredictOptions) {
		o.Temperature = temperature
	}
}

func SetMaxTokens(maxTokens int) PredictOption {
	return func(o *PredictOptions) {
		o.MaxTokens = maxTokens
	}
}

func SetStopWords(stopWords []string) PredictOption {
	return func(o *PredictOptions) {
		o.StopWords = stopWords
	}
}

// NewPredictOptions Create a new PredictOptions object with the given options.
func NewPredictOptions(opts ...PredictOption) PredictOptions {
	p := DefaultOptions
	for _, opt := range opts {
		opt(&p)
	}
	return p
}
