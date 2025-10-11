package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
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

		ctx := c.Context()

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

		if config.MCP.Servers == "" && config.MCP.Stdio == "" {
			return fmt.Errorf("no MCP servers configured")
		}

		// Get MCP config from model config
		remote, stdio := config.MCP.MCPConfigFromYAML()

		// Check if we have tools in cache, or we have to have an initial connection
		sessions, err := mcpTools.SessionsFromMCPConfig(config.Name, remote, stdio)
		if err != nil {
			return err
		}

		if len(sessions) == 0 {
			return fmt.Errorf("no working MCP servers found")
		}

		fragment := cogito.NewEmptyFragment()

		for _, message := range input.Messages {
			fragment = fragment.AddMessage(message.Role, message.StringContent)
		}

		port := appConfig.APIAddress[strings.LastIndex(appConfig.APIAddress, ":")+1:]
		apiKey := ""
		if appConfig.ApiKeys != nil {
			apiKey = appConfig.ApiKeys[0]
		}
		// TODO: instead of connecting to the API, we should just wire this internally
		// and act like completion.go.
		// We can do this as cogito expects an interface and we can create one that
		// we satisfy to just call internally ComputeChoices
		defaultLLM := cogito.NewOpenAILLM(config.Name, apiKey, "http://127.0.0.1:"+port)

		cogitoOpts := []cogito.Option{
			cogito.WithStatusCallback(func(s string) {
				log.Debug().Msgf("[model agent] [model: %s] Status: %s", config.Name, s)
			}),
			cogito.WithContext(ctx),
			cogito.WithMCPs(sessions...),
			cogito.WithIterations(3),  // default to 3 iterations
			cogito.WithMaxAttempts(3), // default to 3 attempts
		}

		if config.Agent.EnableReasoning {
			cogitoOpts = append(cogitoOpts, cogito.EnableToolReasoner)
		}

		if config.Agent.EnableReEvaluation {
			cogitoOpts = append(cogitoOpts, cogito.EnableToolReEvaluator)
		}

		if config.Agent.MaxIterations != 0 {
			cogitoOpts = append(cogitoOpts, cogito.WithIterations(config.Agent.MaxIterations))
		}

		if config.Agent.MaxAttempts != 0 {
			cogitoOpts = append(cogitoOpts, cogito.WithMaxAttempts(config.Agent.MaxAttempts))
		}

		f, err := cogito.ExecuteTools(
			defaultLLM, fragment,
			cogitoOpts...,
		)
		if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
			return err
		}

		f, err = defaultLLM.Ask(ctx, f)
		if err != nil {
			return err
		}

		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: []schema.Choice{{Message: &schema.Message{Role: "assistant", Content: &f.LastMessage().Content}}},
			Object:  "text_completion",
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
