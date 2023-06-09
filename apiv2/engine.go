package apiv2

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	model "github.com/go-skynet/LocalAI/pkg/model"
	transformers "github.com/go-skynet/go-ggml-transformers.cpp"
	llama "github.com/go-skynet/go-llama.cpp"
	"github.com/mitchellh/mapstructure"
	gpt4all "github.com/nomic-ai/gpt4all/gpt4all-bindings/golang"
)

type LocalAIEngine struct {
	loader         *model.ModelLoader
	mutexMapMutex  sync.Mutex
	mutexes        map[ConfigRegistration]*sync.Mutex
	cutstrings     map[ConfigRegistration]map[string]*regexp.Regexp
	cutstringMutex sync.Mutex
}

func NewLocalAIEngine(loader *model.ModelLoader) LocalAIEngine {

	// TODO CLEANUP: Perform evil magic, we only need to do once, and api should NOT be removed yet.
	gpt4alldir := filepath.Join(".", "backend-assets", "gpt4all")
	os.Setenv("GPT4ALL_IMPLEMENTATIONS_PATH", gpt4alldir)
	fmt.Printf("[*HAX*] GPT4ALL_IMPLEMENTATIONS_PATH: %s\n", gpt4alldir)

	return LocalAIEngine{
		loader:     loader,
		mutexes:    make(map[ConfigRegistration]*sync.Mutex),
		cutstrings: make(map[ConfigRegistration]map[string]*regexp.Regexp),
	}
}

// TODO model interface? Currently scheduled for phase 3 lol
func (e *LocalAIEngine) LoadModel(config Config) (interface{}, error) {
	ls := config.GetLocalSettings()
	fmt.Printf("LocalAIEngine.LoadModel => %+v\n\n", config)
	return e.loader.BackendLoader(ls.Backend, ls.ModelPath, config.ToModelOptions(), uint32(ls.Threads))
}

func (e *LocalAIEngine) GetModelPredictionFunction(config Config, tokenCallback func(string) bool) (func() ([]string, error), error) {

	fmt.Printf("LocalAIEngine.GetModelPredictionFunction => %+v\n\n", config)

	supportStreams := false
	var predictOnce func(p Prompt) (string, error) = nil

	inferenceModel, err := e.LoadModel(config)
	if err != nil {
		fmt.Printf("ERROR LOADING MODEL: %s\n", err.Error())
		return nil, err
	}

	prompts, err := config.GetPrompts()
	if err != nil {
		fmt.Printf("ERROR GetPrompts: %s\n", err.Error())
		return nil, err
	}

	switch localModel := inferenceModel.(type) {
	case *llama.LLama:
		fmt.Println("setting predictOnce for llama")
		supportStreams = true
		predictOnce = func(p Prompt) (string, error) {

			if tokenCallback != nil {
				localModel.SetTokenCallback(tokenCallback)
			}

			// TODO: AsTokens? I think that would need to be exposed from llama and the others.
			str, er := localModel.Predict(
				p.AsString(),
				config.ToPredictOptions()...,
			)
			// Seems that if we don't free the callback explicitly we leave functions registered (that might try to send on closed channels)
			// For instance otherwise the API returns: {"error":{"code":500,"message":"send on closed channel","type":""}}
			// after a stream event has occurred
			localModel.SetTokenCallback(nil)
			return str, er
		}
	case *gpt4all.Model:
		fmt.Println("setting predictOnce for gpt4all")
		supportStreams = true

		predictOnce = func(p Prompt) (string, error) {
			if tokenCallback != nil {
				localModel.SetTokenCallback(tokenCallback)
			}

			tempFakePO := []gpt4all.PredictOption{}
			mappedPredictOptions := gpt4all.PredictOptions{}

			mapstructure.Decode(config.ToPredictOptions(), &mappedPredictOptions)

			// str, err := localModel.PredictTEMP(
			str, err := localModel.Predict(
				p.AsString(),
				// mappedPredictOptions,
				tempFakePO...,
			)
			// Seems that if we don't free the callback explicitly we leave functions registered (that might try to send on closed channels)
			// For instance otherwise the API returns: {"error":{"code":500,"message":"send on closed channel","type":""}}
			// after a stream event has occurred
			localModel.SetTokenCallback(nil)
			return str, err
		}
	case *transformers.GPTJ:
		fmt.Println("setting predictOnce for GPTJ")
		supportStreams = false // EXP
		predictOnce = func(p Prompt) (string, error) {
			mappedPredictOptions := transformers.PredictOptions{}

			mapstructure.Decode(config.ToPredictOptions(), &mappedPredictOptions)

			fmt.Printf("MAPPED OPTIONS: %+v\n", mappedPredictOptions)

			// str, err := localModel.PredictTEMP(
			str, err := localModel.Predict(
				p.AsString(),
				// mappedPredictOptions,
				nil,
			)
			return str, err
		}
	}

	if predictOnce == nil {
		fmt.Printf("Failed to find a predictOnce for %T", inferenceModel)
		return nil, fmt.Errorf("failed to find a predictOnce for %T", inferenceModel)
	}

	req := config.GetRequestDefaults()

	return func() ([]string, error) {
		// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
		e.mutexMapMutex.Lock()
		r := config.GetRegistration()
		l, ok := e.mutexes[r]
		if !ok {
			m := &sync.Mutex{}
			e.mutexes[r] = m
			l = m
		}
		e.mutexMapMutex.Unlock()
		l.Lock()
		defer l.Unlock()

		results := []string{}

		n, err := config.GetN()

		if err != nil {
			// TODO live to regret this, but for now...
			n = 1
		}

		for p_i, prompt := range prompts {
			for n_i := 0; n_i < n; n_i++ {
				res, err := predictOnce(prompt)

				if err != nil {
					fmt.Printf("ERROR DURING GetModelPredictionFunction -> PredictionFunction for %T with p_i: %d/n_i: %d\n%s", config, p_i, n_i, err.Error())
					return nil, err
				}

				fmt.Printf("\n\n🤯 raw res: %s\n\n", res)

				// TODO: this used to be a part of finetune. For.... questionable parameter reasons I've moved it up here. Revisit this if it's smelly in the future.
				ccr, is_ccr := req.(CreateCompletionRequest)
				if is_ccr {
					if *ccr.Echo {
						res = prompt.AsString() + res
					}
				}

				res = e.Finetune(config, res)

				if tokenCallback != nil && !supportStreams {
					tokenCallback(res)
				}
				results = append(results, res)
			}
		}

		return results, nil

	}, nil
}

func (e *LocalAIEngine) Finetune(config Config, prediction string) string {

	reg := config.GetRegistration()
	switch req := config.GetRequestDefaults().(type) {
	case *CreateChatCompletionRequest:
	case *CreateCompletionRequest:
		ext := req.XLocalaiExtensions
		if ext != nil {
			for _, c := range *ext.Cutstrings {
				e.cutstringMutex.Lock()
				regex, ok := e.cutstrings[reg][c]
				if !ok {
					e.cutstrings[reg][c] = regexp.MustCompile(c)
					regex = e.cutstrings[reg][c]
				}
				e.cutstringMutex.Unlock()
				prediction = regex.ReplaceAllString(prediction, "")
			}

			for _, c := range *ext.Trimstrings {
				prediction = strings.TrimSpace(strings.TrimPrefix(prediction, c))
			}
		}
	}

	return prediction
}
