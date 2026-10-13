package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// ClaimWake is the payload of a wake hint. It names the kind, so that the
// consumers of the other kinds do not look.
type ClaimWake struct {
	Kind messaging.WorkKind `json:"kind"`
}

// ClaimQueue is the WorkQueue of the carrier that has no broker: a payload
// becomes a row, and a consumer takes the row.
//
// Enqueue succeeds while nobody consumes, and the row waits. That differs from a
// publish to an empty queue group, which is lost.
type ClaimQueue struct {
	db    *gorm.DB
	hints messaging.Publisher
}

// NewClaimQueue returns a queue over db. hints, when not nil, gets a wake hint
// for each unit put in the queue. It may fail or lose the hint: the poll of the
// consumers is what guarantees that the work is found.
func NewClaimQueue(db *gorm.DB, hints messaging.Publisher) *ClaimQueue {
	return &ClaimQueue{db: db, hints: hints}
}

var _ messaging.WorkQueue = (*ClaimQueue)(nil)

// Enqueue writes the payload as a row, and then tells the consumers. A refusal of
// the payload (its kind, its size, its encoding) comes back as an error and
// writes nothing. A hint that cannot be published is not an error.
func (q *ClaimQueue) Enqueue(ctx context.Context, kind messaging.WorkKind, payload any) error {
	if _, err := EnqueueClaim(ctx, q.db, kind, payload); err != nil {
		return err
	}
	if q.hints != nil {
		if err := q.hints.Publish(messaging.SubjectClaimWake, ClaimWake{Kind: kind}); err != nil {
			xlog.Debug("A wake hint for the claim queue could not be published; the poll will find the work", "kind", kind, "error", err)
		}
	}
	return nil
}

// ErrKeepClaim, wrapped in the error of a handler, tells the consumer to leave
// the claim as it is: neither delete it nor release it. It is for work that ran
// and whose answer could not be recorded. Releasing the claim would run the work
// again. The row stays held by this replica, and it is taken over only if the
// replica dies.
var ErrKeepClaim = errors.New("jobs: keep the claim")

// settleTimeout bounds the writes that settle a claim. They run on a context that
// the stop of the replica does not cancel, because a replica that is leaving must
// still release what it holds.
const settleTimeout = 10 * time.Second

// ClaimConsumerConfig is what a ClaimConsumer needs.
type ClaimConsumerConfig struct {
	DB *gorm.DB
	// Owner is the id of this replica in the instances table. The reap asks
	// whether the owner of a claim is live, so the replica must be registered.
	Owner string
	// Events is what the handlers get as the place for their events.
	Events messaging.Publisher
	// Hints is where the wake hints are heard. It may be nil: the consumer then
	// polls only.
	Hints messaging.Broadcaster
	// Interval is the poll. DefaultClaimInterval when zero.
	Interval time.Duration
	// Liveness is the window of replica liveness. cluster.InstanceLiveness when
	// zero: it is the one definition of absence that the cluster has.
	Liveness time.Duration
	// MaxFailures is how many failures that say something about the work a row
	// may have before the consumer ends it (OnExhausted, then delete).
	// DefaultClaimMaxFailures when zero. A release for lack of a worker, a route
	// or a free slot is not a failure of the work and is not counted.
	MaxFailures int
	// OnExhausted is called for a row that has reached MaxFailures, to record in
	// the job that it failed. The row is deleted when it returns nil, and kept
	// (to be tried again) when it returns an error. It may be nil.
	OnExhausted func(ctx context.Context, kind messaging.WorkKind, payload []byte, cause error) error
}

// DefaultClaimMaxFailures is how many failures end a row. With the backoff of
// the releases this is about five minutes of trying.
const DefaultClaimMaxFailures = 10

var (
	// errClaimLost is the cause of the context of a handler whose claim another
	// replica took over: the instances row of this replica was missing for long
	// enough for the reap, and the work was given away.
	errClaimLost = errors.New("jobs: the claim was taken over by another replica")
	// errOwnerNotLive is the cause of the context of a handler when this replica
	// is no longer live in the instances table. Its peers may reap its claims at
	// any moment, so it stops the work instead of running it twice.
	errOwnerNotLive = errors.New("jobs: this replica is no longer live in the cluster")
	// errNoAnswerYet marks the error of a handler that says nothing about the
	// work: no worker, no route, no free slot. The row goes back to the pool and
	// the failure is not counted.
	errNoAnswerYet = errors.New("jobs: the work could not be offered to a worker")
)

// noAnswerYet marks err as one that says nothing about the work.
func noAnswerYet(err error) error { return fmt.Errorf("%w: %w", errNoAnswerYet, err) }

// DefaultClaimInterval is how often a consumer looks for work when no hint woke
// it.
const DefaultClaimInterval = 2 * time.Second

// ClaimConsumer is the WorkConsumer of the claim queue, for a frontend replica.
// It claims the rows of a kind and runs a handler on each. On the tunnel carrier
// the handler hands the work to an agent worker, because an agent worker has no
// database and cannot claim.
//
// The outcome of a handler settles the row. A nil return completes it: the work
// has an answer. An error releases it with a wait: nothing was learned about the
// work, so it must be tried again, possibly by another replica. A panic is an
// error. An error that wraps ErrKeepClaim leaves the row alone. A row whose
// failures reach MaxFailures is ended, and not released again.
//
// The contract is at least once. A handler can be called again for the same work:
// after the replica that held it died, after a release, and after the instances
// row of its replica was missing for as long as a peer needed to reap its claims
// (a stall of the process or of its link to the database for about the liveness
// window). In that last case the replica stops its handler, by cancelling the
// context that the handler was given, as soon as it sees that the claim is no
// longer its own; the work that the handler already did stays done. A handler for
// work that is not safe to repeat must record its progress and check it when it
// is called.
//
// The loops of the kinds share what they ask the database every tick: whether
// this replica is live, which claims it holds, and the reap of the claims of
// departed replicas. Three loops ask once and not three times.
type ClaimConsumer struct {
	cfg ClaimConsumerConfig

	checkMu sync.Mutex
	checkAt time.Time
	live    bool
	reapAt  time.Time

	flightMu sync.Mutex
	flights  map[*flight]struct{}
}

// flight is a run in progress: the claim it drives and how to stop it.
type flight struct {
	id       string
	attempts int
	cancel   context.CancelCauseFunc
}

var _ messaging.WorkConsumer = (*ClaimConsumer)(nil)

// NewClaimConsumer returns a consumer. It refuses a configuration that would
// claim work and never answer it.
func NewClaimConsumer(cfg ClaimConsumerConfig) (*ClaimConsumer, error) {
	switch {
	case cfg.DB == nil:
		return nil, errors.New("the claim consumer was built with no database to claim work from")
	case cfg.Owner == "":
		return nil, errors.New("the claim consumer was built with no instance id: its claims could not be told from those of a dead replica")
	case cfg.Events == nil:
		return nil, errors.New("the claim consumer was built with no publisher for the events of its handlers")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultClaimInterval
	}
	if cfg.Liveness <= 0 {
		cfg.Liveness = cluster.InstanceLiveness
	}
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = DefaultClaimMaxFailures
	}
	return &ClaimConsumer{cfg: cfg, flights: map[*flight]struct{}{}}, nil
}

// Consume claims the rows of kind and runs h on each, at most maxInFlight at a
// time (0 and negative values are unbounded). ctx is the parent of every handler
// call. Unsubscribe stops claiming and waits for the handlers that run, so a
// handler must not call it.
func (c *ClaimConsumer) Consume(ctx context.Context, kind messaging.WorkKind, maxInFlight int, h messaging.WorkHandler) (messaging.Subscription, error) {
	if !knownKind(kind) {
		return nil, fmt.Errorf("consuming work: unknown work kind %q", kind)
	}
	loopCtx, stop := context.WithCancel(ctx)
	l := &claimLoop{
		c:       c,
		cfg:     c.cfg,
		kind:    kind,
		handler: h,
		parent:  ctx,
		stop:    stop,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	if maxInFlight > 0 {
		l.slots = make(chan struct{}, maxInFlight)
	}
	if c.cfg.Hints != nil {
		sub, err := c.cfg.Hints.Subscribe(messaging.SubjectClaimWake, l.hear)
		if err != nil {
			stop()
			return nil, fmt.Errorf("subscribing to the wake hints of the claim queue: %w", err)
		}
		l.hints = sub
	}
	go l.run(loopCtx)
	return l, nil
}

// claimLoop is one subscription: the loop that claims the rows of one kind.
type claimLoop struct {
	c       *ClaimConsumer
	cfg     ClaimConsumerConfig
	kind    messaging.WorkKind
	handler messaging.WorkHandler
	parent  context.Context
	stop    context.CancelFunc
	slots   chan struct{} // nil: unbounded
	hints   messaging.Subscription

	wake chan struct{}
	done chan struct{}
	wg   sync.WaitGroup

	closeOnce sync.Once

	// unregistered remembers that this replica has said that it cannot claim, so
	// the refusal is one line when it begins and one when it ends, and not one
	// line per poll for as long as the misconfiguration lasts.
	unregistered bool
}

// hear takes a wake hint. A hint for another kind is ignored; one that cannot be
// read is taken as for this kind, because a needless look costs one query.
func (l *claimLoop) hear(raw []byte) {
	var w ClaimWake
	if err := json.Unmarshal(raw, &w); err == nil && w.Kind != l.kind {
		return
	}
	select {
	case l.wake <- struct{}{}:
	default: // a look is already asked for
	}
}

func (l *claimLoop) run(ctx context.Context) {
	defer close(l.done)
	l.releaseOwn(ctx)
	ticker := time.NewTicker(l.cfg.Interval)
	defer ticker.Stop()
	l.fill(ctx, true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.fill(ctx, true)
		case <-l.wake:
			l.fill(ctx, false)
		}
	}
}

// take reserves a slot for a handler, without waiting.
func (l *claimLoop) take() bool {
	if l.slots == nil {
		return true
	}
	select {
	case l.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *claimLoop) free() {
	if l.slots != nil {
		<-l.slots
	}
}

// releaseOwn returns the claims of this kind that the id of this replica holds,
// when the loop starts. This process has driven none of them yet: they belong to
// an earlier process that had the same id (LOCALAI_INSTANCE_ID is stable across
// restarts), and the reap would never free them, because the id is live.
func (l *claimLoop) releaseOwn(ctx context.Context) {
	n, err := ReleaseOwned(ctx, l.cfg.DB, l.cfg.Owner, l.kind)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			xlog.Warn("Could not release the claims that an earlier process of this replica left", "kind", l.kind, "error", err)
		}
	case n > 0:
		xlog.Info("Released work that an earlier process of this replica was driving", "kind", l.kind, "count", n)
	}
}

// register adds a run in progress.
func (c *ClaimConsumer) register(claim *WorkClaim, cancel context.CancelCauseFunc) *flight {
	f := &flight{id: claim.ID, attempts: claim.Attempts, cancel: cancel}
	c.flightMu.Lock()
	c.flights[f] = struct{}{}
	c.flightMu.Unlock()
	return f
}

func (c *ClaimConsumer) unregister(f *flight) {
	c.flightMu.Lock()
	delete(c.flights, f)
	c.flightMu.Unlock()
}

func (c *ClaimConsumer) snapshot() []*flight {
	c.flightMu.Lock()
	defer c.flightMu.Unlock()
	out := make([]*flight, 0, len(c.flights))
	for f := range c.flights {
		out = append(out, f)
	}
	return out
}

// tick does what every loop needs once for each interval, and lets the loops that
// ask within half an interval reuse the answer: it checks that this replica is
// live, stops the runs that lost their claim, and, on a poll, frees the claims of
// departed replicas and purges old migrated rows. It says whether this replica
// may claim.
func (c *ClaimConsumer) tick(ctx context.Context, poll bool) (bool, error) {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()
	fresh := !c.checkAt.IsZero() && time.Since(c.checkAt) < c.cfg.Interval/2
	if !fresh {
		// The runs are counted before the question is asked. A claim that is taken
		// after it is not judged by it.
		runs := c.snapshot()
		live, err := OwnerIsLive(ctx, c.cfg.DB, c.cfg.Owner, c.cfg.Liveness)
		if err != nil {
			return false, err
		}
		c.live, c.checkAt = live, time.Now()
		if !live {
			// Its peers may reap the claims of this replica at any moment, and then
			// the work runs on two replicas. Stop it here.
			for _, f := range runs {
				f.cancel(errOwnerNotLive)
			}
		} else if len(runs) > 0 {
			held, err := HeldBy(ctx, c.cfg.DB, c.cfg.Owner)
			if err != nil {
				if ctx.Err() == nil {
					xlog.Warn("Could not check which claims this replica still holds", "error", err)
				}
			} else {
				still := make(map[string]int, len(held))
				for _, h := range held {
					still[h.ID] = h.Attempts
				}
				for _, f := range runs {
					if attempts, ok := still[f.id]; !ok || attempts != f.attempts {
						f.cancel(errClaimLost)
					}
				}
			}
		}
	}
	if poll && c.live && time.Since(c.reapAt) >= c.cfg.Interval/2 {
		c.reapAt = time.Now()
		if released, err := ReapAbandoned(ctx, c.cfg.DB, c.cfg.Liveness); err != nil {
			// Reported and not returned: a reap that could not run says nothing
			// about the claim this tick is about to take.
			if ctx.Err() == nil {
				xlog.Warn("Could not release the claims of departed replicas", "error", err)
			}
		} else if released > 0 {
			xlog.Info("Released work claimed by replicas that are no longer live", "count", released)
		}
		if purged, err := PurgeMigrated(ctx, c.cfg.DB, MigratedRetention); err != nil {
			if ctx.Err() == nil {
				xlog.Warn("Could not purge the work that a change of carrier moved", "error", err)
			}
		} else if purged > 0 {
			xlog.Info("Purged work that a change of carrier moved long ago", "count", purged)
		}
	}
	return c.live, nil
}

// fill claims rows into the free slots. On a poll it first looks after the
// claims that departed replicas left.
func (l *claimLoop) fill(ctx context.Context, poll bool) {
	// The liveness of this replica is checked before it touches the queue. A
	// replica with no row in the instances table is invisible to its peers. That
	// is a degraded state, but not one for holding a claim: the reap would take
	// the claim out from under it while the work still ran, and the work would
	// run twice. Refusing to claim heals by itself when membership registers.
	live, err := l.c.tick(ctx, poll)
	if err != nil {
		if ctx.Err() == nil {
			xlog.Warn("Could not check whether this replica may claim work", "kind", l.kind, "error", err)
		}
		return
	}
	if !live {
		if !l.unregistered {
			l.unregistered = true
			xlog.Error("This replica is not live in the cluster, so it does not claim queued work: its peers could not tell its claims from those of a dead replica",
				"instance", l.cfg.Owner, "kind", l.kind)
		}
		return
	}
	if l.unregistered {
		l.unregistered = false
		xlog.Info("This replica is live in the cluster again and claims queued work", "instance", l.cfg.Owner, "kind", l.kind)
	}

	for ctx.Err() == nil {
		if !l.take() {
			return // this replica drives as much as it may
		}
		claim, err := ClaimNext(ctx, l.cfg.DB, l.cfg.Owner, l.kind)
		if err != nil {
			l.free()
			if !errors.Is(err, ErrNoWork) && ctx.Err() == nil {
				xlog.Warn("Could not claim work", "kind", l.kind, "error", err)
			}
			return
		}
		l.wg.Add(1)
		go l.drive(claim)
	}
}

// drive runs the handler on a claimed row and settles the row.
func (l *claimLoop) drive(claim *WorkClaim) {
	defer l.wg.Done()
	defer l.free()
	hctx, cancel := context.WithCancelCause(l.parent)
	f := l.c.register(claim, cancel)
	defer l.c.unregister(f)
	defer cancel(nil)
	err := l.call(hctx, claim)
	cause := context.Cause(hctx)

	// The settle must outlive the stop of the replica.
	ctx, cancelSettle := context.WithTimeout(context.WithoutCancel(l.parent), settleTimeout)
	defer cancelSettle()
	switch {
	case err != nil && errors.Is(cause, errClaimLost):
		// The row belongs to another replica now. Settling it would be wrong, and
		// the fence of the attempt count would refuse it anyway.
		xlog.Warn("Stopped a run whose claim another replica took over; the work runs there",
			"claim", claim.ID, "kind", claim.Kind)
	case err != nil && errors.Is(cause, errOwnerNotLive):
		// This replica came back, or may come back, before a peer reaped the row.
		// A row that stays claimed by a live id is never reaped, so give it back.
		xlog.Warn("Stopped a run: this replica was not live in the cluster. Returning the claim to the queue",
			"claim", claim.ID, "kind", claim.Kind)
		l.settle(ReleaseClaim(ctx, l.cfg.DB, claim))
	case err == nil:
		l.settle(CompleteClaim(ctx, l.cfg.DB, claim))
	case errors.Is(err, ErrKeepClaim):
		xlog.Warn("Leaving a claim as it is: the work ran and its answer could not be recorded",
			"claim", claim.ID, "kind", claim.Kind, "error", err)
	case errors.Is(err, errNoAnswerYet):
		xlog.Warn("Releasing a claim whose work could not be offered to a worker; it will be tried again after a wait",
			"claim", claim.ID, "kind", claim.Kind, "attempt", claim.Attempts+1, "error", err)
		l.settle(ReleaseClaim(ctx, l.cfg.DB, claim))
	case claim.Failures+1 >= l.cfg.MaxFailures:
		l.exhaust(ctx, claim, err)
	default:
		xlog.Warn("Releasing a claim whose work obtained no answer; it will be tried again after a wait",
			"claim", claim.ID, "kind", claim.Kind, "attempt", claim.Attempts+1, "failure", claim.Failures+1, "of", l.cfg.MaxFailures, "error", err)
		l.settle(ReleaseClaimFailed(ctx, l.cfg.DB, claim))
	}
}

// exhaust ends a row that failed MaxFailures times. The job is recorded as
// failed first and the row is deleted after, so a row is never deleted with its
// job still running.
func (l *claimLoop) exhaust(ctx context.Context, claim *WorkClaim, cause error) {
	xlog.Error("Giving up on queued work: it failed too many times",
		"claim", claim.ID, "kind", claim.Kind, "failures", claim.Failures+1, "error", cause)
	if l.cfg.OnExhausted != nil {
		if err := l.cfg.OnExhausted(ctx, l.kind, claim.Payload, cause); err != nil {
			xlog.Error("Could not record that queued work failed; it will be tried again", "claim", claim.ID, "error", err)
			l.settle(ReleaseClaimFailed(ctx, l.cfg.DB, claim))
			return
		}
	}
	l.settle(CompleteClaim(ctx, l.cfg.DB, claim))
}

func (l *claimLoop) settle(taken bool, err error) {
	switch {
	case err != nil:
		// The row stays claimed by this replica, which is the safe side: it is
		// taken over if this replica dies.
		xlog.Error("Could not settle a claim", "error", err)
	case !taken:
		xlog.Warn("A claim was no longer held by this replica when its work ended; another replica has taken it over")
	}
}

// call runs the handler and turns a panic into an error, so that one bad payload
// cannot take the replica down with it.
func (l *claimLoop) call(ctx context.Context, claim *WorkClaim) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("the handler panicked: %v", p)
		}
	}()
	return l.handler(ctx, claim.Payload, l.cfg.Events)
}

// Unsubscribe stops claiming and waits for the handlers that run.
func (l *claimLoop) Unsubscribe() error {
	l.closeOnce.Do(func() {
		l.stop()
		<-l.done
		if l.hints != nil {
			_ = l.hints.Unsubscribe()
		}
		l.wg.Wait()
	})
	return nil
}
