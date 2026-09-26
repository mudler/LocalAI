package failover

import (
	"context"
	"sync"
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
		}
	}
}

// Tick runs one pass: sync configs, run due probes, recompute chains. It is
// exported so tests can drive the manager without a real ticker. Like Run, it
// must only be called from the scheduler goroutine (see Run's comment on Sync).
func (m *Manager) Tick(ctx context.Context) {
	m.Sync()
	var wg sync.WaitGroup
	for _, j := range m.dueProbes() {
		wg.Add(1)
		go func(j probeJob) {
			defer wg.Done()
			m.runProbe(ctx, j)
		}(j)
	}
	wg.Wait()
	m.Reevaluate()
}

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
	defer m.mu.Unlock()
	now := m.clock.Now()
	var jobs []probeJob
	for _, ts := range m.targets {
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
		return // shutting down: a cancelled probe says nothing about the target
	}
	m.applyProbe(j, err)
}

func (m *Manager) applyProbe(j probeJob, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.targets[j.target]
	if ts == nil || ts.state == StateMissing {
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
