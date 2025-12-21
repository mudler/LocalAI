package localai

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
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
)

// MCP SSE Event Types
type MCPReasoningEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type MCPToolCallEvent struct {
	Type      string                 `json:"type"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
	Reasoning string                 `json:"reasoning"`
}

type MCPToolResultEvent struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Result string `json:"result"`
}

type MCPStatusEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type MCPAssistantEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type MCPErrorEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// MCPStreamEndpoint is the SSE streaming endpoint for MCP chat completions
// @Summary Stream MCP chat completions with reasoning, tool calls, and results
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/mcp/chat/completions [post]
func MCPStreamEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		created := int(time.Now().Unix())

		// Handle Correlation
		id := c.Request().Header.Get("X-Correlation-ID")
		if id == "" {
			id = fmt.Sprintf("mcp-%d", time.Now().UnixNano())
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

		// Build fragment from messages
		fragment := cogito.NewEmptyFragment()
		for _, message := range input.Messages {
			fragment = fragment.AddMessage(message.Role, message.StringContent)
		}

		_, port, err := net.SplitHostPort(appConfig.APIAddress)
		if err != nil {
			return err
		}
		apiKey := ""
		if len(appConfig.ApiKeys) > 0 {
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
		)
		// Check if streaming is requested
		toStream := input.Stream

		if !toStream {
			// Non-streaming mode: execute synchronously and return JSON response
			cogitoOpts = append(
				cogitoOpts,
				cogito.WithStatusCallback(func(s string) {
					xlog.Debug("[model agent] Status", "model", config.Name, "status", s)
				}),
				cogito.WithReasoningCallback(func(s string) {
					xlog.Debug("[model agent] Reasoning", "model", config.Name, "reasoning", s)
				}),
				cogito.WithToolCallBack(func(t *cogito.ToolChoice, state *cogito.SessionState) cogito.ToolCallDecision {
					xlog.Debug("[model agent] Tool call", "model", config.Name, "tool", t.Name, "reasoning", t.Reasoning, "arguments", t.Arguments)
					return cogito.ToolCallDecision{
						Approved: true,
					}
				}),
				cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
					xlog.Debug("[model agent] Tool call result", "model", config.Name, "tool", t.Name, "result", t.Result, "tool_arguments", t.ToolArguments)
				}),
			)

			f, err := cogito.ExecuteTools(
				defaultLLM, fragment,
				cogitoOpts...,
			)
			if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
				return err
			}

			f, err = defaultLLM.Ask(ctxWithCancellation, f)
			if err != nil {
				return err
			}

			resp := &schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{{Message: &schema.Message{Role: "assistant", Content: &f.LastMessage().Content}}},
				Object:  "chat.completion",
			}

			jsonResult, _ := json.Marshal(resp)
			xlog.Debug("Response", "response", string(jsonResult))

			// Return the prediction in the response body
			return c.JSON(200, resp)
		}

		// Streaming mode: use SSE
		// Set up SSE headers
		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().Header().Set("X-Correlation-ID", id)

		// Create channel for streaming events
		events := make(chan interface{})
		ended := make(chan error, 1)

		// Set up callbacks for streaming
		statusCallback := func(s string) {
			events <- MCPStatusEvent{
				Type:    "status",
				Message: s,
			}
		}

		reasoningCallback := func(s string) {
			events <- MCPReasoningEvent{
				Type:    "reasoning",
				Content: s,
			}
		}

		toolCallCallback := func(t *cogito.ToolChoice, state *cogito.SessionState) cogito.ToolCallDecision {
			events <- MCPToolCallEvent{
				Type:      "tool_call",
				Name:      t.Name,
				Arguments: t.Arguments,
				Reasoning: t.Reasoning,
			}
			return cogito.ToolCallDecision{
				Approved: true,
			}
		}

		toolCallResultCallback := func(t cogito.ToolStatus) {
			events <- MCPToolResultEvent{
				Type:   "tool_result",
				Name:   t.Name,
				Result: t.Result,
			}
		}

		cogitoOpts = append(cogitoOpts,
			cogito.WithStatusCallback(statusCallback),
			cogito.WithReasoningCallback(reasoningCallback),
			cogito.WithToolCallBack(toolCallCallback),
			cogito.WithToolCallResultCallback(toolCallResultCallback),
		)

		// Execute tools in a goroutine
		go func() {
			defer close(events)

			f, err := cogito.ExecuteTools(
				defaultLLM, fragment,
				cogitoOpts...,
			)
			if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
				events <- MCPErrorEvent{
					Type:    "error",
					Message: fmt.Sprintf("Failed to execute tools: %v", err),
				}
				ended <- err
				return
			}

			// Get final response
			f, err = defaultLLM.Ask(ctxWithCancellation, f)
			if err != nil {
				events <- MCPErrorEvent{
					Type:    "error",
					Message: fmt.Sprintf("Failed to get response: %v", err),
				}
				ended <- err
				return
			}

			// Stream final assistant response
			content := f.LastMessage().Content
			events <- MCPAssistantEvent{
				Type:    "assistant",
				Content: content,
			}

			ended <- nil
		}()

		// Stream events to client
	LOOP:
		for {
			select {
			case <-ctx.Done():
				// Context was cancelled (client disconnected or request cancelled)
				xlog.Debug("Request context cancelled, stopping stream")
				cancel()
				break LOOP
			case event := <-events:
				if event == nil {
					// Channel closed
					break LOOP
				}
				eventData, err := json.Marshal(event)
				if err != nil {
					xlog.Debug("Failed to marshal event", "error", err)
					continue
				}
				xlog.Debug("Sending event", "event", string(eventData))
				_, err = fmt.Fprintf(c.Response().Writer, "data: %s\n\n", string(eventData))
				if err != nil {
					xlog.Debug("Sending event failed", "error", err)
					cancel()
					return err
				}
				c.Response().Flush()
			case err := <-ended:
				if err == nil {
					// Send done signal
					fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
					c.Response().Flush()
					break LOOP
				}
				xlog.Error("Stream ended with error", "error", err)
				errorEvent := MCPErrorEvent{
					Type:    "error",
					Message: err.Error(),
				}
				errorData, marshalErr := json.Marshal(errorEvent)
				if marshalErr != nil {
					fmt.Fprintf(c.Response().Writer, "data: {\"type\":\"error\",\"message\":\"Internal error\"}\n\n")
				} else {
					fmt.Fprintf(c.Response().Writer, "data: %s\n\n", string(errorData))
				}
				fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
				c.Response().Flush()
				return nil
			}
		}

		xlog.Debug("Stream ended")
		return nil
	}
}
