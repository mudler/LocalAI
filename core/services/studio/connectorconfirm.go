package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// answerPhrase answers a proposal when the whole message is a short answer
// (see ParseReply): "yes", "ok", "cancel", or a word with the code of the
// proposal. It writes the answer back and reports true; for any other message,
// and for a bare word when nothing waits, it does nothing, so the text goes to
// the agent as usual. The text is the sender's own message: nothing the agent
// read or fetched can reach this path. Who may answer is decided by the
// capability gate, not here.
func (c *Connectors) answerPhrase(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, say func(string), v hostmsg.Voice) bool {
	verb, code, ok := ParseReply(m.Text)
	isUndo := isUndoReply(m.Text)
	if !ok {
		return false
	}
	// A write of an add-on that waits in this chat is answered first, since a
	// bare word with one waiting must not be taken by anything else.
	if reply, handled := c.answerHolds(ctx, b, m, verb, code, v); handled {
		say(reply)
		return true
	}
	// Change proposals (a note, a profile, a skill, a page, the dashboard) are
	// looked at first, since a bare word with a change waiting must not be
	// taken by a trigger alone. They return handled false when nothing waits
	// here, or when the code is not theirs.
	if reply, handled := c.answerChanges(ctx, b, m, verb, code, isUndo, v); handled {
		say(reply)
		return true
	}
	h := c.svc.triggers
	if h == nil {
		if code == "" {
			if reply, ok := c.lapsedHoldReply(ctx, b, m, v); ok {
				say(reply)
				return true
			}
		}
		return false
	}
	reply, handled := h.AnswerByPhrase(ctx, PhraseFrom{Binding: b.ID, Owner: b.Owner, Agent: b.AgentID,
		Chat: m.Chat, Direct: m.Direct, Thread: m.Thread, Sender: m.SenderID, Username: m.SenderUsername, Roles: m.Roles, Voice: v}, verb, code)
	if handled {
		say(reply)
		return true
	}
	if code == "" {
		if reply, ok := c.lapsedHoldReply(ctx, b, m, v); ok {
			say(reply)
			return true
		}
	}
	return false
}
