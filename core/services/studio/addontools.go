package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"

	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
	// Shadows is the built-in chat tool this tool replaces (see shadow.go), or
	// empty: the tool keeps its add-on name. A tool that shadows bash is the
	// add-on's tool that runs shell commands and code.
	Shadows string
	// Execution is what the add-on declares about a tool that shadows bash
	// (its provider title and note), or nil.
	Execution *contract.ExecutionDescriptor
	// InstalledAt is when the add-on was first installed (RFC 3339, UTC), or
	// empty. It decides the default provider of a shadowed name.
	InstalledAt string
	// Permission is the capability (contract.ToolPermission) that the sender
	// of a connector turn needs to use the tool, or empty. The host sets it
	// from the manifest; the core does not know which add-on declares it.
	Permission string
	// Credentialed is true for a tool of an add-on that declares a credential.
	// Its calls run with the token of a connection from the set the person
	// enabled for the chat (see Connectors.ConnectionsOf); until then a chat
	// does not offer it.
	Credentialed bool
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
func unavailableToolName(addonID string) string { return addontools.ToolName(addonID, "unavailable") }

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
	// Credentialed is true for an add-on whose tools run with the token of a
	// connection. Connections lists the person's connections for it, Enabled
	// is the set this chat may use (empty when none is enabled) and Enabled.By
	// says why: ConnectionByAgent is read only in the chat.
	Credentialed bool
	Connections  []Connection
	Enabled      ConnectionSet
	// Title and Description are what the add-on declares for people, when its
	// broker can say (see AddonLister). Empty otherwise.
	Title, Description string
}

// AddonListing is the title and the description an add-on declares in its
// manifest.
type AddonListing struct {
	AddonID, Title, Description string
}

// AddonLister is implemented by a broker that knows what each add-on declares
// for people. It is optional: without it the lists show the add-on IDs only.
type AddonLister interface {
	AddonListings(ctx context.Context, user string) ([]AddonListing, error)
}

// SetAddonsOff sets the add-ons a chat has switched off, replacing the earlier
// list. An ID appears once. The chat of another user is session.ErrNotFound.
func (s *Service) SetAddonsOff(ctx context.Context, user, conv string, off []string) error {
	clean, err := cleanAddonSwitches(off)
	if err != nil {
		return err
	}
	return s.d.Store.SetAddonsOff(ctx, principal(user), conv, clean)
}

// cleanAddonSwitches checks and dedups a list of add-on IDs.
func cleanAddonSwitches(off []string) ([]string, error) {
	if len(off) > maxAddonSwitches {
		return nil, fmt.Errorf("%w: at most %d switches", ErrBadAddonID, maxAddonSwitches)
	}
	seen := make(map[string]bool, len(off))
	clean := make([]string, 0, len(off))
	for _, id := range off {
		if !addonIDShape.MatchString(id) {
			return nil, fmt.Errorf("%w: %q", ErrBadAddonID, id)
		}
		if !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	return clean, nil
}

// Addons lists the add-ons whose tools the user may use, with the chat's
// switch for each. Without the add-on host it lists none.
func (s *Service) Addons(ctx context.Context, user, conv string) ([]AddonInfo, error) {
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return nil, err
	}
	return s.addonsFor(ctx, user, rec)
}

// addonsFor lists the add-ons of Addons with the switches of rec.
func (s *Service) addonsFor(ctx context.Context, user string, rec session.ChatRecord) ([]AddonInfo, error) {
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
	credentialed := map[string]bool{}
	for _, t := range tools {
		n[t.AddonID]++
		credentialed[t.AddonID] = credentialed[t.AddonID] || t.Credentialed
	}
	listed := map[string]AddonListing{}
	if l, ok := s.d.AddonTools.(AddonLister); ok {
		if list, err := l.AddonListings(ctx, principal(user)); err != nil {
			s.log.Warn("studio: add-on titles unavailable", "user", user, "error", err)
		} else {
			for _, a := range list {
				listed[a.AddonID] = a
			}
		}
	}
	out := make([]AddonInfo, 0, len(n))
	for id, c := range n {
		info := AddonInfo{ID: id, Tools: c, Off: off[id], Title: listed[id].Title, Description: listed[id].Description, Credentialed: credentialed[id]}
		if info.Credentialed && s.connectors != nil {
			var err error
			if info.Connections, err = s.connectors.Connections(ctx, user, id); err != nil {
				s.log.Warn("studio: connections unavailable", "user", user, "addon", id, "error", err)
			}
			if info.Enabled, err = s.connectors.ConnectionsOf(ctx, user, rec, id); err != nil {
				s.log.Warn("studio: connection unavailable", "user", user, "addon", id, "error", err)
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// addonTools turns the add-on tools the chat's user may use, less the add-ons
// the chat switched off and, for a chat with an agent, those the agent does not
// name, into chat tools. A host that cannot answer gives no tools and a warning;
// it never fails the chat. The second result names the add-ons the agent grants
// that the user cannot use.
func (s *Service) addonTools(ctx context.Context, a workspace.Actor, rec session.ChatRecord, sc *scope) ([]session.Tool, []string, error) {
	pins, err := s.toolPins(ctx)
	if err != nil {
		return nil, nil, err
	}
	if s.d.AddonTools == nil {
		if sc != nil {
			return nil, prefixed("add-on", sc.addonNames), nil
		}
		return nil, nil, nil
	}
	defs, err := s.d.AddonTools.Tools(ctx, a.Principal)
	if err != nil {
		s.log.Warn("studio: add-on tools unavailable", "chat", a.ChatID, "error", err)
		return nil, nil, nil
	}
	off := make(map[string]bool, len(rec.AddonsOff))
	for _, id := range rec.AddonsOff {
		off[id] = true
	}
	have := map[string]bool{}
	// replaces maps an add-on to the built-in tools it replaces, for the
	// stand-in of one that cannot work.
	replaces := map[string][]string{}
	for _, d := range defs {
		if d.Shadows != "" && !slices.Contains(replaces[d.AddonID], d.Shadows) {
			replaces[d.AddonID] = append(replaces[d.AddonID], d.Shadows)
		}
	}
	var tools []session.Tool
	pol, guarded := s.actPolicy(ctx, rec)
	gate, _ := s.d.AddonTools.(AddonGate)
	reasons := map[string]string{}
	enabled := map[string]ConnectionSet{}
	for _, d := range defs {
		have[d.AddonID] = true
		// A tool that runs with a credential is offered only once the person
		// has enabled at least one connection for the chat. The host picks
		// none for the person.
		var conns ConnectionSet
		if d.Credentialed {
			var seen bool
			if conns, seen = enabled[d.AddonID]; !seen {
				conns = s.enabledConnections(ctx, a.Principal, rec, d.AddonID)
				enabled[d.AddonID] = conns
			}
			if len(conns.Items) == 0 {
				continue
			}
		}
		if gate != nil && !off[d.AddonID] && (sc == nil || sc.addons[d.AddonID]) {
			reason, known := reasons[d.AddonID]
			if !known {
				reason = gate.Unavailable(ctx, a.Principal, d.AddonID, s.d.Files.Available())
				reasons[d.AddonID] = reason
				if reason != "" {
					stand := unavailableTool(d.AddonID, reason)
					stand.StandInFor = replaces[d.AddonID]
					tools = append(tools, stand)
				}
			}
			if reason != "" {
				continue
			}
		}
		var grants []string
		if sc != nil {
			grants = sc.addonNames
		}
		if !AddonToolEligible(d, rec, grants) {
			continue
		}
		name := d.Name
		schema := d.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		if len(conns.Items) > 1 {
			// Two or more connections: the model names one in every call.
			var ok bool
			if schema, ok = withConnectionArgument(schema, conns.Items); !ok {
				s.log.Warn("studio: tool not offered, its schema cannot take the connection argument", "addon", d.AddonID, "tool", d.Name)
				continue
			}
		}
		if t, ok := s.shadowTool(a, d, schema); ok {
			if d.Credentialed {
				t.Run = s.connectionRun(a, d.AddonID, conns, t.Run)
			}
			if guarded {
				t = s.gateAddonAct(rec, pol, t, d)
			}
			tools = append(tools, t)
			continue
		}
		run := func(ctx context.Context, args json.RawMessage) (string, error) {
			return s.d.AddonTools.Call(ctx, a.Principal, a.ChatID, name, args)
		}
		if d.Credentialed {
			run = s.connectionRun(a, d.AddonID, conns, run)
		}
		tool := session.Tool{
			Name: name, Description: d.Description, Schema: schema,
			// The user and the conversation come from the chat, never from the
			// arguments the model wrote.
			Run: run,
		}
		if d.Permission != "" && rec.Origin == session.OriginConnector {
			var ok bool
			if tool, ok = s.gateAddonTool(tool, d.Permission, a.ChatID); !ok {
				continue
			}
		}
		if guarded {
			tool = s.gateAddonAct(rec, pol, tool, d)
		}
		tools = append(tools, tool)
	}
	installed := map[string]string{}
	for _, d := range defs {
		installed[d.AddonID] = d.InstalledAt
	}
	tools = resolveShadows(tools, pins, installed, s.shadowersFor(ctx, a.Principal))
	var missing []string
	if sc != nil {
		for _, id := range sc.addonNames {
			if !have[id] {
				missing = append(missing, "add-on "+id)
			}
		}
	}
	return tools, missing, nil
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

// gateAddonTool holds a tool of an add-on to the capability it declares (see
// AddonTool.Permission) in a connector chat. The tool is offered only when the
// sender of the turn holds the capability, and a call checks again and records
// the decision, because the grant of a turn can shrink after the tools were
// offered. The second result is false when the tool must not be offered. A chat
// that no connector started has no sender and is not gated here: the write
// mode of the binding and the confirmation of the proposal govern it.
func (s *Service) gateAddonTool(t session.Tool, permission, chat string) (session.Tool, bool) {
	allow, audit := s.turnAllow(chat)
	c := builtins.Chat{Allow: allow, Audit: audit}
	if held, _ := c.Holds(permission); !held {
		return t, false
	}
	run := t.Run
	t.Run = func(ctx context.Context, args json.RawMessage) (string, error) {
		if err := c.Require(permission); err != nil {
			return "", err
		}
		return run(ctx, args)
	}
	return t, true
}

// enabledConnections is the set of connections the chat rec may use for the
// add-on (see Connectors.ConnectionsOf). Without the connector service nothing
// is enabled, and a doubt counts as none.
func (s *Service) enabledConnections(ctx context.Context, user string, rec session.ChatRecord, addonID string) ConnectionSet {
	if s.connectors == nil {
		return ConnectionSet{}
	}
	set, err := s.connectors.ConnectionsOf(ctx, user, rec, addonID)
	if err != nil {
		return ConnectionSet{}
	}
	return set
}

// connectionRun wraps the call of a tool of an add-on that runs with a
// credential. The host, not the add-on, owns the `connection` argument:
//
//   - The set is read again from the stored chat at each call, so a connection
//     the person took away is gone for the rest of the turn too.
//   - With one connection the argument is not offered. A model that sends it
//     anyway must name that connection.
//   - With two or more it is required, for every tool, read or write: the host
//     never infers it.
//   - The name is looked up in the set only. A name that is not in it, or none,
//     returns a plain sentence that lists the allowed names, and nothing runs.
//   - The argument is removed before the call, and the binding goes to the
//     owner in the context, where only host code sets it.
func (s *Service) connectionRun(a workspace.Actor, addonID string, offered ConnectionSet, run func(context.Context, json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		set := offered
		rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
		switch {
		case err == nil:
			set = s.enabledConnections(ctx, a.Principal, rec, addonID)
		case !errors.Is(err, session.ErrNotFound):
			return "", err
		}
		var in map[string]json.RawMessage
		if len(bytes.TrimSpace(args)) > 0 && json.Unmarshal(args, &in) != nil {
			return "", fmt.Errorf("the arguments of the call are not a JSON object")
		}
		raw, present := in[ConnectionArgument]
		delete(in, ConnectionArgument)
		var given string
		if present && json.Unmarshal(raw, &given) != nil {
			given, present = "", false
		}
		var chosen Connection
		switch {
		case len(set.Items) == 0:
			return connectionSentence(nil, "", false), nil
		case len(set.Items) == 1 && !present:
			chosen = set.Items[0]
		default:
			var ok bool
			if chosen, ok = pickConnection(set.Items, given); !ok {
				return connectionSentence(set.Items, given, present && given != ""), nil
			}
		}
		if in != nil {
			if args, err = json.Marshal(in); err != nil {
				return "", err
			}
		}
		return run(addontools.WithConnection(ctx, chosen.ID), args)
	}
}
