package studio

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The host side of the Sandbox proxy: egress.check and egress.audit. The
// add-on names neither the chat nor the user. The user is the actor of the
// live request and the chat is the resource the host put in that request when
// the chat's turn called bash: an action resource ID, a one-way key of the
// conversation (toolargs.ActionResourceID). The service finds the chat of the
// user with that key.

// ErrEgressNoChat means the live request names no chat of the user.
var ErrEgressNoChat = errors.New("studio: the request names no chat of this user")

// egressChats maps the action resource of a chat back to its ID. The mapping
// never changes (it is a hash), so the cache only saves the search; it is per
// process, and each frontend fills its own.
type egressChats struct {
	mu sync.Mutex
	m  map[string]string // principal + resource -> chat ID
}

const (
	egressChatsMax  = 4096
	egressScanPage  = 200
	egressScanChats = 20000
)

func (c *egressChats) get(k string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.m[k]
	return id, ok
}

func (c *egressChats) put(k, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= egressChatsMax {
		c.m = map[string]string{}
	}
	c.m[k] = id
}

// chatForResource finds the chat of p whose action resource is rid. A chat
// that runs a command is one the user used lately, so the newest chats come
// first.
func (s *Service) chatForResource(ctx context.Context, p, rid string) (session.ChatRecord, error) {
	k := p + "\x00" + rid
	if id, ok := s.egressChats.get(k); ok {
		rec, err := s.d.Store.GetChat(ctx, p, id)
		if err == nil {
			return rec, nil
		}
	}
	var cur session.ChatCursor
	for seen := 0; seen < egressScanChats; {
		page, err := s.d.Store.ListChats(ctx, p, cur, egressScanPage)
		if err != nil {
			return session.ChatRecord{}, err
		}
		for _, rec := range page {
			if toolargs.ActionResourceID(rec.ID) == rid {
				s.egressChats.put(k, rec.ID)
				return rec, nil
			}
		}
		if len(page) < egressScanPage {
			break
		}
		seen += len(page)
		last := page[len(page)-1]
		cur = session.ChatCursor{Updated: last.Updated, ID: last.ID}
	}
	return session.ChatRecord{}, ErrEgressNoChat
}

// egressPolicy is the chat whose policy applies to a request, and whether a
// person watches it. A sub-agent follows its parent's preset and rules with no
// card: nobody watches it, and the block is in its output for the parent. An
// unattended run (trigger, connector) follows its agent's network field and
// never asks.
type egressPolicy struct {
	rec      session.ChatRecord // the chat that owns the state (the parent for a sub-agent)
	chat     egress.Chat
	attended bool
}

func (s *Service) egressPolicyOf(ctx context.Context, rec session.ChatRecord, op string) (egressPolicy, error) {
	owner := rec
	attended := rec.Origin == "" && rec.Kind != session.KindSubAgent
	preset := rec.Egress.Preset
	if rec.Kind == session.KindSubAgent && rec.ParentID != "" {
		parent, err := s.d.Store.GetChat(ctx, rec.Principal, rec.ParentID)
		if err != nil {
			return egressPolicy{}, err
		}
		owner = parent
		preset = parent.Egress.Preset
	}
	if owner.Origin != "" {
		preset = s.agentNetwork(ctx, owner)
	}
	now := s.now()
	st := owner.Egress
	return egressPolicy{rec: owner, attended: attended, chat: egress.Chat{
		Preset: preset, Rules: st.Rules, Once: st.Grants(op, now), Attended: attended, Pending: st.Pending(),
	}}, nil
}

// agentNetwork is the preset of an unattended run: off in this phase.
func (s *Service) agentNetwork(context.Context, session.ChatRecord) egress.Preset {
	return egress.PresetOff
}

// EgressCheck decides one connection of a command of the live call op, in the
// chat with the action resource rid of user, and audits the verdict.
func (s *Service) EgressCheck(ctx context.Context, user, rid, op string, r egress.Request) (egress.Verdict, error) {
	p := principal(user)
	r.Host = canonicalHost(r.Host)
	line := audit.Event{User: p, Host: r.Host, Port: r.Port, Kind: r.Kind, At: s.now().UTC()}
	rec, err := s.chatForResource(ctx, p, rid)
	if err != nil {
		v := egress.Verdict{Action: egress.Block, Reason: "chat"}
		line.Verdict, line.Reason = v.Action, v.Reason
		s.auditEgress(ctx, line)
		if errors.Is(err, ErrEgressNoChat) {
			return v, nil
		}
		return v, err
	}
	line.Chat = rec.ID
	pol, err := s.egressPolicyOf(ctx, rec, op)
	if err != nil {
		return egress.Verdict{Action: egress.Block, Reason: "chat"}, err
	}
	v := egress.Decide(s.egressSettings(), pol.chat, r, egress.BuiltinRegistries())
	if v.Action == egress.Ask {
		// A card asks the person: see the cards. Without one, a block.
		v = s.askEgress(ctx, pol, rec, op, r)
	}
	if v.Action == egress.Allow && v.Reason == egress.ReasonOnce {
		s.claimGrant(ctx, pol.rec, op, r)
	}
	line.Verdict, line.Reason = v.Action, v.Reason
	s.auditEgress(ctx, line)
	return v, nil
}

// askEgress is the card flow; this phase has none, so the request is blocked.
func (s *Service) askEgress(_ context.Context, _ egressPolicy, _ session.ChatRecord, _ string, _ egress.Request) egress.Verdict {
	return egress.Verdict{Action: egress.Block, Reason: egress.ReasonOff}
}

// claimGrant ties a late "allow once" to the call that used it first, so it
// does not carry on to later calls.
func (s *Service) claimGrant(ctx context.Context, rec session.ChatRecord, op string, r egress.Request) {
	now := s.now()
	_ = s.d.Store.UpdateEgress(ctx, rec.Principal, rec.ID, func(st *egress.ChatState) error {
		for i, g := range st.Once {
			if g.Op == "" && g.Host == r.Host && g.Port == r.Port && now.Before(g.Expires) {
				st.Once[i].Op = op
				st.Once[i].Expires = now.Add(time.Hour)
			}
		}
		return nil
	})
}

// EgressEvent is one line the proxy reports about a connection of a call.
type EgressEvent struct {
	Host, Kind, Verdict, Reason, Addr string
	Port                              int
	BytesUp, BytesDown, DurationMS    int64
	At                                time.Time
}

// EgressAudit records what the connections of a call did: the bytes of the
// allowed ones ("done"), and the blocks the proxy made itself (a refused
// address, a TLS name that did not match, a connection while no call ran).
func (s *Service) EgressAudit(ctx context.Context, user, rid string, evs []EgressEvent) error {
	p := principal(user)
	chat := ""
	if rec, err := s.chatForResource(ctx, p, rid); err == nil {
		chat = rec.ID
	}
	now := s.now().UTC()
	lines := make([]audit.Event, 0, len(evs))
	for _, e := range evs {
		at := e.At.UTC()
		if at.IsZero() || at.After(now) {
			at = now
		}
		lines = append(lines, audit.Event{User: p, Chat: chat, Host: canonicalHost(e.Host), Port: e.Port, Kind: e.Kind, Verdict: e.Verdict, Reason: e.Reason,
			Addr: e.Addr, BytesUp: e.BytesUp, BytesDown: e.BytesDown, DurationMS: e.DurationMS, At: at})
	}
	return s.egressAudit().Add(ctx, lines...)
}

func (s *Service) auditEgress(ctx context.Context, e audit.Event) {
	if err := s.egressAudit().Add(context.WithoutCancel(ctx), e); err != nil {
		s.log.Warn("Chats: could not audit a Sandbox network request", "error", err)
	}
}

// canonicalHost is the form a host is stored and compared in: lower case,
// without a trailing dot, cut at 253 bytes.
func canonicalHost(h string) string {
	b := []byte(h)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	out := string(b)
	for len(out) > 0 && out[len(out)-1] == '.' {
		out = out[:len(out)-1]
	}
	if len(out) > 253 {
		out = out[:253]
	}
	return out
}
