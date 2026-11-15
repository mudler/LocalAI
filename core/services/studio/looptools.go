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
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const (
	scheduleNextName = "schedule_next"
	loopDoneName     = "loop_done"
	startLoopName    = "start_loop"
)

const startLoopDescription = "Start a self-paced loop in this chat. Use it whenever you tell the person that you will check, wait, retry or monitor something later: " +
	"without it nothing runs later. prompt is what each run does; it must stand alone, because a run sees only this text and the chat. " +
	"first_run_after is a duration such as 30s or 2m before the first run (at least the minimum the server allows, at most 24h). " +
	"reason is one short line about what you wait for, which the person sees. " +
	"In each run call schedule_next to run again, or loop_done when the task is complete. The person can stop the loop at any time."

const startLoopSchema = `{"type":"object","properties":{` +
	`"prompt":{"type":"string","description":"What each run does. It must make sense without the rest of this turn."},` +
	`"first_run_after":{"type":"string","description":"How long to wait before the first run, such as 30s or 2m."},` +
	`"reason":{"type":"string","maxLength":300,"description":"One short line about what you wait for."}},` +
	`"required":["prompt","first_run_after"]}`

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
// self-paced loop that is active or running. A chat without a self-paced loop
// gets start_loop instead, so that the model can arm one itself. The tools are
// added after every selection of tools and groups, so a chat of a saved agent
// has them whatever the agent restricts. A connector chat gets each one only
// when the sender of the turn holds its capability (see loopAllowed).
func (s *Service) loopTools(ctx context.Context, a workspace.Actor, rec session.ChatRecord) []session.Tool {
	h := s.loops
	if h == nil || !s.loopOriginKnown(rec.Origin) {
		return nil
	}
	connector := rec.Origin == session.OriginConnector
	canStart, canWait := s.loopAllowed(rec, contract.CapLoopsStart), s.loopAllowed(rec, contract.CapLoopsWait)
	list, err := h.o.Store.ListChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return nil
	}
	paced, live := false, false
	for _, l := range list {
		if l.SelfPaced() && l.State.Live() {
			live = true
		}
		if l.SelfPaced() && (l.State == loops.StateActive || l.Running) {
			paced = true
		}
	}
	var out []session.Tool
	if !paced {
		if live {
			// A paused or waiting loop is the person's to resume: it has no
			// tools, and a second loop would only pile up.
			return nil
		}
		if canStart {
			out = append(out, session.Tool{Name: startLoopName, Description: startLoopDescription, Schema: json.RawMessage(startLoopSchema),
				Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.startLoop(ctx, a, raw) }})
		}
	} else if !connector || canStart || s.connectorLoopRunning(ctx, a, rec) {
		// The runs of a loop have no sender, so a loop that a connector chat
		// already has keeps its two tools: only a sender who held the start
		// capability could have started it (see begin).
		out = append(out,
			session.Tool{Name: scheduleNextName, Description: scheduleNextDescription, Schema: json.RawMessage(scheduleNextSchema),
				Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.scheduleNext(ctx, a, raw) }},
			session.Tool{Name: loopDoneName, Description: loopDoneDescription, Schema: json.RawMessage(loopDoneSchema),
				Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.loopDone(ctx, a, raw) }})
	}
	if canWait {
		out = append(out, session.Tool{Name: waitForName, Description: waitForDescription, Schema: json.RawMessage(waitForSchema),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.startWait(ctx, a, raw) }})
	}
	return out
}

// loopCapability is the capability that a start by the model needs: a wait
// needs loops.wait, a loop needs loops.start.
func loopCapability(by *modelStart) string {
	if by.watch != nil {
		return contract.CapLoopsWait
	}
	return contract.CapLoopsStart
}

// connectorLoopRunning says whether a loop run is going on in the chat now.
// Such a run has no sender and so no grant; the loop was started under one.
func (s *Service) connectorLoopRunning(ctx context.Context, a workspace.Actor, rec session.ChatRecord) bool {
	if rec.Origin != session.OriginConnector || s.loops == nil {
		return false
	}
	_, err := s.loops.runningPaced(ctx, a)
	return err == nil
}

// loopOriginKnown says whether chats with that origin can have loop tools at
// all: a person's chat, a trigger, a job, a page, and a connector chat. Which
// tools a connector chat gets depends on its sender (see loopAllowed).
func (s *Service) loopOriginKnown(origin string) bool {
	return origin == session.OriginConnector || modelLoopOrigin(origin)
}

// loopAllowed says whether the model of the chat may use the loop capability
// now. A person's chat, a trigger, a job and a page always may. A connector
// chat may only when the sender of the running turn holds the capability on
// this binding: loops.wait for the waits, loops.start for loops. The check
// reads the grant that Authorize built for the turn, so a bot that accepts
// anyone gives an unpaired sender neither, and a chat with no sender turn
// (a loop run, an owner typing in Studio) gets neither.
func (s *Service) loopAllowed(rec session.ChatRecord, capability string) bool {
	switch {
	case modelLoopOrigin(rec.Origin):
		return true
	case rec.Origin != session.OriginConnector:
		return false
	}
	allow, _ := s.turnAllow(rec.ID)
	ok, _ := allow(capability)
	return ok
}

// modelLoopOrigin says whether the model of a chat with that origin may use
// the loop tools without a check of a sender. A person's chat, a trigger, a
// job and a page may: their prompts come from the owner, or from a page that
// the owner allowed. A connector chat is not in this list, because the sender
// of its messages can be someone from outside, and a message of theirs must
// not make the server run work on a timer. loopAllowed decides it for each
// sender.
func modelLoopOrigin(origin string) bool {
	switch origin {
	case "", session.OriginTrigger, session.OriginJob, session.OriginPage:
		return true
	}
	return false
}

// systemRunMaxRuns is the run limit of a loop in a chat that the system
// started (a trigger, a job or a page), as a share of the operator's limit: no
// one watches such a chat, so its loops end sooner.
const systemRunMaxRuns = 5

// maxRunsFor is the number of runs a loop of the chat may make.
func (h *Loops) maxRunsFor(ctx context.Context, owner, chatID string, lim LoopLimits) int {
	rec, err := h.svc.d.Store.GetChat(ctx, owner, chatID)
	if err != nil || rec.Origin == "" {
		return lim.MaxRuns
	}
	return max(1, lim.MaxRuns/systemRunMaxRuns)
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

// startLoop is the start_loop tool: the model arms a self-paced loop in this
// chat. It needs no yes from the person; the limits of the server bound the
// cost, and the person sees the loop and can stop it.
func (h *Loops) startLoop(ctx context.Context, a workspace.Actor, raw json.RawMessage) (string, error) {
	var args struct {
		Prompt        string `json:"prompt"`
		FirstRunAfter string `json:"first_run_after"`
		Reason        string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return "", errors.New("prompt is empty: say what each run of the loop does")
	}
	lim := h.Limits()
	d := lim.MinInterval
	if s := strings.TrimSpace(args.FirstRunAfter); s != "" {
		var err error
		if d, err = time.ParseDuration(s); err != nil || d <= 0 {
			return "", fmt.Errorf("first_run_after must be a duration such as 30s or 2m, not %q", args.FirstRunAfter)
		}
	}
	list, err := h.o.Store.ListChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return "", err
	}
	for _, l := range list {
		if l.SelfPaced() && l.State.Live() {
			return "", errors.New("this chat has a self-paced loop already; do not start another. " +
				"In a run of that loop, call schedule_next to run again or loop_done to stop it")
		}
	}
	wait := loops.Clamp(d, lim.MinInterval)
	l, err := h.begin(ctx, a.Principal, a.ChatID, LoopInput{Prompt: args.Prompt}, &modelStart{wait: wait, reason: args.Reason})
	switch {
	case errors.Is(err, loops.ErrTooManyInChat):
		return "", fmt.Errorf("no loop was started: this chat has %d loops, the most it may have. Tell the person; they can stop one", lim.PerChat)
	case errors.Is(err, loops.ErrTooManyForOwner):
		return "", fmt.Errorf("no loop was started: the person has %d loops, the most they may have. Tell the person; they can stop one", lim.PerUser)
	case err != nil:
		return "", fmt.Errorf("no loop was started: %w", err)
	}
	out := fmt.Sprintf("The loop is started. The first run is in %s, at %s UTC. Tell the person in one short sentence what you will check and when; they can stop the loop.",
		loops.ShortDuration(wait), l.Next.Format("15:04"))
	if wait != d {
		out += fmt.Sprintf(" The wait was set to %s: it must be from %s to 24h.", loops.ShortDuration(wait), loops.ShortDuration(lim.MinInterval))
	}
	return out, nil
}
