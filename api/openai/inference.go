package openai

import (
	"github.com/go-skynet/LocalAI/api/backend"
	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func ComputeChoices(req *OpenAIRequest, predInput string, config *config.Config, o *options.Option, loader *model.ModelLoader, cb func(string, *[]Choice), tokenCallback func(string) bool) ([]Choice, error) {
	n := req.N
	result := []Choice{}

	if n == 0 {
		n = 1
	}

	// get the model function to call for the result
	predFunc, err := backend.ModelInference(req.Context, predInput, loader, *config, o, tokenCallback)
	if err != nil {
		return result, err
	}

	for i := 0; i < n; i++ {
		prediction, err := predFunc()
		if err != nil {
			return result, err
		}

		prediction = backend.Finetune(*config, predInput, prediction)
		cb(prediction, &result)

		//result = append(result, Choice{Text: prediction})

	}
	return result, err
}
