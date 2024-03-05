package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-skynet/LocalAI/core/backend"
	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"

	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/grammar"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

// https://platform.openai.com/docs/api-reference/completions
func CompletionEndpoint(fce *fiberContext.FiberContextExtractor, llmbs *backend.LLMBackendService) func(c *fiber.Ctx) error {
	id := uuid.New().String()
	created := int(time.Now().Unix())

	return func(c *fiber.Ctx) error {
		_, request, err := fce.OpenAIRequestFromContext(c, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("`OpenAIRequest`: %+v", request)

		// bc, request, err := llmbs.GetConfig(request)
		// if err != nil {
		// 	return err
		// }

		if request.Stream {
			log.Debug().Msgf("Stream request received")
			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")

			// if len(bc.PromptStrings) > 1 {
			// 	return errors.New("cannot handle more than 1 `PromptStrings` when Streaming")
			// }

			// predInput := bc.PromptStrings[0]

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
			if templateFile != "" {
				// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
				templatedInput, err := ml.EvaluateTemplateForPrompt(model.CompletionPromptTemplate, templateFile, model.PromptTemplateData{
					SystemPrompt: config.SystemPrompt,
					Input:        i,
				})
				if err == nil {
					i = templatedInput
					log.Debug().Msgf("Template found, input modified to: %s", i)
				}
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
