package localai

import (
	"fmt"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
)

// MCPServersEndpoint returns the list of MCP servers and their tools for a given model.
// GET /v1/mcp/servers/:model
func MCPServersEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelName := c.Param("model")
		if modelName == "" {
			return echo.ErrBadRequest
		}

		cfg, exists := cl.GetModelConfig(modelName)
		if !exists {
			return fmt.Errorf("model %q not found", modelName)
		}

		if cfg.MCP.Servers == "" && cfg.MCP.Stdio == "" {
			return c.JSON(200, map[string]any{
				"model":   modelName,
				"servers": []any{},
			})
		}

		remote, stdio, err := cfg.MCP.MCPConfigFromYAML()
		if err != nil {
			return fmt.Errorf("failed to parse MCP config: %w", err)
		}

		namedSessions, err := mcpTools.NamedSessionsFromMCPConfig(cfg.Name, remote, stdio, nil)
		if err != nil {
			return fmt.Errorf("failed to get MCP sessions: %w", err)
		}

		servers, err := mcpTools.ListMCPServers(c.Request().Context(), namedSessions)
		if err != nil {
			return fmt.Errorf("failed to list MCP servers: %w", err)
		}

		return c.JSON(200, map[string]any{
			"model":   modelName,
			"servers": servers,
		})
	}
}

// MCPServersEndpointFromMiddleware is a version that uses the middleware-resolved model config.
// This allows it to use the same middleware chain as other endpoints.
func MCPServersEndpointFromMiddleware() echo.HandlerFunc {
	return func(c echo.Context) error {
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		if cfg.MCP.Servers == "" && cfg.MCP.Stdio == "" {
			return c.JSON(200, map[string]any{
				"model":   cfg.Name,
				"servers": []any{},
			})
		}

		remote, stdio, err := cfg.MCP.MCPConfigFromYAML()
		if err != nil {
			return fmt.Errorf("failed to parse MCP config: %w", err)
		}

		namedSessions, err := mcpTools.NamedSessionsFromMCPConfig(cfg.Name, remote, stdio, nil)
		if err != nil {
			return fmt.Errorf("failed to get MCP sessions: %w", err)
		}

		servers, err := mcpTools.ListMCPServers(c.Request().Context(), namedSessions)
		if err != nil {
			return fmt.Errorf("failed to list MCP servers: %w", err)
		}

		return c.JSON(200, map[string]any{
			"model":   cfg.Name,
			"servers": servers,
		})
	}
}
