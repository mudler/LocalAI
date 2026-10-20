// SPDX-License-Identifier: MIT
package recovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const (
	manifestName     = "manifest.json"
	manifestSumName  = "manifest.sha256"
	verifiedName     = "verified.json"
	backupTmpPrefix  = ".tmp-"
	manifestVersion  = 1
	storageMarginMin = 16 << 20
)

// Reasons a backup was made.
const (
	ReasonManual  = "manual"
	ReasonUpgrade = "upgrade"
	ReasonRestore = "restore"
)

// PackageFacts is the trusted identity and format of the package that wrote
// the data. A restore and an upgrade compare against it.
type PackageFacts struct {
	Digest       string `json:"digest"`
	SourceID     string `json:"sourceId"`
	Channel      string `json:"channel,omitempty"`
	Verification string `json:"verification"`
	Version      string `json:"version"`
	Generation   uint64 `json:"generation"`
	DataFormat   uint64 `json:"dataFormat"`
}

// Manifest describes one backup. Its sha256 is the digest of the receipt.
type Manifest struct {
	Version int          `json:"version"`
	ID      string       `json:"id"`
	AddonID string       `json:"addonId"`
	Created time.Time    `json:"created"`
	Reason  string       `json:"reason"`
	Mode    string       `json:"mode"`
	Package PackageFacts `json:"package"`
	// Enabled says whether the add-on was meant to run when it was backed up.
	Enabled bool        `json:"enabled"`
	Files   []FileEntry `json:"files"`
	// Skipped counts symbolic links and special files that were not copied.
	Skipped int `json:"skipped,omitempty"`
}

// BackupReceipt is the result of a backup.
type BackupReceipt struct {
	ID       string    `json:"id"`
	AddonID  string    `json:"addonId"`
	Created  time.Time `json:"created"`
	Reason   string    `json:"reason"`
	Digest   string    `json:"digest"`
	Size     int64     `json:"size"`
	Files    int       `json:"files"`
	Verified bool      `json:"verified"`
	// Pinned is set while the backup is the recovery copy of an upgrade.
	Pinned bool `json:"pinned,omitempty"`
	// Mode and Version are the ownership mode and package version it holds.
	Mode    string `json:"mode"`
	Version string `json:"version"`
}

// Recovery owns the backup, upgrade, restore and purge of add-on data.
type Recovery struct {
	cfg   Config
	locks *lockSet
	// Test-only seams; nil in production.
	free func(path string) (uint64, error)
	fail func(phase string) error
}

type lockSet struct {
	mu   sync.Mutex
	busy map[string]bool
}

// New returns a Recovery. Config.Root must be an absolute path.
func New(cfg Config) (*Recovery, error) {
	if !filepath.IsAbs(cfg.Root) || cfg.Host == nil {
		return nil, ErrInvalid
	}
	if cfg.Retention.Keep < 1 {
		cfg.Retention = DefaultRetention
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.DrainTimeout <= 0 {
		cfg.DrainTimeout = 15 * time.Second
	}
	return &Recovery{cfg: cfg, locks: &lockSet{busy: map[string]bool{}}}, nil
}

// With returns a Recovery with the same state and locks that reaches the
// add-on through host. The lifecycle manager uses it while it holds its own
// lock.
func (r *Recovery) With(host Host) *Recovery {
	c := *r
	c.cfg.Host = host
	return &c
}

// SetPolicy sets the policy port once the owner has one.
func (r *Recovery) SetPolicy(p PolicyPort) { r.cfg.Policy = p }

// SetAccount sets the account directory.
func (r *Recovery) SetAccount(f func(context.Context, string) error) { r.cfg.Account = f }

func (r *Recovery) lock(addonID string) (func(), error) {
	r.locks.mu.Lock()
	defer r.locks.mu.Unlock()
	if r.locks.busy[addonID] {
		return nil, ErrBusy
	}
	r.locks.busy[addonID] = true
	return func() {
		r.locks.mu.Lock()
		delete(r.locks.busy, addonID)
		r.locks.mu.Unlock()
	}, nil
}

func (r *Recovery) dataDir(id string) string    { return filepath.Join(r.cfg.Root, "data", id) }
func (r *Recovery) backupsDir(id string) string { return filepath.Join(r.cfg.Root, "backups", id) }
func (r *Recovery) stateDir(id string) string   { return filepath.Join(r.cfg.Root, "recovery", id) }

func (r *Recovery) hook(phase string) error {
	if r.fail != nil {
		return r.fail(phase)
	}
	return nil
}

func (r *Recovery) admin(ctx context.Context, actor Actor) error {
	if r.cfg.Admin == nil || r.cfg.Admin(ctx, actor) != nil {
		return ErrForbidden
	}
	return nil
}

func (r *Recovery) detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
}

func validID(id string) error {
	if !contract.ValidAddonID(id) {
		return ErrInvalid
	}
	return nil
}

func validBackupID(id string) bool {
	if len(id) < 4 || len(id) > 64 || id[0] != 'b' {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func (r *Recovery) newBackupID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "b" + r.cfg.Now().UTC().Format("20060102t150405z") + "-" + hex.EncodeToString(b[:]), nil
}

func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	if st.Bsize <= 0 {
		return 0, errors.New("the file system reports no block size")
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

func (r *Recovery) freeSpace(path string) (uint64, error) {
	if r.free != nil {
		return r.free(path)
	}
	return freeBytes(path)
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

// replaceJSON writes a small state file atomically.
func replaceJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0o600); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncPath(filepath.Dir(path))
}

func factsOf(info Info) PackageFacts {
	return PackageFacts{
		Digest: info.Receipt.Digest, SourceID: info.Receipt.SourceID, Channel: info.Receipt.Channel,
		Verification: info.Receipt.Verification, Version: info.Receipt.Manifest.Version,
		Generation: info.Generation, DataFormat: info.Receipt.Manifest.DataFormat.Max,
	}
}

// Backup makes a verified, consistent backup of the add-on's data and of the
// owner's access state for it. The add-on is stopped while the files are
// copied and started again afterwards if it was meant to run, whatever the
// outcome. A backup that cannot be completed leaves nothing behind.
func (r *Recovery) Backup(ctx context.Context, addonID string) (BackupReceipt, error) {
	return r.BackupOp(ctx, addonID, "")
}

// BackupOp is Backup under an operation ID. A repeat of the same ID returns
// the receipt of the backup it made and makes no second one.
func (r *Recovery) BackupOp(ctx context.Context, addonID, opID string) (rc BackupReceipt, err error) {
	if err := validID(addonID); err != nil {
		return BackupReceipt{}, err
	}
	unlock, err := r.lock(addonID)
	if err != nil {
		return BackupReceipt{}, err
	}
	defer unlock()
	if raw, ok, rerr := r.replayed(addonID, opID, "backup", ""); rerr != nil {
		return BackupReceipt{}, rerr
	} else if ok {
		var old BackupReceipt
		if json.Unmarshal(raw, &old) == nil {
			return old, nil
		}
	}
	info, err := r.cfg.Host.Info(ctx, addonID)
	if err != nil {
		return BackupReceipt{}, err
	}
	if !info.Installed {
		return BackupReceipt{}, ErrNotInstalled
	}
	if r.cfg.Policy == nil {
		return BackupReceipt{}, ErrUnavailable
	}
	// Data that needs recovery is not a state to keep: a copy of half
	// migrated files would look like the newest good backup.
	if st, serr := r.loadState(addonID); serr != nil || st.State == StateNeedsRecovery {
		return BackupReceipt{}, ErrNeedsRecovery
	}
	// Admission is blocked and pending proposals are invalid before the
	// process stops, so nothing new starts against a draining add-on.
	hctx, cancel := context.WithTimeout(ctx, r.cfg.DrainTimeout)
	release, err := r.cfg.Policy.Hold(hctx, addonID)
	cancel()
	if err != nil {
		return BackupReceipt{}, err
	}
	defer release()
	if err = r.hook("before-quiesce"); err != nil {
		return BackupReceipt{}, err
	}
	q, err := r.cfg.Host.Quiesce(ctx, addonID)
	if err != nil {
		return BackupReceipt{}, err
	}
	defer func() {
		// The prior state comes back whatever happened: a canceled request
		// must not leave the add-on stopped.
		dctx, stop := r.detached(ctx)
		defer stop()
		if e := q.Resume(dctx); e != nil && err == nil {
			err = e
		}
	}()
	rc, err = r.writeBackup(ctx, addonID, info, ReasonManual)
	if err == nil {
		r.remember(addonID, opID, "backup", "", rc)
	}
	return rc, err
}

// writeBackup makes the backup while the add-on is stopped and held.
func (r *Recovery) writeBackup(ctx context.Context, addonID string, info Info, reason string) (BackupReceipt, error) {
	id, err := r.newBackupID()
	if err != nil {
		return BackupReceipt{}, err
	}
	root := r.backupsDir(addonID)
	if err = os.MkdirAll(root, 0o700); err != nil {
		return BackupReceipt{}, err
	}
	r.cleanTemps(addonID)
	data := r.dataDir(addonID)
	// The estimate counts every file once, plus the metadata, and a margin.
	est, err := sizeOf(data)
	if err != nil {
		return BackupReceipt{}, err
	}
	est += 1 << 20
	margin := int64(storageMarginMin)
	if m := est / 10; m > margin {
		margin = m
	}
	free, err := r.freeSpace(root)
	if err != nil {
		return BackupReceipt{}, err
	}
	if free <= math.MaxInt64 && int64(free) < est+margin {
		return BackupReceipt{}, ErrInsufficientStorage
	}
	tmp := filepath.Join(root, backupTmpPrefix+id)
	ok := false
	defer func() {
		if !ok {
			_ = removeAll(tmp)
		}
	}()
	if err = os.Mkdir(tmp, 0o700); err != nil {
		return BackupReceipt{}, err
	}
	var entries []FileEntry
	files := 0
	step := func() error {
		files++
		return r.hook("copy-file")
	}
	var skipped int
	if _, serr := os.Lstat(data); serr == nil {
		entries, skipped, err = copyData(ctx, data, filepath.Join(tmp, "data"), "data", step)
		if err != nil {
			return BackupReceipt{}, err
		}
	} else if !errors.Is(serr, fs.ErrNotExist) {
		return BackupReceipt{}, serr
	} else if err = os.Mkdir(filepath.Join(tmp, "data"), 0o700); err != nil {
		return BackupReceipt{}, err
	}
	if err = r.hook("after-data"); err != nil {
		return BackupReceipt{}, err
	}
	// The owner's access state, as of the moment the add-on is stopped.
	exp, err := r.cfg.Policy.Export(ctx, addonID)
	if err != nil {
		return BackupReceipt{}, err
	}
	stateDir := filepath.Join(tmp, "state")
	if err = os.Mkdir(stateDir, 0o700); err != nil {
		return BackupReceipt{}, err
	}
	for name, b := range map[string][]byte{"policy.json": exp.Policy, "proposals.json": exp.Proposals} {
		if err = writeBytes(filepath.Join(stateDir, name), b); err != nil {
			return BackupReceipt{}, err
		}
		entries = append(entries, FileEntry{Path: "state/" + name})
	}
	if r.cfg.SelectionsDir != "" {
		sel, _, serr := copyData(ctx, r.cfg.SelectionsDir, filepath.Join(stateDir, "selections"), "state/selections", nil)
		if serr != nil && !errors.Is(serr, fs.ErrNotExist) {
			return BackupReceipt{}, serr
		}
		if serr == nil {
			entries = append(entries, sel...)
		}
	}
	if err = describe(ctx, tmp, entries); err != nil {
		return BackupReceipt{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	m := Manifest{Version: manifestVersion, ID: id, AddonID: addonID, Created: r.cfg.Now().UTC(), Reason: reason,
		Mode: r.cfg.Policy.Mode(), Package: factsOf(info), Enabled: info.Enabled, Files: entries, Skipped: skipped}
	if err = writeJSONFile(filepath.Join(tmp, manifestName), m); err != nil {
		return BackupReceipt{}, err
	}
	sum, size, err := hashFile(filepath.Join(tmp, manifestName))
	if err != nil {
		return BackupReceipt{}, err
	}
	if err = writeBytes(filepath.Join(tmp, manifestSumName), []byte(sum+"\n")); err != nil {
		return BackupReceipt{}, err
	}
	// Verified while it is still private: a backup that does not read back
	// is never published.
	if err = verifyDir(ctx, tmp, m, sum); err != nil {
		return BackupReceipt{}, err
	}
	if err = replaceVerified(tmp, true); err != nil {
		return BackupReceipt{}, err
	}
	if err = ctx.Err(); err != nil {
		return BackupReceipt{}, err
	}
	if err = syncTree(tmp); err != nil {
		return BackupReceipt{}, err
	}
	if err = r.hook("before-publish"); err != nil {
		return BackupReceipt{}, err
	}
	final := filepath.Join(root, id)
	if err = os.Rename(tmp, final); err != nil {
		return BackupReceipt{}, err
	}
	ok = true
	if err = syncPath(root); err != nil {
		return BackupReceipt{}, err
	}
	var total int64 = size
	for _, e := range entries {
		total += e.Size
	}
	rc := receiptOf(m, sum, total, true, false)
	if perr := r.prune(addonID); perr != nil {
		return rc, nil
	}
	return rc, nil
}

func writeBytes(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

func receiptOf(m Manifest, sum string, size int64, verified, pinned bool) BackupReceipt {
	return BackupReceipt{ID: m.ID, AddonID: m.AddonID, Created: m.Created, Reason: m.Reason, Digest: "sha256:" + sum,
		Size: size, Files: len(m.Files), Verified: verified, Pinned: pinned, Mode: m.Mode, Version: m.Package.Version}
}

func replaceVerified(dir string, ok bool) error {
	p := filepath.Join(dir, verifiedName)
	_ = os.Remove(p)
	return writeJSONFile(p, struct {
		OK bool `json:"ok"`
	}{ok})
}

// verifyDir re-reads every file of a backup directory against the manifest
// and checks every SQLite file with the integrity check.
func verifyDir(ctx context.Context, dir string, m Manifest, manifestSum string) error {
	if m.Version != manifestVersion || m.ID == "" || m.AddonID == "" {
		return ErrBackupCorrupt
	}
	got, _, err := hashFile(filepath.Join(dir, manifestName))
	if err != nil || got != manifestSum {
		return ErrBackupCorrupt
	}
	listed := map[string]bool{manifestName: true, manifestSumName: true, verifiedName: true}
	for _, e := range m.Files {
		if err = ctx.Err(); err != nil {
			return err
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(e.Path)))
		if clean != e.Path || strings.HasPrefix(clean, "../") || filepath.IsAbs(e.Path) || (!strings.HasPrefix(clean, "data/") && !strings.HasPrefix(clean, "state/")) {
			return ErrBackupCorrupt
		}
		listed[e.Path] = true
		p := filepath.Join(dir, filepath.FromSlash(e.Path))
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() || info.Size() != e.Size {
			return ErrBackupCorrupt
		}
		sum, _, err := hashFile(p)
		if err != nil || sum != e.SHA256 {
			return ErrBackupCorrupt
		}
		if e.SQLite {
			if _, err = checkSQLite(ctx, p); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return ErrBackupCorrupt
			}
		}
	}
	// A file the manifest does not name is as wrong as a missing one.
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if !listed[filepath.ToSlash(rel)] {
			return ErrBackupCorrupt
		}
		return nil
	})
}

func (r *Recovery) readManifest(addonID, backupID string) (Manifest, string, string, error) {
	if !validBackupID(backupID) {
		return Manifest{}, "", "", ErrBackupNotFound
	}
	dir := filepath.Join(r.backupsDir(addonID), backupID)
	b, err := os.ReadFile(filepath.Join(dir, manifestName)) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Manifest{}, dir, "", ErrBackupNotFound
		}
		return Manifest{}, dir, "", err
	}
	sumBytes, err := os.ReadFile(filepath.Join(dir, manifestSumName)) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return Manifest{}, dir, "", ErrBackupCorrupt
	}
	var m Manifest
	if err = json.Unmarshal(b, &m); err != nil || m.ID != backupID || m.AddonID != addonID {
		return Manifest{}, dir, "", ErrBackupCorrupt
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != strings.TrimSpace(string(sumBytes)) {
		return Manifest{}, dir, "", ErrBackupCorrupt
	}
	return m, dir, hex.EncodeToString(sum[:]), nil
}

// Verify re-reads the backup completely and records the result. A corrupt
// backup is reported with ErrBackupCorrupt and stays in place: removing it is
// an explicit choice.
func (r *Recovery) Verify(ctx context.Context, addonID, backupID string) (BackupReceipt, error) {
	if err := validID(addonID); err != nil {
		return BackupReceipt{}, err
	}
	m, dir, sum, err := r.readManifest(addonID, backupID)
	if err != nil {
		if errors.Is(err, ErrBackupCorrupt) {
			_ = replaceVerified(dir, false)
		}
		return BackupReceipt{}, err
	}
	if err = verifyDir(ctx, dir, m, sum); err != nil {
		if errors.Is(err, ErrBackupCorrupt) {
			_ = replaceVerified(dir, false)
		}
		return BackupReceipt{}, err
	}
	_ = replaceVerified(dir, true)
	return r.describeBackup(addonID, m, sum, dir, true), nil
}

func (r *Recovery) describeBackup(addonID string, m Manifest, sum, dir string, verified bool) BackupReceipt {
	var total int64
	for _, e := range m.Files {
		total += e.Size
	}
	if info, err := os.Stat(filepath.Join(dir, manifestName)); err == nil {
		total += info.Size()
	}
	st, _ := r.loadState(addonID)
	return receiptOf(m, sum, total, verified, st.Pinned == m.ID)
}

// BackupInfo is one entry of the backup list. Verification is the last
// recorded result: "verified", "failed" or "unverified".
type BackupInfo struct {
	BackupReceipt
	Verification string `json:"verification"`
}

// List returns the backups of an add-on, newest first, with the last recorded
// verification state. It reads manifests only; Verify reads everything.
func (r *Recovery) List(ctx context.Context, addonID string) ([]BackupInfo, error) {
	if err := validID(addonID); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(r.backupsDir(addonID))
	if errors.Is(err, fs.ErrNotExist) {
		return []BackupInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []BackupInfo{}
	for _, e := range entries {
		if !e.IsDir() || !validBackupID(e.Name()) {
			continue
		}
		m, dir, sum, err := r.readManifest(addonID, e.Name())
		if err != nil {
			out = append(out, BackupInfo{BackupReceipt: BackupReceipt{ID: e.Name(), AddonID: addonID}, Verification: "failed"})
			continue
		}
		state := "unverified"
		if b, err := os.ReadFile(filepath.Join(dir, verifiedName)); err == nil { // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
			var v struct {
				OK bool `json:"ok"`
			}
			if json.Unmarshal(b, &v) == nil {
				state = map[bool]string{true: "verified", false: "failed"}[v.OK]
			}
		}
		out = append(out, BackupInfo{BackupReceipt: r.describeBackup(addonID, m, sum, dir, state == "verified"), Verification: state})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (r *Recovery) cleanTemps(addonID string) {
	entries, _ := os.ReadDir(r.backupsDir(addonID))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), backupTmpPrefix) {
			_ = removeAll(filepath.Join(r.backupsDir(addonID), e.Name()))
		}
	}
}

// protectedBackups returns the IDs retention must keep: the newest verified
// backup, and the recovery copy of the last upgrade until a newer verified
// backup exists.
func (r *Recovery) protectedBackups(list []BackupInfo, st State) map[string]bool {
	pinned := st.Pinned
	keep := map[string]bool{}
	if st.State == StateNeedsRecovery {
		// The data cannot be trusted. The copies that were set aside for
		// its recovery stay until a restore or a purge resolves the state,
		// whatever newer backups exist.
		for _, id := range []string{st.RecoveryBackup, st.Pinned} {
			if id != "" {
				keep[id] = true
			}
		}
	}
	for _, b := range list {
		if b.Verification == "verified" {
			keep[b.ID] = true
			break
		}
	}
	if pinned != "" {
		var pin *BackupInfo
		for i := range list {
			if list[i].ID == pinned {
				pin = &list[i]
			}
		}
		if pin != nil {
			newer := false
			for _, b := range list {
				if b.ID != pinned && b.Verification == "verified" && b.Created.After(pin.Created) {
					newer = true
				}
			}
			if !newer {
				keep[pinned] = true
			}
		}
	}
	return keep
}

// prune applies the explicit retention policy. It never removes the newest
// verified backup nor the recovery copy of an upgrade that has no newer
// verified backup.
func (r *Recovery) prune(addonID string) error {
	list, err := r.List(context.Background(), addonID)
	if err != nil {
		return err
	}
	st, _ := r.loadState(addonID)
	keep := r.protectedBackups(list, st)
	now := r.cfg.Now()
	var firstErr error
	for i, b := range list {
		if keep[b.ID] || b.Created.IsZero() {
			continue
		}
		old := r.cfg.Retention.MaxAge > 0 && now.Sub(b.Created) > r.cfg.Retention.MaxAge
		if i < r.cfg.Retention.Keep && !old {
			continue
		}
		if err := removeAll(filepath.Join(r.backupsDir(addonID), b.ID)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Prune applies the retention policy now, at an administrator's request. It
// removes only what the policy allows: the newest verified backup and the
// recovery copy of an upgrade stay, whatever their age.
func (r *Recovery) Prune(ctx context.Context, actor Actor, addonID string) ([]string, error) {
	if err := r.admin(ctx, actor); err != nil {
		return nil, err
	}
	if err := validID(addonID); err != nil {
		return nil, err
	}
	unlock, err := r.lock(addonID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	before, err := r.List(ctx, addonID)
	if err != nil {
		return nil, err
	}
	if err = r.prune(addonID); err != nil {
		return nil, err
	}
	after, err := r.List(ctx, addonID)
	if err != nil {
		return nil, err
	}
	left := map[string]bool{}
	for _, b := range after {
		left[b.ID] = true
	}
	removed := []string{}
	for _, b := range before {
		if !left[b.ID] {
			removed = append(removed, b.ID)
		}
	}
	return removed, nil
}

// DeleteBackup removes one backup at an administrator's request. The only
// verified copy, and the recovery copy of an upgrade, are never removed this
// way: a purge with explicit scope is the way to remove them.
func (r *Recovery) DeleteBackup(ctx context.Context, actor Actor, addonID, backupID string) error {
	if err := r.admin(ctx, actor); err != nil {
		return err
	}
	if err := validID(addonID); err != nil {
		return err
	}
	unlock, err := r.lock(addonID)
	if err != nil {
		return err
	}
	defer unlock()
	list, err := r.List(ctx, addonID)
	if err != nil {
		return err
	}
	found := false
	for _, b := range list {
		found = found || b.ID == backupID
	}
	if !found || !validBackupID(backupID) {
		return ErrBackupNotFound
	}
	st, _ := r.loadState(addonID)
	if r.protectedBackups(list, st)[backupID] {
		return ErrSoleRecoveryCopy
	}
	if err = removeAll(filepath.Join(r.backupsDir(addonID), backupID)); err != nil {
		return err
	}
	return syncPath(r.backupsDir(addonID))
}

func (r *Recovery) String() string { return fmt.Sprintf("recovery(%s)", r.cfg.Root) }
