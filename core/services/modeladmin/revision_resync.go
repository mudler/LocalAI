package modeladmin

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
)

// ErrNoStoredRevision reports that the controller holds no revision for a
// model, which is the normal state for one that has never been served.
var ErrNoStoredRevision = gorm.ErrRecordNotFound

// RevisionStore is the controller state this resync reads and corrects.
type RevisionStore interface {
	GetModelConfigRevision(ctx context.Context, modelName string) (string, error)
	ApplyConfigRevisions(ctx context.Context, transitions []ModelRevisionTransition) (int, error)
}

// RevisionReader is the read half, satisfied by the node registry.
type RevisionReader interface {
	GetModelConfigRevision(ctx context.Context, modelName string) (string, error)
}

type revisionStore struct {
	RevisionReader
	lifecycle ModelRevisionLifecycle
}

func (s revisionStore) ApplyConfigRevisions(ctx context.Context, t []ModelRevisionTransition) (int, error) {
	return s.lifecycle.ApplyConfigRevisions(ctx, t)
}

// NewRevisionStore pairs the registry that holds the stored revisions with the
// lifecycle that publishes new ones. Returns nil when either half is missing,
// which ResyncModelConfigRevisions treats as "nothing to reconcile".
func NewRevisionStore(reader RevisionReader, lifecycle ModelRevisionLifecycle) RevisionStore {
	if reader == nil || lifecycle == nil {
		return nil
	}
	return revisionStore{RevisionReader: reader, lifecycle: lifecycle}
}

// ResyncModelConfigRevisions makes the controller's stored revision for each
// model agree with what this build computes from the configuration on disk.
//
// The stored revision is what every inference request is checked against, but
// nothing ever re-derived it from the persisted configuration: it moved only on
// an edit, a gallery install, or a peer's change broadcast. Any other way for
// the two to diverge left the model permanently unroutable, because an
// inference request may only establish a revision, never replace one. A
// configuration edited while this frontend was down, or a change in what the
// revision is computed over, both landed there, and the only recovery was
// deleting the row by hand.
//
// Running this at startup makes that self-correcting. Only a model whose stored
// revision disagrees is republished, so replicas of models that did not drift
// keep serving: republishing is not free, it quarantines every replica loaded
// under the old revision.
//
// A model with no stored revision is left alone. It has never been served, and
// inventing controller state for it here would quarantine nothing and describe
// a model that may never be requested.
func ResyncModelConfigRevisions(ctx context.Context, loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, store RevisionStore) error {
	if loader == nil || store == nil || appConfig == nil {
		return nil
	}

	configs := loader.GetAllModelsConfigs()
	if len(configs) == 0 {
		// Reconciling nothing is indistinguishable from reconciling correctly,
		// which is how a caller that ran this before the configs were loaded
		// went unnoticed. Say so rather than report success.
		xlog.Warn("Skipping model config revision resync: no model configurations are loaded")
		return nil
	}

	var transitions []ModelRevisionTransition
	for _, cfg := range configs {
		// Resolve the revision the way an inference request does, through the
		// loader, rather than hashing the stored config directly. SetDefaults
		// is applied again on that path and is not idempotent for every model
		// (it re-runs the GGUF guess and hardware defaults), so hashing the
		// stored config yields a value no request will ever carry, and
		// publishing it would wedge the model this resync exists to unwedge.
		want, err := loader.RevisionFor(cfg.Name, appConfig)
		if err != nil {
			return err
		}

		stored, err := store.GetModelConfigRevision(ctx, cfg.Name)
		if errors.Is(err, ErrNoStoredRevision) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read stored config revision for %q: %w", cfg.Name, err)
		}
		if stored == want {
			continue
		}

		xlog.Warn("Stored model config revision disagrees with the configuration on disk, republishing",
			"model", cfg.Name, "stored", shortRevision(stored), "computed", shortRevision(want))
		transitions = append(transitions, ModelRevisionTransition{
			ModelName: cfg.Name, ConfigRevision: want, Disabled: cfg.IsDisabled(),
		})
	}

	if len(transitions) == 0 {
		return nil
	}
	if _, err := store.ApplyConfigRevisions(ctx, transitions); err != nil {
		return fmt.Errorf("republish model config revisions: %w", err)
	}
	xlog.Info("Republished model config revisions to match the configuration on disk", "models", len(transitions))
	return nil
}

// shortRevision trims a revision for log output; the leading bytes identify it
// well enough to tell two apart.
func shortRevision(revision string) string {
	if revision == "" {
		return "(none)"
	}
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}
