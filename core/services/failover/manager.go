package failover

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

var (
	ErrChainNotFound    = errors.New("failover chain not found")
	ErrTargetNotInChain = errors.New("target is not in this failover chain")
	ErrNoTarget         = errors.New("failover chain has no usable target")
)

// ConfigSource is the part of ModelConfigLoader the manager reads.
type ConfigSource interface {
	GetModelConfig(name string) (config.ModelConfig, bool)
	GetAllModelsConfigs() []config.ModelConfig
}

type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type Option func(*Manager)

func WithClock(c Clock) Option   { return func(m *Manager) { m.clock = c } }
func WithProber(p Prober) Option { return func(m *Manager) { m.prober = p } }

// WithOnWarmChanged is called outside the manager lock when the set of warm
// local targets changes. The application pins and preloads them.
func WithOnWarmChanged(fn func(warm []string)) Option { return func(m *Manager) { m.onWarm = fn } }

// Manager tracks health per target and the active target per chain.
type Manager struct {
	mu          sync.Mutex
	src         ConfigSource
	clock       Clock
	prober      Prober
	onWarm      func([]string)
	targets     map[string]*targetState
	chains      map[string]*chainState
	subs        map[int]chan Event
	nextSub     int
	warm        []string
	warmPending bool
	closed      bool
}

type targetState struct {
	name          string
	kind          Kind
	warm          bool
	state         TargetState
	failures      []time.Time
	consecutiveOK int
	downSince     time.Time
	lastProbe     time.Time
	lastActivity  time.Time
	lastError     string
	// params come from the first chain, in name order, that lists the target.
	params config.FailoverConfig
}

func (ts *targetState) cold() bool { return ts.kind == KindLocal && !ts.warm }

type chainState struct {
	name        string
	cfg         config.FailoverConfig
	targets     []string
	active      int
	activeSince time.Time
	pinned      string
	state       ChainState
}

func New(src ConfigSource, opts ...Option) *Manager {
	m := &Manager{
		src:     src,
		clock:   realClock{},
		targets: map[string]*targetState{},
		chains:  map[string]*chainState{},
		subs:    map[int]chan Event{},
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Sync reconciles chains with the config source. There is no config-change
// hook in the loader, so this runs on every tick and on a lookup miss.
func (m *Manager) Sync() {
	m.mu.Lock()
	m.syncLocked()
	warm, deliver := m.takeWarmLocked()
	m.mu.Unlock()
	if deliver && m.onWarm != nil {
		m.onWarm(warm)
	}
}

func (m *Manager) syncLocked() {
	now := m.clock.Now()
	seenChains := map[string]bool{}
	claimed := map[string]bool{}
	for _, c := range m.src.GetAllModelsConfigs() {
		if !c.IsFailover() {
			continue
		}
		seenChains[c.Name] = true
		names := make([]string, 0, len(c.Failover.Targets))
		for _, t := range c.Failover.Targets {
			names = append(names, t.Model)
		}
		ch := m.chains[c.Name]
		if ch == nil || !slices.Equal(ch.targets, names) {
			pinned := ""
			if ch != nil && slices.Contains(names, ch.pinned) {
				pinned = ch.pinned
			}
			ch = &chainState{name: c.Name, targets: names, activeSince: now, state: ChainPrimary, pinned: pinned}
			m.chains[c.Name] = ch
		}
		ch.cfg = *c.Failover
		for _, t := range c.Failover.Targets {
			ts := m.targets[t.Model]
			if ts == nil {
				ts = &targetState{name: t.Model, state: StateHealthy}
				m.targets[t.Model] = ts
			}
			if !claimed[t.Model] {
				claimed[t.Model] = true
				ts.params = *c.Failover
				ts.warm = false
			}
			tc, ok := m.lookupTarget(t.Model)
			if !ok {
				m.setTargetLocked(ts, StateMissing, ReasonMissing, "target config not found")
				continue
			}
			ts.kind = KindOf(tc)
			if t.Warm && ts.kind == KindLocal {
				ts.warm = true
			}
			if ts.state == StateMissing {
				m.setTargetLocked(ts, StateHealthy, ReasonRecovery, "")
			}
		}
	}
	for name := range m.chains {
		if !seenChains[name] {
			delete(m.chains, name)
		}
	}
	for name := range m.targets {
		if !claimed[name] {
			delete(m.targets, name)
		}
	}
	for _, ch := range m.chains {
		m.recomputeLocked(ch, "")
	}
	var warm []string
	for name, ts := range m.targets {
		if ts.warm {
			warm = append(warm, name)
		}
	}
	sort.Strings(warm)
	if !slices.Equal(warm, m.warm) {
		m.warm = warm
		m.warmPending = true
	}
}

func (m *Manager) takeWarmLocked() ([]string, bool) {
	if !m.warmPending {
		return nil, false
	}
	m.warmPending = false
	return slices.Clone(m.warm), true
}

// lookupTarget returns the config that serves a target, one alias hop deep.
func (m *Manager) lookupTarget(name string) (config.ModelConfig, bool) {
	c, ok := m.src.GetModelConfig(name)
	if ok && c.IsAlias() {
		return m.src.GetModelConfig(c.Alias)
	}
	return c, ok
}

func (m *Manager) chainLocked(name string) *chainState {
	if ch := m.chains[name]; ch != nil {
		return ch
	}
	m.syncLocked()
	return m.chains[name]
}

// targetLocked looks up a target's health state, syncing lazily on a miss so
// ReportFailure/ReportSuccess work before the first Plan or Sync call.
func (m *Manager) targetLocked(name string) *targetState {
	if ts := m.targets[name]; ts != nil {
		return ts
	}
	m.syncLocked()
	return m.targets[name]
}

// WarmTargets returns the warm local targets, sorted.
func (m *Manager) WarmTargets() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.warm)
}

// Reevaluate recomputes every chain. Dwell-based fail-back needs no event, so
// the scheduler calls this on every tick.
func (m *Manager) Reevaluate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.chains {
		m.recomputeLocked(ch, "")
	}
}

func (m *Manager) setTargetLocked(ts *targetState, to TargetState, reason Reason, errMsg string) {
	if ts.state == to {
		return
	}
	from := ts.state
	now := m.clock.Now()
	ts.state = to
	switch to {
	case StateDown:
		ts.downSince = now
		ts.consecutiveOK = 0
		ts.failures = nil
	case StateRecovering, StateHealthy:
		ts.consecutiveOK = 0
		ts.failures = nil
	}
	m.emitLocked(Event{Type: EventTargetState, Target: ts.name, From: string(from), To: string(to), Reason: reason, Error: errMsg, At: now})
}

// recomputeLocked picks the active target. override replaces the reason of a
// resulting switch (pin and unpin are always "manual").
func (m *Manager) recomputeLocked(ch *chainState, override Reason) {
	now := m.clock.Now()
	prev := ch.active
	next := prev
	reason := ReasonTrip
	best := -1
	for i, name := range ch.targets {
		if ts := m.targets[name]; ts != nil && ts.state == StateHealthy {
			best = i
			break
		}
	}
	switch {
	case ch.pinned != "":
		next = slices.Index(ch.targets, ch.pinned)
		reason = ReasonManual
	case best == -1:
		// Nothing is healthy: keep the active target, Plan tries all of them.
	case best > prev:
		next = best // the active target is not healthy
	case best < prev:
		cur := m.targets[ch.targets[prev]]
		curHealthy := cur != nil && cur.state == StateHealthy
		if !curHealthy {
			next = best
		} else if now.Sub(ch.activeSince) >= ch.cfg.MinDwell() {
			next = best
			reason = ReasonRecovery
		}
	}
	if override != "" {
		reason = override
	}
	var state ChainState
	switch {
	case ch.pinned == "" && best == -1:
		state = ChainDegraded
	case next == 0:
		state = ChainPrimary
	default:
		state = ChainFallback
	}
	switch {
	case next != prev:
		ch.active = next
		ch.activeSince = now
		m.emitLocked(Event{Type: EventChainSwitched, Chain: ch.name, From: ch.targets[prev], To: ch.targets[next], State: string(state), Reason: reason, At: now})
	case state == ChainDegraded && ch.state != ChainDegraded:
		// Entering degraded with no active-target change (every target is down).
		m.emitLocked(Event{Type: EventChainSwitched, Chain: ch.name, From: ch.targets[prev], To: ch.targets[next], State: string(state), Reason: ReasonDegraded, At: now})
	case state != ChainDegraded && ch.state == ChainDegraded:
		// Leaving degraded with no active-target change (the active target
		// itself recovered): SSE/realtime consumers watch chain.switched.state,
		// so this must fire or they stay on "degraded" forever.
		m.emitLocked(Event{Type: EventChainSwitched, Chain: ch.name, From: ch.targets[prev], To: ch.targets[next], State: string(state), Reason: ReasonRecovery, At: now})
	}
	ch.state = state
}

func (m *Manager) recomputeForLocked(target string) {
	for _, ch := range m.chains {
		if slices.Contains(ch.targets, target) {
			m.recomputeLocked(ch, "")
		}
	}
}

// Attempt walks the targets of one request in order.
type Attempt struct {
	m        *Manager
	chain    string
	primary  string
	degraded bool
	targets  []string
	i        int
}

// Plan returns the attempt order for one request: the active target, then the
// other healthy targets. A degraded chain tries every target in priority
// order; a pinned chain only the pinned target.
func (m *Manager) Plan(chain string) (*Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.chainLocked(chain)
	if ch == nil {
		return nil, fmt.Errorf("%w: %q", ErrChainNotFound, chain)
	}
	att := &Attempt{m: m, chain: ch.name, primary: ch.targets[0], degraded: ch.state == ChainDegraded}
	usable := func(name string) bool {
		ts := m.targets[name]
		if ts == nil || ts.state == StateMissing {
			return false
		}
		return att.degraded || ts.state == StateHealthy
	}
	switch {
	case ch.pinned != "":
		att.targets = []string{ch.pinned}
	case att.degraded:
		for _, name := range ch.targets {
			if usable(name) {
				att.targets = append(att.targets, name)
			}
		}
	default:
		active := ch.targets[ch.active]
		if usable(active) {
			att.targets = append(att.targets, active)
		}
		for _, name := range ch.targets {
			if name != active && usable(name) {
				att.targets = append(att.targets, name)
			}
		}
	}
	if len(att.targets) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoTarget, chain)
	}
	return att, nil
}

func (a *Attempt) Chain() string   { return a.chain }
func (a *Attempt) Target() string  { return a.targets[a.i] }
func (a *Attempt) Primary() string { return a.primary }
func (a *Attempt) Degraded() bool  { return a.degraded }

// Fail records err against the current target and moves to the next one. It
// returns false when no target is left.
func (a *Attempt) Fail(err error) bool {
	a.m.ReportFailure(a.Target(), err)
	if a.i+1 >= len(a.targets) {
		return false
	}
	a.i++
	return true
}

// Report records err against the current target without moving on: the
// response was already committed, so nothing is left to retry.
func (a *Attempt) Report(err error) { a.m.ReportFailure(a.Target(), err) }

func (a *Attempt) Succeed() { a.m.ReportSuccess(a.Target()) }

func (m *Manager) ReportFailure(target string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.targetLocked(target)
	if ts == nil {
		return
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.recordFailureLocked(ts, msg)
	m.recomputeForLocked(target)
}

func (m *Manager) recordFailureLocked(ts *targetState, msg string) {
	now := m.clock.Now()
	ts.lastError = msg
	switch ts.state {
	case StateRecovering:
		m.setTargetLocked(ts, StateDown, ReasonTrip, msg)
	case StateHealthy:
		cut := now.Add(-ts.params.TripWindow())
		kept := ts.failures[:0]
		for _, f := range ts.failures {
			if f.After(cut) {
				kept = append(kept, f)
			}
		}
		ts.failures = append(kept, now)
		if len(ts.failures) >= ts.params.TripErrors() {
			m.setTargetLocked(ts, StateDown, ReasonTrip, msg)
		}
	}
}

func (m *Manager) ReportSuccess(target string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.targetLocked(target)
	if ts == nil {
		return
	}
	ts.lastActivity = m.clock.Now()
	m.recordPassLocked(ts)
	m.recomputeForLocked(target)
}

// recordPassLocked counts a served request or a passed inference probe.
func (m *Manager) recordPassLocked(ts *targetState) {
	switch ts.state {
	case StateHealthy:
		ts.failures = nil
		return
	case StateMissing:
		return
	case StateDown:
		if ts.cold() {
			// Cold targets are never probed; a served request is proof enough.
			m.setTargetLocked(ts, StateHealthy, ReasonRecovery, "")
			return
		}
		m.setTargetLocked(ts, StateRecovering, ReasonRecovery, "")
	}
	ts.consecutiveOK++
	if ts.consecutiveOK >= ts.params.RecoveryProbes() {
		m.setTargetLocked(ts, StateHealthy, ReasonRecovery, "")
	}
}

func (m *Manager) Pin(chain, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.chainLocked(chain)
	if ch == nil {
		return fmt.Errorf("%w: %q", ErrChainNotFound, chain)
	}
	if !slices.Contains(ch.targets, target) {
		return fmt.Errorf("%w: %q", ErrTargetNotInChain, target)
	}
	ch.pinned = target
	m.recomputeLocked(ch, ReasonManual)
	return nil
}

func (m *Manager) Unpin(chain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.chainLocked(chain)
	if ch == nil {
		return fmt.Errorf("%w: %q", ErrChainNotFound, chain)
	}
	ch.pinned = ""
	m.recomputeLocked(ch, ReasonManual)
	return nil
}

// Status returns every chain, sorted by name.
func (m *Manager) Status() []ChainStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncLocked()
	names := make([]string, 0, len(m.chains))
	for name := range m.chains {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ChainStatus, 0, len(names))
	for _, name := range names {
		out = append(out, m.statusLocked(m.chains[name]))
	}
	return out
}

func (m *Manager) ChainStatus(name string) (ChainStatus, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.chainLocked(name)
	if ch == nil {
		return ChainStatus{}, false
	}
	return m.statusLocked(ch), true
}

func (m *Manager) statusLocked(ch *chainState) ChainStatus {
	cs := ChainStatus{Name: ch.name, State: ch.state, Active: ch.targets[ch.active], ActiveSince: ch.activeSince}
	if ch.pinned != "" {
		p := ch.pinned
		cs.Pinned = &p
	}
	for _, name := range ch.targets {
		st := TargetStatus{Model: name}
		if ts := m.targets[name]; ts != nil {
			st.Kind, st.Warm, st.State = ts.kind, ts.warm, ts.state
			st.ConsecutiveOK, st.LastError = ts.consecutiveOK, ts.lastError
			if !ts.lastProbe.IsZero() {
				lp := ts.lastProbe
				st.LastProbe = &lp
			}
		}
		cs.Targets = append(cs.Targets, st)
	}
	return cs
}

// Subscribe returns a buffered event channel and a cancel func. A subscriber
// that does not keep up loses events rather than blocking the manager.
func (m *Manager) Subscribe(buffer int) (<-chan Event, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan Event, buffer)
	if m.closed {
		close(ch)
		return ch, func() {}
	}
	id := m.nextSub
	m.nextSub++
	m.subs[id] = ch
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if c, ok := m.subs[id]; ok {
				delete(m.subs, id)
				close(c)
			}
		})
	}
}

func (m *Manager) emitLocked(ev Event) {
	for _, c := range m.subs {
		select {
		case c <- ev:
		default:
			xlog.Warn("failover: dropping event for a slow subscriber", "type", ev.Type, "chain", ev.Chain, "target", ev.Target)
		}
	}
}

//nolint:unused // wired by the Task 5 probe scheduler's Stop, which owns the manager's lifecycle
func (m *Manager) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for id, c := range m.subs {
		close(c)
		delete(m.subs, id)
	}
}

// Do runs fn against the chain's targets in plan order. fn calls commit once
// output has reached the client; after that a failure is not retried.
func (m *Manager) Do(ctx context.Context, chain string, fn func(ctx context.Context, target string, commit func()) error) error {
	att, err := m.Plan(chain)
	if err != nil {
		return err
	}
	for {
		var committed atomic.Bool
		err := fn(ctx, att.Target(), func() { committed.Store(true) })
		switch {
		case err == nil:
			att.Succeed()
			return nil
		case ctx.Err() != nil || !IsRetryable(err, 0):
			return err
		case committed.Load():
			att.Report(err)
			return err
		case !att.Fail(err):
			return err
		}
	}
}

// Prober checks targets. Implemented by DefaultProber (prober.go).
type Prober interface {
	// Liveness is the cheap steady-state check.
	Liveness(ctx context.Context, target config.ModelConfig, kind Kind, warm bool) error
	// Inference sends one minimal real request to confirm recovery.
	Inference(ctx context.Context, target config.ModelConfig, kind Kind, warm bool) error
}
