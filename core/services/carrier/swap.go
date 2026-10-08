package carrier

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
)

// Builder builds the Set for a carrier, completely and checked, for the cluster
// row it is given. It is the one place that names the carriers.
type Builder func(ctx context.Context, row cluster.CarrierRow, target cluster.Carrier) (*Set, error)

// RowSource reads the cluster carrier row and the clock of the database.
// *cluster.CarrierStore is one.
type RowSource interface {
	Get(ctx context.Context) (cluster.CarrierRow, error)
	DBNow(ctx context.Context) (time.Time, error)
}

// ReadyReporter records that this replica has built the carrier for an epoch of
// the row, or why it could not. *cluster.Membership is one.
type ReadyReporter interface {
	ReportReady(ctx context.Context, epoch int64, reason string) error
}

// WindowControl is the part of Window that the swapper moves. *Window is one.
type WindowControl interface {
	Open(prev *Set)
	Close()
}

// DefaultSwapInterval is how often a replica reads the cluster carrier row.
const DefaultSwapInterval = 2 * time.Second

// SwapperOptions are the inputs of NewSwapper.
type SwapperOptions struct {
	// Cur is the pointer the holders read. It names the set in use.
	Cur *atomic.Pointer[Set]
	// Bus is the broadcaster holder over Cur.
	Bus *Broadcaster
	// Window routes the calls to workers during a change. It may be nil.
	Window WindowControl
	Rows   RowSource
	Ready  ReadyReporter
	Build  Builder
	// Interval is the poll, DefaultSwapInterval when zero. Each wait is varied by
	// up to a fifth so that replicas do not read the row at the same moment.
	Interval time.Duration
	// Settle is how long after the release of a carrier its handoff runs again,
	// for the producers that were slow to flip. Twice the interval when zero.
	Settle time.Duration
	// StopGrace bounds the wait for the work of a set to stop. A set whose work
	// does not stop in time is logged and left behind, so that the hand-over and
	// the close of the set are not held up by it. 30 seconds when zero.
	StopGrace time.Duration
	// CloseGrace bounds the wait of Close for the hand-over that runs in the
	// background. After it the hand-over is cancelled. 10 seconds when zero.
	CloseGrace time.Duration
	Meter      metric.Meter
}

// Swapper is what a replica does in a change of carrier. It follows the row:
//
//   - prepare: build the target set while the old one serves, attach every
//     subscription to it, and report ready for the epoch of the row.
//   - commit: store the target set, so that publishes, enqueues and new calls go
//     to it; start its work; keep the old set attached; confirm.
//   - stable with a drain: nothing. The old set stays attached.
//   - stable with no drain: release the old set. Stop its work, hand over what it
//     held, close it, and run the reconnect hooks once.
//   - stable after an abort: drop the set that was built for nothing.
//
// The row is authoritative. A replica that polls late, restarts, or joins during a
// change ends in the same place as the others, because it acts on what the row
// says and not on what it saw before. The poll is the only thing that must work;
// the hint only shortens the wait.
type Swapper struct {
	o SwapperOptions

	mu       sync.Mutex
	base     context.Context
	prepared *Set // built for the change in prepare, attached, not yet in use
	draining *Set // the previous set, attached through the drain
	members  map[*Set]*member
	startErr map[*Set]error // why the work of a set did not start, until it does
	reported struct {
		set    bool
		epoch  int64
		reason string
	}
	prepareSeen time.Time
	closed      bool

	// life is the context of the hand-over that runs in the background. It ends
	// when Close gives up waiting for it.
	life       context.Context
	lifeCancel context.CancelFunc

	wake     chan struct{}
	done     chan struct{}
	doneOnce sync.Once
	wg       sync.WaitGroup

	row            atomic.Pointer[cluster.CarrierRow]
	drainRemaining atomic.Int64
	ready          atomic.Bool
	swaps          metric.Int64Counter
	failures       metric.Int64Counter
	swapSeconds    metric.Float64Histogram
}

// member is the work that a set started.
type member struct {
	cancel context.CancelCauseFunc
	stop   func()
}

// NewSwapper returns the swapper of a replica. Cur must already name the set
// that the row names.
func NewSwapper(o SwapperOptions) (*Swapper, error) {
	switch {
	case o.Cur == nil || o.Cur.Load() == nil:
		return nil, errors.New("a swapper needs the pointer to the set in use, and it must name a set")
	case o.Bus == nil:
		return nil, errors.New("a swapper needs the broadcaster holder")
	case o.Rows == nil || o.Ready == nil || o.Build == nil:
		return nil, errors.New("a swapper needs a row source, a readiness reporter and a builder")
	}
	if o.Interval <= 0 {
		o.Interval = DefaultSwapInterval
	}
	if o.Settle <= 0 {
		o.Settle = 2 * o.Interval
	}
	if o.StopGrace <= 0 {
		o.StopGrace = 30 * time.Second
	}
	if o.CloseGrace <= 0 {
		o.CloseGrace = 10 * time.Second
	}
	life, lifeCancel := context.WithCancel(context.Background())
	s := &Swapper{
		o: o, base: context.Background(), life: life, lifeCancel: lifeCancel,
		members:  map[*Set]*member{},
		startErr: map[*Set]error{},
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	s.registerMetrics()
	return s, nil
}

func (s *Swapper) registerMetrics() {
	meter := s.o.Meter
	if meter == nil {
		meter = otel.Meter("github.com/mudler/LocalAI")
	}
	s.swaps, _ = meter.Int64Counter("localai_carrier_swaps_total",
		metric.WithDescription("Times this replica started to use another carrier, by the carrier it moved to"))
	s.failures, _ = meter.Int64Counter("localai_carrier_prepare_failures_total",
		metric.WithDescription("Times this replica could not build the carrier it was asked to prepare"))
	s.swapSeconds, _ = meter.Float64Histogram("localai_carrier_swap_seconds",
		metric.WithDescription("Time from the first sight of a change in prepare to the start of use of the new carrier on this replica"), metric.WithUnit("s"))

	epoch, _ := meter.Int64ObservableGauge("localai_carrier_epoch", metric.WithDescription("Epoch of the cluster carrier row as this replica last read it"))
	state, _ := meter.Int64ObservableGauge("localai_carrier_state", metric.WithDescription("1 for the state the cluster is in (stable, prepare or commit), 0 for the others"))
	active, _ := meter.Int64ObservableGauge("localai_carrier_active", metric.WithDescription("1 for the carrier this replica publishes on, 0 for the other"))
	drain, _ := meter.Float64ObservableGauge("localai_carrier_drain_remaining_seconds", metric.WithDescription("Seconds until the previous carrier is released, 0 when none is draining"), metric.WithUnit("s"))
	ready, _ := meter.Int64ObservableGauge("localai_carrier_replica_ready", metric.WithDescription("1 when this replica has reported ready for the epoch it last read"))
	_, _ = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		row := s.row.Load()
		if row == nil {
			return nil
		}
		o.ObserveInt64(epoch, row.Epoch)
		for _, st := range []cluster.State{cluster.StateStable, cluster.StatePrepare, cluster.StateCommit} {
			v := int64(0)
			if row.State == st {
				v = 1
			}
			o.ObserveInt64(state, v, metric.WithAttributes(attribute.String("state", string(st))))
		}
		cur := s.o.Cur.Load().Name
		for _, c := range []cluster.Carrier{cluster.CarrierNATS, cluster.CarrierTunnel} {
			v := int64(0)
			if cur == c {
				v = 1
			}
			o.ObserveInt64(active, v, metric.WithAttributes(attribute.String("carrier", string(c))))
		}
		o.ObserveFloat64(drain, time.Duration(s.drainRemaining.Load()).Seconds())
		r := int64(0)
		if s.ready.Load() {
			r = 1
		}
		o.ObserveInt64(ready, r)
		return nil
	}, epoch, state, active, drain, ready)
}

// Adopt starts the work of the set in use, and takes ctx as the parent of every
// piece of work that the swapper starts. Call it once, when the replica starts.
func (s *Swapper) Adopt(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.base = ctx
	s.ensureStarted(s.o.Cur.Load())
}

// Wake asks the polling loop to look at the row now. It is the hint that the
// leader sends; losing it costs one interval.
func (s *Swapper) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run polls the row until ctx ends.
func (s *Swapper) Run(ctx context.Context) {
	defer s.doneOnce.Do(func() { close(s.done) })
	s.Adopt(ctx)
	var lastErr string
	for {
		if err := s.Poll(ctx); err != nil && ctx.Err() == nil {
			// One line when it starts and one when it clears, not one per poll.
			if msg := err.Error(); msg != lastErr {
				lastErr = msg
				xlog.Warn("Could not follow the cluster carrier row", "error", err)
			}
		} else if err == nil {
			lastErr = ""
		}
		// #nosec G404 -- spreads the polls of the replicas apart; the value is no secret.
		wait := time.Duration(float64(s.o.Interval) * (0.8 + 0.4*rand.Float64()))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// Poll reads the row and acts on it.
func (s *Swapper) Poll(ctx context.Context) error {
	row, err := s.o.Rows.Get(ctx)
	if errors.Is(err, cluster.ErrNotSeeded) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.Draining != "" && row.DrainingUntil != nil {
		if now, err := s.o.Rows.DBNow(ctx); err == nil {
			s.drainRemaining.Store(int64(max(0, row.DrainingUntil.Sub(now))))
		}
	} else {
		s.drainRemaining.Store(0)
	}
	return s.Reconcile(ctx, row)
}

// Reconcile acts on one reading of the row. Calls are serialised.
func (s *Swapper) Reconcile(ctx context.Context, row cluster.CarrierRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.row.Store(&row)
	if row.State == cluster.StatePrepare && s.prepareSeen.IsZero() {
		s.prepareSeen = time.Now()
	}

	// Be on the carrier that the row says is active before anything else. A
	// replica that polled late, or one that missed a whole change, is behind.
	if cur := s.o.Cur.Load(); cur.Name != row.Active {
		if err := s.swapTo(ctx, row); err != nil {
			s.countFailure()
			xlog.Warn("This replica cannot use the active carrier of the cluster", "carrier", row.Active, "epoch", row.Epoch, "error", err)
			return s.report(ctx, row.Epoch, fmt.Sprintf("cannot build the %s carrier: %v", row.Active, err))
		}
	}
	// A replica that cannot run the work of its carrier is not ready. It says so,
	// and the next poll tries again.
	if cur := s.o.Cur.Load(); s.ensureStarted(cur) != nil {
		s.countFailure()
		return s.report(ctx, row.Epoch, fmt.Sprintf("cannot start the work of the %s carrier: %v", cur.Name, s.startErr[cur]))
	}

	switch row.State {
	case cluster.StatePrepare:
		return s.prepare(ctx, row)
	case cluster.StateCommit:
		return s.report(ctx, row.Epoch, "")
	default:
		s.prepareSeen = time.Time{}
		s.settle(ctx, row)
		return nil
	}
}

func (s *Swapper) countFailure() {
	if s.failures != nil {
		s.failures.Add(context.Background(), 1)
	}
}

// report tells the cluster which epoch this replica is ready for. A report that
// is the same as the last one that went through is not written again.
func (s *Swapper) report(ctx context.Context, epoch int64, reason string) error {
	s.ready.Store(reason == "")
	if s.reported.set && s.reported.epoch == epoch && s.reported.reason == reason {
		return nil
	}
	if err := s.o.Ready.ReportReady(ctx, epoch, reason); err != nil {
		return fmt.Errorf("reporting readiness for epoch %d: %w", epoch, err)
	}
	s.reported.set, s.reported.epoch, s.reported.reason = true, epoch, reason
	return nil
}

// attach builds a set for target, or reuses one that is attached already, and
// makes every subscription listen on it.
func (s *Swapper) attach(ctx context.Context, row cluster.CarrierRow, target cluster.Carrier) (*Set, error) {
	switch {
	case s.prepared != nil && s.prepared.Name == target && builtFrom(s.prepared, row):
		return s.prepared, nil
	case s.draining != nil && s.draining.Name == target && builtFrom(s.draining, row):
		return s.draining, nil
	}
	if s.prepared != nil && s.prepared.Name == target {
		// Built from another address than the row names now. Every replica must
		// build from the address of the row, so this set is dropped.
		stale := s.prepared
		s.prepared = nil
		if stale != s.draining {
			closeSet(s.dropListening(stale))
		}
	}
	set, err := s.o.Build(ctx, row, target)
	if err != nil {
		return nil, err
	}
	if err := set.Validate(); err != nil {
		return nil, err
	}
	if err := s.o.Bus.Listen(set); err != nil {
		closeSet(set)
		return nil, err
	}
	return set, nil
}

// builtFrom says whether set was built from the address that the row names. A
// row with no address, or a set of the other carrier, has nothing to compare.
func builtFrom(set *Set, row cluster.CarrierRow) bool {
	return set.Name != cluster.CarrierNATS || row.NATSURL == "" || set.NATSURL == row.NATSURL
}

func (s *Swapper) prepare(ctx context.Context, row cluster.CarrierRow) error {
	set, err := s.attach(ctx, row, row.Target)
	if err != nil {
		s.countFailure()
		xlog.Warn("This replica cannot build the carrier it was asked to prepare", "target", row.Target, "epoch", row.Epoch, "error", err)
		return s.report(ctx, row.Epoch, err.Error())
	}
	if s.prepared != set {
		s.prepared = set
		xlog.Info("The target carrier is built and attached", "target", row.Target, "epoch", row.Epoch)
	}
	return s.report(ctx, row.Epoch, "")
}

// swapTo starts to use the carrier the row names.
func (s *Swapper) swapTo(ctx context.Context, row cluster.CarrierRow) error {
	next, err := s.attach(ctx, row, row.Active)
	if err != nil {
		return err
	}
	// Listening is idempotent. It matters for a set that was built for a change that
	// this replica never saw in prepare.
	if err := s.o.Bus.Listen(next); err != nil {
		return err
	}
	old := s.o.Cur.Load()
	// The window opens before the pointer moves. While it is open with the old set
	// as both the current and the previous set, every call goes to the old set,
	// which is where it went. Once the pointer moves, a call to a worker that is
	// attached to the old carrier only finds the window already open. The other
	// order leaves a gap in which such a call goes to the new carrier and fails.
	if s.o.Window != nil {
		s.o.Window.Open(old)
	}
	s.o.Cur.Store(next)

	third := s.draining
	if next == s.draining {
		third = nil
	}
	if next == s.prepared {
		s.prepared = nil
	}
	s.draining = old
	if third != nil && third != old {
		// A set from a drain that was never closed. It is not in use and nothing
		// routes to it any more.
		s.retire(third, next)
	}
	startErr := s.ensureStarted(next)
	if s.swaps != nil {
		s.swaps.Add(ctx, 1, metric.WithAttributes(attribute.String("to", string(next.Name))))
	}
	took := time.Duration(0)
	if !s.prepareSeen.IsZero() {
		took = time.Since(s.prepareSeen)
		if s.swapSeconds != nil {
			s.swapSeconds.Record(ctx, took.Seconds())
		}
	}
	xlog.Info("This replica now uses another carrier; the previous one stays attached while it drains",
		"carrier", next.Name, "previous", old.Name, "epoch", row.Epoch, "since_prepare", took.Round(time.Millisecond))
	if startErr != nil {
		return fmt.Errorf("the carrier is in use but its work did not start: %w", startErr)
	}
	return nil
}

// ensureStarted starts the work of a set once. It returns why the work could not
// start, and tries again at the next call.
func (s *Swapper) ensureStarted(set *Set) error {
	if set == nil || set.Start == nil {
		return nil
	}
	if _, ok := s.members[set]; ok {
		return nil
	}
	ctx, cancel := context.WithCancelCause(s.base)
	stop, err := set.Start(ctx)
	if err != nil {
		cancel(err)
		// Tried again at the next poll.
		xlog.Error("Could not start the work of the carrier", "carrier", set.Name, "error", err)
		s.startErr[set] = err
		return err
	}
	delete(s.startErr, set)
	s.members[set] = &member{cancel: cancel, stop: stop}
	return nil
}

// settle acts on a stable row: it drops a set built for a change that was
// aborted, attaches again the carrier that is still draining when this replica
// started during the drain, and releases the previous set once the leader has
// ended the drain.
func (s *Swapper) settle(ctx context.Context, row cluster.CarrierRow) {
	if s.prepared != nil {
		if s.prepared != s.draining {
			closeSet(s.dropListening(s.prepared))
		}
		s.prepared = nil
	}
	if row.Draining != "" && s.draining == nil && row.Draining != s.o.Cur.Load().Name {
		s.reattachDraining(ctx, row)
	}
	if row.Draining == "" && s.draining != nil {
		old := s.draining
		s.draining = nil
		if s.o.Window != nil {
			s.o.Window.Close()
		}
		s.retire(old, s.o.Cur.Load())
	}
}

// reattachDraining builds the carrier that the row says is draining and opens the
// window for it. A replica that starts or restarts during a drain begins on the
// active carrier only, and a worker that is attached to the draining carrier
// only would not be reached until the drain ended. A failure is logged and tried
// again at the next poll; it does not make the replica not ready, because the
// active carrier works.
func (s *Swapper) reattachDraining(ctx context.Context, row cluster.CarrierRow) {
	set, err := s.attach(ctx, row, row.Draining)
	if err != nil {
		xlog.Warn("This replica cannot attach the carrier that is draining; workers that are attached only to it are not reached until the drain ends",
			"carrier", row.Draining, "epoch", row.Epoch, "error", err)
		return
	}
	s.draining = set
	if s.o.Window != nil {
		s.o.Window.Open(set)
	}
	if err := s.ensureStarted(set); err != nil {
		xlog.Warn("The work of the carrier that is draining did not start on this replica", "carrier", row.Draining, "error", err)
	}
	xlog.Info("This replica attached the carrier that is draining", "carrier", row.Draining, "epoch", row.Epoch)
}

// dropListening detaches every subscription from set.
func (s *Swapper) dropListening(set *Set) *Set {
	if err := s.o.Bus.Release(set); err != nil {
		xlog.Warn("Could not detach the subscriptions from a carrier", "carrier", set.Name, "error", err)
	}
	return set
}

// retire releases old after next took over: stop listening on it, stop its work,
// hand over what it held, close it, and run the reconnect hooks once. The slow
// part runs in the background, so that the poll goes on.
func (s *Swapper) retire(old, next *Set) {
	s.dropListening(old)
	m := s.members[old]
	delete(s.members, old)
	delete(s.startErr, old)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if m != nil {
			// A run that is still going loses its carrier here. It is told why, so
			// that it is not offered to the new carrier to run again.
			m.cancel(messaging.ErrCarrierReleased)
			s.stopBounded(old.Name, m.stop)
		}
		s.handoff(old, next)
		// Messages across the flip were not ordered, so the consumers read their
		// tables once.
		s.o.Bus.NotifyReconnect()
		// The set stays open for the settle time. A call that took the old set from
		// the window or from the pointer just before they moved is still using it,
		// and a producer that was slow to flip can still put work on it. Closing it
		// first would fail those calls.
		select {
		case <-time.After(s.o.Settle):
		case <-s.done:
		}
		// The handoff is a compare-and-set, so a second run finds nothing that the
		// first took.
		if cur := s.o.Cur.Load(); cur != old {
			s.handoff(old, cur)
		}
		closeSet(old)
	}()
}

// stopBounded waits for stop, and gives up after StopGrace. Work that does not
// stop would hold the hand-over of the queue, the close of the set and the
// release of its connections for as long as it runs.
func (s *Swapper) stopBounded(name cluster.Carrier, stop func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		stop()
	}()
	timer := time.NewTimer(s.o.StopGrace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		xlog.Warn("The work of a carrier did not stop in time; going on without waiting for it",
			"carrier", name, "grace", s.o.StopGrace)
	}
}

func (s *Swapper) handoff(from, to *Set) {
	if from.Handoff == nil || to == nil {
		return
	}
	// Not bound to the context of the replica, so that a stop of the replica does
	// not drop the hand-over of the queue; Close bounds it with CloseGrace.
	ctx, cancel := context.WithTimeout(s.life, 2*time.Minute)
	defer cancel()
	if err := from.Handoff(ctx, to); err != nil {
		xlog.Warn("Could not hand over what the released carrier held", "from", from.Name, "to", to.Name, "error", err)
	}
}

// Close stops the work of every set, and closes the sets that are not in use.
// The set in use is closed by whoever built it, after this returns. It is safe to
// call more than once.
func (s *Swapper) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	members := s.members
	s.members = map[*Set]*member{}
	var extra []*Set
	for _, set := range []*Set{s.prepared, s.draining} {
		if set != nil && set != s.o.Cur.Load() {
			extra = append(extra, set)
		}
	}
	if len(extra) == 2 && extra[0] == extra[1] {
		extra = extra[:1]
	}
	s.prepared, s.draining = nil, nil
	s.mu.Unlock()
	if s.o.Window != nil {
		s.o.Window.Close()
	}

	var stopping sync.WaitGroup
	for set, m := range members {
		m.cancel(context.Canceled)
		stopping.Go(func() { s.stopBounded(set.Name, m.stop) })
	}
	stopping.Wait()
	for _, set := range extra {
		s.dropListening(set)
		closeSet(set)
	}
	s.doneOnce.Do(func() { close(s.done) })
	// The hand-over that runs in the background gets CloseGrace to finish. After
	// that it is cancelled, and Close returns when it has seen that.
	finished := make(chan struct{})
	go func() { s.wg.Wait(); close(finished) }()
	timer := time.NewTimer(s.o.CloseGrace)
	defer timer.Stop()
	select {
	case <-finished:
	case <-timer.C:
		xlog.Warn("The hand-over of a released carrier did not finish before the replica stopped; cancelling it", "grace", s.o.CloseGrace)
		s.lifeCancel()
		select {
		case <-finished:
		case <-time.After(s.o.CloseGrace):
			xlog.Warn("The hand-over of a released carrier ignored its cancel; the replica stops without it")
		}
	}
	s.lifeCancel()
}

func closeSet(set *Set) {
	if set != nil && set.Close != nil {
		set.Close()
	}
}
