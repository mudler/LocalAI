package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/cogito"

	"github.com/rs/zerolog/log"
)

// MCPCompletionEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/completions
// @Summary Generate completions for a given prompt and model.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /mcp/v1/completions [post]
func MCPCompletionEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {

	// We do not support streaming mode (Yet?)
	return func(c *fiber.Ctx) error {
		created := int(time.Now().Unix())

		// Handle Correlation
		id := c.Get("X-Correlation-ID", uuid.New().String())

		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return fiber.ErrBadRequest
		}

		config, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			return fiber.ErrBadRequest
		}

		fragment := cogito.NewEmptyFragment()

		for _, message := range input.Messages {
			fragment = fragment.AddMessage(message.Role, message.StringContent)
		}

		// TODO: we need to get the IP/PORT of the API here, and an api key if set
		defaultLLM := cogito.NewOpenAILLM(config.Model, "apikey", ":8080")

		f, err := cogito.ExecuteTools(
			defaultLLM, fragment,
			cogito.WithStatusCallback(func(s string) {
				fmt.Println("___________________ START STATUS _________________")
				fmt.Println(s)
				fmt.Println("___________________ END STATUS _________________")
			}),
			cogito.WithTools(
			// TODO: fill with MCP settings
			),
		)
		if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
			return err
		}

		fragment, err = defaultLLM.Ask(c.Context(), fragment)
		if err != nil {
			return err
		}

		fmt.Println(f.LastMessage().Content)

		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: []schema.Choice{{Text: fragment.LastMessage().Content}},
			Object:  "text_completion",
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
