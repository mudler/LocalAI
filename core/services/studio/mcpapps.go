package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/mcpapps"
)

// MCP Apps are tools that declare an interactive page in _meta.ui.resourceUri.
// The page and the tool calls of the page go through the service, which checks
// on every request that the chat may use the server right now.

// Errors of the app calls.
var (
	// ErrMCPAppRefused means the chat cannot use that server, resource or tool
	// for an app.
	ErrMCPAppRefused = errors.New("studio: the chat cannot use this MCP app request")
	// ErrMCPAppUnsupported means the server cannot serve apps on this
	// deployment (a model server in distributed mode).
	ErrMCPAppUnsupported = errors.New("studio: MCP apps are not available for this server")
	// ErrMCPAppFailed means the server did not answer well; the text has no
	// credential.
	ErrMCPAppFailed = errors.New("studio: the MCP server could not serve the request")
	// ErrBadMCPAppRequest means the request itself is malformed.
	ErrBadMCPAppRequest = errors.New("studio: the MCP app request is not valid")
)

// appCardKind is the kind of the card that carries an app.
const appCardKind = "mcp_app"

// ConnectorApps is the part of ConnectorTools that reads the page of an app.
// The connector service implements it; a host without it serves no app from a
// connector.
type ConnectorApps interface {
	ReadResource(ctx context.Context, user, id, uri string) (mcpapps.Resource, error)
}

func refuse(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrMCPAppRefused, fmt.Sprintf(format, a...))
}

// appServer is what an app request needs from one server: its tools with their
// ui metadata, and the two calls.
type appServer struct {
	tools []appTool
	read  func(ctx context.Context, uri string) (mcpapps.Resource, error)
	call  func(ctx context.Context, tool string, args json.RawMessage) (mcpapps.CallResult, error)
}

type appTool struct {
	name string
	ui   mcpapps.UI
}

// appServerFor returns the server the chat may use now, or an error. The rules
// are the ones the turn applies when it builds the chat's tools: the chat is the
// user's, the server is not switched off for the chat, a saved agent names it,
// the connector is the user's and enabled, and a model server is one the model
// picks by itself or the agent names.
func (s *Service) appServerFor(ctx context.Context, user, conv, server string) (*appServer, error) {
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return nil, err
	}
	id, isConn := strings.CutPrefix(server, mcpconns.Prefix)
	if isConn && !connectorSwitchShape.MatchString(server) || !isConn && !serverSwitchShape.MatchString(server) {
		return nil, refuse("not a server name")
	}
	if set(rec.McpOff)[server] {
		return nil, refuse("the server is switched off for this chat")
	}
	sc := s.scopeOf(ctx, rec)
	if sc != nil {
		if sc.deleted || !set(sc.mcp)[server] {
			return nil, refuse("the chat's agent does not grant the server")
		}
	}
	if isConn {
		return s.connectorApp(ctx, user, id)
	}
	return s.modelApp(ctx, user, rec, sc, server)
}

func (s *Service) connectorApp(ctx context.Context, user, id string) (*appServer, error) {
	apps, ok := s.d.Connectors.(ConnectorApps)
	if s.d.Connectors == nil || !ok {
		return nil, refuse("connectors are not available")
	}
	defs, missing := s.d.Connectors.Resolve(ctx, user, []string{id}, false)
	if len(missing) > 0 {
		return nil, refuse("the connector cannot be used")
	}
	srv := &appServer{}
	byName := map[string]mcpconns.Tool{}
	for _, d := range defs {
		if d.ConnectorID != id {
			continue
		}
		srv.tools = append(srv.tools, appTool{name: d.Name, ui: mcpapps.ParseUI(d.Meta)})
		byName[d.Name] = d
	}
	srv.read = func(ctx context.Context, uri string) (mcpapps.Resource, error) {
		return apps.ReadResource(ctx, user, id, uri)
	}
	srv.call = func(ctx context.Context, tool string, args json.RawMessage) (mcpapps.CallResult, error) {
		d, ok := byName[tool]
		if !ok || d.RunApp == nil {
			return mcpapps.CallResult{}, refuse("no such tool")
		}
		return d.RunApp(ctx, args)
	}
	return srv, nil
}

func (s *Service) modelApp(ctx context.Context, user string, rec session.ChatRecord, sc *scope, server string) (*appServer, error) {
	if s.d.MCP == nil {
		return nil, refuse("the model has no MCP servers")
	}
	if sc == nil {
		// A chat with no agent uses the servers the model picks by itself.
		auto := false
		if s.d.MCPServers != nil {
			for _, m := range s.d.MCPServers(ctx, principal(user), rec.Model) {
				if m.Name == server && m.Auto {
					auto = true
				}
			}
		}
		if !auto {
			return nil, refuse("the model does not use this server")
		}
	}
	exec, _ := s.d.MCP(ctx, principal(user), rec.Model, []string{server}, true)
	if exec == nil {
		return nil, refuse("the server cannot be used")
	}
	ae, ok := exec.(mcpendpoint.AppExecutor)
	if !ok {
		return nil, ErrMCPAppUnsupported
	}
	srv := &appServer{}
	for _, t := range ae.AppTools() {
		if t.Server == server {
			srv.tools = append(srv.tools, appTool{name: t.Name, ui: t.UI})
		}
	}
	srv.read = func(ctx context.Context, uri string) (mcpapps.Resource, error) {
		return ae.ReadAppResource(ctx, server, uri)
	}
	srv.call = func(ctx context.Context, tool string, args json.RawMessage) (mcpapps.CallResult, error) {
		return ae.ExecuteAppTool(ctx, server, tool, args)
	}
	return srv, nil
}

// MCPAppResource reads the page of an app. Only a ui:// resource that a tool of
// the server declares is read, and only from a server the chat may use now.
func (s *Service) MCPAppResource(ctx context.Context, user, conv, server, uri string) (mcpapps.Resource, error) {
	if err := mcpapps.CheckURI(uri); err != nil {
		if _, gerr := s.Get(ctx, user, conv); gerr != nil {
			return mcpapps.Resource{}, gerr
		}
		return mcpapps.Resource{}, refuse("%s", err)
	}
	srv, err := s.appServerFor(ctx, user, conv, server)
	if err != nil {
		return mcpapps.Resource{}, err
	}
	declared := false
	for _, t := range srv.tools {
		if t.ui.ResourceURI == uri {
			declared = true
		}
	}
	if !declared {
		return mcpapps.Resource{}, refuse("no tool of the server declares this resource")
	}
	res, err := srv.read(ctx, uri)
	if err != nil {
		return mcpapps.Resource{}, appFailure(err)
	}
	return res, nil
}

// MCPAppCall runs a tool of the server for its app and returns the MCP result.
// The tool must be one the app may call (its visibility holds "app") and must
// not take a reserved name.
func (s *Service) MCPAppCall(ctx context.Context, user, conv, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	srv, err := s.appServerFor(ctx, user, conv, server)
	if err != nil {
		return nil, err
	}
	if mcpendpoint.IsAddonToolName(tool) || reservedToolNames[tool] {
		return nil, refuse("this tool name is reserved")
	}
	var found *appTool
	for i := range srv.tools {
		if srv.tools[i].name == tool {
			found = &srv.tools[i]
		}
	}
	if found == nil {
		return nil, refuse("the server has no such tool")
	}
	if !found.ui.ForApp() {
		return nil, refuse("the tool is not visible to apps")
	}
	if len(args) > 0 && !json.Valid(args) {
		return nil, fmt.Errorf("%w: the arguments are not JSON", ErrBadMCPAppRequest)
	}
	if len(args) > mcpapps.MaxInputBytes {
		return nil, fmt.Errorf("%w: the arguments are too large", ErrBadMCPAppRequest)
	}
	out, err := srv.call(ctx, tool, args)
	if err != nil {
		return nil, appFailure(err)
	}
	return out.Result, nil
}

// appFailure turns the error of a server into an error whose text is safe to
// show: the connector service has already removed its secrets from it.
func appFailure(err error) error {
	switch {
	case errors.Is(err, ErrMCPAppRefused), errors.Is(err, mcpconns.ErrForbidden), errors.Is(err, mcpconns.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrMCPAppRefused, err)
	case errors.Is(err, mcpconns.ErrInvalid), errors.Is(err, mcpapps.ErrBadResource):
		return fmt.Errorf("%w: %s", ErrMCPAppRefused, err)
	}
	return fmt.Errorf("%w: %s", ErrMCPAppFailed, err)
}

// appCard builds the card of a call of a tool that has a view.
func appCard(server, tool string, ui mcpapps.UI, args json.RawMessage, result json.RawMessage) session.ToolCard {
	sum := sha256.Sum256([]byte(server + "\x00" + tool + "\x00" + ui.ResourceURI))
	return session.ToolCard{Kind: appCardKind, ID: hex.EncodeToString(sum[:8]), App: &session.ToolApp{
		Server: server, Tool: tool, ResourceURI: ui.ResourceURI, Visibility: ui.Visibility, Input: args, Result: result,
	}}
}

// withApp wraps the run of a tool that has a view, so that the chat attaches
// the app to the result. The model still gets the text only.
func withApp(server, tool string, ui mcpapps.UI, run func(ctx context.Context, args json.RawMessage) (mcpapps.CallResult, error)) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		out, err := run(ctx, args)
		if err != nil {
			return "", err
		}
		session.SetToolCard(ctx, appCard(server, tool, ui, args, out.Result))
		return out.Text, nil
	}
}
