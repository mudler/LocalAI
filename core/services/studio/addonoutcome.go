package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The outcome of a tool call of an add-on is written to the log of the host
// and kept for the settings page of the connection. Both hold names and codes
// only: never the arguments, the result, a token or a note.

// outcomeQuiet is how long an identical line is held back, and outcomeKeep how
// many distinct lines the limiter remembers.
const (
	outcomeQuiet = 30 * time.Second
	outcomeKeep  = 256
)

// outcomeLimiter drops a log line that repeats within outcomeQuiet, so a loop
// that fails the same way many times writes one line, and the next line after
// the quiet time says how many it held back.
type outcomeLimiter struct {
	mu   sync.Mutex
	seen map[string]*outcomeSeen
}

type outcomeSeen struct {
	at         time.Time
	suppressed int
}

// allow reports whether the line may be written now, and how many identical
// lines were held back since the last one.
func (l *outcomeLimiter) allow(key string, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]*outcomeSeen{}
	}
	if e, ok := l.seen[key]; ok && now.Sub(e.at) < outcomeQuiet {
		e.suppressed++
		return false, 0
	} else if ok {
		n := e.suppressed
		e.at, e.suppressed = now, 0
		return true, n
	}
	if len(l.seen) >= outcomeKeep {
		for k, e := range l.seen {
			if now.Sub(e.at) >= outcomeQuiet {
				delete(l.seen, k)
			}
		}
		if len(l.seen) >= outcomeKeep {
			clear(l.seen)
		}
	}
	l.seen[key] = &outcomeSeen{at: now}
	return true, 0
}

// outcomeRun wraps the call of an add-on tool: it puts an addontools.Outcome in
// the context, runs the call, and writes one log line and one latest-decisions
// event with how it ended. It is the outermost wrapper, so a call that a gate
// stopped is recorded too.
func (s *Service) outcomeRun(rec session.ChatRecord, a workspace.Actor, d AddonTool, run func(context.Context, json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		ctx, out := addontools.WithOutcome(ctx)
		if rec.Origin == session.OriginConnector {
			ctx = addontools.WithSurface(ctx, addontools.SurfaceMessaging)
		}
		start := s.now()
		text, err := run(ctx, args)
		if h := out.HeldProposal(); h != nil && err == nil && rec.Origin == session.OriginConnector && s.connectors != nil {
			// The write waits. When a paired sender can answer in the chat, the
			// host asks there and tells the model so; otherwise the text of the
			// tool stands and the owner answers in the web UI.
			if note, ok := s.connectors.holdInChat(ctx, rec, a.ChatID, *h); ok {
				text = note
				out.Finish(addontools.OutcomeHeld, reasonWaitingInChat, 0)
			}
		}
		s.recordOutcome(rec, a.ChatID, d, out.Data(), err, s.now().Sub(start))
		return text, err
	}
}

// senderKind says who the call is for: the owner in the web UI, how the sender
// of a messaging turn was let in, or the unattended run that started the chat.
func (s *Service) senderKind(rec session.ChatRecord, chat string) (kind string, g turnGrant, held bool) {
	switch rec.Origin {
	case session.OriginTrigger:
		return "trigger", turnGrant{}, false
	case session.OriginJob:
		return "job", turnGrant{}, false
	case session.OriginConnector:
		g, held = s.access.get(chat)
		if !held || g.Sender == "" {
			return "unknown", g, held
		}
		return g.Sender, g, held
	}
	return "owner", turnGrant{}, false
}

func (s *Service) recordOutcome(rec session.ChatRecord, chat string, d AddonTool, o addontools.OutcomeData, err error, took time.Duration) {
	if o.Result == "" {
		o.Result, o.Reason, o.Status = addontools.Classify(addontools.ToolResult{}, err)
	}
	tool := d.Tool
	if tool == "" {
		tool = d.Name
	}
	kind := o.Kind
	if kind == "" {
		kind = "-"
	}
	sender, g, _ := s.senderKind(rec, chat)
	origin := rec.Origin
	if origin == "" {
		origin = "studio"
	}
	now := s.now()
	reason := o.Reason
	if reason == "" {
		reason = "-"
	}
	line := fmt.Sprintf("addon=%s tool=%s kind=%s binding=%s resource=%s chat=%s sender=%s outcome=%s reason=%s status=%s",
		d.AddonID, tool, kind, dash(o.Binding), dash(o.Resource), origin, sender, o.Result, reason, statusText(o.Status))
	if ok, held := s.outcomes.allow(line, now); ok {
		attrs := []any{"addon", d.AddonID, "tool", tool, "kind", kind, "binding", dash(o.Binding), "resource", dash(o.Resource), "chat", origin,
			"sender", sender, "outcome", o.Result, "reason", reason, "status", statusText(o.Status), "ms", took.Milliseconds()}
		if held > 0 {
			attrs = append(attrs, "repeated", held)
		}
		s.log.Info("studio: addon tool", attrs...)
	}
	// The settings page of the connection that was used, and of the messaging
	// connection that the sender came through, show the same event.
	e := AccessEvent{At: now.UTC(), Kind: g.Kind, Senders: g.Senders, Roles: g.Roles, Addon: d.AddonID, Tool: tool,
		Outcome: o.Result, Reason: o.Reason, Status: o.Status}
	for _, id := range uniqueIDs(o.Binding, g.Binding) {
		e.Binding = id
		s.access.recordTool(e)
	}
}

func uniqueIDs(ids ...string) []string {
	var out []string
	for _, id := range ids {
		if id == "" {
			continue
		}
		dup := false
		for _, o := range out {
			dup = dup || o == id
		}
		if !dup {
			out = append(out, id)
		}
	}
	return out
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func statusText(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}
