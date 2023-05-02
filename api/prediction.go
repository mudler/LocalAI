package api

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	model "github.com/go-skynet/LocalAI/pkg/model"
	gpt2 "github.com/go-skynet/go-gpt2.cpp"
	gptj "github.com/go-skynet/go-gpt4all-j.cpp"
	llama "github.com/go-skynet/go-llama.cpp"
	"github.com/hashicorp/go-multierror"
)

// mutex still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
var mutexMap sync.Mutex
var mutexes map[string]*sync.Mutex = make(map[string]*sync.Mutex)

var loadedModels map[string]interface{} = map[string]interface{}{}
var muModels sync.Mutex

func backendLoader(backendString string, loader *model.ModelLoader, modelFile string, llamaOpts []llama.ModelOption) (model interface{}, err error) {
	switch strings.ToLower(backendString) {
	case "llama":
		return loader.LoadLLaMAModel(modelFile, llamaOpts...)
	case "stablelm":
		return loader.LoadStableLMModel(modelFile)
	case "gpt2":
		return loader.LoadGPT2Model(modelFile)
	case "gptj":
		return loader.LoadGPTJModel(modelFile)
	default:
		return nil, fmt.Errorf("backend unsupported: %s", backendString)
	}
}

func greedyLoader(loader *model.ModelLoader, modelFile string, llamaOpts []llama.ModelOption) (model interface{}, err error) {
	updateModels := func(model interface{}) {
		muModels.Lock()
		defer muModels.Unlock()
		loadedModels[modelFile] = model
	}

	muModels.Lock()
	m, exists := loadedModels[modelFile]
	if exists {
		muModels.Unlock()
		return m, nil
	}
	muModels.Unlock()

	model, modelerr := loader.LoadLLaMAModel(modelFile, llamaOpts...)
	if modelerr == nil {
		updateModels(model)
		return model, nil
	} else {
		err = multierror.Append(err, modelerr)
	}

	model, modelerr = loader.LoadGPTJModel(modelFile)
	if modelerr == nil {
		updateModels(model)
		return model, nil
	} else {
		err = multierror.Append(err, modelerr)
	}

	model, modelerr = loader.LoadGPT2Model(modelFile)
	if modelerr == nil {
		updateModels(model)
		return model, nil
	} else {
		err = multierror.Append(err, modelerr)
	}

	model, modelerr = loader.LoadStableLMModel(modelFile)
	if modelerr == nil {
		updateModels(model)
		return model, nil
	} else {
		err = multierror.Append(err, modelerr)
	}

	return nil, fmt.Errorf("could not load model - all backends returned error: %s", err.Error())
}

func ModelInference(s string, loader *model.ModelLoader, c Config, tokenCallback func(string) bool) (func() (string, error), error) {
	supportStreams := false
	modelFile := c.Model

	// Try to load the model
	llamaOpts := []llama.ModelOption{}
	if c.ContextSize != 0 {
		llamaOpts = append(llamaOpts, llama.SetContext(c.ContextSize))
	}
	if c.F16 {
		llamaOpts = append(llamaOpts, llama.EnableF16Memory)
	}

	var inferenceModel interface{}
	var err error
	if c.Backend == "" {
		inferenceModel, err = greedyLoader(loader, modelFile, llamaOpts)
	} else {
		inferenceModel, err = backendLoader(c.Backend, loader, modelFile, llamaOpts)
	}
	if err != nil {
		return nil, err
	}

	var fn func() (string, error)

	switch model := inferenceModel.(type) {
	case *gpt2.StableLM:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []gpt2.PredictOption{
				gpt2.SetTemperature(c.Temperature),
				gpt2.SetTopP(c.TopP),
				gpt2.SetTopK(c.TopK),
				gpt2.SetTokens(c.Maxtokens),
				gpt2.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, gpt2.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, gpt2.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *gpt2.GPT2:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []gpt2.PredictOption{
				gpt2.SetTemperature(c.Temperature),
				gpt2.SetTopP(c.TopP),
				gpt2.SetTopK(c.TopK),
				gpt2.SetTokens(c.Maxtokens),
				gpt2.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, gpt2.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, gpt2.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *gptj.GPTJ:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []gptj.PredictOption{
				gptj.SetTemperature(c.Temperature),
				gptj.SetTopP(c.TopP),
				gptj.SetTopK(c.TopK),
				gptj.SetTokens(c.Maxtokens),
				gptj.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, gptj.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, gptj.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *llama.LLama:
		supportStreams = true
		fn = func() (string, error) {

			if tokenCallback != nil {
				model.SetTokenCallback(tokenCallback)
			}

			// Generate the prediction using the language model
			predictOptions := []llama.PredictOption{
				llama.SetTemperature(c.Temperature),
				llama.SetTopP(c.TopP),
				llama.SetTopK(c.TopK),
				llama.SetTokens(c.Maxtokens),
				llama.SetThreads(c.Threads),
			}

			if c.Debug {
				predictOptions = append(predictOptions, llama.Debug)
			}

			predictOptions = append(predictOptions, llama.SetStopWords(c.StopWords...))

			if c.RepeatPenalty != 0 {
				predictOptions = append(predictOptions, llama.SetPenalty(c.RepeatPenalty))
			}

			if c.Keep != 0 {
				predictOptions = append(predictOptions, llama.SetNKeep(c.Keep))
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, llama.SetBatch(c.Batch))
			}

			if c.F16 {
				predictOptions = append(predictOptions, llama.EnableF16KV)
			}

			if c.IgnoreEOS {
				predictOptions = append(predictOptions, llama.IgnoreEOS)
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, llama.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	}

	return func() (string, error) {
		// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
		mutexMap.Lock()
		l, ok := mutexes[modelFile]
		if !ok {
			m := &sync.Mutex{}
			mutexes[modelFile] = m
			l = m
		}
		mutexMap.Unlock()
		l.Lock()
		defer l.Unlock()

		res, err := fn()
		if tokenCallback != nil && !supportStreams {
			tokenCallback(res)
		}
		return res, err
	}, nil
}

func ComputeChoices(predInput string, input *OpenAIRequest, config *Config, loader *model.ModelLoader, cb func(string, *[]Choice), tokenCallback func(string) bool) ([]Choice, error) {
	result := []Choice{}

	n := input.N

	if input.N == 0 {
		n = 1
	}

	// get the model function to call for the result
	predFunc, err := ModelInference(predInput, loader, *config, tokenCallback)
	if err != nil {
		return result, err
	}

	for i := 0; i < n; i++ {
		prediction, err := predFunc()
		if err != nil {
			return result, err
		}

		prediction = Finetune(*config, predInput, prediction)
		cb(prediction, &result)

		//result = append(result, Choice{Text: prediction})

	}
	return result, err
}

var cutstrings map[string]*regexp.Regexp = make(map[string]*regexp.Regexp)
var mu sync.Mutex = sync.Mutex{}

func Finetune(config Config, input, prediction string) string {
	if config.Echo {
		prediction = input + prediction
	}

	for _, c := range config.Cutstrings {
		mu.Lock()
		reg, ok := cutstrings[c]
		if !ok {
			cutstrings[c] = regexp.MustCompile(c)
			reg = cutstrings[c]
		}
		mu.Unlock()
		prediction = reg.ReplaceAllString(prediction, "")
	}

	for _, c := range config.TrimSpace {
		prediction = strings.TrimSpace(strings.TrimPrefix(prediction, c))
	}
	return prediction

}
