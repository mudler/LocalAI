package modeladmin

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/nodes"
)

type revisionRegistry interface {
	AdvanceModelConfigRevisions(ctx context.Context, transitions []nodes.ModelConfigRevisionTransition) ([]nodes.NodeModel, error)
}

type revisionCleanup interface {
	Cleanup(ctx context.Context, replicas []nodes.NodeModel, force bool) int
}

type localModelShutdown interface {
	ShutdownModel(modelName string) error
}

type LocalModelRevisionLifecycle struct{ loader localModelShutdown }

func NewLocalModelRevisionLifecycle(loader localModelShutdown) *LocalModelRevisionLifecycle {
	if loader == nil {
		return nil
	}
	return &LocalModelRevisionLifecycle{loader: loader}
}

func (s *LocalModelRevisionLifecycle) ApplyConfigRevisions(_ context.Context, transitions []ModelRevisionTransition) (int, error) {
	pending := 0
	seen := make(map[string]struct{}, len(transitions))
	for _, transition := range transitions {
		if _, exists := seen[transition.ModelName]; exists {
			continue
		}
		seen[transition.ModelName] = struct{}{}
		if err := s.loader.ShutdownModel(transition.ModelName); err != nil {
			pending++
		}
	}
	return pending, nil
}

// DistributedModelRevisionLifecycle makes the registry transition authoritative
// before any worker network call. Failed exact stops remain durable unloading
// rows and are retried by ModelCleanupService.Run.
type DistributedModelRevisionLifecycle struct {
	registry revisionRegistry
	cleanup  revisionCleanup
}

func NewDistributedModelRevisionLifecycle(registry revisionRegistry, cleanup revisionCleanup) *DistributedModelRevisionLifecycle {
	if registry == nil || cleanup == nil {
		return nil
	}
	return &DistributedModelRevisionLifecycle{registry: registry, cleanup: cleanup}
}

func (s *DistributedModelRevisionLifecycle) ApplyConfigRevisions(ctx context.Context, transitions []ModelRevisionTransition) (int, error) {
	registryTransitions := make([]nodes.ModelConfigRevisionTransition, 0, len(transitions))
	for _, transition := range transitions {
		registryTransitions = append(registryTransitions, nodes.ModelConfigRevisionTransition{
			ModelName: transition.ModelName, ConfigRevision: transition.ConfigRevision,
		})
	}
	quarantined, err := s.registry.AdvanceModelConfigRevisions(ctx, registryTransitions)
	if err != nil {
		return 0, fmt.Errorf("advance model config revisions: %w", err)
	}
	return s.cleanup.Cleanup(ctx, quarantined, false), nil
}
