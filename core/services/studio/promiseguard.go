package studio

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/nib/promise"

	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The promise guard. A model that ends its turn with "I will check in a
// minute" has promised something that nothing will do, unless it armed a loop,
// a wait or a background sub-agent. After the turn the host reads the last
// sentences of the answer (github.com/mudler/nib/promise: English, Italian,
// Spanish, French, German; questions, offers and negations do not count). When
// the answer promises and nothing in the chat could keep the promise, the host
// sends the model one note and the model gets one more turn: it arms
// something now, or tells the person plainly that it cannot watch.
//
// The guard fires at most once per turn: the turn that answers the note is
// never guarded. It does not apply to sub-agent chats, whose final report goes
// to the parent.

// finalTextCap is how much of the end of the answer a run keeps.
const finalTextCap = 4000

// noteFinalText keeps the text the model wrote since its last tool call, which
// is its answer when the turn ends.
func (r *run) noteFinalText(ev session.Event) {
	switch ev.Kind {
	case session.KindToolStart:
		r.final = ""
	case session.KindText:
		r.final += ev.Text
		if len(r.final) > 2*finalTextCap {
			r.final = r.final[len(r.final)-finalTextCap:]
		}
	}
}

// guardArmed says whether something in the chat will run later or is running
// now: an active, pending or running loop or wait, a sub-agent that runs, or
// a message that waits for the next turn. A read that fails counts as armed,
// because a wrong note is worse than a missed one.
func (s *Service) guardArmed(ctx context.Context, rec session.ChatRecord) bool {
	if h := s.loops; h != nil {
		list, err := h.o.Store.ListChat(ctx, rec.Principal, rec.ID)
		if err != nil {
			return true
		}
		for _, l := range list {
			if l.Running || l.State == loops.StateActive || l.State == loops.StatePending {
				return true
			}
		}
	}
	kids, err := s.runningChildren(ctx, rec.Principal, rec.ID)
	if err != nil || len(kids) > 0 {
		return true
	}
	pend, err := s.d.Store.Pending(ctx, rec.Principal, rec.ID)
	return err != nil || len(pend) > 0
}

// guardNote is the note for the model. canArm says whether the chat has the
// tools to arm something; a connector chat has none, so the note only asks
// for a plain answer.
func guardNote(sentence string, tools []string) string {
	said := fmt.Sprintf("Your last answer promises something for later: %q. ", truncate(sentence, 200))
	if len(tools) == 0 {
		return "(System note: " + said + "Nothing in this chat can run later, so the promise is empty. " +
			"Tell the person plainly, in one or two short sentences, that you cannot watch it or come back to it, and what they can do instead. Do not promise it again.)"
	}
	return "(System note: " + said + "Nothing is armed in this chat: no loop, no wait and no running sub-agent, so nothing will run later. " +
		"Arm it now with " + strings.Join(tools, ", ") + ", or tell the person plainly that you cannot watch it. " +
		"Then say in one or two short sentences what you did. Do not promise it again without arming it.)"
}

// guardTools names the tools that the chat really has for keeping a promise.
// A connector chat has only the loop tools that its sender holds the
// capability for, and no sub-agent: its messages can come from strangers.
func (s *Service) guardTools(ctx context.Context, rec session.ChatRecord) []string {
	if !s.loopOriginKnown(rec.Origin) {
		return nil
	}
	var out []string
	if s.loops != nil {
		if s.loopAllowed(rec, contract.CapLoopsStart) {
			out = append(out, startLoopName+" (a check that needs your judgement each time)")
		}
		if s.loopAllowed(rec, contract.CapLoopsWait) {
			out = append(out, waitForName+" (watch a tool or a URL with no model call until it changes)")
		}
	}
	if rec.Origin == session.OriginConnector {
		return out
	}
	if !s.hiddenFor(ctx, rec)[spawnAgentName] {
		out = append(out, spawnAgentName+" with wait=false (work that runs on its own)")
	}
	return out
}

// promiseGuard starts the extra turn when the answer final of the turn that
// just ended is an empty promise. It reports whether it started a turn. The
// caller holds the lock of the chat.
func (s *Service) promiseGuard(ctx context.Context, p, conv, final string) (bool, error) {
	if strings.TrimSpace(final) == "" || s.isClosed() {
		return false, nil
	}
	m, ok := promise.Find(final)
	if !ok {
		return false, nil
	}
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil || rec.Kind == session.KindSubAgent {
		return false, err
	}
	if s.guardArmed(ctx, rec) {
		return false, nil
	}
	runID := newID()
	if err := s.d.Store.ClaimTurn(ctx, p, conv, runID); err != nil {
		if errors.Is(err, store.ErrTurnRunning) {
			return false, nil
		}
		return false, err
	}
	s.guardRuns.Store(key(p, conv), runID)
	if err := s.startTurn(ctx, p, conv, runID, session.Message{HostNote: guardNote(m.Sentence, s.guardTools(ctx, rec))}); err != nil {
		s.guardRuns.Delete(key(p, conv))
		return false, err
	}
	return true, nil
}
