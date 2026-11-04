package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// answerPhrase answers a proposal when the whole message is a short answer
// (see ParseReply): "yes", "ok", "cancel", or a word with the code of the
// proposal. It writes the answer back and reports true; for any other message,
// and for a bare word when nothing waits, it does nothing, so the text goes to
// the agent as usual. The text is the sender's own message: nothing the agent
// read or fetched can reach this path. Who may answer is decided by the
// capability gate, not here.
func (c *Connectors) answerPhrase(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, say func(string)) bool {
	h := c.svc.triggers
	if h == nil {
		return false
	}
	verb, code, ok := ParseReply(m.Text)
	if !ok {
		return false
	}
	reply, handled := h.AnswerByPhrase(ctx, PhraseFrom{Binding: b.ID, Owner: b.Owner, Agent: b.AgentID,
		Chat: m.Chat, Thread: m.Thread, Sender: m.SenderID, Roles: m.Roles}, verb, code)
	if handled {
		say(reply)
	}
	return handled
}
