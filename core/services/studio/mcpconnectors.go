package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/mcpapps"
)

// ConnectorTools is the user's own MCP servers as the chats see them. Both
// calls run as the user, who must have the MCP feature; the host decides.
type ConnectorTools interface {
	// List returns the user's connectors, without their secrets.
	List(ctx context.Context, user string) ([]mcpconns.Connector, error)
	// Resolve returns the tools of the connectors with these IDs, or of every
	// enabled connector of the user with all. A connector that cannot be used
	// (switched off, deleted, another user's, unreachable) is a phrase for a
	// note to the model, never an error.
	Resolve(ctx context.Context, user string, refs []string, all bool) (tools []mcpconns.Tool, missing []string)
}

// ModelMCPServer is an MCP server of the model's own configuration.
type ModelMCPServer struct {
	Name string
	// Auto says that a chat with no agent uses it by itself.
	Auto bool
}

// ErrBadMCPSwitch means a switch names something that is not an MCP server or a
// connector, or there are too many switches.
var ErrBadMCPSwitch = errors.New("studio: an MCP switch is not valid")

// maxMCPSwitches bounds the MCP switches of one chat.
const maxMCPSwitches = 200

var (
	connectorSwitchShape = regexp.MustCompile(`^connector:[A-Za-z0-9_-]{1,64}$`)
	serverSwitchShape    = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,200}$`)
)

// SetMcpOff sets the MCP servers and connectors a chat has switched off,
// replacing the earlier list: a name for a server of the model, "connector:<id>"
// for a connector. A name appears once. A switch never adds a tool; for a chat
// with an agent it only narrows what the agent grants. The chat of another user
// is session.ErrNotFound.
func (s *Service) SetMcpOff(ctx context.Context, user, conv string, off []string) error {
	if len(off) > maxMCPSwitches {
		return fmt.Errorf("%w: at most %d switches", ErrBadMCPSwitch, maxMCPSwitches)
	}
	seen := make(map[string]bool, len(off))
	clean := make([]string, 0, len(off))
	for _, n := range off {
		ok := serverSwitchShape.MatchString(n)
		if strings.HasPrefix(n, mcpconns.Prefix) {
			ok = connectorSwitchShape.MatchString(n)
		}
		if !ok {
			return fmt.Errorf("%w: %q", ErrBadMCPSwitch, n)
		}
		if !seen[n] {
			seen[n] = true
			clean = append(clean, n)
		}
	}
	return s.d.Store.SetMcpOff(ctx, principal(user), conv, clean)
}

// ModelMCPServerSwitch is a server of the model with the chat's switch.
type ModelMCPServerSwitch struct {
	Name        string
	Off, Usable bool
	Reason      string
}

// ConnectorSwitch is a connector of the user with the chat's switch.
type ConnectorSwitch struct {
	ID, Name    string
	Off, Usable bool
	Reason      string
}

// ToolsPanel is what a chat can use besides its built-in tools, each with the
// chat's switch: add-ons, the MCP servers of the model and the user's
// connectors. A chat with an agent lists only what the agent grants.
type ToolsPanel struct {
	Addons     []AddonInfo
	Servers    []ModelMCPServerSwitch
	Connectors []ConnectorSwitch
	// Off is the list of MCP switches in force.
	Off []string
}

// Tools lists what a chat can use, with its switches.
func (s *Service) Tools(ctx context.Context, user, conv string) (ToolsPanel, error) {
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return ToolsPanel{}, err
	}
	p := ToolsPanel{Off: append([]string{}, rec.McpOff...)}
	if p.Addons, err = s.Addons(ctx, user, conv); err != nil {
		return p, err
	}
	sc := s.scopeOf(ctx, rec)
	off := set(rec.McpOff)
	granted := func(name string) bool { return sc == nil }
	var grantedIDs []string
	if sc != nil {
		have := map[string]bool{}
		for _, n := range sc.mcp {
			have[n] = true
			if id, ok := strings.CutPrefix(n, mcpconns.Prefix); ok {
				grantedIDs = append(grantedIDs, id)
			}
		}
		granted = func(name string) bool { return have[name] }
	}

	if s.d.MCPServers != nil {
		listed := map[string]bool{}
		for _, m := range s.d.MCPServers(ctx, principal(user), rec.Model) {
			listed[m.Name] = true
			if !granted(m.Name) {
				continue
			}
			sw := ModelMCPServerSwitch{Name: m.Name, Off: off[m.Name], Usable: m.Auto || sc != nil}
			if !sw.Usable {
				sw.Reason = "the model does not use this server unless a saved agent names it"
			}
			p.Servers = append(p.Servers, sw)
		}
		if sc != nil {
			for _, n := range sc.mcp {
				if !strings.HasPrefix(n, mcpconns.Prefix) && !listed[n] {
					p.Servers = append(p.Servers, ModelMCPServerSwitch{Name: n, Off: off[n], Reason: "the model has no such server"})
				}
			}
		}
	}

	if s.d.Connectors != nil {
		mine, err := s.d.Connectors.List(ctx, principal(user))
		if err != nil {
			s.log.Warn("studio: connectors unavailable", "chat", conv, "error", err)
		}
		byID := map[string]mcpconns.Connector{}
		for _, c := range mine {
			byID[c.ID] = c
		}
		add := func(c mcpconns.Connector) {
			sw := ConnectorSwitch{ID: c.ID, Name: c.Name, Off: off[mcpconns.Prefix+c.ID], Usable: c.Enabled}
			if !c.Enabled {
				sw.Reason = "switched off on the Connectors page"
			}
			p.Connectors = append(p.Connectors, sw)
		}
		if sc == nil {
			for _, c := range mine {
				add(c)
			}
		} else {
			for _, id := range grantedIDs {
				if c, ok := byID[id]; ok {
					add(c)
				} else {
					p.Connectors = append(p.Connectors, ConnectorSwitch{ID: id, Name: id, Off: off[mcpconns.Prefix+id], Reason: "deleted"})
				}
			}
		}
	} else if sc != nil {
		for _, id := range grantedIDs {
			p.Connectors = append(p.Connectors, ConnectorSwitch{ID: id, Name: id, Off: off[mcpconns.Prefix+id], Reason: "connectors are not available"})
		}
	}
	return p, nil
}

// connectorTools turns the tools of the user's connectors into chat tools.
// A chat with no agent gets every enabled connector that is not switched off; a
// chat with an agent gets the ones in refs (already less the switched-off).
func (s *Service) connectorTools(ctx context.Context, a workspace.Actor, rec session.ChatRecord, sc *scope, refs []string, off map[string]bool, have []session.Tool) ([]session.Tool, []string) {
	if s.d.Connectors == nil {
		if sc == nil {
			return nil, nil
		}
		return nil, prefixed("MCP connector", refs)
	}
	if sc == nil {
		mine, err := s.d.Connectors.List(ctx, a.Principal)
		if err != nil {
			s.log.Warn("studio: connectors unavailable", "chat", a.ChatID, "error", err)
			return nil, nil
		}
		for _, c := range mine {
			if c.Enabled && !off[mcpconns.Prefix+c.ID] {
				refs = append(refs, c.ID)
			}
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	defs, missing := s.d.Connectors.Resolve(ctx, a.Principal, refs, false)

	// A name that is reserved is dropped. A name two connectors share, or that
	// a tool of the chat has already, gets the connector's name in front, so
	// that no tool hides another.
	taken := map[string]bool{}
	for _, t := range have {
		taken[t.Name] = true
	}
	uses := map[string]int{}
	var ok []mcpconns.Tool
	for _, d := range defs {
		if mcpendpoint.IsAddonToolName(d.Name) || reservedToolNames[d.Name] {
			s.log.Warn("studio: a connector tool takes a reserved name and was dropped", "chat", a.ChatID, "connector", d.ConnectorID, "tool", d.Name)
			continue
		}
		if !mcpapps.ParseUI(d.Meta).ForModel() {
			continue // a tool for the app only is never offered to the model
		}
		uses[d.Name]++
		ok = append(ok, d)
	}
	var tools []session.Tool
	for _, d := range ok {
		name := d.Name
		if uses[name] > 1 || taken[name] {
			name = prefixName(d.Connector, d.Name)
			if mcpendpoint.IsAddonToolName(name) || reservedToolNames[name] || taken[name] {
				s.log.Warn("studio: a connector tool could not get a free name and was dropped", "chat", a.ChatID, "connector", d.ConnectorID, "tool", d.Name)
				continue
			}
		}
		taken[name] = true
		run := d.Run
		if ui := mcpapps.ParseUI(d.Meta); ui.ResourceURI != "" && d.RunApp != nil {
			run = withApp(mcpconns.Prefix+d.ConnectorID, d.Name, ui, d.RunApp)
		}
		tools = append(tools, session.Tool{
			Name: name, Description: d.Description, Schema: d.Schema,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				out, err := run(ctx, args)
				if err != nil {
					return "", err
				}
				if len(out) > session.MaxOutput {
					out = session.CapText(out, session.MaxOutput) + "\n[output cut]"
				}
				return out, nil
			},
		})
	}
	return tools, missing
}

var nonToolChar = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// prefixName is "<connector>_<tool>" in the characters a tool name may have, at
// most 64 of them.
func prefixName(connector, tool string) string {
	p := strings.Trim(nonToolChar.ReplaceAllString(connector, "_"), "_")
	if p == "" {
		p = "connector"
	}
	name := p + "_" + tool
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}
