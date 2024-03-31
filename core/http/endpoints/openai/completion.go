package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"

	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"
	"github.com/go-skynet/LocalAI/core/services"

	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

// CompletionEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/completions
// @Summary Generate completions for a given prompt and model.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/completions [post]
func CompletionEndpoint(fce *fiberContext.FiberContextExtractor, oais *services.OpenAIService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		_, request, err := fce.OpenAIRequestFromContext(c, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("`OpenAIRequest`: %+v", request)

		traceID, finalResultChannel, _, _, tokenChannel, err := oais.Completion(request, false, request.Stream)
		if err != nil {
			return err
		}

		if request.Stream {
			log.Debug().Msgf("Completion Stream request received")

			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
				for ev := range tokenChannel {
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					if ev.Error != nil {
						log.Debug().Msgf("[CompletionEndpoint] error to debug during tokenChannel handler: %q", ev.Error)
						enc.Encode(ev.Error)
					} else {
						enc.Encode(ev.Value)
					}

					log.Debug().Msgf("completion streaming sending chunk: %s", buf.String())
					fmt.Fprintf(w, "data: %v\n", buf.String())
					w.Flush()
				}

				resp := &schema.OpenAIResponse{
					ID:      traceID.ID,
					Created: traceID.Created,
					Model:   request.Model, // we have to return what the user sent here, due to OpenAI spec.
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
		// TODO is this proper to have exclusive from Stream, or do we need to issue both responses?
		rawResponse := <-finalResultChannel
		if rawResponse.Error != nil {
			return rawResponse.Error
		}
		jsonResult, _ := json.Marshal(rawResponse.Value)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(rawResponse.Value)
	}
}
