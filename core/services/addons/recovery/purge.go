// SPDX-License-Identifier: MIT
package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// PurgeScope names what a purge removes. It is explicit: there is no default
// and no "everything" shortcut that the caller did not spell out.
type PurgeScope struct {
	// Data is the add-on's data directory and the owner's records of its
	// resources, grants and conversation selections.
	Data bool `json:"data"`
	// Backups are the backups of the add-on.
	Backups bool `json:"backups"`
	// RecoveryCopies are the data copies that restores and failed upgrades
	// set aside.
	RecoveryCopies bool `json:"recoveryCopies"`
}

// PurgeReceipt is the result of a purge.
type PurgeReceipt struct {
	AddonID   string     `json:"addonId"`
	Scope     PurgeScope `json:"scope"`
	At        time.Time  `json:"at"`
	Resources int        `json:"resources"`
	Warnings  []string   `json:"warnings"`
}

// Purge removes retained data of an add-on that is uninstalled or disabled.
// It is not the deletion of a database and not an uninstall: those never
// remove retained data. It is journaled, so a restart finishes it, and it
// touches no other add-on. The confirmation is the add-on's ID, typed.
func (r *Recovery) Purge(ctx context.Context, actor Actor, addonID string, scope PurgeScope, confirm string) (PurgeReceipt, error) {
	return r.PurgeOp(ctx, actor, "", addonID, scope, confirm)
}

// PurgeOp is Purge under an operation ID; a repeat returns the recorded
// receipt.
func (r *Recovery) PurgeOp(ctx context.Context, actor Actor, reqOp, addonID string, scope PurgeScope, confirm string) (PurgeReceipt, error) {
	if err := r.admin(ctx, actor); err != nil {
		return PurgeReceipt{}, err
	}
	if err := validID(addonID); err != nil {
		return PurgeReceipt{}, err
	}
	if confirm != addonID {
		return PurgeReceipt{}, ErrConfirmation
	}
	if !scope.Data && !scope.Backups && !scope.RecoveryCopies {
		return PurgeReceipt{}, ErrInvalid
	}
	if r.cfg.Policy == nil {
		return PurgeReceipt{}, ErrUnavailable
	}
	unlock, err := r.lock(addonID)
	if err != nil {
		return PurgeReceipt{}, err
	}
	defer unlock()
	params := fmt.Sprintf("%t/%t/%t", scope.Data, scope.Backups, scope.RecoveryCopies)
	if raw, ok, rerr := r.replayed(addonID, reqOp, "purge", params); rerr != nil {
		return PurgeReceipt{}, rerr
	} else if ok {
		var old PurgeReceipt
		if json.Unmarshal(raw, &old) == nil {
			return old, nil
		}
	}
	info, err := r.cfg.Host.Info(ctx, addonID)
	if err != nil {
		return PurgeReceipt{}, err
	}
	if info.Running || (info.Installed && info.Enabled) {
		return PurgeReceipt{}, ErrAddonRunning
	}
	if _, busy := r.loadJournal(addonID); busy {
		return PurgeReceipt{}, ErrBusy
	}
	opID, err := r.newBackupID()
	if err != nil {
		return PurgeReceipt{}, err
	}
	// Nothing may start the add-on or use its resources while its data goes.
	hctx, cancel := context.WithTimeout(ctx, r.cfg.DrainTimeout)
	release, err := r.cfg.Policy.Hold(hctx, addonID)
	cancel()
	if err != nil {
		return PurgeReceipt{}, err
	}
	defer release()
	q, err := r.cfg.Host.Quiesce(ctx, addonID)
	if err != nil {
		return PurgeReceipt{}, err
	}
	defer q.Release()
	j := journalRecord{Kind: "purge", Phase: "purging", ID: opID, Op: reqOp, Scope: &scope, Actor: actor.UserID, Started: r.cfg.Now().UTC()}
	if err = r.saveJournal(addonID, j); err != nil {
		return PurgeReceipt{}, err
	}
	if err = r.hook("purge-journaled"); err != nil {
		return PurgeReceipt{}, err
	}
	n, err := r.runPurge(ctx, addonID, j)
	if err != nil {
		return PurgeReceipt{}, err
	}
	w := []string{}
	if scope.Data {
		w = append(w, "The data and every share of the add-on are gone and cannot be restored unless a backup is kept.")
	}
	if scope.Backups {
		w = append(w, "The backups were removed. No recovery copy is left.")
	}
	rc := PurgeReceipt{AddonID: addonID, Scope: scope, At: r.cfg.Now().UTC(), Resources: n, Warnings: w}
	r.remember(addonID, reqOp, "purge", params, rc)
	return rc, nil
}

// runPurge does the work of a journaled purge and can be repeated.
func (r *Recovery) runPurge(ctx context.Context, addonID string, j journalRecord) (int, error) {
	scope := PurgeScope{}
	if j.Scope != nil {
		scope = *j.Scope
	}
	trash := filepath.Join(r.stateDir(addonID), "purging-"+j.ID)
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return 0, err
	}
	move := func(src, name string) error {
		if _, err := os.Lstat(src); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		return os.Rename(src, filepath.Join(trash, name))
	}
	if scope.Data {
		if err := move(r.dataDir(addonID), "data"); err != nil {
			return 0, err
		}
	}
	if scope.Backups {
		if err := move(r.backupsDir(addonID), "backups"); err != nil {
			return 0, err
		}
	}
	if scope.RecoveryCopies {
		for _, p := range r.previousCopies(addonID) {
			if err := move(filepath.Join(r.stateDir(addonID), p), p); err != nil {
				return 0, err
			}
		}
	}
	n := 0
	if scope.Data {
		var err error
		if n, err = r.cfg.Policy.Forget(ctx, addonID); err != nil {
			return 0, err
		}
	}
	if err := removeAll(trash); err != nil {
		return 0, err
	}
	st, _ := r.loadState(addonID)
	if scope.Data {
		// The data the state described is gone.
		st.State, st.Reason, st.RecoveryBackup = StateReady, "", ""
		st.LastRestore = nil
	}
	if scope.Backups {
		st.Pinned, st.RecoveryBackup = "", ""
	}
	if st.State == StatePurging {
		st.State = StateReady
	}
	if err := r.saveState(addonID, st); err != nil {
		return 0, err
	}
	return n, r.clearJournal(addonID)
}

// Reconcile finishes or undoes what a crash left in the journals of the
// add-ons. It needs the policy port for a restore that stopped after its
// swap; without one that restore stays blocked and is finished by a later
// call. It never starts an add-on.
func (r *Recovery) Reconcile(ctx context.Context) error {
	entries, err := os.ReadDir(filepath.Join(r.cfg.Root, "recovery"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var first error
	for _, e := range entries {
		if !e.IsDir() || validID(e.Name()) != nil {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = r.reconcileOne(ctx, e.Name()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (r *Recovery) reconcileOne(ctx context.Context, addonID string) error {
	unlock, err := r.lock(addonID)
	if err != nil {
		return err
	}
	defer unlock()
	r.cleanTemps(addonID)
	j, ok := r.loadJournal(addonID)
	if !ok {
		return nil
	}
	switch j.Kind {
	case "upgrade":
		return r.reconcileUpgrade(ctx, addonID, j)
	case "restore":
		return r.reconcileRestore(ctx, addonID, j)
	case "purge":
		_, err := r.runPurgeTolerant(ctx, addonID, j)
		return err
	}
	_ = r.saveState(addonID, State{State: StateNeedsRecovery, Since: r.cfg.Now().UTC(), Reason: "the recovery journal of the add-on is unreadable"})
	return nil
}

func (r *Recovery) runPurgeTolerant(ctx context.Context, addonID string, j journalRecord) (int, error) {
	if r.cfg.Policy == nil && j.Scope != nil && j.Scope.Data {
		return 0, ErrUnavailable
	}
	return r.runPurge(ctx, addonID, j)
}

func (r *Recovery) reconcileUpgrade(ctx context.Context, addonID string, j journalRecord) error {
	info, err := r.cfg.Host.Info(ctx, addonID)
	if err != nil {
		return err
	}
	if info.Installed && info.Receipt.Digest == j.Digest && info.Generation == j.Number {
		// Published before the crash.
		r.finishUpgrade(addonID, j)
		return nil
	}
	switch j.Phase {
	case "migrating", "healthy", "publishing":
		prev := info.Generation
		if j.FormatChanged {
			_ = r.saveState(addonID, State{State: StateNeedsRecovery, Since: r.cfg.Now().UTC(), RecoveryBackup: j.Backup, Pinned: j.Backup,
				PreviousGeneration: prev, Reason: "the migration to a new data format was interrupted; the previous package cannot read the data the new one began to change"})
			return r.clearJournal(addonID)
		}
		if err = r.restoreFromBackup(ctx, addonID, j.Backup, j.ID); err != nil {
			_ = r.saveState(addonID, State{State: StateNeedsRecovery, Since: r.cfg.Now().UTC(), RecoveryBackup: j.Backup, Pinned: j.Backup,
				PreviousGeneration: prev, Reason: "the data could not be put back from the backup: " + err.Error()})
			return r.clearJournal(addonID)
		}
	}
	// Before the migration the data was never touched.
	_ = r.saveState(addonID, State{State: StateReady, Pinned: j.Backup})
	return r.clearJournal(addonID)
}

func (r *Recovery) reconcileRestore(ctx context.Context, addonID string, j journalRecord) error {
	prior := State{State: StateReady}
	if j.Prior != nil {
		prior = *j.Prior
	}
	undo := func() error {
		_ = removeAll(j.Staged)
		_ = removeAll(j.Staged + ".partial")
		if err := r.saveState(addonID, prior); err != nil {
			return err
		}
		return r.clearJournal(addonID)
	}
	_, stagedErr := os.Lstat(j.Staged)
	_, dataErr := os.Lstat(r.dataDir(addonID))
	_, prevErr := os.Lstat(j.Prev)
	switch j.Phase {
	case "staging":
		return undo()
	case "swap":
		// The data directory is renamed away before the staged copy is
		// renamed in, so "staged and data both exist, no recovery copy" is
		// the state before the first rename.
		if stagedErr == nil && dataErr == nil && prevErr != nil {
			return undo()
		}
		if err := r.swapIn(addonID, j.Staged, j.Prev); err != nil {
			_ = r.saveState(addonID, State{State: StateNeedsRecovery, Since: r.cfg.Now().UTC(), RecoveryBackup: j.Backup, Reason: "an interrupted restore could not be completed: " + err.Error()})
			return r.clearJournal(addonID)
		}
	}
	if r.cfg.Policy == nil {
		return nil // stays blocked until the owner has its policy
	}
	m, dir, _, err := r.readManifest(addonID, j.Backup)
	if err != nil {
		_ = r.saveState(addonID, State{State: StateNeedsRecovery, Since: r.cfg.Now().UTC(), Reason: "the backup of an interrupted restore is not readable"})
		return r.clearJournal(addonID)
	}
	_, err = r.finishRestore(ctx, addonID, dir, m, j)
	return err
}
