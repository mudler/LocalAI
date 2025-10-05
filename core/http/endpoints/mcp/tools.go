package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/sashabaranov/go-openai"
	"github.com/tmc/langchaingo/jsonschema"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
)

func ToolsFromMCPConfig(ctx context.Context, remote config.MCPGenericConfig[config.MCPRemoteServers], stdio config.MCPGenericConfig[config.MCPSTDIOServers]) ([]*MCPTool, error) {
	allTools := []*MCPTool{}

	// Get the list of all the tools that the Agent will be esposed to
	for _, server := range remote.Servers {

		// Create HTTP client with custom roundtripper for bearer token injection
		client := &http.Client{
			Timeout:   360 * time.Second,
			Transport: newBearerTokenRoundTripper(server.Token, http.DefaultTransport),
		}

		tools, err := mcpToolsFromTransport(ctx,
			&mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client},
		)
		if err != nil {
			return nil, err
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
		tools, err := mcpToolsFromTransport(ctx,
			&mcp.CommandTransport{
				Command: command},
		)
		if err != nil {
			return nil, err
		}

		allTools = append(allTools, tools...)
	}

	return allTools, nil
}

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

type MCPTool struct {
	name, description string
	inputSchema       ToolInputSchema
	session           *mcp.ClientSession
	ctx               context.Context
	props             map[string]jsonschema.Definition
}

func (t *MCPTool) Run(args map[string]any) (string, error) {

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

func (t *MCPTool) Tool() openai.Tool {

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

func (t *MCPTool) Close() {
	t.session.Close()
}

type ToolInputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

// probe the MCP remote and generate tools that are compliant with cogito
// TODO: Maybe move this to cogito?
func mcpToolsFromTransport(ctx context.Context, transport mcp.Transport) ([]*MCPTool, error) {
	allTools := []*MCPTool{}

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

		allTools = append(allTools, &MCPTool{
			name:        tool.Name,
			description: tool.Description,
			session:     session,
			ctx:         ctx,
			props:       props,
			inputSchema: inputSchema,
		})
	}

	// We make sure we run Close on signal
	handleSignal(allTools)

	return allTools, nil
}

func handleSignal(tools []*MCPTool) {

	// Create a channel to receive OS signals
	sigChan := make(chan os.Signal, 1)

	// Register for interrupt and terminate signals
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Handle signals in a separate goroutine
	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down gracefully...", sig)

		for _, t := range tools {
			t.Close()
		}

		// Exit the application
		os.Exit(0)
	}()
}
