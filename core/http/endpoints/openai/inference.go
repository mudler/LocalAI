package openai

import (
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"
)

func ComputeChoices(
	req *schema.OpenAIRequest,
	predInput string,
	config *config.BackendConfig,
	o *config.ApplicationConfig,
	loader *model.ModelLoader,
	cb func(string, *[]schema.Choice),
	tokenCallback func(string, backend.TokenUsage) bool) ([]schema.Choice, backend.TokenUsage, error) {
	n := req.N // number of completions to return
	result := []schema.Choice{}

	if n == 0 {
		n = 1
	}

	images := []string{}
	for _, m := range req.Messages {
		images = append(images, m.StringImages...)
	}
	videos := []string{}
	for _, m := range req.Messages {
		videos = append(videos, m.StringVideos...)
	}
	audios := []string{}
	for _, m := range req.Messages {
		audios = append(audios, m.StringAudios...)
	}

	// get the model function to call for the result
	predFunc, err := backend.ModelInference(req.Context, predInput, req.Messages, images, videos, audios, loader, *config, o, tokenCallback)
	if err != nil {
		return result, backend.TokenUsage{}, err
	}

	tokenUsage := backend.TokenUsage{}

	for i := 0; i < n; i++ {
		prediction, err := predFunc()
		if err != nil {
			return result, backend.TokenUsage{}, err
		}

		tokenUsage.Prompt += prediction.Usage.Prompt
		tokenUsage.Completion += prediction.Usage.Completion

		finetunedResponse := backend.Finetune(*config, predInput, prediction.Response)
		cb(finetunedResponse, &result)

		//result = append(result, Choice{Text: prediction})

	}
	return result, tokenUsage, err
}
