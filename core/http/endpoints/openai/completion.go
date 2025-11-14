package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

// CompletionEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/completions
// @Summary Generate completions for a given prompt and model.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/completions [post]
func CompletionEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	process := func(id string, s string, req *schema.OpenAIRequest, config *config.ModelConfig, loader *model.ModelLoader, responses chan schema.OpenAIResponse, extraUsage bool) error {
		tokenCallback := func(s string, tokenUsage backend.TokenUsage) bool {
			created := int(time.Now().Unix())

			usage := schema.OpenAIUsage{
				PromptTokens:     tokenUsage.Prompt,
				CompletionTokens: tokenUsage.Completion,
				TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
			}
			if extraUsage {
				usage.TimingTokenGeneration = tokenUsage.TimingTokenGeneration
				usage.TimingPromptProcessing = tokenUsage.TimingPromptProcessing
			}
			resp := schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{
					{
						Index:        0,
						Text:         s,
						FinishReason: nil,
					},
				},
				Object: "text_completion",
				Usage:  usage,
			}
			log.Debug().Msgf("Sending goroutine: %s", s)

			responses <- resp
			return true
		}
		_, _, err := ComputeChoices(req, s, config, cl, appConfig, loader, func(s string, c *[]schema.Choice) {}, tokenCallback)
		close(responses)
		return err
	}

	return func(c echo.Context) error {

		created := int(time.Now().Unix())

		// Handle Correlation
		id := c.Request().Header.Get("X-Correlation-ID")
		if id == "" {
			id = uuid.New().String()
		}
		extraUsage := c.Request().Header.Get("Extra-Usage") != ""

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		config, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			return echo.ErrBadRequest
		}

		if config.ResponseFormatMap != nil {
			d := schema.ChatCompletionResponseFormat{}
			dat, _ := json.Marshal(config.ResponseFormatMap)
			_ = json.Unmarshal(dat, &d)
			if d.Type == "json_object" {
				input.Grammar = functions.JSONBNF
			}
		}

		config.Grammar = input.Grammar

		log.Debug().Msgf("Parameter Config: %+v", config)

		if input.Stream {
			log.Debug().Msgf("Stream request received")
			c.Response().Header().Set("Content-Type", "text/event-stream")
			c.Response().Header().Set("Cache-Control", "no-cache")
			c.Response().Header().Set("Connection", "keep-alive")

			if len(config.PromptStrings) > 1 {
				return errors.New("cannot handle more than 1 `PromptStrings` when Streaming")
			}

			predInput := config.PromptStrings[0]

			templatedInput, err := evaluator.EvaluateTemplateForPrompt(templates.CompletionPromptTemplate, *config, templates.PromptTemplateData{
				Input:           predInput,
				SystemPrompt:    config.SystemPrompt,
				ReasoningEffort: input.ReasoningEffort,
				Metadata:        input.Metadata,
			})
			if err == nil {
				predInput = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", predInput)
			}

			responses := make(chan schema.OpenAIResponse)

			ended := make(chan error)
			go func() {
				ended <- process(id, predInput, input, config, ml, responses, extraUsage)
			}()

		LOOP:
			for {
				select {
				case ev := <-responses:
					if len(ev.Choices) == 0 {
						log.Debug().Msgf("No choices in the response, skipping")
						continue
					}
					respData, err := json.Marshal(ev)
					if err != nil {
						log.Debug().Msgf("Failed to marshal response: %v", err)
						continue
					}

					log.Debug().Msgf("Sending chunk: %s", string(respData))
					_, err = fmt.Fprintf(c.Response().Writer, "data: %s\n\n", string(respData))
					if err != nil {
						return err
					}
					c.Response().Flush()
				case err := <-ended:
					if err == nil {
						break LOOP
					}
					log.Error().Msgf("Stream ended with error: %v", err)

					stopReason := FinishReasonStop
					errorResp := schema.OpenAIResponse{
						ID:      id,
						Created: created,
						Model:   input.Model,
						Choices: []schema.Choice{
							{
								Index:        0,
								FinishReason: &stopReason,
								Text:         "Internal error: " + err.Error(),
							},
						},
						Object: "text_completion",
					}
					errorData, marshalErr := json.Marshal(errorResp)
					if marshalErr != nil {
						log.Error().Msgf("Failed to marshal error response: %v", marshalErr)
						// Send a simple error message as fallback
						fmt.Fprintf(c.Response().Writer, "data: {\"error\":\"Internal error\"}\n\n")
					} else {
						fmt.Fprintf(c.Response().Writer, "data: %s\n\n", string(errorData))
					}
					c.Response().Flush()
					return nil
				}
			}

			stopReason := FinishReasonStop
			resp := &schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{
					{
						Index:        0,
						FinishReason: &stopReason,
					},
				},
				Object: "text_completion",
			}
			respData, _ := json.Marshal(resp)

			fmt.Fprintf(c.Response().Writer, "data: %s\n\n", respData)
			fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
			c.Response().Flush()
			return nil
		}

		var result []schema.Choice

		totalTokenUsage := backend.TokenUsage{}

		for k, i := range config.PromptStrings {
			templatedInput, err := evaluator.EvaluateTemplateForPrompt(templates.CompletionPromptTemplate, *config, templates.PromptTemplateData{
				SystemPrompt:    config.SystemPrompt,
				Input:           i,
				ReasoningEffort: input.ReasoningEffort,
				Metadata:        input.Metadata,
			})
			if err == nil {
				i = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", i)
			}

			r, tokenUsage, err := ComputeChoices(
				input, i, config, cl, appConfig, ml, func(s string, c *[]schema.Choice) {
					stopReason := FinishReasonStop
					*c = append(*c, schema.Choice{Text: s, FinishReason: &stopReason, Index: k})
				}, nil)
			if err != nil {
				return err
			}

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

		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "text_completion",
			Usage:   usage,
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(200, resp)
	}
}
