package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

// MCPPrompt is a prompt that an MCP server of a chat offers. Server is the name
// of a server of the model, or "connector:<id>" for a connector of the user.
type MCPPrompt struct {
	Server, Name, Title, Description string
	Arguments                        []MCPPromptArg
}

// MCPPromptArg is an argument of an MCPPrompt.
type MCPPromptArg struct {
	Name, Description string
	Required          bool
}

// MCPResource is a resource that an MCP server of a chat offers.
type MCPResource struct {
	Server, URI, Name, Description, MIMEType string
}

// MCPContent reads the prompts and resources of the MCP servers that a model's
// own configuration has. Every call is made for a user, who must be allowed to
// use MCP; servers names the ones to ask, and the caller has checked them
// against the chat. Nil in Deps: model servers offer no prompts or resources.
type MCPContent interface {
	Prompts(ctx context.Context, user, model string, servers []string) ([]MCPPrompt, error)
	GetPrompt(ctx context.Context, user, model, server, name string, args map[string]string) (string, error)
	Resources(ctx context.Context, user, model string, servers []string) ([]MCPResource, error)
	ReadResource(ctx context.Context, user, model, server, uri string) (string, error)
}

// Errors of the MCP context.
var (
	// ErrMCPServerNotUsable means the chat may not use that server now: it is
	// off for the chat, not granted by the chat's agent, or not the user's.
	ErrMCPServerNotUsable = errors.New("studio: the chat cannot use this MCP server")
	// ErrMCPContent means a server did not give the prompt or resource.
	ErrMCPContent = errors.New("studio: the MCP server did not give the content")
)

// maxContextBytes caps the MCP text that one message carries.
const maxContextBytes = 512 << 10

// usableMCP returns the servers and connector IDs the chat may use now.
func (s *Service) usableMCP(ctx context.Context, user, conv string) (servers, connectors []string, model string, err error) {
	panel, err := s.Tools(ctx, user, conv)
	if err != nil {
		return nil, nil, "", err
	}
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return nil, nil, "", err
	}
	for _, sv := range panel.Servers {
		if sv.Usable && !sv.Off {
			servers = append(servers, sv.Name)
		}
	}
	for _, c := range panel.Connectors {
		if c.Usable && !c.Off {
			connectors = append(connectors, c.ID)
		}
	}
	return servers, connectors, rec.Model, nil
}

// MCPPrompts lists the prompts of the MCP servers and connectors the chat can
// use now. A server that cannot be reached is left out.
func (s *Service) MCPPrompts(ctx context.Context, user, conv string) ([]MCPPrompt, error) {
	p := principal(user)
	servers, conns, model, err := s.usableMCP(ctx, user, conv)
	if err != nil {
		return nil, err
	}
	var out []MCPPrompt
	if s.d.MCPContent != nil && len(servers) > 0 {
		ps, err := s.d.MCPContent.Prompts(ctx, p, model, servers)
		if err != nil {
			s.log.Warn("studio: MCP prompts unavailable", "chat", conv, "error", err)
		}
		out = append(out, ps...)
	}
	if s.d.MCPConnectors != nil {
		for _, id := range conns {
			ps, err := s.d.MCPConnectors.Prompts(ctx, p, id)
			if err != nil {
				s.log.Warn("studio: connector prompts unavailable", "chat", conv, "connector", id, "error", err)
				continue
			}
			for _, pr := range ps {
				mp := MCPPrompt{Server: mcpconns.Prefix + id, Name: pr.Name, Title: pr.Title, Description: pr.Description}
				for _, a := range pr.Arguments {
					mp.Arguments = append(mp.Arguments, MCPPromptArg{Name: a.Name, Description: a.Description, Required: a.Required})
				}
				out = append(out, mp)
			}
		}
	}
	return out, nil
}

// MCPResources lists the resources of the MCP servers and connectors the chat
// can use now.
func (s *Service) MCPResources(ctx context.Context, user, conv string) ([]MCPResource, error) {
	p := principal(user)
	servers, conns, model, err := s.usableMCP(ctx, user, conv)
	if err != nil {
		return nil, err
	}
	var out []MCPResource
	if s.d.MCPContent != nil && len(servers) > 0 {
		rs, err := s.d.MCPContent.Resources(ctx, p, model, servers)
		if err != nil {
			s.log.Warn("studio: MCP resources unavailable", "chat", conv, "error", err)
		}
		out = append(out, rs...)
	}
	if s.d.MCPConnectors != nil {
		for _, id := range conns {
			rs, err := s.d.MCPConnectors.Resources(ctx, p, id)
			if err != nil {
				s.log.Warn("studio: connector resources unavailable", "chat", conv, "connector", id, "error", err)
				continue
			}
			for _, r := range rs {
				out = append(out, MCPResource{Server: mcpconns.Prefix + id, URI: r.URI, Name: r.Name, Description: r.Description, MIMEType: r.MIMEType})
			}
		}
	}
	return out, nil
}

// checkServer says whether the chat may use the named server, and returns the
// chat's model.
func (s *Service) checkServer(ctx context.Context, user, conv, server string) (model string, err error) {
	servers, conns, model, err := s.usableMCP(ctx, user, conv)
	if err != nil {
		return "", err
	}
	if id, ok := strings.CutPrefix(server, mcpconns.Prefix); ok {
		if slices.Contains(conns, id) {
			return model, nil
		}
		return "", ErrMCPServerNotUsable
	}
	if slices.Contains(servers, server) {
		return model, nil
	}
	return "", ErrMCPServerNotUsable
}

// MCPPrompt expands a prompt of a server the chat can use into text.
func (s *Service) MCPPrompt(ctx context.Context, user, conv, server, name string, args map[string]string) (string, error) {
	model, err := s.checkServer(ctx, user, conv, server)
	if err != nil {
		return "", err
	}
	p := principal(user)
	var text string
	if id, ok := strings.CutPrefix(server, mcpconns.Prefix); ok {
		if s.d.MCPConnectors == nil {
			return "", ErrMCPServerNotUsable
		}
		text, err = s.d.MCPConnectors.GetPrompt(ctx, p, id, name, args)
	} else {
		if s.d.MCPContent == nil {
			return "", ErrMCPServerNotUsable
		}
		text, err = s.d.MCPContent.GetPrompt(ctx, p, model, server, name, args)
	}
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrMCPContent, err)
	}
	return text, nil
}

// MCPResource reads a resource of a server the chat can use as text.
func (s *Service) MCPResource(ctx context.Context, user, conv, server, uri string) (string, error) {
	model, err := s.checkServer(ctx, user, conv, server)
	if err != nil {
		return "", err
	}
	p := principal(user)
	var text string
	if id, ok := strings.CutPrefix(server, mcpconns.Prefix); ok {
		if s.d.MCPConnectors == nil {
			return "", ErrMCPServerNotUsable
		}
		text, err = s.d.MCPConnectors.ReadText(ctx, p, id, uri)
	} else {
		if s.d.MCPContent == nil {
			return "", ErrMCPServerNotUsable
		}
		text, err = s.d.MCPContent.ReadResource(ctx, p, model, server, uri)
	}
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrMCPContent, err)
	}
	return text, nil
}

// MCPRef names a prompt or a resource to add to a message as context. Set Name
// (and Arguments) for a prompt, URI for a resource.
type MCPRef struct {
	Server    string
	Name      string
	Arguments map[string]string
	URI       string
}

// WithMCPContext expands the prompts and resources into text and puts it before
// the message, so that the model has them as context for this turn only. The
// text is checked against the chat's servers as the user. It is
// ErrMCPServerNotUsable or ErrMCPContent when one cannot be had, and then
// nothing is sent.
func (s *Service) WithMCPContext(ctx context.Context, user, conv, text string, refs []MCPRef) (string, error) {
	if len(refs) == 0 {
		return text, nil
	}
	var b strings.Builder
	for _, r := range refs {
		var (
			body string
			head string
			err  error
		)
		switch {
		case r.URI != "":
			head = fmt.Sprintf("[MCP resource %s from %s]", r.URI, r.Server)
			body, err = s.MCPResource(ctx, user, conv, r.Server, r.URI)
		case r.Name != "":
			head = fmt.Sprintf("[MCP prompt %s from %s]", r.Name, r.Server)
			body, err = s.MCPPrompt(ctx, user, conv, r.Server, r.Name, r.Arguments)
		default:
			return "", fmt.Errorf("%w: a context entry needs a prompt name or a resource URI", ErrMCPContent)
		}
		if err != nil {
			return "", err
		}
		b.WriteString(head + "\n" + body + "\n\n")
		if b.Len() > maxContextBytes {
			return "", fmt.Errorf("%w: the context is larger than %d bytes", ErrMCPContent, maxContextBytes)
		}
	}
	return b.String() + text, nil
}
