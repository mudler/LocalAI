package session

import (
	"context"
	"errors"
	"fmt"
)

// Errors of the context commands.
var (
	// ErrCannotCompact means the harness of the chat cannot compact its
	// context on request.
	ErrCannotCompact = errors.New("session: this chat cannot compact its context")
	// ErrNothingToCompact means the conversation is too short to compact: the
	// harness keeps the last messages as they are and found nothing before them.
	ErrNothingToCompact = errors.New("session: the conversation is too short to compact")
)

// Compactor is a HarnessSession that can shrink the model's context on
// request, the way it does by itself when the context fills up. Compact
// returns the size of the context, in tokens, before and after; equal sizes
// mean there was nothing to compact. The state that Export returns after it
// is the compacted one.
type Compactor interface {
	Compact(ctx context.Context) (before, after int, err error)
}

// ModelRole reports the roles of the transcript the model saw: the person's
// messages, the answers and the tool calls. Checkpoints, reasoning and the
// markers of the context commands are for display only.
func ModelRole(role string) bool {
	return role == RoleUser || role == RoleAssistant || role == RoleTool
}

// SinceCleared returns the entries after the last RoleCleared entry: the part
// of the transcript the model may see. Without such an entry it is entries.
func SinceCleared(entries []Entry) []Entry {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == RoleCleared {
			return entries[i+1:]
		}
	}
	return entries
}

// CompactedText is the text of a RoleCompacted entry.
func CompactedText(before, after int) string {
	return fmt.Sprintf("Compacted: %d tokens to %d", before, after)
}

// ParseCompacted reads the sizes back from the text of a RoleCompacted entry.
func ParseCompacted(text string) (before, after int, ok bool) {
	n, err := fmt.Sscanf(text, "Compacted: %d tokens to %d", &before, &after)
	return before, after, err == nil && n == 2
}

// Compact shrinks the model's context now, with the summary the harness makes
// when the context fills up. The chat must be idle: it is ErrTurnRunning while
// a turn runs, ErrCannotCompact when the harness has no way to do it and
// ErrNothingToCompact when the conversation is too short. On success the
// compacted state is saved, a RoleCompacted entry marks the place in the
// transcript and a compacted event carries the sizes.
func (c *Chat) Compact(ctx context.Context) (before, after int, err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, 0, ErrClosed
	}
	if c.running || c.renaming != nil {
		c.mu.Unlock()
		return 0, 0, ErrTurnRunning
	}
	hs := c.hs
	// The chat counts as running while it compacts, so that no turn starts on
	// the state that is being replaced.
	c.running, c.stopped = true, false
	c.done = make(chan struct{})
	done := c.done
	turn := c.turn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.endLoopLocked(done)
		c.mu.Unlock()
		c.touch()
	}()
	comp, ok := hs.(Compactor)
	if hs == nil || !ok {
		return 0, 0, ErrCannotCompact
	}
	if before, after, err = comp.Compact(ctx); err != nil {
		return before, after, fmt.Errorf("session: compact: %w", err)
	}
	if after >= before {
		return before, after, ErrNothingToCompact
	}
	state, err := hs.Export()
	if err != nil {
		return before, after, fmt.Errorf("session: export the compacted state: %w", err)
	}
	c.mu.Lock()
	c.rec.HarnessState = state
	c.stateSeq++
	c.mu.Unlock()
	if err := c.persist(ctx); err != nil {
		return before, after, err
	}
	c.appendEntry(ctx, Entry{Turn: turn, Role: RoleCompacted, Text: CompactedText(before, after)})
	c.emit(Event{Kind: KindCompacted, Turn: turn, Before: before, After: after})
	return before, after, nil
}
