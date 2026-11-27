package plugins

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mudler/nib/plugin/claudeplugin"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

// GuestRoot is where a chat sees the packages: CLAUDE_PLUGIN_ROOT of a
// plugin is GuestRoot/<digest-hex>.
const GuestRoot = "/workspace/shared-plugins"

// Reasons a remote MCP server of a plugin gets no connector.
const (
	// ServerNeedsSettings: the server's address or headers use a userConfig
	// value or a variable the plugin does not define. The settings form
	// comes in a later version.
	ServerNeedsSettings = "needs settings (later version)"
	// ServerNotSupported: a WebSocket server; connectors have no WebSocket
	// transport.
	ServerNotSupported = "WebSocket servers are not supported"
	// ServerLocal: a stdio server, which runs in the Sandbox in a later
	// version.
	ServerLocal = "runs a local command (later version)"
	// ServerOverLimit: the plugin has more remote servers than one plugin
	// may turn into connectors.
	ServerOverLimit = "over the limit of connectors of one plugin"
)

// MaxPluginConnectors is the most connectors one plugin makes for a user.
// The connectors of plugins do not count against the limit of a user's own
// connectors, so without a cap of their own 50 installs could make hundreds.
const MaxPluginConnectors = 5

// RemoteServer is an http or sse MCP server of a plugin with its templates
// expanded: what its connector uses.
type RemoteServer struct {
	Name    string
	URL     string
	Headers map[string]string
}

// ConnectorServers returns servers in the form the MCP connectors service
// takes.
func ConnectorServers(servers []RemoteServer) []mcpconns.PluginServer {
	out := make([]mcpconns.PluginServer, len(servers))
	for i, r := range servers {
		out[i] = mcpconns.PluginServer(r)
	}
	return out
}

// SkippedServer is an MCP server of a plugin that gets no connector, with
// the reason.
type SkippedServer struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// PluginRoot returns CLAUDE_PLUGIN_ROOT of the package digest in a chat.
func PluginRoot(digest string) string { return GuestRoot + "/" + content.DigestHex(digest) }

// RemoteServers returns the MCP servers of a plugin that connectors serve,
// and the others with the reason they are skipped. ${CLAUDE_PLUGIN_ROOT} and
// the other plugin variables expand; ${VAR} resolves only against them,
// never against the server's environment, and ${VAR:-default} takes its
// default. A server that needs a value nothing gives is skipped.
func RemoteServers(m Model, digest string) (servers []RemoteServer, skipped []SkippedServer) {
	vars := claudeplugin.Vars{
		PluginRoot: PluginRoot(digest),
		// The process environment never reaches a plugin's configuration.
		Env: func(string) (string, bool) { return "", false },
	}
	for _, ref := range m.MCPServers {
		switch ref.Type {
		case "http", "sse":
		case "ws":
			skipped = append(skipped, SkippedServer{ref.Name, ServerNotSupported})
			continue
		default:
			skipped = append(skipped, SkippedServer{ref.Name, ServerLocal})
			continue
		}
		srv, missing := claudeplugin.MCPServer{Name: ref.Name, Type: ref.Type, URL: ref.URL, Headers: ref.Headers}.Expand(vars)
		if len(missing) > 0 || usesUserConfig(srv) {
			skipped = append(skipped, SkippedServer{ref.Name, ServerNeedsSettings})
			continue
		}
		if len(servers) >= MaxPluginConnectors {
			skipped = append(skipped, SkippedServer{ref.Name, ServerOverLimit})
			continue
		}
		servers = append(servers, RemoteServer{Name: ref.Name, URL: srv.URL, Headers: srv.Headers})
	}
	return servers, skipped
}

// usesUserConfig reports a ${user_config.KEY} token that stayed as written:
// no value was given for it.
func usesUserConfig(s claudeplugin.MCPServer) bool {
	if strings.Contains(s.URL, "${user_config.") {
		return true
	}
	for k, v := range s.Headers {
		if strings.Contains(k, "${") || strings.Contains(v, "${user_config.") {
			return true
		}
	}
	return false
}

// ConnectorSync keeps the connectors that plugins own. The chats' MCP
// connectors service implements it.
type ConnectorSync interface {
	// SyncPluginConnectors makes the connectors of an install for one user
	// match servers. It is idempotent.
	SyncPluginConnectors(ctx context.Context, principal, installID, plugin, digest string, servers []RemoteServer) error
	// DropPluginConnectors deletes the connectors of an install: those of
	// principal, or of every user when all is set.
	DropPluginConnectors(ctx context.Context, principal, installID string, all bool) error
	// ResyncPluginConnectors makes the connectors of an install match
	// servers for every user who has them, after the install changed
	// package.
	ResyncPluginConnectors(ctx context.Context, installID, plugin, digest string, servers []RemoteServer) error
}

// syncConnectors makes the connectors of an install for one user follow the
// user's choice. A failure is logged: the next turn of the user's chats
// tries again.
func (s *Service) syncConnectors(ctx context.Context, principal, installID string, on bool) {
	if s.conns == nil {
		return
	}
	if !on {
		if err := s.conns.DropPluginConnectors(ctx, principal, installID, false); err != nil {
			xlog.Warn("plugins: remove the connectors of a plugin", "install", installID, "error", err)
		}
		return
	}
	in, err := s.getInstall(ctx, installID)
	if err != nil {
		xlog.Warn("plugins: read an install for its connectors", "install", installID, "error", err)
		return
	}
	pkg, err := s.st.Package(ctx, in.Digest)
	if err != nil {
		xlog.Warn("plugins: read a package for its connectors", "install", installID, "error", err)
		return
	}
	var m Model
	if err := json.Unmarshal(pkg.Model, &m); err != nil {
		xlog.Warn("plugins: read the model of a package", "digest", in.Digest, "error", err)
		return
	}
	servers, _ := RemoteServers(m, in.Digest)
	if err := s.conns.SyncPluginConnectors(ctx, principal, installID, m.Name, in.Digest, servers); err != nil {
		xlog.Warn("plugins: make the connectors of a plugin", "install", installID, "error", err)
	}
}

// dropConnectors deletes the connectors of an install for every user, for an
// install that goes.
func (s *Service) dropConnectors(ctx context.Context, installID string) {
	if s.conns == nil {
		return
	}
	if err := s.conns.DropPluginConnectors(ctx, "", installID, true); err != nil {
		xlog.Warn("plugins: remove the connectors of an install", "install", installID, "error", err)
	}
}

// followConnectors makes the connectors of an install follow its new
// package pkg, after an update or a rollback. When the change added code,
// the install is off for every user until they turn it on again, so its
// connectors go; the re-enable makes them again.
func (s *Service) followConnectors(ctx context.Context, installID string, pkg store.Package, reenable bool) {
	if s.conns == nil {
		return
	}
	if reenable {
		s.dropConnectors(ctx, installID)
		return
	}
	var m Model
	if err := json.Unmarshal(pkg.Model, &m); err != nil {
		xlog.Warn("plugins: read the model of a package", "digest", pkg.Digest, "error", err)
		return
	}
	servers, _ := RemoteServers(m, pkg.Digest)
	if err := s.conns.ResyncPluginConnectors(ctx, installID, m.Name, pkg.Digest, servers); err != nil {
		xlog.Warn("plugins: update the connectors of an install", "install", installID, "error", err)
	}
}
