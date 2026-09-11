package localai

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
)

// MCPServersEndpoint returns the list of MCP servers and their tools for a given model.
// GET /v1/mcp/servers/:model
func MCPServersEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, natsClient mcpTools.MCPNATSClient) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelName := c.Param("model")
		if modelName == "" {
			return echo.ErrBadRequest
		}

		cfg, exists := cl.GetModelConfig(modelName)
		if !exists {
			return c.JSON(http.StatusNotFound, map[string]any{
				"model":   modelName,
				"servers": []any{},
				"error":   fmt.Sprintf("model %q not found", modelName),
			})
		}

		if cfg.MCP.Servers == "" && cfg.MCP.Stdio == "" {
			return c.JSON(200, map[string]any{
				"model":   modelName,
				"servers": []any{},
			})
		}

		remote, stdio, err := cfg.MCP.MCPConfigFromYAML()
		if err != nil {
			return c.JSON(http.StatusUnprocessableEntity, map[string]any{
				"model":   modelName,
				"servers": []any{},
				"error":   fmt.Sprintf("failed to parse MCP config: %v", err),
			})
		}

		// In distributed mode, route discovery through NATS to an agent worker
		// that can actually connect to the MCP servers.
		if natsClient != nil {
			resp, err := mcpTools.DiscoverMCPToolsRemote(c.Request().Context(), natsClient, cfg.Name, remote, stdio)
			if err != nil {
				return c.JSON(http.StatusOK, map[string]any{
					"model":   modelName,
					"servers": unavailableMCPServers(remote, stdio, fmt.Sprintf("remote discovery failed: %v", err)),
				})
			}
			return c.JSON(200, map[string]any{
				"model":   modelName,
				"servers": resp.Servers,
			})
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
func MCPServersEndpointFromMiddleware(natsClient mcpTools.MCPNATSClient) echo.HandlerFunc {
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
			return c.JSON(http.StatusUnprocessableEntity, map[string]any{
				"model":   cfg.Name,
				"servers": []any{},
				"error":   fmt.Sprintf("failed to parse MCP config: %v", err),
			})
		}

		// In distributed mode, route discovery through NATS to an agent worker.
		if natsClient != nil {
			resp, err := mcpTools.DiscoverMCPToolsRemote(c.Request().Context(), natsClient, cfg.Name, remote, stdio)
			if err != nil {
				return c.JSON(http.StatusOK, map[string]any{
					"model":   cfg.Name,
					"servers": unavailableMCPServers(remote, stdio, fmt.Sprintf("remote discovery failed: %v", err)),
				})
			}
			return c.JSON(200, map[string]any{
				"model":   cfg.Name,
				"servers": resp.Servers,
			})
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

func unavailableMCPServers(
	remote config.MCPGenericConfig[config.MCPRemoteServers],
	stdio config.MCPGenericConfig[config.MCPSTDIOServers],
	errMessage string,
) []mcpTools.MCPServerInfo {
	servers := make([]mcpTools.MCPServerInfo, 0, len(remote.Servers)+len(stdio.Servers))
	for name := range remote.Servers {
		servers = append(servers, mcpTools.MCPServerInfo{Name: name, Type: "remote", Tools: []string{}, Error: errMessage})
	}
	for name := range stdio.Servers {
		servers = append(servers, mcpTools.MCPServerInfo{Name: name, Type: "stdio", Tools: []string{}, Error: errMessage})
	}
	sort.Slice(servers, func(i, j int) bool {
		if servers[i].Type != servers[j].Type {
			return servers[i].Type < servers[j].Type
		}
		return servers[i].Name < servers[j].Name
	})
	return servers
}
