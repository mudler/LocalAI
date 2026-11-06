package studio

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// A sub-agent can start a self-paced loop with start_loop, end its turn and
// wait. The parent is told that the sub-agent is done when the end of its turn
// reaches notifyWaiters; while the sub-agent has an active loop that end is
// held back, and the loop host releases it when the loop ends, pauses, fails
// or is stopped (Loops.release). The sub-agent counts as running meanwhile:
// for the limit of background sub-agents, for the parent's Stop and for the
// list the person sees.

// childPoll is how often a parent that waits for a sub-agent checks the store
// when no word has reached it. The word reaches the frontend that started the
// sub-agent only if the loop host of that frontend ends the loop; with several
// frontends another one may, and this check finds out.
var childPoll = 30 * time.Second

// loopHolds says whether the chat has an active self-paced loop, so that the
// chat is not done for a parent that waits for it.
func (s *Service) loopHolds(ctx context.Context, p, conv string) bool {
	h := s.loops
	if h == nil {
		return false
	}
	list, err := h.o.Store.ListChat(ctx, p, conv)
	if err != nil {
		return false
	}
	for _, l := range list {
		if l.SelfPaced() && l.State == loops.StateActive {
			return true
		}
	}
	return false
}

// holdForLoop says whether the end of a turn must not yet be told to the
// waiters of the chat, because the chat waits for its loop. Only this
// process's waiters matter, so a chat nobody waits for costs no read.
func (s *Service) holdForLoop(ctx context.Context, p, conv string, status store.TurnStatus) bool {
	if status == store.TurnStopped || s.isClosed() {
		return false
	}
	s.mu.Lock()
	n := len(s.waiters[key(p, conv)])
	s.mu.Unlock()
	return n > 0 && s.loopHolds(ctx, p, conv)
}

// endChildLoops ends the loops of a sub-agent chat and tells the parent that
// the sub-agent was stopped, when it had any.
func (s *Service) endChildLoops(ctx context.Context, p, conv string) {
	h := s.loops
	if h == nil {
		return
	}
	n, err := h.o.Store.DeleteChat(context.WithoutCancel(ctx), p, conv)
	if err != nil {
		s.log.Warn("studio: could not end the loops of a stopped sub-agent", "chat", conv, "error", err)
		return
	}
	if n > 0 {
		h.publish(p, conv)
		s.notifyWaiters(key(p, conv), store.TurnStopped)
	}
}

var errChildWait = errors.New("studio: the service closed while a sub-agent was awaited")

// waitChild waits until the turn of a sub-agent ends for good. done is the
// channel of waitFor. It gives up with ctx's error, or when the service
// closes. When the store shows that the sub-agent is idle with no loop for two
// checks in a row, the word did not reach this frontend and the check answers.
func (s *Service) waitChild(ctx context.Context, p, id string, done <-chan store.TurnStatus) (store.TurnStatus, error) {
	var tick <-chan time.Time
	if s.loops != nil {
		t := time.NewTicker(childPoll)
		defer t.Stop()
		tick = t.C
	}
	idle := 0
	for {
		select {
		case st := <-done:
			return st, nil
		case <-ctx.Done():
			return "", ctx.Err()
		case <-s.closed:
			return "", errChildWait
		case <-tick:
			info, err := s.d.Store.Turn(ctx, p, id)
			if err != nil || info.Running || info.Last == "" || s.loopHolds(ctx, p, id) {
				idle = 0
				continue
			}
			if idle++; idle >= 2 {
				s.forgetWaiter(key(p, id), done)
				return info.Last, nil
			}
		}
	}
}
