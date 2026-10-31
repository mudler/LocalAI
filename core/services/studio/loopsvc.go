package studio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// DefaultLoopTick is the time between two passes of the loop host.
const DefaultLoopTick = 5 * time.Second

// loopStartGrace is how long a run that another process claimed may take to
// start its turn before a pass takes the run for one that a dead process left.
const loopStartGrace = time.Minute

// Errors of the loop host.
var (
	// ErrLoopsClosed means the loop host has stopped.
	ErrLoopsClosed = errors.New("studio: the loop host has stopped")
	// ErrLoopsOff means the server runs no loop host.
	ErrLoopsOff = errors.New("loops are turned off on this server")
	// ErrLoopCommand refuses a loop prompt that is a command other than a skill.
	ErrLoopCommand = errors.New("a loop repeats plain text or a skill (/skill-name and its words); other commands cannot repeat")
	// ErrLoopChat refuses a loop in a chat that a person did not start.
	ErrLoopChat = errors.New("a loop can run only in a chat that you started")
	// ErrLoopState refuses a change of state that a loop cannot make.
	ErrLoopState = errors.New("a loop can only be paused or made active, and an ended loop cannot go on: start a new one")
)

// errLoopSkip leaves a loop as it is in a Store.Update.
var errLoopSkip = errors.New("studio: loop unchanged")

// LoopLimits are the operator's limits of the loops. A zero field takes the
// default of package loops.
type LoopLimits struct {
	// MinInterval is the shortest interval, and the shortest wait a self-paced
	// loop may ask for.
	MinInterval time.Duration
	// MaxRuns ends a loop after that many runs.
	MaxRuns int
	// PerChat and PerUser bound the loops that are not ended.
	PerChat, PerUser int
	// DailyRuns is the number of loop runs one owner may start in a UTC day.
	DailyRuns int
}

func (l LoopLimits) withDefaults() LoopLimits {
	if l.MinInterval < loops.MinInterval {
		l.MinInterval = loops.DefaultMinInterval
	}
	if l.MinInterval > loops.MaxInterval {
		l.MinInterval = loops.MaxInterval
	}
	if l.MaxRuns <= 0 {
		l.MaxRuns = loops.DefaultMaxRuns
	}
	if l.PerChat <= 0 {
		l.PerChat = loops.DefaultPerChat
	}
	if l.PerUser <= 0 {
		l.PerUser = loops.DefaultPerUser
	}
	if l.DailyRuns <= 0 {
		l.DailyRuns = loops.DefaultDailyRuns
	}
	return l
}

// LoopOptions configures the loop host.
type LoopOptions struct {
	// Store holds the loops. Required.
	Store loops.Store
	// Clock gives the time; nil means time.Now. A test sets a fake one.
	Clock func() time.Time
	// Tick is the time between two passes; zero means DefaultLoopTick.
	Tick time.Duration
	// RunLimit is how long one run may take; zero means DefaultRunLimit.
	RunLimit time.Duration
	// Lead says whether this frontend runs the loops now. With a database,
	// one frontend leads at a time (the host gives it an advisory lock). Nil:
	// always.
	Lead func(ctx context.Context) bool
	// Limits gives the operator's limits, read at each use so that a change
	// of the settings applies at once. Nil: the defaults.
	Limits func() LoopLimits
	// Logger; nil means slog.Default.
	Logger *slog.Logger
}

// LoopInput is a loop that a person starts.
type LoopInput struct {
	// Interval is the time between runs, such as 5m. Empty makes the loop
	// self-paced.
	Interval string
	Prompt   string
}

// Loops runs the loops of the chats: a prompt that a chat sends to itself
// again, at an interval or when the model asks, as a new message of the person
// in the same chat.
//
// State mode: single-runner. The loops are in the shared store; one frontend
// at a time, the leader, fires them. A run is claimed in the store (a loop
// whose run goes on is not due), and the turn claim of the chat keeps one turn
// per chat, so a short overlap of two leaders starts no run twice. A new
// leader finishes the runs that the old one watched from the store.
type Loops struct {
	svc *Service
	o   LoopOptions
	log *slog.Logger

	pass sync.Mutex // one pass at a time

	mu   sync.Mutex          // guards sup and shut
	shut bool                // Close has begun: no supervisor may be added to wg
	sup  map[string]struct{} // loops whose run this process watches

	wake chan struct{}
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// NewLoops returns the loop host of a service. It does nothing until Run or
// Pass is called.
func NewLoops(svc *Service, o LoopOptions) (*Loops, error) {
	if svc == nil || o.Store == nil {
		return nil, errors.New("studio: the loop host needs a service and a store")
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Tick <= 0 {
		o.Tick = DefaultLoopTick
	}
	if o.RunLimit <= 0 {
		o.RunLimit = DefaultRunLimit
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Loops{svc: svc, o: o, log: o.Logger, sup: map[string]struct{}{}, wake: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

// SetLoops gives the service its loop host. Without one the chats have no
// loops.
func (s *Service) SetLoops(h *Loops) { s.loops = h }

// Loops returns the loop host, or nil when loops are off.
func (s *Service) Loops() *Loops { return s.loops }

// Limits returns the limits that apply now.
func (h *Loops) Limits() LoopLimits {
	var l LoopLimits
	if h.o.Limits != nil {
		l = h.o.Limits()
	}
	return l.withDefaults()
}

// Notify asks Run for a pass at once. It never blocks.
func (h *Loops) Notify() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// Run makes a pass at each tick and at each Notify while this frontend leads,
// until ctx ends or Close is called.
func (h *Loops) Run(ctx context.Context) {
	tick := time.NewTicker(h.o.Tick)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if h.o.Lead == nil || h.o.Lead(ctx) {
			if err := h.Pass(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, ErrLoopsClosed) {
				h.log.Warn("studio: loop pass failed", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-h.done:
			return
		case <-tick.C:
		case <-h.wake:
		}
	}
}

// Close stops the host. A run that goes on stays marked as running; the next
// leader finishes it from the store.
func (h *Loops) Close() {
	h.mu.Lock()
	h.shut = true
	h.mu.Unlock()
	h.once.Do(func() { close(h.done) })
	h.wg.Wait()
}

func (h *Loops) closed() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// publish tells the watchers of a chat that its loops changed.
func (h *Loops) publish(owner, chatID string) {
	h.svc.bus.Publish(key(owner, chatID), session.Event{Kind: session.KindLoops})
}

// --- What a person does -----------------------------------------------------

// chatOf returns the user's chat; another user's chat is session.ErrNotFound.
func (h *Loops) chatOf(ctx context.Context, user, conv string) (session.ChatRecord, error) {
	return h.svc.Get(ctx, user, conv)
}

// List returns the loops of a chat, oldest first. Another user's chat is
// session.ErrNotFound.
func (h *Loops) List(ctx context.Context, user, conv string) ([]loops.Loop, error) {
	if _, err := h.chatOf(ctx, user, conv); err != nil {
		return nil, err
	}
	return h.o.Store.ListChat(ctx, principal(user), conv)
}

// Start starts a loop in a chat. The first loop of a chat waits for the
// person's yes (StatePending); later ones are active at once and run now.
func (h *Loops) Start(ctx context.Context, user, conv string, in LoopInput) (loops.Loop, error) {
	p := principal(user)
	rec, err := h.chatOf(ctx, user, conv)
	if err != nil {
		return loops.Loop{}, err
	}
	switch {
	case rec.Kind == session.KindSubAgent:
		return loops.Loop{}, ErrSubAgentReadOnly
	case rec.Origin != "":
		return loops.Loop{}, ErrLoopChat
	}
	lim := h.Limits()
	prompt := strings.TrimSpace(in.Prompt)
	if strings.HasPrefix(prompt, "/") {
		if err := h.checkSkill(ctx, user, conv, prompt); err != nil {
			return loops.Loop{}, err
		}
	}
	var interval time.Duration
	if s := strings.TrimSpace(in.Interval); s != "" {
		if interval, err = loops.ParseInterval(s, lim.MinInterval); err != nil {
			return loops.Loop{}, err
		}
	}
	ok, err := h.o.Store.Consented(ctx, p, conv)
	if err != nil {
		return loops.Loop{}, err
	}
	now := h.o.Clock().UTC()
	l := loops.Loop{ID: newID(), Owner: p, ChatID: conv, Prompt: prompt, Interval: interval,
		State: loops.StatePending, Created: now, Updated: now}
	if ok {
		l.State, l.Next = loops.StateActive, now
	}
	if err := h.o.Store.Add(ctx, l, loops.Limits{PerChat: lim.PerChat, PerUser: lim.PerUser}); err != nil {
		return loops.Loop{}, err
	}
	h.publish(p, conv)
	h.Notify()
	return l, nil
}

// checkSkill refuses a slash command that is not a skill the chat can use.
func (h *Loops) checkSkill(ctx context.Context, user, conv, prompt string) error {
	name := strings.TrimPrefix(strings.Fields(prompt)[0], "/")
	list, _, err := h.svc.Skills(ctx, user, conv)
	if err != nil {
		return fmt.Errorf("%w: skills are not available here", ErrLoopCommand)
	}
	for _, sk := range list {
		if sk.Name == name && !sk.Off && sk.Invalid == "" && sk.Shadowed == "" && sk.Unavailable == "" {
			return nil
		}
	}
	return fmt.Errorf("%w: /%s is not a skill of this chat", ErrLoopCommand, name)
}

// owned returns the loop id of the user's chat conv, or loops.ErrNotFound.
func (h *Loops) owned(ctx context.Context, user, conv, id string) (loops.Loop, error) {
	if _, err := h.chatOf(ctx, user, conv); err != nil {
		return loops.Loop{}, err
	}
	l, err := h.o.Store.Get(ctx, id)
	if err != nil || l.Owner != principal(user) || l.ChatID != conv {
		return loops.Loop{}, loops.ErrNotFound
	}
	return l, nil
}

// SetState pauses a loop (StatePaused) or makes it active (StateActive). Making
// a pending loop active is the person's yes: the chat asks no more. A loop
// that becomes active runs at once.
func (h *Loops) SetState(ctx context.Context, user, conv, id string, st loops.State) (loops.Loop, error) {
	if st != loops.StateActive && st != loops.StatePaused {
		return loops.Loop{}, ErrLoopState
	}
	cur, err := h.owned(ctx, user, conv, id)
	if err != nil {
		return loops.Loop{}, err
	}
	now := h.o.Clock().UTC()
	l, err := h.o.Store.Update(ctx, id, func(l *loops.Loop) error {
		// A pending loop waits for a yes or a no: Start or Cancel, not Pause.
		if l.State == loops.StateEnded || (l.State == loops.StatePending && st == loops.StatePaused) {
			return ErrLoopState
		}
		if l.State == st {
			return errLoopSkip
		}
		if st == loops.StatePaused {
			l.State, l.Next, l.Reason = loops.StatePaused, time.Time{}, ""
		} else {
			l.State, l.Failures, l.Reason, l.Next = loops.StateActive, 0, "", now
		}
		l.Updated = now
		return nil
	})
	switch {
	case errors.Is(err, errLoopSkip):
		return h.o.Store.Get(ctx, id)
	case err != nil:
		return loops.Loop{}, err
	}
	if cur.State == loops.StatePending && st == loops.StateActive {
		if err := h.o.Store.SetConsented(ctx, l.Owner, conv); err != nil {
			h.log.Warn("studio: could not record the yes to a loop", "chat", conv, "error", err)
		}
	}
	h.publish(l.Owner, conv)
	h.Notify()
	return l, nil
}

// Remove stops a loop for good. A run that goes on is not stopped: the
// person stops it with the chat's Stop.
func (h *Loops) Remove(ctx context.Context, user, conv, id string) error {
	l, err := h.owned(ctx, user, conv, id)
	if err != nil {
		return err
	}
	if err := h.o.Store.Delete(ctx, l.ID); err != nil {
		return err
	}
	h.publish(l.Owner, conv)
	return nil
}

// RemoveAll stops every loop of a chat and returns how many there were.
func (h *Loops) RemoveAll(ctx context.Context, user, conv string) (int, error) {
	list, err := h.List(ctx, user, conv)
	if err != nil {
		return 0, err
	}
	for _, l := range list {
		if err := h.o.Store.Delete(ctx, l.ID); err != nil {
			return 0, err
		}
	}
	if len(list) > 0 {
		h.publish(principal(user), conv)
	}
	return len(list), nil
}

// forgetChat removes the loops of a chat that was deleted.
func (h *Loops) forgetChat(ctx context.Context, p, conv string) {
	if _, err := h.o.Store.DeleteChat(context.WithoutCancel(ctx), p, conv); err != nil {
		h.log.Warn("studio: could not remove the loops of a deleted chat", "chat", conv, "error", err)
	}
}

// --- The runner -------------------------------------------------------------

// Pass makes one pass as the leader: it fires the loops that are due and
// finishes the runs that no supervisor of this process watches. A test calls
// it instead of Run.
func (h *Loops) Pass(ctx context.Context) error {
	h.pass.Lock()
	defer h.pass.Unlock()
	if h.closed() {
		return ErrLoopsClosed
	}
	list, err := h.o.Store.Working(ctx)
	if err != nil {
		return err
	}
	lim := h.Limits()
	for _, l := range list {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		now := h.o.Clock().UTC()
		switch {
		case l.Running:
			h.reconcile(ctx, l, lim)
		case l.State == loops.StateActive && !l.Next.IsZero() && !now.Before(l.Next):
			h.fire(ctx, l, now, lim)
		}
	}
	return nil
}

// fire starts the run of a due loop, or skips it when the chat is busy.
func (h *Loops) fire(ctx context.Context, l loops.Loop, now time.Time, lim LoopLimits) {
	if _, err := h.svc.d.Store.GetChat(ctx, l.Owner, l.ChatID); err != nil {
		if errors.Is(err, session.ErrNotFound) {
			h.drop(ctx, l, "the chat was deleted")
		}
		return
	}
	if why, gone := h.checkOwner(ctx, l.Owner); gone {
		h.drop(ctx, l, why)
		return
	} else if why != "" {
		h.pause(ctx, l, why)
		return
	}
	if h.busy(ctx, l) {
		h.skipBusy(ctx, l, now)
		return
	}
	if l.Runs >= lim.MaxRuns {
		h.end(ctx, l.ID, fmt.Sprintf("Stopped after %d runs, the most a loop may make.", lim.MaxRuns))
		return
	}
	claimed, err := h.o.Store.Update(ctx, l.ID, func(c *loops.Loop) error {
		if c.State != loops.StateActive || c.Running || c.Next.IsZero() || now.Before(c.Next) {
			return errLoopSkip
		}
		c.Running, c.Runs, c.LastRun, c.Updated = true, c.Runs+1, now, now
		c.Scheduled, c.Wait, c.Reason, c.Next = false, "", "", time.Time{}
		return nil
	})
	if err != nil {
		if !errors.Is(err, errLoopSkip) && !errors.Is(err, loops.ErrNotFound) {
			h.log.Warn("studio: could not claim a loop run", "loop", l.ID, "error", err)
		}
		return
	}
	took, err := h.o.Store.TakeRun(ctx, l.Owner, loops.Day(now), lim.DailyRuns)
	if err != nil || !took {
		h.unclaim(ctx, claimed, func(c *loops.Loop) {
			if err != nil {
				c.Next = now.Add(h.o.Tick)
				return
			}
			c.Next = loops.NextDay(now)
			c.Reason = fmt.Sprintf("Waits for tomorrow (UTC): today's limit of %d loop runs is used up.", lim.DailyRuns)
		})
		return
	}
	h.start(ctx, claimed, now)
}

// start sends the prompt of a claimed run to the chat and watches the turn.
func (h *Loops) start(ctx context.Context, l loops.Loop, now time.Time) {
	events, cancel, err := h.svc.Events(ctx, l.Owner, l.ChatID) // before the send: the end must not be missed
	if err != nil {
		h.unclaim(ctx, l, func(c *loops.Loop) { c.Next = now.Add(h.o.Tick) })
		return
	}
	h.mu.Lock()
	if h.shut {
		h.mu.Unlock()
		cancel()
		h.unclaim(ctx, l, func(c *loops.Loop) { c.Next = now })
		return
	}
	h.sup[l.ID] = struct{}{}
	h.wg.Add(1)
	h.mu.Unlock()
	msg := session.Message{Text: l.Prompt, Loop: &session.LoopMark{ID: l.ID, Run: l.Runs, SelfPaced: l.SelfPaced()}}
	started, err := h.svc.startIdle(ctx, l.Owner, l.ChatID, msg)
	if err != nil || !started {
		cancel()
		h.mu.Lock()
		delete(h.sup, l.ID)
		h.mu.Unlock()
		h.wg.Done()
		if err != nil {
			h.log.Warn("studio: a loop run could not start", "loop", l.ID, "chat", l.ChatID, "error", err)
			h.finish(l.ID, true, "the run could not start")
			return
		}
		// The chat became busy between the check and the claim.
		h.unclaim(ctx, l, func(c *loops.Loop) { h.nextAfterBusy(c, now) })
		return
	}
	h.publish(l.Owner, l.ChatID)
	go h.supervise(l, events, cancel)
}

// unclaim gives back a run that did not start: the loop is as before the
// claim, with the next due time that set gives it.
func (h *Loops) unclaim(ctx context.Context, l loops.Loop, set func(*loops.Loop)) {
	_, err := h.o.Store.Update(context.WithoutCancel(ctx), l.ID, func(c *loops.Loop) error {
		if !c.Running {
			return errLoopSkip
		}
		c.Running, c.Runs = false, max(c.Runs-1, 0)
		set(c)
		return nil
	})
	if err != nil && !errors.Is(err, errLoopSkip) && !errors.Is(err, loops.ErrNotFound) {
		h.log.Warn("studio: could not give back a loop run", "loop", l.ID, "error", err)
	}
	h.publish(l.Owner, l.ChatID)
}

// busy reports whether the chat runs a turn or has messages that wait.
func (h *Loops) busy(ctx context.Context, l loops.Loop) bool {
	info, err := h.svc.d.Store.Turn(ctx, l.Owner, l.ChatID)
	if err != nil || info.Running {
		return true
	}
	pend, err := h.svc.d.Store.Pending(ctx, l.Owner, l.ChatID)
	return err != nil || len(pend) > 0
}

// skipBusy moves an interval loop to its next slot: the runs a busy chat
// missed do not pile up. A self-paced loop tries again at the next pass.
func (h *Loops) skipBusy(ctx context.Context, l loops.Loop, now time.Time) {
	if l.SelfPaced() {
		return
	}
	_, err := h.o.Store.Update(ctx, l.ID, func(c *loops.Loop) error {
		if c.State != loops.StateActive || c.Running {
			return errLoopSkip
		}
		h.nextAfterBusy(c, now)
		return nil
	})
	if err == nil {
		h.publish(l.Owner, l.ChatID)
	}
}

func (h *Loops) nextAfterBusy(c *loops.Loop, now time.Time) {
	if c.SelfPaced() {
		c.Next = now
		return
	}
	from := c.Next
	if from.IsZero() {
		from = now
	}
	c.Next = loops.NextSlot(from, c.Interval, now)
}

// supervise waits for the end of a run's turn and records it.
func (h *Loops) supervise(l loops.Loop, events <-chan session.Event, cancel func()) {
	defer h.wg.Done()
	defer cancel()
	defer func() {
		h.mu.Lock()
		delete(h.sup, l.ID)
		h.mu.Unlock()
	}()
	end := h.svc.awaitTurn(l.Owner, l.ChatID, events, h.o.RunLimit, h.done)
	switch end.How {
	case TurnAbandoned:
		return
	case TurnTimedOut:
		h.stopChat(l)
		h.finish(l.ID, true, "the run took longer than "+limitText(h.o.RunLimit))
	default:
		h.finish(l.ID, turnFailed(end.Info.Last), end.ErrText)
	}
}

func turnFailed(last store.TurnStatus) bool {
	return last == store.TurnFailed || last == store.TurnInterrupted
}

func (h *Loops) stopChat(l loops.Loop) {
	ctx, cancel := context.WithTimeout(context.Background(), stopWait)
	defer cancel()
	if err := h.svc.Stop(ctx, l.Owner, l.ChatID); err != nil {
		h.log.Warn("studio: could not stop the run of a loop", "chat", l.ChatID, "error", err)
	}
}

// reconcile finishes a run that no supervisor of this process watches: one
// that a former leader started, or whose frontend died.
func (h *Loops) reconcile(ctx context.Context, l loops.Loop, _ LoopLimits) {
	h.mu.Lock()
	_, mine := h.sup[l.ID]
	h.mu.Unlock()
	if mine {
		return
	}
	info, err := h.svc.d.Store.Turn(ctx, l.Owner, l.ChatID)
	switch {
	case errors.Is(err, session.ErrNotFound):
		h.drop(ctx, l, "the chat was deleted")
	case err != nil:
	case !info.Running && h.o.Clock().Sub(l.LastRun) < loopStartGrace:
		// Another frontend may have claimed the run and not yet the chat.
	case !info.Running:
		h.finish(l.ID, turnFailed(info.Last), "")
	case !l.LastRun.IsZero() && h.o.Clock().Sub(l.LastRun) > h.o.RunLimit:
		h.stopChat(l)
		h.finish(l.ID, true, "the run took longer than "+limitText(h.o.RunLimit))
	}
}

// finish records the end of a run and decides what comes next.
func (h *Loops) finish(id string, failed bool, why string) {
	ctx, cancel := context.WithTimeout(context.Background(), finishTimeout)
	defer cancel()
	now := h.o.Clock().UTC()
	lim := h.Limits()
	l, err := h.o.Store.Update(ctx, id, func(c *loops.Loop) error {
		if !c.Running {
			return errLoopSkip
		}
		c.Running, c.Updated = false, now
		if failed {
			c.Failures++
		} else {
			c.Failures = 0
		}
		switch {
		case c.State == loops.StateEnded:
			// loop_done during the run
		case c.State != loops.StateActive:
			c.Next = time.Time{}
		case failed && c.Failures >= loops.MaxFailures:
			c.State, c.Next = loops.StatePaused, time.Time{}
			c.Reason = fmt.Sprintf("Paused after %d failed runs in a row.", loops.MaxFailures)
			if why != "" {
				c.Reason = loops.Cut(c.Reason + " Last error: " + why)
			}
		case c.Runs >= lim.MaxRuns:
			c.State, c.Next = loops.StateEnded, time.Time{}
			c.Reason = fmt.Sprintf("Stopped after %d runs, the most a loop may make.", lim.MaxRuns)
		case c.SelfPaced() && failed:
			c.Next = now.Add(lim.MinInterval)
		case c.SelfPaced() && !c.Scheduled:
			c.State, c.Next = loops.StateEnded, time.Time{}
			c.Reason = "Stopped: the model did not ask for another run."
		case c.SelfPaced():
			// schedule_next set Next
		default:
			c.Next = loops.NextSlot(c.LastRun, c.Interval, now)
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, errLoopSkip) && !errors.Is(err, loops.ErrNotFound) {
			h.log.Warn("studio: could not record the end of a loop run", "loop", id, "error", err)
		}
		return
	}
	if l.State == loops.StateEnded {
		h.pruneEnded(ctx, l.Owner, l.ChatID)
	}
	h.publish(l.Owner, l.ChatID)
	h.Notify()
}

// end ends a loop with a reason.
func (h *Loops) end(ctx context.Context, id, reason string) {
	l, err := h.o.Store.Update(ctx, id, func(c *loops.Loop) error {
		c.State, c.Next, c.Reason, c.Updated = loops.StateEnded, time.Time{}, loops.Cut(reason), h.o.Clock().UTC()
		return nil
	})
	if err != nil {
		return
	}
	h.pruneEnded(ctx, l.Owner, l.ChatID)
	h.publish(l.Owner, l.ChatID)
}

// pause pauses a loop with a reason.
func (h *Loops) pause(ctx context.Context, l loops.Loop, reason string) {
	_, err := h.o.Store.Update(ctx, l.ID, func(c *loops.Loop) error {
		if c.State != loops.StateActive {
			return errLoopSkip
		}
		c.State, c.Next, c.Reason, c.Updated = loops.StatePaused, time.Time{}, loops.Cut(reason), h.o.Clock().UTC()
		return nil
	})
	if err == nil {
		h.publish(l.Owner, l.ChatID)
	}
}

// drop removes a loop that can never run again.
func (h *Loops) drop(ctx context.Context, l loops.Loop, why string) {
	if err := h.o.Store.Delete(ctx, l.ID); err != nil {
		h.log.Warn("studio: could not remove a loop", "loop", l.ID, "error", err)
		return
	}
	h.log.Info("studio: removed a loop", "loop", l.ID, "chat", l.ChatID, "why", why)
}

// pruneEnded keeps the newest loops.KeepEnded ended loops of a chat.
func (h *Loops) pruneEnded(ctx context.Context, owner, chatID string) {
	list, err := h.o.Store.ListChat(ctx, owner, chatID)
	if err != nil {
		return
	}
	var ended []loops.Loop
	for _, l := range list {
		if l.State == loops.StateEnded && !l.Running {
			ended = append(ended, l)
		}
	}
	if len(ended) <= loops.KeepEnded {
		return
	}
	sort.Slice(ended, func(i, j int) bool { return ended[i].Updated.Before(ended[j].Updated) })
	for _, l := range ended[:len(ended)-loops.KeepEnded] {
		_ = h.o.Store.Delete(ctx, l.ID)
	}
}

// checkOwner tells why the owner cannot run a loop now. gone is true when the
// owner no longer exists, so the loop can never run.
func (h *Loops) checkOwner(ctx context.Context, owner string) (why string, gone bool) {
	if h.svc.d.Users == nil {
		return "", false
	}
	_, err := h.svc.d.Users(ctx, owner)
	switch {
	case err == nil, ctx.Err() != nil:
		return "", false
	case errors.Is(err, ErrUserDisabled):
		return "Paused: the account is disabled.", false
	case errors.Is(err, ErrChatsOff):
		return "Paused: chats are switched off for the account.", false
	case errors.Is(err, ErrNoUser):
		return "the owner no longer exists", true
	}
	return "", false
}
