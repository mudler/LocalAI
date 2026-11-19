package modelupdates

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	sidecars, _ := gallery.SidecarNames(u.modelsPath())
	for _, name := range sidecars {
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
		u.removeStaged(r, false)
		u.discardOnWorkers(ctx, r, false)
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
	restoreErr, advanceErr := u.finishRollback(ctx, r, cause, "the old version was put back")
	return errors.Join(restoreErr, advanceErr)
}

// RecoverAbandoned ends the installs and upgrades that a frontend left behind
// when it stopped, while the other frontends keep running. It needs Lock. A
// journal is abandoned when its model lock is free (the frontend that ran it
// held the lock until its end, and the database frees the lock of a dead
// session) and the journal has not been touched for quiet: a running upgrade
// touches its journal every Heartbeat, so a frontend that only lost its
// database session is not taken for a dead one. It runs under the lock, so
// it never touches a change that a live frontend runs.
//
// An install is finished or undone as at startup; an upgrade as Recover
// does. After each, Runtime (when set) reads the config again. It returns the
// models it recovered.
func (u *Upgrader) RecoverAbandoned(ctx context.Context, quiet time.Duration) ([]string, error) {
	if u.Lock == nil {
		return nil, nil
	}
	var names []string
	var errs []error
	sidecars, _ := gallery.SidecarNames(u.modelsPath())
	for _, name := range sidecars {
		sc, err := gallery.ReadSidecar(u.modelsPath(), name)
		if err != nil || sc.Upgrade == nil || sc.Upgrade.Terminal() || !sc.Upgrade.Stale(u.now(), quiet) {
			continue
		}
		ok, err := u.recoverAbandoned(ctx, name, quiet)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
		if ok {
			names = append(names, name)
		}
	}
	return names, errors.Join(errs...)
}

func (u *Upgrader) recoverAbandoned(ctx context.Context, name string, quiet time.Duration) (bool, error) {
	unlock, err := u.acquire(ctx, name)
	if errors.Is(err, ErrBusy) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer unlock()
	// Read again under the lock: the journal may have ended meanwhile.
	sc, err := gallery.ReadSidecar(u.modelsPath(), name)
	if err != nil || sc.Upgrade == nil || sc.Upgrade.Terminal() || !sc.Upgrade.Stale(u.now(), quiet) {
		return false, nil
	}
	switch sc.Upgrade.Kind {
	case gallery.JournalInstall:
		if _, err := gallery.RecoverInstallJournal(u.modelsPath(), name); err != nil {
			return false, err
		}
		if u.Runtime != nil {
			if err := u.Runtime.Reload(ctx, name); err != nil {
				xlog.Warn("Could not read the config of a recovered install", "model", name, "error", err)
			}
		}
	case gallery.JournalUpgrade:
		if err := u.recoverOne(ctx, name, sc); err != nil {
			return false, err
		}
	default:
		return false, nil
	}
	u.changed(name)
	return true, nil
}
