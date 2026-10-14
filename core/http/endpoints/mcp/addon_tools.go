package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/functions"
)

// Add-on tools reach a model only through this file. They are not MCP
// sessions: an MCP session is built from model configuration and is shared by
// every user of that model, so it can carry no per-user authority. An add-on
// tool is instead bound per request to the authenticated principal and goes
// through the host broker (core/services/addons/tools):
//
//   - the actor is read once from the request, held by value, and used for
//     every call of that request;
//   - reads use the gateway; changes become proposals that only the
//     proposer's own browser session can confirm;
//   - every other surface rejects add-on tools with ErrAddonToolsUnsupported,
//     and tool sources that are not the broker never serve the reserved
//     namespace (see DiscoverMCPTools).

// MetadataKeyAddonTools is the request-metadata key that opts a chat request
// into the add-on tools the authenticated caller may use. The key is consumed
// so it does not reach the backend.
const MetadataKeyAddonTools = "addon_tools"

// ConversationHeader carries the identifier of the chat conversation a
// request belongs to. It is read from the authenticated request, never from
// the model's arguments, and it only names the namespace of the caller's own
// selections: the owner keys a selection by the authenticated user as well.
const ConversationHeader = "X-LocalAI-Conversation-Id"

// ErrAddonToolsUnsupported means a surface that cannot bind a per-request
// actor was asked for add-on tools. It is also the answer for a tool name in
// the reserved add-on namespace on any non-broker executor.
var ErrAddonToolsUnsupported = errors.New("addon tools are not available on this surface")

// AddonToolsFromMetadata reports whether the request opted into add-on tools.
// It removes the key whatever its value. Truthy values: 1, true, yes.
func AddonToolsFromMetadata(metadata map[string]string) bool {
	raw, ok := metadata[MetadataKeyAddonTools]
	if !ok {
		return false
	}
	delete(metadata, MetadataKeyAddonTools)
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// RejectAddonTools answers a request for add-on tools on a surface that does
// not serve them. The body is typed so a client can tell it from a failure.
func RejectAddonTools(c echo.Context) error {
	return c.JSON(http.StatusBadRequest, map[string]any{"error": map[string]any{
		"type":    "addon_tools_unsupported",
		"message": ErrAddonToolsUnsupported.Error(),
		"code":    http.StatusBadRequest,
	}})
}

// SessionToolFilter is the tool filter for any consumer that hands MCP
// sessions straight to an agent library (agent runs and agent jobs). It drops
// every tool in the add-on namespace: a session is built from configuration
// and shared, so it can never be the broker.
func SessionToolFilter() func(*mcp.ClientSession, string) bool {
	return func(_ *mcp.ClientSession, name string) bool { return !tools.IsToolName(name) }
}

// AddonToolsHTTPError maps a refusal to bind add-on tools to an HTTP error:
// a denial or a request without a named principal is 403, anything else is
// unavailable. It never reports an unknown failure as success.
func AddonToolsHTTPError(err error) error {
	if errors.Is(err, ErrAddonToolsUnsupported) || gateway.StatusFor(err) == http.StatusForbidden {
		return echo.NewHTTPError(http.StatusForbidden, "add-on tools are not available to this caller")
	}
	return echo.NewHTTPError(http.StatusServiceUnavailable, "add-on tools are unavailable")
}

// AddonToolProvider binds add-on tools to the principal of one request.
type AddonToolProvider struct {
	adapter *tools.ToolAdapter
	mode    string
}

// NewAddonToolProvider returns a provider over the broker adapter. mode is
// the configured ownership mode, never taken from a request.
func NewAddonToolProvider(adapter *tools.ToolAdapter, mode string) *AddonToolProvider {
	return &AddonToolProvider{adapter: adapter, mode: mode}
}

// ExecutorFor returns an executor bound to the authenticated principal of c.
// The principal comes from the trusted authentication context only. A request
// without a named user or key is refused.
func (p *AddonToolProvider) ExecutorFor(c echo.Context) (ToolExecutor, error) {
	if p == nil || p.adapter == nil {
		return nil, ErrAddonToolsUnsupported
	}
	actor, err := auth.AddonActorForMode(c, p.mode)
	if err != nil {
		return nil, err
	}
	// A header that is not the shape of a conversation ID names no
	// conversation: a call without a resource is then invalid, as before.
	conversation := c.Request().Header.Get(ConversationHeader)
	if !tools.ValidConversationID(conversation) {
		conversation = ""
	}
	return NewAddonToolExecutorFor(c.Request().Context(), p.adapter, actor, conversation)
}

// AddonToolExecutor is a ToolExecutor for add-on tools with one immutable
// actor. It has no setter: a new principal is a new executor.
type AddonToolExecutor struct {
	adapter *tools.ToolAdapter
	actor   contract.Actor
	// conversation is the chat conversation of the authenticated request. It
	// is bound once, like the actor, and arguments cannot change it.
	conversation string
	defs         []functions.Function
}

// NewAddonToolExecutor discovers the tools actor may use now, for a consumer
// that has no conversation.
func NewAddonToolExecutor(ctx context.Context, adapter *tools.ToolAdapter, actor contract.Actor) (*AddonToolExecutor, error) {
	return NewAddonToolExecutorFor(ctx, adapter, actor, "")
}

// NewAddonToolExecutorFor is NewAddonToolExecutor for a chat conversation: a
// call that names no resource uses the conversation's selection, which the
// owner authorizes again for the actor on every call.
func NewAddonToolExecutorFor(ctx context.Context, adapter *tools.ToolAdapter, actor contract.Actor, conversation string) (*AddonToolExecutor, error) {
	defs, err := adapter.Discover(ctx, actor)
	if err != nil {
		return nil, err
	}
	e := &AddonToolExecutor{adapter: adapter, actor: actor, conversation: conversation}
	for _, d := range defs {
		e.defs = append(e.defs, functions.Function{Name: d.Name, Description: d.Description, Parameters: d.Parameters})
	}
	return e, nil
}

func (e *AddonToolExecutor) DiscoverTools(context.Context) ([]functions.Function, error) {
	return append([]functions.Function(nil), e.defs...), nil
}

// IsTool claims the whole reserved namespace, so a name the actor may not call
// ends in a typed error and never in another executor.
func (e *AddonToolExecutor) IsTool(name string) bool { return tools.IsToolName(name) }

func (e *AddonToolExecutor) HasTools() bool { return len(e.defs) > 0 }

// ExecuteTool runs one call as the bound actor and returns the host's result
// document: a read result, a proposal reference or artifact references.
func (e *AddonToolExecutor) ExecuteTool(ctx context.Context, toolName, arguments string) (string, error) {
	res, err := e.adapter.ExecuteIn(ctx, e.actor, e.conversation, toolName, json.RawMessage(arguments))
	if err != nil {
		return "", err
	}
	return res.JSON(), nil
}

// combinedExecutor serves the tools of two sources. Names never collide: the
// add-on namespace is reserved to the first source that claims it.
type combinedExecutor struct{ first, second ToolExecutor }

// CombineExecutors merges two executors. A nil executor is ignored.
func CombineExecutors(first, second ToolExecutor) ToolExecutor {
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return &combinedExecutor{first: first, second: second}
}

func (c *combinedExecutor) DiscoverTools(ctx context.Context) ([]functions.Function, error) {
	a, err := c.first.DiscoverTools(ctx)
	if err != nil {
		return nil, err
	}
	b, err := c.second.DiscoverTools(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []functions.Function
	for _, f := range append(a, b...) {
		if !seen[f.Name] {
			seen[f.Name] = true
			out = append(out, f)
		}
	}
	return out, nil
}

func (c *combinedExecutor) IsTool(name string) bool {
	return c.first.IsTool(name) || c.second.IsTool(name)
}
func (c *combinedExecutor) HasTools() bool { return c.first.HasTools() || c.second.HasTools() }

func (c *combinedExecutor) ExecuteTool(ctx context.Context, toolName, arguments string) (string, error) {
	// The namespace belongs to the source that discovers add-on tools.
	if tools.IsToolName(toolName) {
		if _, ok := c.second.(*AddonToolExecutor); ok {
			return c.second.ExecuteTool(ctx, toolName, arguments)
		}
		if _, ok := c.first.(*AddonToolExecutor); ok {
			return c.first.ExecuteTool(ctx, toolName, arguments)
		}
		return "", ErrAddonToolsUnsupported
	}
	if c.first.IsTool(toolName) {
		return c.first.ExecuteTool(ctx, toolName, arguments)
	}
	return c.second.ExecuteTool(ctx, toolName, arguments)
}
