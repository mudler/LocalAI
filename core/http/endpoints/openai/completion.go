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

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/templates"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

// CompletionEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/completions
// @Summary Generate completions for a given prompt and model.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/completions [post]
func CompletionEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	id := uuid.New().String()
	created := int(time.Now().Unix())

	process := func(s string, req *schema.OpenAIRequest, config *config.BackendConfig, loader *model.ModelLoader, responses chan schema.OpenAIResponse) {
		ComputeChoices(req, s, config, appConfig, loader, func(s string, c *[]schema.Choice) {}, func(s string, usage backend.TokenUsage) bool {
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
				Usage: schema.OpenAIUsage{
					PromptTokens:     usage.Prompt,
					CompletionTokens: usage.Completion,
					TotalTokens:      usage.Prompt + usage.Completion,
				},
			}
			log.Debug().Msgf("Sending goroutine: %s", s)

			responses <- resp
			return true
		})
		close(responses)
	}

	return func(c *fiber.Ctx) error {
		// Add Correlation
		c.Set("X-Correlation-ID", id)
		modelFile, input, err := readRequest(c, cl, ml, appConfig, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("`input`: %+v", input)

		config, input, err := mergeRequestWithConfig(modelFile, input, cl, ml, appConfig.Debug, appConfig.Threads, appConfig.ContextSize, appConfig.F16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
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
				Input:        predInput,
				SystemPrompt: config.SystemPrompt,
			})
			if err == nil {
				predInput = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", predInput)
			}

			responses := make(chan schema.OpenAIResponse)

			go process(predInput, input, config, ml, responses)

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
				SystemPrompt: config.SystemPrompt,
				Input:        i,
			})
			if err == nil {
				i = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", i)
			}

			r, tokenUsage, err := ComputeChoices(
				input, i, config, appConfig, ml, func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s, FinishReason: "stop", Index: k})
				}, nil)
			if err != nil {
				return err
			}

			totalTokenUsage.Prompt += tokenUsage.Prompt
			totalTokenUsage.Completion += tokenUsage.Completion

			result = append(result, r...)
		}

		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "text_completion",
			Usage: schema.OpenAIUsage{
				PromptTokens:     totalTokenUsage.Prompt,
				CompletionTokens: totalTokenUsage.Completion,
				TotalTokens:      totalTokenUsage.Prompt + totalTokenUsage.Completion,
			},
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
