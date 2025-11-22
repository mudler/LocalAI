package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"

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
func MCPCompletionEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	// We do not support streaming mode (Yet?)
	return func(c echo.Context) error {
		created := int(time.Now().Unix())

		ctx := c.Request().Context()

		// Handle Correlation
		id := c.Request().Header.Get("X-Correlation-ID")
		if id == "" {
			id = uuid.New().String()
		}

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		config, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			return echo.ErrBadRequest
		}

		if config.MCP.Servers == "" && config.MCP.Stdio == "" {
			return fmt.Errorf("no MCP servers configured")
		}

		// Get MCP config from model config
		remote, stdio, err := config.MCP.MCPConfigFromYAML()
		if err != nil {
			return fmt.Errorf("failed to get MCP config: %w", err)
		}

		// Check if we have tools in cache, or we have to have an initial connection
		sessions, err := mcpTools.SessionsFromMCPConfig(config.Name, remote, stdio)
		if err != nil {
			return fmt.Errorf("failed to get MCP sessions: %w", err)
		}

		if len(sessions) == 0 {
			return fmt.Errorf("no working MCP servers found")
		}

		fragment := cogito.NewEmptyFragment()

		for _, message := range input.Messages {
			fragment = fragment.AddMessage(message.Role, message.StringContent)
		}

		_, port, err := net.SplitHostPort(appConfig.APIAddress)
		if err != nil {
			return err
		}

		apiKey := ""
		if appConfig.ApiKeys != nil {
			apiKey = appConfig.ApiKeys[0]
		}

		ctxWithCancellation, cancel := context.WithCancel(ctx)
		defer cancel()

		// TODO: instead of connecting to the API, we should just wire this internally
		// and act like completion.go.
		// We can do this as cogito expects an interface and we can create one that
		// we satisfy to just call internally ComputeChoices
		defaultLLM := cogito.NewOpenAILLM(config.Name, apiKey, "http://127.0.0.1:"+port)

		// Build cogito options using the consolidated method
		cogitoOpts := config.BuildCogitoOptions()

		cogitoOpts = append(
			cogitoOpts,
			cogito.WithContext(ctxWithCancellation),
			cogito.WithMCPs(sessions...),
			cogito.WithStatusCallback(func(s string) {
				log.Debug().Msgf("[model agent] [model: %s] Status: %s", config.Name, s)
			}),
			cogito.WithReasoningCallback(func(s string) {
				log.Debug().Msgf("[model agent] [model: %s] Reasoning: %s", config.Name, s)
			}),
			cogito.WithToolCallBack(func(t *cogito.ToolChoice) bool {
				log.Debug().Msgf("[model agent] [model: %s] Tool call: %s, reasoning: %s, arguments: %+v", config.Name, t.Name, t.Reasoning, t.Arguments)
				return true
			}),
			cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
				log.Debug().Msgf("[model agent] [model: %s] Tool call result: %s, result: %s, tool arguments: %+v", config.Name, t.Name, t.Result, t.ToolArguments)
			}),
		)

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
		return c.JSON(200, resp)
	}
}
