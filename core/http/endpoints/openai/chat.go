package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"

	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

// ChatEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/chat/create
// @Summary Generate a chat completions for a given prompt and model.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/chat/completions [post]
func ChatEndpoint(fce *fiberContext.FiberContextExtractor, oais *services.OpenAIService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		_, request, err := fce.OpenAIRequestFromContext(c, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request: %w", err)
		}

		traceID, finalResultChannel, _, tokenChannel, err := oais.Chat(request, false, request.Stream)
		if err != nil {
			return err
		}

		if request.Stream {

			log.Debug().Msgf("Chat Stream request received")

			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
				usage := &schema.OpenAIUsage{}
				toolsCalled := false
				for ev := range tokenChannel {
					if ev.Error != nil {
						log.Debug().Err(ev.Error).Msg("chat streaming responseChannel error")
						request.Cancel()
						break
					}
					usage = &ev.Value.Usage // Copy a pointer to the latest usage chunk so that the stop message can reference it

					if len(ev.Value.Choices[0].Delta.ToolCalls) > 0 {
						toolsCalled = true
					}
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					if ev.Error != nil {
						log.Debug().Err(ev.Error).Msg("[ChatEndpoint] error to debug during tokenChannel handler")
						enc.Encode(ev.Error)
					} else {
						enc.Encode(ev.Value)
					}
					log.Debug().Msgf("chat streaming sending chunk: %s", buf.String())
					_, err := fmt.Fprintf(w, "data: %v\n", buf.String())
					if err != nil {
						log.Debug().Err(err).Msgf("Sending chunk failed")
						request.Cancel()
						break
					}
					err = w.Flush()
					if err != nil {
						log.Debug().Msg("error while flushing, closing connection")
						request.Cancel()
						break
					}
				}

				finishReason := "stop"
				if toolsCalled {
					finishReason = "tool_calls"
				} else if toolsCalled && len(request.Tools) == 0 {
					finishReason = "function_call"
				}

				resp := &schema.OpenAIResponse{
					ID:      traceID.ID,
					Created: traceID.Created,
					Model:   request.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{
						{
							FinishReason: finishReason,
							Index:        0,
							Delta:        &schema.Message{Content: ""},
						}},
					Object: "chat.completion.chunk",
					Usage:  *usage,
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
		log.Debug().Str("jsonResult", string(jsonResult)).Msg("Chat Final Response")

		// Return the prediction in the response body
		return c.JSON(rawResponse.Value)
	}
}
