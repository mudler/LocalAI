package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/sashabaranov/go-openai"
	"github.com/tmc/langchaingo/jsonschema"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/cogito"
	"github.com/rs/zerolog/log"
)

// bearerTokenRoundTripper is a custom roundtripper that injects a bearer token
// into HTTP requests
type bearerTokenRoundTripper struct {
	token string
	base  http.RoundTripper
}

// RoundTrip implements the http.RoundTripper interface
func (rt *bearerTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.token != "" {
		req.Header.Set("Authorization", "Bearer "+rt.token)
	}
	return rt.base.RoundTrip(req)
}

// newBearerTokenRoundTripper creates a new roundtripper that injects the given token
func newBearerTokenRoundTripper(token string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &bearerTokenRoundTripper{
		token: token,
		base:  base,
	}
}

type mcpTool struct {
	name, description string
	inputSchema       ToolInputSchema
	session           *mcp.ClientSession
	ctx               context.Context
	props             map[string]jsonschema.Definition
}

func (t *mcpTool) Run(args map[string]any) (string, error) {

	// Call a tool on the server.
	params := &mcp.CallToolParams{
		Name:      t.name,
		Arguments: args,
	}
	res, err := t.session.CallTool(t.ctx, params)
	if err != nil {
		log.Error().Msgf("CallTool failed: %v", err)
		return "", err
	}
	if res.IsError {
		log.Error().Msgf("tool failed")
		return "", errors.New("tool failed")
	}

	result := ""
	for _, c := range res.Content {
		result += c.(*mcp.TextContent).Text
	}

	return result, nil
}

func (t *mcpTool) Tool() openai.Tool {

	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        t.name,
			Description: t.description,
			Parameters: jsonschema.Definition{
				Type:       jsonschema.Object,
				Properties: t.props,
				Required:   t.inputSchema.Required,
			},
		},
	}
}

func (t *mcpTool) Close() {
	t.session.Close()
}

type ToolInputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

// probe the MCP remote and generate tools that are compliant with cogito
// TODO: Maybe move this to cogito?
func newMCPTools(ctx context.Context, transport mcp.Transport) ([]cogito.Tool, error) {
	allTools := []cogito.Tool{}

	// Create a new client, with no features.
	client := mcp.NewClient(&mcp.Implementation{Name: "LocalAI", Version: "v1.0.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		log.Error().Msgf("Error connecting to MCP server: %v", err)
		return nil, err
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		log.Error().Msgf("Error listing tools: %v", err)
		return nil, err
	}

	for _, tool := range tools.Tools {
		dat, err := json.Marshal(tool.InputSchema)
		if err != nil {
			log.Error().Msgf("Error marshalling input schema: %v", err)
			continue
		}

		// XXX: This is a wild guess, to verify (data types might be incompatible)
		var inputSchema ToolInputSchema
		err = json.Unmarshal(dat, &inputSchema)
		if err != nil {
			log.Error().Msgf("Error unmarshalling input schema: %v", err)
			continue
		}

		props := map[string]jsonschema.Definition{}
		dat, err = json.Marshal(inputSchema.Properties)
		if err != nil {
			log.Error().Msgf("Error marshalling input schema: %v", err)
			continue
		}
		err = json.Unmarshal(dat, &props)
		if err != nil {
			log.Error().Msgf("Error unmarshalling input schema properties: %v", err)
			continue
		}

		allTools = append(allTools, &mcpTool{
			name:        tool.Name,
			session:     session,
			ctx:         ctx,
			props:       props,
			inputSchema: inputSchema,
		})
	}

	return allTools, nil
}

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

		allTools := []cogito.Tool{}

		// TODO: we should cache the MCP clients somehow, and not re-create these for each request.
		remote, stdio := config.MCP.MCPConfigFromYAML()

		for _, server := range remote.Servers {

			// Create HTTP client with custom roundtripper for bearer token injection
			client := &http.Client{
				Timeout:   360 * time.Second,
				Transport: newBearerTokenRoundTripper(server.Token, http.DefaultTransport),
			}

			tools, err := newMCPTools(c.Context(),
				&mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client},
			)
			if err != nil {
				return err
			}
			allTools = append(allTools, tools...)
		}

		for _, server := range stdio.Servers {

			log.Debug().Msgf("[MCP stdio server] Configuration : %+v", server)
			command := exec.Command(server.Command, server.Args...)
			command.Env = os.Environ()
			for key, value := range server.Env {
				command.Env = append(command.Env, key+"="+value)
			}
			tools, err := newMCPTools(c.Context(),
				&mcp.CommandTransport{
					Command: command},
			)
			if err != nil {
				return err
			}
			allTools = append(allTools, tools...)
		}

		for _, tool := range allTools {
			defer tool.(*mcpTool).Close()
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
		defaultLLM := cogito.NewOpenAILLM(config.Model, apiKey, "http://127.0.0.1:"+port)

		f, err := cogito.ExecuteTools(
			defaultLLM, fragment,
			cogito.WithStatusCallback(func(s string) {
				log.Debug().Msgf("[model agent] [model:] Status: %s", s)
			}),
			cogito.WithTools(
				allTools...,
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
