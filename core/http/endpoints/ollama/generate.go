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

// GenerateEndpoint handles Ollama-compatible /api/generate requests
func GenerateEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OllamaGenerateRequest)
		if !ok || input.Model == "" {
			return ollamaError(c, 400, "model is required")
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return ollamaError(c, 400, "model configuration not found")
		}

		// Handle empty prompt — return immediately with "load" reason
		if input.Prompt == "" {
			resp := schema.OllamaGenerateResponse{
				Model:      input.Model,
				CreatedAt:  time.Now().UTC(),
				Response:   "",
				Done:       true,
				DoneReason: "load",
			}
			if input.IsStream() {
				c.Response().Header().Set("Content-Type", "application/x-ndjson")
				writeNDJSON(c, resp)
				return nil
			}
			return c.JSON(200, resp)
		}

		applyOllamaOptions(input.Options, cfg)

		// Build messages from prompt
		var messages []schema.Message
		if input.System != "" {
			messages = append(messages, schema.Message{
				Role:          "system",
				StringContent: input.System,
				Content:       input.System,
			})
		}
		messages = append(messages, schema.Message{
			Role:          "user",
			StringContent: input.Prompt,
			Content:       input.Prompt,
		})

		openAIReq := &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{
				BasicModelRequest: schema.BasicModelRequest{Model: input.Model},
			},
			Messages: messages,
			Stream:   input.IsStream(),
			Context:  input.Ctx,
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

		var predInput string
		if input.Raw {
			// Raw mode: skip chat template, use prompt directly
			predInput = input.Prompt
		} else {
			predInput = evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, nil, false)
		}
		xlog.Debug("Ollama Generate - Prompt", "prompt_len", len(predInput), "raw", input.Raw)

		if input.IsStream() {
			return handleOllamaGenerateStream(c, input, cfg, ml, cl, appConfig, predInput, openAIReq)
		}

		return handleOllamaGenerateNonStream(c, input, cfg, ml, cl, appConfig, predInput, openAIReq)
	}
}

func handleOllamaGenerateNonStream(c echo.Context, input *schema.OllamaGenerateRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest) error {
	startTime := time.Now()
	var result string

	cb := func(s string, choices *[]schema.Choice) {
		result = s
	}

	_, tokenUsage, _, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, cb, nil)
	if err != nil {
		xlog.Error("Ollama generate inference failed", "error", err)
		return ollamaError(c, 500, fmt.Sprintf("model inference failed: %v", err))
	}

	totalDuration := time.Since(startTime)

	resp := schema.OllamaGenerateResponse{
		Model:           input.Model,
		CreatedAt:       time.Now().UTC(),
		Response:        result,
		Done:            true,
		DoneReason:      "stop",
		TotalDuration:   totalDuration.Nanoseconds(),
		PromptEvalCount: tokenUsage.Prompt,
		EvalCount:       tokenUsage.Completion,
	}

	return c.JSON(200, resp)
}

func handleOllamaGenerateStream(c echo.Context, input *schema.OllamaGenerateRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest) error {
	c.Response().Header().Set("Content-Type", "application/x-ndjson")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	startTime := time.Now()

	tokenCallback := func(token string, usage backend.TokenUsage) bool {
		chunk := schema.OllamaGenerateResponse{
			Model:     input.Model,
			CreatedAt: time.Now().UTC(),
			Response:  token,
			Done:      false,
		}
		return writeNDJSON(c, chunk)
	}

	_, tokenUsage, _, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, func(s string, choices *[]schema.Choice) {}, tokenCallback)
	if err != nil {
		xlog.Error("Ollama generate stream inference failed", "error", err)
		errChunk := schema.OllamaGenerateResponse{
			Model:      input.Model,
			CreatedAt:  time.Now().UTC(),
			Done:       true,
			DoneReason: "error",
		}
		writeNDJSON(c, errChunk)
		return nil
	}

	totalDuration := time.Since(startTime)
	finalChunk := schema.OllamaGenerateResponse{
		Model:           input.Model,
		CreatedAt:       time.Now().UTC(),
		Response:        "",
		Done:            true,
		DoneReason:      "stop",
		TotalDuration:   totalDuration.Nanoseconds(),
		PromptEvalCount: tokenUsage.Prompt,
		EvalCount:       tokenUsage.Completion,
	}
	writeNDJSON(c, finalChunk)

	return nil
}
