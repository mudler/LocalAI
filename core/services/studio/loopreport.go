package studio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// A loop may carry a report: a channel of one of the owner's connections where
// it says how it ended. start_loop sets it, after the same destination checks
// that a trigger passes; the person sees it on the loop. Each delivery checks
// the destination again, because the loop may run for days.

// reportTimeout bounds one delivery of a loop report.
const reportTimeout = 30 * time.Second

// reportArgs is the report object of start_loop.
type reportArgs struct {
	Connection string `json:"connection"`
	Channel    string `json:"channel"`
	Mode       string `json:"mode"`
}

// checkReport checks the report that start_loop asks for and returns the one to
// store, with the channel as an ID. Nil means no report: none was asked for, or
// its mode is never. The destination must be one of the owner's connections
// that can receive messages, with an approved channel, as for a trigger.
func (h *Loops) checkReport(ctx context.Context, owner, agentID string, in *reportArgs) (*loops.Report, error) {
	if in == nil {
		return nil, nil
	}
	switch mode := strings.TrimSpace(in.Mode); mode {
	case loops.ReportNever:
		return nil, nil
	case loops.ReportOnDone, loops.ReportOnProblem:
		conns := h.svc.connectors
		if conns == nil {
			return nil, errors.New("report: connections to messaging apps are off on this server, so a loop cannot report there; leave report out")
		}
		b, ch, err := conns.checkDestination(ctx, owner, agentID, in.Connection, in.Channel)
		if err != nil {
			return nil, fmt.Errorf("report: %w", err)
		}
		route := triggers.Route{Target: triggers.TargetChannel, Connection: b.ID, Channel: ch.ID}
		rep := loops.Report{Connection: b.ID, Channel: ch.ID, Mode: mode}
		if err := errors.Join(route.Validate(), rep.Validate()); err != nil {
			return nil, fmt.Errorf("report: %w", err)
		}
		return &rep, nil
	default:
		return nil, fmt.Errorf("report.mode must be %q, %q or %q", loops.ReportOnDone, loops.ReportOnProblem, loops.ReportNever)
	}
}

// ReportView is where a loop reports, in the words of the person: the names
// that the owner gave. The names are empty when the connection or the channel is
// gone.
type ReportView struct {
	ConnectionName, ChannelLabel string
}

// ReportOf names the destination of the report of a loop.
func (h *Loops) ReportOf(ctx context.Context, l loops.Loop) ReportView {
	c := h.svc.connectors
	if l.Report == nil || c == nil {
		return ReportView{}
	}
	b, ok := c.ownBinding(ctx, l.Owner, l.Report.Connection)
	if !ok {
		return ReportView{}
	}
	v := ReportView{ConnectionName: b.Name}
	if ch, found := b.FindChannel(l.Report.Channel); found {
		v.ChannelLabel = ch.Label
	}
	return v
}

// loopLabel is the start of the prompt of a loop, for a line that others read.
func loopLabel(l loops.Loop) string {
	first, _, _ := strings.Cut(strings.TrimSpace(l.Prompt), "\n")
	if r := []rune(first); len(r) > 60 {
		first = string(r[:59]) + "…"
	}
	return first
}

// report tells the owner of the loop how it ended, when the loop has a report of
// that mode. It runs in the background, so a slow platform holds up neither
// the pass of the host nor the tool that ended the loop. A failure is logged and
// audited; the loop keeps its state, which the person sees on it.
func (h *Loops) report(l loops.Loop, mode, text string) {
	if !l.Reports(mode) {
		return
	}
	h.mu.Lock()
	if h.shut {
		h.mu.Unlock()
		return
	}
	h.wg.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.wg.Done()
		h.deliverReport(l, text)
	}()
}

func (h *Loops) deliverReport(l loops.Loop, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
	defer cancel()
	fail := func(why string, err error) {
		h.log.Warn("studio: a loop report was not delivered", "loop", l.ID, "chat", l.ChatID, "why", why, "error", err)
	}
	conns := h.svc.connectors
	if conns == nil {
		fail("connections are off", nil)
		return
	}
	rec, err := h.svc.getChatEffective(ctx, l.Owner, l.ChatID)
	if err != nil {
		fail("the chat is gone", err)
		return
	}
	text = "Loop \"" + loopLabel(l) + "\": " + text
	queued, err := conns.sendToOwner(ctx, ownerSend{Owner: l.Owner, Agent: rec.AgentID, Connection: l.Report.Connection, Channel: l.Report.Channel,
		Text: text, Kind: SendLoopReport, OnFail: func(err error) { fail("it could not be sent after it waited", err) }})
	switch {
	case err != nil:
		fail("the destination was refused or the send failed", err)
	case queued:
		h.log.Info("studio: a loop report waits in the outbox", "loop", l.ID)
	}
}
