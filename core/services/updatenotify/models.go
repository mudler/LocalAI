package updatenotify

import (
	"context"
	"sort"

	"github.com/mudler/LocalAI/core/services/notify"
)

// Codes of the model update items. The UI words them from the params.
const (
	// CodeModelsUpdateAvailable is the digest of the gallery models that need
	// a person: an update, an edited update or a failed upgrade.
	CodeModelsUpdateAvailable = "models.updateAvailable"
	// CodeModelUpgradeDone and CodeModelUpgradeFailed tell the end of a model
	// upgrade. A failed upgrade was rolled back, or did not start changing
	// anything; the old version serves.
	CodeModelUpgradeDone   = "models.upgradeDone"
	CodeModelUpgradeFailed = "models.upgradeFailed"
	// CodeModelRemovedOld tells that the files an upgrade kept were removed.
	CodeModelRemovedOld = "models.removedOld"
)

// KeyModelsDigest is the key of the model digest row, one per admin.
const KeyModelsDigest = "updates.models"

// FamilyModels names gallery models in the params.
const FamilyModels = "models"

// ModelUpdate is one model that needs a person.
type ModelUpdate struct {
	Name string
	// State is update-available, edited or failed.
	State string
}

// ModelDigest sets the digest of the gallery models that need a person, or
// removes it when there are none. Models have no automatic update, so every
// model with an update is in it. The caller must be the runner of the model
// update check, and must not call it when the galleries did not answer: an
// empty list removes the digest.
func (e *Emitter) ModelDigest(ctx context.Context, items []ModelUpdate) error {
	n := notify.Notification{
		Key: KeyModelsDigest, Kind: notify.KindNeedsAttention, Status: notify.StatusOK, Source: notify.SourceSystem,
		Link: LinkReady, Code: CodeModelsUpdateAvailable,
	}
	if len(items) > 0 {
		names := make([]string, len(items))
		ids := make([]string, len(items))
		for i, it := range items {
			names[i] = it.Name
			ids[i] = "model:" + it.Name + ":" + it.State
		}
		sort.Strings(names)
		sort.Strings(ids)
		n.Ids = ids
		n.Title = plural(len(items), "model update available", "model updates available")
		n.Body = listWithMore(names, len(names))
		n.Params = map[string]any{"family": FamilyModels, "count": len(items), "names": first(names)}
	}
	return e.upsertAll(ctx, n, notify.ModeSet)
}

// ModelUpgrade is the end of one model upgrade.
type ModelUpgrade struct {
	// OpID is the operation of the upgrade; the item is keyed by it, so the
	// same operation is told once.
	OpID string
	Name string
	// Failed is true when the upgrade did not complete. RolledBack is true
	// when it had changed files and put the old version back.
	Failed, RolledBack bool
	From, To           string
	Err                string
}

// ModelUpgradeResult tells the administrators how a model upgrade ended. The
// row is keyed by the operation (the same key as Operation), and posted once.
func (e *Emitter) ModelUpgradeResult(ctx context.Context, r ModelUpgrade) error {
	if r.OpID == "" || r.Name == "" {
		return nil
	}
	n := notify.Notification{
		Key: keyOp + r.OpID, Source: notify.SourceSystem, Kind: notify.KindRunEnded, Status: notify.StatusOK,
		Link: LinkPage, Code: CodeModelUpgradeDone, Title: "Model " + r.Name + " upgraded",
		Params: map[string]any{"name": r.Name, "from": r.From, "to": r.To},
	}
	if r.Failed {
		n.Code, n.Kind, n.Status, n.Link = CodeModelUpgradeFailed, notify.KindNeedsAttention, notify.StatusFailed, LinkNeedsYou
		n.Title = "Model " + r.Name + ": the upgrade failed"
		n.Body = errorLine(r.Err)
		n.Params["rolledBack"] = r.RolledBack
		if n.Body != "" {
			n.Params["error"] = n.Body
		}
	}
	return e.upsertAll(ctx, n, notify.ModeOnce)
}

// ModelRemovedOld tells the administrators that the old files of a model
// upgrade were removed. key identifies the removal (the upgrade it ends).
func (e *Emitter) ModelRemovedOld(ctx context.Context, key, name string, files int) error {
	if key == "" || name == "" {
		return nil
	}
	n := notify.Notification{
		Key: keyOp + "removed-old." + key, Source: notify.SourceSystem, Kind: notify.KindRunEnded, Status: notify.StatusOK,
		Link: LinkPage, Code: CodeModelRemovedOld, Title: "Model " + name + ": the old version was removed",
		Params: map[string]any{"name": name, "files": files},
	}
	return e.upsertAll(ctx, n, notify.ModeOnce)
}
