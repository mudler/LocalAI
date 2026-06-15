package ollama

import (
	"fmt"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	openaiEndpoint "github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// ChatEndpoint handles Ollama-compatible /api/chat requests
func ChatEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OllamaChatRequest)
		if !ok || input.Model == "" {
			return ollamaError(c, 400, "model is required")
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return ollamaError(c, 400, "model configuration not found")
		}

		// Apply Ollama options to config
		applyOllamaOptions(input.Options, cfg)

		// Convert Ollama messages to OpenAI format
		openAIMessages := ollamaMessagesToOpenAI(input.Messages)

		// Build an OpenAI-compatible request
		openAIReq := &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{
				BasicModelRequest: schema.BasicModelRequest{Model: input.Model},
			},
			Messages: openAIMessages,
			Stream:   input.IsStream(),
			Context:  input.Context,
			Cancel:   input.Cancel,
		}

		if input.Options != nil {
			openAIReq.Temperature = input.Options.Temperature
			openAIReq.TopP = input.Options.TopP
			openAIReq.TopK = input.Options.TopK
			openAIReq.RepeatPenalty = input.Options.RepeatPenalty
			if input.Options.NumPredict != nil {
				openAIReq.Maxtokens = input.Options.NumPredict
			}
			if len(input.Options.Stop) > 0 {
				openAIReq.Stop = input.Options.Stop
			}
		}

		predInput := evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, nil, false)
		xlog.Debug("Ollama Chat - Prompt (after templating)", "prompt_len", len(predInput))

		if input.IsStream() {
			return handleOllamaChatStream(c, input, cfg, ml, cl, appConfig, predInput, openAIReq)
		}

		return handleOllamaChatNonStream(c, input, cfg, ml, cl, appConfig, predInput, openAIReq)
	}
}

func handleOllamaChatNonStream(c echo.Context, input *schema.OllamaChatRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest) error {
	startTime := time.Now()
	var result string

	cb := func(s string, choices *[]schema.Choice) {
		result = s
	}

	_, tokenUsage, _, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, cb, nil)
	if err != nil {
		xlog.Error("Ollama chat inference failed", "error", err)
		return ollamaError(c, 500, fmt.Sprintf("model inference failed: %v", err))
	}

	totalDuration := time.Since(startTime)

	resp := schema.OllamaChatResponse{
		Model:     input.Model,
		CreatedAt: time.Now().UTC(),
		Message: schema.OllamaMessage{
			Role:    "assistant",
			Content: result,
		},
		Done:            true,
		DoneReason:      "stop",
		TotalDuration:   totalDuration.Nanoseconds(),
		PromptEvalCount: tokenUsage.Prompt,
		EvalCount:       tokenUsage.Completion,
	}

	return c.JSON(200, resp)
}

func handleOllamaChatStream(c echo.Context, input *schema.OllamaChatRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest) error {
	c.Response().Header().Set("Content-Type", "application/x-ndjson")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	startTime := time.Now()

	tokenCallback := func(token string, usage backend.TokenUsage) bool {
		chunk := schema.OllamaChatResponse{
			Model:     input.Model,
			CreatedAt: time.Now().UTC(),
			Message: schema.OllamaMessage{
				Role:    "assistant",
				Content: token,
			},
			Done: false,
		}
		return writeNDJSON(c, chunk)
	}

	_, tokenUsage, _, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, func(s string, choices *[]schema.Choice) {}, tokenCallback)
	if err != nil {
		xlog.Error("Ollama chat stream inference failed", "error", err)
		errChunk := schema.OllamaChatResponse{
			Model:     input.Model,
			CreatedAt: time.Now().UTC(),
			Done:      true,
			DoneReason: "error",
		}
		writeNDJSON(c, errChunk)
		return nil
	}

	// Send final done message
	totalDuration := time.Since(startTime)
	finalChunk := schema.OllamaChatResponse{
		Model:           input.Model,
		CreatedAt:       time.Now().UTC(),
		Message:         schema.OllamaMessage{Role: "assistant", Content: ""},
		Done:            true,
		DoneReason:      "stop",
		TotalDuration:   totalDuration.Nanoseconds(),
		PromptEvalCount: tokenUsage.Prompt,
		EvalCount:       tokenUsage.Completion,
	}
	writeNDJSON(c, finalChunk)

	return nil
}
