package studio

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// TurnHow tells how awaitTurn came back.
type TurnHow int

// The ways a watched turn ends.
const (
	// TurnEnded means the turn finished (done, failed, stopped or interrupted);
	// Info holds the outcome the store recorded.
	TurnEnded TurnHow = iota
	// TurnTimedOut means the time limit passed first. The turn still runs: the
	// caller stops it.
	TurnTimedOut
	// TurnAbandoned means the caller's stop channel closed first. Nothing is
	// known about the turn.
	TurnAbandoned
)

// TurnEnd is what awaitTurn found.
type TurnEnd struct {
	How TurnHow
	// Info is the turn state of the store, set for TurnEnded.
	Info store.TurnInfo
	// ErrText is the text of the error event of the turn, if one was raised.
	ErrText string
	// Tokens is the sum of the prompt and completion tokens of the model
	// requests of the turn that the events reported; 0 when none were seen.
	Tokens int
}

// awaitTurn waits for the turn that the caller started on a chat and reports how
// it ended. events must have been opened before the message was sent, so the
// start and the end cannot be missed. When the subscriber falls behind and its
// channel closes, awaitTurn watches the store instead. limit bounds the wait; a
// closed stop abandons it. It is shared by the trigger runs and the agent jobs,
// the two ways a chat runs without a person at the keyboard.
func (s *Service) awaitTurn(owner, chatID string, events <-chan session.Event, limit time.Duration, stop <-chan struct{}) TurnEnd {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	var poll <-chan time.Time
	started, errText, tokens := false, "", 0
	for {
		select {
		case ev, ok := <-events:
			if !ok { // the subscriber fell behind: watch the store instead
				events = nil
				p := time.NewTicker(time.Second)
				defer p.Stop()
				poll = p.C
				continue
			}
			switch ev.Kind {
			case session.KindTurnStart:
				started = true
			case session.KindError:
				errText = ev.Text
			case session.KindUsage:
				if ev.Usage != nil && started {
					tokens += ev.Usage.Used + ev.Usage.Completion
				}
			case session.KindTurnEnd:
				if started {
					return TurnEnd{How: TurnEnded, Info: s.settledTurn(owner, chatID), ErrText: errText, Tokens: tokens}
				}
			}
		case <-poll:
			if info, err := s.Turn(context.Background(), owner, chatID); err == nil && !info.Running && info.Last != "" {
				return TurnEnd{How: TurnEnded, Info: info, ErrText: errText, Tokens: tokens}
			}
		case <-timer.C:
			return TurnEnd{How: TurnTimedOut, ErrText: errText, Tokens: tokens}
		case <-stop:
			return TurnEnd{How: TurnAbandoned}
		}
	}
}

// settledTurn reads the turn state after its end event. The event comes before
// the store records the outcome, so it waits a moment for the claim to go.
func (s *Service) settledTurn(owner, chatID string) store.TurnInfo {
	ctx := context.Background()
	var info store.TurnInfo
	for i := 0; i < 250; i++ {
		var err error
		if info, err = s.Turn(ctx, owner, chatID); err != nil || !info.Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return info
}
