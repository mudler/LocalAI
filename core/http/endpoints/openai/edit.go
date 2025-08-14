package openai

import (
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/rs/zerolog/log"
)

// EditEndpoint is the OpenAI edit API endpoint
// @Summary OpenAI edit endpoint
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/edits [post]
func EditEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {

	return func(c *fiber.Ctx) error {

		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return fiber.ErrBadRequest
		}
		// Opt-in extra usage flag
		extraUsage := c.Get("Extra-Usage", "") != ""

		config, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			return fiber.ErrBadRequest
		}

		log.Debug().Msgf("Edit Endpoint Input : %+v", input)
		log.Debug().Msgf("Edit Endpoint Config: %+v", *config)

		var result []schema.Choice
		totalTokenUsage := backend.TokenUsage{}

		for _, i := range config.InputStrings {
			templatedInput, err := evaluator.EvaluateTemplateForPrompt(templates.EditPromptTemplate, *config, templates.PromptTemplateData{
				Input:           i,
				Instruction:     input.Instruction,
				SystemPrompt:    config.SystemPrompt,
				ReasoningEffort: input.ReasoningEffort,
				Metadata:        input.Metadata,
			})
			if err == nil {
				i = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", i)
			}

			r, tokenUsage, err := ComputeChoices(input, i, config, cl, appConfig, ml, func(s string, c *[]schema.Choice) {
				*c = append(*c, schema.Choice{Text: s})
			}, nil)
			if err != nil {
				return err
			}

			totalTokenUsage.Prompt += tokenUsage.Prompt
			totalTokenUsage.Completion += tokenUsage.Completion

			totalTokenUsage.TimingTokenGeneration += tokenUsage.TimingTokenGeneration
			totalTokenUsage.TimingPromptProcessing += tokenUsage.TimingPromptProcessing

			result = append(result, r...)
		}
		usage := schema.OpenAIUsage{
			PromptTokens:     totalTokenUsage.Prompt,
			CompletionTokens: totalTokenUsage.Completion,
			TotalTokens:      totalTokenUsage.Prompt + totalTokenUsage.Completion,
		}
		if extraUsage {
			usage.TimingTokenGeneration = totalTokenUsage.TimingTokenGeneration
			usage.TimingPromptProcessing = totalTokenUsage.TimingPromptProcessing
		}

		id := uuid.New().String()
		created := int(time.Now().Unix())
		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "edit",
			Usage:   usage,
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
