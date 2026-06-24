package localai

import (
	"fmt"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
)

// MCPPromptsEndpoint returns the list of MCP prompts for a given model.
// GET /v1/mcp/prompts/:model
func MCPPromptsEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
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

		prompts, err := mcpTools.DiscoverMCPPrompts(c.Request().Context(), namedSessions)
		if err != nil {
			return fmt.Errorf("failed to discover MCP prompts: %w", err)
		}

		type promptArgJSON struct {
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			Required    bool   `json:"required,omitempty"`
		}
		type promptJSON struct {
			Name        string          `json:"name"`
			Description string          `json:"description,omitempty"`
			Title       string          `json:"title,omitempty"`
			Arguments   []promptArgJSON `json:"arguments,omitempty"`
			Server      string          `json:"server"`
		}

		var result []promptJSON
		for _, p := range prompts {
			pj := promptJSON{
				Name:        p.PromptName,
				Description: p.Description,
				Title:       p.Title,
				Server:      p.ServerName,
			}
			for _, arg := range p.Arguments {
				pj.Arguments = append(pj.Arguments, promptArgJSON{
					Name:        arg.Name,
					Description: arg.Description,
					Required:    arg.Required,
				})
			}
			result = append(result, pj)
		}

		return c.JSON(200, result)
	}
}

// MCPGetPromptEndpoint expands a prompt by name with the given arguments.
// POST /v1/mcp/prompts/:model/:prompt
func MCPGetPromptEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelName := c.Param("model")
		promptName := c.Param("prompt")
		if modelName == "" || promptName == "" {
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
			Arguments map[string]string `json:"arguments"`
		}
		if err := c.Bind(&req); err != nil {
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

		prompts, err := mcpTools.DiscoverMCPPrompts(c.Request().Context(), namedSessions)
		if err != nil {
			return fmt.Errorf("failed to discover MCP prompts: %w", err)
		}

		messages, err := mcpTools.GetMCPPrompt(c.Request().Context(), prompts, promptName, req.Arguments)
		if err != nil {
			return fmt.Errorf("failed to get prompt: %w", err)
		}

		type messageJSON struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		var result []messageJSON
		for _, m := range messages {
			result = append(result, messageJSON{
				Role:    string(m.Role),
				Content: mcpTools.PromptMessageToText(m),
			})
		}

		return c.JSON(200, map[string]any{
			"messages": result,
		})
	}
}
