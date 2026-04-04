package localai

import (
	"fmt"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
)

// MCP SSE Event Types (kept for backward compatibility with MCP endpoint consumers)
type MCPReasoningEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type MCPToolCallEvent struct {
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Reasoning string         `json:"reasoning"`
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

// MCPEndpoint is the endpoint for MCP chat completions.
// It enables all MCP servers for the model and delegates to the standard chat endpoint,
// which handles MCP tool injection and server-side execution.
// Both streaming and non-streaming modes use standard OpenAI response format.
// @Summary MCP chat completions with automatic tool execution
// @Tags mcp
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/mcp/chat/completions [post]
func MCPEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig, natsClient mcpTools.MCPNATSClient) echo.HandlerFunc {
	chatHandler := openai.ChatEndpoint(cl, ml, evaluator, appConfig, natsClient)

	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		modelConfig, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || modelConfig == nil {
			return echo.ErrBadRequest
		}

		if modelConfig.MCP.Servers == "" && modelConfig.MCP.Stdio == "" {
			return fmt.Errorf("no MCP servers configured")
		}

		// Enable all MCP servers if none explicitly specified (preserve original behavior)
		if input.Metadata == nil {
			input.Metadata = map[string]string{}
		}
		if _, hasMCP := input.Metadata["mcp_servers"]; !hasMCP {
			remote, stdio, err := modelConfig.MCP.MCPConfigFromYAML()
			if err != nil {
				return fmt.Errorf("failed to get MCP config: %w", err)
			}
			var allServers []string
			for name := range remote.Servers {
				allServers = append(allServers, name)
			}
			for name := range stdio.Servers {
				allServers = append(allServers, name)
			}
			input.Metadata["mcp_servers"] = strings.Join(allServers, ",")
		}

		// Delegate to the standard chat endpoint which handles MCP tool
		// injection and server-side execution for both streaming and non-streaming.
		return chatHandler(c)
	}
}
