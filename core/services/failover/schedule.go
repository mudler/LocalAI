package failover

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/config"
)

// Run drives probes and dwell-based fail-back until ctx ends. Only this
// scheduler goroutine calls Sync: onWarm callbacks run after the manager's
// lock is released, and a concurrent Sync from elsewhere could reorder them.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	m.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			m.close()
			return
		case <-ticker.C:
			m.Tick(ctx)
			// Publishes are fire-and-forget, so a frontend that missed one
			// (restart, dropped message) converges within ten seconds.
			m.ticks++
			if m.ticks%10 == 0 && m.IsLeader() {
				m.Republish()
			}
		}
	}
}

// Tick runs one pass: sync configs, start due probes, recompute chains. It is
// exported so tests can drive the manager without a real ticker. Like Run, it
// must only be called from the scheduler goroutine (see Run's comment on Sync).
//
// Tick does not wait for the probes it starts: one slow target (a probe that
// hangs until its timeout) must not delay probing and fail-back of every
// other chain. Each probe applies its own result, and a target whose probe is
// still running is skipped until it ends.
//
// With a leader gate, only the leader probes and decides chains; followers
// still recompute, which only moves chains that have not yet adopted a
// leader decision.
func (m *Manager) Tick(ctx context.Context) {
	m.Sync()
	m.mu.Lock()
	gate := m.gate // SetLeaderGate may replace it after construction
	m.mu.Unlock()
	if gate == nil {
		m.lead(ctx)
		return
	}
	if gate(ctx, func() { m.lead(ctx) }) {
		return
	}
	m.mu.Lock()
	m.leader = false
	m.mu.Unlock()
	m.Reevaluate()
}

// lead is the leader's share of a tick.
func (m *Manager) lead(ctx context.Context) {
	m.mu.Lock()
	became := !m.leader
	m.leader = true
	if became {
		// The previous leader owned the warm-set callback's effects;
		// deliver the set again so this frontend takes them over.
		m.warmPending = true
	}
	warm, deliver := m.takeWarmLocked()
	m.unlockAndFlush()
	if deliver && m.onWarm != nil {
		m.onWarm(warm)
	}
	if became {
		// Followers hold the old leader's view; send ours at once instead of
		// letting them wait for the periodic republish.
		m.Republish()
	}
	for _, j := range m.dueProbes() {
		m.probes.Add(1)
		go func(j probeJob) {
			defer m.probes.Done()
			m.runProbe(ctx, j)
		}(j)
	}
	m.Reevaluate()
}

// waitProbes waits for the probes started so far. Tests use it to see a
// tick's results; the scheduler never waits.
func (m *Manager) waitProbes() { m.probes.Wait() }

type probeJob struct {
	target    string
	cfg       config.ModelConfig
	kind      Kind
	warm      bool
	inference bool
	timeout   time.Duration
}

func (m *Manager) dueProbes() []probeJob {
	m.mu.Lock()
	defer m.unlockAndFlush()
	now := m.clock.Now()
	var jobs []probeJob
	for _, ts := range m.targets {
		if ts.probing {
			continue
		}
		interval := ts.params.ProbeInterval()
		inference := false
		switch ts.state {
		case StateMissing:
			continue
		case StateHealthy:
			// A served request is as good as a liveness probe.
			if now.Sub(ts.lastActivity) < interval || now.Sub(ts.lastProbe) < interval {
				continue
			}
		case StateDown:
			if ts.cold() {
				// Loading a cold model only to probe it could evict others.
				if now.Sub(ts.downSince) >= ts.params.MinDwell() {
					m.setTargetLocked(ts, StateHealthy, ReasonRecovery, "")
					m.recomputeForLocked(ts.name)
				}
				continue
			}
			if now.Sub(ts.lastProbe) < interval {
				continue
			}
		case StateRecovering:
			if ts.cold() || now.Sub(ts.lastProbe) < interval {
				continue
			}
			inference = true
		}
		if m.prober == nil {
			continue
		}
		cfg, ok := m.lookupTarget(ts.name)
		if !ok {
			continue
		}
		ts.lastProbe = now
		ts.probing = true
		jobs = append(jobs, probeJob{
			target: ts.name, cfg: cfg, kind: ts.kind, warm: ts.warm,
			inference: inference, timeout: ts.params.ProbeTimeout(),
		})
	}
	return jobs
}

func (m *Manager) runProbe(ctx context.Context, j probeJob) {
	pctx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()
	var err error
	if j.inference {
		err = m.prober.Inference(pctx, j.cfg, j.kind, j.warm)
	} else {
		err = m.prober.Liveness(pctx, j.cfg, j.kind, j.warm)
	}
	if ctx.Err() != nil {
		m.endProbe(j.target)
		return // shutting down: a cancelled probe says nothing about the target
	}
	m.applyProbe(j, err)
}

func (m *Manager) endProbe(target string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ts := m.targets[target]; ts != nil {
		ts.probing = false
	}
}

func (m *Manager) applyProbe(j probeJob, err error) {
	m.mu.Lock()
	defer m.unlockAndFlush()
	ts := m.targets[j.target]
	if ts == nil {
		return
	}
	ts.probing = false
	if ts.state == StateMissing {
		return
	}
	if errors.Is(err, ErrNotLoaded) {
		// Nothing running to confirm recovery against: judge the target like
		// a cold one, by real requests once min_dwell has passed.
		if ts.state == StateRecovering && m.clock.Now().Sub(ts.downSince) >= ts.params.MinDwell() {
			m.setTargetLocked(ts, StateHealthy, ReasonRecovery, "")
			m.recomputeForLocked(ts.name)
		}
		return
	}
	if err != nil {
		if ts.state == StateDown {
			ts.lastError = err.Error()
		} else {
			m.recordFailureLocked(ts, err.Error())
		}
		m.recomputeForLocked(ts.name)
		return
	}
	switch ts.state {
	case StateHealthy:
		ts.lastActivity = m.clock.Now()
		ts.failures = nil
	case StateDown:
		m.setTargetLocked(ts, StateRecovering, ReasonRecovery, "")
	case StateRecovering:
		if j.inference {
			m.recordPassLocked(ts)
		}
	}
	m.recomputeForLocked(ts.name)
}
