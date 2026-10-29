package studio

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// What a sender sees while a turn runs.
const (
	// workingText is the working message before anything else is known.
	workingText = "Working…"
	// cursorMark ends a preview of an answer that is still being written.
	cursorMark = " ▍"
	// typingEvery is how often typing is shown again: a platform shows it
	// for about five seconds.
	typingEvery = 4 * time.Second
	// maxPreviewRunes is how much of the answer a preview shows: its end.
	maxPreviewRunes = 3000
	// maxStatusRunes is how much reasoning a preview shows: its end.
	maxStatusRunes = 400
	// maxToolsShown is how many tool names a progress message lists.
	maxToolsShown = 5
	// editTimeout bounds one edit: a slow edit is skipped, not waited for.
	editTimeout = 5 * time.Second
)

// liveReply is the working message of one turn in one conversation. One reply
// goroutine owns it, so it needs no lock.
type liveReply struct {
	mode string
	// id is the working message. It is "" when the mode is off, when the
	// message could not be sent, or after an edit failed: the answer then
	// goes as a new message.
	id string
	// shown is what the working message shows now.
	shown string
	tools []string
	// answer is the text of the answer so far, and status the latest
	// reasoning or tool line, for the stream mode.
	answer strings.Builder
	status string
	// reasoning tells that status holds reasoning, which grows, rather than
	// a tool line, which is replaced.
	reasoning bool
}

// observe takes one event of the turn.
func (l *liveReply) observe(ev session.Event) {
	switch ev.Kind {
	case session.KindToolStart:
		if ev.Tool == nil || ev.Tool.Name == "" {
			return
		}
		if n := len(l.tools); n == 0 || l.tools[n-1] != ev.Tool.Name {
			l.tools = append(l.tools, ev.Tool.Name)
		}
		if len(l.tools) > maxToolsShown {
			l.tools = l.tools[len(l.tools)-maxToolsShown:]
		}
		// The text before a tool call is a preamble; the answer comes after.
		l.answer.Reset()
		l.status, l.reasoning = "Using "+ev.Tool.Name+"…", false
	case session.KindText:
		l.answer.WriteString(ev.Text)
	case session.KindReasoning:
		if !l.reasoning {
			l.status, l.reasoning = "", true
		}
		l.status = tailRunes(l.status+ev.Text, maxStatusRunes)
	}
}

// view is what the working message should show now.
func (l *liveReply) view() string {
	switch l.mode {
	case connectors.ReplyStream:
		if t := strings.TrimSpace(l.answer.String()); t != "" {
			return tailRunes(t, maxPreviewRunes) + cursorMark
		}
		if s := strings.TrimSpace(l.status); s != "" {
			return workingText + "\n" + s
		}
	case connectors.ReplyProgress:
		if len(l.tools) > 0 {
			return workingText + "\n• " + strings.Join(l.tools, "\n• ")
		}
	}
	return workingText
}

// tailRunes keeps the last n runes of s, and marks a cut with an ellipsis.
func tailRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return "…" + string(r[len(r)-n:])
}

// startLive shows that the bot works: typing at once, and the working message
// unless the binding's mode is off.
func (c *Connectors) startLive(b connectors.Binding, token string, m contract.ConnectorMessage) *liveReply {
	l := &liveReply{mode: b.ReplyMode()}
	// Typing goes after the working message, which it names: a platform
	// without typing would otherwise post a working message of its own.
	defer func() { c.typing(b, token, m, l.id) }()
	if l.mode == connectors.ReplyOff {
		return l
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.o.Transport.Call(ctx, b.AddonID, contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: b.Kind, Token: token, Chat: m.Chat, Thread: m.Thread, Text: workingText})
	if err == nil && resp.Error != nil {
		err = resp.Error
	}
	if err != nil {
		c.log.Warn("studio: could not send a connector working message", "binding", b.ID, "kind", b.Kind, "error", err)
		return l
	}
	l.id, l.shown = resp.MessageID, workingText
	return l
}

// typing shows typing in the conversation. id names the working message, so
// a platform without typing does not post one of its own.
func (c *Connectors) typing(b connectors.Binding, token string, m contract.ConnectorMessage, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpTyping, Token: token, Chat: m.Chat, Thread: m.Thread, MessageID: id})
}

// mayEdit spends the edit budget of a binding: at most one edit each
// EditEvery, across all its conversations. A refused edit stays pending and
// goes at a later tick, with what is new by then.
func (c *Connectors) mayEdit(binding string) bool {
	now := time.Now()
	c.emu.Lock()
	defer c.emu.Unlock()
	if now.Before(c.nextEdit[binding]) {
		return false
	}
	c.nextEdit[binding] = now.Add(c.o.EditEvery)
	return true
}

// holdEdits keeps the edits of a binding back for the wait a platform asked for.
func (c *Connectors) holdEdits(binding string, d time.Duration) {
	c.emu.Lock()
	defer c.emu.Unlock()
	if until := time.Now().Add(d); until.After(c.nextEdit[binding]) {
		c.nextEdit[binding] = until
	}
}

// flush edits the working message when it should show something new and the
// binding's budget allows. A failed edit gives up the working message, so the
// answer goes as a new message; a rate-limited one only waits.
func (c *Connectors) flush(b connectors.Binding, token string, m contract.ConnectorMessage, l *liveReply) {
	if l.id == "" {
		return
	}
	v := l.view()
	if v == l.shown || !c.mayEdit(b.ID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), editTimeout)
	defer cancel()
	err := c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpEdit, Token: token, Chat: m.Chat, Thread: m.Thread, MessageID: l.id, Text: v})
	var ce *contract.ConnectorError
	switch {
	case err == nil:
		l.shown = v
	case errors.As(err, &ce) && ce.Code == contract.ConnectorRateLimited:
		c.holdEdits(b.ID, time.Duration(max(ce.RetryAfter, 1))*time.Second)
	default:
		c.log.Debug("studio: a connector edit failed; the answer goes as a new message", "binding", b.ID, "kind", b.Kind, "error", err)
		l.id = ""
	}
}

// finish sends the answer of a turn. It replaces the working message, when
// there is one, and returns the IDs of the messages that hold the answer.
func (c *Connectors) finish(b connectors.Binding, token string, m contract.ConnectorMessage, l *liveReply, answer string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: b.Kind, Token: token, Chat: m.Chat, Thread: m.Thread, Text: answer, MessageID: l.id}
	resp, err := c.o.Transport.Call(ctx, b.AddonID, req)
	if err == nil && resp.Error != nil {
		err = resp.Error
	}
	if err != nil {
		c.log.Warn("studio: could not send a connector answer", "binding", b.ID, "kind", b.Kind, "error", err)
		return nil
	}
	var ids []string
	for _, id := range []string{l.id, resp.MessageID} {
		if id != "" && (len(ids) == 0 || ids[0] != id) {
			ids = append(ids, id)
		}
	}
	return ids
}
