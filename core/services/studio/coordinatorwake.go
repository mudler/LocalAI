package studio

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// defaultWakeRunLimit bounds a wake turn when the options give no limit.
const defaultWakeRunLimit = 15 * time.Minute

// pruneEvery is how often one process prunes the audit log.
const pruneEvery = time.Hour

// wakeNoteDefault is what the coordinator reads when its creator wrote no
// note for the wake.
const wakeNoteDefault = "Read your state page and your topics, do the next slice of work, write your state, and stop."

// coordinatorWakeText is the message of a wake: when it was, why, and the note the
// creator wrote.
func coordinatorWakeText(cfg coordinator.Config, src WakeSource, at time.Time) string {
	why := string(src)
	if src == WakeOwner {
		why = "owner"
	}
	note := cfg.WakeNote
	if note == "" {
		note = wakeNoteDefault
	}
	return "[Coordinator wake: " + why + " at " + at.UTC().Format(time.RFC3339) + "]\n" + note
}

// wakeState is what the host keeps in memory for its wakes: the supervisors
// of the turns it started and the time of the last prune.
type wakeState struct {
	once sync.Once
	done chan struct{}
	wg   sync.WaitGroup

	mu        sync.Mutex
	shut      bool
	lastPrune time.Time
}

func (w *wakeState) init() { w.done = make(chan struct{}) }

// Close stops the supervisors of the wakes. A wake turn that goes on stays
// running: the reaper of the next leader settles it, as for any turn.
func (h *Coordinators) Close() {
	h.wake.mu.Lock()
	h.wake.shut = true
	h.wake.mu.Unlock()
	h.wake.once.Do(func() { close(h.wake.done) })
	h.wake.wg.Wait()
}

// spawn runs f as a supervisor, unless the host is closed.
func (h *Coordinators) spawn(f func()) bool {
	h.wake.mu.Lock()
	defer h.wake.mu.Unlock()
	if h.wake.shut {
		return false
	}
	h.wake.wg.Add(1)
	go func() {
		defer h.wake.wg.Done()
		f()
	}()
	return true
}

func (h *Coordinators) runLimit() time.Duration {
	if h.o.RunLimit != nil {
		if d := h.o.RunLimit(); d > 0 {
			return d
		}
	}
	return defaultWakeRunLimit
}

// Tick is the work of a leader pass: it wakes the coordinators that are due,
// starts the messages that wait for a coordinator that may run now, and prunes
// the audit log. It runs on the frontend that holds the lock of the reaper, so
// one frontend does it; every step is also safe when two run at once (the wake
// is a conditional claim in the store). A failure with one coordinator does not
// stop the others; the first one is returned.
func (h *Coordinators) Tick(ctx context.Context) error {
	cfgs, err := h.o.Store.List(ctx)
	if err != nil {
		return err
	}
	var first error
	keep := func(err error) {
		if err != nil && first == nil && !errors.Is(err, context.Canceled) {
			first = err
		}
	}
	now := h.now()
	for _, cfg := range cfgs {
		if ctx.Err() != nil {
			break
		}
		keep(h.tickWake(ctx, cfg, now))
		// Messages that wait for the budget, or that came while a wake was not
		// possible, start as soon as the coordinator may run.
		if !cfg.Paused {
			if _, err := h.Release(ctx, cfg); err != nil {
				h.log.Warn("studio: could not start the waiting messages of a coordinator", "coordinator", cfg.AgentID, "error", err)
				keep(err)
			}
		}
	}
	keep(h.pruneAudit(ctx, now))
	return first
}

// tickWake wakes cfg when it is due. The claim comes first: of many frontends
// only one wins it, and the next wake is set whatever happens after, so a
// coordinator that cannot run now is not retried every pass, and a period that
// was missed wakes once.
func (h *Coordinators) tickWake(ctx context.Context, cfg coordinator.Config, now time.Time) error {
	if cfg.Paused || cfg.WakeEvery <= 0 || cfg.NextWake.After(now) {
		return nil
	}
	won, err := h.o.Store.ClaimWake(ctx, cfg.AgentID, now, now.Add(cfg.WakeEvery))
	if err != nil || !won {
		return err
	}
	return h.Wake(ctx, cfg.AgentID, WakeSchedule)
}

// Wake posts the wake message into the chat of a coordinator and follows the
// turn. It does nothing, and says nothing, when the chat has a turn running or
// messages waiting: a wake does not pile up. A coordinator that may not run now
// (paused, over its budget, at the concurrency limit) gets one audit row with
// the reason. The chat is made again when it was deleted.
func (h *Coordinators) Wake(ctx context.Context, agentID string, src WakeSource) error {
	s := h.svc
	cfg, err := h.o.Store.Get(ctx, agentID)
	if errors.Is(err, coordinator.ErrNotFound) {
		return ErrNotACoordinator
	}
	if err != nil {
		return err
	}
	rec, err := h.EnsureChat(ctx, cfg)
	if err != nil {
		return err
	}
	if info, err := s.d.Store.Turn(ctx, cfg.Principal, rec.ID); err == nil && info.Running {
		return nil
	}
	if waiting, err := s.d.Store.Pending(ctx, cfg.Principal, rec.ID); err == nil && len(waiting) > 0 {
		return nil
	}
	if v := h.Check(ctx, rec, src); !v.Allow {
		h.auditWake(ctx, cfg, coordinator.OutcomeDenied, v.Reason)
		return nil
	}
	events, cancel, err := s.Events(ctx, cfg.Principal, rec.ID)
	if err != nil {
		return err
	}
	at := h.now()
	res, err := s.SendBackground(WithWake(ctx, src), cfg.Principal, rec.ID, coordinatorWakeText(cfg, src, at), nil)
	switch {
	case err != nil:
		cancel()
		return err
	case res.Held:
		// The gate changed between the check and the send (a pause, or another
		// frontend took the last run). The message waits; say why.
		cancel()
		reason := coordinator.ReasonBudget
		if v := h.Check(ctx, rec, src); v.Reason != "" {
			reason = v.Reason
		}
		h.auditWake(ctx, cfg, coordinator.OutcomeDenied, reason)
		return nil
	case !res.Started:
		cancel() // it joined a turn that started meanwhile
		return nil
	}
	h.auditWake(ctx, cfg, coordinator.OutcomeInfo, coordinator.ReasonOK)
	limit := h.runLimit()
	if !h.spawn(func() { h.supervise(cfg, rec.ID, events, cancel, limit) }) {
		cancel()
	}
	return nil
}

// supervise follows a wake turn and stops it when it overruns the limit.
func (h *Coordinators) supervise(cfg coordinator.Config, chatID string, events <-chan session.Event, cancel func(), limit time.Duration) {
	defer cancel()
	end := h.svc.awaitTurn(cfg.Principal, chatID, events, limit, h.wake.done)
	if end.How != TurnTimedOut {
		return
	}
	ctx, stop := context.WithTimeout(context.Background(), stopWait)
	defer stop()
	if err := h.svc.Stop(ctx, cfg.Principal, chatID); err != nil && !errors.Is(err, session.ErrNotFound) {
		h.log.Warn("studio: could not stop a wake turn of a coordinator that overran", "coordinator", cfg.AgentID, "error", err)
	}
	h.auditWake(ctx, cfg, coordinator.OutcomeDenied, coordinator.ReasonTimeout)
}

func (h *Coordinators) auditWake(ctx context.Context, cfg coordinator.Config, outcome, reason string) {
	h.record(ctx, coordinator.AuditEntry{At: h.now(), CoordinatorID: cfg.AgentID, Principal: cfg.Principal,
		Kind: coordinator.KindWake, Outcome: outcome, Reason: reason, Actor: "coordinator", Via: string(WakeSchedule)})
}

// pruneAudit drops the audit rows and counters older than the audit days. It
// runs at most once an hour in a process.
func (h *Coordinators) pruneAudit(ctx context.Context, now time.Time) error {
	h.wake.mu.Lock()
	if !h.wake.lastPrune.IsZero() && now.Sub(h.wake.lastPrune) < pruneEvery {
		h.wake.mu.Unlock()
		return nil
	}
	h.wake.lastPrune = now
	h.wake.mu.Unlock()
	_, err := h.o.Store.Prune(ctx, now.AddDate(0, 0, -h.Limits().AuditDays))
	return err
}
