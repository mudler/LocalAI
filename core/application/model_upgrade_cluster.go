package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// clusterReplicas is the part of the node registry the cluster runtime reads.
type clusterReplicas interface {
	FindNodesWithModel(ctx context.Context, modelName string) ([]nodes.BackendNode, error)
}

// localModels is the part of the model loader the cluster runtime uses.
type localModels interface {
	ShutdownModel(modelName string) error
	ListLoadedModels() []*model.Model
}

const clusterRegistryTimeout = 10 * time.Second

// clusterRuntime is the stop-and-start runtime of a cluster. The model runs on
// workers, which the frontend's model loader does not list, so "loaded" is
// read from the node registry. Unload stops every replica on every node (the
// loader's shutdown reaches them through the remote unloader) and checks that
// none is left. Reload advances the config revision, which also stops a
// replica the reconciler started in the meantime, and Load routes one load of
// the new revision to a worker: that is the load check.
type clusterRuntime struct {
	*modelRuntime
	replicas clusterReplicas
	loader   localModels
}

func newClusterRuntime(base *modelRuntime, replicas clusterReplicas, loader localModels) *clusterRuntime {
	return &clusterRuntime{modelRuntime: base, replicas: replicas, loader: loader}
}

func (r *clusterRuntime) IsLoaded(name string) bool {
	for _, m := range r.loader.ListLoadedModels() {
		if m.ID == name {
			return true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), clusterRegistryTimeout)
	defer cancel()
	found, err := r.replicas.FindNodesWithModel(ctx, name)
	if err != nil {
		// Not knowing is not "not loaded": stopping a model that was not
		// running costs nothing, leaving one running on the old files does.
		xlog.Warn("Could not read where a model runs; treating it as loaded", "model", name, "error", err)
		return true
	}
	return len(found) > 0
}

func (r *clusterRuntime) Unload(ctx context.Context, name string) error {
	if err := r.loader.ShutdownModel(name); err != nil && !errors.Is(err, model.ErrModelNotFound) {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, clusterRegistryTimeout)
	defer cancel()
	left, err := r.replicas.FindNodesWithModel(rctx, name)
	if err != nil {
		return fmt.Errorf("check that every replica stopped: %w", err)
	}
	if len(left) > 0 {
		names := make([]string, 0, len(left))
		for _, n := range left {
			names = append(names, n.Name)
		}
		return fmt.Errorf("replicas still run on %v", names)
	}
	return nil
}

// clusterSchedulingRules is the part of the registry that holds the upgrade
// strategy of a model.
type clusterSchedulingRules interface {
	GetGoverningScheduling(ctx context.Context, modelName string) (*nodes.ModelSchedulingConfig, error)
}

// clusterStrategyNote reads the strategy the model's scheduling rule asks
// for. Canary is stored but not run yet: the upgrade stops and starts, and
// the note says so.
func clusterStrategyNote(rules clusterSchedulingRules) func(context.Context, string) string {
	return func(ctx context.Context, name string) string {
		rule, err := rules.GetGoverningScheduling(ctx, name)
		if err != nil {
			xlog.Warn("Could not read the upgrade strategy of a model; stopping and starting it", "model", name, "error", err)
			return ""
		}
		if rule != nil && rule.EffectiveUpgradeStrategy() == nodes.UpgradeStrategyCanary {
			return modelupdates.CanaryNotAvailable
		}
		return ""
	}
}

// upgradeWorkers adapts nodes.UpgradeFiles to the Workers seam of the
// upgrader.
type upgradeWorkers struct{ files *nodes.UpgradeFiles }

func prefetchFiles(files []modelupdates.WorkerFile) []workerctl.ModelPrefetchFile {
	out := make([]workerctl.ModelPrefetchFile, len(files))
	for i, f := range files {
		out[i] = workerctl.ModelPrefetchFile{Filename: f.Path, URI: f.URI, SHA256: f.SHA256}
	}
	return out
}

func (w upgradeWorkers) Stage(ctx context.Context, name string, files []modelupdates.WorkerFile) {
	if failed := w.files.Stage(ctx, name, prefetchFiles(files)); len(failed) > 0 {
		xlog.Info("Some workers did not download the upgrade's files; they get them from the frontend at their next load", "model", name, "nodes", len(failed))
	}
}

func (w upgradeWorkers) Commit(ctx context.Context, name string, files []modelupdates.WorkerFile) error {
	return w.files.Commit(ctx, name, prefetchFiles(files))
}

func (w upgradeWorkers) Discard(ctx context.Context, name string, files []modelupdates.WorkerFile, evict bool) {
	w.files.Discard(ctx, name, prefetchFiles(files), evict)
}
