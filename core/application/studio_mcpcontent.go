package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio"
)

// studioMCPContent reads the prompts and resources of the MCP servers that a
// model's configuration has, for the chats. It uses the sessions the model's
// other MCP calls use.
type studioMCPContent struct{ a *Application }

var _ studio.MCPContent = studioMCPContent{}

// sessions returns the named sessions of the model for the servers asked, when
// the user may use MCP at all.
func (c studioMCPContent) sessions(ctx context.Context, user, model string, servers []string) ([]mcpendpoint.NamedSession, error) {
	a := c.a
	cfg, ok := a.ModelConfigLoader().GetModelConfig(model)
	if !ok || !cfg.MCP.HasMCPServers() || a.applicationConfig.DisableMCP {
		return nil, errors.New("the model has no MCP servers")
	}
	if a.authDB != nil {
		u, err := a.loadStudioUser(ctx, user)
		if err != nil || !auth.HasFeatureAccess(a.authDB, u, auth.FeatureMCP) {
			return nil, errors.New("MCP is not available to this account")
		}
	}
	remote, stdio, err := cfg.MCP.MCPConfigFromYAML()
	if err != nil {
		return nil, fmt.Errorf("the MCP configuration of the model is not valid: %w", err)
	}
	return mcpendpoint.NamedSessionsFromMCPConfig(cfg.MCPSessionKey(), remote, stdio, servers)
}

func (c studioMCPContent) Prompts(ctx context.Context, user, model string, servers []string) ([]studio.MCPPrompt, error) {
	ns, err := c.sessions(ctx, user, model, servers)
	if err != nil {
		return nil, err
	}
	found, err := mcpendpoint.DiscoverMCPPrompts(ctx, ns)
	if err != nil {
		return nil, err
	}
	out := make([]studio.MCPPrompt, 0, len(found))
	for _, p := range found {
		mp := studio.MCPPrompt{Server: p.ServerName, Name: p.PromptName, Title: p.Title, Description: p.Description}
		for _, a := range p.Arguments {
			mp.Arguments = append(mp.Arguments, studio.MCPPromptArg{Name: a.Name, Description: a.Description, Required: a.Required})
		}
		out = append(out, mp)
	}
	return out, nil
}

func (c studioMCPContent) GetPrompt(ctx context.Context, user, model, server, name string, args map[string]string) (string, error) {
	ns, err := c.sessions(ctx, user, model, []string{server})
	if err != nil {
		return "", err
	}
	found, err := mcpendpoint.DiscoverMCPPrompts(ctx, ns)
	if err != nil {
		return "", err
	}
	msgs, err := mcpendpoint.GetMCPPrompt(ctx, found, name, args)
	if err != nil {
		return "", err
	}
	text := ""
	for _, m := range msgs {
		if text != "" {
			text += "\n\n"
		}
		text += mcpendpoint.PromptMessageToText(m)
	}
	return text, nil
}

func (c studioMCPContent) Resources(ctx context.Context, user, model string, servers []string) ([]studio.MCPResource, error) {
	ns, err := c.sessions(ctx, user, model, servers)
	if err != nil {
		return nil, err
	}
	found, err := mcpendpoint.DiscoverMCPResources(ctx, ns)
	if err != nil {
		return nil, err
	}
	out := make([]studio.MCPResource, 0, len(found))
	for _, r := range found {
		out = append(out, studio.MCPResource{Server: r.ServerName, URI: r.URI, Name: r.Name, Description: r.Description, MIMEType: r.MIMEType})
	}
	return out, nil
}

func (c studioMCPContent) ReadResource(ctx context.Context, user, model, server, uri string) (string, error) {
	ns, err := c.sessions(ctx, user, model, []string{server})
	if err != nil {
		return "", err
	}
	found, err := mcpendpoint.DiscoverMCPResources(ctx, ns)
	if err != nil {
		return "", err
	}
	return mcpendpoint.ReadMCPResource(ctx, found, uri)
}
