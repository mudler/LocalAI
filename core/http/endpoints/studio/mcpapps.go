package studio

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// maxAppCallBody bounds the body of a tool call from an app: the arguments are
// capped at 16 KiB, the rest is the names.
const maxAppCallBody = 32 << 10

// appRules map the error of an app request. A refusal and a failure of the
// server carry text that holds no secret.
var appRules = []errRule{
	sentinelText(http.StatusForbidden, "mcp_app_refused", studiosvc.ErrMCPAppRefused),
	fixed(http.StatusConflict, "mcp_app_unsupported", "MCP apps are not available for this server", studiosvc.ErrMCPAppUnsupported),
	sentinelText(http.StatusBadRequest, typeInvalid, studiosvc.ErrBadMCPAppRequest),
	sentinelText(http.StatusBadGateway, "mcp_app_failed", studiosvc.ErrMCPAppFailed),
}

func failApp(c echo.Context, err error) error { return mapErr(c, err, appRules, failFor) }

// GetMCPAppResource reads the page of an MCP App.
// @Summary Read the page of an MCP App.
// @Description An MCP server can declare an interactive page for a tool (_meta.ui.resourceUri). The tool_end event and the transcript line of the call name the server and the resource. This route reads that page from the server, for the interface to show in a sandboxed frame. server is connector:<id> for one of your connectors, or the name of an MCP server of the chat's model. It answers only for a server the chat may use now (not switched off for the chat, granted by the chat's agent if it has one, a connector that is yours and enabled), only for a ui:// resource that a tool of that server declares, and only for text/html up to 4 MiB; anything else is 403. Needs the MCP feature. The answer is not cached and never holds a header or token of the connector.
// @Tags chats
// @Param id path string true "conversation id"
// @Param server query string true "connector:<id> or the name of an MCP server of the model"
// @Param uri query string true "the ui:// resource"
// @Success 200 {object} schema.ChatsMCPAppResource "The page and its metadata"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/{id}/mcp-apps/resource [get]
func (h *Handlers) GetMCPAppResource(c echo.Context) error {
	server, uri := c.QueryParam("server"), c.QueryParam("uri")
	if server == "" || uri == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "server and uri are required")
	}
	res, err := h.svc.MCPAppResource(c.Request().Context(), userID(c), c.Param("id"), server, uri)
	if err != nil {
		return failApp(c, err)
	}
	raw, _ := json.Marshal(res.Meta)
	meta := map[string]any{}
	_ = json.Unmarshal(raw, &meta)
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, schema.ChatsMCPAppResource{URI: res.URI, MimeType: res.MIMEType, HTML: res.HTML, Meta: meta})
}

// CallMCPAppTool runs a tool of an MCP server for its app.
// @Summary Call a tool for an MCP App.
// @Description The page of an MCP App can call tools of its own server. The interface passes the call here. The tool must belong to the server named, the chat must be able to use that server now (the same checks as the resource route), and the tool's _meta.ui.visibility must include "app" (the default includes it). A tool in the add-on namespace or with a built-in's name is refused. The call has the timeout of a model's call and its result is cut at 256 KiB. Needs the MCP feature.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsMCPAppCall true "the call"
// @Success 200 {object} schema.ChatsMCPAppCallResult "The MCP result"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 413 {object} schema.ErrorResponse "The body is over 32 KiB"
// @Router /api/chats/{id}/mcp-apps/call [post]
func (h *Handlers) CallMCPAppTool(c echo.Context) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxAppCallBody)
	req, ok := bind[schema.ChatsMCPAppCall](c, "the body is not valid JSON")
	if !ok {
		return nil
	}
	if req.Server == "" || req.Tool == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "server and tool are required")
	}
	out, err := h.svc.MCPAppCall(c.Request().Context(), userID(c), c.Param("id"), req.Server, req.Tool, req.Arguments)
	if err != nil {
		return failApp(c, err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, schema.ChatsMCPAppCallResult{Result: out})
}

// appJSON converts the app of a card. With payload it keeps the input and the
// result of the call; a live event has the reference only.
func appJSON(a *session.ToolApp, payload bool) *schema.ChatsToolApp {
	if a == nil {
		return nil
	}
	vis := a.Visibility
	if vis == nil {
		vis = []string{}
	}
	out := &schema.ChatsToolApp{Server: a.Server, Tool: a.Tool, ResourceURI: a.ResourceURI, Visibility: vis, Cut: a.Cut}
	if payload {
		out.Input, out.Result = a.Input, a.Result
	}
	return out
}
