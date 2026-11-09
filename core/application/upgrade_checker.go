package application

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/updatenotify"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// The schedule of the periodic upgrade checks. The backend check and the
// add-on check both follow it.
const (
	upgradeInitialDelay  = 10 * time.Second
	upgradeCheckInterval = 6 * time.Hour
)

// Variables so that tests can shorten them.
var (
	// checkNowTimeout bounds a check an administrator asked for. It is the
	// figure the add-on update check uses.
	checkNowTimeout = 45 * time.Second
	// checkNowCooldown is how long a finished check answers the next
	// check-now call, so a double click does not ask the registries twice.
	checkNowCooldown = 15 * time.Second
)

// UpgradeChecker periodically checks for backend upgrades and optionally
// auto-upgrades them. It caches the last check results for API queries.
//
// In standalone mode it runs a simple ticker loop; every check may
// auto-upgrade.
// In distributed mode every frontend detects upgrades to fill its own cache
// (first check, on-demand triggers), but only the holder of a PostgreSQL
// advisory lock auto-upgrades, once per interval. Several frontends starting
// the same install would race on the same workers.
type UpgradeChecker struct {
	appConfig   *config.ApplicationConfig
	modelLoader *model.ModelLoader
	galleries   []config.Gallery
	systemState *system.SystemState
	db          *gorm.DB // non-nil in distributed mode
	// backendManagerFn lazily returns the current backend manager (may be
	// swapped from Local to Distributed after startup). Pulled through each
	// check so the UpgradeChecker uses whichever is active. In distributed
	// mode this ensures CheckUpgrades asks workers instead of the (empty)
	// frontend filesystem — fixing the bug where upgrades never surfaced.
	backendManagerFn func() galleryop.BackendManager

	// libraryFn returns the installer of library backends, or nil when the
	// host has none. It is read on each check because the add-on owner starts
	// after the checker is built.
	libraryFn func() libraryUpgrader

	initialDelay  time.Duration
	checkInterval time.Duration
	stop          chan struct{}
	done          chan struct{}
	triggerCh     chan struct{}

	mu            sync.RWMutex
	lastUpgrades  map[string]gallery.UpgradeInfo
	lastCheckTime time.Time
	// lastCheckErr is the error of the last check, nil when it succeeded. A
	// failed check keeps the previous lastUpgrades, so this is the only sign
	// that the cached list may be old.
	lastCheckErr error
	// lastSuccess is the end of the last check that did not fail.
	lastSuccess time.Time

	// The library source has its own cache and clock: it is checked with the
	// gallery, but it can fail alone.
	libUpdates     []backendlib.Update
	libCheckedAt   time.Time
	libLastSuccess time.Time
	libErr         string

	// libraryRefreshFn refreshes the library index before a check an
	// administrator asked for. nil when this instance needs none: a frontend
	// asks the owner live at each check.
	libraryRefreshFn func(ctx context.Context) error
	// loadedModelsFn lists the models loaded on the nodes, to name the models
	// an update would stop. nil in standalone mode.
	loadedModelsFn func(ctx context.Context) ([]backendupdates.LoadedModel, error)

	// flight coalesces concurrent CheckNow calls into one check.
	flight singleflight.Group

	// announcer writes the inbox items of the update center. nil: none.
	announcer updateAnnouncer
	// failStreak counts, for each source, the automatic passes in a row that
	// ended with a failed check. It lives in this process: a new leader starts
	// again at zero, which only delays the notice.
	failStreak map[string]int
}

// checkFailedAfter is how many automatic passes in a row must fail the check of
// a source before the inbox says so. One failure is a network blip; the pass
// runs every six hours, so two in a row is more than half a day.
const checkFailedAfter = 2

// updateAnnouncer writes the inbox items of the update center. Only the
// process that runs the automatic pass may call it (see announce).
type updateAnnouncer interface {
	Digest(ctx context.Context, snap backendupdates.Snapshot) error
	CheckFailed(ctx context.Context, sources []string) error
	Result(ctx context.Context, r updatenotify.Result) error
}

// SetAnnouncer sets the writer of the inbox items. The pass that installs
// updates calls it at its end.
func (uc *UpgradeChecker) SetAnnouncer(a updateAnnouncer) { uc.announcer = a }

// NewUpgradeChecker creates a new UpgradeChecker service.
// Pass db=nil for standalone mode, or a *gorm.DB for distributed mode
// (uses advisory locks so only one instance runs periodic checks).
// backendManagerFn is optional; when set, CheckUpgrades is routed through
// the active backend manager — required in distributed mode so the check
// aggregates from workers rather than the empty frontend filesystem.
func NewUpgradeChecker(appConfig *config.ApplicationConfig, ml *model.ModelLoader, db *gorm.DB, backendManagerFn func() galleryop.BackendManager) *UpgradeChecker {
	return &UpgradeChecker{
		appConfig:        appConfig,
		modelLoader:      ml,
		galleries:        appConfig.BackendGalleries,
		systemState:      appConfig.SystemState,
		db:               db,
		backendManagerFn: backendManagerFn,
		initialDelay:     upgradeInitialDelay,
		checkInterval:    upgradeCheckInterval,
		stop:             make(chan struct{}),
		done:             make(chan struct{}),
		triggerCh:        make(chan struct{}, 1),
		lastUpgrades:     make(map[string]gallery.UpgradeInfo),
	}
}

// Run starts the upgrade checker loop. It waits upgradeInitialDelay after
// startup, performs an initial check, then re-checks every 6 hours.
//
// In distributed mode, the periodic pass is guarded by a PostgreSQL advisory
// lock so only one frontend instance runs it, and only that pass installs
// upgrades. On-demand triggers (TriggerCheck) and the initial check run on
// every instance to warm its cache, and never install.
func (uc *UpgradeChecker) Run(ctx context.Context) {
	defer close(uc.done)

	// Initial delay: don't slow down startup. Short enough that operators
	// don't stare at an empty upgrade banner for long; long enough that
	// workers have registered and reported their installed backends.
	initialDelay := uc.initialDelay
	select {
	case <-ctx.Done():
		return
	case <-uc.stop:
		return
	case <-time.After(initialDelay):
	}

	// First check always runs locally (to warm the cache on this instance)
	uc.runOnDemand(ctx)

	if uc.db != nil {
		// Distributed mode: use advisory lock for periodic checks.
		// RunLeaderLoop ticks every checkInterval; only the lock holder executes.
		go advisorylock.RunLeaderLoop(ctx, uc.db, advisorylock.KeyBackendUpgradeCheck, uc.checkInterval, func() {
			uc.runLeaderPass(ctx)
		})

		// Still listen for on-demand triggers (from API / settings change)
		// and stop signal — these run on every instance.
		for {
			select {
			case <-ctx.Done():
				return
			case <-uc.stop:
				return
			case <-uc.triggerCh:
				uc.runOnDemand(ctx)
			}
		}
	} else {
		// Standalone mode: simple ticker loop
		ticker := time.NewTicker(uc.checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-uc.stop:
				return
			case <-ticker.C:
				uc.runLeaderPass(ctx)
			case <-uc.triggerCh:
				uc.runOnDemand(ctx)
			}
		}
	}
}

// Shutdown stops the upgrade checker loop.
func (uc *UpgradeChecker) Shutdown() {
	close(uc.stop)
	<-uc.done
}

// TriggerCheck forces an immediate upgrade check on this instance. It does
// nothing on a nil checker.
func (uc *UpgradeChecker) TriggerCheck() {
	if uc == nil {
		return
	}
	select {
	case uc.triggerCh <- struct{}{}:
	default:
		// Already triggered, skip
	}
}

// GetAvailableUpgrades returns the cached upgrade check results. A nil
// checker (none is built without backend galleries or with installs disabled)
// has no results: the methods are nil-safe because Application.UpgradeChecker
// is handed to code that takes an interface, where a nil pointer is not nil.
func (uc *UpgradeChecker) GetAvailableUpgrades() map[string]gallery.UpgradeInfo {
	if uc == nil {
		return map[string]gallery.UpgradeInfo{}
	}
	uc.mu.RLock()
	defer uc.mu.RUnlock()

	// Return a copy to avoid races
	result := make(map[string]gallery.UpgradeInfo, len(uc.lastUpgrades))
	for k, v := range uc.lastUpgrades {
		result[k] = v
	}
	return result
}

// libraryUpgrader is the part of the library backend installer the checker
// needs: the newer builds the library lists, and the install of one.
type libraryUpgrader interface {
	Updates(ctx context.Context) ([]backendlib.Update, error)
	Install(ctx context.Context, sourceID, backend, version string, progress backendlib.Progress) error
}

// SetLibraryBackends makes the checker upgrade library backends too. fn may
// return nil while the host has no library.
func (uc *UpgradeChecker) SetLibraryBackends(fn func() libraryUpgrader) { uc.libraryFn = fn }

// upgradeLibraryBackends follows the same switch as the gallery backends:
// with auto_upgrade_backends on, a library backend moves to the newer build
// the library lists; with it off nothing is installed and the Update button on
// the Backends page stays the only way. The switch is read on every check, so
// a change in the runtime settings applies at the next one.
func (uc *UpgradeChecker) upgradeLibraryBackends(ctx context.Context, pass *upgradePass) {
	if !uc.appConfig.AutoUpgradeBackends || uc.libraryFn == nil {
		return
	}
	lib := uc.libraryFn()
	if lib == nil {
		return
	}
	updates, err := lib.Updates(ctx)
	if err != nil {
		xlog.Debug("Library backend upgrade check failed", "error", err)
		return
	}
	for _, u := range updates {
		xlog.Info("Auto-upgrading library backend", "backend", u.Name, "library", u.SourceID,
			"from", u.InstalledVersion, "to", u.Version)
		target := cmp.Or(u.Version, u.Digest)
		o := updatenotify.Outcome{ID: backendupdates.KindLibrary + ":" + u.SourceID + "/" + u.Name + "@" + target, Name: u.Name, From: u.InstalledVersion, To: u.Version}
		if err := lib.Install(ctx, u.SourceID, u.Name, u.Version, backendlib.Progress{}); err != nil {
			xlog.Error("Failed to auto-upgrade library backend", "backend", u.Name, "error", err)
			o.Kind, o.Err = updatenotify.OutcomeFailed, err.Error()
		} else {
			o.Kind = updatenotify.OutcomeUpdated
		}
		pass.outcomes = append(pass.outcomes, o)
	}
}

// detectGallery asks the active backend manager (the workers, in distributed
// mode) for the backends with a newer build and stores the answer. A failure
// keeps the previous list and records the error. It installs nothing.
func (uc *UpgradeChecker) detectGallery(ctx context.Context) (map[string]gallery.UpgradeInfo, error) {
	var (
		upgrades map[string]gallery.UpgradeInfo
		err      error
	)
	if uc.backendManagerFn != nil {
		if bm := uc.backendManagerFn(); bm != nil {
			upgrades, err = bm.CheckUpgrades(ctx)
		}
	}
	if upgrades == nil && err == nil {
		upgrades, err = gallery.CheckBackendUpgrades(ctx, uc.galleries, uc.systemState)
	}

	uc.mu.Lock()
	defer uc.mu.Unlock()
	now := time.Now()
	uc.lastCheckTime = now
	uc.lastCheckErr = err
	if err != nil {
		return nil, err
	}
	uc.lastSuccess = now
	uc.lastUpgrades = upgrades
	return upgrades, nil
}

// detectLibrary reads the library backends with a newer build. With refresh it
// first refreshes the library index; a refresh that fails is the error of the
// source and the previous list stays. It installs nothing. A host without a
// library records nothing.
func (uc *UpgradeChecker) detectLibrary(ctx context.Context, refresh bool) {
	lib := uc.library()
	if lib == nil {
		return
	}
	var (
		updates []backendlib.Update
		err     error
	)
	if refresh && uc.libraryRefreshFn != nil {
		if rerr := uc.libraryRefreshFn(ctx); rerr != nil {
			err = fmt.Errorf("refresh the library: %w", rerr)
		}
	}
	if err == nil {
		updates, err = lib.Updates(ctx)
	}
	uc.mu.Lock()
	defer uc.mu.Unlock()
	now := time.Now()
	uc.libCheckedAt = now
	if err != nil {
		uc.libErr = err.Error()
		xlog.Debug("Library backend update check failed", "error", err)
		return
	}
	uc.libErr = ""
	uc.libLastSuccess = now
	uc.libUpdates = updates
}

func (uc *UpgradeChecker) library() libraryUpgrader {
	if uc.libraryFn == nil {
		return nil
	}
	return uc.libraryFn()
}

// SetLibraryRefresh sets the call that refreshes the library index before a
// check an administrator asked for.
func (uc *UpgradeChecker) SetLibraryRefresh(fn func(ctx context.Context) error) {
	uc.libraryRefreshFn = fn
}

// SetLoadedModels sets the call that lists the models loaded on the nodes.
func (uc *UpgradeChecker) SetLoadedModels(fn func(ctx context.Context) ([]backendupdates.LoadedModel, error)) {
	uc.loadedModelsFn = fn
}

// UpdatesSnapshot returns the read model from this instance's cache. It
// contacts no registry. A nil checker answers with both sources disabled.
//
// In distributed mode the cache is per frontend (mode 4 of
// .agents/distributed-state.md): each frontend answers from what its own
// last check found, and checkedAt tells how old that is.
func (uc *UpgradeChecker) UpdatesSnapshot(ctx context.Context) backendupdates.Snapshot {
	return uc.snapshot(ctx, false)
}

func (uc *UpgradeChecker) snapshot(ctx context.Context, throttled bool) backendupdates.Snapshot {
	return uc.snapshotWith(ctx, throttled, true)
}

// snapshotWith is snapshot, with or without the lookup of the loaded models,
// which only the node view of the page needs.
func (uc *UpgradeChecker) snapshotWith(ctx context.Context, throttled, withModels bool) backendupdates.Snapshot {
	if uc == nil {
		const why = "backend updates are not checked on this server: installs are disabled or no backend gallery is configured"
		return backendupdates.Build(backendupdates.Input{
			GalleryStatus: backendupdates.SourceStatus{Disabled: true, Reason: why},
			LibraryStatus: backendupdates.SourceStatus{Disabled: true, Reason: why},
		})
	}
	uc.mu.RLock()
	in := backendupdates.Input{
		Gallery: make(map[string]gallery.UpgradeInfo, len(uc.lastUpgrades)),
		GalleryStatus: backendupdates.SourceStatus{
			CheckedAt: uc.lastCheckTime, LastSuccess: uc.lastSuccess,
		},
		Library: append([]backendlib.Update(nil), uc.libUpdates...),
		LibraryStatus: backendupdates.SourceStatus{
			CheckedAt: uc.libCheckedAt, LastSuccess: uc.libLastSuccess, Err: uc.libErr,
		},
		AutoBackends:     uc.appConfig != nil && uc.appConfig.AutoUpgradeBackends,
		InstallsDisabled: uc.appConfig != nil && uc.appConfig.DisableBackendInstall,
		Throttled:        throttled,
	}
	for k, v := range uc.lastUpgrades {
		in.Gallery[k] = v
	}
	if uc.lastCheckErr != nil {
		in.GalleryStatus.Err = uc.lastCheckErr.Error()
	}
	uc.mu.RUnlock()

	if uc.db != nil {
		in.LibraryStatus.Note = "Library backends of this host. Library backends installed on cluster workers are not listed."
	}
	if uc.library() == nil {
		in.LibraryStatus = backendupdates.SourceStatus{Disabled: true, Reason: "the add-on backend library is not enabled on this instance"}
	}
	if withModels && uc.loadedModelsFn != nil && len(in.Gallery) > 0 {
		if loaded, err := uc.loadedModelsFn(ctx); err == nil {
			in.Loaded = loaded
		} else {
			xlog.Debug("Could not list the loaded models for the update view", "error", err)
		}
	}
	return backendupdates.Build(in)
}

// CheckNow runs a check and waits for it: the gallery backends, and the
// library backends after a refresh of the library index. It returns the new
// read model. Failures are in the status of the source, and the previous list
// stays. It installs nothing, so any frontend can run it, leader or not; it
// updates the cache of this instance only.
//
// Callers that arrive while a check runs wait for that check. A call within
// checkNowCooldown of the last check is answered from it with Throttled set.
// The check is bounded by checkNowTimeout; when it runs out, the source shows
// the timeout as its error.
func (uc *UpgradeChecker) CheckNow(ctx context.Context) backendupdates.Snapshot {
	if uc == nil {
		return uc.snapshot(ctx, false)
	}
	uc.mu.RLock()
	recent := !uc.lastCheckTime.IsZero() && time.Since(uc.lastCheckTime) < checkNowCooldown
	uc.mu.RUnlock()
	if recent {
		return uc.snapshot(ctx, true)
	}
	// The check outlives a caller that leaves, so the callers that wait for the
	// same check still get an answer.
	ch := uc.flight.DoChan("check", func() (any, error) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), checkNowTimeout)
		defer cancel()
		uc.checkFresh(cctx)
		return nil, nil
	})
	select {
	case <-ch:
	case <-ctx.Done():
	}
	return uc.snapshot(ctx, false)
}

// checkFresh is the detection of CheckNow.
func (uc *UpgradeChecker) checkFresh(ctx context.Context) {
	if _, err := uc.detectGallery(ctx); err != nil {
		xlog.Debug("Backend update check failed", "error", err)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			uc.mu.Lock()
			uc.lastCheckErr = fmt.Errorf("the check timed out after %s: %w", checkNowTimeout, err)
			uc.mu.Unlock()
		}
	}
	uc.detectLibrary(ctx, true)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		uc.mu.Lock()
		if uc.libErr != "" {
			uc.libErr = fmt.Sprintf("the check timed out after %s: %s", checkNowTimeout, uc.libErr)
		}
		uc.mu.Unlock()
	}
}

// LastSuccessTime returns when the last check that did not fail finished, or
// the zero time.
func (uc *UpgradeChecker) LastSuccessTime() time.Time {
	if uc == nil {
		return time.Time{}
	}
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	return uc.lastSuccess
}

// LastCheckTime returns when the last check finished, successful or not. It is
// the zero time before the first check.
func (uc *UpgradeChecker) LastCheckTime() time.Time {
	if uc == nil {
		return time.Time{}
	}
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	return uc.lastCheckTime
}

// LastCheckError returns the error of the last check, or nil when it
// succeeded or none ran. After an error GetAvailableUpgrades still returns the
// results of the last good check.
func (uc *UpgradeChecker) LastCheckError() error {
	if uc == nil {
		return nil
	}
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	return uc.lastCheckErr
}

// runLeaderPass is the periodic pass: it detects and installs. In distributed
// mode only the advisory lock holder calls it.
func (uc *UpgradeChecker) runLeaderPass(ctx context.Context) {
	uc.announce(ctx, uc.runCheck(ctx, true))
}

// runOnDemand serves the first check and the triggers, which run on every
// frontend. Standalone there is one process, so it may install like the
// periodic pass. In distributed mode it only refreshes this frontend's cache.
func (uc *UpgradeChecker) runOnDemand(ctx context.Context) {
	auto := uc.db == nil
	pass := uc.runCheck(ctx, auto)
	if auto {
		uc.announce(ctx, pass)
	}
}

// runCheck detects upgrades and fills the cache. With autoUpgrade it also
// installs them when the setting is on.
func (uc *UpgradeChecker) runCheck(ctx context.Context, autoUpgrade bool) *upgradePass {
	pass := &upgradePass{}
	if autoUpgrade {
		defer func() {
			uc.upgradeLibraryBackends(ctx, pass)
			// The pass may have installed library updates.
			uc.detectLibrary(ctx, false)
		}()
	}
	upgrades, err := uc.detectGallery(ctx)
	uc.detectLibrary(ctx, false)
	if err != nil {
		xlog.Debug("Backend upgrade check failed", "error", err)
		return pass
	}

	if len(upgrades) == 0 {
		xlog.Debug("All backends up to date")
		return pass
	}

	// Log available upgrades
	for name, info := range upgrades {
		if info.AvailableVersion != "" {
			xlog.Info("Backend upgrade available",
				"backend", name,
				"installed", info.InstalledVersion,
				"available", info.AvailableVersion)
		} else {
			xlog.Info("Backend upgrade available (new build)",
				"backend", name)
		}
	}

	// Auto-upgrade if enabled. Route through the active BackendManager so
	// distributed-mode upgrades fan out to workers via NATS — calling
	// gallery.UpgradeBackend directly would look up the backend on the
	// frontend filesystem, which is empty in distributed mode and produces
	// "backend not found" while the cluster still reports an upgrade.
	if autoUpgrade && uc.appConfig.AutoUpgradeBackends {
		var bm galleryop.BackendManager
		if uc.backendManagerFn != nil {
			bm = uc.backendManagerFn()
		}
		for name, info := range upgrades {
			xlog.Info("Auto-upgrading backend", "backend", name,
				"from", info.InstalledVersion, "to", info.AvailableVersion)
			var err error
			if bm != nil {
				// Background auto-upgrade: no live admin watching a progress bar,
				// so op.ID is empty and the distributed path skips progress streaming.
				err = bm.UpgradeBackend(ctx, &galleryop.ManagementOp[gallery.GalleryBackend, any]{
					GalleryElementName: name,
					Upgrade:            true,
				}, nil)
			} else {
				err = gallery.UpgradeBackend(ctx, uc.systemState, uc.modelLoader,
					uc.galleries, name, nil, uc.appConfig.RequireBackendIntegrity)
			}
			o := updatenotify.Outcome{ID: backendupdates.KindGallery + ":" + name + "@" + cmp.Or(info.AvailableVersion, info.AvailableDigest),
				Name: name, From: info.InstalledVersion, To: info.AvailableVersion}
			if err != nil {
				xlog.Error("Failed to auto-upgrade backend",
					"backend", name, "error", err)
				o.Kind, o.Err = updatenotify.OutcomeFailed, err.Error()
			} else {
				xlog.Info("Backend upgraded successfully", "backend", name,
					"version", info.AvailableVersion)
				o.Kind = updatenotify.OutcomeUpdated
			}
			pass.outcomes = append(pass.outcomes, o)
		}
		// Re-check to update cache after upgrades. Route through the same
		// BackendManager so distributed mode reflects the worker view.
		var freshUpgrades map[string]gallery.UpgradeInfo
		var freshErr error
		if bm != nil {
			freshUpgrades, freshErr = bm.CheckUpgrades(ctx)
		} else {
			freshUpgrades, freshErr = gallery.CheckBackendUpgrades(ctx, uc.galleries, uc.systemState)
		}
		if freshErr == nil {
			uc.mu.Lock()
			uc.lastUpgrades = freshUpgrades
			uc.mu.Unlock()
		}
	}
	return pass
}

// upgradePass is what one automatic pass did, for the inbox.
type upgradePass struct {
	outcomes []updatenotify.Outcome
}

// announce writes the inbox items at the end of a pass that may install: the
// result of what it installed, the notice of a check that keeps failing, and
// the digest of the updates that need a person.
//
// It is called only where the automatic update runs: the leader pass in a
// cluster, and every installing pass of a standalone process. That is the
// single runner of the mode (.agents/distributed-state.md), the holder of
// KeyBackendUpgradeCheck, so no new lock key is needed. It is not called from
// TriggerCheck, from the first check of a frontend or from CheckNow.
func (uc *UpgradeChecker) announce(ctx context.Context, pass *upgradePass) {
	if uc.announcer == nil || ctx.Err() != nil {
		return
	}
	if len(pass.outcomes) > 0 {
		if err := uc.announcer.Result(ctx, updatenotify.Result{Family: updatenotify.FamilyBackends, Outcomes: pass.outcomes}); err != nil {
			xlog.Warn("Could not write the result of the backend update pass to the inbox", "error", err)
		}
	}
	// No models lookup: the digest needs names and versions only.
	snap := uc.snapshotWith(ctx, false, false)
	if err := uc.announcer.CheckFailed(ctx, uc.failingSources(snap)); err != nil {
		xlog.Warn("Could not write the failed update check to the inbox", "error", err)
	}
	if err := uc.announcer.Digest(ctx, snap); err != nil {
		xlog.Warn("Could not write the update digest to the inbox", "error", err)
	}
}

// failingSources updates the failure streaks with the sources of snap and
// returns the sources that failed checkFailedAfter passes in a row.
func (uc *UpgradeChecker) failingSources(snap backendupdates.Snapshot) []string {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	if uc.failStreak == nil {
		uc.failStreak = map[string]int{}
	}
	var out []string
	for name, src := range snap.Sources {
		if src.State == backendupdates.StateFailed {
			uc.failStreak[name]++
		} else {
			uc.failStreak[name] = 0
		}
		if uc.failStreak[name] >= checkFailedAfter {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
