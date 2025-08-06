package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

// CompletionEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/completions
// @Summary Generate completions for a given prompt and model.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/completions [post]
func CompletionEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	created := int(time.Now().Unix())

	process := func(id string, s string, req *schema.OpenAIRequest, config *config.BackendConfig, loader *model.ModelLoader, responses chan schema.OpenAIResponse, extraUsage bool) {
		tokenCallback := func(s string, tokenUsage backend.TokenUsage) bool {
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
						Index: 0,
						Text:  s,
					},
				},
				Object: "text_completion",
				Usage:  usage,
			}
			log.Debug().Msgf("Sending goroutine: %s", s)

			responses <- resp
			return true
		}
		ComputeChoices(req, s, config, cl, appConfig, loader, func(s string, c *[]schema.Choice) {}, tokenCallback)
		close(responses)
	}

	return func(c *fiber.Ctx) error {
		// Handle Correlation
		id := c.Get("X-Correlation-ID", uuid.New().String())
		extraUsage := c.Get("Extra-Usage", "") != ""

		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return fiber.ErrBadRequest
		}

		config, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.BackendConfig)
		if !ok || config == nil {
			return fiber.ErrBadRequest
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
			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")
		}

		if input.Stream {
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

			go process(id, predInput, input, config, ml, responses, extraUsage)

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {

				for ev := range responses {
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					enc.Encode(ev)

					log.Debug().Msgf("Sending chunk: %s", buf.String())
					fmt.Fprintf(w, "data: %v\n", buf.String())
					w.Flush()
				}

				resp := &schema.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{
						{
							Index:        0,
							FinishReason: "stop",
						},
					},
					Object: "text_completion",
				}
				respData, _ := json.Marshal(resp)

				w.WriteString(fmt.Sprintf("data: %s\n\n", respData))
				w.WriteString("data: [DONE]\n\n")
				w.Flush()
			}))
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
					*c = append(*c, schema.Choice{Text: s, FinishReason: "stop", Index: k})
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
		return c.JSON(resp)
	}
}
