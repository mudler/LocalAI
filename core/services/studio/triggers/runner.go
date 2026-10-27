package triggers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Defaults of a Runner.
const (
	DefaultTick       = 5 * time.Second
	DefaultMaxRunning = 2
	// maxCatchUp bounds the due times that one pass counts for one trigger.
	maxCatchUp = 100000
	// feedPage is the number of change sets read from the feed at once.
	feedPage = 200
)

// Options configures a Runner.
type Options struct {
	Store Store
	// Workspace is read for the change feed of file triggers. Nil means there
	// are no file triggers: they never fire.
	Workspace workspace.Store
	// Admin reads the change feed and the files; it sees every path.
	Admin workspace.Actor
	// Start starts the work of a run, for example a chat of the owner, and
	// returns the ID of that chat. It must return soon: the work goes on in
	// the background, and the host calls Runner.Finish when it ends. The
	// Runner has marked the run running before the call. An error makes the
	// run failed, with the error text; a *SkipError makes it skipped. Start
	// must not call Finish.
	// The ctx is owned by the Runner, not by the request that caused the run:
	// it ends when the Runner stops (when the context of Run ends). It carries
	// none of the values of the request that caused the run: pass what Start
	// needs in the Trigger or the Run.
	Start func(ctx context.Context, t Trigger, r Run) (chatID string, err error)
	// Stop stops the work of a run by its chat ID. The host still calls Finish
	// when the work has ended.
	Stop func(chatID string)
	// Live tells whether the work of a run with this chat ID goes on in this
	// process. Nil means no work is live. At the first pass, a running run
	// that is not live becomes failed with "interrupted by a restart".
	Live func(chatID string) bool
	// Clock gives the time; nil means time.Now. A test sets a fake one.
	Clock func() time.Time
	// Tick is the time between two passes of Run; zero means DefaultTick.
	Tick time.Duration
	// MaxRunning is the number of runs at once; zero means DefaultMaxRunning.
	MaxRunning int
	Logf       func(format string, args ...any)
}

// Runner starts the runs of the triggers in a Store.
type Runner struct {
	o    Options
	logf func(string, ...any)
	wake chan struct{}

	mu        sync.Mutex // one pass, Finish, RunNow or Cancel at a time
	recovered bool
	closed    bool            // Run has returned
	runCtx    context.Context // the context of Run; nil while Run is not going
	feed      feedState
	chats     map[string]chatRun // chat ID of a run this Runner started
	chatSeq   int64
}

// NewRunner returns a Runner. It does nothing until Run or Sync is called.
func NewRunner(o Options) (*Runner, error) {
	if o.Store == nil || o.Start == nil {
		return nil, errors.New("triggers: a Runner needs a Store and a Start function")
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Tick <= 0 {
		o.Tick = DefaultTick
	}
	if o.MaxRunning <= 0 {
		o.MaxRunning = DefaultMaxRunning
	}
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Runner{o: o, logf: logf, wake: make(chan struct{}, 1), feed: newFeedState()}, nil
}

// Notify asks Run for a pass at once. A host calls it after a write to the
// workspace, so that a file trigger sees the change without waiting for the
// next tick. It never blocks.
func (r *Runner) Notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run makes a pass at once, then one at each tick and at each Notify, until
// ctx ends. It leaves no goroutine behind and starts no run after ctx ends.
func (r *Runner) Run(ctx context.Context) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.runCtx = ctx
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
	}()
	tick := time.NewTicker(r.o.Tick)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := r.Sync(ctx); err != nil && ctx.Err() == nil {
			r.logf("triggers: pass failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-r.wake:
		}
	}
}

// Sync makes one pass: it recovers the runs that a restart interrupted (at
// the first pass), claims the schedule runs that are due, reads the change
// feed for file triggers, and starts the queued runs that fit. Run calls it;
// a test or a host may call it too.
func (r *Runner) Sync(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped() {
		return ErrStopped
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	now := r.o.Clock()
	if err := r.ensureRecovered(ctx, now); err != nil {
		return err
	}
	trs, err := r.o.Store.EnabledTriggers(ctx)
	if err != nil {
		return err
	}
	var errs []error
	if err := r.schedulePass(ctx, trs, now); err != nil {
		errs = append(errs, err)
	}
	if err := r.filePass(ctx, trs, now); err != nil {
		errs = append(errs, err)
	}
	if err := r.startQueued(ctx, now); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// stopped tells whether the Runner has stopped: Run has returned, or the
// context of Run has ended. A stopped Runner starts nothing. The caller holds
// the lock.
func (r *Runner) stopped() bool {
	return r.closed || (r.runCtx != nil && r.runCtx.Err() != nil)
}

// ensureRecovered recovers the runs of a dead process once, before anything
// else acts. The caller holds the lock.
func (r *Runner) ensureRecovered(ctx context.Context, now time.Time) error {
	if r.recovered {
		return nil
	}
	if err := r.recover(ctx, now); err != nil {
		return err
	}
	r.recovered = true
	return nil
}

func (r *Runner) recover(ctx context.Context, now time.Time) error {
	running, err := r.o.Store.RunningRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range running {
		if r.o.Live != nil && run.ChatID != "" && r.o.Live(run.ChatID) {
			continue
		}
		run.Status, run.Reason, run.Ended = StatusFailed, ReasonInterrupted, now
		r.endChat(ctx, run.ID)
		if err := r.o.Store.SetRunStatus(ctx, run); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}

// schedulePass claims one run for each schedule trigger that is due.
func (r *Runner) schedulePass(ctx context.Context, trs []Trigger, now time.Time) error {
	var errs []error
	for _, t := range trs {
		if t.Kind != KindSchedule {
			continue
		}
		if err := r.scheduleOne(ctx, t, now); err != nil {
			errs = append(errs, fmt.Errorf("trigger %s: %w", t.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (r *Runner) scheduleOne(ctx context.Context, t Trigger, now time.Time) error {
	sched, err := ParseSchedule(t.Schedule)
	if err != nil {
		r.logf("triggers: trigger %s has a bad schedule: %v", t.ID, err)
		return nil
	}
	loc, err := t.Location()
	if err != nil {
		r.logf("triggers: trigger %s has a bad time zone: %v", t.ID, err)
		return nil
	}
	last, err := r.o.Store.LastDue(ctx, t.ID)
	if err != nil {
		return err
	}
	if last.Before(t.Since) {
		last = t.Since
	}
	if last.IsZero() { // a trigger that was never armed starts to watch now
		return r.o.Store.SetLastDue(ctx, t.ID, now)
	}
	due, n := sched.lastDue(last, now, loc, maxCatchUp)
	if n == 0 {
		return nil
	}
	// One pass counts at most maxCatchUp times. Past that, go on to the latest
	// due time not after now, so that the next passes find nothing to catch up.
	for k := n; k >= maxCatchUp; {
		var next time.Time
		if next, k = sched.lastDue(due, now, loc, maxCatchUp); k > 0 {
			due = next
		}
	}
	run := Run{ID: newID(), TriggerID: t.ID, DueKey: due.UTC().Format(time.RFC3339), Due: due.UTC()}
	if n > 1 {
		run.Reason = ReasonCaughtUp
	}
	if err := r.admit(ctx, t, &run, now, true, false); err != nil {
		return err
	}
	if _, err := r.o.Store.Claim(ctx, run); err != nil {
		return err
	}
	// Claim first: a crash between the two calls finds the same due time
	// again, and the claim is refused.
	return r.o.Store.SetLastDue(ctx, t.ID, due.UTC())
}

// admit decides what happens to a run that was just made: it stays queued, or
// it becomes skipped with a reason. strict makes a run skipped while another
// run of the trigger is queued or running; a file run waits instead (see
// startQueued). manual runs skip the daily cap. The cap counts the day of
// run.Due, the same instant that CountRunsSince compares, so a run due before
// midnight and claimed after it counts for the day it is due in.
func (r *Runner) admit(ctx context.Context, t Trigger, run *Run, now time.Time, strict, manual bool) error {
	run.Status = StatusQueued
	if strict {
		why, err := r.active(ctx, t.ID)
		if err != nil {
			return err
		}
		if why != "" {
			run.Status, run.Reason, run.Ended = StatusSkipped, why, now.UTC()
			return nil
		}
	}
	if manual {
		return nil
	}
	due := run.Due.In(capLocation(t, now))
	y, m, d := due.Date()
	dayStart := time.Date(y, m, d, 0, 0, 0, 0, due.Location())
	n, err := r.o.Store.CountRunsSince(ctx, t.ID, dayStart)
	if err != nil {
		return err
	}
	if cap := t.CapOrDefault(); n >= cap {
		run.Status, run.Reason, run.Ended = StatusSkipped, fmt.Sprintf("daily limit of %d runs reached", cap), now.UTC()
	}
	return nil
}

// capLocation is the zone whose days the daily cap counts: the TZ of the
// trigger when it has one, else UTC for a schedule trigger and the zone of the
// clock for a file trigger.
func capLocation(t Trigger, now time.Time) *time.Location {
	if t.TZ == "" && t.Kind == KindFile {
		return now.Location()
	}
	loc, err := t.Location()
	if err != nil {
		return now.Location()
	}
	return loc
}

// active tells why the trigger cannot take a new strict run: ReasonStillGoing
// when it has a running run, ReasonWaiting when it has only a queued one (for
// example one that a restart put back), and "" when it has neither.
func (r *Runner) active(ctx context.Context, triggerID string) (string, error) {
	for _, c := range []struct {
		list   func(context.Context) ([]Run, error)
		reason string
	}{{r.o.Store.RunningRuns, ReasonStillGoing}, {r.o.Store.QueuedRuns, ReasonWaiting}} {
		runs, err := c.list(ctx)
		if err != nil {
			return "", err
		}
		for _, x := range runs {
			if x.TriggerID == triggerID {
				return c.reason, nil
			}
		}
	}
	return "", nil
}

// startQueued starts the queued runs, oldest claim first, while fewer than
// MaxRunning run. A trigger has one running run at a time, so a queued run of
// a trigger that is running waits for it.
func (r *Runner) startQueued(ctx context.Context, now time.Time) error {
	running, err := r.o.Store.RunningRuns(ctx)
	if err != nil {
		return err
	}
	busy := map[string]bool{}
	for _, x := range running {
		busy[x.TriggerID] = true
	}
	queued, err := r.o.Store.QueuedRuns(ctx)
	if err != nil {
		return err
	}
	n := len(running)
	for _, run := range queued {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.stopped() {
			return ErrStopped
		}
		t, err := r.o.Store.GetTrigger(ctx, run.TriggerID)
		manual := strings.HasPrefix(run.DueKey, ManualPrefix)
		switch {
		case errors.Is(err, ErrNotFound):
			err = r.end(ctx, run, StatusSkipped, ReasonDropped, now)
		case err == nil && !t.Enabled && !manual:
			err = r.end(ctx, run, StatusSkipped, ReasonSwitchedOff, now)
		case err == nil && (n >= r.o.MaxRunning || busy[run.TriggerID]):
			continue
		case err == nil:
			var started bool
			started, err = r.begin(ctx, t, run, now)
			if started { // a failed start leaves its slot free for the next run
				busy[run.TriggerID] = true
				n++
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ManualPrefix starts the DueKey of a run that RunNow made. A host that cannot
// call RunNow (it does not lead) may claim a queued run with this prefix; the
// leader starts it.
const ManualPrefix = "manual@"

// ReasonSwitchedOff is the reason of a queued run whose trigger was switched
// off before the run started.
const ReasonSwitchedOff = "trigger switched off"

func (r *Runner) end(ctx context.Context, run Run, st Status, reason string, now time.Time) error {
	run.Status, run.Reason, run.Ended = st, CutReason(reason), now.UTC()
	r.endChat(ctx, run.ID)
	if err := r.o.Store.SetRunStatus(ctx, run); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// startContext is the context that Start gets: the one of Run while it goes,
// else Background. It never ends with the request of a RunNow.
func (r *Runner) startContext() context.Context {
	if r.runCtx != nil {
		return r.runCtx
	}
	return context.Background()
}

// callStart calls Start and turns a panic into an error.
func (r *Runner) callStart(t Trigger, run Run) (chatID string, panicked bool, err error) {
	defer func() {
		if p := recover(); p != nil {
			panicked = true
			r.logf("triggers: Start panicked for trigger %s: %v", t.ID, p)
			err = fmt.Errorf("could not start: %v", p)
		}
	}()
	chatID, err = r.o.Start(r.startContext(), t, run)
	return chatID, false, err
}

// begin marks the run running, calls Start, and records the chat. It tells
// whether the run is now running. A panic in Start fails the run; it does not
// leave it running. An error from Start while the Runner stops puts the run
// back to queued.
func (r *Runner) begin(ctx context.Context, t Trigger, run Run, now time.Time) (bool, error) {
	run.Status, run.Started = StatusRunning, now.UTC()
	if err := r.o.Store.SetRunStatus(ctx, run); err != nil {
		return false, err
	}
	chatID, panicked, err := r.callStart(t, run)
	var skip *SkipError
	switch {
	case errors.As(err, &skip):
		return false, r.end(ctx, run, StatusSkipped, skip.Reason, r.o.Clock())
	case err != nil && !panicked && (r.stopped() || r.startContext().Err() != nil):
		// Shutdown ended the start, not the work: put the run back, so that
		// the next Runner starts it. It keeps its claim, so the due time or
		// file version does not run twice, and it is still the one run of
		// its trigger, as a queued run counts as active.
		r.logf("triggers: run %s of trigger %s put back to queued, start ended with the Runner: %v", run.ID, t.ID, err)
		run.Status, run.Started = StatusQueued, time.Time{}
		if reErr := r.o.Store.SetRunStatus(context.WithoutCancel(ctx), run); reErr != nil {
			r.logf("triggers: run %s could not be put back to queued: %v", run.ID, reErr)
			return false, reErr
		}
		return false, nil
	case err != nil:
		if endErr := r.end(ctx, run, StatusFailed, err.Error(), r.o.Clock()); endErr != nil {
			r.logf("triggers: run %s could not be marked failed: %v", run.ID, endErr)
			return false, endErr
		}
		return false, nil
	}
	run.ChatID = chatID
	r.rememberChat(run)
	if err := r.o.Store.SetRunStatus(ctx, run); err != nil {
		// Start succeeded: one more try, so that the run is not left running
		// without its chat.
		rc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err2 := r.o.Store.SetRunStatus(rc, run); err2 != nil {
			r.logf("triggers: run %s started chat %s but the chat could not be recorded: %v", run.ID, chatID, err2)
			return true, err2
		}
	}
	return true, nil
}

// Finish ends a running run. status is StatusDone or StatusFailed. An empty
// reason keeps the reason the run has. Finish of a run that already ended, or
// that no longer exists, does nothing. A host calls it when the work of a run
// has ended, and never from Options.Start.
func (r *Runner) Finish(runID string, status Status, reason string) error {
	if !status.Final() {
		return fmt.Errorf("triggers: %q is not an end state", status)
	}
	ctx := context.Background()
	r.mu.Lock()
	run, err := r.o.Store.GetRun(ctx, runID)
	if err == nil && !run.Status.Final() {
		run.Status, run.Ended = status, r.o.Clock().UTC()
		r.endChat(ctx, run.ID)
		if reason != "" {
			run.Reason = CutReason(reason)
		}
		err = r.o.Store.SetRunStatus(ctx, run)
	}
	r.mu.Unlock()
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	r.Notify() // a slot is free
	return err
}

// RunNow claims a run of the trigger at once, whether the trigger is enabled
// or not, and starts it when a slot is free. It skips the daily limit. It
// returns the run; its status is skipped when the trigger has a run going.
func (r *Runner) RunNow(ctx context.Context, triggerID string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped() {
		return Run{}, ErrStopped
	}
	now := r.o.Clock()
	if err := r.ensureRecovered(ctx, now); err != nil {
		return Run{}, err
	}
	t, err := r.o.Store.GetTrigger(ctx, triggerID)
	if err != nil {
		return Run{}, err
	}
	id := newID()
	run := Run{ID: id, TriggerID: t.ID, DueKey: ManualPrefix + id, Due: now.UTC()}
	if err := r.admit(ctx, t, &run, now, true, true); err != nil {
		return Run{}, err
	}
	if _, err := r.o.Store.Claim(ctx, run); err != nil {
		return Run{}, err
	}
	if err := r.startQueued(ctx, now); err != nil {
		return Run{}, err
	}
	return r.o.Store.GetRun(ctx, run.ID)
}

// Cancel drops the queued runs of a trigger, stops its running run through
// Options.Stop, and forgets the files that wait for their quiet period. A
// host calls it before it deletes a trigger. The host must switch the trigger
// off or delete it before it calls Cancel, or a pass can start a new run
// after Cancel has looked.
func (r *Runner) Cancel(ctx context.Context, triggerID string) error {
	r.mu.Lock()
	now := r.o.Clock()
	if err := r.ensureRecovered(ctx, now); err != nil {
		r.mu.Unlock()
		return err
	}
	r.feed.forget(triggerID)
	var stop []string
	var firstErr error
	queued, err := r.o.Store.QueuedRuns(ctx)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	for _, run := range queued {
		if run.TriggerID == triggerID {
			if err := r.end(ctx, run, StatusSkipped, ReasonDropped, now); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	running, err := r.o.Store.RunningRuns(ctx)
	if err != nil && firstErr == nil {
		firstErr = err
	}
	for _, run := range running {
		if run.TriggerID == triggerID && run.ChatID != "" {
			stop = append(stop, run.ChatID)
		}
	}
	r.mu.Unlock()
	if r.o.Stop != nil {
		for _, id := range stop {
			r.o.Stop(id) // outside the lock: a stop can take seconds
		}
	}
	return firstErr
}

// ErrStopped is returned by Sync and RunNow once Run has returned: a stopped
// Runner starts nothing.
var ErrStopped = errors.New("triggers: the Runner has stopped")
