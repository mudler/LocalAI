package upgraderun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/distributed"
)

// runner runs one run on this frontend. Its goroutine is the only writer of
// the run: effects run in their own goroutines and come back as events.
type runner struct {
	svc  *Service
	id   string
	lock *advisorylock.HeldLock

	events chan Event
	poke   chan struct{}
	ctx    context.Context
	stop   context.CancelFunc
	done   chan struct{}

	mu  sync.Mutex
	run Run

	// Only the runner goroutine touches these.
	lastSaved  string
	lastPhase  map[string]string
	missing    map[string]time.Time
	seen       map[string]bool
	lastTouch  time.Time
	lastVerify time.Time
	// recovered are the items a takeover follows, with the time their
	// status last changed and that status.
	recovered    map[string]time.Time
	recoveredSig map[string]string
}

func newRunner(svc *Service, run Run, lock *advisorylock.HeldLock) *runner {
	// The checks and the job paths of the run act for its actor, and the
	// starters know its source.
	ctx, stop := context.WithCancel(WithSource(WithActor(svc.ctx, run.Actor), run.Source))
	return &runner{
		svc: svc, id: run.ID, lock: lock, run: run,
		events: make(chan Event, 64), poke: make(chan struct{}, 1),
		ctx: ctx, stop: stop, done: make(chan struct{}),
		lastPhase: map[string]string{}, missing: map[string]time.Time{}, seen: map[string]bool{},
		recovered: map[string]time.Time{}, recoveredSig: map[string]string{},
	}
}

// snapshot returns a copy of the run as the runner sees it now.
func (r *runner) snapshot() Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.run.Clone()
}

// post hands an event to the runner; it is dropped once the runner stopped.
func (r *runner) post(ev Event) {
	select {
	case r.events <- ev:
	case <-r.done:
	}
}

// wake makes the runner read the flags of other frontends now.
func (r *runner) wake() {
	select {
	case r.poke <- struct{}{}:
	default:
	}
}

// loop runs the run until it ends, the lock is lost or the service stops.
// first are the events to apply before anything else (RestartRecovered for a
// run taken over) and initial the effects of the step that created the run.
func (r *runner) loop(first []Event, initial []Effect) {
	defer close(r.done)
	defer r.svc.dropRunner(r)
	defer r.lock.Close()
	defer r.stop()

	r.save(r.snapshot())
	r.execute(initial)
	for _, ev := range first {
		if rr, ok := ev.(RestartRecovered); ok {
			for key := range rr.Live {
				r.recovered[key] = r.svc.now()
			}
		}
		r.apply(ev)
	}
	t := time.NewTicker(r.svc.opts.PollInterval)
	defer t.Stop()
	for r.snapshot().Active() {
		select {
		case <-r.ctx.Done():
			return
		case ev := <-r.events:
			r.apply(ev)
		case <-r.poke:
			r.readFlags()
		case <-t.C:
			if !r.tick() {
				return
			}
		}
	}
}

// apply runs one event through the reducer, stores the result, tells the
// observers and executes the effects.
func (r *runner) apply(ev Event) {
	cur := r.snapshot()
	res := Reduce(cur, ev, Env{Config: r.svc.config(), Now: r.svc.now()})
	if res.Err != nil || !res.Changed {
		if res.Err != nil {
			xlog.Debug("upgrade run ignored an event", "run", r.id, "event", ev, "error", res.Err)
		}
		return
	}
	// Store first, then show: a reader that sees the run ended must find it
	// ended in the store too, or a start right after it is refused.
	r.save(res.Run)
	if r.ctx.Err() != nil {
		// Fenced by another frontend, or stopping: nothing of this step is
		// shown or done here.
		return
	}
	r.mu.Lock()
	r.run = res.Run
	r.mu.Unlock()
	r.svc.notify(Change{Run: res.Run.Clone(), Key: keyOf(ev), Local: true})
	r.svc.publish(r.id)
	r.execute(res.Effects)
}

func keyOf(ev Event) string {
	switch e := ev.(type) {
	case ItemStarted:
		return e.Key
	case ItemStartFailed:
		return e.Key
	case ItemProgress:
		return e.Key
	case ItemJobEnded:
		return e.Key
	case ItemVerified:
		return e.Key
	case CancelItem:
		return e.Key
	case NodeResult:
		return e.Key
	}
	return ""
}

// save writes the run when what is stored changed. Progress ticks change
// nothing that is stored, so they cost no write.
func (r *runner) save(run Run) {
	rec, items, err := toRecords(run, r.svc.opts.Instance)
	if err != nil {
		xlog.Warn("upgrade run: could not store the run", "run", r.id, "error", err)
		return
	}
	sig := signature(rec, items)
	if sig == r.lastSaved {
		return
	}
	if err := r.svc.opts.Store.Save(rec, items); err != nil {
		if errors.Is(err, distributed.ErrUpgradeRunFenced) {
			// Another frontend took the run over: this runner lost its lock
			// and stops without writing.
			xlog.Warn("upgrade run: another frontend runs the run now; stopping here", "run", r.id)
			r.stop()
			return
		}
		xlog.Warn("upgrade run: could not store the run", "run", r.id, "error", err)
		return
	}
	r.lastSaved = sig
}

// signature is what save compares; it is taken before Save stamps the
// update times.
func signature(rec any, items any) string {
	b, _ := json.Marshal([]any{rec, items})
	return string(b)
}

// spawn runs fn on its own goroutine, counted by the service so Close waits
// for it. It is only called from the goroutine of the runner, which the
// service counts too, so the count is never zero while it adds.
func (r *runner) spawn(fn func()) {
	r.svc.wg.Add(1)
	go func() {
		defer r.svc.wg.Done()
		fn()
	}()
}

func (r *runner) execute(fx []Effect) {
	for _, f := range fx {
		switch e := f.(type) {
		case StartJob:
			it := r.snapshot().Item(e.Key)
			if it == nil {
				continue
			}
			item := *it
			r.spawn(func() { r.startJob(item) })
		case CancelJob:
			r.spawn(func() { r.cancelJob(e) })
		case VerifyItem:
			it := r.snapshot().Item(e.Key)
			if it == nil {
				continue
			}
			item := *it
			r.spawn(func() { r.verify(item) })
		case RunEnded:
			xlog.Info("upgrade run ended", "run", r.id, "state", e.State)
		}
	}
}

// startJob checks that the target of the item did not move, then starts its
// operation.
func (r *runner) startJob(it Item) {
	actor := r.snapshot().Actor
	if ev, stop := r.svc.precheck(r.ctx, it); stop {
		r.post(ev)
		return
	}
	starter := r.svc.opts.Starters[it.Kind]
	if starter == nil {
		r.post(ItemStartFailed{Key: it.Key, ErrorKey: ErrKeyUnknown, Error: "updates of this kind are not available on this server"})
		return
	}
	id, err := starter.Start(r.ctx, actor, it)
	if err != nil {
		r.post(startFailure(it.Key, err))
		return
	}
	cancellable := false
	if st := r.svc.opts.Jobs.GetStatus(id); st != nil {
		cancellable = st.Cancellable && !st.Processed
	}
	r.post(ItemStarted{Key: it.Key, JobID: id, Cancellable: cancellable})
}

func (r *runner) cancelJob(e CancelJob) {
	if err := r.svc.opts.Jobs.CancelOperation(e.JobID); err != nil {
		xlog.Warn("upgrade run: the operation refused the cancel", "run", r.id, "item", e.Key, "job", e.JobID, "error", err)
	}
}

// verify asks the checks whether the item is still listed.
func (r *runner) verify(it Item) {
	r.post(ItemVerified{Key: it.Key, Result: r.svc.verify(r.ctx, it)})
}

// tick follows the operations of the running items, reads the flags of other
// frontends, touches the operations so the stale reaper leaves them alone and
// checks that this frontend still holds the run. It returns false when the
// lock was lost: another frontend may take the run over.
func (r *runner) tick() bool {
	now := r.svc.now()
	run := r.snapshot()
	var busy []string
	for _, it := range run.Items {
		if it.JobID != "" && it.Busy() {
			busy = append(busy, it.JobID)
		}
		if it.Phase != PhaseRunning || it.Verifying || it.JobID == "" {
			continue
		}
		r.follow(it, now)
	}
	r.readFlags()
	if t := r.svc.opts.Toucher; t != nil && len(busy) > 0 && now.Sub(r.lastTouch) >= r.svc.opts.TouchInterval {
		if err := t.TouchActive(busy); err != nil {
			xlog.Debug("upgrade run: could not touch its operations", "run", r.id, "error", err)
		}
		r.lastTouch = now
	}
	if now.Sub(r.lastVerify) >= r.svc.opts.LockCheckInterval {
		r.lastVerify = now
		if !r.lock.Verify(r.ctx) {
			xlog.Warn("upgrade run: lost the lock of the run; another frontend takes it over", "run", r.id)
			return false
		}
	}
	return true
}

// follow reads the status of the operation of one running item.
func (r *runner) follow(it Item, now time.Time) {
	st := r.svc.opts.Jobs.GetStatus(it.JobID)
	v := viewOf(it.Kind, st, r.lastPhase[it.Key])
	if !v.known {
		// The status is written when the operation is admitted, so a status
		// that never shows, or goes away, means its outcome is lost.
		first, ok := r.missing[it.Key]
		if !ok {
			r.missing[it.Key] = now
			first = now
		}
		if r.seen[it.Key] || now.Sub(first) >= r.svc.opts.LostAfter {
			r.apply(ItemJobEnded{Key: it.Key, Outcome: JobLost})
		}
		return
	}
	r.seen[it.Key] = true
	delete(r.missing, it.Key)
	if since, ok := r.recovered[it.Key]; ok && !v.ended {
		sig := fmt.Sprintf("%s|%d|%s|%v", v.phase, v.progress, v.message, v.nodes)
		switch {
		case sig != r.recoveredSig[it.Key]:
			r.recovered[it.Key], r.recoveredSig[it.Key] = now, sig
		case now.Sub(since) >= r.svc.opts.RecoveredIdleAfter:
			// The status did not move since the takeover: the frontend that
			// ran it is gone, and so is the operation.
			delete(r.recovered, it.Key)
			r.apply(ItemJobEnded{Key: it.Key, Outcome: JobLost})
			return
		}
	} else if v.ended {
		delete(r.recovered, it.Key)
	}
	if v.phase != "" {
		r.lastPhase[it.Key] = v.phase
	}
	if v.ended {
		r.apply(ItemJobEnded{Key: it.Key, Outcome: v.outcome, ErrorKey: v.errorKey, Error: v.err, RolledBack: v.rolledBack, Nodes: v.nodes})
		return
	}
	msg := v.message
	if msg == "" {
		msg = it.Message
	}
	if clampProgress(v.progress) != it.Progress || msg != it.Message || v.cancellable != it.Cancellable || !reflect.DeepEqual(v.nodes, it.Nodes) {
		r.apply(ItemProgress{Key: it.Key, Progress: v.progress, Message: msg, Cancellable: v.cancellable, Nodes: v.nodes})
	}
}

// readFlags applies the cancels other frontends wrote.
func (r *runner) readFlags() {
	rec, items, err := r.svc.opts.Store.Get(r.id)
	if err != nil {
		return
	}
	cancelRun, keys := cancelFlags(rec, items)
	if cancelRun && !r.snapshot().CancelRequested {
		r.apply(CancelRequested{})
	}
	for _, k := range keys {
		it := r.snapshot().Item(k)
		if it == nil || it.Phase.Terminal() || it.CancelSent {
			continue
		}
		r.apply(CancelItem{Key: k})
	}
}
