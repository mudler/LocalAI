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
	"strings"

	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
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
	// ApprovalOverridable is true for a write tool whose confirmation the
	// person can relax with their own setting. Tool is its name inside the
	// add-on, the key of that setting.
	ApprovalOverridable bool
	Tool                string
	// ResourceScoped is true for a tool of an add-on whose manifest opts in to
	// per-agent resources: a chat of a saved agent reaches only the resources
	// on the list of the agent (see Agent.Resources).
	ResourceScoped bool
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

// holdForPerson marks a call whose write must wait for the person, even in a
// chat that applies the writes of an add-on at once.
func holdForPerson(ctx context.Context) context.Context { return addontools.HoldForPerson(ctx) }

// roleToolName is the name a chat sees for a tool of an add-on.
func roleToolName(addonID, tool string) string { return addontools.ToolName(addonID, tool) }

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
	// ToolList names the tools of the add-on the user may use and says which
	// change things.
	ToolList []AddonToolEntry
	// Resources is set when the add-on scopes its resources per agent.
	Resources *ResourceLabel
}

// AddonToolEntry is the name of a tool of an add-on, as the add-on declares
// it, and whether it changes things.
type AddonToolEntry struct {
	Name  string
	Write bool
}

// AddonListing is the title and the description an add-on declares in its
// manifest.
type AddonListing struct {
	AddonID, Title, Description string
	// Resources is set for an add-on whose manifest scopes its resources per
	// agent (see AddonTool.ResourceScoped).
	Resources *ResourceLabel
}

// ResourceLabel is the word an add-on uses for one of its resources: the
// singular, the plural, and the translations by language tag.
type ResourceLabel struct {
	Noun, NounPlural string
	Translations     map[string]contract.ResourceText
}

// AddonResourceChooser is implemented by a broker that can list the resources
// of an add-on that a person may give to a saved agent.
type AddonResourceChooser interface {
	// ResourceChoices lists the resources the user may use now for the add-on,
	// before any limit of an agent. The bool says the list is cut short. An
	// add-on that does not scope its resources per agent is ErrNotScoped.
	ResourceChoices(ctx context.Context, user, addonID string) ([]ResourceChoice, bool, error)
}

// ResourceChoice is one resource a person may give to an agent. Role is the
// person's own role on it: viewer, editor or owner.
type ResourceChoice struct{ ID, Name, Role string }

// ErrNotScoped means the add-on does not scope its resources per agent, or is
// not there for the user.
var ErrNotScoped = errors.New("studio: the add-on has no resources to give to an agent")

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
	list := map[string][]AddonToolEntry{}
	for _, t := range tools {
		n[t.AddonID]++
		credentialed[t.AddonID] = credentialed[t.AddonID] || t.Credentialed
		list[t.AddonID] = append(list[t.AddonID], AddonToolEntry{Name: t.Tool, Write: t.Action})
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
		info := AddonInfo{ID: id, Tools: c, Off: off[id], Title: listed[id].Title, Description: listed[id].Description, Credentialed: credentialed[id],
			ToolList: list[id], Resources: listed[id].Resources}
		slices.SortFunc(info.ToolList, func(a, b AddonToolEntry) int { return strings.Compare(a.Name, b.Name) })
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
	tools, missing, _, err := s.addonToolsHeld(ctx, a, rec, sc)
	return tools, missing, err
}

// addonToolsHeld is addonTools that also says which tools of an add-on the chat
// would use but must not have, and why, so a messaging chat can tell the model
// (see deniedToolsNote). Only a tool the agent and the chat switches let in is
// reported: a tool the owner never gave to the agent is not "held back".
func (s *Service) addonToolsHeld(ctx context.Context, a workspace.Actor, rec session.ChatRecord, sc *scope) ([]session.Tool, []string, []builtins.Built, error) {
	pins, err := s.toolPins(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if s.d.AddonTools == nil {
		if sc != nil {
			return nil, prefixed("add-on", sc.addonNames), nil, nil
		}
		return nil, nil, nil, nil
	}
	defs, err := s.d.AddonTools.Tools(ctx, a.Principal)
	if err != nil {
		s.log.Warn("studio: add-on tools unavailable", "chat", a.ChatID, "error", err)
		return nil, nil, nil, nil
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
	providers := s.providerGateFor(ctx, a.Principal, &rec)
	// The writes of a provider count against the same limit of a turn as the
	// notes the built-in provider saves at once.
	writes := &autoTurn{}
	held := &heldTools{}
	for _, d := range defs {
		have[d.AddonID] = true
		// The tools a provider fills a role with are offered only while it is
		// the provider the person picked and the chat has the slot on.
		if providers.hidden(d.AddonID, d.Name) {
			continue
		}
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
				if !off[d.AddonID] && (sc == nil || sc.addons[d.AddonID]) {
					held.add(d, "no connection is selected for the "+d.AddonID+" add-on in this agent, so its tools are not available")
				}
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
			if d.ResourceScoped {
				t.Run = s.agentResourceRun(a, t.Run)
			}
			if d.Credentialed {
				t.Run = s.connectionRun(a, d.AddonID, conns, t.Run)
			}
			if guarded {
				t = s.gateAddonAct(rec, pol, t, d)
			}
			t.Run = s.outcomeRun(rec, a, d, t.Run)
			tools = append(tools, t)
			continue
		}
		run := func(ctx context.Context, args json.RawMessage) (string, error) {
			return s.d.AddonTools.Call(ctx, a.Principal, a.ChatID, name, args)
		}
		if d.ResourceScoped {
			run = s.agentResourceRun(a, run)
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
		// A write of the provider that serves the notes follows the save
		// level, the grants and the messaging rules of the host.
		if ref, ok := providers.roleOf(d.AddonID, d.Name); ok && ref.Slot == MemorySlot && (ref.Role == roleWrite || ref.Role == roleForget) {
			var offered bool
			if tool, offered = s.gateMemoryWrite(rec, tool, ref, writes); !offered {
				continue
			}
		}
		if d.Permission != "" && rec.Origin == session.OriginConnector {
			var ok bool
			var why string
			var connected bool
			if d.Permission == contract.CapToolsWrite && d.Credentialed {
				connected = s.agentConnectedForWrite(ctx, a.Principal, d.AddonID, conns)
			}
			if tool, ok, why = s.gateAddonTool(tool, d.Permission, a.ChatID, connected); !ok {
				held.add(d, strings.TrimSuffix(why, "."))
				continue
			}
		}
		if guarded {
			tool = s.gateAddonAct(rec, pol, tool, d)
		}
		tool.Run = s.outcomeRun(rec, a, d, tool.Run)
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
	return tools, missing, held.built(), nil
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
//
// connected says the agent selected a connection of the add-on whose write
// mode is not off. A sender who holds tools.write only by that connection (see
// impliedWrite) then passes for this tool, at the offer and at each call; the
// write mode and the approval of the connection still decide whether the call
// runs or waits for a confirmation.
func (s *Service) gateAddonTool(t session.Tool, permission, chat string, connected bool) (session.Tool, bool, string) {
	allow, audit := s.turnAllow(chat)
	if connected && permission == contract.CapToolsWrite {
		plain := allow
		allow = func(capability string) (bool, string) {
			ok, why := plain(capability)
			if ok || capability != contract.CapToolsWrite {
				return ok, why
			}
			if g, found := s.access.get(chat); found && g.ImpliedWrite {
				return true, ""
			}
			return false, why
		}
	}
	c := builtins.Chat{Allow: allow, Audit: audit}
	if held, why := c.Holds(permission); !held {
		return t, false, why
	}
	run := t.Run
	t.Run = func(ctx context.Context, args json.RawMessage) (string, error) {
		if err := c.Require(permission); err != nil {
			addontools.OutcomeFrom(ctx).Deny(s.deniedCode(chat, permission))
			return "", err
		}
		return run(ctx, args)
	}
	return t, true, ""
}

// deniedCode is the code of the link that refuses a capability to the sender of
// the turn of chat: the code of the refusal that was computed with the grant,
// or no_grant when no sender of this chat holds a grant now.
func (s *Service) deniedCode(chat, capability string) string {
	g, ok := s.access.get(chat)
	switch {
	case !ok:
		return "no_grant"
	case g.DeniedCodes[capability] != "":
		return g.DeniedCodes[capability]
	}
	return CodeConnectionPermissionOff
}

// agentConnectedForWrite says whether the agent selected a connection for the
// add-on, and at least one of them is in a write mode other than off. A
// connection that the chat or the only-connection rule picked does not count:
// the owner connected the agent to it, or did not.
func (s *Service) agentConnectedForWrite(ctx context.Context, user, addonID string, set ConnectionSet) bool {
	if s.connectors == nil || set.By != ConnectionByAgent {
		return false
	}
	return len(writeModeOn(s.connectors.agentConnections(ctx, user, addonID, set))) > 0
}

// heldTools collects, for one turn, the tools of an add-on that the chat would
// use but was refused, grouped by add-on and reason.
type heldTools struct {
	order []string
	by    map[string]*heldGroup
}

type heldGroup struct {
	addon, why string
	tools      []string
}

// maxHeldNames bounds the tool names one reason lists: the note is short.
const maxHeldNames = 3

func (h *heldTools) add(d AddonTool, why string) {
	if why == "" {
		return
	}
	if h.by == nil {
		h.by = map[string]*heldGroup{}
	}
	k := d.AddonID + "\x00" + why
	g := h.by[k]
	if g == nil {
		g = &heldGroup{addon: d.AddonID, why: why}
		h.by[k] = g
		h.order = append(h.order, k)
	}
	name := d.Tool
	if name == "" {
		name = d.Name
	}
	g.tools = append(g.tools, name)
}

// built turns the groups into the entries deniedToolsNote reads: one reason per
// add-on and cause, naming the tools it holds back.
func (h *heldTools) built() []builtins.Built {
	var out []builtins.Built
	for _, k := range h.order {
		g := h.by[k]
		names := g.tools
		more := ""
		if len(names) > maxHeldNames {
			more = fmt.Sprintf(", and %d more", len(names)-maxHeldNames)
			names = names[:maxHeldNames]
		}
		out = append(out, builtins.Built{ID: g.addon, Reason: builtins.ReasonNotPermitted + ": " + g.why +
			" " + heldMark + g.addon + " add-on: " + strings.Join(names, ", ") + more + ")"})
	}
	return out
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
			addontools.OutcomeFrom(ctx).Deny(addontools.CodeSelectConnection)
			return connectionSentence(nil, "", false), nil
		case len(set.Items) == 1 && !present:
			chosen = set.Items[0]
		default:
			var ok bool
			if chosen, ok = pickConnection(set.Items, given); !ok {
				addontools.OutcomeFrom(ctx).Deny(addontools.CodeSelectConnection)
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

// maxAgentChain bounds how far a sub-agent looks up for the agents it works
// for.
const maxAgentChain = 8

// limitOf turns the resources of an agent into the limit the add-on tools
// read.
func limitOf(r resourcesets.Sets) addontools.ResourceLimit {
	out := make(addontools.ResourceLimit, len(r))
	for addon, entries := range r {
		m := make(map[string]string, len(entries))
		for _, e := range entries {
			m[e.ID] = e.Mode
		}
		out[addon] = m
	}
	return out
}

// agentResourceLayers returns one limit for each saved agent that a chat works
// for: the agent of the chat, and for a sub-agent also the agents above it, so
// a sub-agent never reaches more than the agent that started it. A chat with
// no agent has no layer, which means no limit. An agent that cannot be read
// gives an empty layer: the chat reaches no scoped resource until it can.
// Everything is read from the stored chats and agents at the call, so a change
// of the list holds at once.
func (s *Service) agentResourceLayers(ctx context.Context, user, chatID string) ([]addontools.ResourceLimit, error) {
	var layers []addontools.ResourceLimit
	id := chatID
	for range maxAgentChain {
		rec, err := s.d.Store.GetChat(ctx, user, id)
		if errors.Is(err, session.ErrNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		if rec.AgentID != "" {
			var layer addontools.ResourceLimit
			if s.d.Agents != nil {
				if ag, aerr := s.d.Agents.Get(ctx, rec.Principal, rec.AgentID); aerr == nil {
					layer = limitOf(ag.Resources)
				} else if !errors.Is(aerr, agents.ErrNotFound) {
					s.log.Warn("studio: agent resources unavailable", "chat", rec.ID, "agent", rec.AgentID, "error", aerr)
				}
			}
			if layer == nil {
				layer = addontools.ResourceLimit{}
			}
			layers = append(layers, layer)
		}
		if rec.Kind != session.KindSubAgent || rec.ParentID == "" {
			break
		}
		id = rec.ParentID
	}
	return layers, nil
}

// agentResourceRun wraps the call of a tool of an add-on whose resources are
// scoped per agent. The host, not the model, sets the limit: it is read from
// the stored agent at each call and rides in the context, where only host code
// sets it. The add-on does not know the agent.
func (s *Service) agentResourceRun(a workspace.Actor, run func(context.Context, json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		layers, err := s.agentResourceLayers(ctx, a.Principal, a.ChatID)
		if err != nil {
			return "", err
		}
		if len(layers) > 0 {
			ctx = addontools.WithResourceLimits(ctx, addontools.ResourceLimits{Layers: layers})
		}
		return run(ctx, args)
	}
}

// ResourceSelectable says whether the chat may select the resource of an
// add-on whose resources are scoped per agent. A chat of an agent selects only
// a resource on the list of the agent; a chat with no agent, and an add-on
// that is not scoped, have no such limit. The selection a chat made earlier is
// checked again at every call, so a list that shrinks later holds too.
func (s *Service) ResourceSelectable(ctx context.Context, user, chatID, addonID, resourceID string) (bool, error) {
	if s.d.AddonTools == nil {
		return true, nil
	}
	defs, err := s.d.AddonTools.Tools(ctx, principal(user))
	if err != nil {
		return false, err
	}
	scoped := false
	for _, d := range defs {
		if d.AddonID == addonID && d.ResourceScoped {
			scoped = true
			break
		}
	}
	if !scoped {
		return true, nil
	}
	layers, err := s.agentResourceLayers(ctx, principal(user), chatID)
	if err != nil {
		return false, err
	}
	return addontools.ResourceLimits{Layers: layers}.Allows(addonID, resourceID, false), nil
}

// AgentResourceChoices is what a person may give a saved agent for one add-on:
// the resources they may use now and the add-on's word for them.
type AgentResourceChoices struct {
	Items     []ResourceChoice
	Truncated bool
	Label     *ResourceLabel
}

// AgentResourceChoices lists the resources of an add-on that scopes its
// resources per agent which the user may use now, through the listing the
// manifest declares. It is the choice for Agent.Resources. An add-on that does
// not scope its resources, or that the user cannot see, is ErrNotScoped.
func (s *Service) AgentResourceChoices(ctx context.Context, user, addonID string) (AgentResourceChoices, error) {
	chooser, ok := s.d.AddonTools.(AddonResourceChooser)
	if !ok {
		return AgentResourceChoices{}, ErrNotScoped
	}
	items, truncated, err := chooser.ResourceChoices(ctx, principal(user), addonID)
	if err != nil {
		return AgentResourceChoices{}, err
	}
	out := AgentResourceChoices{Items: items, Truncated: truncated}
	if l, ok := s.d.AddonTools.(AddonLister); ok {
		if list, err := l.AddonListings(ctx, principal(user)); err == nil {
			for _, a := range list {
				if a.AddonID == addonID {
					out.Label = a.Resources
				}
			}
		}
	}
	return out, nil
}
