package apiv2

import (
	"fmt"
	"strings"

	llama "github.com/go-skynet/go-llama.cpp"
	"github.com/rs/zerolog/log"
)

type ConfigRegistration struct {
	Endpoint string `yaml:"endpoint" json:"endpoint" mapstructure:"endpoint"`
	Model    string `yaml:"model" json:"model" mapstructure:"model"`
}

type ConfigLocalSettings struct {
	ModelPath    string `yaml:"model" mapstructure:"model"`
	TemplatePath string `yaml:"template" mapstructure:"template"`
	Backend      string `yaml:"backend" mapstructure:"backend"`
	Threads      int    `yaml:"threads" mapstructure:"threads"`
}

type ConfigStub struct {
	Registration  ConfigRegistration  `yaml:"registration" mapstructure:"registration"`
	LocalSettings ConfigLocalSettings `yaml:"local_paths" mapstructure:"local_paths"`
}

type SpecificConfig[RequestModel any] struct {
	ConfigStub      `mapstructure:",squash"`
	RequestDefaults RequestModel `yaml:"request_defaults" mapstructure:"request_defaults"`
}

type Config interface {
	GetRequestDefaults() interface{}
	GetLocalSettings() ConfigLocalSettings
	GetRegistration() ConfigRegistration

	// Go People: Is this good design?
	ToPredictOptions() []llama.PredictOption
	ToModelOptions() []llama.ModelOption

	// Go People: Also curious about these two. Even more sketchy!
	GetPrompts() ([]Prompt, error)
	GetN() (int, error)
}

type Prompt interface {
	AsString() string //, bool)
	AsTokens() []int
}

// How do Go people name these? Should I just ditch the interface entirely?
type PromptImpl struct {
	sVal string
	tVal []int
}

func (p PromptImpl) AsString() string {
	return p.sVal
}

func (p PromptImpl) AsTokens() []int {
	return p.tVal
}

func (cs ConfigStub) GetRequestDefaults() interface{} {
	return nil
}

func (cs ConfigStub) GetLocalSettings() ConfigLocalSettings {
	return cs.LocalSettings
}

func (cs ConfigStub) GetRegistration() ConfigRegistration {
	return cs.Registration
}

func (cs ConfigStub) ToPredictOptions() []llama.PredictOption {
	return []llama.PredictOption{}
}

func (cs ConfigStub) ToModelOptions() []llama.ModelOption {
	return []llama.ModelOption{}
}

func (cs ConfigStub) GetPrompts() ([]Prompt, error) {
	// Does this make sense?
	return nil, fmt.Errorf("unsupported operation GetPrompts for %T", cs)
}

func (cs ConfigStub) GetN() (int, error) {
	return 0, fmt.Errorf("unsupported operation GetN for %T", cs)
}

func (sc SpecificConfig[RequestModel]) GetRequestDefaults() interface{} {
	return sc.RequestDefaults
}

func (sc SpecificConfig[RequestModel]) GetRequest() RequestModel {
	return sc.RequestDefaults
}

func (sc SpecificConfig[RequestModel]) GetLocalSettings() ConfigLocalSettings {
	return sc.LocalSettings
}

func (sc SpecificConfig[RequestModel]) GetRegistration() ConfigRegistration {
	return sc.Registration
}

// These functions I'm a bit dubious about. I think there's a better refactoring down in pkg/model
// But to get a minimal test up and running, here we go!
// TODO: non text completion
func (sc SpecificConfig[RequestModel]) ToModelOptions() []llama.ModelOption {

	llamaOpts := []llama.ModelOption{}

	switch req := sc.GetRequestDefaults().(type) {
	case CreateCompletionRequest:
	case CreateChatCompletionRequest:
		if req.XLocalaiExtensions.F16 != nil && *(req.XLocalaiExtensions.F16) {
			llamaOpts = append(llamaOpts, llama.EnableF16Memory)
		}

		if req.MaxTokens != nil && *req.MaxTokens > 0 {
			llamaOpts = append(llamaOpts, llama.SetContext(*req.MaxTokens)) // todo is this right?
		}

		// TODO DO MORE!

	}
	// Code to Port:

	// if c.Embeddings {
	// 	llamaOpts = append(llamaOpts, llama.EnableEmbeddings)
	// }

	// if c.NGPULayers != 0 {
	// 	llamaOpts = append(llamaOpts, llama.SetGPULayers(c.NGPULayers))
	// }

	return llamaOpts
}

func (sc SpecificConfig[RequestModel]) ToPredictOptions() []llama.PredictOption {
	llamaOpts := []llama.PredictOption{
		llama.SetThreads(sc.GetLocalSettings().Threads),
	}

	switch req := sc.GetRequestDefaults().(type) {
	// TODO Refactor this when we get to p2 and add image / audio
	// I expect that it'll be worth pulling out the base case first, and doing fancy fallthrough things.
	// Text Requests:
	case CreateCompletionRequest:
	case CreateChatCompletionRequest:

		if req.Temperature != nil {
			llamaOpts = append(llamaOpts, llama.SetTemperature(float64(*req.Temperature))) // Oh boy. TODO Investigate. This is why I'm doing this.
		}

		if req.TopP != nil {
			llamaOpts = append(llamaOpts, llama.SetTopP(float64(*req.TopP))) // CAST
		}

		if req.MaxTokens != nil {
			llamaOpts = append(llamaOpts, llama.SetTokens(*req.MaxTokens))
		}

		if req.FrequencyPenalty != nil {
			llamaOpts = append(llamaOpts, llama.SetPenalty(float64(*req.FrequencyPenalty))) // CAST
		}

		if req.Stop != nil {

			if stop0, err := req.Stop.AsCreateChatCompletionRequestStop0(); err == nil {
				llamaOpts = append(llamaOpts, llama.SetStopWords(stop0))
			}

			if stop1, err := req.Stop.AsCreateChatCompletionRequestStop1(); err == nil && len(stop1) > 0 {
				llamaOpts = append(llamaOpts, llama.SetStopWords(stop1...))
			}
		}

		if req.XLocalaiExtensions != nil {

			if req.XLocalaiExtensions.TopK != nil {
				llamaOpts = append(llamaOpts, llama.SetTopK(*req.XLocalaiExtensions.TopK))
			}

			if req.XLocalaiExtensions.F16 != nil && *(req.XLocalaiExtensions.F16) {
				llamaOpts = append(llamaOpts, llama.EnableF16KV)
			}

			if req.XLocalaiExtensions.Seed != nil {
				llamaOpts = append(llamaOpts, llama.SetSeed(*req.XLocalaiExtensions.Seed))
			}

			if req.XLocalaiExtensions.IgnoreEos != nil && *(req.XLocalaiExtensions.IgnoreEos) {
				llamaOpts = append(llamaOpts, llama.IgnoreEOS)
			}

			if req.XLocalaiExtensions.Debug != nil && *(req.XLocalaiExtensions.Debug) {
				llamaOpts = append(llamaOpts, llama.Debug)
			}

			if req.XLocalaiExtensions.Mirostat != nil {
				llamaOpts = append(llamaOpts, llama.SetMirostat(*req.XLocalaiExtensions.Mirostat))
			}

			if req.XLocalaiExtensions.MirostatEta != nil {
				llamaOpts = append(llamaOpts, llama.SetMirostatETA(*req.XLocalaiExtensions.MirostatEta))
			}

			if req.XLocalaiExtensions.MirostatTau != nil {
				llamaOpts = append(llamaOpts, llama.SetMirostatTAU(*req.XLocalaiExtensions.MirostatTau))
			}

			if req.XLocalaiExtensions.Keep != nil {
				llamaOpts = append(llamaOpts, llama.SetNKeep(*req.XLocalaiExtensions.Keep))
			}

			if req.XLocalaiExtensions.Batch != nil && *(req.XLocalaiExtensions.Batch) != 0 {
				llamaOpts = append(llamaOpts, llama.SetBatch(*req.XLocalaiExtensions.Batch))
			}

		}

	}

	// CODE TO PORT

	// SKIPPING PROMPT CACHE FOR PASS ONE, TODO READ ABOUT IT

	// if c.PromptCacheAll {
	// 	predictOptions = append(predictOptions, llama.EnablePromptCacheAll)
	// }

	// if c.PromptCachePath != "" {
	// 	// Create parent directory
	// 	p := filepath.Join(modelPath, c.PromptCachePath)
	// 	os.MkdirAll(filepath.Dir(p), 0755)
	// 	predictOptions = append(predictOptions, llama.SetPathPromptCache(p))
	// }

	return llamaOpts
}

// It's unclear if this code belongs here or somewhere else, but I'm jamming it here for now.
func (sc SpecificConfig[RequestModel]) GetPrompts() ([]Prompt, error) {
	prompts := []Prompt{}

	switch req := sc.GetRequestDefaults().(type) {
	case CreateCompletionRequest:
		p0, err := req.Prompt.AsCreateCompletionRequestPrompt0()
		if err == nil {
			p := PromptImpl{sVal: p0}
			return []Prompt{p}, nil
		}
		p1, err := req.Prompt.AsCreateCompletionRequestPrompt1()
		if err == nil {
			for _, m := range p1 {
				prompts = append(prompts, PromptImpl{sVal: m})
			}
			return prompts, nil
		}
		p2, err := req.Prompt.AsCreateCompletionRequestPrompt2()
		if err == nil {
			p := PromptImpl{tVal: p2}
			return []Prompt{p}, nil
		}
		p3, err := req.Prompt.AsCreateCompletionRequestPrompt3()
		if err == nil {
			for _, t := range p3 {
				prompts = append(prompts, PromptImpl{tVal: t})
			}
			return prompts, nil
		}
	case CreateChatCompletionRequest:
		for _, message := range req.Messages {
			var content string
			var role string
			if req.XLocalaiExtensions.Roles != nil {
				// TODO this is ugly, and seems like I did it wrong, fix after
				switch strings.ToLower(string(message.Role)) {
				case "system":
					if r := req.XLocalaiExtensions.Roles.System; r != nil {
						role = *r
					}
				case "user":
					if r := req.XLocalaiExtensions.Roles.User; r != nil {
						role = *r
					}
				case "assistant":
					if r := req.XLocalaiExtensions.Roles.Assistant; r != nil {
						role = *r
					}
				default:
					log.Error().Msgf("Unrecognized message role: %s", message.Role)
					role = ""
				}
			}
			if role != "" {
				content = fmt.Sprint(role, " ", message.Content)
			} else {
				content = message.Content
			}

			if content != "" {
				prompts = append(prompts, PromptImpl{sVal: content})
			}

		}
		return prompts, nil
	}

	return nil, fmt.Errorf("string prompt not found for %T", sc.GetRequestDefaults())
}

func (sc SpecificConfig[RequestModel]) GetN() (int, error) {
	switch req := sc.GetRequestDefaults().(type) {

	case CreateChatCompletionRequest:
	case CreateCompletionRequest:
	case CreateEditRequest:
	case CreateImageRequest:
		// TODO I AM SORRY FOR THIS DIRTY HACK.
		// YTT is currently mangling the n property and renaming it to false.
		// This needs to be fixed before merging. However for testing.....
		return *req.False, nil
	}

	return 0, fmt.Errorf("unsupported operation GetN for %T", sc)
}

// TODO: Not even using this, but illustration of difficulty: should this be integrated to make GetPrompts(), returning an interface of {Tokens []int, String string}
// func (sc SpecificConfig[RequestModel]) GetTokenPrompts() ([]int, error) {}
