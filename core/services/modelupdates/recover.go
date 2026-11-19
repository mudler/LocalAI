package modelupdates

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/xlog"
)

// Recover ends the upgrades that a restart or a dead frontend interrupted.
// act decides which journals this process may touch (in a cluster, its own
// and stale ones). A journal that had not reached the unloading phase changed
// nothing a person sees: its staged files are removed and it ends failed.
// From unloading on, the new version never passed its load check, so the old
// version is put back and the journal ends rolled-back; the person can retry.
//
// At startup Runtime may be nil: nothing is loaded yet, and the configs are
// read after the recovery. It returns the names of the models it recovered.
func (u *Upgrader) Recover(ctx context.Context, act func(*gallery.Journal) bool) ([]string, error) {
	var names []string
	var errs []error
	for _, name := range sidecarNames(u.modelsPath()) {
		sc, err := gallery.ReadSidecar(u.modelsPath(), name)
		if err != nil || sc.Upgrade == nil || sc.Upgrade.Kind != gallery.JournalUpgrade || sc.Upgrade.Terminal() {
			continue
		}
		if act != nil && !act(sc.Upgrade) {
			continue
		}
		if !u.claim(name) {
			continue
		}
		err = u.recoverOne(ctx, name, sc)
		u.release(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		names = append(names, name)
	}
	return names, errors.Join(errs...)
}

func (u *Upgrader) recoverOne(ctx context.Context, name string, sc *gallery.ModelConfig) error {
	r := &upgradeRun{u: u, name: name, sc: sc}
	phase := sc.Upgrade.Phase
	cause := fmt.Errorf("the upgrade was interrupted in the %s phase (a restart or a stopped frontend)", phase)
	xlog.Warn("Ending an interrupted model upgrade", "model", name, "phase", phase)
	if !touchesFiles(phase) {
		u.removeStaged(r)
		return r.advance(EvInterrupted, func(j *gallery.Journal) {
			j.Error = cause.Error()
			j.Previous, j.To = nil, nil
		})
	}
	if phase != gallery.PhaseRollingBack {
		if err := r.advance(EvInterrupted, func(j *gallery.Journal) { j.Error = cause.Error() }); err != nil {
			return err
		}
	}
	if err := u.restore(ctx, r); err != nil {
		return errors.Join(err, r.advance(EvFailed, func(j *gallery.Journal) {
			j.Error = cause.Error() + "; the rollback did not complete: " + err.Error()
		}))
	}
	return r.advance(EvOK, func(j *gallery.Journal) {
		j.Error = cause.Error() + "; the old version was put back"
		j.Previous, j.To, j.Files = nil, nil, nil
	})
}
