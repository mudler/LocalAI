package application

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// UpgradeChecker periodically checks for backend upgrades and optionally
// auto-upgrades them. It caches the last check results for API queries.
//
// In standalone mode it runs a simple ticker loop.
// In distributed mode it uses a PostgreSQL advisory lock so that only one
// frontend instance performs periodic checks and auto-upgrades at a time.
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

	checkInterval time.Duration
	stop          chan struct{}
	done          chan struct{}
	triggerCh     chan struct{}

	mu            sync.RWMutex
	lastUpgrades  map[string]gallery.UpgradeInfo
	lastCheckTime time.Time
}

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
		checkInterval:    6 * time.Hour,
		stop:             make(chan struct{}),
		done:             make(chan struct{}),
		triggerCh:        make(chan struct{}, 1),
		lastUpgrades:     make(map[string]gallery.UpgradeInfo),
	}
}

// Run starts the upgrade checker loop. It waits 30 seconds after startup,
// performs an initial check, then re-checks every 6 hours.
//
// In distributed mode, periodic checks are guarded by a PostgreSQL advisory
// lock so only one frontend instance runs them. On-demand triggers (TriggerCheck)
// and the initial check always run locally for fast API response cache warming.
func (uc *UpgradeChecker) Run(ctx context.Context) {
	defer close(uc.done)

	// Initial delay: don't slow down startup. Short enough that operators
	// don't stare at an empty upgrade banner for long; long enough that
	// workers have registered and reported their installed backends.
	initialDelay := 10 * time.Second
	select {
	case <-ctx.Done():
		return
	case <-uc.stop:
		return
	case <-time.After(initialDelay):
	}

	// First check always runs locally (to warm the cache on this instance)
	uc.runCheck(ctx)

	if uc.db != nil {
		// Distributed mode: use advisory lock for periodic checks.
		// RunLeaderLoop ticks every checkInterval; only the lock holder executes.
		go advisorylock.RunLeaderLoop(ctx, uc.db, advisorylock.KeyBackendUpgradeCheck, uc.checkInterval, func() {
			uc.runCheck(ctx)
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
				uc.runCheck(ctx)
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
				uc.runCheck(ctx)
			case <-uc.triggerCh:
				uc.runCheck(ctx)
			}
		}
	}
}

// Shutdown stops the upgrade checker loop.
func (uc *UpgradeChecker) Shutdown() {
	close(uc.stop)
	<-uc.done
}

// TriggerCheck forces an immediate upgrade check on this instance.
func (uc *UpgradeChecker) TriggerCheck() {
	select {
	case uc.triggerCh <- struct{}{}:
	default:
		// Already triggered, skip
	}
}

// GetAvailableUpgrades returns the cached upgrade check results.
func (uc *UpgradeChecker) GetAvailableUpgrades() map[string]gallery.UpgradeInfo {
	uc.mu.RLock()
	defer uc.mu.RUnlock()

	// Return a copy to avoid races
	result := make(map[string]gallery.UpgradeInfo, len(uc.lastUpgrades))
	for k, v := range uc.lastUpgrades {
		result[k] = v
	}
	return result
}

func (uc *UpgradeChecker) runCheck(ctx context.Context) {
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
	uc.lastCheckTime = time.Now()
	if err != nil {
		xlog.Debug("Backend upgrade check failed", "error", err)
		uc.mu.Unlock()
		return
	}
	uc.lastUpgrades = upgrades
	uc.mu.Unlock()

	if len(upgrades) == 0 {
		xlog.Debug("All backends up to date")
		return
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
	if uc.appConfig.AutoUpgradeBackends {
		var bm galleryop.BackendManager
		if uc.backendManagerFn != nil {
			bm = uc.backendManagerFn()
		}
		for name, info := range upgrades {
			xlog.Info("Auto-upgrading backend", "backend", name,
				"from", info.InstalledVersion, "to", info.AvailableVersion)
			var err error
			if bm != nil {
				err = bm.UpgradeBackend(ctx, name, nil)
			} else {
				err = gallery.UpgradeBackend(ctx, uc.systemState, uc.modelLoader,
					uc.galleries, name, nil)
			}
			if err != nil {
				xlog.Error("Failed to auto-upgrade backend",
					"backend", name, "error", err)
			} else {
				xlog.Info("Backend upgraded successfully", "backend", name,
					"version", info.AvailableVersion)
			}
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
}
