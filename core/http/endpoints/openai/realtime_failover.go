package openai

import (
	"context"
	"sort"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/services/failover"
)

// isChainStage reports whether stage names a failover chain.
func (m *wrappedModel) isChainStage(stage string) bool {
	_, ok := m.stageChains[stage]
	return ok && m.failover != nil
}

// stageCall runs fn with the config that should serve stage now. A plain
// stage uses base. A chain stage goes through the failover plan on every
// call, so a switch takes effect on the next call without rebuilding the
// session, and fn is retried on the next target until it calls commit.
func (m *wrappedModel) stageCall(ctx context.Context, stage string, base *config.ModelConfig, fn func(cfg *config.ModelConfig, commit func()) error) error {
	if !m.isChainStage(stage) {
		return fn(base, func() {})
	}
	chain := m.stageChains[stage]
	return m.failover.Do(ctx, chain, func(ctx context.Context, target string, commit func()) error {
		cfg, err := m.stageTargetConfig(target)
		if err != nil {
			return err
		}
		err = fn(cfg, commit)
		if err != nil {
			failover.RecordAttemptTrace(m.appTracing, chain, target, err)
		}
		return err
	})
}

// startFailoverEvents tells the client which target serves each chain stage
// now, and again whenever a chain switches. The returned func stops it.
func startFailoverEvents(t Transport, fm *failover.Manager, stageChains map[string]string) func() {
	// Subscribe before reading the status, so a switch that lands in
	// between is still delivered.
	events, cancel := fm.Subscribe(16)
	stages := make([]string, 0, len(stageChains))
	for s := range stageChains {
		stages = append(stages, s)
	}
	sort.Strings(stages)
	for _, stage := range stages {
		chain := stageChains[stage]
		if st, ok := fm.ChainStatus(chain); ok {
			sendEvent(t, types.ModelFailoverEvent{Chain: chain, Stage: stage, To: st.Active, State: string(st.State), Reason: string(failover.ReasonInitial)})
		}
	}
	go func() {
		for ev := range events {
			if ev.Type != failover.EventChainSwitched {
				continue
			}
			for _, stage := range stages {
				if stageChains[stage] == ev.Chain {
					sendEvent(t, types.ModelFailoverEvent{Chain: ev.Chain, Stage: stage, From: ev.From, To: ev.To, State: ev.State, Reason: string(ev.Reason)})
				}
			}
		}
	}()
	return cancel
}
