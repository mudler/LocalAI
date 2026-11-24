package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	"In each run call schedule_next to run again, or loop_done when the task is complete. The person can stop the loop at any time. " +
	"report is optional and sends nothing by default: set it only when the person asked to be told in a messaging app. " +
	"report.connection is the id of one of the person's connections and report.channel an approved channel of it (see the description of send_message for both); " +
	"report.mode is on_done (the summary of loop_done), on_problem (a run fails, the loop stops at a limit, or the system stops it) or never. " +
	"The loop shows the destination to the person."

const startLoopSchema = `{"type":"object","properties":{` +
	`"prompt":{"type":"string","description":"What each run does. It must make sense without the rest of this turn."},` +
	`"first_run_after":{"type":"string","description":"How long to wait before the first run, such as 30s or 2m."},` +
	`"reason":{"type":"string","maxLength":300,"description":"One short line about what you wait for."},` +
	`"report":{"type":"object","description":"Optional. Where the loop tells the person how it ended. Leave it out to send nothing.","properties":{` +
	`"connection":{"type":"string","description":"The id of one of the person's connections."},` +
	`"channel":{"type":"string","description":"The name or the id of an approved channel of that connection."},` +
	`"mode":{"type":"string","enum":["on_done","on_problem","never"]}},"required":["mode"]}},` +
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

// loopOffer is what the loop tools of a chat offer the model now. loopTools
// builds the tools from it and the promise guard names them from it, so the
// note of the guard never offers a tool that the model does not have.
type loopOffer struct {
	// start offers start_loop; pace offers schedule_next and loop_done.
	start, pace bool
	// wait offers wait_for; script says that a wait on a command is allowed.
	wait, script bool
}

// loopOfferIn is the offer of the chat of rec, with its loops at hand.
func (s *Service) loopOfferIn(rec session.ChatRecord, list []loops.Loop) loopOffer {
	connector := rec.Origin == session.OriginConnector
	canStart, canWait := s.loopAllowedIn(rec, list, contract.CapLoopsStart), s.loopAllowedIn(rec, list, contract.CapLoopsWait)
	paced, live, runningPaced := false, false, false
	for _, l := range list {
		if l.SelfPaced() && l.State.Live() {
			live = true
		}
		if l.SelfPaced() && (l.State == loops.StateActive || l.Running) {
			paced = true
		}
		if l.SelfPaced() && l.Running {
			runningPaced = true
		}
	}
	var o loopOffer
	if !paced {
		if live {
			// A paused or waiting loop is the person's to resume: it has no
			// tools, and a second loop would only pile up.
			return o
		}
		o.start = canStart
	} else {
		// The runs of a loop have no sender, so a loop that a connector chat
		// already has keeps its two tools: only a sender who held the start
		// capability could have started it (see begin).
		o.pace = !connector || canStart || runningPaced
	}
	o.wait = canWait
	o.script = canWait && s.scriptWaitAllowedIn(rec, list)
	return o
}

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
	list, err := h.o.Store.ListChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return nil
	}
	o := s.loopOfferIn(rec, list)
	var out []session.Tool
	if o.start {
		out = append(out, session.Tool{Name: startLoopName, Description: startLoopDescription, Schema: json.RawMessage(startLoopSchema),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.startLoop(ctx, a, raw) }})
	}
	if o.pace {
		out = append(out,
			session.Tool{Name: scheduleNextName, Description: scheduleNextDescription, Schema: json.RawMessage(scheduleNextSchema),
				Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.scheduleNext(ctx, a, raw) }},
			session.Tool{Name: loopDoneName, Description: loopDoneDescription, Schema: json.RawMessage(loopDoneSchema),
				Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.loopDone(ctx, a, raw) }})
	}
	if o.wait {
		out = append(out, session.Tool{Name: waitForName, Description: waitForDescription, Schema: json.RawMessage(waitForSchema),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return h.startWait(ctx, a, raw) }})
	}
	return out
}

// scriptWaitAllowedIn says whether the model of the chat may arm a wait on a
// command. A wait on a command runs it at every check with no model call, so
// in a connector chat it needs a paired sender, even when a sender who is only
// listed holds loops.wait. A turn with a sender is decided by that sender;
// the run of a loop or a wait continues the pairing of the sender who armed
// it (loops.GrantPaired). Elsewhere the owner's own rules (the bash mode and
// its approval) decide, as before.
func (s *Service) scriptWaitAllowedIn(rec session.ChatRecord, list []loops.Loop) bool {
	if modelLoopOrigin(rec.Origin) {
		return true
	}
	if rec.Origin != session.OriginConnector {
		return false
	}
	if g, held := s.access.get(rec.ID); held {
		return g.Paired
	}
	for _, l := range list {
		if l.Running && slices.Contains(l.Grant, loops.GrantPaired) {
			return true
		}
	}
	return false
}

// scriptWaitAllowed is scriptWaitAllowedIn for the chat of a. A read that
// fails refuses.
func (h *Loops) scriptWaitAllowed(ctx context.Context, a workspace.Actor) bool {
	rec, err := h.svc.getChatEffective(ctx, a.Principal, a.ChatID)
	if err != nil {
		return false
	}
	list, err := h.o.Store.ListChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return false
	}
	return h.svc.scriptWaitAllowedIn(rec, list)
}

// errScriptNeedsPairing refuses a wait on a command in a connector chat whose
// sender is not paired.
var errScriptNeedsPairing = errors.New("a wait that runs a command needs a verified person in this chat. " +
	"Wait on a URL, a read-only tool or a job instead, or tell the person to ask the owner to verify them")

// loopCapability is the capability that a start by the model needs: a wait
// needs loops.wait, a loop needs loops.start.
func loopCapability(by *modelStart) string {
	if by.watch != nil {
		return contract.CapLoopsWait
	}
	return contract.CapLoopsStart
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
// (an owner typing in Studio) gets neither. The one exception is the run of a
// wait or loop that a granted sender armed: see loopAllowedIn.
func (s *Service) loopAllowed(ctx context.Context, rec session.ChatRecord, capability string) bool {
	if modelLoopOrigin(rec.Origin) {
		return true
	}
	if rec.Origin != session.OriginConnector {
		return false
	}
	var list []loops.Loop
	if s.loops != nil {
		list, _ = s.loops.o.Store.ListChat(ctx, rec.Principal, rec.ID)
	}
	return s.loopAllowedIn(rec, list, capability)
}

// loopAllowedIn is loopAllowed with the loops of the chat at hand. When no
// sender turn runs in a connector chat, the run of a loop or a wait goes on as
// a continuation of the sender who armed it: it holds the loop capabilities
// that sender held then (Loop.Grant), and nothing else. A turn with a sender
// is decided by its own grant alone.
func (s *Service) loopAllowedIn(rec session.ChatRecord, list []loops.Loop, capability string) bool {
	if modelLoopOrigin(rec.Origin) {
		return true
	}
	if rec.Origin != session.OriginConnector {
		return false
	}
	allow, _ := s.turnAllow(rec.ID)
	if ok, _ := allow(capability); ok {
		return true
	}
	if _, held := s.access.get(rec.ID); held {
		return false
	}
	if capability != contract.CapLoopsWait && capability != contract.CapLoopsStart {
		return false
	}
	for _, l := range list {
		if l.Running && slices.Contains(l.Grant, capability) {
			return true
		}
	}
	return false
}

// armedGrant is the Grant to store on a loop that the model arms in rec: the
// loop capabilities that the model holds now, by the sender of the turn or,
// in a run of such a loop, by the grant that run continues, and
// loops.GrantPaired when that sender was paired. It is nil outside a
// connector chat, where no grant is needed.
func (s *Service) armedGrant(ctx context.Context, rec session.ChatRecord) []string {
	if rec.Origin != session.OriginConnector {
		return nil
	}
	var list []loops.Loop
	if s.loops != nil {
		list, _ = s.loops.o.Store.ListChat(ctx, rec.Principal, rec.ID)
	}
	var out []string
	for _, c := range []string{contract.CapLoopsWait, contract.CapLoopsStart} {
		if s.loopAllowedIn(rec, list, c) {
			out = append(out, c)
		}
	}
	if len(out) > 0 && s.scriptWaitAllowedIn(rec, list) {
		out = append(out, loops.GrantPaired)
	}
	return out
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

// runLimitNote tells the model how many runs the loop may make, and why
// fewer than the server's limit when the chat is not a person's own.
func runLimitNote(most int, lim LoopLimits) string {
	if most >= lim.MaxRuns {
		return fmt.Sprintf(" The loop ends after at most %d runs.", most)
	}
	return fmt.Sprintf(" The loop ends after at most %d runs: a fifth of the %d that the server allows a loop, in a chat of a messaging app, a trigger, a job or a page. Tell the person if the task may need more.", most, lim.MaxRuns)
}

// maxRunsFor is the number of runs a loop of the chat may make.
func (h *Loops) maxRunsFor(ctx context.Context, owner, chatID string, lim LoopLimits) int {
	rec, err := h.svc.getChatEffective(ctx, owner, chatID)
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
	done := strings.TrimSpace(args.Summary)
	if done == "" {
		done = "the task is complete."
	}
	h.report(l, loops.ReportOnDone, "done. "+loops.Cut(done))
	return "The loop is stopped. Tell the person in one short sentence what it did.", nil
}

// startLoop is the start_loop tool: the model arms a self-paced loop in this
// chat. It needs no yes from the person; the limits of the server bound the
// cost, and the person sees the loop and can stop it.
func (h *Loops) startLoop(ctx context.Context, a workspace.Actor, raw json.RawMessage) (string, error) {
	var args struct {
		Prompt        string      `json:"prompt"`
		FirstRunAfter string      `json:"first_run_after"`
		Reason        string      `json:"reason"`
		Report        *reportArgs `json:"report"`
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
	rec, err := h.chatOf(ctx, a.Principal, a.ChatID)
	if err != nil {
		return "", err
	}
	report, err := h.checkReport(ctx, a.Principal, rec.AgentID, args.Report)
	if err != nil {
		return "", fmt.Errorf("no loop was started: %w", err)
	}
	wait := loops.Clamp(d, lim.MinInterval)
	l, err := h.begin(ctx, a.Principal, a.ChatID, LoopInput{Prompt: args.Prompt}, &modelStart{wait: wait, reason: args.Reason, report: report})
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
	out += runLimitNote(h.maxRunsFor(ctx, a.Principal, a.ChatID, lim), lim)
	if report != nil {
		v := h.ReportOf(ctx, l)
		out += fmt.Sprintf(" The loop will report to the channel %s of the connection %s (%s). The person sees this on the loop.",
			firstNonEmpty(v.ChannelLabel, report.Channel), firstNonEmpty(v.ConnectionName, report.Connection), report.Mode)
	}
	return out, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
