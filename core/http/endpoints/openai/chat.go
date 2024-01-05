package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/schema"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

func ChatEndpoint(cl *services.ConfigLoader, ml *model.ModelLoader, startupOptions *schema.StartupOptions) func(c *fiber.Ctx) error {

	emptyMessage := ""

	return func(c *fiber.Ctx) error {
		modelName, input, err := readInput(c, startupOptions, ml, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		// The scary comment I feel like I forgot about along the way:
		//
		// functions are not supported in stream mode (yet?)
		//
		if input.Stream {
			log.Debug().Msgf("Stream request received")
			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//	c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")

			responses, err := backend.StreamingChatGenerationOpenAIRequest(modelName, input, cl, ml, startupOptions)
			if err != nil {
				return fmt.Errorf("failed establishing streaming chat request :%w", err)
			}
			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
				usage := &schema.OpenAIUsage{}
				id := ""
				created := 0
				for ev := range responses {
					usage = &ev.Usage // Copy a pointer to the latest usage chunk so that the stop message can reference it
					id = ev.ID
					created = ev.Created // Similarly, grab the ID and created from any / the last response so we can use it for the stop
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					enc.Encode(ev)
					log.Debug().Msgf("Sending chunk: %s", buf.String())
					_, err := fmt.Fprintf(w, "data: %v\n", buf.String())
					if err != nil {
						log.Debug().Msgf("Sending chunk failed: %v", err)
						input.Cancel()
						break
					}
					w.Flush()
				}

				resp := &schema.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{
						{
							FinishReason: "stop",
							Index:        0,
							Delta:        &schema.Message{Content: &emptyMessage},
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
		//////////////////////////////////////////

		resp, err := backend.ChatGenerationOpenAIRequest(modelName, input, cl, ml, startupOptions)
		if err != nil {
			return fmt.Errorf("error generating chat request: +%w", err)
		}
		respData, _ := json.Marshal(resp) // TODO this is only used for the debug log and costs performance. monitor this?
		log.Debug().Msgf("Response: %s", respData)
		return c.JSON(resp)
	}
}
