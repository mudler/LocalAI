package studio

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// This file is the enforcement of the permission model of connectors (see
// contract.ConnectorCapabilityNames). One pure function, Authorize, decides
// whether a sender may use a capability. The connector manager runs it for the
// sender of each message and hands the outcome to the turn (turnGrant), where
// the tool builder offers a gated tool only for a capability the sender holds
// and the tool itself checks again when the model calls it.

// AccessSubject is what the host knows of the sender of a message. Roles are
// the IDs that the kind declares (Ingest dropped the others); nothing here is
// a name or text that the sender wrote.
type AccessSubject struct {
	SenderID string
	Roles    []string
	// Allowlisted says the message was let in: the sender is listed, or the
	// chat is (a listed group), or the binding accepts anyone.
	Allowlisted bool
	// Listed says the sender itself is on the allowlist, by ID or by handle.
	// A match through a channel or group entry, or a binding that accepts
	// anyone, does not set it: those let a whole room talk to the bot, and the
	// trust of a listed sender must not reach the rest of the room.
	Listed bool
	// ViaChannel says the message was let in because its chat is an entry of
	// the allowlist. ChannelActs says that entry has the level TrustAct (see
	// connectors.Binding.AllowTrust): every member of the chat then holds the
	// capabilities that are not sensitive, as a listed sender does. Neither is
	// set for a direct message, so a member who is not listed gets nothing from
	// the entry of a channel when they write in private.
	ViaChannel, ChannelActs bool
}

// Decision is the outcome of Authorize for one capability.
type Decision struct {
	Capability string `json:"capability"`
	Allowed    bool   `json:"allowed"`
	// Via says how the sender got the capability: "paired", "allowlist" (a
	// sender on the allowlist, for a capability that is not sensitive) or
	// "role:<id>".
	Via string `json:"via,omitempty"`
	// Reason says why the capability was refused. It is safe to show to the
	// sender and to the model.
	Reason string `json:"reason,omitempty"`
	// The fields below describe the first link of the chain that refused (see
	// connectorreasons.go). They are empty for an allowed capability.
	//
	// Code is one of the Code constants; BlockedBy is the link (server,
	// tools, agent, connection or sender); Message is one plain sentence for
	// the owner; Fix points at the one place that solves it; ThenCheck lists
	// the links after it that were not looked at.
	Code      string   `json:"code,omitempty"`
	BlockedBy string   `json:"blocked_by,omitempty"`
	Message   string   `json:"message,omitempty"`
	Fix       *Fix     `json:"fix,omitempty"`
	ThenCheck []string `json:"then_check,omitempty"`
}

// Authorize decides whether the sender may use a capability on a binding. The
// default is no. The links are checked in this order, and the first that
// fails is the answer:
//
//   - the binding must have an agent and the capability must be on the closed
//     list;
//   - off: a switch that is off now (the server, the Tools switch of the
//     agent, the grants of the agent);
//   - the capability must be in the ceiling of the binding (Caps);
//   - the sender must be allowed to talk to the bot, and then
//   - a sensitive capability needs a pairing that has not ended; neither a
//     role nor the allowlist counts;
//   - any other capability is held by a pairing, by a sender who is on the
//     allowlist themselves (Listed: their ID or handle is an entry), by a
//     chat that the owner set to let everyone in it act (ChannelActs; a
//     channel entry set to answer only does not count), or by a role of the sender that
//     the owner granted the capability (Grants) and that the kind can grant
//     (Permissions).
//
// kind is nil when the kind is not running: a role then counts for nothing.
// off is the verdict of the switches (see Connectors.offReason); the zero
// value means none is off. Authorize reads nothing else and writes nothing,
// so the whole matrix can be tested.
func Authorize(b connectors.Binding, kind *contract.ConnectorKind, s AccessSubject, capability string, now time.Time, off Block) Decision {
	deny := func(code, format string, a ...any) Decision {
		return refusal(b, capability, code, fmt.Sprintf(format, a...), nil)
	}
	switch {
	case b.CredentialsOnly():
		return deny(CodeNoAgent, "this connection has no agent: it holds credentials only")
	case !contract.KnownCapability(capability):
		d := deny(CodeUnknownCapability, "%q is not a capability", capability)
		d.ThenCheck = nil
		return d
	case off.Off():
		code := off.Code
		if code == "" {
			code = CodeServerSettingOff
		}
		return refusal(b, capability, code, off.Reason, off.Fix)
	case !slices.Contains(b.Caps, capability):
		return deny(CodeConnectionPermissionOff, "the owner has not enabled %s for this bot", capability)
	case !s.Allowlisted:
		return deny(CodeSenderNotListed, "the sender is not on the allowlist")
	}
	if s.SenderID != "" && b.IsPaired(s.SenderID, now) {
		return Decision{Capability: capability, Allowed: true, Via: "paired"}
	}
	if contract.SensitiveCapability(capability) {
		return deny(CodeSenderNeedsPairing, "%s needs a sender that the owner paired with this bot", capability)
	}
	if s.Listed && s.SenderID != "" {
		return Decision{Capability: capability, Allowed: true, Via: "allowlist"}
	}
	if s.ChannelActs && s.SenderID != "" {
		return Decision{Capability: capability, Allowed: true, Via: ViaChannel}
	}
	if kind != nil && kind.Grantable(capability) {
		for _, id := range kind.FilterRoles(s.Roles) {
			for _, g := range b.Grants {
				if g.Role == id && slices.Contains(g.Caps, capability) {
					return Decision{Capability: capability, Allowed: true, Via: "role:" + id}
				}
			}
		}
	}
	reason := fmt.Sprintf("%s needs a sender on this bot's allowlist, or one that the owner paired with this bot", capability)
	switch {
	case len(s.Roles) > 0 && kind == nil:
		return refusal(b, capability, CodeKindNotRunning, reason, nil)
	case len(s.Roles) > 0:
		return refusal(b, capability, CodeRoleNotGranted, reason, nil)
	}
	return refusal(b, capability, CodeChannelNotTrusted, untrustedReason(capability, s), nil)
}

// ViaChannel is the Via of a capability that a sender holds because the chat
// they write in is set to let everyone in it act.
const ViaChannel = "channel"

// untrustedReason is the English line of the log for a sender who is let in
// but not trusted. It names the sender and the way they were let in, and the
// two fixes, so the owner does not have to guess.
func untrustedReason(capability string, s AccessSubject) string {
	who := "the sender"
	if s.SenderID != "" {
		who = "sender " + s.SenderID
	}
	how := "because the bot accepts anyone"
	fix := "put the sender on the allowlist, or use a sender the owner paired"
	if s.ViaChannel {
		how = "through a channel set to Answer only"
		fix = "add " + strings.TrimPrefix(who, "sender ") + " to the allowed people, or set the channel to Let everyone here ask for actions"
		if s.SenderID == "" {
			fix = "add the sender to the allowed people, or set the channel to Let everyone here ask for actions"
		}
	}
	return fmt.Sprintf("%s needs a trusted sender: %s was let in only %s; %s", capability, who, how, fix)
}

// ViaAgentConnection is the Via of a write that the connection of the agent
// allows without a tick of the messaging connection (see AuthorizeConnected).
const ViaAgentConnection = "agent-connection"

// AgentConnection is a connection that the agent itself selected for an
// add-on it lists, with the write mode of that connection.
type AgentConnection struct {
	Addon, Name, WriteMode string
}

// impliedWrite says whether the sender is one for whom connecting the agent to
// a connection already means that the agent may change things with it: the
// owner or a paired sender, let in, on a binding whose ceiling has no tools.write
// that the owner did not take away. It checks no connection and no mode; the
// off switches still decide first. Roles, listed senders, anyone-mode senders,
// triggers and sub-agents never pass: they have no pairing here.
func impliedWrite(b connectors.Binding, s AccessSubject, now time.Time, off Block) bool {
	return !b.CredentialsOnly() && !off.Off() && !b.WriteTickRemoved && !slices.Contains(b.Caps, contract.CapToolsWrite) &&
		s.Allowlisted && s.SenderID != "" && b.IsPaired(s.SenderID, now)
}

// writeModeOn lists the connections whose write mode lets a call run or wait
// for a confirmation. The mode and the approval still decide which of the two.
func writeModeOn(conns []AgentConnection) []AgentConnection {
	return slices.DeleteFunc(slices.Clone(conns), func(c AgentConnection) bool { return c.WriteMode == "" || c.WriteMode == connectors.WriteOff })
}

// AuthorizeConnected is Authorize for the write tools of an add-on that the
// agent is connected to. It answers like Authorize, except that tools.write
// needs no tick in the ceiling when the sender is the owner or a paired sender
// and at least one connection the agent selected for the add-on is not in write
// mode off. conns are the connections of that add-on the agent selected (empty
// when the agent does not list the add-on or has none). The write mode and the
// approval keep deciding whether a call runs or waits: nothing here runs it.
func AuthorizeConnected(b connectors.Binding, kind *contract.ConnectorKind, s AccessSubject, now time.Time, off Block, conns []AgentConnection) Decision {
	d := Authorize(b, kind, s, contract.CapToolsWrite, now, off)
	if d.Allowed || !impliedWrite(b, s, now, off) || len(writeModeOn(conns)) == 0 {
		return d
	}
	return Decision{Capability: contract.CapToolsWrite, Allowed: true, Via: ViaAgentConnection}
}

// refusal builds the Decision of a refused capability from its code: who holds
// the link, the sentence for the owner, the fix and the links not looked at.
// reason is the English sentence of the log. fix, when nil, is the usual one
// for the code.
func refusal(b connectors.Binding, capability, code, reason string, fix *Fix) Decision {
	row := blockTable[code]
	if fix == nil {
		fix = defaultFix(b, capability, code)
	}
	return Decision{Capability: capability, Reason: reason, Code: code, BlockedBy: row.By,
		Message: blockMessage(code, capability), Fix: fix, ThenCheck: thenCheck(row.By, capability)}
}

// defaultFix is the fix of the links that Authorize itself checks. The
// switches name their own.
func defaultFix(b connectors.Binding, capability, code string) *Fix {
	switch code {
	case CodeNoAgent:
		return &Fix{Kind: FixOpenConnection, Target: "agent_id"}
	case CodeConnectionPermissionOff:
		return &Fix{Kind: FixOpenConnection, Target: "caps", Field: capability}
	case CodeSenderNotListed, CodeChannelNotTrusted:
		return &Fix{Kind: FixAddPerson, Target: "allow"}
	case CodeSenderNeedsPairing:
		return &Fix{Kind: FixPairSender, Target: "pairing"}
	case CodeRoleNotGranted:
		return &Fix{Kind: FixOpenConnection, Target: "grants", Field: capability}
	case CodeAgentUnavailable, CodeAgentPermissionOff, CodeToolsSwitchOff:
		if b.AgentID != "" {
			return &Fix{Kind: FixOpenAgent, Target: b.AgentID, Field: capability}
		}
	}
	return nil
}

// CapabilitySwitch is an extra switch in the chain of a capability: a global
// setting, the Tools switch of the agent, or the grants of the agent. It
// returns the verdict when the capability is off for the binding, or the zero
// Block when it is on. It runs for every check, so it must be cheap and must
// not block.
type CapabilitySwitch func(ctx context.Context, b connectors.Binding, capability string) Block

// AddCapabilitySwitch adds a switch. The capability is off when any gate says so.
func (c *Connectors) AddCapabilitySwitch(g CapabilitySwitch) {
	c.gmu.Lock()
	defer c.gmu.Unlock()
	c.gates = append(c.gates, g)
}

// offReason runs every switch and returns the refusal that stands first in the
// chain (server, then Tools, then agent), so the answer does not depend on the
// order in which the switches were added.
func (c *Connectors) offReason(ctx context.Context, b connectors.Binding, capability string) Block {
	c.gmu.Lock()
	gates := slices.Clone(c.gates)
	c.gmu.Unlock()
	var first Block
	for _, g := range gates {
		if why := g(ctx, b, capability); why.Off() && (!first.Off() || chainRank(why.Code) < chainRank(first.Code)) {
			first = why
		}
	}
	return first
}

// kindOf finds the running kind that serves a binding.
func (c *Connectors) kindOf(ctx context.Context, b connectors.Binding) *contract.ConnectorKind {
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return nil
	}
	if k, ok := kindFor(kinds, b); ok {
		kind := k.ConnectorKind
		return &kind
	}
	return nil
}

// subjectOf describes the sender of a message that Ingest let in. Listed is
// true only when the sender itself is an entry of the allowlist (ID or
// handle). The entry of a channel or group is matched against the chat, never
// against the sender, and AcceptAny lists nobody: neither makes a sender
// Listed.
func (c *Connectors) subjectOf(b connectors.Binding, m contract.ConnectorMessage, allowlisted bool) AccessSubject {
	channel := !m.Direct && m.Chat != "" && b.Allows(m.Chat, "")
	return AccessSubject{SenderID: m.SenderID, Roles: m.Roles, Allowlisted: allowlisted,
		Listed:     m.SenderID != "" && b.Allows(m.SenderID, m.SenderUsername),
		ViaChannel: channel, ChannelActs: channel && b.ChatTrust(m.Chat) == connectors.TrustAct}
}

// Authorize decides whether the sender of a message may use a capability, and
// records the decision. The message must come from Ingest, which kept only the
// roles that the kind declares. It is the one entry point for a feature that
// needs a sender's right: the confirmation of a proposal by phrase, the switch
// of a trigger that a message fires, a talk-back tool. check, when given, are
// the context rules of the feature (same thread, a fixed list of channels):
// each returns the reason it refuses, or "".
func (c *Connectors) Authorize(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, capability string, check ...func() string) Decision {
	d := Authorize(b, c.kindOf(ctx, b), c.subjectOf(b, m, true), capability, c.o.Clock(), c.offReason(ctx, b, capability))
	if d.Allowed {
		for _, f := range check {
			if why := f(); why != "" {
				d = refusal(b, capability, CodeContextRule, why, nil)
				break
			}
		}
	}
	c.svc.access.record(c.o.Clock(), b, []string{m.SenderID}, m.Roles, d)
	return d
}

// CeilingAllows tells whether the binding allows the capability to anyone
// now: it is in the ceiling and no switch is off. A feature that runs without
// a sender, such as the runner of a trigger, asks this to pause its work. It
// records nothing.
func (c *Connectors) CeilingAllows(ctx context.Context, b connectors.Binding, capability string) Decision {
	switch {
	case !contract.KnownCapability(capability):
		return refusal(b, capability, CodeUnknownCapability, "not a capability", nil)
	case !slices.Contains(b.Caps, capability):
		return refusal(b, capability, CodeConnectionPermissionOff, "not enabled for this bot", nil)
	}
	if off := c.offReason(ctx, b, capability); off.Off() {
		code := off.Code
		if code == "" {
			code = CodeServerSettingOff
		}
		return refusal(b, capability, code, off.Reason, off.Fix)
	}
	return Decision{Capability: capability, Allowed: true, Via: "binding"}
}

// turnGrant is what the sender of one turn may do. When several senders share
// a turn, it is the intersection.
type turnGrant struct {
	Binding, Kind string
	Senders       []string
	Roles         []string
	Caps          []string
	// Denied says why a capability of the closed list is not held, for the
	// ones that are in the ceiling of the binding. The tools use it to tell
	// the person what the owner can change.
	Denied map[string]string
	// Direct says the chat of the turn is one-to-one (a direct message, a
	// private chat) and Paired that the sender is paired with the bot. Both
	// are false when unknown, and only both together let a note be saved at
	// once (see effectiveSaveLevel). With several senders they hold only when
	// they hold for each.
	Direct, Paired bool
	// ImpliedWrite says tools.write is held by this sender for the write tools
	// of an add-on the agent is connected to, although the ceiling does not
	// list it (see impliedWrite). The tool builder still checks the connection.
	// With several senders it holds only when it holds for each.
	ImpliedWrite bool
	// Sender says how the sender was let in: SenderPaired, SenderListed,
	// SenderRolePrefix+id (shown as "role"), SenderChannelMember, SenderAnyone,
	// or senderMixed for senders that differ. DeniedCodes holds the refusal
	// code of each capability in Denied. Both feed the log of the tool calls.
	Sender      string
	DeniedCodes map[string]string
}

// senderMixed is the Sender of a turn with senders who were let in in
// different ways.
const senderMixed = "mixed"

func (g turnGrant) merge(o turnGrant) turnGrant {
	out := turnGrant{Binding: g.Binding, Kind: g.Kind, Direct: g.Direct && o.Direct, Paired: g.Paired && o.Paired, ImpliedWrite: g.ImpliedWrite && o.ImpliedWrite, Sender: g.Sender}
	if g.Sender != o.Sender {
		out.Sender = senderMixed
	}
	if len(g.DeniedCodes)+len(o.DeniedCodes) > 0 {
		out.DeniedCodes = maps.Clone(g.DeniedCodes)
		if out.DeniedCodes == nil {
			out.DeniedCodes = map[string]string{}
		}
		for k, v := range o.DeniedCodes {
			if _, ok := out.DeniedCodes[k]; !ok {
				out.DeniedCodes[k] = v
			}
		}
	}
	out.Senders = unionOf(g.Senders, o.Senders)
	out.Roles = unionOf(g.Roles, o.Roles)
	if len(g.Denied)+len(o.Denied) > 0 {
		out.Denied = maps.Clone(g.Denied)
		if out.Denied == nil {
			out.Denied = map[string]string{}
		}
		for k, v := range o.Denied {
			if _, ok := out.Denied[k]; !ok {
				out.Denied[k] = v
			}
		}
	}
	for _, c := range g.Caps {
		if slices.Contains(o.Caps, c) {
			out.Caps = append(out.Caps, c)
		}
	}
	return out
}

func unionOf(a, b []string) []string {
	out := slices.Clone(a)
	for _, s := range b {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func grantOf(w *workerctl.ChatGrant) turnGrant {
	return turnGrant{Binding: w.Binding, Kind: w.Kind, Senders: w.Senders, Roles: w.Roles, Caps: w.Caps, Denied: w.Denied, Direct: w.Direct, Paired: w.Paired, ImpliedWrite: w.ImpliedWrite,
		Sender: w.Sender, DeniedCodes: w.DeniedCodes}
}

func (g turnGrant) wire() *workerctl.ChatGrant {
	return &workerctl.ChatGrant{Binding: g.Binding, Kind: g.Kind, Senders: g.Senders, Roles: g.Roles, Caps: g.Caps, Denied: g.Denied, Direct: g.Direct, Paired: g.Paired, ImpliedWrite: g.ImpliedWrite,
		Sender: g.Sender, DeniedCodes: g.DeniedCodes}
}

// grantSenderKind says how the sender of a message was let in, in the words of the
// SenderXxx constants: a paired sender, one on the allowlist by name, one with
// a role the kind declares, a bot that accepts anyone, or a member of a listed
// channel.
func grantSenderKind(b connectors.Binding, s AccessSubject, paired bool, kind *contract.ConnectorKind) string {
	switch {
	case paired:
		return SenderPaired
	case s.Listed && s.SenderID != "":
		return SenderListed
	case kind != nil && len(kind.FilterRoles(s.Roles)) > 0:
		return "role"
	case b.AcceptAny:
		return SenderAnyone
	}
	return "channel"
}

// grantFor computes what the sender of a message may do, for the whole closed
// list. It records nothing: the tools record a decision when they use it.
func (c *Connectors) grantFor(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage) turnGrant {
	kind := c.kindOf(ctx, b)
	now := c.o.Clock()
	g := turnGrant{Binding: b.ID, Kind: b.Kind, Senders: []string{m.SenderID}, Roles: slices.Clone(m.Roles),
		Direct: m.Direct, Paired: m.SenderID != "" && b.IsPaired(m.SenderID, now)}
	g.ImpliedWrite = impliedWrite(b, c.subjectOf(b, m, true), now, c.offReason(ctx, b, contract.CapToolsWrite))
	g.Sender = grantSenderKind(b, c.subjectOf(b, m, true), g.Paired, kind)
	for _, name := range contract.ConnectorCapabilityNames() {
		d := Authorize(b, kind, c.subjectOf(b, m, true), name, now, c.offReason(ctx, b, name))
		switch {
		case d.Allowed:
			g.Caps = append(g.Caps, name)
		case d.Reason != "":
			if g.Denied == nil {
				g.Denied = map[string]string{}
			}
			// The model and the person read this, so it is the plain
			// sentence of the code, not the English line of the log.
			g.Denied[name] = d.Say()
			if d.Code != "" {
				if g.DeniedCodes == nil {
					g.DeniedCodes = map[string]string{}
				}
				g.DeniedCodes[name] = d.Code
			}
		}
	}
	return g
}

type grantCtxKey struct{}

// withTurnGrant carries the grant of a connector turn to SendBackground, which
// sets it where the turn runs.
func withTurnGrant(ctx context.Context, g turnGrant) context.Context {
	return context.WithValue(ctx, grantCtxKey{}, g)
}

func turnGrantFrom(ctx context.Context) (turnGrant, bool) {
	g, ok := ctx.Value(grantCtxKey{}).(turnGrant)
	return g, ok
}

// Limits of the access state.
const (
	// grantTTL bounds how long a process keeps the grant of a chat whose
	// turn it did not see end (a chat worker). The connector manager ends a
	// grant when its reply ends.
	grantTTL = 2 * time.Hour
	// AccessLogSize is how many recent decisions a process keeps for the
	// settings page of a binding.
	AccessLogSize = 200
	// ToolOutcomesPerBinding is how many tool outcomes a process keeps for a
	// binding, and ToolOutcomeLogSize in all.
	ToolOutcomesPerBinding = 20
	ToolOutcomeLogSize     = 400
)

// AccessEvent is one audited decision. It holds IDs and capability names,
// never message text, pairing codes or tokens.
type AccessEvent struct {
	At         time.Time `json:"at"`
	Binding    string    `json:"binding"`
	Kind       string    `json:"kind,omitempty"`
	Senders    []string  `json:"senders,omitempty"`
	Roles      []string  `json:"roles,omitempty"`
	Capability string    `json:"capability"`
	Allowed    bool      `json:"allowed"`
	Via        string    `json:"via,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	// Code is the code of the refusal (see connectorreasons.go).
	Code string `json:"code,omitempty"`
	// The fields below are set by a message the host wrote on its own (see
	// Connectors.Send). They hold where, how long and a hash of the text, and
	// never the text.
	Action  string `json:"action,omitempty"`
	Channel string `json:"channel,omitempty"`
	Thread  string `json:"thread,omitempty"`
	Length  int    `json:"length,omitempty"`
	Hash    string `json:"hash,omitempty"`
	// The fields below are set by the outcome of a tool call of an add-on (see
	// recordTool). They hold names and codes, never arguments or results. The
	// Capability of such an event is empty.
	Addon string `json:"addon,omitempty"`
	Tool  string `json:"tool,omitempty"`
	// Outcome is ok, denied, held or error; Reason is the code of the cause (see
	// connectortext.ToolReason) and Status the status of the service behind the
	// add-on, when it answered one.
	Outcome string `json:"outcome,omitempty"`
	Status  int    `json:"status,omitempty"`
}

type heldGrant struct {
	turnGrant
	at time.Time
}

// accessState holds the grants of the running connector turns of this process
// and the recent decisions. The zero value is ready.
type accessState struct {
	mu     sync.Mutex
	grants map[string]heldGrant // by chat
	log    []AccessEvent
	// tools holds the latest outcomes of the tool calls of add-ons, at most
	// ToolOutcomesPerBinding for each binding and ToolOutcomeLogSize in all.
	tools []AccessEvent
}

// The grant lifetime is wall time, not the clock of the connector manager, so
// a frontend and a worker agree and a test clock cannot expire a grant.
func (a *accessState) set(chat string, g turnGrant) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.grants == nil {
		a.grants = map[string]heldGrant{}
	}
	for id, h := range a.grants {
		if now.Sub(h.at) > grantTTL {
			delete(a.grants, id)
		}
	}
	a.grants[chat] = heldGrant{turnGrant: g, at: now}
}

func (a *accessState) clear(chat string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.grants, chat)
}

func (a *accessState) get(chat string) (turnGrant, bool) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	h, ok := a.grants[chat]
	if !ok || now.Sub(h.at) > grantTTL {
		return turnGrant{}, false
	}
	return h.turnGrant, true
}

func (a *accessState) record(now time.Time, b connectors.Binding, senders, roles []string, d Decision) {
	e := AccessEvent{At: now.UTC(), Binding: b.ID, Kind: b.Kind, Senders: senders, Roles: roles,
		Capability: d.Capability, Allowed: d.Allowed, Via: d.Via, Reason: d.Reason, Code: d.Code}
	a.append(e)
}

func (a *accessState) append(e AccessEvent) {
	slog.Info("studio: connector access", "binding", e.Binding, "kind", e.Kind, "senders", e.Senders, "roles", e.Roles,
		"capability", e.Capability, "allowed", e.Allowed, "via", e.Via, "code", e.Code, "reason", e.Reason)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.log = append(a.log, e)
	if len(a.log) > AccessLogSize {
		a.log = slices.Clone(a.log[len(a.log)-AccessLogSize:])
	}
}

// recordTool keeps the outcome of a tool call for the settings page of the
// binding it names. The oldest events of that binding go first.
func (a *accessState) recordTool(e AccessEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools = append(a.tools, e)
	n := 0
	for i := len(a.tools) - 1; i >= 0; i-- {
		if a.tools[i].Binding != e.Binding {
			continue
		}
		if n++; n > ToolOutcomesPerBinding {
			a.tools = slices.Delete(a.tools, i, i+1)
		}
	}
	if len(a.tools) > ToolOutcomeLogSize {
		a.tools = slices.Clone(a.tools[len(a.tools)-ToolOutcomeLogSize:])
	}
}

// recentTools returns the newest tool outcomes of a binding, newest first.
func (a *accessState) recentTools(binding string, limit int) []AccessEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []AccessEvent
	for i := len(a.tools) - 1; i >= 0 && len(out) < limit; i-- {
		if a.tools[i].Binding == binding {
			out = append(out, a.tools[i])
		}
	}
	return out
}

// recent returns the newest decisions of a binding, newest first.
func (a *accessState) recent(binding string, limit int) []AccessEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []AccessEvent
	for i := len(a.log) - 1; i >= 0 && len(out) < limit; i-- {
		if a.log[i].Binding == binding {
			out = append(out, a.log[i])
		}
	}
	return out
}

// turnAllow is the check that the tools of a connector chat use. A chat with
// no grant (no sender started the turn: a loop, an owner typing in Studio, a
// grant that expired) holds nothing.
func (s *Service) turnAllow(chat string) (allow func(string) (bool, string), audit func(string, bool, string)) {
	allow = func(capability string) (bool, string) {
		g, ok := s.access.get(chat)
		switch {
		case !ok:
			return false, "no sender of this conversation is authorized for " + capability
		case slices.Contains(g.Caps, capability):
			return true, ""
		}
		if why := g.Denied[capability]; why != "" {
			return false, why
		}
		return false, BlockSay(CodeConnectionPermissionOff, capability)
	}
	audit = func(capability string, ok bool, reason string) {
		g, _ := s.access.get(chat)
		d := Decision{Capability: capability, Allowed: ok, Reason: reason}
		if ok {
			d.Via = "turn"
		}
		s.access.record(s.now(), connectors.Binding{ID: g.Binding, Kind: g.Kind}, g.Senders, g.Roles, d)
	}
	return allow, audit
}

// messagingFacts says whether the chat of the running turn is one-to-one and
// its sender paired. Without a grant (no turn of a sender runs) both are false:
// an unknown chat counts as a group and an unknown sender as unpaired.
func (s *Service) messagingFacts(chat string) (direct, paired bool) {
	g, ok := s.access.get(chat)
	if !ok {
		return false, false
	}
	return g.Direct, g.Paired
}

// NotesReadCapability names the decision that says whether a sender's turn
// reads the owner's personal notes. It is not on the closed list of
// capabilities: the owner cannot tick it, and no grant gives it. A paired
// sender holds it and nobody else (see Service.personalNotesOpen); the
// notes of the conversation itself are read whatever the answer.
const NotesReadCapability = "notes.read"

// notesDecision is the verdict of the read rule for a kind of sender, on the
// same terms as the turn applies it.
func notesDecision(b connectors.Binding, s AccessSubject, now time.Time) Decision {
	if b.CredentialsOnly() {
		return refusal(b, NotesReadCapability, CodeNoAgent, "this connection has no agent: it holds credentials only", nil)
	}
	if s.SenderID != "" && b.IsPaired(s.SenderID, now) {
		return Decision{Capability: NotesReadCapability, Allowed: true, Via: "paired"}
	}
	return refusal(b, NotesReadCapability, CodeSenderNeedsPairing,
		"the personal notes of the owner are read only for a sender that the owner paired with this bot", nil)
}

// AccessSubjectView is what one kind of sender may do on a binding.
type AccessSubjectView struct {
	// ID is "listed" (a sender on the allowlist by ID or name), "anyone" (a
	// sender let in by a listed channel, with no role), "paired", or
	// "role:<id>".
	ID    string `json:"id"`
	Title string `json:"title"`
	// Decisions has one entry for each capability of the closed list, then
	// the one of NotesReadCapability.
	Decisions []Decision `json:"decisions"`
	// NoteSave is set only when the agent of the binding has the rule Save it
	// and tell me (auto): "now" when a note from this kind of sender, in a
	// one-to-one chat, is saved at once with an Undo, and "ask" when it waits
	// for the owner. It is empty for every other agent rule.
	NoteSave string `json:"note_save,omitempty" enums:"now,ask"`
}

// noteSaveOf says how a note from a kind of sender is saved on the binding, but
// only for an agent whose rule is auto (see AccessSubjectView.NoteSave). It
// asks effectiveSaveLevel for a one-to-one chat; a sender that is not paired
// never gets "now".
func (s *Service) noteSaveOf(ctx context.Context, b connectors.Binding, paired bool) string {
	if b.AgentID == "" || s.changes == nil || s.agentMemorySave(ctx, b.Owner, b.AgentID) != agents.MemorySaveAuto {
		return ""
	}
	level := effectiveSaveLevel(SaveInputs{User: s.changes.storedSaveMode(ctx, b.Owner), Agent: agents.MemorySaveAuto,
		Messaging: s.changes.storedMessagingSave(ctx, b.Owner), Origin: session.OriginConnector, Direct: true, Paired: paired})
	if level == SaveAuto {
		return "now"
	}
	return "ask"
}

// AccessView is the effective permissions of a binding, for the settings page
// of the add-on that serves it, with the recent decisions of this process.
type AccessView struct {
	Binding  string              `json:"binding"`
	Subjects []AccessSubjectView `json:"subjects"`
	Recent   []AccessEvent       `json:"recent"`
	// ToolOutcomes are the latest outcomes of add-on tool calls that used this
	// binding, newest first, at most ToolOutcomesPerBinding.
	ToolOutcomes []AccessEvent `json:"tool_outcomes,omitempty"`
	// Approvals lists the tools of the binding's add-on whose confirmation the
	// owner can relax, and says for whom each one runs without asking now.
	Approvals []ToolApproval `json:"approvals,omitempty"`
	// Preview is true when the view describes a draft that is not saved.
	Preview bool `json:"preview,omitempty"`
	// AddonTools lists the tools of the add-ons the binding's agent uses that a
	// permission of this binding decides, so the owner sees which tools a
	// missing tick holds back. The decision itself is in Subjects, under the
	// capability.
	AddonTools []AddonToolHold `json:"addon_tools,omitempty"`
}

// AddonToolHold names the tools of one add-on that need one capability of the
// binding in a messaging chat (see AddonTool.Permission).
type AddonToolHold struct {
	Addon      string   `json:"addon"`
	Capability string   `json:"capability"`
	Tools      []string `json:"tools"`
	// Connections are the connections of the add-on that the agent selected,
	// with their write modes, for the capability tools.write. Empty for
	// other capabilities and when the agent selected none.
	Connections []AddonToolConnection `json:"connections,omitempty"`
	// WithoutTick lists the senders who use these tools although the
	// connection does not list the capability, because the agent is connected
	// ("paired"). It is empty when the tick is set, when the owner took it away
	// (TickRemoved), when every selected connection is in write mode off, and
	// while a switch is off.
	WithoutTick []string `json:"without_tick,omitempty"`
	// TickRemoved says the owner took the tick away, so no sender is covered
	// without it.
	TickRemoved bool `json:"tick_removed,omitempty"`
}

// AddonToolConnection is a connection the agent selected for an add-on.
type AddonToolConnection struct {
	Name      string `json:"name"`
	WriteMode string `json:"write_mode"`
}

// addonToolHolds lists the tools of the add-ons that the agent of b names and
// that a messaging chat holds to a capability. A doubt (no broker, an agent
// that cannot be read) lists none.
func (c *Connectors) addonToolHolds(ctx context.Context, b connectors.Binding) []AddonToolHold {
	holds, _ := c.addonToolHoldsWith(ctx, b)
	return holds
}

// addonToolHoldsWith is addonToolHolds that also returns the connections of all
// the add-ons whose write tools a paired sender uses without the tick.
func (c *Connectors) addonToolHoldsWith(ctx context.Context, b connectors.Binding) ([]AddonToolHold, []AgentConnection) {
	if b.AgentID == "" || c.svc.d.AddonTools == nil || c.svc.d.Agents == nil {
		return nil, nil
	}
	ag, err := c.svc.d.Agents.Get(ctx, principal(b.Owner), b.AgentID)
	if err != nil {
		return nil, nil
	}
	tools, err := c.svc.d.AddonTools.Tools(ctx, principal(b.Owner))
	if err != nil {
		return nil, nil
	}
	var out []AddonToolHold
	for _, t := range tools {
		if t.Permission == "" || !slices.Contains(ag.Addons, t.AddonID) {
			continue
		}
		name := t.Tool
		if name == "" {
			name = t.Name
		}
		i := slices.IndexFunc(out, func(h AddonToolHold) bool { return h.Addon == t.AddonID && h.Capability == t.Permission })
		if i < 0 {
			out = append(out, AddonToolHold{Addon: t.AddonID, Capability: t.Permission})
			i = len(out) - 1
		}
		out[i].Tools = append(out[i].Tools, name)
	}
	var covered []AgentConnection
	for i := range out {
		slices.Sort(out[i].Tools)
		h := &out[i]
		if h.Capability != contract.CapToolsWrite {
			continue
		}
		conns := c.agentConnectionsOf(ctx, b, ag, h.Addon)
		for _, x := range conns {
			h.Connections = append(h.Connections, AddonToolConnection{Name: x.Name, WriteMode: x.WriteMode})
		}
		h.TickRemoved = b.WriteTickRemoved && !slices.Contains(b.Caps, h.Capability)
		sender := AccessSubject{SenderID: "paired", Allowlisted: true}
		paired := b
		paired.Paired = []connectors.PairedSender{{SenderID: "paired", At: c.o.Clock()}}
		if impliedWrite(paired, sender, c.o.Clock(), c.offReason(ctx, b, h.Capability)) && len(writeModeOn(conns)) > 0 {
			h.WithoutTick = []string{SenderPaired}
			covered = append(covered, conns...)
		}
	}
	slices.SortFunc(out, func(x, y AddonToolHold) int {
		return strings.Compare(x.Addon+"\x00"+x.Capability, y.Addon+"\x00"+y.Capability)
	})
	return out, covered
}

// agentConnectionsOf lists the connections the agent ag selected for the
// add-on, with their write modes (the second rule of ConnectionsOf). An agent
// with no selection gives none: the chat does not fill the gap.
func (c *Connectors) agentConnectionsOf(ctx context.Context, b connectors.Binding, ag agents.Agent, addonID string) []AgentConnection {
	ids := ag.Connections[addonID]
	if len(ids) == 0 {
		return nil
	}
	list, err := c.Connections(ctx, b.Owner, addonID)
	if err != nil {
		return nil
	}
	var set ConnectionSet
	for _, x := range list {
		if slices.Contains(ids, x.ID) {
			set.Items = append(set.Items, x)
		}
	}
	return c.agentConnections(ctx, b.Owner, addonID, set)
}

// ToolApproval is the confirmation of one tool that the owner may relax (see
// contract.ToolDefinition.ApprovalOverridable).
type ToolApproval struct {
	// Tool is the name of the tool inside the add-on.
	Tool string `json:"tool"`
	// Skip is the owner's setting: true when the owner chose not to be asked.
	Skip bool `json:"skip"`
	// RunsWithoutAsking says who the tool runs for without a confirmation right
	// now: "owner" (the owner's own chats) and "paired" (a sender paired with
	// the bot in a messaging chat). It is empty when the setting is on ask, or
	// when the write mode of the binding is off. Every other sender, a listed
	// one, one let in because the bot accepts anyone, and a role, is still
	// asked, and so is a trigger, a job and a sub-agent.
	RunsWithoutAsking []string `json:"runs_without_asking,omitempty"`
}

// Access explains what each kind of sender may do on a binding, by running
// Authorize for a sender with no role, a paired sender and a sender of each
// role the kind declares. Nothing in it is a guess: it is the same function
// the turns use.
func (c *Connectors) Access(ctx context.Context, user, id string) (AccessView, error) {
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return AccessView{}, err
	}
	out := c.accessOf(ctx, b, "")
	out.Recent = c.svc.access.recent(b.ID, 50)
	out.ToolOutcomes = c.svc.access.recentTools(b.ID, ToolOutcomesPerBinding)
	return out, nil
}

// The kinds of sender that a preview can ask for. SenderChannelMember and
// SenderAnyone both mean a sender let in without being on the list by name;
// the answer lists it as "anyone".
const (
	SenderListed        = "listed"
	SenderPaired        = "paired"
	SenderChannelMember = "channel_member"
	SenderAnyone        = "anyone"
	// SenderChannelActs is a sender let in through a channel that the owner set
	// to Let everyone here ask for actions.
	SenderChannelActs = "channel_acts"
	SenderRolePrefix  = "role:"
)

// AccessDraft is the state of a connection as the form holds it, before it is
// saved. A field that is nil (or an empty Kind) keeps what is saved. Only the
// fields that the permission chain reads are here. There is no token: a
// preview never takes one, and never returns one.
type AccessDraft struct {
	// Kind is read only for a connection that is not saved yet (the id "new").
	Kind      string
	AgentID   *string
	AcceptAny *bool
	Allow     []string
	// AllowTrust is the trust level of the chat entries of Allow. Nil keeps the
	// saved levels.
	AllowTrust map[string]string
	Caps       []string
	Grants     []connectors.RoleGrant
	// Sender, when set, limits the answer to one kind of sender: "listed",
	// "paired", "channel_member", "anyone" or "role:<id>".
	Sender string
}

// ErrBadSender means a preview asked for a kind of sender that does not exist.
var ErrBadSender = fmt.Errorf("%w: sender must be listed, paired, channel_member, channel_acts, anyone or role:<id>", connectors.ErrInvalid)

// NewConnectionID is the id under which a preview describes a connection that
// is not saved yet.
const NewConnectionID = "new"

// AccessPreview answers like Access for the state in the draft, which is not
// saved. It writes nothing, records no decision and returns nothing from the
// token: the binding's stored secrets are not read. The chain is the one the
// turns use, run on the draft.
func (c *Connectors) AccessPreview(ctx context.Context, user, id string, d AccessDraft) (AccessView, error) {
	var b connectors.Binding
	if id == NewConnectionID {
		if d.Kind == "" {
			return AccessView{}, connectorBad("a connection that is not saved needs a kind")
		}
		addon, err := c.resolveAddon(ctx, d.Kind)
		if err != nil {
			return AccessView{}, err
		}
		b = connectors.Binding{ID: NewConnectionID, Owner: principal(user), Kind: d.Kind, AddonID: addon}
	} else {
		var err error
		if b, err = c.owned(ctx, user, id); err != nil {
			return AccessView{}, err
		}
	}
	b.Token = nil // the preview never needs it, so it never holds it
	if d.AgentID != nil {
		b.AgentID = *d.AgentID
	}
	if d.AcceptAny != nil {
		b.AcceptAny = *d.AcceptAny
	}
	if d.Allow != nil {
		b.Allow = slices.Clone(d.Allow)
	}
	if d.AllowTrust != nil {
		b.AllowTrust = maps.Clone(d.AllowTrust)
	}
	for k := range b.AllowTrust {
		if !slices.Contains(b.Allow, k) {
			delete(b.AllowTrust, k)
		}
	}
	if d.Caps != nil {
		b.WriteTickRemoved = writeTickRemoved(b.Caps, d.Caps, b.WriteTickRemoved)
		b.Caps = slices.Clone(d.Caps)
	}
	if d.Grants != nil {
		b.Grants = slices.Clone(d.Grants)
	}
	switch s := d.Sender; {
	case s == "", s == SenderListed, s == SenderPaired, s == SenderChannelMember, s == SenderAnyone, s == SenderChannelActs:
	case strings.HasPrefix(s, SenderRolePrefix):
		kind := c.kindOf(ctx, b)
		if kind == nil || !slices.ContainsFunc(kind.Roles, func(r contract.ConnectorRole) bool { return SenderRolePrefix+r.ID == s }) {
			return AccessView{}, ErrBadSender
		}
	default:
		return AccessView{}, ErrBadSender
	}
	out := c.accessOf(ctx, b, d.Sender)
	out.Preview = true
	return out, nil
}

// accessOf runs the chain for each kind of sender of a binding, or for the one
// kind asked for. It is the one computation behind the saved view and the
// preview.
func (c *Connectors) accessOf(ctx context.Context, b connectors.Binding, only string) AccessView {
	kind := c.kindOf(ctx, b)
	now := c.o.Clock()
	holds, covered := c.addonToolHoldsWith(ctx, b)
	view := func(id, title string, bind connectors.Binding, s AccessSubject) AccessSubjectView {
		v := AccessSubjectView{ID: id, Title: title}
		for _, name := range contract.ConnectorCapabilityNames() {
			if name == contract.CapToolsWrite {
				// The write tools of the add-ons the agent is connected to
				// need no tick for the owner and paired senders.
				v.Decisions = append(v.Decisions, AuthorizeConnected(bind, kind, s, now, c.offReason(ctx, b, name), covered))
				continue
			}
			v.Decisions = append(v.Decisions, Authorize(bind, kind, s, name, now, c.offReason(ctx, b, name)))
		}
		v.Decisions = append(v.Decisions, notesDecision(bind, s, now))
		v.NoteSave = c.svc.noteSaveOf(ctx, b, bind.IsPaired(s.SenderID, now))
		return v
	}
	want := func(id string) bool {
		if only == SenderChannelMember {
			only = SenderAnyone
		}
		return only == "" || only == id
	}
	out := AccessView{Binding: b.ID, Approvals: c.svc.approvalsOf(ctx, b), AddonTools: holds}
	if want("listed") {
		out.Subjects = append(out.Subjects, view("listed", "A sender on the allowlist", b, AccessSubject{SenderID: "x", Allowlisted: true, Listed: true}))
	}
	if want("anyone") {
		out.Subjects = append(out.Subjects, view("anyone", "A sender let in through a listed channel", b, AccessSubject{SenderID: "x", Allowlisted: true, ViaChannel: true}))
	}
	if want("channel_acts") {
		out.Subjects = append(out.Subjects, view("channel_acts", "A sender in a channel set to let everyone act", b, AccessSubject{SenderID: "x", Allowlisted: true, ViaChannel: true, ChannelActs: true}))
	}
	if want("paired") {
		paired := b
		paired.Paired = []connectors.PairedSender{{SenderID: "paired", At: now}}
		out.Subjects = append(out.Subjects, view("paired", "A paired sender", paired, AccessSubject{SenderID: "paired", Allowlisted: true}))
	}
	if kind != nil {
		for _, r := range kind.Roles {
			if want("role:" + r.ID) {
				out.Subjects = append(out.Subjects, view("role:"+r.ID, r.Title, b, AccessSubject{SenderID: "x", Allowlisted: true, Roles: []string{r.ID}}))
			}
		}
	}
	return out
}

// CapabilityGate returns the gate that answers the questions of the features
// that act from a connector chat (see Service.SetCapabilityGate) with
// Authorize. A question without a sender, such as the tools of a turn or a run
// that is due, gets the binding's answer: the capability is in its ceiling and
// no switch is off. A question with a sender gets the full check for that
// sender. A request marked Sensitive ignores roles, so only a paired sender
// passes. Any doubt (unknown binding, other owner) is a no.
func (c *Connectors) CapabilityGate() CapabilityGate {
	return CapabilityGateFunc(func(ctx context.Context, r CapabilityRequest) bool {
		b, err := c.o.Store.GetBinding(ctx, r.Binding)
		if err != nil || (r.Owner != "" && b.Owner != r.Owner) {
			return false
		}
		if r.Sender == "" {
			return c.CeilingAllows(ctx, b, r.Capability).Allowed
		}
		roles := r.Roles
		listed := b.Allows(r.Sender, r.Username)
		channel := !r.Direct && r.Chat != "" && b.Allows(r.Chat, "")
		acts := channel && b.ChatTrust(r.Chat) == connectors.TrustAct
		if r.Sensitive {
			roles, listed, acts = nil, false, false
		}
		d := Authorize(b, c.kindOf(ctx, b), AccessSubject{SenderID: r.Sender, Roles: roles, Allowlisted: true, Listed: listed, ViaChannel: channel, ChannelActs: acts},
			r.Capability, c.o.Clock(), c.offReason(ctx, b, r.Capability))
		c.svc.access.record(c.o.Clock(), b, []string{r.Sender}, roles, d)
		return d.Allowed
	})
}
