package nodes

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mudler/xlog"
)

// The cold-load hold has to satisfy two requirements that a single wall-clock
// timeout cannot express at once:
//
//  1. A legitimately huge checkpoint must be allowed to finish. Staging time is
//     bytes/bandwidth, not a constant: a 70 GB model at 26 MB/s needs ~45m and a
//     600 GB checkpoint needs hours. Any fixed ceiling is therefore a model-size
//     cliff — raise it and the cliff simply moves to the next larger model.
//  2. A worker that died mid-transfer must still release the per-model advisory
//     lock promptly, or it pins every other replica's request for that model.
//
// The discriminator between the two is progress: bytes moving means healthy,
// silence means wedged. So the hold is a deadline that is *pushed forward* every
// time the transfer reports bytes, rather than a countdown from when the load
// began. Duration then scales with the work instead of with the clock.

const (
	// stagingStallWindow is how long the transfer may report zero bytes before
	// the load is declared wedged. It replaces the old fixed
	// modelLoadStagingMargin: the same 5 minutes, but rolling rather than
	// one-shot, so it bounds silence instead of bounding total transfer size.
	//
	// 5m is well beyond any legitimate gap in a healthy stream (the receiving
	// worker fsyncing a multi-GB shard, a transient LAN blip inside the
	// resumable-upload retry loop, a GC pause) while still releasing the
	// advisory lock quickly enough that a dead worker is not felt as an outage.
	stagingStallWindow = 5 * time.Minute

	// modelLoadAbsoluteMax bounds the hold even while progress keeps arriving,
	// so a degenerate peer that trickles a few bytes per stall window forever
	// cannot pin the advisory lock for all time. It is deliberately far above
	// any legitimate transfer: 600 GB at the 26 MB/s measured in production is
	// ~6.5h, and at a pessimistic 10 MB/s ~17h, so 24h leaves real headroom
	// while still guaranteeing the lock is eventually released.
	modelLoadAbsoluteMax = 24 * time.Hour

	// loadProgressQuantumDivisor coarsens progress observation. The byte-level
	// callback fires on every read of the upload body (~32 KB), which is far too
	// often to touch a timer; extending at most once per stall/20 keeps the hot
	// path cheap while costing at most 5% of the stall window in precision.
	loadProgressQuantumDivisor = 20

	// loadProgressQuantumMax caps the coarsening so a long stall window doesn't
	// make progress observation itself laggy.
	loadProgressQuantumMax = time.Second
)

// errLoadDeadlineExpired is the cancellation cause recorded when the cold-load
// hold expires, so loadDeadlineContext can report context.DeadlineExceeded and
// stay distinguishable from a caller-driven cancel.
var errLoadDeadlineExpired = errors.New("cold-load deadline expired")

// loadDeadline is the mutable expiry behind a loadDeadlineContext. Observe()
// pushes the expiry forward; the absolute cap and the parent context still
// bound it.
type loadDeadline struct {
	mu          sync.Mutex
	timer       *time.Timer
	stall       time.Duration
	quantum     time.Duration
	expiry      time.Time
	hardExpiry  time.Time
	lastObserve time.Time
	extensions  int
	stopped     bool
	cancel      context.CancelCauseFunc
}

// loadDeadlineKey retrieves the active loadDeadline from a context chain.
type loadDeadlineKey struct{}

// loadDeadlineContext is a context whose expiry moves forward while the work it
// covers reports progress.
//
// It reports context.DeadlineExceeded (not context.Canceled) on expiry, because
// downstream code — notably the resumable upload loop in file_stager_http.go —
// branches on that distinction to tell "we ran out of budget" apart from "the
// caller gave up", and the two lead to different retry decisions.
type loadDeadlineContext struct {
	context.Context
	d *loadDeadline
}

// Deadline reports the ABSOLUTE cap rather than the current rolling expiry.
// Children derive their own budgets from the parent deadline (a gRPC
// context.WithTimeout takes the earlier of the two), and the rolling expiry is
// a floor that moves, not a promise that the work stops then. Reporting the
// rolling value would let a child silently inherit a few seconds of budget.
func (c *loadDeadlineContext) Deadline() (time.Time, bool) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	return c.d.hardExpiry, true
}

func (c *loadDeadlineContext) Err() error {
	err := c.Context.Err()
	if err == nil {
		return nil
	}
	if errors.Is(context.Cause(c.Context), errLoadDeadlineExpired) {
		return context.DeadlineExceeded
	}
	return err
}

func (c *loadDeadlineContext) Value(key any) any {
	if _, ok := key.(loadDeadlineKey); ok {
		return c.d
	}
	return c.Context.Value(key)
}

// newLoadDeadlineContext builds a cold-load hold context. base is the initial
// budget granted before any progress is seen (it covers the steps that report
// none: node selection, backend install, the remote LoadModel). stall is how
// long zero progress is tolerated once the transfer has started, and absoluteMax
// bounds the whole hold regardless of progress.
//
// Non-positive base/stall/absoluteMax fall back to their package defaults, and
// stall is clamped to base so a caller with a deliberately tight ceiling (tests,
// or an operator who wants fast failure) doesn't get a stall window wider than
// the ceiling it was derived from.
func newLoadDeadlineContext(parent context.Context, base, stall, absoluteMax time.Duration) (context.Context, context.CancelFunc) {
	if base <= 0 {
		base = minModelLoadCeiling
	}
	if stall <= 0 {
		stall = stagingStallWindow
	}
	if stall > base {
		stall = base
	}
	if absoluteMax <= 0 {
		absoluteMax = modelLoadAbsoluteMax
	}
	if absoluteMax < base {
		absoluteMax = base
	}

	ctx, cancel := context.WithCancelCause(parent)
	now := time.Now()

	quantum := stall / loadProgressQuantumDivisor
	if quantum > loadProgressQuantumMax {
		quantum = loadProgressQuantumMax
	}

	d := &loadDeadline{
		stall:      stall,
		quantum:    quantum,
		expiry:     now.Add(base),
		hardExpiry: now.Add(absoluteMax),
		cancel:     cancel,
	}
	d.timer = time.AfterFunc(base, d.fire)

	// The absolute cap is a plain one-shot: progress can never move it.
	hardTimer := time.AfterFunc(absoluteMax, func() {
		xlog.Warn("Cold-load hold hit its absolute cap while still reporting progress; releasing the model lock",
			"cap", absoluteMax)
		d.stop()
		cancel(errLoadDeadlineExpired)
	})

	stopAll := func() {
		hardTimer.Stop()
		d.stop()
		cancel(context.Canceled)
	}
	return &loadDeadlineContext{Context: ctx, d: d}, stopAll
}

func (d *loadDeadline) fire() {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	// A late-arriving Observe may have pushed the expiry out after this timer
	// was already scheduled to fire; re-arm instead of cancelling.
	if remaining := time.Until(d.expiry); remaining > 0 {
		d.timer.Reset(remaining)
		d.mu.Unlock()
		return
	}
	d.stopped = true
	extensions := d.extensions
	stall := d.stall
	d.mu.Unlock()

	if extensions > 0 {
		xlog.Warn("Cold load stalled: no staging progress within the stall window, releasing the model lock",
			"stallWindow", stall, "progressExtensions", extensions)
	}
	d.cancel(errLoadDeadlineExpired)
}

func (d *loadDeadline) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
	}
}

// Observe records that real work happened, pushing the expiry out by a full
// stall window. It never pulls the expiry in, so it cannot shorten the base
// budget, and it never pushes past the absolute cap.
func (d *loadDeadline) Observe() {
	if d == nil {
		return
	}
	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	// Coarsen: the byte callback fires per read, we only need per-quantum.
	if !d.lastObserve.IsZero() && now.Sub(d.lastObserve) < d.quantum {
		return
	}
	d.lastObserve = now

	next := now.Add(d.stall)
	if next.After(d.hardExpiry) {
		next = d.hardExpiry
	}
	if !next.After(d.expiry) {
		return
	}
	d.expiry = next
	d.extensions++
	d.timer.Reset(time.Until(next))
}

// observeLoadProgress pushes out the cold-load deadline attached to ctx, if any.
// Callers report byte-level movement here; a context without a load deadline
// (single-host paths, tests constructing stagers directly) is a no-op.
func observeLoadProgress(ctx context.Context) {
	if d, ok := ctx.Value(loadDeadlineKey{}).(*loadDeadline); ok {
		d.Observe()
	}
}
