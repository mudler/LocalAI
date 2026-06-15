package pii

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

// StreamFilter applies the regex PII tier to a streaming response,
// chunk by chunk, with a buffered-emit invariant: for any active
// pattern with bounded max-length L, the filter never emits the
// trailing L-1 characters of the cumulative input until either
//
//   (a) more text arrives that disambiguates the boundary, or
//   (b) the stream closes (Drain).
//
// That keeps the redactor honest across chunk splits — an email
// arriving as "alice@" + "example.com" still masks the same way as
// "alice@example.com" arriving in one piece.
//
// Action handling in stream mode differs from the request-side
// middleware. Earlier chunks of the response are already on the wire
// by the time later chunks are scanned, so a "block" can't actually
// reject the request. We remap block → mask for redaction purposes
// while still recording PIIEvent rows with action="block" so audits
// surface the original intent ("the model would have leaked X here,
// suppressed in flight"). allow on the output side is a no-op — the
// text is left intact, matching its request-side detect-and-log
// behaviour.
//
// StreamFilter is NOT safe for concurrent use across goroutines; one
// instance per response stream.
type StreamFilter struct {
	redactor      *Redactor
	maskOverrides map[string]Action // block → mask map used for redaction
	auditActions  map[string]Action // original action per pattern, used for events
	store         EventStore
	correlationID string
	userID        string
	holdLen       int
	buffer        strings.Builder
	emittedBytes  int
}

// NewStreamFilter constructs a per-response filter. modelOverrides is
// the per-model action override map (same shape the request-side
// middleware uses); it can be nil when the model only accepts global
// defaults.
//
// store may be nil — events are then computed but not persisted, which
// is what the chat handler does when --disable-stats is set.
func NewStreamFilter(redactor *Redactor, modelOverrides map[string]Action, store EventStore, correlationID, userID string) *StreamFilter {
	if redactor == nil {
		return &StreamFilter{}
	}

	patterns := redactor.Patterns()

	// auditActions: the action we *would* have applied if this match
	// occurred on the request side. Honours the per-model override.
	auditActions := make(map[string]Action, len(patterns))
	for _, p := range patterns {
		auditActions[p.ID] = p.Action
	}
	for id, action := range modelOverrides {
		auditActions[id] = action
	}

	// maskOverrides: the action we actually apply to the stream. Same
	// as auditActions, but with every block remapped to mask.
	maskOverrides := make(map[string]Action, len(auditActions))
	for id, action := range auditActions {
		if action == ActionBlock {
			maskOverrides[id] = ActionMask
		} else {
			maskOverrides[id] = action
		}
	}

	return &StreamFilter{
		redactor:      redactor,
		maskOverrides: maskOverrides,
		auditActions:  auditActions,
		store:         store,
		correlationID: correlationID,
		userID:        userID,
		holdLen:       redactor.MaxPatternLength() - 1,
	}
}

// Push appends new text to the filter's buffer and returns the prefix
// safe to emit downstream — the cumulative input minus a tail of
// holdLen characters that might still be the start of a longer match.
// Returned text has masks already applied.
//
// Returns an empty string when not enough text has arrived to clear
// the hold window.
func (sf *StreamFilter) Push(text string) string {
	if sf.redactor == nil || sf.holdLen <= 0 {
		return text
	}
	sf.buffer.WriteString(text)
	bufStr := sf.buffer.String()
	n := len(bufStr)

	if n <= sf.holdLen {
		return ""
	}

	emitBoundary := n - sf.holdLen

	// Scan the entire buffer. A match whose start is before the
	// boundary but whose end runs past it crosses the window — pull
	// the boundary back to match.start so the pattern stays whole in
	// the buffer for the next Push to scan again.
	full := sf.redactor.RedactWithOverrides(bufStr, sf.maskOverrides)
	for _, span := range full.Spans {
		if span.Start < emitBoundary && span.End > emitBoundary {
			emitBoundary = span.Start
		}
	}

	// holdLen is byte-sized but a chunk boundary may land mid-codepoint.
	// Snap back to the nearest rune start so neither the emitted prefix
	// nor the retained tail contains a split codepoint — otherwise the
	// next regex scan over an invalid-UTF-8 prefix could mis-match.
	for emitBoundary > 0 && emitBoundary < n && !utf8.RuneStart(bufStr[emitBoundary]) {
		emitBoundary--
	}

	if emitBoundary <= 0 {
		return ""
	}

	emitted := sf.applyAndEmit(bufStr[:emitBoundary])
	sf.buffer.Reset()
	sf.buffer.WriteString(bufStr[emitBoundary:])
	return emitted
}

// Drain emits whatever's left in the buffer with all matches applied.
// Call exactly once when the stream closes — repeat calls return the
// empty string.
func (sf *StreamFilter) Drain() string {
	if sf.redactor == nil {
		return sf.buffer.String()
	}
	bufStr := sf.buffer.String()
	if bufStr == "" {
		return ""
	}
	emitted := sf.applyAndEmit(bufStr)
	sf.buffer.Reset()
	return emitted
}

// applyAndEmit runs the redactor over a committed-for-emit fragment,
// substitutes mask/block placeholders inline, and records one
// PIIEvent per matched span (with the audit action, not the masked
// one). ByteOffset is referenced to the cumulative emitted output so
// admins can correlate event positions against the streamed body.
func (sf *StreamFilter) applyAndEmit(fragment string) string {
	res := sf.redactor.RedactWithOverrides(fragment, sf.maskOverrides)
	output := res.Redacted

	if len(res.Spans) > 0 {
		now := time.Now().UTC()
		for _, span := range res.Spans {
			ev := PIIEvent{
				ID:            newStreamEventID(),
				CorrelationID: sf.correlationID,
				UserID:        sf.userID,
				Direction:     DirectionOut,
				PatternID:     span.Pattern,
				ByteOffset:    sf.emittedBytes + span.Start,
				Length:        span.End - span.Start,
				HashPrefix:    span.HashPrefix,
				Action:        sf.auditActions[span.Pattern],
				CreatedAt:     now,
			}
			if sf.store != nil {
				_ = sf.store.Record(context.Background(), ev)
			}
		}
	}

	sf.emittedBytes += len(fragment)
	return output
}

func newStreamEventID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "pii_" + hex.EncodeToString(b[:])
}
