package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/xlog"
)

// The automatic update passes (backends and library backends in the update
// check, add-ons in the add-on pass) start an upgrade run with the source
// "auto" instead of installing inside the pass, so the work shows in the
// Updates page, in Operations and in the inbox like any other run. See
// docs/content/models/upgrade-runs.md.
//
// Only one run runs at a time in a cluster. A pass that finds one running does
// not lose its items: the next pass lists them again, and a short retry (see
// autoRunner) starts them as soon as the other run ends.

// autoRunActor is the actor of a run that the automatic update started. The
// add-on items of such a run go through the owner's own automatic upgrade
// (autoAddonStarter), never through the path of an administrator.
const autoRunActor = "system:auto-upgrade"

const (
	// autoRunRetryDelay is how long a pass waits before it tries to start its
	// run again while another run is running.
	autoRunRetryDelay = time.Minute
	// autoRunRetries bounds those tries. After them the next scheduled pass
	// finds the items again.
	autoRunRetries = 30
)

// autoRunService is the part of upgraderun.Service the passes need.
type autoRunService interface {
	Start(ctx context.Context, actor string, req upgraderun.StartRequest) (upgraderun.Run, error)
}

// autoRunOutcome says what begin did.
type autoRunOutcome int

const (
	// autoRunUnavailable: there is no run service; the caller keeps its
	// inline path.
	autoRunUnavailable autoRunOutcome = iota
	// autoRunStarted: the run started.
	autoRunStarted
	// autoRunWaiting: another run is running; a retry starts this one later.
	autoRunWaiting
	// autoRunRefused: the service refused the items (the check moved, a
	// source did not answer). Nothing was changed; the next pass tries again.
	autoRunRefused
)

// autoRunner starts the runs of the automatic passes.
type autoRunner struct {
	// service returns the run service, or nil while there is none.
	service func() autoRunService
	delay   time.Duration
	retries int

	mu      sync.Mutex
	waiting map[string]bool
}

func newAutoRunner(service func() autoRunService) *autoRunner {
	return &autoRunner{service: service, delay: autoRunRetryDelay, retries: autoRunRetries, waiting: map[string]bool{}}
}

// available reports whether runs can be started. A nil runner has none.
func (r *autoRunner) available() bool {
	return r != nil && r.service != nil && r.service() != nil
}

// begin starts one run with the source "auto" for the items of a family (a
// label for the log and for the retry: one retry waits for each family).
func (r *autoRunner) begin(ctx context.Context, family string, items []upgraderun.StartItem) autoRunOutcome {
	if !r.available() || len(items) == 0 {
		return autoRunUnavailable
	}
	run, err := r.start(ctx, items)
	switch {
	case err == nil:
		xlog.Info("Automatic update started as an upgrade run", "family", family, "run", run.ID, "items", len(items))
		return autoRunStarted
	case upgraderun.CodeOf(err) == upgraderun.CodeRunActive:
		xlog.Debug("Automatic update waits: another upgrade run is running", "family", family)
		r.retryLater(ctx, family, items)
		return autoRunWaiting
	default:
		xlog.Warn("Automatic update did not start its run; the next pass tries again", "family", family, "error", err)
		return autoRunRefused
	}
}

func (r *autoRunner) start(ctx context.Context, items []upgraderun.StartItem) (upgraderun.Run, error) {
	svc := r.service()
	if svc == nil {
		return upgraderun.Run{}, errors.New("upgrade runs are not available")
	}
	return svc.Start(ctx, autoRunActor, upgraderun.StartRequest{Source: upgraderun.SourceAuto, Items: items})
}

// retryLater tries to start the run again every delay while another run is
// running, at most retries times, one waiter for each family. It stops at the
// first answer that is not run_active.
func (r *autoRunner) retryLater(ctx context.Context, family string, items []upgraderun.StartItem) {
	r.mu.Lock()
	if r.waiting[family] {
		r.mu.Unlock()
		return
	}
	r.waiting[family] = true
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.waiting, family)
			r.mu.Unlock()
		}()
		for range r.retries {
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.delay):
			}
			run, err := r.start(ctx, items)
			switch {
			case err == nil:
				xlog.Info("Automatic update started as an upgrade run", "family", family, "run", run.ID, "items", len(items))
				return
			case upgraderun.CodeOf(err) == upgraderun.CodeRunActive:
			default:
				xlog.Debug("Automatic update did not start its delayed run; the next pass tries again", "family", family, "error", err)
				return
			}
		}
	}()
}

// autoRunner returns the runner of the automatic passes.
func (a *Application) autoRunner() *autoRunner {
	a.autoRunnerOnce.Do(func() {
		a.autoRuns = newAutoRunner(func() autoRunService {
			if s := a.UpgradeRuns(); s != nil {
				return s
			}
			return nil
		})
	})
	return a.autoRuns
}

// addonAutoUpgrader is what the add-on pass needs from the add-on owner.
type addonAutoUpgrader interface {
	AutoUpgrade(ctx context.Context) []addons.AutoUpgradeResult
	AutoUpgradePending(ctx context.Context) []addons.AutoUpgradeCandidate
	AutoUpgradeOne(ctx context.Context, id string) (addons.AutoUpgradeResult, bool)
}

// addonOpQueue admits an add-on operation (galleryop.GalleryService).
type addonOpQueue interface {
	EnqueueAddonOp(op galleryop.AddonOp)
}

// autoAddonStarter starts the add-on items of a run. A run of an administrator
// goes through inner (the recorded upgrade of that administrator). A run that
// the automatic update started goes through the owner's automatic upgrade of
// that one add-on, so its rules stay as they are: the owner's own actor, the
// pinned digests, a candidate that needs an approval is held and never
// approved, and a candidate that failed is not tried again. The upgrade is an
// operation of the queue, so it shows in Operations and can be cancelled while
// it waits.
type autoAddonStarter struct {
	inner   upgraderun.Starter
	manager func() addonAutoUpgrader
	queue   func() addonOpQueue
	opCache func() *galleryop.OpCache
}

func (s autoAddonStarter) Start(ctx context.Context, actor string, it upgraderun.Item) (string, error) {
	if actor != autoRunActor {
		return s.inner.Start(ctx, actor, it)
	}
	var m addonAutoUpgrader
	if s.manager != nil {
		m = s.manager()
	}
	var q addonOpQueue
	if s.queue != nil {
		q = s.queue()
	}
	if m == nil || q == nil {
		return "", &upgraderun.StartError{ErrorKey: "unavailable", Retryable: true, Err: errors.New("add-on upgrades are not available on this server")}
	}
	jobID := uuid.NewString()
	if s.opCache != nil {
		if c := s.opCache(); c != nil {
			c.Set(galleryop.AddonOpKey(it.Name), jobID)
		}
	}
	id := it.Name
	q.EnqueueAddonOp(galleryop.AddonOp{
		ID: jobID, AddonID: id, Upgrade: true,
		Run: func(ctx context.Context, _ func(*galleryop.OpStatus)) error {
			r, ok := m.AutoUpgradeOne(ctx, id)
			switch {
			case !ok:
				// No update any more: the check of the run decides.
				return nil
			case r.SkippedAsFailure:
				return errors.New("this release failed before and is not tried again until the library lists another one")
			case r.HeldForApproval:
				return fmt.Errorf("held: the new release needs host access that nobody approved; approve the upgrade of %s on its page", id)
			case r.Err != nil:
				return r.Err
			}
			return nil
		},
	})
	return jobID, nil
}
