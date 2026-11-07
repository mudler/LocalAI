package studio

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

// checkMCPServers refuses a name in the agent's mcp_servers that this server
// cannot resolve, when the save adds it. "connector:<id>" must be a connector
// the user has defined; a plain name must be an MCP server of the agent's
// model, when the agent fixes one and the model has any. A name the stored
// agent already holds is tolerated, so an agent saved before this check still
// loads and saves (the form shows such a name as "not defined", and the chats
// of the agent ignore it). The error wraps agents.ErrInvalid.
func (s *Service) checkMCPServers(ctx context.Context, p string, a agents.Agent) error {
	if len(a.MCPServers) == 0 {
		return nil
	}
	var stored []string
	if a.ID != "" {
		if old, err := s.d.Agents.Get(ctx, p, a.ID); err == nil {
			stored = old.MCPServers
		}
	}
	var connectors []mcpconns.Connector
	var servers []ModelMCPServer
	loadedConnectors, loadedServers := false, false
	for _, name := range a.MCPServers {
		if slices.Contains(stored, name) {
			continue
		}
		if strings.HasPrefix(name, mcpconns.Prefix) {
			if s.d.Connectors == nil {
				continue
			}
			if !loadedConnectors {
				list, err := s.d.Connectors.List(ctx, p)
				if err != nil {
					return err
				}
				connectors, loadedConnectors = list, true
			}
			id := strings.TrimPrefix(name, mcpconns.Prefix)
			if !slices.ContainsFunc(connectors, func(c mcpconns.Connector) bool { return c.ID == id }) {
				return fmt.Errorf("%w: %q is not one of your MCP connectors. Add it on the MCP connectors page, or remove it from the agent", agents.ErrInvalid, name)
			}
			continue
		}
		if a.Model == "" || s.d.MCPServers == nil {
			continue
		}
		if !loadedServers {
			servers, loadedServers = s.d.MCPServers(ctx, p, a.Model), true
		}
		if len(servers) > 0 && !slices.ContainsFunc(servers, func(m ModelMCPServer) bool { return m.Name == name }) {
			return fmt.Errorf("%w: %q is not an MCP server of the model %q. Choose one of its servers, or remove it from the agent", agents.ErrInvalid, name, a.Model)
		}
	}
	return nil
}
