package session

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The text of an assistant message is stored as one line when the message ends.
// A process that dies in the middle of a long answer would lose it all, and the
// answer is the part of a turn that takes longest to write. While a message
// streams, its text so far is saved into the chat record (PartialStore) at most
// every partialEvery, or when partialBytes of new text have come, and cleared
// when the line is stored. A turn that goes on after a death stores the saved
// text as a line first, marked NoteCut, and tells the model where its message
// stopped. A Store without PartialStore keeps nothing, and a message that ends
// in under partialEvery never writes.
const (
	partialEvery = 2 * time.Second
	partialBytes = 4 << 10
	// partialFloor keeps a fast stream from writing more than a few times a second.
	partialFloor = 250 * time.Millisecond
)

// NoteCut is the Note of an assistant line that holds the text of a message
// that a death cut off, kept as it was when it was last saved.
const NoteCut = "cut"

// partialDueLocked says whether the text so far is to be saved now, and gives
// it. The caller holds c.mu and has just added to c.text.
func (c *Chat) partialDueLocked(now time.Time) (bool, string) {
	if _, ok := c.m.o.Store.(PartialStore); !ok {
		return false, ""
	}
	if c.partialAt.IsZero() {
		c.partialAt, c.partialLen = now, 0
	}
	grown, since := c.text.Len()-c.partialLen, now.Sub(c.partialAt)
	if grown <= 0 || since < partialFloor || (since < partialEvery && grown < partialBytes) {
		return false, ""
	}
	c.partialAt, c.partialLen = now, c.text.Len()
	return true, c.text.String()
}

// savePartial stores the text so far. A failure is logged and does not stop the
// turn: the save is a safety net, and the next one retries.
func (c *Chat) savePartial(ctx context.Context, turn int, text string) {
	ps, ok := c.m.o.Store.(PartialStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ps.SavePartial(ctx, c.id, turn, text); err != nil {
		c.m.log.Warn("session: could not save the text of a message being written", "chat", c.id, "error", err)
		return
	}
	c.mu.Lock()
	c.partialSaved = true
	c.mu.Unlock()
}

// clearPartial runs when a message is stored as a line: its saved text, if any,
// is no longer needed. The next message starts its own timing.
func (c *Chat) clearPartial(ctx context.Context) {
	c.mu.Lock()
	saved := c.partialSaved
	c.partialAt, c.partialLen, c.partialSaved = time.Time{}, 0, false
	c.mu.Unlock()
	if !saved {
		return
	}
	ps, ok := c.m.o.Store.(PartialStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ps.SavePartial(ctx, c.id, 0, ""); err != nil {
		c.m.log.Warn("session: could not clear the saved text of a message", "chat", c.id, "error", err)
	}
}

// settleCut runs when a turn starts. A saved text that is still there belongs
// to a turn that died: it becomes an assistant line, marked NoteCut, so the
// transcript shows what was written and the next turn comes after it. It returns
// that text, so that a resume can tell the model, or "" when there was none (or
// its line was stored already: a death between the line and the clear). It also
// returns the transcript, which is read only when the text is there or a resume
// (needHistory) will look for calls whose outcome is not known; a transcript
// that cannot be read gives none, and the resume note is the plain one.
func (c *Chat) settleCut(ctx context.Context, needHistory bool) (cut string, history []Entry) {
	var turn int
	var text string
	if ps, ok := c.m.o.Store.(PartialStore); ok {
		var err error
		if turn, text, err = ps.TakePartial(ctx, c.id); err != nil {
			c.m.log.Warn("session: could not read the saved text of a cut message", "chat", c.id, "error", err)
			text = ""
		}
	}
	text = strings.TrimSpace(text)
	if text == "" && !needHistory {
		return "", nil
	}
	history, err := c.History(ctx)
	if err != nil {
		c.m.log.Warn("session: could not read the transcript to settle a turn", "chat", c.id, "error", err)
	}
	if text == "" || storedAlready(history, text) {
		return "", history
	}
	c.appendEntry(ctx, Entry{Turn: turn, Role: RoleAssistant, Text: text, Note: NoteCut})
	return text, history
}

// storedAlready says whether the last message of the transcript is the text: a
// death between storing a line and clearing its saved copy leaves both.
func storedAlready(entries []Entry, text string) bool {
	for i := len(entries) - 1; i >= 0; i-- {
		switch entries[i].Role {
		case RoleCheckpoint, RoleReasoning, RoleToolIntent, RoleFailure:
			continue
		case RoleAssistant:
			return strings.HasPrefix(entries[i].Text, text)
		}
		return false
	}
	return false
}

// cutNote is the sentence that tells the model where its message stopped.
func cutNote(text string) string {
	const keep = 1500
	tail := text
	if len(tail) > keep {
		tail = "..." + tail[len(tail)-keep:]
	}
	return fmt.Sprintf("Your last message was cut off. The person already read this much of it, ending: %q. "+
		"Go on from there without writing it again.", tail)
}
