package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/modeladmin"
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
	BlockLoads(modelID string)
	UnblockLoads(modelID string)
}

// revisionPublisher publishes the config revision of a model to the node
// registry (modeladmin.ModelRevisionLifecycle). Every replica of another
// revision is stopped, and a request or the reconciler can load the model only
// with the published revision.
type revisionPublisher interface {
	ApplyConfigRevisions(ctx context.Context, transitions []modeladmin.ModelRevisionTransition) (int, error)
}

// upgradeBlockRevision is the revision a model has in the node registry while
// an upgrade switches its files. No config has it, so publishing it stops every
// replica, and every frontend and the reconciler refuse to load the model
// until the upgrade publishes the revision of the config on disk again.
func upgradeBlockRevision(name string) string { return "model-upgrade:" + name }

const clusterRegistryTimeout = 10 * time.Second

// clusterRuntime is the stop-and-start runtime of a cluster. The model runs on
// workers, which the frontend's model loader does not list, so "loaded" is
// read from the node registry. Unload stops every replica on every node (the
// loader's shutdown reaches them through the remote unloader) and checks that
// none is left. BlockLoads holds the model in the registry under a revision no
// config has, so no request and no reconciler pass can start the old version
// while the files change; UnblockLoads publishes the revision of the new
// config, and Load routes one load of it to a worker: that is the load check.
// The revision of a change of the weights alone does not move, so the block,
// not a revision change, is what keeps the old version from coming back.
type clusterRuntime struct {
	*modelRuntime
	replicas clusterReplicas
	loader   localModels
	// revisions and revisionOf hold the model in the registry while its files
	// change (BlockLoads). Nil skips it.
	revisions  revisionPublisher
	revisionOf func(name string) (string, error)
	// storedRevision reads the revision the registry holds for a model, or
	// modeladmin.ErrNoStoredRevision. Nil turns RepublishBlocked off.
	storedRevision func(ctx context.Context, name string) (string, error)
}

func newClusterRuntime(base *modelRuntime, replicas clusterReplicas, loader localModels) *clusterRuntime {
	return &clusterRuntime{modelRuntime: base, replicas: replicas, loader: loader}
}

// BlockLoads refuses the loads of the model on this frontend and, through the
// registry, on every frontend and in the reconciler, and stops every replica.
func (r *clusterRuntime) BlockLoads(ctx context.Context, name string) error {
	r.loader.BlockLoads(name)
	if r.revisions == nil {
		return nil
	}
	if _, err := r.revisions.ApplyConfigRevisions(ctx, []modeladmin.ModelRevisionTransition{{ModelName: name, ConfigRevision: upgradeBlockRevision(name)}}); err != nil {
		r.loader.UnblockLoads(name)
		return err
	}
	return nil
}

// UnblockLoads publishes the revision of the config on disk again, which also
// stops any replica of another revision, and lets this frontend load the
// model.
func (r *clusterRuntime) UnblockLoads(ctx context.Context, name string) error {
	defer r.loader.UnblockLoads(name)
	if r.revisions == nil || r.revisionOf == nil {
		return nil
	}
	return r.publishDiskRevision(ctx, name)
}

// RepublishBlocked publishes the revision of the config on disk for every
// model the registry still holds under its upgrade block revision while no
// upgrade of it runs. An upgrade that ended without publishing (its frontend
// stopped, or the publish failed) leaves the block in the registry, and
// nothing else would lift it: every request for the model is refused. A model
// is no longer upgrading when its lock is free; a nil lock (no cluster lock)
// skips the check. It returns the models it republished.
func (r *clusterRuntime) RepublishBlocked(ctx context.Context, names []string, lock gallery.ModelLock) []string {
	if r.revisions == nil || r.revisionOf == nil || r.storedRevision == nil {
		return nil
	}
	var done []string
	for _, name := range names {
		stored, err := r.storedRevision(ctx, name)
		if err != nil || stored != upgradeBlockRevision(name) {
			continue
		}
		release := func() {}
		if lock != nil {
			rel, err := lock(ctx, name)
			if err != nil {
				// Locked: an upgrade or an install of this model runs.
				continue
			}
			release = rel
		}
		if err := r.publishDiskRevision(ctx, name); err != nil {
			xlog.Warn("Could not lift the upgrade block of a model in the registry", "model", name, "error", err)
		} else {
			done = append(done, name)
		}
		release()
	}
	return done
}

// publishDiskRevision publishes the revision of the config on disk for a model.
func (r *clusterRuntime) publishDiskRevision(ctx context.Context, name string) error {
	revision, err := r.revisionOf(name)
	if err != nil {
		return fmt.Errorf("read the config revision: %w", err)
	}
	_, err = r.revisions.ApplyConfigRevisions(ctx, []modeladmin.ModelRevisionTransition{{ModelName: name, ConfigRevision: revision}})
	return err
}

func (r *clusterRuntime) IsLoaded(ctx context.Context, name string) bool {
	if isLoadedIn(r.loader.ListLoadedModels(), name) {
		return true
	}
	rctx, cancel := context.WithTimeout(ctx, clusterRegistryTimeout)
	defer cancel()
	found, err := r.replicas.FindNodesWithModel(rctx, name)
	if err != nil {
		// A model that runs on a worker is not seen here; the block that
		// precedes the stop of an upgrade already stops every replica in the
		// registry, and Unload checks that none is left.
		xlog.Warn("Could not read where a model runs; treating it as not loaded", "model", name, "error", err)
		return false
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
