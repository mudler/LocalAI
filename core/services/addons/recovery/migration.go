// SPDX-License-Identifier: MIT
package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/addons/packages"
)

// CheckCompatible decides whether a package may take over the data that the
// package of prev wrote. The trusted identity must be the same: the same
// add-on from the same source and channel with the same verification level,
// so an upgrade or a reinstall can never move retained data to a different
// publisher. The data format must not go back to an older one, because the
// package could not read what its predecessor wrote.
func CheckCompatible(prev, next packages.PackageReceipt) error {
	if prev.Manifest.ID == "" {
		return nil
	}
	if prev.Manifest.ID != next.Manifest.ID || prev.SourceID != next.SourceID || prev.Channel != next.Channel || prev.Verification != next.Verification {
		return ErrIdentityChanged
	}
	if next.Manifest.DataFormat.Max < prev.Manifest.DataFormat.Max {
		return ErrIncompatibleFormat
	}
	return nil
}

func formatChanged(prev, next packages.PackageReceipt) bool {
	return next.Manifest.DataFormat.Max != prev.Manifest.DataFormat.Max
}

// migrationState reads the status file the add-on writes while it migrates.
// The bool is false when it wrote none.
func (r *Recovery) migrationState(addonID string) (string, string, bool) {
	b, err := os.ReadFile(filepath.Join(r.dataDir(addonID), "migration.json"))
	if err != nil {
		return "", "", false
	}
	var st struct {
		State string `json:"state"`
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &st) != nil {
		return "unreadable", "", true
	}
	return st.State, st.Error, true
}

// hasData reports whether the add-on has retained data.
func (r *Recovery) HasData(addonID string) bool {
	entries, err := os.ReadDir(r.dataDir(addonID))
	return err == nil && len(entries) > 0
}

// Upgrade moves an add-on with data to the staged package. See UpgradeReceipt.
func (r *Recovery) Upgrade(ctx context.Context, addonID string, staged packages.StagedPackage) error {
	_, err := r.UpgradeReceipt(ctx, addonID, staged)
	return err
}

// UpgradeReceipt runs the upgrade of an add-on that has data and returns the
// receipt of the package it published.
//
// The order is fixed: block admission and invalidate proposals, drain and
// stop, take a verified backup, check the package against the trusted
// identity and data format, publish the package, migrate under exclusive
// ownership by starting the candidate while nothing else touches the data,
// check its health, and only then publish the generation. Every step is
// journaled before it runs, so a restart finds the operation at a known phase
// and applies one rule: a generation that was not published is undone.
//
// A failure before the migration leaves the data as it was. A failure of a
// migration that changes the data format leaves the add-on unavailable in
// the needs-recovery state, with the previous package and the verified
// backup retained: the previous package alone cannot read data the candidate
// began to change, and nothing here claims that it can.
func (r *Recovery) UpgradeReceipt(ctx context.Context, addonID string, staged Candidate) (receipt packages.PackageReceipt, err error) {
	host, ok := r.cfg.Host.(UpgradeHost)
	if !ok {
		return packages.PackageReceipt{}, ErrInvalid
	}
	if err = validID(addonID); err != nil {
		return packages.PackageReceipt{}, err
	}
	unlock, err := r.lock(addonID)
	if err != nil {
		return packages.PackageReceipt{}, err
	}
	defer unlock()
	info, err := host.Info(ctx, addonID)
	if err != nil {
		return packages.PackageReceipt{}, err
	}
	if !info.Installed {
		return packages.PackageReceipt{}, ErrNotInstalled
	}
	if r.cfg.Policy == nil {
		return packages.PackageReceipt{}, ErrUnavailable
	}
	cand := staged.Receipt()
	// Refused before anything is stopped or written.
	if err = CheckCompatible(info.Receipt, cand); err != nil {
		return packages.PackageReceipt{}, err
	}
	if st, _ := r.loadState(addonID); st.State != StateReady {
		return packages.PackageReceipt{}, ErrNeedsRecovery
	}
	if _, busy := r.loadJournal(addonID); busy {
		return packages.PackageReceipt{}, ErrBusy
	}
	opID, err := r.newBackupID()
	if err != nil {
		return packages.PackageReceipt{}, err
	}
	number := info.Generation + 1
	changed := formatChanged(info.Receipt, cand)
	j := journalRecord{Kind: "upgrade", Phase: "begin", ID: opID, Digest: cand.Digest, Number: number, Started: r.cfg.Now().UTC(), FormatChanged: changed}
	if err = r.saveJournal(addonID, j); err != nil {
		return packages.PackageReceipt{}, err
	}
	if err = r.saveState(addonID, State{State: StateUpgrading, Since: j.Started, PreviousGeneration: info.Generation}); err != nil {
		_ = r.clearJournal(addonID)
		return packages.PackageReceipt{}, err
	}
	phase := func(p string) error {
		j.Phase = p
		return r.saveJournal(addonID, j)
	}
	var q Quiesced
	quiesced := false
	published := false
	defer func() {
		if err == nil {
			return
		}
		if errors.Is(err, errCrash) {
			return
		}
		dctx, stop := r.detached(ctx)
		defer stop()
		err = r.undoUpgrade(dctx, host, addonID, info, j, q, quiesced && !published, err)
	}()
	if err = r.hook("before-backup"); err != nil {
		return packages.PackageReceipt{}, err
	}
	hctx, cancel := context.WithTimeout(ctx, r.cfg.DrainTimeout)
	release, err := r.cfg.Policy.Hold(hctx, addonID)
	cancel()
	if err != nil {
		return packages.PackageReceipt{}, err
	}
	defer release()
	if q, err = host.Quiesce(ctx, addonID); err != nil {
		return packages.PackageReceipt{}, err
	}
	quiesced = true
	br, err := r.writeBackup(ctx, addonID, info, ReasonUpgrade)
	if err != nil {
		return packages.PackageReceipt{}, err
	}
	j.Backup = br.ID
	if err = phase("backed-up"); err != nil {
		return packages.PackageReceipt{}, err
	}
	if err = r.hook("after-backup"); err != nil {
		return packages.PackageReceipt{}, err
	}
	committed, err := host.Commit(ctx, staged)
	if err != nil {
		return packages.PackageReceipt{}, err
	}
	if err = phase("staged"); err != nil {
		return packages.PackageReceipt{}, err
	}
	if err = r.hook("after-stage"); err != nil {
		return packages.PackageReceipt{}, err
	}
	// From here the candidate may change the data.
	if err = phase("migrating"); err != nil {
		return packages.PackageReceipt{}, err
	}
	if err = r.hook("mid-migrate"); err != nil {
		return packages.PackageReceipt{}, err
	}
	_ = os.Remove(filepath.Join(r.dataDir(addonID), "migration.json"))
	if err = host.StartCandidate(ctx, addonID, committed, number); err != nil {
		return packages.PackageReceipt{}, fmt.Errorf("candidate did not become ready: %w", err)
	}
	if state, msg, found := r.migrationState(addonID); found && state != "complete" {
		return packages.PackageReceipt{}, fmt.Errorf("migration %s: %s", state, msg)
	}
	if err = phase("healthy"); err != nil {
		return packages.PackageReceipt{}, err
	}
	if err = r.hook("after-migrate"); err != nil {
		return packages.PackageReceipt{}, err
	}
	if !info.Enabled {
		// The add-on was stopped by choice: it ran only to migrate.
		if err = host.StopCandidate(ctx, addonID); err != nil {
			return packages.PackageReceipt{}, err
		}
	}
	if err = phase("publishing"); err != nil {
		return packages.PackageReceipt{}, err
	}
	if err = host.Publish(ctx, addonID, committed, number); err != nil {
		return packages.PackageReceipt{}, err
	}
	published = true
	// The generation is durable. What follows is bookkeeping that a restart
	// completes from the journal if it does not finish here.
	if herr := r.hook("after-publish"); herr != nil {
		return committed, herr
	}
	r.finishUpgrade(addonID, j)
	dctx, stop := r.detached(ctx)
	defer stop()
	if q != nil {
		_ = q.Resume(dctx)
	}
	return committed, nil
}

// finishUpgrade records the end of a published upgrade: the backup taken
// before it stays the recovery copy until a newer verified one exists.
func (r *Recovery) finishUpgrade(addonID string, j journalRecord) {
	_ = r.saveState(addonID, State{State: StateReady, Pinned: j.Backup})
	_ = r.clearJournal(addonID)
	_ = r.prune(addonID)
}

// undoUpgrade returns the add-on to a defined state after a failure that
// happened before the generation was published.
func (r *Recovery) undoUpgrade(ctx context.Context, host UpgradeHost, addonID string, info Info, j journalRecord, q Quiesced, quiesced bool, cause error) error {
	if j.Phase == "publishing" || j.Phase == "published" {
		// The publication may have been made durable even though its caller
		// saw an error. Only the journal of the owner can say; keep the
		// operation open for the restart reconciliation.
		if got, err := host.Info(ctx, addonID); err == nil && got.Receipt.Digest == j.Digest && got.Generation == j.Number {
			r.finishUpgrade(addonID, j)
			if q != nil {
				_ = q.Resume(ctx)
			}
			return cause
		}
	}
	if j.Phase == "healthy" || j.Phase == "publishing" || j.Phase == "migrating" || j.Phase == "staged" {
		_ = host.StopCandidate(ctx, addonID)
	}
	changed := j.Phase == "migrating" || j.Phase == "healthy" || j.Phase == "publishing"
	if changed && j.FormatChanged {
		_ = r.saveState(addonID, State{State: StateNeedsRecovery, Since: r.cfg.Now().UTC(), RecoveryBackup: j.Backup, Pinned: j.Backup,
			PreviousGeneration: info.Generation, Reason: "the migration to a new data format did not complete; the previous package cannot read the data the new one began to change"})
		_ = r.clearJournal(addonID)
		if q != nil && quiesced {
			q.Release()
		}
		return fmt.Errorf("%w: %v", ErrNeedsRecovery, cause)
	}
	if changed {
		// The format did not change, so the previous package can read the
		// data, but not what a half finished migration left: the verified
		// backup is put back before it starts.
		if err := r.restoreFromBackup(ctx, addonID, j.Backup, j.ID); err != nil {
			_ = r.saveState(addonID, State{State: StateNeedsRecovery, Since: r.cfg.Now().UTC(), RecoveryBackup: j.Backup, Pinned: j.Backup,
				PreviousGeneration: info.Generation, Reason: "the data could not be put back from the backup: " + err.Error()})
			_ = r.clearJournal(addonID)
			if q != nil && quiesced {
				q.Release()
			}
			return fmt.Errorf("%w: %v", ErrNeedsRecovery, errors.Join(cause, err))
		}
	}
	pinned := j.Backup
	_ = r.saveState(addonID, State{State: StateReady, Pinned: pinned})
	_ = r.clearJournal(addonID)
	if q != nil && quiesced {
		// Whatever was running before runs again, on the data it had.
		_ = q.Resume(ctx)
	}
	return cause
}
