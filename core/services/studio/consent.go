package studio

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// The two consent cards of a chat, the network card of the Sandbox and the
// card of the built-in bash, share one rule for when a card still has
// someone waiting on it. Both are stored in the chat record, which every
// frontend reads, and both name the turn that asked. A card is live only
// while that turn holds the chat and its wait has not run out: a frontend
// that died mid-turn leaves its cards behind, and the next turn, the reaper
// or a reader must not take them for waiting ones.

// consentGrace covers a clock difference between the frontend that stored a
// card and the one that reads it.
const consentGrace = 30 * time.Second

// consentLive says whether a card asked by the turn turn at asked, with a
// waiter that gives up after wait, may still have that waiter. info is the
// turn state of the chat now. A card that names its turn is live only while
// that turn holds the chat. A card that names none (stored before cards named
// their turn, or asked while the store showed no turn) is judged by its age
// alone. Either way a card older than its wait has no waiter.
func consentLive(turn string, asked time.Time, wait time.Duration, info store.TurnInfo, now time.Time) bool {
	if turn != "" && (!info.Running || turn != info.RunID) {
		return false
	}
	return now.Sub(asked) < wait+consentGrace
}

// consentTurn is the turn a new card names: the running turn, or none.
func consentTurn(info store.TurnInfo) string {
	if info.Running {
		return info.RunID
	}
	return ""
}

// chatTurn is the turn state of a chat. A store error reads as no turn: the
// cards it decides then count as left over, which never blocks a request.
func (s *Service) chatTurn(ctx context.Context, p, conv string) store.TurnInfo {
	info, err := s.d.Store.Turn(ctx, p, conv)
	if err != nil {
		s.log.Warn("Chats: could not read the turn of a chat for its cards", "chat", conv, "error", err)
		return store.TurnInfo{}
	}
	return info
}

// liveEgress returns st as a reader should see it: a pending card nobody
// waits on reads as expired, and live holds the pending cards that still
// wait. The record is not changed.
func (s *Service) liveEgress(st egress.ChatState, info store.TurnInfo) (view egress.ChatState, live []egress.Card) {
	if len(st.Pending()) == 0 {
		return st, nil
	}
	now, wait := s.now(), s.egressCardWait()
	view = st.Clone()
	for i, c := range view.Cards {
		if c.State != egress.CardPending {
			continue
		}
		if consentLive(c.Turn, c.AskedAt, wait, info, now) {
			live = append(live, c)
			continue
		}
		view.Cards[i].State = egress.CardExpired
	}
	return view, live
}

// expireConsents ends the cards of a turn that is over: the pending network
// cards expire (they stay visible and can be answered late) and the card of
// the built-in bash is cleared (an answer to it gets ErrHostExecExpired). run names the turn; empty ends the cards of
// any turn, as at the start of a new turn, when none of them has a waiter.
func (s *Service) expireConsents(ctx context.Context, p, conv, run string) {
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return
	}
	mine := func(turn string) bool { return run == "" || turn == "" || turn == run }
	if hc := rec.HostConsent; hc != nil && mine(hc.Turn) {
		s.clearHostConsent(ctx, p, conv, hc.ID)
	}
	pending := false
	for _, c := range rec.Egress.Pending() {
		if mine(c.Turn) {
			pending = true
		}
	}
	if !pending {
		return
	}
	now := s.now().UTC()
	changed := false
	_ = s.d.Store.UpdateEgress(ctx, p, conv, func(st *egress.ChatState) error {
		for _, c := range st.Cards {
			if c.State == egress.CardPending && mine(c.Turn) {
				c.State, c.AnsweredAt = egress.CardExpired, now
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
