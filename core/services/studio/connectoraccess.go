package studio

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
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
	SenderID    string
	Roles       []string
	Allowlisted bool
}

// Decision is the outcome of Authorize for one capability.
type Decision struct {
	Capability string `json:"capability"`
	Allowed    bool   `json:"allowed"`
	// Via says how the sender got the capability: "paired" or "role:<id>".
	Via string `json:"via,omitempty"`
	// Reason says why the capability was refused. It is safe to show to the
	// sender and to the model.
	Reason string `json:"reason,omitempty"`
}

// Authorize decides whether the sender may use a capability on a binding. The
// default is no. The capability must be in the closed list and in the ceiling
// of the binding (Caps), the sender must be on the allowlist, and then:
//
//   - a sensitive capability needs a pairing that has not ended; a role never
//     counts;
//   - any other capability needs a pairing, or a role of the sender that the
//     owner granted the capability (Grants) and that the kind can grant
//     (Permissions).
//
// kind is nil when the kind is not running: a role then counts for nothing.
// off is the reason of a switch that is off now (a global setting or the
// grants of the agent), or empty. Authorize reads nothing else and writes
// nothing, so the whole matrix can be tested.
func Authorize(b connectors.Binding, kind *contract.ConnectorKind, s AccessSubject, capability string, now time.Time, off string) Decision {
	deny := func(format string, a ...any) Decision {
		return Decision{Capability: capability, Reason: fmt.Sprintf(format, a...)}
	}
	switch {
	case b.CredentialsOnly():
		return deny("this connection has no agent: it holds credentials only")
	case !contract.KnownCapability(capability):
		return deny("%q is not a capability", capability)
	case !s.Allowlisted:
		return deny("the sender is not on the allowlist")
	case !slices.Contains(b.Caps, capability):
		return deny("the owner has not enabled %s for this bot", capability)
	case off != "":
		return deny("%s", off)
	}
	if s.SenderID != "" && b.IsPaired(s.SenderID, now) {
		return Decision{Capability: capability, Allowed: true, Via: "paired"}
	}
	if contract.SensitiveCapability(capability) {
		return deny("%s needs a sender that the owner paired with this bot", capability)
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
	return deny("%s is not granted to this sender", capability)
}

// CapabilitySwitch is an extra switch in the chain of a capability: a global
// setting, or the grants of the agent. It returns the reason when the
// capability is off for the binding, or "" when it is on. It runs for every
// check, so it must be cheap and must not block.
type CapabilitySwitch func(ctx context.Context, b connectors.Binding, capability string) string

// AddCapabilitySwitch adds a switch. The capability is off when any gate says so.
func (c *Connectors) AddCapabilitySwitch(g CapabilitySwitch) {
	c.gmu.Lock()
	defer c.gmu.Unlock()
	c.gates = append(c.gates, g)
}

func (c *Connectors) offReason(ctx context.Context, b connectors.Binding, capability string) string {
	c.gmu.Lock()
	gates := slices.Clone(c.gates)
	c.gmu.Unlock()
	for _, g := range gates {
		if why := g(ctx, b, capability); why != "" {
			return why
		}
	}
	return ""
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

func (c *Connectors) subjectOf(b connectors.Binding, m contract.ConnectorMessage, allowlisted bool) AccessSubject {
	return AccessSubject{SenderID: m.SenderID, Roles: m.Roles, Allowlisted: allowlisted}
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
				d = Decision{Capability: capability, Reason: why}
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
		return Decision{Capability: capability, Reason: "not a capability"}
	case !slices.Contains(b.Caps, capability):
		return Decision{Capability: capability, Reason: "not enabled for this bot"}
	}
	if why := c.offReason(ctx, b, capability); why != "" {
		return Decision{Capability: capability, Reason: why}
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
}

func (g turnGrant) merge(o turnGrant) turnGrant {
	out := turnGrant{Binding: g.Binding, Kind: g.Kind}
	out.Senders = unionOf(g.Senders, o.Senders)
	out.Roles = unionOf(g.Roles, o.Roles)
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
	return turnGrant{Binding: w.Binding, Kind: w.Kind, Senders: w.Senders, Roles: w.Roles, Caps: w.Caps}
}

func (g turnGrant) wire() *workerctl.ChatGrant {
	return &workerctl.ChatGrant{Binding: g.Binding, Kind: g.Kind, Senders: g.Senders, Roles: g.Roles, Caps: g.Caps}
}

// grantFor computes what the sender of a message may do, for the whole closed
// list. It records nothing: the tools record a decision when they use it.
func (c *Connectors) grantFor(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage) turnGrant {
	kind := c.kindOf(ctx, b)
	g := turnGrant{Binding: b.ID, Kind: b.Kind, Senders: []string{m.SenderID}, Roles: slices.Clone(m.Roles)}
	now := c.o.Clock()
	for _, name := range contract.ConnectorCapabilityNames() {
		if Authorize(b, kind, c.subjectOf(b, m, true), name, now, c.offReason(ctx, b, name)).Allowed {
			g.Caps = append(g.Caps, name)
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
	// The fields below are set by a message the host wrote on its own (see
	// Connectors.Send). They hold where, how long and a hash of the text, and
	// never the text.
	Action  string `json:"action,omitempty"`
	Channel string `json:"channel,omitempty"`
	Thread  string `json:"thread,omitempty"`
	Length  int    `json:"length,omitempty"`
	Hash    string `json:"hash,omitempty"`
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
		Capability: d.Capability, Allowed: d.Allowed, Via: d.Via, Reason: d.Reason}
	a.append(e)
}

func (a *accessState) append(e AccessEvent) {
	slog.Info("studio: connector access", "binding", e.Binding, "kind", e.Kind, "senders", e.Senders, "roles", e.Roles,
		"capability", e.Capability, "allowed", e.Allowed, "via", e.Via)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.log = append(a.log, e)
	if len(a.log) > AccessLogSize {
		a.log = slices.Clone(a.log[len(a.log)-AccessLogSize:])
	}
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
		return false, "the owner has not allowed " + capability + " for the sender of this message"
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

// AccessSubjectView is what one kind of sender may do on a binding.
type AccessSubjectView struct {
	// ID is "anyone" (an allowlisted sender with no role), "paired", or
	// "role:<id>".
	ID    string `json:"id"`
	Title string `json:"title"`
	// Decisions has one entry for each capability of the closed list.
	Decisions []Decision `json:"decisions"`
}

// AccessView is the effective permissions of a binding, for the settings page
// of the add-on that serves it, with the recent decisions of this process.
type AccessView struct {
	Binding  string              `json:"binding"`
	Subjects []AccessSubjectView `json:"subjects"`
	Recent   []AccessEvent       `json:"recent"`
}

// Access explains what each kind of sender may do on a binding of the user,
// by running Authorize for a sender with no role, a paired sender and a sender
// of each role the kind declares. Nothing in it is a guess: it is the same
// function the turns use.
func (c *Connectors) Access(ctx context.Context, user, id string) (AccessView, error) {
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return AccessView{}, err
	}
	kind := c.kindOf(ctx, b)
	now := c.o.Clock()
	view := func(id, title string, bind connectors.Binding, s AccessSubject) AccessSubjectView {
		v := AccessSubjectView{ID: id, Title: title}
		for _, name := range contract.ConnectorCapabilityNames() {
			v.Decisions = append(v.Decisions, Authorize(bind, kind, s, name, now, c.offReason(ctx, b, name)))
		}
		return v
	}
	out := AccessView{Binding: b.ID}
	out.Subjects = append(out.Subjects, view("anyone", "A sender on the allowlist", b, AccessSubject{SenderID: "x", Allowlisted: true}))
	paired := b
	paired.Paired = []connectors.PairedSender{{SenderID: "paired", At: now}}
	out.Subjects = append(out.Subjects, view("paired", "A paired sender", paired, AccessSubject{SenderID: "paired", Allowlisted: true}))
	if kind != nil {
		for _, r := range kind.Roles {
			out.Subjects = append(out.Subjects, view("role:"+r.ID, r.Title, b, AccessSubject{SenderID: "x", Allowlisted: true, Roles: []string{r.ID}}))
		}
	}
	out.Recent = c.svc.access.recent(b.ID, 50)
	return out, nil
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
		if r.Sensitive {
			roles = nil
		}
		d := Authorize(b, c.kindOf(ctx, b), AccessSubject{SenderID: r.Sender, Roles: roles, Allowlisted: true},
			r.Capability, c.o.Clock(), c.offReason(ctx, b, r.Capability))
		c.svc.access.record(c.o.Clock(), b, []string{r.Sender}, roles, d)
		return d.Allowed
	})
}
