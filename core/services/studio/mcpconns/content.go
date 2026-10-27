package mcpconns

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxContentBytes caps the text of a prompt or resource read from a connector.
const maxContentBytes = 256 << 10

// Prompt is a prompt that a connector's server offers.
type Prompt struct {
	Name, Title, Description string
	Arguments                []PromptArg
}

// PromptArg is an argument of a Prompt.
type PromptArg struct {
	Name, Description string
	Required          bool
}

// Resource is a resource that a connector's server offers.
type Resource struct {
	URI, Name, Description, MIMEType string
}

// live returns the open session of an enabled connector of the user, checking
// what every use checks: MCP is allowed for the user, the connector is theirs
// and on, and a local command is allowed here.
func (s *Service) live(ctx context.Context, user, id string) (Connector, *session, error) {
	if err := s.permit(ctx, user); err != nil {
		return Connector{}, nil, err
	}
	c, err := s.owned(ctx, user, id)
	if err != nil {
		return Connector{}, nil, err
	}
	if !c.Enabled {
		return Connector{}, nil, fmt.Errorf("%w: the connector is switched off", ErrForbidden)
	}
	if c.Transport == TransportStdio {
		if err := s.stdioAllowed(ctx, user); err != nil {
			return Connector{}, nil, err
		}
	}
	se, err := s.session(ctx, c)
	if err != nil {
		return Connector{}, nil, err
	}
	return c, se, nil
}

// fail turns an error of the server into one that holds no secret.
func (s *Service) fail(ctx context.Context, c Connector, what string, err error) error {
	sec, _ := s.open(ctx, c)
	return fmt.Errorf("the connector %s could not %s: %s", c.Name, what, redact(err.Error(), c, sec))
}

// Prompts lists the prompts of the user's connector.
func (s *Service) Prompts(ctx context.Context, user, id string) ([]Prompt, error) {
	c, se, err := s.live(ctx, user, id)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer cancel()
	res, err := se.conn.cs.ListPrompts(cctx, nil)
	if err != nil {
		return nil, s.fail(ctx, c, "list its prompts", err)
	}
	out := make([]Prompt, 0, len(res.Prompts))
	for _, p := range res.Prompts {
		pr := Prompt{Name: p.Name, Title: p.Title, Description: p.Description}
		for _, a := range p.Arguments {
			pr.Arguments = append(pr.Arguments, PromptArg{Name: a.Name, Description: a.Description, Required: a.Required})
		}
		out = append(out, pr)
	}
	return out, nil
}

// GetPrompt expands a prompt of the user's connector into text: its messages,
// one block per message, headed with the role.
func (s *Service) GetPrompt(ctx context.Context, user, id, name string, args map[string]string) (string, error) {
	c, se, err := s.live(ctx, user, id)
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer cancel()
	res, err := se.conn.cs.GetPrompt(cctx, &mcp.GetPromptParams{Name: name, Arguments: args})
	if err != nil {
		return "", s.fail(ctx, c, "expand the prompt", err)
	}
	var b strings.Builder
	for _, m := range res.Messages {
		if tc, ok := m.Content.(*mcp.TextContent); ok {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(tc.Text)
		}
	}
	return capText(b.String()), nil
}

// Resources lists the resources of the user's connector.
func (s *Service) Resources(ctx context.Context, user, id string) ([]Resource, error) {
	c, se, err := s.live(ctx, user, id)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer cancel()
	res, err := se.conn.cs.ListResources(cctx, nil)
	if err != nil {
		return nil, s.fail(ctx, c, "list its resources", err)
	}
	out := make([]Resource, 0, len(res.Resources))
	for _, r := range res.Resources {
		out = append(out, Resource{URI: r.URI, Name: r.Name, Description: r.Description, MIMEType: r.MIMEType})
	}
	return out, nil
}

// ReadText reads a resource of the user's connector as text. Binary parts are
// left out.
func (s *Service) ReadText(ctx context.Context, user, id, uri string) (string, error) {
	c, se, err := s.live(ctx, user, id)
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer cancel()
	res, err := se.conn.cs.ReadResource(cctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return "", s.fail(ctx, c, "read the resource", err)
	}
	var parts []string
	for _, ct := range res.Contents {
		if ct.Text != "" {
			parts = append(parts, ct.Text)
		}
	}
	return capText(strings.Join(parts, "\n")), nil
}

func capText(s string) string {
	if len(s) <= maxContentBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxContentBytes], "") + "\n[cut: the content is longer than 256 KiB]"
}
