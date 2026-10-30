package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

func (h *Handlers) mcpHost(c echo.Context) (*mcpconns.Service, bool) {
	return feature(c, h.svc.MCPConnectors(), func(c echo.Context) error {
		return fail(c, http.StatusConflict, "mcp_connectors_off", "MCP connectors are not available")
	})
}

var mcpRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "connector not found", mcpconns.ErrNotFound),
	verbatim(http.StatusForbidden, typeAuthz, mcpconns.ErrForbidden.Error()+": ", mcpconns.ErrForbidden),
	verbatim(http.StatusBadRequest, typeInvalid, mcpconns.ErrInvalid.Error()+": ", mcpconns.ErrInvalid),
}

func failMCP(c echo.Context, err error) error { return mapErr(c, err, mcpRules, failFor) }

func mcpOut(m mcpconns.Connector) schema.ChatsMCPConnector {
	args := m.Args
	if args == nil {
		args = []string{}
	}
	return schema.ChatsMCPConnector{ID: m.ID, Name: m.Name, Transport: m.Transport, URL: m.URL, Command: m.Command, Args: args,
		HasToken: m.HasToken, HasHeaders: m.HasHeaders, HasEnv: m.HasEnv, Enabled: m.Enabled, Created: m.Created, Updated: m.Updated}
}

func mcpIn(r schema.ChatsMCPConnectorWrite) mcpconns.Input {
	return mcpconns.Input{Name: r.Name, Transport: r.Transport, URL: r.URL, Command: r.Command, Args: r.Args,
		Token: r.Token, Headers: r.Headers, Env: r.Env, Enabled: r.Enabled}
}

// ListMCPConnectors lists your MCP connectors.
// @Summary List your MCP connectors.
// @Description An MCP connector is an MCP server that you defined. Your chats use the enabled ones next to the MCP servers of the model, and a saved agent can name one as connector:<id>. Tokens, headers and the environment of a local command are never returned. Needs the MCP feature.
// @Tags chats
// @Success 200 {object} schema.ChatsMCPConnectorList "Your connectors, sorted by name"
// @Router /api/chats/mcp-connectors [get]
func (h *Handlers) ListMCPConnectors(c echo.Context) error {
	host, ok := h.mcpHost(c)
	if !ok {
		return nil
	}
	list, err := host.List(c.Request().Context(), userID(c))
	if err != nil {
		return failMCP(c, err)
	}
	out := schema.ChatsMCPConnectorList{Connectors: make([]schema.ChatsMCPConnector, 0, len(list))}
	for _, m := range list {
		out.Connectors = append(out.Connectors, mcpOut(m))
	}
	return c.JSON(http.StatusOK, out)
}

// CreateMCPConnector creates an MCP connector.
// @Summary Create an MCP connector.
// @Description transport is http (a remote server, streamable HTTP with a fall back to SSE) or stdio (a command run on the host, administrators only and refused in distributed mode). The address of an http connector must be http or https; an address on a private network is refused unless an administrator allows it (the mcp_connectors_allow_private setting), and the check is made again when the connection is opened. token, headers and env are sealed at rest and never returned. At most 20 connectors; names are unique per user. OAuth for remote servers is not supported.
// @Tags chats
// @Param request body schema.ChatsMCPConnectorWrite true "the connector"
// @Success 201 {object} schema.ChatsMCPConnector "The connector"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/mcp-connectors [post]
func (h *Handlers) CreateMCPConnector(c echo.Context) error {
	host, ok := h.mcpHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsMCPConnectorWrite
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
	}
	m, err := host.Create(c.Request().Context(), userID(c), mcpIn(req))
	if err != nil {
		return failMCP(c, err)
	}
	return c.JSON(http.StatusCreated, mcpOut(m))
}

// GetMCPConnector reads an MCP connector.
// @Summary Read an MCP connector.
// @Tags chats
// @Param id path string true "the connector's id"
// @Success 200 {object} schema.ChatsMCPConnector "The connector"
// @Router /api/chats/mcp-connectors/{id} [get]
func (h *Handlers) GetMCPConnector(c echo.Context) error {
	host, ok := h.mcpHost(c)
	if !ok {
		return nil
	}
	m, err := host.Get(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failMCP(c, err)
	}
	return c.JSON(http.StatusOK, mcpOut(m))
}

// UpdateMCPConnector replaces an MCP connector.
// @Summary Replace an MCP connector.
// @Description The body replaces every field. token, headers and env keep the stored value when left out, and are cleared by an empty one. The open session of the connector is closed.
// @Tags chats
// @Param id path string true "the connector's id"
// @Param request body schema.ChatsMCPConnectorWrite true "the connector"
// @Success 200 {object} schema.ChatsMCPConnector "The connector"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/mcp-connectors/{id} [put]
func (h *Handlers) UpdateMCPConnector(c echo.Context) error {
	host, ok := h.mcpHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsMCPConnectorWrite
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
	}
	m, err := host.Update(c.Request().Context(), userID(c), c.Param("id"), mcpIn(req))
	if err != nil {
		return failMCP(c, err)
	}
	return c.JSON(http.StatusOK, mcpOut(m))
}

// DeleteMCPConnector deletes an MCP connector.
// @Summary Delete an MCP connector.
// @Description Its open session is closed. A saved agent that names it gets a note that it could not be used.
// @Tags chats
// @Param id path string true "the connector's id"
// @Success 204 "Deleted"
// @Router /api/chats/mcp-connectors/{id} [delete]
func (h *Handlers) DeleteMCPConnector(c echo.Context) error {
	host, ok := h.mcpHost(c)
	if !ok {
		return nil
	}
	if err := host.Delete(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failMCP(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handlers) setMCPEnabled(c echo.Context, on bool) error {
	host, ok := h.mcpHost(c)
	if !ok {
		return nil
	}
	m, err := host.SetEnabled(c.Request().Context(), userID(c), c.Param("id"), on)
	if err != nil {
		return failMCP(c, err)
	}
	return c.JSON(http.StatusOK, mcpOut(m))
}

// EnableMCPConnector switches an MCP connector on.
// @Summary Switch an MCP connector on.
// @Description The address is checked again; one on a private network is refused unless an administrator allows it.
// @Tags chats
// @Param id path string true "the connector's id"
// @Success 200 {object} schema.ChatsMCPConnector "The connector"
// @Router /api/chats/mcp-connectors/{id}/enable [post]
func (h *Handlers) EnableMCPConnector(c echo.Context) error { return h.setMCPEnabled(c, true) }

// DisableMCPConnector switches an MCP connector off.
// @Summary Switch an MCP connector off.
// @Tags chats
// @Param id path string true "the connector's id"
// @Success 200 {object} schema.ChatsMCPConnector "The connector"
// @Router /api/chats/mcp-connectors/{id}/disable [post]
func (h *Handlers) DisableMCPConnector(c echo.Context) error { return h.setMCPEnabled(c, false) }

// TestMCPConnector connects to an MCP connector and lists its tools.
// @Summary Test an MCP connector.
// @Description Connects with the stored definition, with the same address and credential checks as a chat, and lists the tool names. It works for a connector that is switched off. A server that cannot be used is a 200 with ok false and the reason, which never holds a header value.
// @Tags chats
// @Param id path string true "the connector's id"
// @Success 200 {object} schema.ChatsMCPConnectorTest "The tools, or the reason"
// @Router /api/chats/mcp-connectors/{id}/test [post]
func (h *Handlers) TestMCPConnector(c echo.Context) error {
	host, ok := h.mcpHost(c)
	if !ok {
		return nil
	}
	p, err := host.Test(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failMCP(c, err)
	}
	tools := p.Tools
	if tools == nil {
		tools = []string{}
	}
	return c.JSON(http.StatusOK, schema.ChatsMCPConnectorTest{OK: p.Error == "", Tools: tools, Error: p.Error})
}

func toolsPanel(c echo.Context, h *Handlers) error {
	p, err := h.svc.Tools(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsToolsPanel{Builtins: make([]schema.ChatsBuiltinGroup, 0, len(p.Builtins)), Addons: make([]schema.ChatsAddon, 0, len(p.Addons)), Servers: make([]schema.ChatsMCPServerSwitch, 0, len(p.Servers)),
		Connectors: make([]schema.ChatsMCPConnectorSwitch, 0, len(p.Connectors)), Off: p.Off}
	for _, g := range p.Builtins {
		out.Builtins = append(out.Builtins, schema.ChatsBuiltinGroup{Group: g.Group, Tools: g.Tools, Off: g.Off, Provider: g.Provider})
	}
	for _, a := range p.Addons {
		out.Addons = append(out.Addons, schema.ChatsAddon{ID: a.ID, Tools: a.Tools, Off: a.Off})
	}
	for _, s := range p.Servers {
		out.Servers = append(out.Servers, schema.ChatsMCPServerSwitch{Name: s.Name, Off: s.Off, Usable: s.Usable, Reason: s.Reason})
	}
	for _, s := range p.Connectors {
		out.Connectors = append(out.Connectors, schema.ChatsMCPConnectorSwitch{ID: s.ID, Name: s.Name, Off: s.Off, Usable: s.Usable, Reason: s.Reason})
	}
	return c.JSON(http.StatusOK, out)
}

// ListChatTools lists what a chat can use besides its built-in tools.
// @Summary List the tools a chat can switch on and off.
// @Description The groups of built-in tools (such as web), add-ons, the MCP servers of the chat's model and your MCP connectors, each with the chat's switch and whether it can be used now (with the reason when not). A chat with a saved agent lists only what the agent grants.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsToolsPanel "The tools and their switches"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/tools [get]
func (h *Handlers) ListChatTools(c echo.Context) error { return toolsPanel(c, h) }

// SetChatBuiltins sets the groups of built-in tools a chat has switched off.
// @Summary Switch groups of built-in tools on and off for a chat.
// @Description The list replaces the earlier one. A group is a name such as web (web_search and web_fetch). A switch only takes away: a tool the administrator has off stays off. The next turn of the chat gets the tools that are on.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsBuiltinSwitches true "the groups that are off"
// @Success 200 {object} schema.ChatsToolsPanel "The tools and their switches now in force"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/builtins [put]
func (h *Handlers) SetChatBuiltins(c echo.Context) error {
	var req schema.ChatsBuiltinSwitches
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "off is required")
	}
	if err := h.svc.SetBuiltinsOff(c.Request().Context(), userID(c), c.Param("id"), req.Off); err != nil {
		return failFor(c, err)
	}
	return toolsPanel(c, h)
}

// SetChatMCP sets the MCP servers and connectors a chat has switched off.
// @Summary Switch MCP servers and connectors on and off for a chat.
// @Description The list replaces the earlier one: the name of a server of the model, or connector:<id>. A switch only takes away; a chat with a saved agent never gets more than the agent grants. The next turn of the chat gets the tools that are on.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsMCPSwitches true "what is off"
// @Success 200 {object} schema.ChatsToolsPanel "The tools and their switches now in force"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/mcp [put]
func (h *Handlers) SetChatMCP(c echo.Context) error {
	var req schema.ChatsMCPSwitches
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "off is required")
	}
	if err := h.svc.SetMcpOff(c.Request().Context(), userID(c), c.Param("id"), req.Off); err != nil {
		return failFor(c, err)
	}
	return toolsPanel(c, h)
}
