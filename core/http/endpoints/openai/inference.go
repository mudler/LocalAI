package openai

import (
	"encoding/json"
	"strings"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAI/core/schema"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

func ComputeChoices(
	req *schema.OpenAIRequest,
	predInput string,
	config *config.ModelConfig,
	bcl *config.ModelConfigLoader,
	o *config.ApplicationConfig,
	loader *model.ModelLoader,
	cb func(string, *[]schema.Choice),
	tokenCallback func(string, backend.TokenUsage) bool,
	shouldRetry ...func(int) bool,
) ([]schema.Choice, backend.TokenUsage, []*pb.ChatDelta, error) {
	n := req.N // number of completions to return
	result := []schema.Choice{}

	if n == 0 {
		n = 1
	}

	// Extract the optional shouldRetry callback
	var shouldRetryFn func(int) bool
	if len(shouldRetry) > 0 {
		shouldRetryFn = shouldRetry[0]
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
	predFunc, err := backend.ModelInferenceFunc(
		req.Context, predInput, req.Messages, images, videos, audios, loader, config, bcl, o, tokenCallback, toolsJSON, toolChoiceJSON, logprobs, topLogprobs, logitBias, req.Metadata)
	if err != nil {
		return result, backend.TokenUsage{}, nil, err
	}

	tokenUsage := backend.TokenUsage{}
	var allChatDeltas []*pb.ChatDelta

	const maxRetries = 5

	for range n {
		var prediction backend.LLMResponse

		for attempt := 0; attempt <= maxRetries; attempt++ {
			p, err := predFunc()
			if err != nil {
				return result, backend.TokenUsage{}, nil, err
			}
			prediction = p

			// Built-in: retry on truly empty response (no tokens at all).
			// However, when the C++ autoparser is active, it clears the raw
			// message and delivers content via ChatDeltas instead. Do NOT
			// retry if ChatDeltas contain tool calls or content.
			if strings.TrimSpace(prediction.Response) == "" && attempt < maxRetries {
				hasChatDeltaData := false
				for _, d := range prediction.ChatDeltas {
					if d.Content != "" || len(d.ToolCalls) > 0 {
						hasChatDeltaData = true
						break
					}
				}
				if !hasChatDeltaData {
					xlog.Warn("Backend returned empty response, retrying",
						"attempt", attempt+1, "maxRetries", maxRetries)
					continue
				}
			}

			tokenUsage.Prompt = prediction.Usage.Prompt
			tokenUsage.Completion = prediction.Usage.Completion
			tokenUsage.TimingPromptProcessing = prediction.Usage.TimingPromptProcessing
			tokenUsage.TimingTokenGeneration = prediction.Usage.TimingTokenGeneration

			allChatDeltas = prediction.ChatDeltas

			finetunedResponse := backend.Finetune(*config, predInput, prediction.Response)
			cb(finetunedResponse, &result)

			// Caller-driven retry (tool parsing, reasoning-only, etc.).
			// When the C++ autoparser is active, it clears the raw response
			// and delivers data via ChatDeltas. If the response is empty but
			// ChatDeltas contain actionable data, skip the caller retry —
			// the autoparser already parsed the response successfully.
			skipCallerRetry := false
			if strings.TrimSpace(prediction.Response) == "" && len(prediction.ChatDeltas) > 0 {
				for _, d := range prediction.ChatDeltas {
					if d.Content != "" || len(d.ToolCalls) > 0 {
						skipCallerRetry = true
						break
					}
				}
			}
			if shouldRetryFn != nil && !skipCallerRetry && shouldRetryFn(attempt) && attempt < maxRetries {
				// Caller has already reset its state inside shouldRetry
				result = result[:0]
				allChatDeltas = nil
				continue
			}
			break
		}

		// Add logprobs to the last choice if present
		if prediction.Logprobs != nil && len(result) > 0 {
			result[len(result)-1].Logprobs = prediction.Logprobs
		}
	}
	return result, tokenUsage, allChatDeltas, err
}
