package worker

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/cluster"
)

// Timings of a worker that follows a change of carrier.
const (
	// DefaultFollowMaxDelay bounds the random wait before a worker attaches to a
	// carrier it follows. Workers learn of a change within a few seconds of each
	// other, and without it they would all connect in the same instant.
	DefaultFollowMaxDelay = 10 * time.Second
	// followBackoffMin and followBackoffMax bound the wait between two attempts
	// to attach to a carrier.
	followBackoffMin = time.Second
	followBackoffMax = 30 * time.Second
	// followFastBeat is the interval of the heartbeat while a change is under
	// way or a carrier is being released, so that the news moves faster than at
	// the usual pace of ten seconds.
	followFastBeat = 2 * time.Second
	// followDrainGrace is how long a worker keeps a carrier open after the
	// cluster released it, when a request is still running on it.
	followDrainGrace = 2 * time.Minute
	// followCredentialWait bounds the wait for the credential of a carrier.
	followCredentialWait = time.Minute
	// followRecheck is how often a worker that cannot follow looks again.
	followRecheck = 30 * time.Second
)

// Attachment is a worker's hold on one carrier: a NATS connection with the verbs
// served on it, or a tunnel with the control plane mounted behind it.
type Attachment interface {
	// Connected reports whether the carrier can be used now. It is false while the
	// attachment connects and while it connects again.
	Connected() bool
	// InFlight is the number of requests that run on the carrier. The carrier is
	// not closed while one runs.
	InFlight() int
	// Close ends the hold. The process of a backend is not touched.
	Close() error
}

// failable is implemented by an attachment that can end by itself for good, for
// example when its credential cannot be renewed. The follower then attaches
// again.
type failable interface{ Failed() bool }

// Handover is what the frontend gave a worker for one carrier.
type Handover struct {
	Carrier cluster.Carrier
	// Creds holds the credential of the carrier. It keeps the credential fresh.
	Creds *workerregistry.CredentialManager
	// The address of NATS, its CA as PEM, and whether the server asks for a
	// client certificate. Set for NATS only.
	NATSURL       string
	NATSCAPEM     string
	NATSClientTLS bool
}

// Attacher connects a worker to one carrier. It returns once the attachment
// exists. It does not wait for the carrier to answer.
type Attacher func(ctx context.Context, h Handover) (Attachment, error)

// CarrierView is what the frontend last said about the carrier of the cluster.
type CarrierView struct {
	Known         bool
	Active        cluster.Carrier
	Epoch         int64
	State         cluster.State
	Target        cluster.Carrier
	Draining      cluster.Carrier
	NATSClientTLS bool
}

// Beat is the answer to a heartbeat or to a registration.
type Beat = workerregistry.HeartbeatReply

// FollowerOptions configures a Follower.
type FollowerOptions struct {
	// Attachers connect to each carrier the worker can hold.
	Attachers map[cluster.Carrier]Attacher
	// Credentials returns the manager that registers the worker for one carrier
	// and keeps its credential. It asks the frontend for that carrier.
	Credentials func(c cluster.Carrier) *workerregistry.CredentialManager
	// Heartbeat sends a heartbeat and returns the answer.
	Heartbeat func(ctx context.Context, body map[string]any) (*Beat, error)
	// Body returns the usual content of a heartbeat. May be nil.
	Body func() map[string]any
	// Cannot says, for each carrier, why this worker cannot attach to it, or
	// returns an empty string. A carrier with no entry can be attached to.
	Cannot map[cluster.Carrier]func(CarrierView) string
	// Interval is the usual interval of the heartbeat.
	Interval time.Duration
	// MaxDelay bounds the random wait before an attach. Zero uses
	// DefaultFollowMaxDelay. A negative value means no wait.
	MaxDelay time.Duration
}

// Follower keeps a worker on the carrier of the cluster.
//
// The worker never decides which carrier is active. It reads the answer of every
// heartbeat, attaches to the carrier that the frontend names (and, while a change
// is prepared, to its target), keeps the previous carrier open while the cluster
// drains it, and closes it when the cluster releases it and nothing runs on it.
// A backend process keeps serving all through: only the channel of control moves.
// A worker that cannot follow stays on the carrier it has, says why in its
// heartbeat, and tries again.
//
// What a worker reports in every heartbeat is the single source for the
// frontends of where the worker is attached.
type Follower struct {
	o FollowerOptions

	mu       sync.Mutex
	view     CarrierView
	attached map[cluster.Carrier]Attachment
	errs     map[cluster.Carrier]string
	tries    map[cluster.Carrier]int
	retryAt  map[cluster.Carrier]time.Time
	delayed  map[cluster.Carrier]int64
	relFrom  map[cluster.Carrier]time.Time

	kick chan struct{}
	beat sync.Mutex
	root context.Context
}

// NewFollower returns a follower. Run starts it.
func NewFollower(o FollowerOptions) *Follower {
	if o.Interval <= 0 {
		o.Interval = 10 * time.Second
	}
	if o.MaxDelay == 0 {
		o.MaxDelay = DefaultFollowMaxDelay
	}
	return &Follower{
		o:        o,
		attached: map[cluster.Carrier]Attachment{},
		errs:     map[cluster.Carrier]string{},
		tries:    map[cluster.Carrier]int{},
		retryAt:  map[cluster.Carrier]time.Time{},
		delayed:  map[cluster.Carrier]int64{},
		relFrom:  map[cluster.Carrier]time.Time{},
		kick:     make(chan struct{}, 1),
	}
}

// Adopt records an attachment that exists already, for example the one a worker
// started on.
func (f *Follower) Adopt(c cluster.Carrier, a Attachment, epoch int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attached[c] = a
	if f.view.Epoch == 0 {
		f.view.Epoch = epoch
	}
}

// Attach attaches to c with the credential that a registration returned, adopts
// the attachment, and records what the answer said about the cluster. A worker
// does this once at start, and the follower does it by itself afterwards.
func (f *Follower) Attach(ctx context.Context, c cluster.Carrier, mgr *workerregistry.CredentialManager, res *workerregistry.RegisterResponse) (Attachment, error) {
	attach := f.o.Attachers[c]
	if attach == nil {
		return nil, fmt.Errorf("this worker has no way to use the %s carrier", c)
	}
	h := handoverOf(c, mgr, res)
	a, err := attach(ctx, h)
	if err != nil {
		return nil, err
	}
	f.Adopt(c, a, res.CarrierEpoch)
	f.Observe(beatOf(res))
	return a, nil
}

func handoverOf(c cluster.Carrier, mgr *workerregistry.CredentialManager, res *workerregistry.RegisterResponse) Handover {
	h := Handover{Carrier: c, Creds: mgr}
	if c == cluster.CarrierNATS {
		h.NATSURL, h.NATSCAPEM, h.NATSClientTLS = res.NatsURL, res.NatsCA, res.NatsClientTLS
	}
	return h
}

// Observe records what the frontend said about the carrier, from a registration.
func (f *Follower) Observe(b *Beat) {
	if b == nil || b.Carrier == "" {
		return
	}
	f.mu.Lock()
	f.view = CarrierView{
		Known:         true,
		Active:        cluster.Carrier(b.Carrier),
		Epoch:         b.CarrierEpoch,
		State:         cluster.State(b.CarrierState),
		Target:        cluster.Carrier(b.CarrierTarget),
		Draining:      cluster.Carrier(b.CarrierDraining),
		NATSClientTLS: b.NatsClientTLS,
	}
	f.mu.Unlock()
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// View returns what the frontend last said.
func (f *Follower) View() CarrierView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view
}

// Attached returns the carriers that are connected now, in a fixed order.
func (f *Follower) Attached() []cluster.Carrier {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connectedLocked()
}

func (f *Follower) connectedLocked() []cluster.Carrier {
	var out []cluster.Carrier
	for _, c := range []cluster.Carrier{cluster.CarrierNATS, cluster.CarrierTunnel} {
		if a := f.attached[c]; a != nil && a.Connected() {
			out = append(out, c)
		}
	}
	return out
}

// Ready is nil while at least one carrier is connected. A worker whose carriers
// are all down still sends its heartbeat to the frontend, and the registry would
// show a node that looks healthy and cannot be reached.
func (f *Follower) Ready() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.connectedLocked()) > 0 {
		return nil
	}
	var held []string
	for c := range f.attached {
		held = append(held, string(c))
	}
	slices.Sort(held)
	return fmt.Errorf("worker carrier is down (%s): the frontend cannot reach this worker", strings.Join(held, ", "))
}

// Report is the carrier part of a heartbeat.
func (f *Follower) Report() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reportLocked()
}

func (f *Follower) reportLocked() map[string]any {
	attached := f.connectedLocked()
	follow := slices.Clone(attached)
	var reasons []string
	for _, c := range []cluster.Carrier{cluster.CarrierNATS, cluster.CarrierTunnel} {
		if _, ok := f.o.Attachers[c]; !ok {
			continue
		}
		reason := ""
		if cannot := f.o.Cannot[c]; cannot != nil {
			reason = cannot(f.view)
		}
		if reason == "" {
			reason = f.errs[c]
		}
		held := f.attached[c] != nil
		switch {
		case reason == "":
			if !slices.Contains(follow, c) {
				follow = append(follow, c)
			}
		case !held:
			reasons = append(reasons, fmt.Sprintf("%s: %s", c, reason))
		}
	}
	// A worker holds a carrier it can follow, even before it is connected, so a
	// report always names something.
	for c := range f.attached {
		if !slices.Contains(follow, c) {
			follow = append(follow, c)
		}
	}
	slices.Sort(follow)
	names := func(cs []cluster.Carrier) []string {
		out := make([]string, 0, len(cs))
		for _, c := range cs {
			out = append(out, string(c))
		}
		return out
	}
	return map[string]any{
		"attached":            names(attached),
		"attached_epoch":      f.view.Epoch,
		"follow_capabilities": names(follow),
		"follow_error":        strings.Join(reasons, "; "),
	}
}

// Run sends the heartbeats and follows the carrier until ctx ends. It returns
// when both loops have ended. The attachments are not closed: whoever attached
// them closes them (Close).
func (f *Follower) Run(ctx context.Context) {
	f.mu.Lock()
	f.root = ctx
	f.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); f.heartbeats(ctx) }()
	go func() { defer wg.Done(); f.reconcileLoop(ctx) }()
	wg.Wait()
}

// Close closes every attachment.
func (f *Follower) Close() {
	f.mu.Lock()
	held := f.attached
	f.attached = map[cluster.Carrier]Attachment{}
	f.mu.Unlock()
	for c, a := range held {
		if err := a.Close(); err != nil {
			xlog.Warn("Closing a carrier of the worker failed", "carrier", c, "error", err)
		}
	}
}

func (f *Follower) heartbeats(ctx context.Context) {
	wait := time.Duration(0) // the first heartbeat is at once
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		f.sendBeat(ctx)
		wait = f.nextBeat()
	}
}

// nextBeat is the wait to the next heartbeat. It is short while a change is under
// way or a carrier is being released.
func (f *Follower) nextBeat() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.view
	busy := v.Known && (v.State != cluster.StateStable || v.Draining != "")
	if !busy && v.Known {
		for c := range f.attached {
			if c != v.Active {
				busy = true
			}
		}
		if f.activeMissingLocked() {
			busy = true
		}
	}
	if busy {
		return min(f.o.Interval, followFastBeat)
	}
	return f.o.Interval
}

func (f *Follower) activeMissingLocked() bool {
	return f.view.Known && f.attached[f.view.Active] == nil
}

// sendBeat sends one heartbeat, with the report, and takes the news from the
// answer. It is skipped when no carrier is connected, as the worker has always
// done: the registry must not show a worker healthy that nobody can reach.
func (f *Follower) sendBeat(ctx context.Context) {
	f.beat.Lock()
	defer f.beat.Unlock()
	if err := f.Ready(); err != nil {
		xlog.Warn("Skipping heartbeat: " + err.Error())
		return
	}
	body := map[string]any{}
	if f.o.Body != nil {
		body = f.o.Body()
	}
	for k, v := range f.Report() {
		body[k] = v
	}
	reply, err := f.o.Heartbeat(ctx, body)
	if err != nil {
		xlog.Warn("Heartbeat failed", "error", err)
		return
	}
	f.Observe(reply)
}

func (f *Follower) reconcileLoop(ctx context.Context) {
	for {
		next := f.reconcile(ctx)
		var timer <-chan time.Time
		if next > 0 {
			timer = time.After(next)
		}
		select {
		case <-ctx.Done():
			return
		case <-f.kick:
		case <-timer:
		}
	}
}

// wanted returns the carriers the worker should hold now, active first, and the
// carriers it may keep open besides.
func wanted(v CarrierView) (hold []cluster.Carrier, keep []cluster.Carrier) {
	hold = []cluster.Carrier{v.Active}
	if v.Target != "" && v.State != cluster.StateStable && v.Target != v.Active {
		hold = append(hold, v.Target)
	}
	keep = slices.Clone(hold)
	if v.Draining != "" && !slices.Contains(keep, v.Draining) {
		keep = append(keep, v.Draining)
	}
	return hold, keep
}

// reconcile moves the attachments towards what the frontend said. It returns the
// wait until it should run again by itself, or zero.
func (f *Follower) reconcile(ctx context.Context) time.Duration {
	v := f.View()
	if !v.Known {
		return 0
	}
	hold, keep := wanted(v)
	var again time.Duration
	setAgain := func(d time.Duration) {
		if d > 0 && (again == 0 || d < again) {
			again = d
		}
	}

	// An attachment that ended for good is dropped, so that it is made again.
	f.mu.Lock()
	for c, a := range f.attached {
		if fa, ok := a.(failable); ok && fa.Failed() {
			xlog.Warn("A carrier of the worker ended and will be attached again", "carrier", c)
			delete(f.attached, c)
			go func() { _ = a.Close() }()
		}
	}
	f.mu.Unlock()

	for _, c := range hold {
		f.mu.Lock()
		have := f.attached[c] != nil
		due := f.retryAt[c]
		f.mu.Unlock()
		if have {
			continue
		}
		if wait := time.Until(due); wait > 0 {
			setAgain(wait)
			continue
		}
		if d := f.follow(ctx, c, v); d > 0 {
			setAgain(d)
		}
	}

	f.release(ctx, v, keep, setAgain)
	return again
}

// follow attaches to c. It returns the wait before the next attempt when it
// fails or cannot try.
func (f *Follower) follow(ctx context.Context, c cluster.Carrier, v CarrierView) time.Duration {
	attach := f.o.Attachers[c]
	if attach == nil {
		f.setErr(c, fmt.Sprintf("this worker has no way to use the %s carrier", c))
		return followRecheck
	}
	if cannot := f.o.Cannot[c]; cannot != nil {
		if reason := cannot(v); reason != "" {
			f.setErr(c, reason)
			return followRecheck
		}
	}
	if err := f.pause(ctx, c, v); err != nil {
		return 0
	}
	// The cluster may have moved while the worker waited.
	if now := f.View(); now.Epoch != v.Epoch {
		return time.Millisecond
	}
	cctx, cancel := context.WithTimeout(ctx, followCredentialWait)
	defer cancel()
	mgr := f.o.Credentials(c)
	res, err := mgr.Acquire(cctx)
	if err != nil {
		return f.failed(c, fmt.Errorf("getting the credential of the %s carrier: %w", c, err))
	}
	a, err := attach(f.rootCtx(ctx), handoverOf(c, mgr, res))
	if err != nil {
		return f.failed(c, fmt.Errorf("attaching to the %s carrier: %w", c, err))
	}
	f.mu.Lock()
	f.attached[c] = a
	delete(f.errs, c)
	f.tries[c] = 0
	f.mu.Unlock()
	xlog.Info("The worker attached to a carrier", "carrier", c, "epoch", v.Epoch)
	// The frontends route by what the worker reports, so tell them once the
	// carrier answers, and not at the next tick.
	go f.reportWhenConnected(ctx, c, a)
	return 0
}

func (f *Follower) rootCtx(fallback context.Context) context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.root != nil {
		return f.root
	}
	return fallback
}

func (f *Follower) setErr(c cluster.Carrier, reason string) {
	f.mu.Lock()
	f.errs[c] = reason
	f.mu.Unlock()
}

// failed records a failed attempt and returns the wait before the next one.
func (f *Follower) failed(c cluster.Carrier, err error) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[c] = err.Error()
	f.tries[c]++
	d := followBackoffMin << min(f.tries[c]-1, 5)
	d = min(d, followBackoffMax)
	f.retryAt[c] = time.Now().Add(d)
	xlog.Warn("The worker could not follow the carrier; it keeps the one it has and tries again", "carrier", c, "retry_in", d, "error", err)
	return d
}

// pause waits a random time before the first attach to a carrier at an epoch, so
// that workers do not connect in the same instant.
func (f *Follower) pause(ctx context.Context, c cluster.Carrier, v CarrierView) error {
	f.mu.Lock()
	done := f.delayed[c] == v.Epoch
	f.delayed[c] = v.Epoch
	// The first carrier of a worker that has none is not a change to follow.
	alone := len(f.attached) == 0
	f.mu.Unlock()
	if done || alone || f.o.MaxDelay < 0 {
		return nil
	}
	d := time.Duration(rand.Int64N(int64(f.o.MaxDelay) + 1)) // #nosec G404 -- a spread of reconnects, not a secret
	xlog.Info("The worker follows a change of carrier", "to", c, "epoch", v.Epoch, "after", d.Round(time.Millisecond))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// reportWhenConnected sends a heartbeat as soon as the new carrier answers.
func (f *Follower) reportWhenConnected(ctx context.Context, c cluster.Carrier, a Attachment) {
	deadline := time.Now().Add(followCredentialWait)
	for !a.Connected() {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	f.sendBeat(ctx)
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// release closes the carriers the cluster has released: not wanted, not draining,
// and with the carrier it moved to connected. A carrier with a request on it is
// kept for a grace period, because the answer to that request goes back on it.
func (f *Follower) release(ctx context.Context, v CarrierView, keep []cluster.Carrier, setAgain func(time.Duration)) {
	f.mu.Lock()
	active := f.attached[v.Active]
	moved := active != nil && active.Connected()
	var closing []cluster.Carrier
	for c, a := range f.attached {
		if slices.Contains(keep, c) {
			delete(f.relFrom, c)
			continue
		}
		if !moved {
			// The worker cannot follow, or has not yet. It keeps what it has.
			continue
		}
		since, seen := f.relFrom[c]
		if !seen {
			since = time.Now()
			f.relFrom[c] = since
		}
		if a.InFlight() > 0 && time.Since(since) < followDrainGrace {
			setAgain(time.Second)
			continue
		}
		closing = append(closing, c)
	}
	var drop []Attachment
	for _, c := range closing {
		drop = append(drop, f.attached[c])
		delete(f.attached, c)
		delete(f.relFrom, c)
	}
	f.mu.Unlock()
	if len(drop) == 0 {
		return
	}
	// Tell the frontends first: a request they send after this report goes to the
	// carrier that stays.
	f.sendBeat(ctx)
	for i, a := range drop {
		if err := a.Close(); err != nil {
			xlog.Warn("Closing a released carrier failed", "carrier", closing[i], "error", err)
		}
		xlog.Info("The worker released a carrier", "carrier", closing[i])
	}
}

// ErrNoCredentials is returned by an attacher that was not given what it needs.
var ErrNoCredentials = errors.New("the frontend did not hand over a credential for this carrier")
