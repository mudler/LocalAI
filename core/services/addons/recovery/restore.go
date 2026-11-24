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
	"sort"
	"strings"
	"time"
)

// RestoreReceipt is what a restore reports. The warnings are meant to be
// shown to the administrator, in the receipt and in the page.
type RestoreReceipt struct {
	AddonID       string    `json:"addonId"`
	BackupID      string    `json:"backupId"`
	BackupCreated time.Time `json:"backupCreated"`
	At            time.Time `json:"at"`
	Actor         string    `json:"actor"`
	// LostWrites counts the operations applied after the backup. Their
	// effects are gone from the data.
	LostWrites int `json:"lostWrites"`
	// PreviousCopy names the directory that holds the data that was replaced.
	// It stays until an administrator purges it.
	PreviousCopy string        `json:"previousCopy,omitempty"`
	Policy       RestoreReport `json:"policy"`
	Warnings     []string      `json:"warnings"`
}

// Validate checks the mapping against the account directory. A mapping is
// explicit: no empty key or value, no wildcard, no two sources for one
// target, and every target is a current account.
func (m OwnershipMapping) Validate(ctx context.Context, account func(context.Context, string) error) error {
	seen := map[string]bool{}
	for from, to := range m {
		if from == "" || to == "" || strings.ContainsAny(from, "*?") || strings.ContainsAny(to, "*?") || strings.TrimSpace(from) != from || strings.TrimSpace(to) != to || len(from) > 256 || len(to) > 256 {
			return ErrMappingInvalid
		}
		if seen[to] && false {
			return ErrMappingInvalid
		}
		seen[to] = true
		if account == nil {
			return ErrUnavailable
		}
		if err := account(ctx, to); err != nil {
			return fmt.Errorf("%w: %q is not a current account", ErrMappingInvalid, to)
		}
	}
	return nil
}

// find locates the add-on that holds a backup ID.
func (r *Recovery) find(backupID string) (string, error) {
	if !validBackupID(backupID) {
		return "", ErrBackupNotFound
	}
	entries, err := os.ReadDir(filepath.Join(r.cfg.Root, "backups"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrBackupNotFound
		}
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() || validID(e.Name()) != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(r.backupsDir(e.Name()), backupID)); err == nil && info.IsDir() {
			return e.Name(), nil
		}
	}
	return "", ErrBackupNotFound
}

// Restore puts a backup back. See RestoreReceipt for the result.
func (r *Recovery) Restore(ctx context.Context, actor Actor, backupID string, mapping OwnershipMapping) error {
	_, err := r.RestoreReceipt(ctx, actor, backupID, mapping)
	return err
}

// RestoreReceipt is an explicit, administrator-started restore. The add-on
// must not be running. The backup is verified before anything changes, the
// files are staged next to the data and swapped in atomically, and the data
// they replace stays as a recovery copy. Access is not restored as it was:
// every grant is checked again against the accounts and the lowerings of
// access that happened after the backup, and proposals are never replayed.
func (r *Recovery) RestoreReceipt(ctx context.Context, actor Actor, backupID string, mapping OwnershipMapping) (RestoreReceipt, error) {
	if err := r.admin(ctx, actor); err != nil {
		return RestoreReceipt{}, err
	}
	addonID, err := r.find(backupID)
	if err != nil {
		return RestoreReceipt{}, err
	}
	return r.RestoreOp(ctx, actor, "", addonID, backupID, mapping)
}

// RestoreOp is RestoreReceipt for the add-on named by addonID, under an
// operation ID. The backup must belong to that add-on: a backup of another
// one is refused with ErrBackupAddonMismatch before anything is touched. A
// repeat of the same ID and backup returns the receipt of the restore it made
// and does not restore again: a second restore would lose the writes made
// since the first.
func (r *Recovery) RestoreOp(ctx context.Context, actor Actor, opID, addonID, backupID string, mapping OwnershipMapping) (rc RestoreReceipt, err error) {
	if err = r.admin(ctx, actor); err != nil {
		return RestoreReceipt{}, err
	}
	if err = validID(addonID); err != nil {
		return RestoreReceipt{}, err
	}
	owner, err := r.find(backupID)
	if err != nil {
		return RestoreReceipt{}, err
	}
	if owner != addonID {
		return RestoreReceipt{}, ErrBackupAddonMismatch
	}
	unlock, err := r.lock(addonID)
	if err != nil {
		return RestoreReceipt{}, err
	}
	defer unlock()
	if raw, ok, rerr := r.replayed(addonID, opID, "restore", backupID); rerr != nil {
		return RestoreReceipt{}, rerr
	} else if ok {
		var old RestoreReceipt
		if json.Unmarshal(raw, &old) == nil {
			return old, nil
		}
	}
	if r.cfg.Policy == nil {
		return RestoreReceipt{}, ErrUnavailable
	}
	info, err := r.cfg.Host.Info(ctx, addonID)
	if err != nil {
		return RestoreReceipt{}, err
	}
	if info.Running {
		return RestoreReceipt{}, ErrAddonRunning
	}
	if _, busy := r.loadJournal(addonID); busy {
		return RestoreReceipt{}, ErrBusy
	}
	m, dir, sum, err := r.readManifest(addonID, backupID)
	if err != nil {
		return RestoreReceipt{}, err
	}
	if err = verifyDir(ctx, dir, m, sum); err != nil {
		if errors.Is(err, ErrBackupCorrupt) {
			_ = replaceVerified(dir, false)
		}
		return RestoreReceipt{}, err
	}
	if info.Installed {
		if m.Package.SourceID != info.Receipt.SourceID || m.Package.Channel != info.Receipt.Channel || m.Package.Verification != info.Receipt.Verification {
			return RestoreReceipt{}, ErrIdentityChanged
		}
		if info.Receipt.Manifest.DataFormat.Max < m.Package.DataFormat {
			return RestoreReceipt{}, ErrIncompatibleFormat
		}
	}
	if err = r.checkMapping(ctx, m, mapping); err != nil {
		return RestoreReceipt{}, err
	}
	hctx, cancel := context.WithTimeout(ctx, r.cfg.DrainTimeout)
	release, err := r.cfg.Policy.Hold(hctx, addonID)
	cancel()
	if err != nil {
		return RestoreReceipt{}, err
	}
	defer release()
	q, err := r.cfg.Host.Quiesce(ctx, addonID)
	if err != nil {
		return RestoreReceipt{}, err
	}
	finished := false
	defer func() {
		dctx, stop := r.detached(ctx)
		defer stop()
		if finished {
			_ = q.Resume(dctx)
			return
		}
		q.Release()
	}()
	prior, _ := r.loadState(addonID)
	lost, err := r.cfg.Policy.LostWrites(ctx, addonID, m.Created)
	if err != nil {
		return RestoreReceipt{}, err
	}
	workID, err := r.newBackupID()
	if err != nil {
		return RestoreReceipt{}, err
	}
	reqOp := opID
	opID = workID
	j := journalRecord{Kind: "restore", Phase: "staging", ID: opID, Op: reqOp, Backup: backupID, Mapping: mapping, Actor: actor.UserID,
		Started: r.cfg.Now().UTC(), Prior: &prior, Staged: r.stagedPath(addonID, opID), Prev: r.previousPath(addonID, opID)}
	j.Receipt = &RestoreReceipt{AddonID: addonID, BackupID: backupID, BackupCreated: m.Created, At: j.Started, Actor: actor.UserID, LostWrites: lost}
	if err = r.saveJournal(addonID, j); err != nil {
		return RestoreReceipt{}, err
	}
	if err = r.saveState(addonID, State{State: StateRestoring, Since: j.Started, Reason: "a restore is in progress", RecoveryBackup: prior.RecoveryBackup, Pinned: prior.Pinned, PreviousGeneration: prior.PreviousGeneration}); err != nil {
		_ = r.clearJournal(addonID)
		return RestoreReceipt{}, err
	}
	if err = r.hook("restore-staging"); err != nil {
		return RestoreReceipt{}, r.abortRestore(addonID, j, err)
	}
	if err = r.stageBackup(ctx, addonID, dir, m, j.Staged); err != nil {
		return RestoreReceipt{}, r.abortRestore(addonID, j, err)
	}
	j.Phase = "swap"
	if err = r.saveJournal(addonID, j); err != nil {
		return RestoreReceipt{}, r.abortRestore(addonID, j, err)
	}
	if err = r.hook("restore-swap"); err != nil {
		return RestoreReceipt{}, r.abortRestore(addonID, j, err)
	}
	if err = r.swapIn(addonID, j.Staged, j.Prev); err != nil {
		return RestoreReceipt{}, r.abortRestore(addonID, j, err)
	}
	j.Phase = "policy"
	if err = r.saveJournal(addonID, j); err != nil {
		return RestoreReceipt{}, err
	}
	if err = r.hook("restore-policy"); err != nil {
		return RestoreReceipt{}, err
	}
	rc, err = r.finishRestore(ctx, addonID, dir, m, j)
	if err != nil {
		return RestoreReceipt{}, err
	}
	finished = true
	return rc, nil
}

// checkMapping validates the ownership mapping against the mode of the
// backup and the mode of the store.
func (r *Recovery) checkMapping(ctx context.Context, m Manifest, mapping OwnershipMapping) error {
	now := r.cfg.Policy.Mode()
	switch {
	case m.Mode == "local-owner" && now == "named-user":
		if len(mapping) != 1 || mapping["local-owner"] == "" {
			return ErrMappingRequired
		}
	case m.Mode == "named-user" && now == "local-owner":
		// Whether the backup holds anything is the policy's to say; a mapping
		// has no meaning here.
		if len(mapping) != 0 {
			return ErrMappingInvalid
		}
	default:
		if now == "local-owner" && len(mapping) != 0 {
			return ErrMappingInvalid
		}
	}
	return mapping.Validate(ctx, r.cfg.Account)
}

func (r *Recovery) stagedPath(addonID, op string) string {
	return filepath.Join(r.cfg.Root, "data", ".restore-"+addonID+"-"+op)
}

func (r *Recovery) previousPath(addonID, op string) string {
	return filepath.Join(r.stateDir(addonID), "previous-"+op)
}

// stageBackup copies the data of a verified backup to staged. The copy is
// made under a partial name and renamed when it is complete and verified, so
// that the existence of staged says it can be swapped in.
func (r *Recovery) stageBackup(ctx context.Context, addonID, backupDir string, m Manifest, staged string) error {
	if _, err := os.Lstat(staged); err == nil {
		return nil
	}
	partial := staged + ".partial"
	_ = removeAll(partial)
	if err := os.MkdirAll(filepath.Dir(staged), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(partial, 0o700); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = removeAll(partial)
		}
	}()
	if err := copyTree(ctx, filepath.Join(backupDir, "data"), partial); err != nil {
		return err
	}
	for _, e := range m.Files {
		rel, found := strings.CutPrefix(e.Path, "data/")
		if !found {
			continue
		}
		sum, n, err := hashFile(filepath.Join(partial, filepath.FromSlash(rel)))
		if err != nil || sum != e.SHA256 || n != e.Size {
			return ErrBackupCorrupt
		}
	}
	if err := syncTree(partial); err != nil {
		return err
	}
	if err := os.Rename(partial, staged); err != nil {
		return err
	}
	ok = true
	return syncPath(filepath.Dir(staged))
}

// copyTree copies a tree of regular files and directories exactly.
func copyTree(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case d.Type().IsRegular():
			return copyPlain(p, target)
		}
		return fmt.Errorf("%w: unexpected file type at %s", ErrBackupCorrupt, rel)
	})
}

// swapIn replaces the data directory with the staged copy and keeps the old
// one as the recovery copy. It can be repeated after a crash at any point.
func (r *Recovery) swapIn(addonID, staged, prev string) error {
	data := r.dataDir(addonID)
	_, serr := os.Lstat(staged)
	staging := serr == nil
	_, derr := os.Lstat(data)
	hasData := derr == nil
	_, perr := os.Lstat(prev)
	hasPrev := perr == nil
	switch {
	case !staging && hasData && hasPrev:
		return nil // already swapped
	case !staging && !hasPrev && hasData:
		return fmt.Errorf("%w: neither a staged copy nor a recovery copy exists", ErrBackupCorrupt)
	case !staging && !hasData:
		return fmt.Errorf("%w: no staged copy and no data", ErrBackupCorrupt)
	}
	if err := os.MkdirAll(filepath.Dir(prev), 0o700); err != nil {
		return err
	}
	if hasData && !hasPrev {
		if err := os.Rename(data, prev); err != nil {
			return err
		}
		if err := syncPath(filepath.Dir(prev)); err != nil {
			return err
		}
	}
	if err := r.hook("restore-swap-mid"); err != nil {
		return err
	}
	if err := os.Rename(staged, data); err != nil {
		return err
	}
	if err := os.Chmod(data, 0o700); err != nil { // #nosec G302 -- directory mode: the owner needs the search bit
		return err
	}
	return syncPath(filepath.Dir(data))
}

// restoreFromBackup stages and swaps a verified backup in, for a rollback.
func (r *Recovery) restoreFromBackup(ctx context.Context, addonID, backupID, op string) error {
	m, dir, sum, err := r.readManifest(addonID, backupID)
	if err != nil {
		return err
	}
	if err = verifyDir(ctx, dir, m, sum); err != nil {
		return err
	}
	staged, prev := r.stagedPath(addonID, op), r.previousPath(addonID, op)
	if err = r.stageBackup(ctx, addonID, dir, m, staged); err != nil {
		return err
	}
	return r.swapIn(addonID, staged, prev)
}

// abortRestore undoes a restore that failed before the swap changed the
// data: the staged copy goes, the earlier state returns.
func (r *Recovery) abortRestore(addonID string, j journalRecord, cause error) error {
	if errors.Is(cause, errCrash) {
		return cause
	}
	if j.Phase == "swap" {
		// Either the swap did not start or it finished; ask the file system.
		if _, err := os.Lstat(j.Staged); err != nil {
			return fmt.Errorf("restore stopped after the swap: %w", cause)
		}
	}
	_ = removeAll(j.Staged)
	_ = removeAll(j.Staged + ".partial")
	prior := State{State: StateReady}
	if j.Prior != nil {
		prior = *j.Prior
	}
	_ = r.saveState(addonID, prior)
	_ = r.clearJournal(addonID)
	return cause
}

// finishRestore applies the policy part of a restore and records the end.
func (r *Recovery) finishRestore(ctx context.Context, addonID, backupDir string, m Manifest, j journalRecord) (RestoreReceipt, error) {
	pol, err := os.ReadFile(filepath.Join(backupDir, "state", "policy.json")) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return RestoreReceipt{}, err
	}
	props, err := os.ReadFile(filepath.Join(backupDir, "state", "proposals.json")) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return RestoreReceipt{}, err
	}
	rep, err := r.cfg.Policy.Restore(ctx, addonID, PolicyExport{Policy: pol, Proposals: props}, RestoreOptions{
		Mapping: OwnershipMapping(j.Mapping), Account: r.cfg.Account, Since: m.Created,
	})
	if err != nil {
		// The data is restored and the add-on stays blocked: the same restore
		// is repeated from the journal at restart or by the administrator.
		return RestoreReceipt{}, err
	}
	rc := RestoreReceipt{AddonID: addonID, BackupID: j.Backup, BackupCreated: m.Created, At: r.cfg.Now().UTC(), Actor: j.Actor, Policy: rep}
	if j.Receipt != nil {
		rc.LostWrites = j.Receipt.LostWrites
	}
	rc.PreviousCopy = filepath.Base(j.Prev)
	if _, err := os.Lstat(j.Prev); err != nil {
		rc.PreviousCopy = ""
	}
	rc.Warnings = restoreWarnings(rc)
	st := State{State: StateReady, LastRestore: &rc}
	if j.Prior != nil {
		st.Pinned = j.Prior.Pinned
		st.Ops = j.Prior.Ops
	}
	if raw, merr := json.Marshal(rc); merr == nil {
		st.record(OpRecord{ID: j.Op, Kind: "restore", Params: j.Backup, Result: raw})
	}
	if err = r.saveState(addonID, st); err != nil {
		return RestoreReceipt{}, err
	}
	if err = r.clearJournal(addonID); err != nil {
		return RestoreReceipt{}, err
	}
	return rc, nil
}

func restoreWarnings(rc RestoreReceipt) []string {
	var w []string
	w = append(w, fmt.Sprintf("The data is back to %s. Changes made after that time are not in it.", rc.BackupCreated.Format(time.RFC3339)))
	plural := func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	}
	if rc.LostWrites > 0 {
		w = append(w, fmt.Sprintf("%d %s after the backup %s lost.", rc.LostWrites, plural(rc.LostWrites, "change applied", "changes applied"), plural(rc.LostWrites, "is", "are")))
	}
	if n := rc.Policy.GrantsDropped; n > 0 {
		w = append(w, fmt.Sprintf("%d %s not restored because the person lost access or the account is gone.", n, plural(n, "share was", "shares were")))
	}
	if n := len(rc.Policy.ResourcesSkipped); n > 0 {
		w = append(w, fmt.Sprintf("%d %s not restored for access because the owner is not a current account.", n, plural(n, "database was", "databases were")))
	}
	if n := rc.Policy.ProposalsCancelled; n > 0 {
		w = append(w, fmt.Sprintf("%d %s waiting for confirmation %s cancelled.", n, plural(n, "proposal that was", "proposals that were"), plural(n, "was", "were")))
	}
	if n := rc.Policy.ProposalsUnknown; n > 0 {
		w = append(w, fmt.Sprintf("%d %s no known outcome; %s looked up by receipt and never applied again.", n, plural(n, "operation had", "operations had"), plural(n, "it is", "they are")))
	}
	if rc.Policy.HistoryTruncated {
		w = append(w, "The record of removed access may be incomplete for this backup, so no share was restored. The data is back; share it again where it is needed.")
	}
	if rc.PreviousCopy != "" {
		w = append(w, "The data that was replaced is kept as a recovery copy until you purge it.")
	}
	sort.Strings(w[1:])
	return w
}

// errCrash is returned by a test seam to stop an operation without running
// its clean-up, as a process that dies would. It is never produced otherwise.
var errCrash = errors.New("simulated crash")
