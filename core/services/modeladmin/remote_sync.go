package modeladmin

import (
	"context"
	"fmt"
	"sort"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
)

// ApplyRemoteChange refreshes this replica's in-memory model state from a peer
// replica's model-config change broadcast (messaging.CacheInvalidateEvent on
// SubjectCacheInvalidateModels). It is the subscriber-side counterpart to
// GalleryService.BroadcastModelsChanged.
//
// The event is only a wake-up signal. Its operation and revision may be stale
// or reordered, so named changes are always reconciled against the current
// shared filesystem state.
//
// Revision-aware events apply the same idempotent lifecycle transition as the
// originating frontend. modelsPath and opts are forwarded to
// LoadModelConfigsFromPath.
func ApplyRemoteChange(ctx context.Context, cl *config.ModelConfigLoader, modelsPath string, evt messaging.CacheInvalidateEvent, lifecycle ModelRevisionLifecycle, opts ...config.ConfigLoaderOption) error {
	return cl.WithModelConfigMutation(func() error {
		return applyRemoteChange(ctx, cl, modelsPath, evt, lifecycle, opts...)
	})
}

func applyRemoteChange(ctx context.Context, cl *config.ModelConfigLoader, modelsPath string, evt messaging.CacheInvalidateEvent, lifecycle ModelRevisionLifecycle, opts ...config.ConfigLoaderOption) error {
	authoritative := config.NewModelConfigLoader(modelsPath, cl.ReloadDiagnosticsOption())
	if err := authoritative.LoadModelConfigsFromPathStrict(modelsPath, opts...); err != nil {
		return err
	}
	currentConfigs := cl.GetAllModelsConfigs()
	current := make(map[string]config.ModelConfig, len(currentConfigs))
	outside := map[string]struct{}{}
	for _, cfg := range currentConfigs {
		// A config read from outside the models directory (--config-file) is
		// invisible to the snapshot. Treating it as removed would drop it and
		// publish a deletion revision for a model that still exists.
		if cfg.DefinedOutside(modelsPath) {
			outside[cfg.Name] = struct{}{}
			continue
		}
		current[cfg.Name] = cfg
	}
	snapshotConfigs := authoritative.GetAllModelsConfigs()
	snapshot := configsByName(snapshotConfigs)
	for name := range outside {
		delete(snapshot, name)
	}
	named := evt.Element
	if _, isOutside := outside[named]; isOutside {
		named = ""
	}
	changed, err := changedConfigNames(current, snapshot, named)
	if err != nil {
		return err
	}

	if lifecycle != nil {
		transitions := make([]ModelRevisionTransition, 0, len(changed))
		for _, name := range changed {
			cfg, exists := snapshot[name]
			revision := DeletedModelConfigRevision(name)
			disabled := true
			if exists {
				var err error
				revision, err = authoritative.RevisionForPath(name, modelsPath, opts...)
				if err != nil {
					return fmt.Errorf("resolve authoritative model config revision for %q: %w", name, err)
				}
				disabled = cfg.IsDisabled()
			}
			transitions = append(transitions, ModelRevisionTransition{ModelName: name, ConfigRevision: revision, Disabled: disabled})
		}
		if len(transitions) > 0 {
			if _, err := lifecycle.ApplyConfigRevisions(ctx, transitions); err != nil {
				return err
			}
		}
	}
	cl.ReplaceModelConfigs(config.MergeDirectorySnapshot(currentConfigs, snapshotConfigs, modelsPath))
	return nil
}

func configsByName(configs []config.ModelConfig) map[string]config.ModelConfig {
	result := make(map[string]config.ModelConfig, len(configs))
	for _, cfg := range configs {
		result[cfg.Name] = cfg
	}
	return result
}

func changedConfigNames(current, snapshot map[string]config.ModelConfig, named string) ([]string, error) {
	changed := map[string]struct{}{}
	for name, cfg := range snapshot {
		previous, exists := current[name]
		if !exists {
			changed[name] = struct{}{}
			continue
		}
		// Both sides come from a loader, so both carry the revision stamped
		// when their file was parsed. Comparing the stamps compares the files.
		if previous.PersistedConfigRevision() != cfg.PersistedConfigRevision() {
			changed[name] = struct{}{}
		}
	}
	for name := range current {
		if _, exists := snapshot[name]; !exists {
			changed[name] = struct{}{}
		}
	}
	if named != "" {
		changed[named] = struct{}{}
	}
	names := make([]string, 0, len(changed))
	for name := range changed {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// DeletedModelConfigRevision is a stable tombstone generation for an absent
// model. It lets every frontend derive the same authoritative state regardless
// of which reordered cache-invalidation event woke it up.
func DeletedModelConfigRevision(modelName string) string {
	return config.DeletedModelConfigRevision(modelName)
}
