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

	"github.com/google/uuid"
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

// WithConstrained marks targets that should carry requests only when no
// other target can, such as a remote reached through a relay that limits
// the data or the time of a connection. A balanced chain tries them last.
// fn runs under the manager lock and must not call back into the manager.
func WithConstrained(fn func(target config.ModelConfig) bool) Option {
	return func(m *Manager) { m.constrained = fn }
}

// Manager tracks health per target and the active target per chain.
type Manager struct {
	mu sync.Mutex
	// id tells this manager's own target publishes apart when the sync
	// layer echoes them back.
	id     string
	src    ConfigSource
	clock  Clock
	prober Prober
	onWarm func([]string)
	// constrained marks targets a balanced chain tries last; nil marks none.
	constrained func(config.ModelConfig) bool
	// pins holds every known pin by chain name, including pins for chains
	// or targets this frontend's config does not have yet: with a sync
	// layer the pin is delivered once, and a chain that appears (or is
	// rebuilt) later must still pick it up.
	pins        map[string]string
	targets     map[string]*targetState
	chains      map[string]*chainState
	subs        map[int]chan Event
	nextSub     int
	warm        []string
	warmPending bool
	closed      bool
	// hasChains mirrors len(chains) > 0 as of the last sync, so the request
	// path can check it without the lock or a config-source scan.
	hasChains atomic.Bool
	// probes counts running probes; only tests wait on it.
	probes sync.WaitGroup

	// sync shares state with other frontends; nil when standalone.
	sync StateSync
	// gate grants probing and chain decisions to one frontend; nil means
	// this manager is always the leader.
	gate   LeaderGate
	leader bool
	// applying is set while a peer's target state is applied, so the
	// transition is not published back to the peers.
	applying bool
	// pending holds publishes queued under the lock; unlockAndFlush runs
	// them after unlocking because the sync layer can call back into Apply*.
	pending []func()
	// ticks counts Run's ticks for the periodic republish; only Run uses it.
	ticks int
}

type targetState struct {
	name string
	// serving is the model that serves this target: its own name, or the
	// model an alias points to. Model loading and eviction only know it.
	serving       string
	kind          Kind
	warm          bool
	state         TargetState
	failures      []time.Time
	consecutiveOK int
	downSince     time.Time
	lastProbe     time.Time
	lastActivity  time.Time
	lastError     string
	// since and reason describe the last state change, for snapshots.
	since  time.Time
	reason Reason
	// probing is set while a probe runs, so the scheduler does not start a
	// second one for the same target.
	probing bool
	// params come from the first chain, in name order, that lists the target.
	params config.FailoverConfig
	// inFlight counts the requests of this server that the target serves
	// now, over every chain that lists it. Least busy reads it.
	inFlight int
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
	// adopted is set once a follower received the leader's decision for this
	// chain; from then on it stops choosing the active target itself.
	adopted bool
	// turn rotates the first target of a balanced chain, so round robin
	// moves on and least busy breaks ties fairly.
	turn int
}

// drained reports whether the chain takes new requests away from target.
func (ch *chainState) drained(target string) bool {
	for _, t := range ch.cfg.Targets {
		if t.Model == target {
			return t.Drain
		}
	}
	return false
}

// serves reports whether target i of the chain can take a new request in
// priority order: healthy and not drained.
func (m *Manager) serves(ch *chainState, i int) bool {
	name := ch.targets[i]
	ts := m.targets[name]
	return ts != nil && ts.state == StateHealthy && !ch.drained(name)
}

func New(src ConfigSource, opts ...Option) *Manager {
	m := &Manager{
		id:      uuid.NewString(),
		src:     src,
		clock:   realClock{},
		targets: map[string]*targetState{},
		chains:  map[string]*chainState{},
		pins:    map[string]string{},
		subs:    map[int]chan Event{},
	}
	for _, o := range opts {
		o(m)
	}
	m.leader = m.gate == nil
	return m
}

// Sync reconciles chains with the config source. There is no config-change
// hook in the loader, so this runs on every tick and on a lookup miss.
func (m *Manager) Sync() {
	m.mu.Lock()
	m.syncLocked()
	warm, deliver := m.takeWarmLocked()
	m.unlockAndFlush()
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
			pinned := m.pins[c.Name]
			if !slices.Contains(names, pinned) {
				if m.sync == nil {
					// Standalone, the pin lives with the chain: a target
					// dropped from it takes the pin along.
					delete(m.pins, c.Name)
				}
				pinned = ""
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
			if tc.IsFailover() {
				// Nested chains are rejected when a chain is saved, but a
				// target edited into a chain later slips past that check.
				// Serving its chain config would load a model with no
				// backend, so treat it as unusable.
				m.setTargetLocked(ts, StateMissing, ReasonMissing, "target is itself a failover chain")
				continue
			}
			ts.kind = KindOf(tc)
			ts.serving = tc.Name
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
			if m.sync == nil {
				// With a sync layer the shared pin outlives a chain this
				// frontend has not (re)loaded yet; standalone it does not.
				delete(m.pins, name)
			}
		}
	}
	for name := range m.targets {
		if !claimed[name] {
			delete(m.targets, name)
		}
	}
	m.hasChains.Store(len(m.chains) > 0)
	for _, ch := range m.chains {
		m.recomputeLocked(ch, "")
	}
	var warm []string
	for _, ts := range m.targets {
		if ts.warm && ts.serving != "" && !slices.Contains(warm, ts.serving) {
			warm = append(warm, ts.serving)
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

// HasChains reports whether any failover chain was configured at the last
// sync. The request path calls it on every request to skip chain bookkeeping
// on installations without chains, so it reads a flag instead of scanning
// the config source (which takes the loader's lock and copies every config).
// A chain added since the last sync is still served, because Plan syncs on a
// miss; only in-request retry is missing for it until the scheduler's next
// tick, at most one second later.
func (m *Manager) HasChains() bool {
	if m == nil {
		return false
	}
	return m.hasChains.Load()
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

// targetStates snapshots each target's health state for the metrics gauge.
func (m *Manager) targetStates() map[string]TargetState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]TargetState, len(m.targets))
	for name, ts := range m.targets {
		out[name] = ts.state
	}
	return out
}

// Reevaluate recomputes every chain. Dwell-based fail-back needs no event, so
// the scheduler calls this on every tick.
func (m *Manager) Reevaluate() {
	m.mu.Lock()
	defer m.unlockAndFlush()
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
	ts.since, ts.reason = now, reason
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
	m.queuePublishTargetLocked(ts, from)
}

// recomputeLocked picks the active target. override replaces the reason of a
// resulting switch (pin and unpin are always "manual").
func (m *Manager) recomputeLocked(ch *chainState, override Reason) {
	if m.sync != nil && !m.leader && ch.adopted && ch.pinned == "" {
		// The leader decides; deciding here too would let frontends serve
		// different targets. A pin is exempt: it fixes the active target
		// the same way on every frontend, and applying it at once gives the
		// caller read-your-writes.
		return
	}
	now := m.clock.Now()
	prev := ch.active
	next := prev
	reason := ReasonTrip
	best := -1
	for i := range ch.targets {
		if m.serves(ch, i) {
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
		if !m.serves(ch, prev) {
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
	changed := next != prev || state != ch.state
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
	if changed {
		pub := reason
		if next == prev {
			// Same reasons as the events above for a state-only change.
			pub = ReasonRecovery
			if state == ChainDegraded {
				pub = ReasonDegraded
			}
		}
		m.queuePublishChainLocked(ch, pub)
	}
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
	// held is the target whose in-flight count this attempt holds; empty
	// once released. The manager lock guards it.
	held string
}

// Plan returns the attempt order for one request. A priority chain starts
// on the active target and goes on with the other healthy targets; a
// balanced chain orders its healthy targets by its policy. A degraded chain
// tries every target, a pinned chain only the pinned target, and drained
// targets are left out unless pinned. The attempt counts as in flight on its
// current target until Release.
func (m *Manager) Plan(chain string) (*Attempt, error) {
	m.mu.Lock()
	defer m.unlockAndFlush()
	ch := m.chainLocked(chain)
	if ch == nil {
		return nil, fmt.Errorf("%w: %q", ErrChainNotFound, chain)
	}
	att := &Attempt{m: m, chain: ch.name, primary: ch.targets[0], degraded: ch.state == ChainDegraded}
	switch {
	case ch.pinned != "":
		att.targets = []string{ch.pinned}
	case ch.cfg.Balanced():
		att.targets = m.balancedLocked(ch, att.degraded)
	default:
		att.targets = m.priorityLocked(ch, att.degraded)
	}
	if len(att.targets) == 0 && ch.pinned == "" && !att.degraded {
		// Only drained targets are healthy: try the others as a degraded
		// chain would, rather than refuse the request.
		att.degraded = true
		if ch.cfg.Balanced() {
			att.targets = m.balancedLocked(ch, true)
		} else {
			att.targets = m.priorityLocked(ch, true)
		}
	}
	if len(att.targets) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoTarget, chain)
	}
	m.acquireLocked(att, att.targets[0])
	return att, nil
}

// usableLocked reports whether a request may start on target: known, not
// drained, and healthy unless every target is to be tried.
func (m *Manager) usableLocked(ch *chainState, name string, all bool) bool {
	ts := m.targets[name]
	if ts == nil || ts.state == StateMissing || ch.drained(name) {
		return false
	}
	return all || ts.state == StateHealthy
}

func (m *Manager) priorityLocked(ch *chainState, degraded bool) []string {
	var out []string
	if !degraded {
		if active := ch.targets[ch.active]; m.usableLocked(ch, active, false) {
			out = append(out, active)
		}
	}
	for _, name := range ch.targets {
		if !slices.Contains(out, name) && m.usableLocked(ch, name, degraded) {
			out = append(out, name)
		}
	}
	return out
}

// balancedLocked orders the usable targets of a balanced chain: each request
// starts one target further along the list, least busy then moves the
// targets with fewer requests in flight to the front, and constrained
// targets go last.
func (m *Manager) balancedLocked(ch *chainState, degraded bool) []string {
	var open, constrained []string
	for _, name := range ch.targets {
		if !m.usableLocked(ch, name, degraded) {
			continue
		}
		if m.constrainedLocked(name) {
			constrained = append(constrained, name)
		} else {
			open = append(open, name)
		}
	}
	turn := ch.turn
	ch.turn++
	order := func(names []string) []string {
		if len(names) == 0 {
			return nil
		}
		k := turn % len(names)
		out := append(slices.Clone(names[k:]), names[:k]...)
		if ch.cfg.Policy == config.FailoverPolicyLeastBusy {
			sort.SliceStable(out, func(i, j int) bool {
				return m.targets[out[i]].inFlight < m.targets[out[j]].inFlight
			})
		}
		return out
	}
	return append(order(open), order(constrained)...)
}

func (m *Manager) constrainedLocked(name string) bool {
	if m.constrained == nil {
		return false
	}
	cfg, ok := m.lookupTarget(name)
	return ok && m.constrained(cfg)
}

// acquireLocked moves the in-flight count of the attempt to target.
func (m *Manager) acquireLocked(a *Attempt, target string) {
	m.releaseLocked(a)
	if ts := m.targets[target]; ts != nil {
		ts.inFlight++
	}
	a.held = target
}

func (m *Manager) releaseLocked(a *Attempt) {
	if a.held == "" {
		return
	}
	// A target dropped and added again by a sync starts from zero, so the
	// count of an older attempt must not take it below.
	if ts := m.targets[a.held]; ts != nil && ts.inFlight > 0 {
		ts.inFlight--
	}
	a.held = ""
}

// Release ends the attempt's claim on its current target, so least busy
// stops counting it. It is safe to call more than once and from another
// goroutine.
func (a *Attempt) Release() {
	a.m.mu.Lock()
	defer a.m.mu.Unlock()
	a.m.releaseLocked(a)
}

// next moves to the following target and moves the in-flight count with it.
func (a *Attempt) next() bool {
	if a.i+1 >= len(a.targets) {
		return false
	}
	a.i++
	a.m.mu.Lock()
	a.m.acquireLocked(a, a.targets[a.i])
	a.m.mu.Unlock()
	return true
}

func (a *Attempt) Chain() string   { return a.chain }
func (a *Attempt) Target() string  { return a.targets[a.i] }
func (a *Attempt) Primary() string { return a.primary }
func (a *Attempt) Degraded() bool  { return a.degraded }

// Fail records err against the current target and moves to the next one. It
// returns false when no target is left.
func (a *Attempt) Fail(err error) bool {
	a.m.ReportFailure(a.Target(), err)
	return a.next()
}

// Skip moves to the next target without recording a failure, for a target
// that could not take this request although nothing is wrong with it (at
// capacity, disabled). It returns false when no target is left.
func (a *Attempt) Skip() bool { return a.next() }

// Report records err against the current target without moving on: the
// response was already committed, so nothing is left to retry.
func (a *Attempt) Report(err error) { a.m.ReportFailure(a.Target(), err) }

func (a *Attempt) Succeed() { a.m.ReportSuccess(a.Target()) }

func (m *Manager) ReportFailure(target string, err error) {
	m.mu.Lock()
	defer m.unlockAndFlush()
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
	defer m.unlockAndFlush()
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

// Pin takes effect here at once (read-your-writes), then is shared with the
// other frontends.
func (m *Manager) Pin(chain, target string) error {
	m.mu.Lock()
	ch := m.chainLocked(chain)
	if ch == nil {
		m.unlockAndFlush()
		return fmt.Errorf("%w: %q", ErrChainNotFound, chain)
	}
	if !slices.Contains(ch.targets, target) {
		m.unlockAndFlush()
		return fmt.Errorf("%w: %q", ErrTargetNotInChain, target)
	}
	prev := m.pins[chain]
	ch.pinned = target
	m.pins[chain] = target
	m.recomputeLocked(ch, ReasonManual)
	s := m.sync
	m.unlockAndFlush()
	if s == nil {
		return nil
	}
	if err := s.SetPin(chain, target); err != nil {
		m.rollbackPin(chain, target, prev)
		return err
	}
	return nil
}

// rollbackPin restores prev after a pin change that the other frontends
// never saw: serving it here alone would split the cluster. A newer change
// made meanwhile is kept.
func (m *Manager) rollbackPin(chain, applied, prev string) {
	m.mu.Lock()
	current := m.pins[chain]
	m.mu.Unlock()
	if current == applied {
		m.ApplyPin(chain, prev)
	}
}

func (m *Manager) Unpin(chain string) error {
	m.mu.Lock()
	ch := m.chainLocked(chain)
	if ch == nil {
		m.unlockAndFlush()
		return fmt.Errorf("%w: %q", ErrChainNotFound, chain)
	}
	prev := m.pins[chain]
	ch.pinned = ""
	delete(m.pins, chain)
	m.recomputeLocked(ch, ReasonManual)
	s := m.sync
	m.unlockAndFlush()
	if s == nil {
		return nil
	}
	if err := s.ClearPin(chain); err != nil {
		m.rollbackPin(chain, "", prev)
		return err
	}
	return nil
}

// Status returns every chain, sorted by name.
func (m *Manager) Status() []ChainStatus {
	m.mu.Lock()
	defer m.unlockAndFlush()
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
	defer m.unlockAndFlush()
	ch := m.chainLocked(name)
	if ch == nil {
		return ChainStatus{}, false
	}
	return m.statusLocked(ch), true
}

func (m *Manager) statusLocked(ch *chainState) ChainStatus {
	cs := ChainStatus{Name: ch.name, Policy: ch.cfg.Policy, State: ch.state, Active: ch.targets[ch.active], ActiveSince: ch.activeSince}
	if cs.Policy == "" {
		cs.Policy = config.FailoverPolicyPriority
	}
	if ch.pinned != "" {
		p := ch.pinned
		cs.Pinned = &p
	}
	for _, name := range ch.targets {
		st := TargetStatus{Model: name, Drained: ch.drained(name), Constrained: m.constrainedLocked(name)}
		if ts := m.targets[name]; ts != nil {
			st.Kind, st.Warm, st.State = ts.kind, ts.warm, ts.state
			st.InFlight = ts.inFlight
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
	// Every frontend emits the switch to its own subscribers, but only the
	// one that decided it counts it, or the cluster-wide total would be N
	// times the real one. Followers adopt the leader's switches (ApplyChain)
	// and apply pins the leader applies too. Without a sync layer each
	// frontend decides for itself, so each counts.
	if ev.Type == EventChainSwitched && (m.leader || m.sync == nil) {
		recordSwitch(ev)
	}
	for _, c := range m.subs {
		select {
		case c <- ev:
		default:
			xlog.Warn("failover: dropping event for a slow subscriber", "type", ev.Type, "chain", ev.Chain, "target", ev.Target)
		}
	}
}

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
	defer att.Release()
	for {
		var committed atomic.Bool
		err := fn(ctx, att.Target(), func() { committed.Store(true) })
		switch {
		case err == nil:
			att.Succeed()
			return nil
		case IsCapabilityGap(err) && !committed.Load():
			// This target cannot serve this kind of request at all; it is
			// not broken, so move on without counting a failure.
			if !att.Skip() {
				return err
			}
			continue
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
