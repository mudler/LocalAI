package modelupdates

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// ErrNoOldVersion is returned by RemoveOld when the last upgrade of the model
// is not confirmed or kept no files.
var ErrNoOldVersion = errors.New("there is no old version to remove: the last upgrade of this model is not confirmed, or kept no files")

// RemovedOldNotifier tells that the old files of an upgrade were removed.
type RemovedOldNotifier interface {
	ModelRemovedOld(ctx context.Context, key, name string, files int) error
}

// RemoveOld deletes the files a confirmed upgrade kept (the old version). It
// refuses while an upgrade of the model runs (its load check included) and
// when no confirmed upgrade kept files. A kept file that another model uses
// now is not deleted: it stays listed, and the error names it. It returns the
// removed files.
func (u *Upgrader) RemoveOld(ctx context.Context, name string) ([]string, error) {
	if err := utils.VerifyPath(name+".yaml", u.modelsPath()); err != nil {
		return nil, ErrNotFound
	}
	// The model lock an upgrade holds: in a cluster an upgrade on another
	// frontend cannot start over the files this removes, nor this rewrite
	// its gallery file.
	unlock, err := u.acquire(ctx, name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	sc, err := u.readUpgradable(name)
	if err != nil {
		return nil, err
	}
	j := sc.Upgrade
	if j == nil || !j.Terminal() {
		if j != nil {
			return nil, ErrBusy
		}
		return nil, ErrNoOldVersion
	}
	if j.Phase != gallery.PhaseConfirmed || len(j.RetainedFiles) == 0 {
		return nil, ErrNoOldVersion
	}
	inUse := gallery.FilesInUse(u.SystemState, name)
	var removed, kept []string
	var errs []error
	for _, rel := range j.RetainedFiles {
		if err := utils.VerifyPath(rel, u.modelsPath()); err != nil {
			errs = append(errs, fmt.Errorf("%s is outside the models directory", rel))
			kept = append(kept, rel)
			continue
		}
		if users := inUse[rel]; len(users) > 0 {
			errs = append(errs, fmt.Errorf("%s is used by %s", rel, strings.Join(users, ", ")))
			kept = append(kept, rel)
			continue
		}
		if err := u.fs().Remove(u.path(rel)); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", rel, err))
			kept = append(kept, rel)
			continue
		}
		removed = append(removed, rel)
	}
	j.RetainedFiles = kept
	j.UpdatedAt = u.now()
	if err := gallery.WriteSidecar(u.modelsPath(), name, sc); err != nil {
		errs = append(errs, err)
	}
	if len(removed) > 0 {
		if n, ok := u.Notifier.(RemovedOldNotifier); ok {
			key := j.OpID
			if key == "" {
				key = name + "." + digest8(j.ToEntryDigest)
			}
			if err := n.ModelRemovedOld(ctx, key, name, len(removed)); err != nil {
				xlog.Warn("Could not write the notification of a removed old version", "model", name, "error", err)
			}
		}
	}
	return removed, u.relErr(errors.Join(errs...))
}

// SetHold sets or clears the owner's hold of a model: a held model is
// reported as held and offered no update. It writes the gallery file only:
// the model config, its revision and the loaded model do not change, and a
// reinstall keeps the flag. It refuses while an upgrade of the model runs,
// which owns the gallery file until it ends. It takes the model lock an
// upgrade holds, so in a cluster a hold set on one frontend is never written
// over by an upgrade on another.
func (u *Upgrader) SetHold(ctx context.Context, name string, hold bool) error {
	if err := utils.VerifyPath(name+".yaml", u.modelsPath()); err != nil {
		return ErrNotFound
	}
	unlock, err := u.acquire(ctx, name)
	if err != nil {
		return err
	}
	defer unlock()
	sc, err := u.readUpgradable(name)
	if err != nil {
		return err
	}
	if sc.Upgrade != nil && !sc.Upgrade.Terminal() {
		return ErrBusy
	}
	if sc.Hold == hold {
		return nil
	}
	sc.Hold = hold
	if err := gallery.WriteSidecar(u.modelsPath(), name, sc); err != nil {
		return err
	}
	u.changed(name)
	return nil
}

// changed tells Changed that what the model needs from a person may differ.
func (u *Upgrader) changed(name string) {
	if u.Changed != nil {
		u.Changed(name)
	}
}

// readUpgradable reads the gallery file of an installed model with a
// recorded origin.
func (u *Upgrader) readUpgradable(name string) (*gallery.ModelConfig, error) {
	if !u.fs().Exists(u.path(name + ".yaml")) {
		return nil, ErrNotFound
	}
	sc, err := gallery.ReadSidecar(u.modelsPath(), name)
	if errors.Is(err, gallery.ErrNoSidecar) {
		return nil, ErrNotUpgradable
	}
	if err != nil {
		return nil, err
	}
	if !sc.IsV2() || sc.Origin == nil {
		return nil, ErrNotUpgradable
	}
	return sc, nil
}
