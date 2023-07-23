package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

// https://platform.openai.com/docs/api-reference/completions
func CompletionEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	process := func(s string, req *OpenAIRequest, config *config.Config, loader *model.ModelLoader, responses chan OpenAIResponse) {
		ComputeChoices(req, s, config, o, loader, func(s string, c *[]Choice) {}, func(s string) bool {
			resp := OpenAIResponse{
				Model: req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []Choice{
					{
						Index: 0,
						Text:  s,
					},
				},
				Object: "text_completion",
			}
			log.Debug().Msgf("Sending goroutine: %s", s)

			responses <- resp
			return true
		})
		close(responses)
	}

	return func(c *fiber.Ctx) error {
		modelFile, input, err := readInput(c, o, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("`input`: %+v", input)

		config, input, err := readConfig(modelFile, input, cm, o.Loader, o.Debug, o.Threads, o.ContextSize, o.F16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
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
			templatedInput, err := o.Loader.EvaluateTemplateForPrompt(model.CompletionPromptTemplate, templateFile, model.PromptTemplateData{
				Input: predInput,
			})
			if err == nil {
				predInput = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", predInput)
			}

			responses := make(chan OpenAIResponse)

			go process(predInput, input, config, o.Loader, responses)

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {

				for ev := range responses {
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					enc.Encode(ev)

					log.Debug().Msgf("Sending chunk: %s", buf.String())
					fmt.Fprintf(w, "data: %v\n", buf.String())
					w.Flush()
				}

				resp := &OpenAIResponse{
					Model: input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []Choice{
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

		var result []Choice
		for k, i := range config.PromptStrings {
			// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
			templatedInput, err := o.Loader.EvaluateTemplateForPrompt(model.CompletionPromptTemplate, templateFile, model.PromptTemplateData{
				Input: i,
			})
			if err == nil {
				i = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", i)
			}

			r, err := ComputeChoices(input, i, config, o, o.Loader, func(s string, c *[]Choice) {
				*c = append(*c, Choice{Text: s, FinishReason: "stop", Index: k})
			}, nil)
			if err != nil {
				return err
			}

			result = append(result, r...)
		}

		resp := &OpenAIResponse{
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "text_completion",
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
