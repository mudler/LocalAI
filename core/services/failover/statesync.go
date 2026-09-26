package failover

import (
	"context"
	"slices"
	"time"

	"github.com/mudler/xlog"
)

// TargetSnapshot is one target's health as shared between frontends.
type TargetSnapshot struct {
	Target        string      `json:"target"`
	State         TargetState `json:"state"`
	Reason        Reason      `json:"reason"`
	Error         string      `json:"error,omitempty"`
	ConsecutiveOK int         `json:"consecutive_ok"`
	Since         time.Time   `json:"since"`
}

// ChainSnapshot is one chain's active target as decided by the leader.
type ChainSnapshot struct {
	Chain       string     `json:"chain"`
	Active      string     `json:"active"`
	ActiveSince time.Time  `json:"active_since"`
	State       ChainState `json:"state"`
	Reason      Reason     `json:"reason"`
}

// StateSync shares failover state between frontends. Implementations may
// deliver a publish back to the publisher synchronously (NATS echoes), so the
// manager never calls it while holding its lock.
type StateSync interface {
	PublishTarget(TargetSnapshot)
	PublishChain(ChainSnapshot)
	SetPin(chain, target string) error
	ClearPin(chain string) error
	Pins() map[string]string
}

// LeaderGate runs fn only on the one frontend that holds leadership and
// reports whether it did. Probing and chain decisions happen on the leader
// only, so N frontends do not probe every target N times or disagree on the
// active target.
type LeaderGate func(ctx context.Context, fn func()) bool

// WithLeaderGate makes the manager probe and decide chains only while the gate
// grants leadership. Without it the manager is always the leader.
func WithLeaderGate(g LeaderGate) Option { return func(m *Manager) { m.gate = g } }

// SetStateSync attaches the sync layer and hydrates pins from it. Pins are
// read outside the lock because the store may need I/O.
func (m *Manager) SetStateSync(s StateSync) {
	m.mu.Lock()
	m.sync = s
	m.mu.Unlock()
	if s == nil {
		return
	}
	for chain, target := range s.Pins() {
		m.ApplyPin(chain, target)
	}
}

// IsLeader reports whether this manager probed and decided chains at its last
// tick. A standalone manager (no gate) is always the leader.
func (m *Manager) IsLeader() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.leader
}

// unlockAndFlush releases the lock and then runs the publishes queued while
// it was held: the sync layer may call straight back into Apply*, which takes
// the lock again.
func (m *Manager) unlockAndFlush() {
	pending := m.pending
	m.pending = nil
	m.mu.Unlock()
	for _, f := range pending {
		f()
	}
}

func (m *Manager) targetSnapshotLocked(ts *targetState) TargetSnapshot {
	since := ts.since
	if ts.state == StateDown {
		since = ts.downSince
	}
	return TargetSnapshot{
		Target: ts.name, State: ts.state, Reason: ts.reason, Error: ts.lastError,
		ConsecutiveOK: ts.consecutiveOK, Since: since,
	}
}

func (m *Manager) chainSnapshotLocked(ch *chainState, reason Reason) ChainSnapshot {
	return ChainSnapshot{
		Chain: ch.name, Active: ch.targets[ch.active], ActiveSince: ch.activeSince,
		State: ch.state, Reason: reason,
	}
}

// queuePublishTargetLocked shares a local target transition. Missing is a fact
// about this frontend's config, not about the target, so it is never shared.
func (m *Manager) queuePublishTargetLocked(ts *targetState, from TargetState) {
	if m.sync == nil || m.applying || ts.state == StateMissing || from == StateMissing {
		return
	}
	s, snap := m.sync, m.targetSnapshotLocked(ts)
	m.pending = append(m.pending, func() { s.PublishTarget(snap) })
}

func (m *Manager) queuePublishChainLocked(ch *chainState, reason Reason) {
	if m.sync == nil || !m.leader {
		return
	}
	s, snap := m.sync, m.chainSnapshotLocked(ch, reason)
	m.pending = append(m.pending, func() { s.PublishChain(snap) })
}

// ApplyTarget takes a target state published by any frontend, this one
// included. The echo of an own publish finds the same state and does nothing.
func (m *Manager) ApplyTarget(s TargetSnapshot) {
	m.mu.Lock()
	defer m.unlockAndFlush()
	ts := m.targetLocked(s.Target)
	if ts == nil || ts.state == StateMissing || s.State == StateMissing {
		return
	}
	if ts.state == s.State {
		return
	}
	m.applying = true
	m.setTargetLocked(ts, s.State, s.Reason, s.Error)
	m.applying = false
	// Keep the publisher's clock, so dwell timers (cold recovery, fail-back)
	// run from when the target actually changed, not from when we heard.
	if !s.Since.IsZero() {
		ts.since = s.Since
		if s.State == StateDown {
			ts.downSince = s.Since
		}
	}
	ts.consecutiveOK = s.ConsecutiveOK
	if s.Error != "" {
		ts.lastError = s.Error
	}
	m.recomputeForLocked(ts.name)
}

// ApplyChain adopts the leader's decision for a chain. The leader ignores it:
// it is the source of these decisions, and a late publish from a previous
// leader must not undo its own.
func (m *Manager) ApplyChain(s ChainSnapshot) {
	m.mu.Lock()
	defer m.unlockAndFlush()
	if m.leader {
		return
	}
	ch := m.chainLocked(s.Chain)
	if ch == nil {
		return
	}
	next := slices.Index(ch.targets, s.Active)
	if next < 0 {
		// The frontends disagree on the chain's targets while a config
		// change propagates; keep deciding locally until they agree.
		xlog.Debug("failover: ignoring chain state for an unknown target", "chain", s.Chain, "target", s.Active)
		return
	}
	prev := ch.active
	changed := next != prev || s.State != ch.state
	ch.active, ch.activeSince, ch.state, ch.adopted = next, s.ActiveSince, s.State, true
	if changed {
		m.emitLocked(Event{Type: EventChainSwitched, Chain: ch.name, From: ch.targets[prev], To: ch.targets[next], State: string(s.State), Reason: s.Reason, At: m.clock.Now()})
	}
}

// ApplyPin sets (target != "") or clears a pin set on any frontend. A pin for
// a chain or target this frontend does not know yet is kept and applied by
// syncLocked once the config catches up.
func (m *Manager) ApplyPin(chain, target string) {
	m.mu.Lock()
	defer m.unlockAndFlush()
	if target == "" {
		delete(m.pins, chain)
	} else {
		m.pins[chain] = target
	}
	ch := m.chainLocked(chain)
	if ch == nil {
		return
	}
	if target != "" && !slices.Contains(ch.targets, target) {
		xlog.Debug("failover: deferring pin to a target not in the chain yet", "chain", chain, "target", target)
		return
	}
	if ch.pinned == target {
		return
	}
	ch.pinned = target
	m.recomputeLocked(ch, ReasonManual)
}

// Republish sends every target and chain state, so frontends that missed a
// publish (joined late, dropped a message) converge. Only the leader's view
// is authoritative, so followers do nothing.
func (m *Manager) Republish() {
	m.mu.Lock()
	defer m.unlockAndFlush()
	if m.sync == nil || !m.leader {
		return
	}
	s := m.sync
	for _, ts := range m.targets {
		if ts.state == StateMissing {
			continue
		}
		snap := m.targetSnapshotLocked(ts)
		m.pending = append(m.pending, func() { s.PublishTarget(snap) })
	}
	for _, ch := range m.chains {
		m.queuePublishChainLocked(ch, ReasonInitial)
	}
}
