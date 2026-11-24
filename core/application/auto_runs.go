package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mudler/LocalAI/core/services/addons"
	addonupdates "github.com/mudler/LocalAI/core/services/addons/updates"
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

// autoRunActor is the actor of a run that the automatic update started: the
// user id of the owner's own automatic upgrade, which the add-on routes
// recognise too. The add-on items of such a run go through that automatic
// upgrade (autoAddonStarter), never through the path of an administrator.
var autoRunActor = addons.AutoUpgradeActor.UserID

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

// autoRunner starts the runs of the automatic passes. It owns the retry
// goroutines it starts: Close ends them and waits for them.
type autoRunner struct {
	// service returns the run service, or nil while there is none.
	service func() autoRunService
	delay   time.Duration
	retries int

	mu      sync.Mutex
	waiting map[string]bool
	closed  bool
	quit    chan struct{}
	wg      sync.WaitGroup
}

func newAutoRunner(service func() autoRunService) *autoRunner {
	return &autoRunner{
		service: service, delay: autoRunRetryDelay, retries: autoRunRetries,
		waiting: map[string]bool{}, quit: make(chan struct{}),
	}
}

// Close stops the retries that wait for another run to end and waits for
// their goroutines. A retry that is starting its run finishes that start
// first. After Close no retry starts; the next scheduled pass of a new
// process lists the items again. A nil runner does nothing.
func (r *autoRunner) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.quit)
	}
	r.mu.Unlock()
	r.wg.Wait()
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
	case upgraderun.CodeOf(err) == upgraderun.CodeHeldBack:
		// Every item is a release that did not start before; the Updates
		// page says so, and a new release in the gallery ends the hold.
		xlog.Debug("Automatic update skipped releases that did not start before", "family", family)
		return autoRunRefused
	default:
		xlog.Warn("Automatic update did not start its run; the next pass tries again", "family", family, "error", err)
		return autoRunRefused
	}
}

// heldBackSource is the part of upgraderun.Service that tells the releases an
// automatic run leaves out.
type heldBackSource interface {
	HeldBack(ctx context.Context) (upgraderun.HeldBack, error)
}

// heldBack returns the releases the automatic runs leave out, or nil when
// there is no run service or it cannot tell.
func (r *autoRunner) heldBack(ctx context.Context) upgraderun.HeldBack {
	if !r.available() {
		return nil
	}
	src, ok := r.service().(heldBackSource)
	if !ok {
		return nil
	}
	held, err := src.HeldBack(ctx)
	if err != nil {
		xlog.Debug("Could not read the releases the automatic update holds back", "error", err)
		return nil
	}
	return held
}

func (r *autoRunner) start(ctx context.Context, items []upgraderun.StartItem) (upgraderun.Run, error) {
	svc := r.service()
	if svc == nil {
		return upgraderun.Run{}, errors.New("upgrade runs are not available")
	}
	return svc.Start(withAutoRun(ctx), autoRunActor, upgraderun.StartRequest{Source: upgraderun.SourceAuto, Items: items})
}

// retryLater tries to start the run again every delay while another run is
// running, at most retries times, one waiter for each family. It stops at the
// first answer that is not run_active, when ctx ends, or when the runner is
// closed.
func (r *autoRunner) retryLater(ctx context.Context, family string, items []upgraderun.StartItem) {
	r.mu.Lock()
	if r.closed || r.waiting[family] {
		r.mu.Unlock()
		return
	}
	r.waiting[family] = true
	// Added under the lock that Close takes, so Wait never misses it.
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			delete(r.waiting, family)
			r.mu.Unlock()
		}()
		timer := time.NewTimer(r.delay)
		defer timer.Stop()
		for range r.retries {
			select {
			case <-ctx.Done():
				return
			case <-r.quit:
				return
			case <-timer.C:
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
			timer.Reset(r.delay)
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
	// The owner's automatic upgrade ignores the pins of the item and follows
	// the automatic policy, so only a run that the automatic update started
	// may take it. A manual run never carries the system actor (a retry
	// belongs to the administrator who retries); refuse one that does rather
	// than upgrade against the policy.
	if upgraderun.SourceFrom(ctx) != upgraderun.SourceAuto {
		return "", &upgraderun.StartError{ErrorKey: upgraderun.ErrKeyForbidden, Err: errors.New("only an automatic run upgrades add-ons as the automatic update")}
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
		return "", &upgraderun.StartError{ErrorKey: upgraderun.ErrKeyUnavailable, Retryable: true, Err: errors.New("add-on upgrades are not available on this server")}
	}
	jobID := uuid.NewString()
	if s.opCache != nil {
		if c := s.opCache(); c != nil {
			c.Set(galleryop.AddonOpKey(it.Name), jobID)
		}
	}
	id := it.Name
	q.EnqueueAddonOp(galleryop.AddonOp{
		ID: jobID, AddonID: id, Upgrade: true, InRun: true,
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

type autoRunKey struct{}

// withAutoRun marks the context of the checks of a run that the automatic
// update starts.
func withAutoRun(ctx context.Context) context.Context {
	return context.WithValue(ctx, autoRunKey{}, true)
}

// isAutoRunCtx reports whether the checks that ctx belongs to are those of an
// automatic run: the start of a pass marks its context, and the service puts
// the actor of the run in the context of every later check (the check before
// an item starts, the check after it ended, a retry).
func isAutoRunCtx(ctx context.Context) bool {
	if v, _ := ctx.Value(autoRunKey{}).(bool); v {
		return true
	}
	return upgraderun.ActorFrom(ctx) == autoRunActor
}

// ownerStatusSource is the owner's list of add-on update statuses
// (addons.Manager implements it).
type ownerStatusSource interface {
	AutoUpgradeStatuses(ctx context.Context, fresh bool) ([]addonupdates.Status, bool, error)
}

// ownerAddonChecker lists the add-ons with an update for an automatic run. It
// reads the add-on owner of this process and acts as no identity: the system
// actor of the automatic upgrade must never reach the add-on gateway, whose
// admin check refuses it. Only the owner's process answers; any other process
// has no list, so the kind is unchecked and no add-on item can start there.
type ownerAddonChecker struct {
	src   ownerStatusSource
	owner func() bool
}

func (ownerAddonChecker) Kinds() []upgraderun.Kind { return []upgraderun.Kind{upgraderun.KindAddon} }

func (c ownerAddonChecker) List(ctx context.Context, fresh bool) (upgraderun.Listing, error) {
	if c.src == nil || c.owner == nil || !c.owner() {
		return upgraderun.Listing{}, errors.New("only the add-on owner lists the add-ons of an automatic run")
	}
	sts, complete, err := c.src.AutoUpgradeStatuses(ctx, fresh)
	if err != nil {
		return upgraderun.Listing{}, err
	}
	l := upgraderun.Listing{Complete: complete}
	for _, st := range sts {
		if st.Throttled {
			l.Throttled = true
		}
		if st.State != addonupdates.UpdateAvailable || st.InstalledDigest == "" || st.CandidateDigest == "" {
			continue
		}
		l.Candidates = append(l.Candidates, upgraderun.AddonCandidate(upgraderun.AddonVersions{
			ID:               st.AddonID,
			InstalledVersion: st.InstalledVersion, InstalledDigest: st.InstalledDigest,
			CandidateVersion: st.CandidateVersion, CandidateDigest: st.CandidateDigest,
		}))
	}
	return l, nil
}

// addonCheckerBySource lists the add-ons for a run through the checker that
// fits its source: the owner-local one for an automatic run, the one of the
// administrator's own path for every other run.
type addonCheckerBySource struct {
	auto, manual upgraderun.Checker
	isAuto       func(context.Context) bool
}

func (addonCheckerBySource) Kinds() []upgraderun.Kind { return []upgraderun.Kind{upgraderun.KindAddon} }

func (c addonCheckerBySource) List(ctx context.Context, fresh bool) (upgraderun.Listing, error) {
	if c.isAuto != nil && c.isAuto(ctx) {
		return c.auto.List(ctx, fresh)
	}
	return c.manual.List(ctx, fresh)
}
