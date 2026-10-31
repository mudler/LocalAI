package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

const (
	scheduleNextName = "schedule_next"
	loopDoneName     = "loop_done"
)

const scheduleNextDescription = "Say when the self-paced loop of this chat runs again. Call it once at the end of a loop run, " +
	"when the task needs another run later: after is a duration such as 90s, 10m or 2h (at least the minimum the server allows, at most 24h), " +
	"and reason is one short line about what you wait for, which the person sees. " +
	"If you call neither this nor loop_done, the loop stops after this run."

const scheduleNextSchema = `{"type":"object","properties":{` +
	`"after":{"type":"string","description":"How long to wait before the next run, such as 90s, 10m or 2h."},` +
	`"reason":{"type":"string","maxLength":300,"description":"One short line about what you wait for."}},` +
	`"required":["after"]}`

const loopDoneDescription = "Stop the self-paced loop of this chat because its task is complete. " +
	"summary is one short sentence for the person about what the loop did."

const loopDoneSchema = `{"type":"object","properties":{` +
	`"summary":{"type":"string","maxLength":300,"description":"One short sentence about what the loop did."}}}`

// errNoLoopRun means a loop tool was called outside a run of a self-paced loop.
var errNoLoopRun = errors.New("no run of a self-paced loop goes on in this chat; use this tool only in a loop run")

// loopTools returns schedule_next and loop_done while the chat has a
// self-paced loop that is active or running. A chat without one has no such
// tools, so they do not change its other turns.
func (s *Service) loopTools(ctx context.Context, a workspace.Actor, rec session.ChatRecord) []session.Tool {
	h := s.loops
	if h == nil || rec.Kind == session.KindSubAgent || rec.Origin != "" {
		return nil
	}
	list, err := h.o.Store.ListChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return nil
	}
	paced := false
	for _, l := range list {
		if l.SelfPaced() && (l.State == loops.StateActive || l.Running) {
			paced = true
			break
		}
	}
	if !paced {
		return nil
	}
	return []session.Tool{
		{Name: scheduleNextName, Description: scheduleNextDescription, Schema: json.RawMessage(scheduleNextSchema),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.scheduleNext(ctx, a, raw) }},
		{Name: loopDoneName, Description: loopDoneDescription, Schema: json.RawMessage(loopDoneSchema),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.loopDone(ctx, a, raw) }},
	}
}

// runningPaced returns the self-paced loop of the chat whose run goes on.
func (h *Loops) runningPaced(ctx context.Context, a workspace.Actor) (loops.Loop, error) {
	list, err := h.o.Store.ListChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return loops.Loop{}, err
	}
	for _, l := range list {
		if l.Running && l.SelfPaced() {
			return l, nil
		}
	}
	return loops.Loop{}, errNoLoopRun
}

func (h *Loops) scheduleNext(ctx context.Context, a workspace.Actor, raw json.RawMessage) (string, error) {
	var args struct {
		After  string `json:"after"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	d, err := time.ParseDuration(strings.TrimSpace(args.After))
	if err != nil || d <= 0 {
		return "", fmt.Errorf("after must be a duration such as 90s, 10m or 2h, not %q", args.After)
	}
	l, err := h.runningPaced(ctx, a)
	if err != nil {
		return "", err
	}
	lim := h.Limits()
	wait := loops.Clamp(d, lim.MinInterval)
	now := h.o.Clock().UTC()
	l, err = h.o.Store.Update(ctx, l.ID, func(c *loops.Loop) error {
		if c.State == loops.StateEnded {
			return errors.New("the loop has ended already")
		}
		c.Scheduled, c.Next, c.Wait, c.Updated = true, now.Add(wait), loops.Cut(args.Reason), now
		return nil
	})
	if err != nil {
		return "", err
	}
	h.publish(l.Owner, l.ChatID)
	out := fmt.Sprintf("The next run is in %s, at %s UTC.", loops.ShortDuration(wait), now.Add(wait).Format("15:04"))
	if wait != d {
		out += fmt.Sprintf(" The wait was set to %s: it must be from %s to 24h.", loops.ShortDuration(wait), loops.ShortDuration(lim.MinInterval))
	}
	if l.State == loops.StatePaused {
		out += " The person paused the loop, so it runs only when they resume it."
	}
	return out, nil
}

func (h *Loops) loopDone(ctx context.Context, a workspace.Actor, raw json.RawMessage) (string, error) {
	var args struct {
		Summary string `json:"summary"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	l, err := h.runningPaced(ctx, a)
	if err != nil {
		return "", err
	}
	reason := "Done."
	if s := strings.TrimSpace(args.Summary); s != "" {
		reason = "Done: " + s
	}
	l, err = h.o.Store.Update(ctx, l.ID, func(c *loops.Loop) error {
		c.State, c.Next, c.Scheduled, c.Reason, c.Updated = loops.StateEnded, time.Time{}, false, loops.Cut(reason), h.o.Clock().UTC()
		return nil
	})
	if err != nil {
		return "", err
	}
	h.publish(l.Owner, l.ChatID)
	return "The loop is stopped. Tell the person in one short sentence what it did.", nil
}
