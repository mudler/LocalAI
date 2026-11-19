package modelupdates

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// ErrNoOldVersion is returned by RemoveOld when the last upgrade of the model
// is not confirmed or kept no files.
var ErrNoOldVersion = errors.New("there is no old version to remove: the last upgrade of this model is not confirmed, or kept no files")

// ErrFilesKept is the error of a removal that kept some old files: a file
// another model uses, one outside the models directory, or one that could
// not be deleted.
var ErrFilesKept = errors.New("some old files were kept")

// RemovedOldNotifier tells that the old files of an upgrade were removed.
type RemovedOldNotifier interface {
	ModelRemovedOld(ctx context.Context, key, name string, files int) error
}

// RemoveOldResult is what RemoveOldVersion did: the old files it deleted, the
// ones it kept, and why it kept them. Paths are relative to the models
// directory.
type RemoveOldResult struct {
	Removed []string `json:"removed"`
	// Kept are the files still listed for a later removal.
	Kept []string `json:"kept,omitempty"`
	// Errors say why files were kept. Set only when something was removed:
	// a removal that deleted nothing returns its error.
	Errors []string `json:"errors,omitempty"`
}

// Partial reports that some files were removed and some were not.
func (r RemoveOldResult) Partial() bool { return len(r.Removed) > 0 && len(r.Errors) > 0 }

// RemoveOldVersion is RemoveOld with the outcome of a partial removal kept:
// when some files were deleted and others stayed (a file another model uses),
// it returns the result with Kept and Errors and no error. When nothing was
// deleted and something failed, it returns that error.
func (u *Upgrader) RemoveOldVersion(ctx context.Context, name string) (RemoveOldResult, error) {
	res, err := u.removeOld(ctx, name)
	if err != nil && len(res.Removed) == 0 {
		return res, err
	}
	if res.Removed == nil {
		res.Removed = []string{}
	}
	return res, nil
}

// RemoveOld deletes the files a confirmed upgrade kept (the old version). It
// refuses while an upgrade of the model runs (its load check included) and
// when no confirmed upgrade kept files. A kept file that another model uses
// now is not deleted: it stays listed, and the error names it. It returns the
// removed files.
func (u *Upgrader) RemoveOld(ctx context.Context, name string) ([]string, error) {
	res, err := u.removeOld(ctx, name)
	return res.Removed, err
}

func (u *Upgrader) removeOld(ctx context.Context, name string) (RemoveOldResult, error) {
	if err := utils.VerifyPath(name+".yaml", u.modelsPath()); err != nil {
		return RemoveOldResult{}, ErrNotFound
	}
	// The model lock an upgrade holds: in a cluster an upgrade on another
	// frontend cannot start over the files this removes, nor this rewrite
	// its gallery file.
	unlock, err := u.acquire(ctx, name)
	if err != nil {
		return RemoveOldResult{}, err
	}
	defer unlock()
	// The gallery file is read, changed and written as one step, so a config
	// edit cannot record its hash in between and lose it.
	unlockSidecar := sync.OnceFunc(gallery.LockSidecar(name))
	defer unlockSidecar()
	sc, err := u.readUpgradable(name)
	if err != nil {
		return RemoveOldResult{}, err
	}
	j := sc.Upgrade
	if j == nil || !j.Terminal() {
		if j != nil {
			return RemoveOldResult{}, ErrBusy
		}
		return RemoveOldResult{}, ErrNoOldVersion
	}
	if j.Phase != gallery.PhaseConfirmed || len(j.RetainedFiles) == 0 {
		return RemoveOldResult{}, ErrNoOldVersion
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
	unlockSidecar()
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
	res := RemoveOldResult{Removed: removed, Kept: kept}
	for _, e := range errs {
		res.Errors = append(res.Errors, u.relText(e.Error()))
	}
	if len(errs) == 0 {
		return res, nil
	}
	return res, u.relErr(fmt.Errorf("%w: %w", ErrFilesKept, errors.Join(errs...)))
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
	defer gallery.LockSidecar(name)()
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

// readGalleryFile reads the gallery file of an installed model: the model
// config must exist. A model without a gallery file gives gallery.ErrNoSidecar
// as it is, because Adopt wants exactly that model.
func (u *Upgrader) readGalleryFile(name string) (*gallery.ModelConfig, error) {
	if err := utils.VerifyPath(name+".yaml", u.modelsPath()); err != nil {
		return nil, ErrNotFound
	}
	if !u.fs().Exists(u.path(name + ".yaml")) {
		return nil, ErrNotFound
	}
	return gallery.ReadSidecar(u.modelsPath(), name)
}

// readUpgradable reads the gallery file of an installed model with a
// recorded origin.
func (u *Upgrader) readUpgradable(name string) (*gallery.ModelConfig, error) {
	sc, err := u.readGalleryFile(name)
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

// preconditions reads the gallery file of a model an upgrade (or a retry)
// would change and refuses what cannot run: no recorded origin, an upgrade
// of it that runs, a hold, or a retry without a failed upgrade. Upgrade
// calls it under the model lock; Validate calls it to refuse early.
func (u *Upgrader) preconditions(name string, retry bool) (*gallery.ModelConfig, error) {
	sc, err := u.readUpgradable(name)
	if err != nil {
		return nil, err
	}
	j := sc.Upgrade
	if j != nil && !j.Terminal() {
		return nil, ErrBusy
	}
	// Read under the model lock, which SetHold takes too: a hold set on any
	// frontend before this point stops the upgrade, and none can be set
	// until it ends.
	if sc.Hold {
		return nil, ErrHeld
	}
	if retry && (j == nil || j.Kind != gallery.JournalUpgrade || (j.Phase != gallery.PhaseFailed && j.Phase != gallery.PhaseRolledBack)) {
		return nil, ErrNothingFailed
	}
	return sc, nil
}
