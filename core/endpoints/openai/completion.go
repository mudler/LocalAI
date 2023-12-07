package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/go-skynet/LocalAI/pkg/grammar"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

// https://platform.openai.com/docs/api-reference/completions
func CompletionEndpoint(cl *backend.ConfigLoader, ml *model.ModelLoader, so *datamodel.StartupOptions) func(c *fiber.Ctx) error {
	id := uuid.New().String()
	created := int(time.Now().Unix())

	process := func(s string, req *datamodel.OpenAIRequest, config *datamodel.Config, loader *model.ModelLoader, responses chan datamodel.OpenAIResponse) {
		ComputeChoices(req, s, config, so, loader, func(s string, c *[]datamodel.Choice) {}, func(s string, usage backend.TokenUsage) bool {
			resp := datamodel.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []datamodel.Choice{
					{
						Index: 0,
						Text:  s,
					},
				},
				Object: "text_completion",
				Usage: datamodel.OpenAIUsage{
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
		modelFile, input, err := readInput(c, so, ml, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("`input`: %+v", input)

		config, input, err := readConfig(modelFile, input, cl, ml, so.Debug, so.Threads, so.ContextSize, so.F16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		if input.ResponseFormat.Type == "json_object" {
			input.Grammar = grammar.JSONBNF
		}

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

		templateFile := config.Model

		if config.TemplateConfig.Completion != "" {
			templateFile = config.TemplateConfig.Completion
		}

		if input.Stream {
			if len(config.PromptStrings) > 1 {
				return errors.New("cannot handle more than 1 `PromptStrings` when Streaming")
			}

			predInput := config.PromptStrings[0]

			// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
			templatedInput, err := ml.EvaluateTemplateForPrompt(model.CompletionPromptTemplate, templateFile, model.PromptTemplateData{
				Input: predInput,
			})
			if err == nil {
				predInput = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", predInput)
			}

			responses := make(chan datamodel.OpenAIResponse)

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

				resp := &datamodel.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []datamodel.Choice{
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

		var result []datamodel.Choice

		totalTokenUsage := backend.TokenUsage{}

		for k, i := range config.PromptStrings {
			// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
			templatedInput, err := ml.EvaluateTemplateForPrompt(model.CompletionPromptTemplate, templateFile, model.PromptTemplateData{
				SystemPrompt: config.SystemPrompt,
				Input:        i,
			})
			if err == nil {
				i = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", i)
			}

			r, tokenUsage, err := ComputeChoices(
				input, i, config, so, ml, func(s string, c *[]datamodel.Choice) {
					*c = append(*c, datamodel.Choice{Text: s, FinishReason: "stop", Index: k})
				}, nil)
			if err != nil {
				return err
			}

			totalTokenUsage.Prompt += tokenUsage.Prompt
			totalTokenUsage.Completion += tokenUsage.Completion

			result = append(result, r...)
		}

		resp := &datamodel.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "text_completion",
			Usage: datamodel.OpenAIUsage{
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
