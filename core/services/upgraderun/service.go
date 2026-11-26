package upgraderun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"
)

// SubjectChanged is the broadcast that a run changed. Every frontend
// subscribes: the runner of the run reads the flags of other frontends at
// once instead of at its next tick, and the observers of other frontends
// learn of the change.
const SubjectChanged = "gallery.upgraderun.changed"

// changedEvent is the payload of SubjectChanged.
type changedEvent struct {
	RunID    string `json:"run_id"`
	Instance string `json:"instance"`
}

// Change is what an Observer is told.
type Change struct {
	// Run is the run after the change.
	Run Run
	// Key is the item that changed, or "" for the run itself.
	Key string
	// Local is true on the frontend that runs the run, which sees every
	// change; another frontend sees a change of the run as a whole (Key is
	// empty), told over the bus. Act on Local changes for what must happen
	// once per cluster, such as a notification.
	Local bool
}

// Observer is told of every change of a run. It is called on the goroutine
// of the runner and must return quickly; throttling is its own business.
type Observer interface {
	RunChanged(Change)
}

// ObserverFunc adapts a function to an Observer.
type ObserverFunc func(Change)

func (f ObserverFunc) RunChanged(c Change) { f(c) }

// OperationToucher keeps the operations of a live run from going stale
// (distributed.GalleryStore implements it).
type OperationToucher interface {
	TouchActive(ids []string) error
}

// Options configure a Service.
type Options struct {
	// Store keeps the runs. Required.
	Store *distributed.UpgradeRunStore
	// DB takes the advisory locks; the database of Store when nil.
	DB *gorm.DB
	// Bus tells the other frontends of a change. Optional (standalone).
	Bus messaging.Broadcaster
	// Instance names this frontend in the run record and on the bus.
	Instance string
	// Jobs reads and cancels the operations. Required.
	Jobs Jobs
	// Starters start the operation of an item, by kind.
	Starters map[Kind]Starter
	// Checkers list the items with an update. Each kind is listed by one.
	Checkers []Checker
	// Observers are told of every change of a run from the start, also of
	// the first change of a run that Run takes over at once. AddObserver
	// adds one later.
	Observers []Observer
	// Toucher keeps the operations of a live run from being reaped. Set it
	// to the gallery store in distributed mode.
	Toucher OperationToucher
	// Config returns the limits; read at every step so a settings change
	// applies to running runs. Nil uses a backend limit of 2.
	Config func() Config

	// PollInterval is how often a runner reads the operation statuses.
	PollInterval time.Duration
	// SweepInterval is how often a frontend looks for runs nobody runs.
	SweepInterval time.Duration
	// LostAfter is how long a started operation may have no status before
	// its outcome counts as lost.
	LostAfter time.Duration
	// RecoveredIdleAfter is how long the operation of an item that a takeover
	// follows (its status was still there) may show no change before its
	// outcome counts as lost. A frontend that lost its lock keeps its
	// download going, which changes the status; the status of a frontend
	// that died stays as it was on the others.
	RecoveredIdleAfter time.Duration
	// TouchInterval is how often a runner touches its operations.
	TouchInterval time.Duration
	// Retention is how long and how many finished runs are kept. Nil uses
	// DefaultRetention.
	Retention *Retention
	// PruneInterval is how often the sweep removes the finished runs beyond
	// the retention.
	PruneInterval time.Duration
	// LockCheckInterval is how often a runner checks it still holds its run.
	LockCheckInterval time.Duration
	// VerifyTries and VerifyRetry bound the fresh checks of one item.
	VerifyTries int
	VerifyRetry time.Duration
	// Now is the clock. Nil uses time.Now.
	Now func() time.Time
}

func (o *Options) defaults() {
	if o.DB == nil && o.Store != nil {
		o.DB = o.Store.DB()
	}
	if o.PollInterval <= 0 {
		o.PollInterval = time.Second
	}
	if o.SweepInterval <= 0 {
		o.SweepInterval = 15 * time.Second
	}
	if o.LostAfter <= 0 {
		o.LostAfter = 30 * time.Second
	}
	if o.RecoveredIdleAfter <= 0 {
		o.RecoveredIdleAfter = 2 * time.Minute
	}
	if o.TouchInterval <= 0 {
		o.TouchInterval = 5 * time.Minute
	}
	if o.Retention == nil {
		r := DefaultRetention
		o.Retention = &r
	}
	if o.PruneInterval <= 0 {
		o.PruneInterval = time.Hour
	}
	if o.LockCheckInterval <= 0 {
		o.LockCheckInterval = 5 * time.Second
	}
	if o.VerifyTries <= 0 {
		o.VerifyTries = 4
	}
	if o.VerifyRetry <= 0 {
		o.VerifyRetry = 5 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Starters == nil {
		o.Starters = map[Kind]Starter{}
	}
}

// Retention bounds the finished runs the store keeps.
type Retention struct {
	// MaxAge removes a finished run that ended longer ago. Zero or less keeps
	// runs of any age.
	MaxAge time.Duration
	// Keep is the most finished runs kept: the oldest beyond it are removed.
	// A negative value keeps any number.
	Keep int
}

// DefaultRetention keeps finished runs for 30 days, and at most the newest
// 200 of them.
var DefaultRetention = Retention{MaxAge: 30 * 24 * time.Hour, Keep: 200}

// Service owns the upgrade runs of this frontend and answers for every run of
// the cluster.
type Service struct {
	opts Options
	ctx  context.Context
	quit context.CancelFunc

	mu        sync.Mutex
	lastPrune time.Time
	runners   map[string]*runner
	observers []Observer
	sub       messaging.Subscription
	wg        sync.WaitGroup
	closeOnce sync.Once
	// corrupt holds the damaged columns already logged, so a run that is
	// read on every poll of the Updates page logs each of them once.
	corrupt sync.Map
}

// New creates a Service. Call Run to take over runs nobody runs.
func New(opts Options) (*Service, error) {
	if opts.Store == nil || opts.Jobs == nil {
		return nil, errors.New("upgraderun: a store and the jobs are required")
	}
	opts.defaults()
	ctx, quit := context.WithCancel(context.Background())
	s := &Service{opts: opts, ctx: ctx, quit: quit, runners: map[string]*runner{}, observers: append([]Observer(nil), opts.Observers...)}
	if opts.Bus != nil {
		sub, err := opts.Bus.Subscribe(SubjectChanged, s.onChanged)
		if err != nil {
			quit()
			return nil, fmt.Errorf("upgraderun: subscribe: %w", err)
		}
		s.sub = sub
	}
	return s, nil
}

// AddObserver adds an observer of the changes of runs.
func (s *Service) AddObserver(o Observer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, o)
}

// Run takes over, every SweepInterval, the runs that nobody runs: after a
// restart of this frontend, or when the frontend that ran them died. Every
// PruneInterval it also removes the finished runs beyond the retention. It
// returns when ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.opts.SweepInterval)
	defer t.Stop()
	for {
		s.Sweep(ctx)
		s.maybePrune(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Sweep takes over the runs that nobody runs, once.
func (s *Service) Sweep(ctx context.Context) {
	ids, err := s.opts.Store.ListActive()
	if err != nil {
		xlog.Debug("upgrade run sweep: could not list the runs", "error", err)
		return
	}
	for _, id := range ids {
		if s.runnerOf(id) != nil {
			continue
		}
		s.adopt(ctx, id)
	}
}

func (s *Service) maybePrune(ctx context.Context) {
	s.mu.Lock()
	due := s.now().Sub(s.lastPrune) >= s.opts.PruneInterval
	if due {
		s.lastPrune = s.now()
	}
	s.mu.Unlock()
	if !due {
		return
	}
	if n, err := s.Prune(ctx); err != nil {
		xlog.Debug("upgrade run: could not remove old runs", "error", err)
	} else if n > 0 {
		xlog.Info("upgrade run: removed finished runs beyond the retention", "count", n)
	}
}

// Prune removes the finished runs beyond the retention, once, and returns
// how many it removed. It runs under the admission lock of the runs, so one
// frontend at a time prunes and no run starts meanwhile; when another
// frontend holds that lock it does nothing, and the next sweep tries again.
// A running run is never removed.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	ret := *s.opts.Retention
	if ret.MaxAge <= 0 && ret.Keep < 0 {
		return 0, nil
	}
	var n int64
	_, err := advisorylock.TryWithLockCtx(ctx, s.opts.DB, advisorylock.KeyUpgradeRuns, func() error {
		var err error
		n, err = s.opts.Store.Prune(ret.MaxAge, ret.Keep)
		return err
	})
	return n, err
}

// adopt runs a run whose lock is free. Its running items become unconfirmed
// until a fresh check decides.
func (s *Service) adopt(ctx context.Context, id string) {
	lock := advisorylock.NewHeldLock(s.opts.DB, runLockKey(id))
	ok, err := lock.TryAcquire(ctx)
	if err != nil || !ok {
		lock.Close()
		return
	}
	rec, items, err := s.opts.Store.Get(id)
	if err != nil || rec.State != string(RunRunning) {
		lock.Close()
		return
	}
	// From here on the previous runner, if it still lives without its lock,
	// cannot overwrite the run.
	if err := s.opts.Store.Claim(id, s.opts.Instance); err != nil {
		lock.Close()
		return
	}
	run := s.decode(rec, items)
	xlog.Info("upgrade run: taking over a run nobody runs", "run", id, "previous_runner", rec.Runner)
	s.startRunner(run, lock, []Event{RestartRecovered{Live: s.liveItems(run)}}, nil)
}

// liveItems are the running items whose operation still has a status. A
// runner that lost its lock leaves its operations running, and the gallery
// operations keep going on the frontend that started them.
func (s *Service) liveItems(run Run) map[string]bool {
	live := map[string]bool{}
	for _, it := range run.Items {
		if it.Phase != PhaseRunning || it.Verifying || it.JobID == "" {
			continue
		}
		if s.opts.Jobs.GetStatus(it.JobID) != nil {
			live[it.Key] = true
		}
	}
	return live
}

// Close stops the runners of this frontend. Their runs stay running in the
// store; this frontend after a restart, or another one, takes them over.
func (s *Service) Close() {
	s.closeOnce.Do(func() {
		s.quit()
		if s.sub != nil {
			_ = s.sub.Unsubscribe()
		}
	})
	s.wg.Wait()
}

// Local returns the ids of the runs this frontend runs now.
func (s *Service) Local() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.runners))
	for id := range s.runners {
		ids = append(ids, id)
	}
	return ids
}

func runLockKey(id string) int64 { return advisorylock.KeyFromString("upgrade-run:" + id) }

func (s *Service) config() Config {
	if s.opts.Config != nil {
		return s.opts.Config()
	}
	return Config{BackendLimit: 2, ModelLimit: 1}
}

func (s *Service) now() time.Time { return s.opts.Now() }

func (s *Service) runnerOf(id string) *runner {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runners[id]
}

func (s *Service) startRunner(run Run, lock *advisorylock.HeldLock, first []Event, initial []Effect) {
	r := newRunner(s, run, lock)
	s.mu.Lock()
	s.runners[run.ID] = r
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		r.loop(first, initial)
	}()
}

func (s *Service) dropRunner(r *runner) {
	s.mu.Lock()
	if s.runners[r.id] == r {
		delete(s.runners, r.id)
	}
	s.mu.Unlock()
}

func (s *Service) notify(c Change) {
	s.mu.Lock()
	obs := append([]Observer(nil), s.observers...)
	s.mu.Unlock()
	for _, o := range obs {
		o.RunChanged(c)
	}
}

func (s *Service) publish(runID string) {
	if s.opts.Bus == nil {
		return
	}
	if err := s.opts.Bus.Publish(SubjectChanged, changedEvent{RunID: runID, Instance: s.opts.Instance}); err != nil {
		xlog.Debug("upgrade run: could not publish a change", "run", runID, "error", err)
	}
}

// onChanged handles the broadcast of a change on another frontend.
func (s *Service) onChanged(data []byte) {
	var ev changedEvent
	if json.Unmarshal(data, &ev) != nil || ev.RunID == "" || ev.Instance == s.opts.Instance {
		return
	}
	if r := s.runnerOf(ev.RunID); r != nil {
		r.wake()
		return
	}
	run, err := s.Get(s.ctx, ev.RunID)
	if err != nil {
		return
	}
	s.notify(Change{Run: run, Local: false})
}

// checkerFor returns the checker of a kind.
func (s *Service) checkerFor(k Kind) Checker {
	for _, c := range s.opts.Checkers {
		if slices.Contains(c.Kinds(), k) {
			return c
		}
	}
	return nil
}

// candidates lists every checker. unchecked holds the kinds whose checker did
// not answer, so a missing item of such a kind may still have an update.
func (s *Service) candidates(ctx context.Context) ([]Candidate, map[Kind]bool) {
	var all []Candidate
	unchecked := map[Kind]bool{}
	for _, c := range s.opts.Checkers {
		l, err := c.List(ctx, false)
		if err != nil {
			xlog.Debug("upgrade run: a check failed", "kinds", c.Kinds(), "error", err)
		}
		if err != nil || !l.Complete {
			for _, k := range c.Kinds() {
				unchecked[k] = true
			}
		}
		all = append(all, l.Candidates...)
	}
	return all, unchecked
}

// precheck stops an item whose target moved since the request, before its
// operation starts. A source that does not answer does not stop it: the
// operation checks again.
func (s *Service) precheck(ctx context.Context, it Item) (Event, bool) {
	c := s.checkerFor(it.Kind)
	if c == nil {
		return nil, false
	}
	l, err := c.List(ctx, false)
	if err != nil || !l.Complete {
		return nil, false
	}
	for _, cand := range l.Candidates {
		if cand.Key() != it.Key {
			continue
		}
		if it.ExpectedTo != "" && cand.pin() != it.ExpectedTo {
			return ItemStartFailed{Key: it.Key, ErrorKey: ErrKeyStale, Retryable: true,
				Error: fmt.Sprintf("the update moved to %q since the run was requested", cand.To)}, true
		}
		if r := skipReason(cand); r != "" {
			key := ErrKeyHeld
			if r == ReasonEditedKeys {
				key = ErrKeyEditedKeys
			}
			return ItemStartFailed{Key: it.Key, ErrorKey: key, Skip: true, Error: "the item cannot be updated by a run now: " + r}, true
		}
		return nil, false
	}
	return ItemStartFailed{Key: it.Key, ErrorKey: ErrKeyStale, Skip: true, Error: "the item no longer has an update"}, true
}

// verify asks for fresh checks until one decides whether the item is still
// listed. A throttled answer that still lists it may predate the update, so
// it is asked again.
func (s *Service) verify(ctx context.Context, it Item) VerifyResult {
	c := s.checkerFor(it.Kind)
	if c == nil {
		return VerifyUnanswered
	}
	for try := 0; try < s.opts.VerifyTries; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return VerifyUnanswered
			case <-time.After(s.opts.VerifyRetry):
			}
		}
		l, err := c.List(ctx, true)
		if err != nil || !l.Complete {
			continue
		}
		listed := false
		for _, cand := range l.Candidates {
			if cand.Key() != it.Key {
				continue
			}
			// Listed with another target: the item moved to what the run
			// asked for and a newer release came out since.
			listed = it.ExpectedTo == "" || cand.pin() == it.ExpectedTo
		}
		if !listed {
			return VerifyGone
		}
		if l.Throttled && try < s.opts.VerifyTries-1 {
			continue
		}
		return VerifyListed
	}
	return VerifyUnanswered
}

// view returns a run: from the runner when this frontend runs it, otherwise
// from the store with the progress of each running item read from its
// operation.
func (s *Service) view(rec *distributed.UpgradeRunRecord, items []distributed.UpgradeRunItemRecord) Run {
	if r := s.runnerOf(rec.ID); r != nil {
		return r.snapshot()
	}
	run := s.decode(rec, items)
	for i := range run.Items {
		it := &run.Items[i]
		if it.Phase != PhaseRunning || it.Verifying || it.JobID == "" {
			continue
		}
		v := viewOf(it.Kind, s.opts.Jobs.GetStatus(it.JobID), "")
		if !v.known || v.ended {
			continue
		}
		it.Progress, it.Cancellable, it.Nodes = clampProgress(v.progress), v.cancellable, v.nodes
		if v.message != "" {
			it.Message = v.message
		}
	}
	return run
}

// decode turns rows into a run, as fromRecords does, and logs each damaged
// column of a row once.
func (s *Service) decode(rec *distributed.UpgradeRunRecord, items []distributed.UpgradeRunItemRecord) Run {
	run, err := fromRecords(rec, items)
	if err == nil {
		return run
	}
	var errs []error
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	} else {
		errs = []error{err}
	}
	for _, e := range errs {
		var ce *corruptColumnError
		if !errors.As(e, &ce) {
			continue
		}
		if _, seen := s.corrupt.LoadOrStore(ce.run+"\x00"+ce.item+"\x00"+ce.column, struct{}{}); seen {
			continue
		}
		xlog.Warn("upgrade run: a stored item has a column that is not valid JSON; it is read without it",
			"run", ce.run, "item", ce.item, "column", ce.column)
	}
	return run
}

// activeRun returns the run that runs now, if any.
func (s *Service) activeRun() (*Run, error) {
	ids, err := s.opts.Store.ListActive()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	rec, items, err := s.opts.Store.Get(ids[0])
	if err != nil {
		return nil, err
	}
	run := s.view(rec, items)
	return &run, nil
}

// Preview orders the items with an update into a plan, without starting
// anything. refs limits it to some items; empty lists them all. A start
// request built from it sends each ExpectedTo back.
func (s *Service) Preview(ctx context.Context, actor string, refs []ItemRef) (Plan, error) {
	cands, unchecked := s.candidates(WithActor(ctx, actor))
	busy := map[string]bool{}
	active, err := s.activeRun()
	if err != nil {
		return Plan{}, err
	}
	if active != nil {
		for _, it := range active.Items {
			if it.Phase == PhaseQueued || it.Busy() {
				busy[it.Key] = true
			}
		}
	}
	plan := BuildPlan(cands, refs, busy)
	plan.Complete = len(unchecked) == 0
	if active != nil {
		plan.ActiveRunID = active.ID
	}
	return plan, nil
}

// RefuseIfActive answers run_active while a run runs in the cluster. A start
// asks it first, before it checks the items, so a caller that falls back to a
// direct upgrade when the run's check does not list its item never does so
// beside a run. The routes that upgrade one item outside a run (a model
// upgrade with options, a model retry) ask it before they queue anything.
func (s *Service) RefuseIfActive() error {
	ids, err := s.opts.Store.ListActive()
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		return refuse(CodeRunActive, "", "another upgrade run is running (%s)", ids[0])
	}
	return nil
}

// Start validates every item (installed, an update available, the target the
// caller saw still current) and starts a run. One run runs at a time in the
// cluster; a start while one runs is refused with run_active, before the
// items are checked. A held model or one whose update changes edited keys is
// part of the run as skipped.
func (s *Service) Start(ctx context.Context, actor string, req StartRequest) (Run, error) {
	if req.Source == "" {
		req.Source = SourceManual
	}
	if req.Source != SourceManual && req.Source != SourceAuto {
		return Run{}, refuse(CodeInvalid, "", "unknown source %q", req.Source)
	}
	if err := s.RefuseIfActive(); err != nil {
		return Run{}, err
	}
	cands, unchecked := s.candidates(WithActor(ctx, actor))
	items, err := planStart(cands, unchecked, req)
	if err != nil {
		return Run{}, err
	}
	if req.Source == SourceAuto {
		// A release that did not start is not tried again at each pass: the
		// pass would run its start check, roll it back and write a new item
		// in the inbox every time.
		held, err := s.HeldBack(ctx)
		if err != nil {
			return Run{}, err
		}
		var dropped []string
		items, dropped = dropHeldBack(items, held)
		if len(dropped) > 0 {
			xlog.Info("upgrade run: the automatic update leaves out releases that did not start before", "items", dropped)
		}
		if len(items) == 0 {
			return Run{}, refuse(CodeHeldBack, "", "every item is a release that did not start before; start it by hand to try again")
		}
	}
	return s.create(ctx, actor, req.Source, items)
}

// create stores and starts a run, under the admission lock so two frontends
// never start two runs at once. The lock of the run is taken before its row
// is written: the sweep of another frontend, which does not take the
// admission lock, can then never take over a run while it is created.
func (s *Service) create(ctx context.Context, actor string, source Source, items []Item) (Run, error) {
	var out Run
	err := advisorylock.WithLockCtx(ctx, s.opts.DB, advisorylock.KeyUpgradeRuns, func() error {
		active, err := s.opts.Store.ListActive()
		if err != nil {
			return err
		}
		if len(active) > 0 {
			return refuse(CodeRunActive, "", "another upgrade run is running (%s)", active[0])
		}
		res := Reduce(Run{}, RunRequested{ID: uuid.NewString(), Source: source, Actor: actor, Items: items}, Env{Config: s.config(), Now: s.now()})
		if res.Err != nil {
			return res.Err
		}
		lock := advisorylock.NewHeldLock(s.opts.DB, runLockKey(res.Run.ID))
		if ok, err := lock.TryAcquire(ctx); err != nil || !ok {
			lock.Close()
			if err == nil {
				err = errors.New("the lock of a new run is taken")
			}
			return fmt.Errorf("upgrade run: could not take the lock of a new run: %w", err)
		}
		rec, recItems, err := toRecords(res.Run, s.opts.Instance)
		if err != nil {
			lock.Close()
			return err
		}
		if err := s.opts.Store.Create(rec, recItems); err != nil {
			lock.Close()
			return err
		}
		s.startRunner(res.Run, lock, nil, res.Effects)
		out = res.Run
		return nil
	})
	if err != nil {
		return Run{}, err
	}
	s.notify(Change{Run: out.Clone(), Local: true})
	s.publish(out.ID)
	return out, nil
}

// Get returns a run.
func (s *Service) Get(_ context.Context, id string) (Run, error) {
	rec, items, err := s.opts.Store.Get(id)
	if err != nil {
		return Run{}, storeError(err, "")
	}
	return s.view(rec, items), nil
}

// ListOptions limit List.
type ListOptions struct {
	// Limit is the most runs to return; 0 returns all.
	Limit int
	// WithDismissed includes the dismissed runs.
	WithDismissed bool
}

// List returns the runs, the running one first, then the newest.
func (s *Service) List(_ context.Context, opts ListOptions) ([]Run, error) {
	recs, items, err := s.opts.Store.List(opts.Limit, opts.WithDismissed)
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(recs))
	for i := range recs {
		out = append(out, s.view(&recs[i], items[recs[i].ID]))
	}
	return out, nil
}

// Cancel cancels a run: queued items are dropped, running ones are cancelled
// when their operation can stop, and the run ends cancelled.
func (s *Service) Cancel(ctx context.Context, runID string) error {
	if err := s.opts.Store.RequestCancel(runID); err != nil {
		return storeError(err, "")
	}
	s.changed(ctx, runID)
	return nil
}

// CancelItem cancels one item: a queued one is dropped, a running one is
// cancelled while its operation says it can stop.
func (s *Service) CancelItem(ctx context.Context, runID, key string) error {
	run, err := s.Get(ctx, runID)
	if err != nil {
		return err
	}
	if !run.Active() {
		return refuse(CodeRunFinished, key, "the run is over")
	}
	it := run.Item(key)
	if it == nil {
		return refuse(CodeNotFound, key, "no such item in the run")
	}
	switch {
	case it.Phase == PhaseQueued:
	case it.Phase == PhaseRunning && !it.Verifying && it.Cancellable && it.JobID != "":
	default:
		return refuse(CodeNotCancellable, key, "the item cannot be cancelled now")
	}
	if err := s.opts.Store.RequestCancelItem(runID, key); err != nil {
		return storeError(err, key)
	}
	s.changed(ctx, runID)
	return nil
}

// Retry starts a new run with the retryable items of a run that is over and
// still have an update, toward what the checks offer now. The old run does
// not change. The new run belongs to actor, the administrator who retries,
// and is always manual: a retry never acts as the actor of the old run, and
// the retry of an automatic run never runs as the system actor of the
// automatic update, whose add-on path skips the pins and the policy of a
// manual run.
func (s *Service) Retry(ctx context.Context, actor, runID string) (Run, error) {
	run, err := s.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Active() {
		return Run{}, refuse(CodeRunNotFinished, "", "the run still runs")
	}
	var req StartRequest
	for _, it := range run.Items {
		if it.RetryEligible() {
			req.Items = append(req.Items, StartItem{Kind: it.Kind, Name: it.Name})
		}
	}
	if len(req.Items) == 0 {
		return Run{}, refuse(CodeNothingToRetry, "", "no item can be retried")
	}
	cands, unchecked := s.candidates(WithActor(ctx, actor))
	listed := map[string]bool{}
	for _, c := range cands {
		listed[c.Key()] = true
	}
	kept := req.Items[:0]
	for _, si := range req.Items {
		if listed[Key(si.Kind, si.Name)] {
			kept = append(kept, si)
		}
	}
	if len(kept) == 0 {
		return Run{}, refuse(CodeNothingToRetry, "", "no retryable item still has an update")
	}
	req.Items, req.Source = kept, SourceManual
	items, err := planStart(cands, unchecked, req)
	if err != nil {
		return Run{}, err
	}
	return s.create(ctx, actor, SourceManual, items)
}

// Dismiss hides a run that is over.
func (s *Service) Dismiss(ctx context.Context, runID string) error {
	if err := s.opts.Store.Dismiss(runID); err != nil {
		if errors.Is(err, distributed.ErrUpgradeRunState) {
			return refuse(CodeRunNotFinished, "", "the run still runs")
		}
		return storeError(err, "")
	}
	if run, err := s.Get(ctx, runID); err == nil {
		s.notify(Change{Run: run, Local: true})
	}
	s.publish(runID)
	return nil
}

// changed wakes the local runner of a run, or tells the others.
func (s *Service) changed(_ context.Context, runID string) {
	if r := s.runnerOf(runID); r != nil {
		r.wake()
	}
	s.publish(runID)
}

func storeError(err error, key string) error {
	switch {
	case errors.Is(err, distributed.ErrUpgradeRunNotFound):
		return refuse(CodeNotFound, key, "no such run")
	case errors.Is(err, distributed.ErrUpgradeRunState):
		return refuse(CodeRunFinished, key, "the run is not in a state that allows this")
	}
	return err
}
