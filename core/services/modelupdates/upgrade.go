package modelupdates

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/atomicfile"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

// Runtime loads and unloads a model and makes the frontends read its config
// again. Standalone it is the local model loader. In a cluster the strategy
// of the upgrade (stop and start today, a canary later) is a Runtime: this
// package only says when to unload, reload and load.
type Runtime interface {
	// IsLoaded reports whether the model runs now. A cluster runtime asks
	// its registry, so the call stops with ctx.
	IsLoaded(ctx context.Context, name string) bool
	// Unload stops the model.
	Unload(ctx context.Context, name string) error
	// Reload makes every frontend read the model config from disk again
	// (config loader, config revision, the message to peer frontends).
	Reload(ctx context.Context, name string) error
	// Load loads the model now. It is the load check: an error rolls the
	// upgrade back.
	Load(ctx context.Context, name string) error
}

// LoadGate is a Runtime that can refuse the loads of a model while its files
// change. Between stopping the model and switching its files, a request (or,
// in a cluster, the reconciler keeping min_replicas) would otherwise start the
// old version again; for a change of the weights alone the config, and so its
// revision, stays the same, nothing would stop that process, and the load
// check would confirm the old version. Standalone it is the model loader; in a
// cluster it also holds the model's revision in the node registry, so every
// frontend and the reconciler refuse the model until UnblockLoads.
type LoadGate interface {
	BlockLoads(ctx context.Context, name string) error
	// UnblockLoads ends BlockLoads. It runs after the new (or, in a
	// rollback, the old) config was read again, so the loads it lets through
	// start the version now on disk.
	UnblockLoads(ctx context.Context, name string) error
}

// WorkerFile is one file of an upgrade that the workers of a cluster download
// for themselves: its path relative to the models directory, where it comes
// from and its sha256. A file with no URI is one the new version no longer
// has; the workers drop their copy.
type WorkerFile struct {
	Path   string
	URI    string
	SHA256 string
}

// Workers has each worker of a cluster download and verify its own copy of
// the files of an upgrade, so a load does not send the files from the
// frontend. The frontend still downloads its copy: it is the source of a load
// on a worker that has none. Nil (standalone, or a cluster whose nodes share
// one models directory) skips all three steps.
type Workers interface {
	// Stage asks the workers that run the model to download each file beside
	// its place and verify it. It runs while the frontend downloads. A worker
	// that cannot (a failed download, a wrong checksum, an older worker that
	// does not know the request) is logged and gets the file from the
	// frontend at its next load, so Stage never fails the upgrade.
	Stage(ctx context.Context, name string, files []WorkerFile)
	// Commit runs after the frontend switched its files and before the load
	// check. Every worker puts the files it staged in place and drops its
	// copy of the others, so its next load gets the new version. An error
	// rolls the upgrade back.
	Commit(ctx context.Context, name string, files []WorkerFile) error
	// Discard drops the staged files. With evict it also drops the copies in
	// place, so the next load sends the version the frontend has.
	Discard(ctx context.Context, name string, files []WorkerFile, evict bool)
}

// CanaryNotAvailable is the note of an upgrade whose model asks for the
// canary strategy, which a cluster does not run yet.
const CanaryNotAvailable = "canary is not available yet; the upgrade stopped and started the model"

// FS is the part of the filesystem an upgrade changes. Tests use one that
// fails at a chosen step.
type FS interface {
	Exists(path string) bool
	Rename(oldPath, newPath string) error
	Remove(path string) error
	ReadFile(path string) ([]byte, error)
	WriteFileAtomic(path string, data []byte) error
}

// OSFS is the real filesystem.
type OSFS struct{}

func (OSFS) Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
func (OSFS) Rename(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }
func (OSFS) Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ReadFile reads path. The caller passes a path it has checked with VerifyPath
// against the models directory.
func (OSFS) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path) // #nosec G304 -- see above: callers verify the path against the models directory
}
func (OSFS) WriteFileAtomic(path string, data []byte) error {
	return atomicfile.Write(path, data, 0644)
}

// Progress is what an upgrade reports while it runs.
type Progress struct {
	Phase    string
	Message  string
	FileName string
	Current  string
	Total    string
	Percent  float64
}

// UpgradeNotifier tells how an upgrade ended.
type UpgradeNotifier interface {
	ModelUpgradeResult(ctx context.Context, r UpgradeResult) error
}

// UpgradeResult is how an upgrade ended.
type UpgradeResult struct {
	OpID, Name         string
	From, To           string
	Failed, RolledBack bool
	Err                string
}

// Request asks for one upgrade.
type Request struct {
	OpID string
	Name string
	// ConfigOnly applies the model config and the prompt templates and
	// downloads nothing; it fails when the new config needs new files.
	ConfigOnly bool
	// Choices are keep-mine (ChoiceMine) or take-gallery (ChoiceGallery)
	// for each edited key the update also changes, by dotted key.
	Choices map[string]string
	// Retry runs a failed upgrade again with its choices; it fails when the
	// last upgrade did not fail.
	Retry bool
	// Progress, when set, gets each phase and the download progress.
	Progress func(Progress)
}

// Errors of an upgrade request that the API answers with a client error.
var (
	ErrNotUpgradable = errors.New("the model was not installed from a gallery by a release that records its origin; reinstall it to enable upgrades")
	ErrBusy          = errors.New("an upgrade of this model is already running")
	ErrUpToDate      = errors.New("the model is up to date")
	ErrNothingFailed = errors.New("the last upgrade of this model did not fail; there is nothing to retry")
	ErrNotFound      = errors.New("the model is not installed")
	// ErrHeld refuses an upgrade or a retry of a held model. The server
	// enforces the hold; the UI only hides the button.
	ErrHeld = errors.New("the model is held at its installed version; release the hold before you upgrade it")
	// ErrConfigChanged ends an upgrade whose model config was edited after
	// the upgrade read it: the merged config would drop the edit. Nothing of
	// the upgrade is applied and the edit stays; run the upgrade again.
	ErrConfigChanged = errors.New("the model config was edited while the upgrade ran; the upgrade was not applied and the edit was kept, run the upgrade again")
)

// ErrRefused is a preflight refusal: the upgrade did not start changing
// anything.
type ErrRefused struct{ Reason string }

func (e *ErrRefused) Error() string { return e.Reason }

// Upgrader runs upgrades. It is safe for concurrent use, but runs one upgrade
// of a model at a time; the gallery operation queue already runs one model
// operation at a time per frontend.
type Upgrader struct {
	SystemState *system.SystemState
	// ListGallery is the gallery listing (see Options.ListGallery).
	ListGallery func(ctx context.Context, fresh bool) ([]*gallery.GalleryModel, error)
	Runtime     Runtime
	FS          FS
	// Download fetches the staged files and verifies their sha256. Nil uses
	// the downloader.
	Download func(ctx context.Context, tasks []downloader.FileTask, status func(string, string, string, float64)) error
	// BackendAvailable reports whether the backend a model needs is
	// installed where it runs. Nil skips the check.
	BackendAvailable func(name string) (bool, error)
	// FreeSpace reports the free bytes of the filesystem of the models
	// directory. Nil uses xsysinfo; an error skips the check.
	FreeSpace func(path string) (uint64, error)
	// ContentLength reports the size of a remote file, 0 when unknown. Nil
	// asks the server; the check is best effort.
	ContentLength func(ctx context.Context, uri string) int64
	// Mutate runs fn while no edit of a model config runs in this process
	// (config.ModelConfigLoader.WithModelConfigMutation). Nil runs fn.
	Mutate func(fn func() error) error
	// Workers moves the files onto the workers of a cluster. Nil skips it.
	Workers Workers
	// InstallOptions are passed to the planning of the new version, for
	// instance the artifact materializer.
	InstallOptions []gallery.InstallOption
	// StrategyNote returns what a person should know about how the upgrade
	// of the model replaces its running replicas, or "" (stop and start, as
	// asked). Nil means stop and start.
	StrategyNote func(ctx context.Context, name string) string
	// Changed runs after a change that can alter which models need a person
	// (a hold, the end of an upgrade), so the digest notification follows at
	// once instead of at the next check. Nil does nothing.
	Changed func(name string)
	// Lock is the cluster-wide lock of one model (an advisory lock in
	// distributed mode). An upgrade holds it from its start to its end, so
	// two frontends never upgrade one model at once, and a journal whose lock
	// is free belongs to a frontend that stopped (RecoverAbandoned). Nil
	// (standalone) uses the process lock alone.
	Lock gallery.ModelLock
	// Instance names this frontend in the journal. It is informative: the
	// lock, not the name, tells whether the frontend of a journal still runs.
	Instance string
	Notifier UpgradeNotifier
	Now      func() time.Time
	// Heartbeat is how often a running upgrade touches its journal, so other
	// frontends do not take it for an abandoned one. Zero uses 30 seconds.
	Heartbeat time.Duration

	mu       sync.Mutex
	releases map[string]func()
}

func (u *Upgrader) modelsPath() string { return u.SystemState.Model.ModelsPath }

func (u *Upgrader) fs() FS {
	if u.FS == nil {
		return OSFS{}
	}
	return u.FS
}

func (u *Upgrader) now() time.Time {
	if u.Now == nil {
		return time.Now().UTC()
	}
	return u.Now().UTC()
}

func (u *Upgrader) mutate(fn func() error) error {
	if u.Mutate == nil {
		return fn()
	}
	return u.Mutate(fn)
}

// claim takes the claim of the model for this process (gallery.TryClaimModel),
// shared with the installs, which the upgrader does not run.
func (u *Upgrader) claim(name string) bool {
	release, ok := gallery.TryClaimModel(name)
	if !ok {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.releases == nil {
		u.releases = map[string]func(){}
	}
	u.releases[name] = release
	return true
}

func (u *Upgrader) release(name string) {
	u.mu.Lock()
	release := u.releases[name]
	delete(u.releases, name)
	u.mu.Unlock()
	if release != nil {
		release()
	}
}

// writeSidecar writes the gallery file of a model under the short lock that
// a config edit takes to record its hash.
func (u *Upgrader) writeSidecar(name string, sc *gallery.ModelConfig) error {
	defer gallery.LockSidecar(name)()
	return gallery.WriteSidecar(u.modelsPath(), name, sc)
}

// acquire takes the process lock and the cluster-wide lock of the model, or
// returns ErrBusy when another change of the model holds either.
func (u *Upgrader) acquire(ctx context.Context, name string) (func(), error) {
	if !u.claim(name) {
		return nil, ErrBusy
	}
	if u.Lock == nil {
		return func() { u.release(name) }, nil
	}
	unlock, err := u.Lock(ctx, name)
	if err != nil {
		u.release(name)
		if errors.Is(err, gallery.ErrModelLocked) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("take the lock of the model: %w", err)
	}
	return func() {
		unlock()
		u.release(name)
	}, nil
}

// upgradeRun is one upgrade: the gallery file in memory and its journal.
// Every phase change goes through advance, which writes the gallery file.
type upgradeRun struct {
	u    *Upgrader
	name string
	sc   *gallery.ModelConfig
	// next is the gallery file of the new version, without its journal. It
	// replaces sc at the end of the commit. Nil in a restart recovery.
	next *gallery.ModelConfig
	mu   sync.Mutex
	req  Request
	// blocked is true while the loads of the model are refused (LoadGate).
	blocked bool
	// planConfigSHA256 is the hash of the model config the upgrade merged
	// its new config from. A different config at the switch is a newer edit.
	planConfigSHA256 string
	// keptConfig is set by a rollback that left a config edited after the
	// switch in place instead of the old config.
	keptConfig bool
	// prevJournal is the journal of the last change before this upgrade.
	// A cancel before the stop puts it back: nothing changed, so nothing
	// failed.
	prevJournal *gallery.Journal
	// cancelled is set when the upgrade ended on a cancel or a pause. The
	// operation, not the upgrade, tells it (galleryop.OpCancelled).
	cancelled bool
}

func (r *upgradeRun) j() *gallery.Journal { return r.sc.Upgrade }

func (r *upgradeRun) report(p Progress) {
	if r.req.Progress != nil {
		r.req.Progress(p)
	}
}

// advance moves the journal to the next phase and writes the gallery file.
// It tells the progress callback of the new phase after it released the lock
// of the run: the callback is the caller's and may take its own locks.
func (r *upgradeRun) advance(ev Event, change func(j *gallery.Journal)) error {
	p, err := r.advanceLocked(ev, change)
	if err != nil {
		return err
	}
	r.report(p)
	return nil
}

func (r *upgradeRun) advanceLocked(ev Event, change func(j *gallery.Journal)) (Progress, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next, err := Transition(r.j().Phase, ev)
	if err != nil {
		return Progress{}, err
	}
	// A failed write must leave the journal in memory as it was on disk:
	// the failure path then moves from the phase that really was written
	// (to failed) and not from one that never was (to rolling-back, a phase
	// nothing leaves until a restart).
	oldSC, oldJournal := r.sc, *r.j()
	var oldNextJournal *gallery.Journal
	if r.next != nil {
		oldNextJournal = r.next.Upgrade
	}
	r.j().Phase = next
	r.j().UpdatedAt = r.u.now()
	if change != nil {
		change(r.j())
	}
	// The journal error is shown by the API and the notifications: no
	// absolute path of the models directory in it.
	r.j().Error = r.u.relText(r.j().Error)
	if err := r.u.writeSidecar(r.name, r.sc); err != nil {
		*oldSC.Upgrade = oldJournal
		r.sc = oldSC
		if r.next != nil {
			r.next.Upgrade = oldNextJournal
		}
		return Progress{}, fmt.Errorf("write the upgrade journal: %w", err)
	}
	msg := phaseMessage(next)
	if note := r.j().Note; note != "" && !r.j().Terminal() {
		msg += " (" + note + ")"
	}
	return Progress{Phase: next, Message: msg}, nil
}

// write writes the gallery file as it is in memory. A journal that ended is
// not touched again.
func (r *upgradeRun) write() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.j() == nil || r.j().Terminal() {
		return nil
	}
	r.j().UpdatedAt = r.u.now()
	return r.u.writeSidecar(r.name, r.sc)
}

func phaseMessage(phase string) string {
	switch phase {
	case gallery.PhasePreflight:
		return "checking that the upgrade can run"
	case gallery.PhaseStaging:
		return "downloading the new files beside the old ones"
	case gallery.PhaseUnloading:
		return "stopping the model"
	case gallery.PhaseCommitting:
		return "switching to the new version"
	case gallery.PhaseLoading:
		return "loading the new version"
	case gallery.PhaseConfirmed:
		return "completed"
	case gallery.PhaseRollingBack:
		return "putting the old version back"
	case gallery.PhaseRolledBack:
		return "the upgrade failed; the old version was put back"
	case gallery.PhaseFailed:
		return "the upgrade failed"
	}
	return phase
}

// heartbeat touches the journal until stop is closed.
func (r *upgradeRun) heartbeat(stop <-chan struct{}) {
	every := r.u.Heartbeat
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if err := r.write(); err != nil {
				xlog.Warn("Could not touch the upgrade journal", "model", r.name, "error", err)
			}
		}
	}
}

// Upgrade upgrades a model to what its gallery entry installs now. It returns
// when the upgrade is confirmed, failed or rolled back; the error says why it
// did not complete. Errors returned before anything was written
// (ErrNotUpgradable, ErrBusy, ErrUpToDate, ErrNothingFailed, *ErrRefused and
// *ErrNeedsChoices) leave no journal behind.
//
// The upgrader is the only writer of the notification of a completed or failed
// upgrade, a refusal before the journal included. A cancel or a pause is told
// by the operation.
func (u *Upgrader) Upgrade(ctx context.Context, req Request) error {
	started, err := u.upgrade(ctx, req)
	err = u.relErr(err)
	if err != nil && !started {
		u.notifyRefused(ctx, req, err)
	}
	return err
}

// upgrade runs the upgrade. started is true once the journal exists; from
// then on upgrade has told how it ended (see notify).
func (u *Upgrader) upgrade(ctx context.Context, req Request) (started bool, err error) {
	name := req.Name
	if err := utils.VerifyPath(name+".yaml", u.modelsPath()); err != nil {
		return false, ErrNotFound
	}
	unlock, err := u.acquire(ctx, name)
	if err != nil {
		return false, err
	}
	defer unlock()

	sc, err := u.preconditions(name, req.Retry)
	if err != nil {
		return false, err
	}
	prevJournal := sc.Upgrade
	attempts := 1
	if req.Retry {
		attempts = prevJournal.Attempts + 1
		if req.Choices == nil {
			req.Choices = prevJournal.Choices
		}
		if !req.ConfigOnly {
			req.ConfigOnly = prevJournal.ConfigOnly
		}
	}

	// Fresh, not the per-process index cache: in a cluster the check that
	// found the update may have run on another frontend, and a cached index
	// here would answer "up to date".
	models, err := u.ListGallery(ctx, true)
	if err != nil {
		return false, fmt.Errorf("read the galleries: %w", err)
	}
	// The plan leaves the artifact of an entry unbound: binding it downloads
	// the artifact, and that belongs to the staging phase, after the
	// preflight and the journal, not to the plan.
	planOptions := append(slices.Clone(u.InstallOptions), gallery.WithoutArtifactBinding())
	inst, plan, err := PlanUpgrade(ctx, u.SystemState, models, name, sc, planOptions...)
	if err != nil {
		if errors.Is(err, ErrGone) {
			return false, &ErrRefused{Reason: err.Error()}
		}
		return false, err
	}
	if inst.Origin.EntryDigest == sc.Origin.EntryDigest && !req.Retry {
		return false, ErrUpToDate
	}

	configPath := filepath.Join(u.modelsPath(), name+".yaml")
	disk, err := u.fs().ReadFile(configPath)
	if err != nil {
		return false, fmt.Errorf("read the model config: %w", err)
	}
	// finalConfig merges the new gallery config with the config on disk.
	finalConfig := func() ([]byte, error) {
		if !plan.WriteConfig {
			return disk, nil
		}
		if sc.Origin.ConfigYAML != "" {
			return MergeConfig([]byte(sc.Origin.ConfigYAML), disk, plan.ConfigYAML, req.Choices)
		}
		// No record of what the install wrote, so an edit cannot be told
		// from what the old version wrote: the new config is the gallery's,
		// and only the operator state (pinned, disabled) carries over. The
		// disk config as "installed" makes no key an edit.
		return MergeConfig(disk, disk, plan.ConfigYAML, nil)
	}
	finalYAML, err := finalConfig()
	if err != nil {
		return false, err
	}

	staged, err := u.stageList(sc, plan.Gallery)
	if err != nil {
		return false, err
	}
	if req.ConfigOnly {
		var needs []string
		for _, f := range staged {
			if f.Next != "" && f.Role != gallery.RoleTemplate {
				needs = append(needs, f.Path)
			}
		}
		if len(needs) > 0 {
			return false, &ErrRefused{Reason: "a config-only upgrade cannot apply this update: it needs new files (" + strings.Join(needs, ", ") + ")"}
		}
	}

	to := &gallery.Previous{Origin: inst.Origin, ConfigSHA256: gallery.SHA256Hex(finalYAML), Managed: gallery.ManagedFiles(plan.Gallery)}
	to.Origin.InstalledAt = u.now()
	if plan.WriteConfig {
		// What the gallery writes, without the person's edits: the next
		// check compares the config on disk with it, so the kept edits
		// still read as edits.
		to.Origin.ConfigYAML = string(plan.ConfigYAML)
	}
	oldSidecar := *sc
	oldSidecar.Upgrade = nil
	oldSidecarYAML, err := yaml.Marshal(&oldSidecar)
	if err != nil {
		return false, err
	}
	previous := &gallery.Previous{
		Origin: sc.Origin, ConfigSHA256: sc.ConfigSHA256, Managed: sc.Managed,
		ConfigYAML: base64.StdEncoding.EncodeToString(disk), HadConfig: true,
		Sidecar: base64.StdEncoding.EncodeToString(oldSidecarYAML), HadSidecar: true,
	}
	// The gallery file of the new version: the new gallery config with the
	// version 2 fields set here, never taken from a base config.
	next := *plan.Gallery
	next.Version, next.Origin, next.ConfigSHA256, next.Managed, next.Hold = gallery.SidecarVersion, to.Origin, to.ConfigSHA256, to.Managed, sc.Hold
	run := &upgradeRun{u: u, name: name, sc: sc, req: req, next: &next, planConfigSHA256: gallery.SHA256Hex(disk), prevJournal: prevJournal}
	var carried []string
	if prevJournal != nil {
		// Old files of an earlier upgrade that nobody removed yet stay
		// listed, or they would be forgotten on disk.
		carried = slices.Clone(prevJournal.RetainedFiles)
	}
	now := u.now()
	sc.Upgrade = &gallery.Journal{
		Kind: gallery.JournalUpgrade, Phase: "", OpID: req.OpID, Instance: u.Instance,
		StartedAt: now, FromEntryDigest: sc.Origin.EntryDigest, ToEntryDigest: inst.Origin.EntryDigest,
		ToConfigSHA256: to.ConfigSHA256, ConfigOnly: req.ConfigOnly, Choices: req.Choices,
		Files: staged, To: to, Previous: previous, Attempts: attempts, RetainedFiles: carried,
	}
	if err := run.advance(EvStart, nil); err != nil {
		return false, err
	}
	stop := make(chan struct{})
	go run.heartbeat(stop)
	defer close(stop)

	// bind materializes the artifact during the staging phase. The resolved
	// spec changes the config the upgrade writes, so the journal and the
	// gallery file take the new hash.
	bind := func(ctx context.Context) ([]byte, error) {
		applied, err := gallery.BindPlanArtifact(ctx, u.SystemState, plan, u.InstallOptions...)
		if err != nil || !applied {
			return nil, err
		}
		bound, err := finalConfig()
		if err != nil {
			return nil, err
		}
		sum := gallery.SHA256Hex(bound)
		to.ConfigSHA256, to.Origin.ConfigYAML = sum, string(plan.ConfigYAML)
		next.ConfigSHA256, next.Origin.ConfigYAML = sum, string(plan.ConfigYAML)
		return bound, nil
	}
	err = u.drive(ctx, run, plan.Model.Backend, finalYAML, bind)
	u.notify(context.WithoutCancel(ctx), run, err)
	u.changed(name)
	return true, err
}

// relText removes the models directory from a message, so that an error
// shown by the API or a notification (a browser push included) names the
// files relative to it.
func (u *Upgrader) relText(msg string) string {
	if msg == "" || u.SystemState == nil {
		return msg
	}
	dir := filepath.Clean(u.modelsPath())
	msg = strings.ReplaceAll(msg, dir+string(filepath.Separator), "")
	return strings.ReplaceAll(msg, dir, "the models directory")
}

// pathlessError is an error whose message has no models directory path. It
// unwraps to the original, so errors.Is and errors.As still see it.
type pathlessError struct {
	err error
	msg string
}

func (e *pathlessError) Error() string { return e.msg }
func (e *pathlessError) Unwrap() error { return e.err }

// relErr is err with relText applied to its message.
func (u *Upgrader) relErr(err error) error {
	if err == nil {
		return nil
	}
	msg := u.relText(err.Error())
	if msg == err.Error() {
		return err
	}
	return &pathlessError{err: err, msg: msg}
}

// stageList works out the files the upgrade downloads, writes or retires.
func (u *Upgrader) stageList(sc *gallery.ModelConfig, cfg *gallery.ModelConfig) ([]gallery.StagedFile, error) {
	old := managedIndex(sc.Managed)
	suffix := ".prev-" + digest8(sc.Origin.EntryDigest)
	seen := map[string]bool{}
	var out []gallery.StagedFile
	var noChecksum []string
	for _, f := range cfg.Files {
		if err := utils.VerifyPath(f.Filename, u.modelsPath()); err != nil {
			return nil, &ErrRefused{Reason: fmt.Sprintf("the file %q is outside the models directory", f.Filename)}
		}
		seen[f.Filename] = true
		prev, known := old[f.Filename]
		live := filepath.Join(u.modelsPath(), f.Filename)
		if known && sameManaged(prev, gallery.ManagedFileOf(f)) && u.fs().Exists(live) {
			continue
		}
		if !known && f.SHA256 == "" && u.fs().Exists(live) {
			// Present and not verifiable: the install would keep it too.
			continue
		}
		role := gallery.FileRole(f.Filename)
		if f.SHA256 == "" && role != gallery.RoleTemplate && role != gallery.RoleArtifact {
			noChecksum = append(noChecksum, f.Filename)
			continue
		}
		s := gallery.StagedFile{Path: f.Filename, Next: f.Filename + ".next", SHA256: f.SHA256, URI: f.URI, Role: role}
		if u.fs().Exists(live) {
			s.Prev = f.Filename + suffix
		}
		out = append(out, s)
	}
	if len(noChecksum) > 0 {
		return nil, &ErrRefused{Reason: "the gallery lists new weights without a sha256, which cannot be verified: " + strings.Join(noChecksum, ", ")}
	}
	for _, t := range cfg.PromptTemplates {
		path := t.Name + ".tmpl"
		if err := utils.VerifyPath(path, u.modelsPath()); err != nil {
			return nil, &ErrRefused{Reason: fmt.Sprintf("the template %q is outside the models directory", path)}
		}
		seen[path] = true
		cur := gallery.ManagedTemplateOf(t)
		sum := cur.SHA256
		if prev, ok := old[path]; ok && sameManaged(prev, cur) && u.fs().Exists(filepath.Join(u.modelsPath(), path)) {
			continue
		}
		s := gallery.StagedFile{Path: path, Next: path + ".next", SHA256: sum, Role: gallery.RoleTemplate, Content: t.Content}
		if u.fs().Exists(filepath.Join(u.modelsPath(), path)) {
			s.Prev = path + suffix
		}
		out = append(out, s)
	}
	for _, m := range droppedManaged(sc.Managed, seen) {
		if !u.fs().Exists(filepath.Join(u.modelsPath(), m.Path)) {
			continue
		}
		out = append(out, gallery.StagedFile{Path: m.Path, Prev: m.Path + suffix, Role: m.Role, Retire: true})
	}
	return out, nil
}

func digest8(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	if len(hex) >= 8 {
		return hex[:8]
	}
	return "old"
}

func (u *Upgrader) path(rel string) string { return filepath.Join(u.modelsPath(), rel) }

// drive runs the phases from preflight to confirmed, or to the rollback.
func (u *Upgrader) drive(ctx context.Context, r *upgradeRun, backend string, finalYAML []byte, bind func(context.Context) ([]byte, error)) error {
	// preflight
	if err := u.preflight(ctx, r, backend); err != nil {
		return u.failBeforeCommit(ctx, r, err)
	}
	if err := r.advance(EvOK, nil); err != nil { // -> staging
		return u.failBeforeCommit(ctx, r, err)
	}
	if err := u.stage(ctx, r); err != nil {
		return u.failBeforeCommit(ctx, r, err)
	}
	if bind != nil {
		bound, err := bind(ctx)
		if err != nil {
			return u.failBeforeCommit(ctx, r, err)
		}
		if bound != nil {
			finalYAML = bound
		}
	}
	wasLoaded := u.Runtime != nil && u.Runtime.IsLoaded(ctx, r.name)
	note := ""
	if u.StrategyNote != nil {
		if note = u.StrategyNote(ctx, r.name); note != "" {
			xlog.Info("Model upgrade strategy", "model", r.name, "note", note)
		}
	}
	// The stop is the point of no return: from here on the upgrade ends
	// confirmed or rolled back, never half done. The status says first that
	// it can no longer be cancelled, so a cancel that arrives after this
	// report is refused; one that arrived before it ends the upgrade now,
	// with nothing changed. The rest runs on a context a cancel does not
	// reach, so the rollback cannot fail on it.
	r.report(Progress{Phase: gallery.PhaseUnloading, Message: phaseMessage(gallery.PhaseUnloading)})
	if err := ctx.Err(); err != nil {
		return u.failBeforeCommit(ctx, r, err)
	}
	// A config edited during the download ends the upgrade before the model
	// stops; the commit checks again for an edit in the short window after.
	if err := u.checkConfigUnchanged(r); err != nil {
		return u.failBeforeCommit(ctx, r, err)
	}
	ctx = context.WithoutCancel(ctx)
	if err := r.advance(EvOK, func(j *gallery.Journal) { // -> unloading
		j.WasLoaded, j.Note = wasLoaded, note
		j.ToConfigSHA256 = gallery.SHA256Hex(finalYAML)
	}); err != nil {
		return u.failBeforeCommit(ctx, r, err)
	}
	// From here on the loads of the model are refused until the new config
	// is read, and whatever path the upgrade takes it lets them through again.
	defer func() { _ = u.unblockLoads(context.WithoutCancel(ctx), r) }()
	if err := u.blockLoads(ctx, r); err != nil {
		return u.rollback(ctx, r, fmt.Errorf("stop the loads of the model: %w", err))
	}
	if r.j().WasLoaded {
		if err := u.Runtime.Unload(ctx, r.name); err != nil {
			return u.rollback(ctx, r, fmt.Errorf("stop the model: %w", err))
		}
	}
	if err := r.advance(EvOK, nil); err != nil { // -> committing
		return u.rollback(ctx, r, err)
	}
	if err := u.mutate(func() error { return u.commit(r, finalYAML) }); err != nil {
		return u.rollback(ctx, r, err)
	}
	if u.Workers != nil {
		if files := workerCommitList(r.j()); len(files) > 0 {
			if err := u.Workers.Commit(ctx, r.name, files); err != nil {
				return u.rollback(ctx, r, fmt.Errorf("place the new files on the workers: %w", err))
			}
		}
	}
	if err := r.advance(EvOK, func(j *gallery.Journal) { // -> loading
		r.next.Upgrade = j
		r.sc = r.next
	}); err != nil {
		return u.rollback(ctx, r, err)
	}
	if u.Runtime != nil {
		if err := u.Runtime.Reload(ctx, r.name); err != nil {
			return u.rollback(ctx, r, fmt.Errorf("read the new config: %w", err))
		}
		// Whatever runs now started before the files changed: a load that
		// was already in flight when the loads were blocked, or one a
		// runtime without a LoadGate let through. The config revision does
		// not tell (a change of the weights alone keeps it), so stop it
		// without asking: the load check below must start the new files.
		if err := u.stopIfLoaded(ctx, r.name); err != nil {
			return u.rollback(ctx, r, fmt.Errorf("stop the old version: %w", err))
		}
		if err := u.unblockLoads(ctx, r); err != nil {
			return u.rollback(ctx, r, fmt.Errorf("allow the loads of the model again: %w", err))
		}
		if err := u.Runtime.Load(ctx, r.name); err != nil {
			return u.rollback(ctx, r, fmt.Errorf("load the new version: %w", err))
		}
		if !r.j().WasLoaded {
			// The check loaded a model nobody had loaded: leave it as it was.
			if err := u.Runtime.Unload(ctx, r.name); err != nil {
				xlog.Warn("Could not unload a model after its load check", "model", r.name, "error", err)
			}
		}
	}
	var retained []string
	for _, f := range r.j().Files {
		if f.Prev != "" && u.fs().Exists(u.path(f.Prev)) {
			retained = append(retained, f.Prev)
		}
	}
	u.syncConfigHash(r)
	return r.advance(EvOK, func(j *gallery.Journal) { // -> confirmed
		j.RetainedFiles = append(j.RetainedFiles, retained...)
		j.Error = ""
		j.Previous, j.To, j.Files = nil, nil, nil
	})
}

// blockLoads refuses the loads of the model while its files change, when the
// runtime can (LoadGate).
func (u *Upgrader) blockLoads(ctx context.Context, r *upgradeRun) error {
	gate, ok := u.Runtime.(LoadGate)
	if !ok || r.blocked {
		return nil
	}
	r.blocked = true
	return gate.BlockLoads(ctx, r.name)
}

// unblockLoads ends blockLoads. It does nothing once the loads are allowed
// again. When the gate fails the run stays blocked, so the next call (the
// deferred one at the end of the upgrade, or the next upgrade) tries again.
func (u *Upgrader) unblockLoads(ctx context.Context, r *upgradeRun) error {
	gate, ok := u.Runtime.(LoadGate)
	if !ok || !r.blocked {
		return nil
	}
	if err := gate.UnblockLoads(ctx, r.name); err != nil {
		xlog.Warn("Could not allow the loads of a model again", "model", r.name, "error", err)
		return err
	}
	r.blocked = false
	return nil
}

// stopIfLoaded stops every instance of the model that runs.
func (u *Upgrader) stopIfLoaded(ctx context.Context, name string) error {
	if !u.Runtime.IsLoaded(ctx, name) {
		return nil
	}
	return u.Runtime.Unload(ctx, name)
}

func (u *Upgrader) preflight(ctx context.Context, r *upgradeRun, backend string) error {
	if backend != "" && u.BackendAvailable != nil {
		ok, err := u.BackendAvailable(backend)
		if err != nil {
			return fmt.Errorf("check the backend %q: %w", backend, err)
		}
		if !ok {
			return &ErrRefused{Reason: fmt.Sprintf("the new version needs the backend %q, which is not installed", backend)}
		}
	}
	inUse := gallery.FilesInUse(u.SystemState, r.name)
	var shared []string
	for _, f := range r.j().Files {
		if users := inUse[f.Path]; len(users) > 0 {
			shared = append(shared, fmt.Sprintf("%s (used by %s)", f.Path, strings.Join(users, ", ")))
		}
	}
	if len(shared) > 0 {
		return &ErrRefused{Reason: "the upgrade would replace files that other models use: " + strings.Join(shared, "; ")}
	}
	var tasks []downloader.FileTask
	for _, f := range r.j().Files {
		if f.Next == "" || f.Content != "" {
			continue
		}
		tasks = append(tasks, downloader.FileTask{URI: downloader.URI(f.URI)})
	}
	// An unknown size counts as zero: the check refuses only when the known
	// sizes already exceed the free space.
	total, _ := downloader.TotalSizeFunc(ctx, tasks, func(ctx context.Context, uri downloader.URI) (int64, error) {
		return u.contentLength(ctx, string(uri)), nil
	})
	var need uint64
	if total > 0 {
		need = uint64(total)
	}
	if need > 0 {
		free, err := u.freeSpace(u.modelsPath())
		if err == nil && free < need+need/20 {
			return &ErrRefused{Reason: fmt.Sprintf("not enough free disk space: the new files need %d bytes and %d are free; the old files stay until you remove them", need, free)}
		}
	}
	return nil
}

func (u *Upgrader) contentLength(ctx context.Context, uri string) int64 {
	if u.ContentLength != nil {
		return u.ContentLength(ctx, uri)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	n, err := downloader.URI(uri).ContentLength(cctx)
	if err != nil {
		return 0
	}
	return n
}

func (u *Upgrader) freeSpace(path string) (uint64, error) {
	if u.FreeSpace != nil {
		return u.FreeSpace(path)
	}
	info, err := xsysinfo.GetDiskInfo(path)
	if err != nil {
		return 0, err
	}
	return info.Available, nil
}

// stage downloads the new files to their .next names and verifies them, and
// writes the new prompt templates to theirs.
func (u *Upgrader) stage(ctx context.Context, r *upgradeRun) error {
	var tasks []downloader.FileTask
	for _, f := range r.j().Files {
		if f.Next == "" {
			continue
		}
		if f.Role == gallery.RoleTemplate && f.URI == "" {
			if err := u.fs().WriteFileAtomic(u.path(f.Next), []byte(f.Content)); err != nil {
				return fmt.Errorf("write the template %s: %w", f.Path, err)
			}
			continue
		}
		tasks = append(tasks, downloader.FileTask{URI: downloader.URI(f.URI), Destination: u.path(f.Next), SHA256: f.SHA256})
	}
	for i := range tasks {
		tasks[i].FileIndex, tasks[i].TotalFiles = i, len(tasks)
	}
	if len(tasks) == 0 {
		return nil
	}
	download := u.Download
	if download == nil {
		download = func(ctx context.Context, tasks []downloader.FileTask, status func(string, string, string, float64)) error {
			return downloader.DownloadFilesWithContext(ctx, tasks, status)
		}
	}
	// The workers download their own copies while the frontend downloads
	// its own: the frontend copy is the source of a load on a worker that
	// could not.
	var workers sync.WaitGroup
	if u.Workers != nil {
		if files := workerStageList(r.j()); len(files) > 0 {
			workers.Go(func() { u.Workers.Stage(ctx, r.name, files) })
		}
	}
	err := download(ctx, tasks, func(file, current, total string, percent float64) {
		r.report(Progress{Phase: gallery.PhaseStaging, Message: phaseMessage(gallery.PhaseStaging), FileName: file, Current: current, Total: total, Percent: percent})
	})
	workers.Wait()
	return err
}

// workerStageList is what the workers download: the new files that come
// from a URI. Templates stay on the frontend, which renders them.
func workerStageList(j *gallery.Journal) []WorkerFile {
	var out []WorkerFile
	for _, f := range j.Files {
		if f.Next != "" && f.URI != "" && f.SHA256 != "" {
			out = append(out, WorkerFile{Path: f.Path, URI: f.URI, SHA256: f.SHA256})
		}
	}
	return out
}

// workerCommitList is every file whose copy on a worker the upgrade changes:
// the downloaded files and the files the new version no longer has.
func workerCommitList(j *gallery.Journal) []WorkerFile {
	var out []WorkerFile
	for _, f := range j.Files {
		switch {
		case f.Next != "" && f.URI != "":
			out = append(out, WorkerFile{Path: f.Path, URI: f.URI, SHA256: f.SHA256})
		case f.Retire:
			out = append(out, WorkerFile{Path: f.Path})
		}
	}
	return out
}

// discardOnWorkers drops what the upgrade put on the workers.
func (u *Upgrader) discardOnWorkers(ctx context.Context, r *upgradeRun, evict bool) {
	if u.Workers == nil || r.j() == nil {
		return
	}
	if files := workerCommitList(r.j()); len(files) > 0 {
		u.Workers.Discard(ctx, r.name, files, evict)
	}
}

// commit swaps the files and writes the new config. Every step can run again
// after a crash: a rename whose source is gone was done.
func (u *Upgrader) commit(r *upgradeRun, finalYAML []byte) error {
	// Under the config mutation lock: an edit in this process cannot land
	// between this check and the write below.
	if err := u.checkConfigUnchanged(r); err != nil {
		return err
	}
	fs := u.fs()
	for _, f := range r.j().Files {
		if f.Prev == "" {
			continue
		}
		if fs.Exists(u.path(f.Path)) && !fs.Exists(u.path(f.Prev)) {
			if err := fs.Rename(u.path(f.Path), u.path(f.Prev)); err != nil {
				return fmt.Errorf("keep the old %s: %w", f.Path, err)
			}
		}
	}
	for _, f := range r.j().Files {
		if f.Next == "" || !fs.Exists(u.path(f.Next)) {
			continue
		}
		if err := fs.Rename(u.path(f.Next), u.path(f.Path)); err != nil {
			return fmt.Errorf("put the new %s in place: %w", f.Path, err)
		}
	}
	if err := fs.WriteFileAtomic(u.path(r.name+".yaml"), finalYAML); err != nil {
		return fmt.Errorf("write the new config: %w", err)
	}
	return nil
}

// keptConfigNote is added to the error of a rollback that left in place a
// config edited after the switch.
const keptConfigNote = "the model config was edited after the switch to the new version; the rollback kept that edit and put back the old files"

// checkConfigUnchanged returns ErrConfigChanged when the model config on disk
// is not the one the upgrade merged its new config from.
func (u *Upgrader) checkConfigUnchanged(r *upgradeRun) error {
	if r.planConfigSHA256 == "" {
		return nil
	}
	data, err := u.fs().ReadFile(u.path(r.name + ".yaml"))
	if err != nil {
		return fmt.Errorf("read the model config: %w", err)
	}
	if gallery.SHA256Hex(data) != r.planConfigSHA256 {
		return ErrConfigChanged
	}
	return nil
}

// syncConfigHash records in the gallery file the hash of the config now on
// disk. An edit during the upgrade recorded its own hash, which the journal
// writes of the upgrade then replaced from memory.
func (u *Upgrader) syncConfigHash(r *upgradeRun) {
	data, err := u.fs().ReadFile(u.path(r.name + ".yaml"))
	if err != nil {
		return
	}
	r.mu.Lock()
	r.sc.ConfigSHA256 = gallery.SHA256Hex(data)
	r.mu.Unlock()
}

// failBeforeCommit ends an upgrade that changed nothing: it removes the
// staged files and records the failure.
func (u *Upgrader) failBeforeCommit(ctx context.Context, r *upgradeRun, cause error) error {
	// A pause (or a server shutdown) keeps the partial downloads, so the next
	// upgrade of the same version resumes them; a cancel removes them.
	u.removeStaged(r, ctx.Err() != nil && !errors.Is(context.Cause(ctx), downloader.ErrUserCancelled))
	u.discardOnWorkers(context.WithoutCancel(ctx), r, false)
	u.syncConfigHash(r)
	if ctx.Err() != nil {
		// Cancelled or paused before the stop: nothing changed, so the model
		// gets back the journal it had and is not "failed". The operation
		// tells the cancel.
		r.mu.Lock()
		r.cancelled = true
		r.sc.Upgrade = r.prevJournal
		err := r.u.writeSidecar(r.name, r.sc)
		r.mu.Unlock()
		if err != nil {
			return errors.Join(ctx.Err(), err)
		}
		return ctx.Err()
	}
	if err := r.advance(EvFailed, func(j *gallery.Journal) {
		j.Error = cause.Error()
		j.Previous, j.To = nil, nil
	}); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// removeStaged removes the files the staging phase left. keepPartial is for a
// pause or a server shutdown: the next upgrade of the same version resumes
// from what is there, so it keeps the partial downloads and the finished
// files that carry a checksum, which the downloader verifies again before it
// reuses them. A file without a checksum could not be verified and goes.
func (u *Upgrader) removeStaged(r *upgradeRun, keepPartial bool) {
	for _, f := range r.j().Files {
		if f.Next == "" {
			continue
		}
		var paths []string
		if !keepPartial || f.SHA256 == "" {
			paths = append(paths, u.path(f.Next))
		}
		if !keepPartial {
			paths = append(paths, u.path(f.Next)+".partial")
		}
		for _, p := range paths {
			if err := u.fs().Remove(p); err != nil {
				xlog.Warn("Could not remove a staged file", "file", p, "error", err)
			}
		}
	}
}

// rollback puts the old version back: config, files and gallery file, and
// loads the old model again when it was loaded. It moves the journal to
// rolled-back, or to failed when the rollback itself could not complete.
func (u *Upgrader) rollback(ctx context.Context, r *upgradeRun, cause error) error {
	xlog.Warn("Model upgrade failed, putting the old version back", "model", r.name, "error", cause)
	if r.j().Phase != gallery.PhaseRollingBack {
		if err := r.advance(EvFailed, func(j *gallery.Journal) { j.Error = cause.Error() }); err != nil {
			return errors.Join(cause, err)
		}
	}
	rbErr, err := u.finishRollback(ctx, r, cause, "")
	if rbErr != nil {
		return errors.Join(cause, rbErr, err)
	}
	if err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// finishRollback undoes the commit (restore) and ends the journal: rolled-back
// when the undo worked, failed when it did not. The error of a rolled-back
// journal is cause, then suffix when it is not empty, then the note about a
// config edit that the rollback kept. restoreErr is the error of the undo;
// advanceErr is the error of the journal write.
func (u *Upgrader) finishRollback(ctx context.Context, r *upgradeRun, cause error, suffix string) (restoreErr, advanceErr error) {
	if restoreErr = u.restore(ctx, r); restoreErr != nil {
		advanceErr = r.advance(EvFailed, func(j *gallery.Journal) {
			j.Error = cause.Error() + "; the rollback did not complete: " + restoreErr.Error()
		})
		return restoreErr, advanceErr
	}
	advanceErr = r.advance(EvOK, func(j *gallery.Journal) { // -> rolled-back
		j.Error = cause.Error()
		if suffix != "" {
			j.Error += "; " + suffix
		}
		if r.keptConfig {
			j.Error += "; " + keptConfigNote
		}
		j.Previous, j.To, j.Files = nil, nil, nil
	})
	return nil, advanceErr
}

// restore is the undo of the commit. It can run again after a crash.
func (u *Upgrader) restore(ctx context.Context, r *upgradeRun) error {
	j := r.j()
	if u.Runtime != nil {
		if err := u.blockLoads(ctx, r); err != nil {
			xlog.Warn("Could not stop the loads of a model during a rollback", "model", r.name, "error", err)
		}
		defer func() { _ = u.unblockLoads(context.WithoutCancel(ctx), r) }()
		if u.Runtime.IsLoaded(ctx, r.name) {
			if err := u.Runtime.Unload(ctx, r.name); err != nil {
				xlog.Warn("Could not stop the new version during a rollback", "model", r.name, "error", err)
			}
		}
	}
	var errs []error
	err := u.mutate(func() error {
		fs := u.fs()
		if prev := j.Previous; prev != nil && prev.HadConfig {
			// The rule for a config edited during the upgrade: the old config
			// goes back only over the config the commit wrote. Any other
			// config on disk is the old one (the commit did not reach it) or
			// an edit made after the switch, which a rollback must not undo;
			// the journal says it was kept.
			current, readErr := fs.ReadFile(u.path(r.name + ".yaml"))
			if readErr == nil && j.ToConfigSHA256 != "" && gallery.SHA256Hex(current) != j.ToConfigSHA256 {
				old, _ := base64.StdEncoding.DecodeString(prev.ConfigYAML)
				r.keptConfig = gallery.SHA256Hex(current) != gallery.SHA256Hex(old)
			} else {
				data, err := base64.StdEncoding.DecodeString(prev.ConfigYAML)
				if err != nil {
					return fmt.Errorf("decode the old config: %w", err)
				}
				if err := fs.WriteFileAtomic(u.path(r.name+".yaml"), data); err != nil {
					return fmt.Errorf("restore the old config: %w", err)
				}
			}
		}
		for _, f := range j.Files {
			switch {
			case f.Prev != "" && fs.Exists(u.path(f.Prev)):
				if err := fs.Remove(u.path(f.Path)); err != nil {
					errs = append(errs, err)
					continue
				}
				if err := fs.Rename(u.path(f.Prev), u.path(f.Path)); err != nil {
					errs = append(errs, fmt.Errorf("restore the old %s: %w", f.Path, err))
				}
			case f.Prev == "" && !f.Retire && f.Next != "" && !fs.Exists(u.path(f.Next)):
				// A file the new version added and the commit put in place.
				if err := fs.Remove(u.path(f.Path)); err != nil {
					errs = append(errs, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	u.removeStaged(r, false)
	// The workers may hold the new files in place: drop them, so the load of
	// the old version below sends the old ones.
	u.discardOnWorkers(ctx, r, true)
	if prev := j.Previous; prev != nil && prev.HadSidecar {
		data, err := base64.StdEncoding.DecodeString(prev.Sidecar)
		var old gallery.ModelConfig
		if err == nil {
			err = yaml.Unmarshal(data, &old)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("decode the old gallery file: %w", err))
		} else {
			r.mu.Lock()
			old.Upgrade, old.Hold = j, r.sc.Hold
			r.sc = &old
			r.mu.Unlock()
		}
	}
	u.syncConfigHash(r)
	if err := r.write(); err != nil {
		errs = append(errs, err)
	}
	if u.Runtime != nil {
		if err := u.Runtime.Reload(ctx, r.name); err != nil {
			errs = append(errs, fmt.Errorf("read the old config: %w", err))
		} else {
			if err := u.stopIfLoaded(ctx, r.name); err != nil {
				xlog.Warn("Could not stop the new version during a rollback", "model", r.name, "error", err)
			}
			if err := u.unblockLoads(ctx, r); err != nil {
				errs = append(errs, err)
			} else if j.WasLoaded {
				if err := u.Runtime.Load(ctx, r.name); err != nil {
					errs = append(errs, fmt.Errorf("load the old version: %w", err))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// notifyRefused tells that an upgrade ended before it had a journal: a
// refusal, an up-to-date model, a missing choice. A cancel is not told here.
func (u *Upgrader) notifyRefused(ctx context.Context, req Request, err error) {
	if u.Notifier == nil || req.OpID == "" || ctx.Err() != nil {
		return
	}
	res := UpgradeResult{OpID: req.OpID, Name: req.Name, Failed: true, Err: u.relText(err.Error())}
	if nerr := u.Notifier.ModelUpgradeResult(context.WithoutCancel(ctx), res); nerr != nil {
		xlog.Warn("Could not write the model upgrade notification", "model", req.Name, "error", nerr)
	}
}

func (u *Upgrader) notify(ctx context.Context, r *upgradeRun, err error) {
	if u.Notifier == nil || r.req.OpID == "" || r.cancelled {
		return
	}
	j := r.j()
	res := UpgradeResult{OpID: r.req.OpID, Name: r.name, From: j.FromEntryDigest, To: j.ToEntryDigest}
	if err != nil {
		res.Failed = true
		res.RolledBack = j.Phase == gallery.PhaseRolledBack
		res.Err = u.relText(err.Error())
	}
	if nerr := u.Notifier.ModelUpgradeResult(ctx, res); nerr != nil {
		xlog.Warn("Could not write the model upgrade notification", "model", r.name, "error", nerr)
	}
}

// Validate checks, without the galleries, that an upgrade (or a retry) of a
// model can be queued: the model is installed with a recorded origin, is not
// held, and no upgrade of it runs. The upgrade itself checks again.
func (u *Upgrader) Validate(name string, retry bool) error {
	_, err := u.preconditions(name, retry)
	return err
}

// ErrBadChoice is a choice that is neither ChoiceMine nor ChoiceGallery.
var ErrBadChoice = errors.New("a choice must be " + ChoiceMine + " or " + ChoiceGallery)

// CheckRequest refuses at once an upgrade or a retry that cannot run, before
// it is queued: a bad choice (ErrBadChoice), what Validate refuses, and an
// upgrade without a choice for an edited key the update changes
// (*ErrNeedsChoices, read from the last check of svc; nil svc skips it). The
// REST route and the assistant both call it.
func CheckRequest(up *Upgrader, svc *Service, name string, retry bool, choices map[string]string) error {
	for key, choice := range choices {
		if choice != ChoiceMine && choice != ChoiceGallery {
			return fmt.Errorf("%w (key %q)", ErrBadChoice, key)
		}
	}
	if err := up.Validate(name, retry); err != nil {
		return err
	}
	if !retry && svc != nil {
		if missing := svc.MissingChoices(name, choices); len(missing) > 0 {
			return &ErrNeedsChoices{Keys: missing}
		}
	}
	return nil
}
