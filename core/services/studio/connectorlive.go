package studio

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// What a sender sees while a turn runs.
const (
	// cursorMark ends a live preview: the answer is still being written.
	cursorMark = " ▉"
	// typingEvery is how often typing is shown again: a platform shows it
	// for about five seconds.
	typingEvery = 4 * time.Second
	// maxPreviewRunes is how long a live preview is, cursor included: the
	// end of the answer, or of the reasoning, that fits.
	maxPreviewRunes = 3000
	// draftHeartbeat is how often a draft that shows nothing new is sent
	// again: a platform drops a draft that is not renewed.
	draftHeartbeat = 25 * time.Second
	// maxToolsShown is how many tool names a progress message lists.
	maxToolsShown = 5
	// editTimeout bounds one edit: a slow edit is skipped, not waited for.
	editTimeout = 5 * time.Second
	// answerTimeout bounds the sending of an answer. It is long, because the
	// add-on waits for the wait a platform asks for before it gives up the
	// edit of the preview.
	answerTimeout = 2 * time.Minute
	// maxEditFails is how many previews in a row may fail before the reply
	// stops editing its working message. The message stays: the answer still
	// replaces it, or deletes it.
	maxEditFails = 3
	// maxEditBackoff bounds the wait after a failed preview.
	maxEditBackoff = 30 * time.Second
)

// liveReply is what a sender sees of one turn in one conversation: a working
// message that the reply edits, or in the stream mode of a direct
// conversation a draft. One reply goroutine owns it, so it needs no lock.
type liveReply struct {
	mode string
	// v speaks the fixed words of the preview in the sender's language.
	v hostmsg.Voice
	// id is the working message. It is "" when the mode is off, while
	// drafts show the preview, or when the message could not be sent. It
	// stays set when an edit fails: the answer must replace that message, or
	// delete it, so that no preview with a cursor stays in the conversation.
	id string
	// fails counts the previews that failed in a row; muted tells that the
	// reply no longer edits id.
	fails int
	muted bool
	// draft names the draft that shows the preview; 0 when there is none.
	// A platform that refuses a draft gets a working message instead, for
	// the rest of the turn.
	draft int64
	// drafted is when the draft was last sent, for the heartbeat.
	drafted time.Time
	// shown is what the working message or the draft shows now.
	shown string
	tools []string
	// answer is the text of the answer so far, and status the latest tool
	// line, for the stream mode. Reasoning is never shown to the sender.
	answer strings.Builder
	status string
}

// livePace is how a binding shows previews: how often, and whether its
// platform has drafts. Its add-on declares both with its kind.
type livePace struct {
	every  time.Duration
	drafts bool
}

// draftSeq numbers drafts. A platform animates the drafts of one ID, so each
// turn takes a new one.
var draftSeq atomic.Int64

// nextDraftID is a new draft ID. It is never zero, which a platform refuses.
func nextDraftID() int64 {
	for {
		if id := draftSeq.Add(1); id != 0 {
			return id
		}
	}
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
		l.status = l.v.T("live.using", "tool", ev.Tool.Name)
	case session.KindText:
		l.answer.WriteString(ev.Text)
	}
	// session.KindReasoning is not shown: the reasoning of the model is not
	// the answer, and a sender must never read it.
}

// view is what the working message should show now.
func (l *liveReply) view() string {
	switch l.mode {
	case connectors.ReplyStream:
		// The answer once it starts; before it, the tool.
		t := strings.TrimSpace(l.answer.String())
		if t == "" {
			t = strings.TrimSpace(l.status)
		}
		if t == "" {
			t = l.v.T("live.working")
		}
		return tailRunes(t, maxPreviewRunes-utf8.RuneCountInString(cursorMark)) + cursorMark
	case connectors.ReplyProgress:
		if len(l.tools) > 0 {
			return l.v.T("live.working") + "\n• " + strings.Join(l.tools, "\n• ")
		}
	}
	return l.v.T("live.working")
}

// tailRunes keeps the last n runes of s.
func tailRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[len(r)-n:])
}

// livePaceOf reads how the kind of a binding shows previews. A kind that
// declares nothing gets EditEvery and no drafts.
func (c *Connectors) livePaceOf(b connectors.Binding) livePace {
	p := livePace{every: c.o.EditEvery}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return p
	}
	if k, ok := kindFor(kinds, b); ok {
		if k.PreviewMs > 0 {
			p.every = time.Duration(k.PreviewMs) * time.Millisecond
		}
		p.drafts = k.Drafts
	}
	return p
}

// startLive shows that the bot works: typing at once, and unless the
// binding's mode is off the first preview. In the stream mode of a direct
// conversation on a platform with drafts it is a draft, else a working
// message.
func (c *Connectors) startLive(b connectors.Binding, token string, m contract.ConnectorMessage, pace livePace) *liveReply {
	l := &liveReply{mode: b.ReplyMode(), v: c.voice(context.Background(), b, m.SenderID, m.Chat)}
	// Typing goes after the working message, which it names: a platform
	// without typing would otherwise post a working message of its own.
	defer func() { c.typing(b, token, m, l.id) }()
	if l.mode == connectors.ReplyOff {
		return l
	}
	v := l.view()
	// The first preview goes now, whatever the budget, and counts: the next
	// one waits a whole interval.
	c.holdEdits(b.ID, pace.every)
	if l.mode == connectors.ReplyStream && m.Direct && pace.drafts {
		l.draft = nextDraftID()
		if c.showDraft(b, token, m, l, v) {
			return l
		}
	}
	c.workingMessage(b, token, m, l, v)
	return l
}

// workingMessage sends the working message that the reply edits.
func (c *Connectors) workingMessage(b connectors.Binding, token string, m contract.ConnectorMessage, l *liveReply, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.o.Transport.Call(ctx, BindingConnectorScope(b), contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: b.Kind, Token: token, Chat: m.Chat, Thread: m.Thread, Text: text})
	if err == nil && resp.Error != nil {
		err = resp.Error
	}
	if err != nil {
		c.log.Warn("studio: could not send a connector working message", "binding", b.ID, "kind", b.Kind, "error", err)
		return
	}
	l.id, l.shown = resp.MessageID, text
}

// showDraft sends the draft of l with text. It reports false when the
// platform refuses drafts: l then has none, and the caller sends a working
// message. A rate-limited draft stays: the next tick sends it again.
func (c *Connectors) showDraft(b connectors.Binding, token string, m contract.ConnectorMessage, l *liveReply, text string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), editTimeout)
	defer cancel()
	err := c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpDraft, Token: token, Chat: m.Chat, Thread: m.Thread, DraftID: l.draft, Text: text})
	var ce *contract.ConnectorError
	switch {
	case err == nil:
		l.shown, l.drafted = text, time.Now()
		return true
	case errors.As(err, &ce) && ce.Code == contract.ConnectorRateLimited:
		c.holdEdits(b.ID, time.Duration(max(ce.RetryAfter, 1))*time.Second)
		return true
	}
	c.log.Debug("studio: the platform refused a draft; the preview goes in a working message", "binding", b.ID, "kind", b.Kind, "error", err)
	l.draft = 0
	return false
}

// typing shows typing in the conversation. id names the working message, so
// a platform without typing does not post one of its own.
func (c *Connectors) typing(b connectors.Binding, token string, m contract.ConnectorMessage, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpTyping, Token: token, Chat: m.Chat, Thread: m.Thread, MessageID: id})
}

// mayEdit spends the preview budget of a binding: at most one edit or draft
// each every, across all its conversations. A refused preview stays pending
// and goes at a later tick, with what is new by then, so the previews in
// between are coalesced.
func (c *Connectors) mayEdit(binding string, every time.Duration) bool {
	now := time.Now()
	c.emu.Lock()
	defer c.emu.Unlock()
	if now.Before(c.nextEdit[binding]) {
		return false
	}
	c.nextEdit[binding] = now.Add(every)
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

// flush shows the preview when it should show something new and the
// binding's budget allows. A draft that shows nothing new is sent again after
// the heartbeat, so the platform keeps it. A draft the platform refuses
// becomes a working message. A failed edit backs off and, after a few in a
// row, stops the previews; the working message stays for the answer to
// replace. A rate-limited edit only waits.
func (c *Connectors) flush(b connectors.Binding, token string, m contract.ConnectorMessage, l *liveReply, pace livePace) {
	if (l.id == "" || l.muted) && l.draft == 0 {
		return
	}
	v := l.view()
	beat := l.draft != 0 && time.Since(l.drafted) >= c.o.DraftHeartbeat
	if (v == l.shown && !beat) || !c.mayEdit(b.ID, pace.every) {
		return
	}
	if l.draft != 0 {
		if !c.showDraft(b, token, m, l, v) {
			c.workingMessage(b, token, m, l, v)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), editTimeout)
	defer cancel()
	err := c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpEdit, Token: token, Chat: m.Chat, Thread: m.Thread, MessageID: l.id, Text: v})
	var ce *contract.ConnectorError
	switch {
	case err == nil:
		l.shown, l.fails = v, 0
	case errors.As(err, &ce) && ce.Code == contract.ConnectorRateLimited:
		c.holdEdits(b.ID, time.Duration(max(ce.RetryAfter, 1))*time.Second)
	default:
		// The working message stays: it is not given up on one failure, and
		// the answer replaces it or deletes it. The preview backs off, and
		// stops after a few failures in a row.
		l.fails++
		c.log.Debug("studio: a connector edit failed", "binding", b.ID, "kind", b.Kind, "failures", l.fails, "error", err)
		if l.fails >= maxEditFails {
			l.muted = true
		} else {
			c.holdEdits(b.ID, min(pace.every<<l.fails, maxEditBackoff))
		}
	}
}

// finish sends the answer of a turn. It replaces the working message, when
// there is one; when that cannot be done, the add-on deletes the working
// message after it posts the answer. If the answer cannot be sent at all, the
// working message becomes a short failure line, or is deleted, so that no
// preview with a cursor stays. It returns the IDs of the messages that hold
// the answer.
func (c *Connectors) finish(b connectors.Binding, token string, m contract.ConnectorMessage, l *liveReply, answer string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), answerTimeout)
	defer cancel()
	req := contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: b.Kind, Token: token, Chat: m.Chat, Thread: m.Thread, Text: answer, MessageID: l.id}
	resp, err := c.o.Transport.Call(ctx, BindingConnectorScope(b), req)
	if err == nil && resp.Error != nil {
		err = resp.Error
	}
	if err != nil {
		c.log.Warn("studio: could not send a connector answer", "binding", b.ID, "kind", b.Kind, "error", err)
		c.dropPreview(b, token, m, l)
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

// dropPreview takes the preview out of the conversation when the answer could
// not be sent: it shows a short failure line, and when even that fails it
// deletes the message. A platform that can do neither leaves the message.
func (c *Connectors) dropPreview(b connectors.Binding, token string, m contract.ConnectorMessage, l *liveReply) {
	if l.id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), editTimeout*2)
	defer cancel()
	err := c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpEdit, Token: token, Chat: m.Chat, Thread: m.Thread, MessageID: l.id, Text: l.v.T("failed")})
	if err == nil {
		return
	}
	if err := c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpDelete, Token: token, Chat: m.Chat, Thread: m.Thread, MessageID: l.id}); err != nil {
		c.log.Debug("studio: could not remove a connector preview", "binding", b.ID, "kind", b.Kind, "error", err)
	}
}
