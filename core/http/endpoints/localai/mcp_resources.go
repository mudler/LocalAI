package localai

import (
	"fmt"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
)

// MCPResourcesEndpoint returns the list of MCP resources for a given model.
// GET /v1/mcp/resources/:model
func MCPResourcesEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
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
			return c.JSON(200, []any{})
		}

		remote, stdio, err := cfg.MCP.MCPConfigFromYAML()
		if err != nil {
			return fmt.Errorf("failed to parse MCP config: %w", err)
		}

		namedSessions, err := mcpTools.NamedSessionsFromMCPConfig(cfg.Name, remote, stdio, nil)
		if err != nil {
			return fmt.Errorf("failed to get MCP sessions: %w", err)
		}

		resources, err := mcpTools.DiscoverMCPResources(c.Request().Context(), namedSessions)
		if err != nil {
			return fmt.Errorf("failed to discover MCP resources: %w", err)
		}

		type resourceJSON struct {
			Name        string `json:"name"`
			URI         string `json:"uri"`
			Description string `json:"description,omitempty"`
			MIMEType    string `json:"mimeType,omitempty"`
			Server      string `json:"server"`
		}

		var result []resourceJSON
		for _, r := range resources {
			result = append(result, resourceJSON{
				Name:        r.Name,
				URI:         r.URI,
				Description: r.Description,
				MIMEType:    r.MIMEType,
				Server:      r.ServerName,
			})
		}

		return c.JSON(200, result)
	}
}

// MCPReadResourceEndpoint reads a specific MCP resource by URI.
// POST /v1/mcp/resources/:model/read
func MCPReadResourceEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
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
			return fmt.Errorf("no MCP servers configured for model %q", modelName)
		}

		var req struct {
			URI string `json:"uri"`
		}
		if err := c.Bind(&req); err != nil || req.URI == "" {
			return echo.ErrBadRequest
		}

		remote, stdio, err := cfg.MCP.MCPConfigFromYAML()
		if err != nil {
			return fmt.Errorf("failed to parse MCP config: %w", err)
		}

		namedSessions, err := mcpTools.NamedSessionsFromMCPConfig(cfg.Name, remote, stdio, nil)
		if err != nil {
			return fmt.Errorf("failed to get MCP sessions: %w", err)
		}

		resources, err := mcpTools.DiscoverMCPResources(c.Request().Context(), namedSessions)
		if err != nil {
			return fmt.Errorf("failed to discover MCP resources: %w", err)
		}

		content, err := mcpTools.ReadMCPResource(c.Request().Context(), resources, req.URI)
		if err != nil {
			return fmt.Errorf("failed to read resource: %w", err)
		}

		// Find the resource info for mimeType
		mimeType := ""
		for _, r := range resources {
			if r.URI == req.URI {
				mimeType = r.MIMEType
				break
			}
		}

		return c.JSON(200, map[string]any{
			"uri":      req.URI,
			"content":  content,
			"mimeType": mimeType,
		})
	}
}
