package agents

import (
	"context"
	"os/exec"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/mcptransport"
	"github.com/mudler/xlog"
)

// setupMCPSessions creates MCP client sessions from the agent config.
// Returns the sessions, a cleanup function, and any error.
func setupMCPSessions(ctx context.Context, cfg *AgentConfig) ([]*gomcp.ClientSession, func()) {
	var sessions []*gomcp.ClientSession

	client := gomcp.NewClient(&gomcp.Implementation{
		Name:    "localai-agent",
		Version: "v1.0.0",
	}, nil)

	// HTTP MCP servers: Streamable HTTP first, legacy SSE as fallback, both bounded by the
	// discovery timeout (same helper as the model MCP path).
	for _, srv := range cfg.MCPServers {
		if srv.URL == "" {
			continue
		}
		session, err := mcptransport.Connect(ctx, client, srv.URL, nil, config.DefaultMCPDiscoveryTimeout)
		if err != nil {
			xlog.Warn("Failed to connect to HTTP MCP server", "url", srv.URL, "error", err)
			continue
		}
		sessions = append(sessions, session)
	}

	// STDIO MCP servers (using command transport)
	for _, srv := range cfg.MCPSTDIOServers {
		if srv.Cmd == "" {
			continue
		}
		cmd := exec.CommandContext(ctx, srv.Cmd, srv.Args...)
		if len(srv.Env) > 0 {
			cmd.Env = append(cmd.Environ(), srv.Env...)
		}
		transport := &gomcp.CommandTransport{Command: cmd}
		session, err := client.Connect(ctx, transport, nil)
		if err != nil {
			xlog.Warn("Failed to connect to STDIO MCP server", "cmd", srv.Cmd, "error", err)
			continue
		}
		sessions = append(sessions, session)
	}

	cleanup := func() {
		for _, s := range sessions {
			if err := s.Close(); err != nil {
				xlog.Warn("Failed to close MCP session", "error", err)
			}
		}
	}

	return sessions, cleanup
}
