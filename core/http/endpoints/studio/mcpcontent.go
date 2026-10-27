package studio

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
)

// failMCPContent maps the errors of MCP prompts and resources.
func failMCPContent(c echo.Context, err error) error {
	switch {
	case errors.Is(err, studiosvc.ErrMCPServerNotUsable):
		return fail(c, http.StatusForbidden, "mcp_refused", "the chat cannot use this MCP server")
	case errors.Is(err, studiosvc.ErrMCPContent):
		return fail(c, http.StatusBadGateway, "mcp_error", err.Error())
	}
	return failMCP(c, err)
}

func mcpRefs(in []schema.ChatsMCPRef) []studiosvc.MCPRef {
	out := make([]studiosvc.MCPRef, len(in))
	for i, r := range in {
		out[i] = studiosvc.MCPRef{Server: r.Server, Name: r.Name, Arguments: r.Arguments, URI: r.URI}
	}
	return out
}

// parseContext reads the context form field of a multipart message.
func parseContext(raw string) ([]schema.ChatsMCPRef, error) {
	if raw == "" {
		return nil, nil
	}
	var refs []schema.ChatsMCPRef
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil, err
	}
	return refs, nil
}

// MCPPrompts lists the MCP prompts a chat can use.
// @Summary List the MCP prompts of a chat.
// @Description Lists the prompts of the MCP servers of the chat's model and of your connectors that this chat can use now: not switched off for the chat, and granted by its saved agent when it has one. Add one to a message with the context field of POST /api/chats/{id}/messages.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsMCPPromptList "The prompts"
// @Router /api/chats/{id}/mcp/prompts [get]
func (h *Handlers) MCPPrompts(c echo.Context) error {
	ps, err := h.svc.MCPPrompts(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failMCPContent(c, err)
	}
	out := schema.ChatsMCPPromptList{Prompts: make([]schema.ChatsMCPPrompt, 0, len(ps))}
	for _, p := range ps {
		mp := schema.ChatsMCPPrompt{Server: p.Server, Name: p.Name, Title: p.Title, Description: p.Description}
		for _, a := range p.Arguments {
			mp.Arguments = append(mp.Arguments, schema.ChatsMCPPromptArg{Name: a.Name, Description: a.Description, Required: a.Required})
		}
		out.Prompts = append(out.Prompts, mp)
	}
	return c.JSON(http.StatusOK, out)
}

// MCPExpandPrompt expands an MCP prompt.
// @Summary Expand an MCP prompt.
// @Description Returns the text of the prompt with the given arguments. 403 mcp_refused when the chat cannot use the server.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsMCPPromptRequest true "the prompt"
// @Success 200 {object} schema.ChatsMCPContent "The expanded prompt"
// @Router /api/chats/{id}/mcp/prompts [post]
func (h *Handlers) MCPExpandPrompt(c echo.Context) error {
	var req schema.ChatsMCPPromptRequest
	if err := c.Bind(&req); err != nil || req.Server == "" || req.Name == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "server and name are required")
	}
	text, err := h.svc.MCPPrompt(c.Request().Context(), userID(c), c.Param("id"), req.Server, req.Name, req.Arguments)
	if err != nil {
		return failMCPContent(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsMCPContent{Text: text})
}

// MCPResources lists the MCP resources a chat can use.
// @Summary List the MCP resources of a chat.
// @Description Lists the resources of the MCP servers of the chat's model and of your connectors that this chat can use now. Add one to a message with the context field of POST /api/chats/{id}/messages.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsMCPResourceList "The resources"
// @Router /api/chats/{id}/mcp/resources [get]
func (h *Handlers) MCPResources(c echo.Context) error {
	rs, err := h.svc.MCPResources(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failMCPContent(c, err)
	}
	out := schema.ChatsMCPResourceList{Resources: make([]schema.ChatsMCPResource, 0, len(rs))}
	for _, r := range rs {
		out.Resources = append(out.Resources, schema.ChatsMCPResource{Server: r.Server, URI: r.URI, Name: r.Name, Description: r.Description, MimeType: r.MIMEType})
	}
	return c.JSON(http.StatusOK, out)
}

// MCPReadResource reads an MCP resource.
// @Summary Read an MCP resource.
// @Description Returns the text of the resource (binary parts are left out, the text is cut at 256 KiB). 403 mcp_refused when the chat cannot use the server.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsMCPReadRequest true "the resource"
// @Success 200 {object} schema.ChatsMCPContent "The resource text"
// @Router /api/chats/{id}/mcp/resources/read [post]
func (h *Handlers) MCPReadResource(c echo.Context) error {
	var req schema.ChatsMCPReadRequest
	if err := c.Bind(&req); err != nil || req.Server == "" || req.URI == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "server and uri are required")
	}
	text, err := h.svc.MCPResource(c.Request().Context(), userID(c), c.Param("id"), req.Server, req.URI)
	if err != nil {
		return failMCPContent(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsMCPContent{Text: text})
}
