package studio

import (
	"context"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The new chat command lets a sender ask for a fresh chat from the app. The
// host reads it before any model turn, as it does a pairing code, so the text
// never reaches the agent. It only drops the mapping from the sender (or the
// shared conversation) to its chat: the old chat stays in Studio, and its
// memory, files and triggers stay as they are. Add-ons only deliver messages,
// so this works for every connector.

// newCommand is the whole text of the command. The slash form is the one of
// Telegram, with the name of the bot after an at sign in a group. Slack takes
// a message that begins with a slash for its own commands, so the bang form
// is the one to use there. Anything around the command other than white space
// makes the message plain text.
var newCommand = regexp.MustCompile(`(?i)^(?:/new(?:@[a-z0-9_]{1,64})?|!new)$`)

// isNewCommand tells whether the message is the new chat command. A message
// with files is never a command.
func isNewCommand(m contract.ConnectorMessage) bool {
	return len(m.Media) == 0 && newCommand.MatchString(strings.TrimSpace(m.Text))
}

// newChat handles the command. The caller has checked that the sender may
// talk to the bot. It never logs the text of the message.
func (c *Connectors) newChat(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, tell func(string, ...any)) {
	mode := b.ConversationMode()
	if mode == connectors.ConversationThread {
		c.audit(b, m.SenderID, "chat.new", "thread mode")
		tell("new.each")
		return
	}
	key := c.conversationOf(ctx, b, m)
	// A conversation that other people write in changes for all of them: in
	// the shared chat mode, and in a thread of a group. Only a paired sender
	// may reset it. A direct conversation has the sender only.
	if !m.Direct && (mode == connectors.ConversationChat || m.Thread != "") && !b.IsPaired(m.SenderID, c.o.Clock()) {
		c.audit(b, m.SenderID, "chat.new", "not paired")
		tell("new.shared")
		return
	}
	// A turn that runs keeps its chat: the reset waits for the sender to ask
	// again once the answer is out.
	st := c.sender(b.ID, "\x01"+key)
	c.smu.Lock()
	busy := st.active
	c.smu.Unlock()
	if busy {
		c.audit(b, m.SenderID, "chat.new", "busy")
		tell("new.busy")
		return
	}
	if err := c.o.Store.ClearChat(ctx, b.ID, key); err != nil {
		c.log.Warn("studio: could not start a new connector chat", "binding", b.ID, "error", err)
		tell("unavailable")
		return
	}
	c.audit(b, m.SenderID, "chat.new", "reset")
	tell("new.done")
}
