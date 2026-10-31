package studio

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The approve-on-block card: when a command of a chat that a person watches
// asks for a host the chat does not allow, but the ceiling would, the check
// stores a card in the chat, publishes egress_consent, and waits for the
// answer inside the open connection, so pip goes on by itself after the
// click. The answer is written to the chat record first and then sent on the
// chat bus; the waiting check wakes on the message, or on a re-read of the
// card every two seconds, so a lost message costs at most two seconds and the
// answer may reach any frontend.

// The answers of a card.
const (
	EgressOnce = "once"
	EgressChat = "chat"
	EgressDeny = "deny"
)

// DefaultEgressCardWait is how long a connection waits for a card's answer.
const DefaultEgressCardWait = 60 * time.Second

// egressReread is how often a waiting check reads the card again.
const egressReread = 2 * time.Second

var (
	// ErrEgressDecision means the person may not give the answer: a private
	// target needs an administrator to allow it.
	ErrEgressDecision = errors.New("studio: only an administrator can allow a host inside your network; anyone can deny")
	// ErrEgressBadDecision means the answer is not once, chat or deny.
	ErrEgressBadDecision = errors.New("studio: the answer must be once, chat or deny")
	// ErrEgressNoCard means the chat has no such card.
	ErrEgressNoCard = errors.New("studio: the chat has no such network card")
	// ErrEgressAnswered means the card was answered already with another
	// answer. The same answer again is not an error: it changes nothing.
	ErrEgressAnswered = errors.New("studio: the network card was answered already")
	// ErrEgressHostAboveCeiling means the administrator's limit does not admit
	// the host of the card, so only an administrator can make it work, by
	// raising the limit. The card is refused and stays so.
	ErrEgressHostAboveCeiling = errors.New("studio: the limit an administrator set for the Sandbox network does not admit this host")
)

// EgressDecision is the answer of a card, sent on the chat bus to the check
// that waits for it.
type EgressDecision struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
}

func (s *Service) egressCardWait() time.Duration {
	if s.d.EgressCardWait > 0 {
		return s.d.EgressCardWait
	}
	return DefaultEgressCardWait
}

// askEgress stores a card in the chat that owns the policy and waits for the
// answer. A second connection to the same host and port waits on the same
// card. It returns the verdict of the answer, or a block when nobody answered
// in time, the call ended or the ceiling went down meanwhile.
func (s *Service) askEgress(ctx context.Context, pol egressPolicy, _ session.ChatRecord, op string, r egress.Request) egress.Verdict {
	owner := pol.rec
	k := key(owner.Principal, owner.ID)
	var want atomic.Value
	want.Store(newID())
	answer := make(chan string, 1)
	cancel := s.bus.OnControl(k, func(m ControlMsg) {
		if m.Egress != nil && m.Egress.ID == want.Load().(string) {
			select {
			case answer <- m.Egress.Decision:
			default:
			}
		}
	})
	defer cancel()

	now := s.now().UTC()
	info := s.chatTurn(ctx, owner.Principal, owner.ID)
	created, stale := false, false
	err := s.d.Store.UpdateEgress(ctx, owner.Principal, owner.ID, func(st *egress.ChatState) error {
		// A pending card nobody waits on (its turn is over, or a frontend
		// that died left it) expires here: it neither takes a place of the
		// cap nor gets a second connection to wait on it.
		_, live := s.liveEgress(*st, info)
		for _, c := range st.Pending() {
			if !slices.ContainsFunc(live, func(l egress.Card) bool { return l.ID == c.ID }) {
				c.State, c.AnsweredAt = egress.CardExpired, now
				st.PutCard(c)
				stale = true
			}
		}
		for _, c := range live {
			if c.Host == r.Host && c.Port == r.Port {
				want.Store(c.ID)
				return nil
			}
		}
		if len(live) >= egress.MaxPendingCards {
			return errEgressCardsFull
		}
		st.PutCard(egress.Card{ID: want.Load().(string), Host: r.Host, Port: r.Port, Kind: r.Kind, State: egress.CardPending,
			Private: r.Private || isPrivateLiteral(r.Host), Op: op, Turn: consentTurn(info), AskedAt: now})
		created = true
		return nil
	})
	if stale {
		s.publishEgress(owner.Principal, owner.ID, "")
	}
	switch {
	case errors.Is(err, errEgressCardsFull):
		return egress.Verdict{Action: egress.Block, Reason: egress.ReasonCardsFull}
	case err != nil:
		s.log.Warn("Chats: could not keep a network card with the chat", "chat", owner.ID, "error", err)
		return egress.Verdict{Action: egress.Block, Reason: egress.ReasonNoAnswer}
	}
	id := want.Load().(string)
	if created {
		s.bus.Publish(k, session.Event{Kind: session.KindEgressConsent, IDs: []string{id}, Text: net.JoinHostPort(r.Host, strconv.Itoa(r.Port))})
	}

	wait := s.egressCardWait()
	if dl, ok := ctx.Deadline(); ok {
		// Never past the call's own timeout: leave time to answer the proxy.
		if left := time.Until(dl) - 5*time.Second; left < wait {
			wait = max(left, 0)
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	tick := time.NewTicker(egressReread)
	defer tick.Stop()
	for {
		select {
		case <-answer:
		case <-tick.C:
		case <-timer.C:
			return egress.Verdict{Action: egress.Block, Reason: egress.ReasonNoAnswer}
		case <-ctx.Done():
			return egress.Verdict{Action: egress.Block, Reason: egress.ReasonNoAnswer}
		}
		if v, done := s.cardVerdict(ctx, owner, id, op, r); done {
			return v
		}
	}
}

var errEgressCardsFull = errors.New("studio: the chat has as many network cards waiting as it may")

// cardVerdict reads the card and the chat again. Once the card is answered
// (or refused), the request is decided again with the stored state, which now
// holds the rule or the grant; done is false while the card waits.
func (s *Service) cardVerdict(ctx context.Context, owner session.ChatRecord, id, op string, r egress.Request) (egress.Verdict, bool) {
	rec, err := s.d.Store.GetChat(ctx, owner.Principal, owner.ID)
	if err != nil {
		return egress.Verdict{}, false
	}
	c, ok := rec.Egress.Card(id)
	if !ok {
		return egress.Verdict{Action: egress.Block, Reason: egress.ReasonNoAnswer}, true
	}
	set := s.egressSettings()
	if c.State == egress.CardPending {
		if egress.Admits(set, r, egress.BuiltinRegistries()) {
			return egress.Verdict{}, false
		}
		// The ceiling went down while the card waited.
		s.refuseCard(ctx, owner, id)
		return egress.Verdict{Action: egress.Block, Reason: egress.ReasonCeiling}, true
	}
	if c.State == egress.CardRefused {
		return egress.Verdict{Action: egress.Block, Reason: egress.ReasonCeiling}, true
	}
	pol, err := s.egressPolicyOf(ctx, rec, op)
	if err != nil {
		return egress.Verdict{Action: egress.Block, Reason: egress.ReasonNoAnswer}, true
	}
	pol.chat.Attended = false // decided by the answer: no second card
	v := egress.Decide(set, pol.chat, r, egress.BuiltinRegistries())
	if v.Action != egress.Allow && c.State == egress.CardDenied {
		v.Reason = egress.ReasonDenied
	}
	return v, true
}

func (s *Service) refuseCard(ctx context.Context, owner session.ChatRecord, id string) {
	_ = s.d.Store.UpdateEgress(context.WithoutCancel(ctx), owner.Principal, owner.ID, func(st *egress.ChatState) error {
		if c, ok := st.Card(id); ok && c.State == egress.CardPending {
			c.State = egress.CardRefused
			c.AnsweredAt = s.now().UTC()
			st.PutCard(c)
		}
		return nil
	})
	s.publishEgress(owner.Principal, owner.ID, id)
}

func isPrivateLiteral(host string) bool {
	return !egress.Admits(egress.Settings{Ceiling: egress.PresetAll}, egress.Request{Host: host}, egress.BuiltinRegistries())
}

// EgressDecide answers a card of the user's chat: once (the rest of the
// command, or the next call within ten minutes when it comes late), chat (a
// rule for the chat) or deny (a deny rule, so the host does not ask again). A
// card that expired with its turn may still be answered. A private target
// needs an administrator to allow it. The chat of another user is
// session.ErrNotFound.
func (s *Service) EgressDecide(ctx context.Context, user, conv, id, decision string) error {
	p := principal(user)
	if id == "" || len(id) > 64 {
		return ErrEgressNoCard
	}
	if decision != EgressOnce && decision != EgressChat && decision != EgressDeny {
		return ErrEgressBadDecision
	}
	admin := s.isAdmin(ctx, p)
	set := s.egressSettings()
	now := s.now().UTC()
	refused, again := false, false
	err := s.d.Store.UpdateEgress(ctx, p, conv, func(st *egress.ChatState) error {
		c, ok := st.Card(id)
		if !ok {
			return ErrEgressNoCard
		}
		switch c.State {
		case egress.CardPending, egress.CardExpired:
		case egress.CardRefused:
			// The same reason on every click, not a silent conflict.
			again = true
			return nil
		default:
			if answerState(decision) == c.State {
				again = true // a double click: it is applied, say so again
				return nil
			}
			return ErrEgressAnswered
		}
		if c.Private && decision != EgressDeny && !admin {
			return ErrEgressDecision
		}
		req := egress.Request{Host: c.Host, Port: c.Port, Kind: c.Kind, TLS: c.Port == 443 && c.Kind == egress.KindConnect, Private: c.Private}
		if decision != EgressDeny && !egress.Admits(set, req, egress.BuiltinRegistries()) {
			c.State, c.AnsweredAt = egress.CardRefused, now
			st.PutCard(c)
			refused = true
			return nil
		}
		c.AnsweredAt, c.AnsweredBy = now, p
		switch decision {
		case EgressOnce:
			c.State = egress.CardOnce
			// Any call of the chat may take it in the next ten minutes; the
			// first that does keeps it for the rest of that command.
			st.AddGrant(egress.Grant{Host: c.Host, Port: c.Port, Admin: admin, Expires: now.Add(egress.LateOnce)}, now)
		case EgressChat:
			c.State = egress.CardChat
			st.PutRule(egress.Rule{Host: c.Host, Port: c.Port, Allow: true, By: p, Admin: admin, At: now, Card: c.ID})
		case EgressDeny:
			c.State = egress.CardDenied
			st.PutRule(egress.Rule{Host: c.Host, Port: c.Port, Allow: false, By: p, Admin: admin, At: now, Card: c.ID})
		}
		st.PutCard(c)
		return nil
	})
	if err != nil {
		return err
	}
	if again {
		if card, ok := s.egressCard(ctx, p, conv, id); ok && card.State == egress.CardRefused {
			return ErrEgressHostAboveCeiling
		}
		return nil
	}
	s.bus.Control(key(p, conv), ControlMsg{Egress: &EgressDecision{ID: id, Decision: decision}})
	s.publishEgress(p, conv, id)
	if refused {
		return ErrEgressHostAboveCeiling
	}
	return nil
}

// answerState is the state a card takes when it gets the answer.
func answerState(decision string) string {
	switch decision {
	case EgressOnce:
		return egress.CardOnce
	case EgressChat:
		return egress.CardChat
	case EgressDeny:
		return egress.CardDenied
	}
	return ""
}

func (s *Service) egressCard(ctx context.Context, p, conv, id string) (egress.Card, bool) {
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return egress.Card{}, false
	}
	return rec.Egress.Card(id)
}

// settleEgressCards runs when a turn of the chat ends: a card nobody answered
// expires (it stays visible and can still be answered from the network panel),
// and each card of the turn is tied to the transcript line of the bash call
// that asked, the first tool line that ended after it was asked, so a reload
// draws it under that line.
func (s *Service) settleEgressCards(ctx context.Context, p, conv string, since time.Time) {
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil || len(rec.Egress.Cards) == 0 {
		return
	}
	open := false
	for _, c := range rec.Egress.Cards {
		if c.State == egress.CardPending || c.ToolSeq == 0 {
			open = true
		}
	}
	if !open {
		return
	}
	var tools []session.Entry
	if entries, err := s.d.Store.Entries(ctx, conv, 0, 0); err == nil {
		for _, e := range entries {
			if e.Role == session.RoleTool && !e.At.Before(since.Add(-time.Minute)) {
				tools = append(tools, e)
			}
		}
	}
	now := s.now().UTC()
	changed := false
	_ = s.d.Store.UpdateEgress(ctx, p, conv, func(st *egress.ChatState) error {
		for _, c := range st.Cards {
			before := c
			if c.State == egress.CardPending {
				c.State, c.AnsweredAt = egress.CardExpired, now
			}
			if c.ToolSeq == 0 {
				for _, e := range tools {
					if !e.At.Before(c.AskedAt) {
						c.ToolSeq = e.Seq
						break
					}
				}
			}
			if c != before {
				st.PutCard(c)
				changed = true
			}
		}
		return nil
	})
	if changed {
		s.publishEgress(p, conv, "")
	}
}
