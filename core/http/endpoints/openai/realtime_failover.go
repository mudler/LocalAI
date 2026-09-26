package openai

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/pkg/model"
)

// stageRouter routes realtime pipeline stages that name a failover chain.
// Every realtime model kind (full pipeline, transcription-only, sound-only)
// embeds one, so a chain resolves the same way whatever the session does.
type stageRouter struct {
	// failover and stageChains route pipeline stages that name a failover
	// chain; stageChains maps a stage ("llm", "tts", ...) to its chain.
	// The model's *Config fields then hold the target that was active at
	// session start, for the checks that run once (voice, templates).
	failover          *failover.Manager
	stageChains       map[string]string
	stageTargetConfig func(name string) (*config.ModelConfig, error)
	appTracing        bool
}

func newStageRouter(fm *failover.Manager, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) stageRouter {
	return stageRouter{
		failover:    fm,
		stageChains: map[string]string{},
		stageTargetConfig: func(name string) (*config.ModelConfig, error) {
			cfg, err := cl.LoadResolvedModelConfig(name, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
			if err != nil {
				return nil, err
			}
			failover.PrepareTarget(cfg)
			return cfg, nil
		},
		appTracing: appConfig.EnableTracing,
	}
}

// resolveStage records a stage that names a chain and returns the chain's
// active target, so everything that inspects stage configs at session start
// sees a real model. Any other config is returned as is. A chain config
// reaching the model loader would have no backend and trigger backend
// auto-detection.
func (r *stageRouter) resolveStage(stage string, cfg *config.ModelConfig) (*config.ModelConfig, error) {
	if cfg == nil || !cfg.IsFailover() {
		return cfg, nil
	}
	if r.failover == nil {
		return nil, fmt.Errorf("pipeline %s stage %q is a failover chain, but failover is not running", stage, cfg.Name)
	}
	st, ok := r.failover.ChainStatus(cfg.Name)
	if !ok {
		return nil, fmt.Errorf("failover chain %q not found", cfg.Name)
	}
	r.stageChains[stage] = cfg.Name
	return r.stageTargetConfig(st.Active)
}

// isChainStage reports whether stage names a failover chain.
func (r *stageRouter) isChainStage(stage string) bool {
	_, ok := r.stageChains[stage]
	return ok && r.failover != nil
}

// hasChainStages reports whether any stage names a chain.
func (r *stageRouter) hasChainStages() bool {
	return r.failover != nil && len(r.stageChains) > 0
}

func (r *stageRouter) router() *stageRouter { return r }

// stageRouted is implemented by every realtime model that embeds a
// stageRouter; the session uses it to send failover events.
type stageRouted interface{ router() *stageRouter }

// stageCall runs fn with the config that should serve stage now. A plain
// stage uses base. A chain stage goes through the failover plan on every
// call, so a switch takes effect on the next call without rebuilding the
// session, and fn is retried on the next target until it calls commit.
func (r *stageRouter) stageCall(ctx context.Context, stage string, base *config.ModelConfig, fn func(cfg *config.ModelConfig, commit func()) error) error {
	if !r.isChainStage(stage) {
		return fn(base, func() {})
	}
	chain := r.stageChains[stage]
	return r.failover.Do(ctx, chain, func(ctx context.Context, target string, commit func()) error {
		cfg, err := r.stageTargetConfig(target)
		if err != nil {
			return err
		}
		err = fn(cfg, commit)
		if err != nil {
			failover.RecordAttemptTrace(r.appTracing, chain, target, err)
		}
		return err
	})
}

// warmStages preloads the stages. A chain stage warms through its failover
// plan: a target that fails to load moves the stage to the next one instead
// of failing the session.
func (r *stageRouter) warmStages(ctx context.Context, ml *model.ModelLoader, appConfig *config.ApplicationConfig, stages []backend.PreloadStage) error {
	var (
		plain []backend.PreloadStage
		wg    sync.WaitGroup
		mu    sync.Mutex
		errs  []error
	)
	for _, s := range stages {
		if !r.isChainStage(s.Role) {
			plain = append(plain, s)
			continue
		}
		wg.Go(func() {
			err := r.stageCall(ctx, s.Role, s.Cfg, func(cfg *config.ModelConfig, _ func()) error {
				_, err := backend.PreloadStages(ctx, ml, appConfig, []backend.PreloadStage{{Role: s.Role, Cfg: cfg}})
				return err
			})
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		})
	}
	_, err := backend.PreloadStages(ctx, ml, appConfig, plain)
	wg.Wait()
	return errors.Join(append(errs, err)...)
}

// startModelFailoverEvents starts failover events for m when it has chain
// stages. The returned func stops them and is never nil.
func startModelFailoverEvents(t Transport, m Model) func() {
	sr, ok := m.(stageRouted)
	if !ok {
		return func() {}
	}
	r := sr.router()
	if !r.hasChainStages() {
		return func() {}
	}
	return startFailoverEvents(t, r.failover, r.stageChains)
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
