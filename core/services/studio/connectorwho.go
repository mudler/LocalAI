package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// turnWho is what a connector turn knows about the message it carries, for the
// transcript: who wrote it and, for the first turn of a chat that a thread
// started, the block of earlier thread messages. It is display data. It rides
// the context from the connector manager to the place that builds the session
// message, and over the wire to a chat worker (workerctl.ChatStartRequest).
type turnWho struct {
	Sender *session.Sender
	// Thread is the block that the turn's directive carries. Only the
	// directive reaches the model; the transcript keeps Thread as a display
	// line of its own.
	Thread      string
	ThreadLines int
	ThreadCut   bool
}

type whoCtxKey struct{}

func withTurnWho(ctx context.Context, w turnWho) context.Context {
	return context.WithValue(ctx, whoCtxKey{}, w)
}

func turnWhoFrom(ctx context.Context) (turnWho, bool) {
	w, ok := ctx.Value(whoCtxKey{}).(turnWho)
	return w, ok
}

func senderWire(s *session.Sender) *workerctl.ChatSender {
	if s == nil {
		return nil
	}
	return &workerctl.ChatSender{ID: s.ID, Name: s.Name, Username: s.Username, Standing: s.Standing}
}

func senderOf(w *workerctl.ChatSender) *session.Sender {
	if w == nil {
		return nil
	}
	return &session.Sender{ID: w.ID, Name: w.Name, Username: w.Username, Standing: w.Standing}
}

// sameSender reports whether a and b name the same sender; two unknown
// senders are not the same.
func sameSender(a, b *session.Sender) bool {
	return a != nil && b != nil && a.ID != "" && a.ID == b.ID
}

// mergeSender is the sender of a message made of several waiting ones: the
// common sender, or nil when they differ or one is unknown. A line that joins
// words of different people names none of them.
func mergeSender(senders []*session.Sender) *session.Sender {
	if len(senders) == 0 {
		return nil
	}
	first := senders[0]
	for _, s := range senders[1:] {
		if !sameSender(first, s) {
			return nil
		}
	}
	return first
}
