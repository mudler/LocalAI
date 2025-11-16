package openai

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"
)

func ComputeChoices(
	req *schema.OpenAIRequest,
	predInput string,
	config *config.ModelConfig,
	bcl *config.ModelConfigLoader,
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

	// Serialize tools and tool_choice to JSON strings
	toolsJSON := ""
	if len(req.Tools) > 0 {
		toolsBytes, err := json.Marshal(req.Tools)
		if err == nil {
			toolsJSON = string(toolsBytes)
		}
	}
	toolChoiceJSON := ""
	if req.ToolsChoice != nil {
		toolChoiceBytes, err := json.Marshal(req.ToolsChoice)
		if err == nil {
			toolChoiceJSON = string(toolChoiceBytes)
		}
	}

	// Extract logprobs from request
	// According to OpenAI API: logprobs is boolean, top_logprobs (0-20) controls how many top tokens per position
	var logprobs *int
	var topLogprobs *int
	if req.Logprobs.IsEnabled() {
		// If logprobs is enabled, use top_logprobs if provided, otherwise default to 1
		if req.TopLogprobs != nil {
			topLogprobs = req.TopLogprobs
			// For backend compatibility, set logprobs to the top_logprobs value
			logprobs = req.TopLogprobs
		} else {
			// Default to 1 if logprobs is true but top_logprobs not specified
			val := 1
			logprobs = &val
			topLogprobs = &val
		}
	}

	// Extract logit_bias from request
	// According to OpenAI API: logit_bias is a map of token IDs (as strings) to bias values (-100 to 100)
	var logitBias map[string]float64
	if len(req.LogitBias) > 0 {
		logitBias = req.LogitBias
	}

	// get the model function to call for the result
	predFunc, err := backend.ModelInference(
		req.Context, predInput, req.Messages, images, videos, audios, loader, config, bcl, o, tokenCallback, toolsJSON, toolChoiceJSON, logprobs, topLogprobs, logitBias)
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
		tokenUsage.TimingPromptProcessing += prediction.Usage.TimingPromptProcessing
		tokenUsage.TimingTokenGeneration += prediction.Usage.TimingTokenGeneration

		finetunedResponse := backend.Finetune(*config, predInput, prediction.Response)
		cb(finetunedResponse, &result)

		// Add logprobs to the last choice if present
		if prediction.Logprobs != nil && len(result) > 0 {
			result[len(result)-1].Logprobs = prediction.Logprobs
		}

		//result = append(result, Choice{Text: prediction})

	}
	return result, tokenUsage, err
}
