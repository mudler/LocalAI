package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// AddonTool is one tool of an installed add-on, as a chat offers it to the model.
type AddonTool struct {
	Name        string // the function name the model calls
	AddonID     string
	Description string
	Schema      json.RawMessage
	// Action is true for a tool that acts at once as the user (runs code, edits
	// files, changes data) instead of only reading.
	Action bool
	// Runs is true for the tool that runs shell commands and code. A chat
	// without such a tool is told that it cannot run anything.
	Runs bool
	// Shadows is the built-in chat tool this tool replaces (see shadow.go), or
	// empty: the tool keeps its add-on name.
	Shadows string
	// InstalledAt is when the add-on was first installed (RFC 3339, UTC), or
	// empty. It decides the default provider of a shadowed name.
	InstalledAt string
}

// AddonTools is the add-on broker as the chats see it. Both calls run as the
// user; the host decides what that user may see and do, and turns a result into
// text for the model (a change the chat made says what was done and whether the
// user can undo it, with the change ID; one that stays a proposal asks the user
// to confirm it).
type AddonTools interface {
	// Tools lists the tools the user may use now.
	Tools(ctx context.Context, user string) ([]AddonTool, error)
	// Call runs one tool of a conversation as the user and returns what the
	// model is told.
	Call(ctx context.Context, user, conv, name string, args json.RawMessage) (string, error)
}

// AddonGate is implemented by a broker that knows when an add-on cannot work
// for a user now: its backend is missing, or it needs the workspace and the
// workspace is off. It is optional; a broker without it offers every tool.
type AddonGate interface {
	// Unavailable returns "" when the tools of the add-on can work, or a short
	// plain reason when they cannot. workspace says whether the Files
	// workspace is on, for an add-on that needs it. It must not fail a chat:
	// when the broker cannot tell, it answers "".
	Unavailable(ctx context.Context, user, addonID string, workspace bool) string
}

// unavailableToolName is the name of the stand-in tool of an add-on that
// cannot work. It has the shape of the broker's tool names.
func unavailableToolName(addonID string) string { return "addon__" + addonID + "__unavailable" }

// ErrBadAddonID means a switch names something that is not an add-on ID, or
// there are too many switches.
var ErrBadAddonID = errors.New("studio: an add-on ID is not valid")

// maxAddonSwitches bounds the switches of one chat.
const maxAddonSwitches = 200

var addonIDShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// AddonInfo is one add-on a chat can use, with its switch.
type AddonInfo struct {
	ID    string
	Tools int
	Off   bool
}

// SetAddonsOff sets the add-ons a chat has switched off, replacing the earlier
// list. An ID appears once. The chat of another user is session.ErrNotFound.
func (s *Service) SetAddonsOff(ctx context.Context, user, conv string, off []string) error {
	if len(off) > maxAddonSwitches {
		return fmt.Errorf("%w: at most %d switches", ErrBadAddonID, maxAddonSwitches)
	}
	seen := make(map[string]bool, len(off))
	clean := make([]string, 0, len(off))
	for _, id := range off {
		if !addonIDShape.MatchString(id) {
			return fmt.Errorf("%w: %q", ErrBadAddonID, id)
		}
		if !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	return s.d.Store.SetAddonsOff(ctx, principal(user), conv, clean)
}

// Addons lists the add-ons whose tools the user may use, with the chat's
// switch for each. Without the add-on host it lists none.
func (s *Service) Addons(ctx context.Context, user, conv string) ([]AddonInfo, error) {
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return nil, err
	}
	if s.d.AddonTools == nil {
		return nil, nil
	}
	tools, err := s.d.AddonTools.Tools(ctx, principal(user))
	if err != nil {
		return nil, err
	}
	off := map[string]bool{}
	for _, id := range rec.AddonsOff {
		off[id] = true
	}
	n := map[string]int{}
	for _, t := range tools {
		n[t.AddonID]++
	}
	out := make([]AddonInfo, 0, len(n))
	for id, c := range n {
		out = append(out, AddonInfo{ID: id, Tools: c, Off: off[id]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// addonTools turns the add-on tools the chat's user may use, less the add-ons
// the chat switched off and, for a chat with an agent, those the agent does not
// name, into chat tools. A host that cannot answer gives no tools and a warning;
// it never fails the chat. The second result names the add-ons the agent grants
// that the user cannot use.
func (s *Service) addonTools(ctx context.Context, a workspace.Actor, rec session.ChatRecord, sc *scope) ([]session.Tool, []string) {
	if s.d.AddonTools == nil {
		if sc != nil {
			return nil, prefixed("add-on", sc.addonNames)
		}
		return nil, nil
	}
	defs, err := s.d.AddonTools.Tools(ctx, a.Principal)
	if err != nil {
		s.log.Warn("studio: add-on tools unavailable", "chat", a.ChatID, "error", err)
		return nil, nil
	}
	off := make(map[string]bool, len(rec.AddonsOff))
	for _, id := range rec.AddonsOff {
		off[id] = true
	}
	have := map[string]bool{}
	var tools []session.Tool
	gate, _ := s.d.AddonTools.(AddonGate)
	reasons := map[string]string{}
	for _, d := range defs {
		have[d.AddonID] = true
		s.runners.mark(d)
		if gate != nil && !off[d.AddonID] && (sc == nil || sc.addons[d.AddonID]) {
			reason, known := reasons[d.AddonID]
			if !known {
				reason = gate.Unavailable(ctx, a.Principal, d.AddonID, s.d.Files.Available())
				reasons[d.AddonID] = reason
				if reason != "" {
					tools = append(tools, unavailableTool(d.AddonID, reason))
				}
			}
			if reason != "" {
				continue
			}
		}
		if off[d.AddonID] || reservedToolNames[d.Name] || (sc != nil && !sc.addons[d.AddonID]) {
			continue
		}
		// An unattended run with no agent never gets a tool that acts at once:
		// the owner's explicit grant, in an agent, is what allows it.
		if d.Action && rec.Origin != "" && rec.AgentID == "" {
			continue
		}
		name := d.Name
		schema := d.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		if t, ok := s.shadowTool(a, d, schema); ok {
			tools = append(tools, t)
			continue
		}
		tools = append(tools, session.Tool{
			Name: name, Description: d.Description, Schema: schema,
			// The user and the conversation come from the chat, never from the
			// arguments the model wrote.
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				return s.d.AddonTools.Call(ctx, a.Principal, a.ChatID, name, args)
			},
		})
	}
	var pins map[string]string
	if s.d.ToolPins != nil {
		pins = s.d.ToolPins()
	}
	installed := map[string]string{}
	for _, d := range defs {
		installed[d.AddonID] = d.InstalledAt
	}
	tools = resolveShadows(tools, pins, installed)
	var missing []string
	if sc != nil {
		for _, id := range sc.addonNames {
			if !have[id] {
				missing = append(missing, "add-on "+id)
			}
		}
	}
	return tools, missing
}

// unavailableTool is the one tool a chat gets for an add-on that cannot work:
// it tells the model why, so the model does not try the others and the user
// gets a plain answer.
func unavailableTool(addonID, reason string) session.Tool {
	msg := "The " + addonID + " add-on cannot be used in this chat: " + reason + ". Tell the user this instead of trying."
	return session.Tool{
		Name:        unavailableToolName(addonID),
		Description: msg,
		Schema:      json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Run:         func(context.Context, json.RawMessage) (string, error) { return msg, nil },
	}
}
