package api

import (
	"fmt"
	"sync"

	model "github.com/go-skynet/LocalAI/pkg/model"
	gpt2 "github.com/go-skynet/go-gpt2.cpp"
	gptj "github.com/go-skynet/go-gpt4all-j.cpp"
	llama "github.com/go-skynet/go-llama.cpp"
)

// mutex still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
var mutexMap sync.Mutex
var mutexes map[string]*sync.Mutex = make(map[string]*sync.Mutex)

func ModelInference(s string, loader *model.ModelLoader, c Config) (func() (string, error), error) {
	var model *llama.LLama
	var gptModel *gptj.GPTJ
	var gpt2Model *gpt2.GPT2
	var stableLMModel *gpt2.StableLM

	modelFile := c.Model

	// Try to load the model
	var llamaerr, gpt2err, gptjerr, stableerr error
	llamaOpts := []llama.ModelOption{}
	if c.ContextSize != 0 {
		llamaOpts = append(llamaOpts, llama.SetContext(c.ContextSize))
	}
	if c.F16 {
		llamaOpts = append(llamaOpts, llama.EnableF16Memory)
	}

	// TODO: this is ugly, better identifying the model somehow! however, it is a good stab for a first implementation..
	model, llamaerr = loader.LoadLLaMAModel(modelFile, llamaOpts...)
	if llamaerr != nil {
		gptModel, gptjerr = loader.LoadGPTJModel(modelFile)
		if gptjerr != nil {
			gpt2Model, gpt2err = loader.LoadGPT2Model(modelFile)
			if gpt2err != nil {
				stableLMModel, stableerr = loader.LoadStableLMModel(modelFile)
				if stableerr != nil {
					return nil, fmt.Errorf("llama: %s gpt: %s gpt2: %s stableLM: %s", llamaerr.Error(), gptjerr.Error(), gpt2err.Error(), stableerr.Error()) // llama failed first, so we want to catch both errors
				}
			}
		}
	}

	var fn func() (string, error)

	switch {
	case stableLMModel != nil:
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

			return stableLMModel.Predict(
				s,
				predictOptions...,
			)
		}
	case gpt2Model != nil:
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

			return gpt2Model.Predict(
				s,
				predictOptions...,
			)
		}
	case gptModel != nil:
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

			return gptModel.Predict(
				s,
				predictOptions...,
			)
		}
	case model != nil:
		fn = func() (string, error) {
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

		return fn()
	}, nil
}
