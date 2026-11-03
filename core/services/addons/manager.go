// SPDX-License-Identifier: MIT
// Package addons owns durable addon intent on one explicitly designated host.
package addons

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/core/services/addons/uploads"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
)

var (
	ErrConfiguration    = errors.New("invalid addon owner configuration")
	ErrNotOwner         = errors.New("addon owner unavailable on this replica")
	ErrOwnerBusy        = errors.New("addon storage already owned")
	ErrForbidden        = errors.New("addon administration denied")
	ErrConflict         = errors.New("conflicting addon lifecycle command")
	ErrOperationFailed  = errors.New("addon operation failed; use a new operation ID for an explicit retry")
	ErrRecoveryRequired = errors.New("addon journal requires reconciliation")
	// ErrStaleGeneration is a denial: the caller acted on a generation that is
	// no longer the installed one. Nothing was recorded.
	ErrStaleGeneration = errors.New("addon generation changed")
	// ErrStaleInstalled is a denial of an explicit upgrade: the installed
	// package is not the one the caller checked. Nothing was recorded.
	ErrStaleInstalled = errors.New("installed add-on changed since the update was checked")
	// ErrCandidateChanged is a denial of an explicit upgrade: the library no
	// longer resolves to the candidate the caller checked. Nothing was recorded.
	ErrCandidateChanged = errors.New("update candidate changed since it was checked")
	// ErrTransitionConflict is a denial: the same upgrade, from the same
	// installed package to the same candidate, is recorded under another
	// operation ID and has not failed.
	ErrTransitionConflict = errors.New("this upgrade is already recorded under another operation ID")
	// ErrIncompatible is a denial: the candidate cannot run on this host.
	ErrIncompatible = errors.New("update candidate is not compatible with this host")
	// ErrIntegrity is a denial: the library lists the installed immutable
	// version with another digest. It is never an update.
	ErrIntegrity = errors.New("library rewrote an immutable version")
	// ErrCheckFailed is a denial: the owner could not tell what the library
	// offers, so nothing was started. It is not an unknown outcome.
	ErrCheckFailed = errors.New("update check failed")
)

// Codes of the cause of a failed install or upgrade. A cause is typed so that
// an administrator is told which step failed without reading the server log.
const (
	// CauseLibraryUnavailable: the library could not be read.
	CauseLibraryUnavailable = "library_unavailable"
	// CauseLibraryConflict: two enabled libraries list the same add-on.
	CauseLibraryConflict = "library_conflict"
	// CauseReleaseNotFound: the library does not list the requested release
	// for this platform.
	CauseReleaseNotFound = "release_not_found"
	// CausePackageFetch: the package could not be downloaded or staged.
	CausePackageFetch = "package_fetch_failed"
	// CauseInstallStorage: the verified package could not be written to disk.
	CauseInstallStorage = "install_storage_failed"
	// CauseFileMissing: the staged file of a local file install is gone, has
	// expired or belongs to another user.
	CauseFileMissing = "staged_file_missing"
	// CauseVersionNotNewer: the file holds the version that is installed.
	CauseVersionNotNewer = "version_not_newer"
	// CauseDowngradeRefused: the file holds an older version and the request
	// did not confirm the downgrade.
	CauseDowngradeRefused = "downgrade_refused"
)

// FailureCause is the typed cause of a failed operation. Its text is fixed and
// safe to show; the underlying error stays in the server log.
type FailureCause struct {
	Code   string
	detail error
}

func (c *FailureCause) Error() string { return "addon operation failed: " + c.Code }

// These hooks are contracts for the runtime/migration tasks. This manager does
// not claim to drain processes, back up databases, or execute migrations.
type Drainer interface {
	Drain(context.Context, string) error
}
type Backupper interface {
	Backup(context.Context, string) error
}
type Migrator interface {
	Migrate(context.Context, string, Generation) error
}

type Manager struct {
	// One lock also serializes journal publication across addons. No operation can
	// overtake another operation on the same addon, including reconciliation.
	mu                            sync.Mutex
	upgradeMu                     sync.Mutex
	upgradeViews                  map[string]upgradeSnapshot
	finishedUpgrades              []string // oldest first; guarded by upgradeMu
	upgradeCtx                    context.Context
	upgradeCancel                 context.CancelFunc
	upgradeWG                     sync.WaitGroup
	upgradeActive, upgradeClosing bool
	// opMu serializes Submit and Reconcile as whole operations, so mu can be
	// released around a slow fetch without letting another operation in.
	opMu                    sync.Mutex
	toolCache               map[string]toolDeclaration
	config                  OwnerConfig
	catalog                 catalog.Catalog
	installer               *packages.Installer
	admin                   catalog.AdminCheck
	release                 func() error
	closed, uncertain       bool
	journal                 journal
	boundary                func(string) error
	runtimeRecoveryRequired bool
	runtime                 *addonruntime.Supervisor
	instances               map[string]addonruntime.Instance
	// startFailed holds the last start error of each enabled add-on whose
	// process is not running. It is an observation, never durable intent: a
	// restart tries again, and one add-on that cannot start never stops the
	// owner or fails the commands of another add-on.
	startFailed map[string]error
	// canceled holds the upgrades whose request was canceled while the
	// candidate started. The package is already published and the last good
	// generation runs again; the same operation ID continues from here.
	// In memory only: a restart marks the intent failed and starts the
	// journaled generation.
	canceled map[string]packages.PackageReceipt
	// restoreTimeout bounds the work that puts the last good generation back
	// and does not depend on the request. Zero means the default.
	restoreTimeout time.Duration
	// scanner reads the size of data directories outside mu.
	scanner storageScanner
	// recovery backs up, migrates and restores add-on data. Nil means plain
	// upgrades only.
	recovery *recovery.Recovery
	// quiesced holds the add-ons that recovery keeps stopped, whatever the
	// journal says. It is an observation, never durable intent.
	quiesced map[string]bool
	// updates is the owner's update check. Nil means no checks and no pinned
	// upgrades.
	updates *updates.Checker
	// autoFailures holds, per add-on, the candidate digest of the last
	// automatic upgrade that failed. In memory only: a restart tries again.
	autoMu       sync.Mutex
	autoFailures map[string]string
	// heldUpgrades holds, per add-on, an upgrade that waits for approval.
	heldUpgrades map[string]heldUpgrade
	// skillSync keeps the chats' skill layer in step with the installed
	// packages. Nil until the chats' files exist (see SetSkillSync).
	skillSync SkillSync
	// hostOps is the host setting of the host operation prefixes of each
	// add-on. The apps list reads it to decide which host controls it draws.
	hostOps    map[string][]string
	hostOpsSet bool
	// authority answers what each add-on may call now. Nil means the startup
	// snapshot in hostOps. It is read on every admission and every listing,
	// so a grant or a revoke applies without a restart.
	authority atomic.Pointer[hostops.Authority]
	floor     atomic.Pointer[hostops.Authority]
	// grants records the grants made in the UI; pending holds, per add-on, the
	// operations an install or upgrade in flight is about to be granted.
	grantsMu sync.Mutex
	grants   GrantStore
	pending  map[string][]string
	// completed holds the operations that completed; see OperationCompleted.
	completed map[string]bool
	// identities maps an installed add-on to its trusted identity; see
	// TrustedIdentity.
	identities map[string]string
	// uploads holds the zip files waiting for an install. Nil means the owner
	// does not install from a file.
	uploads atomic.Pointer[uploads.Store]
}

// SetAuthority sets the Authority that admission and the apps list ask. The
// first call wins; set it before attaching the runtime. Without it the
// startup snapshot of SetHostOps is the authority.
func (m *Manager) SetAuthority(a hostops.Authority) {
	if a == nil {
		return
	}
	m.authority.CompareAndSwap(nil, &a)
}

// hostAuthority is the Authority in force: the one set, or the startup
// snapshot of host operation prefixes.
func (m *Manager) hostAuthority() hostops.Authority {
	if a := m.authority.Load(); a != nil {
		return *a
	}
	if f := m.floor.Load(); f != nil {
		return *f
	}
	return hostops.NewStaticAuthority(nil)
}

// SetHostOps sets the host operation grants of the add-ons (the host setting,
// read at startup). The apps list draws a host control only for an add-on
// whose grants entitle it. The first call selects an immutable startup snapshot;
// subsequent calls do not reload policy. Call before attaching the runtime.
func (m *Manager) SetHostOps(ops map[string][]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hostOpsSet {
		return
	}
	m.hostOpsSet = true
	m.hostOps = make(map[string][]string, len(ops))
	for id, prefixes := range ops {
		m.hostOps[id] = slices.Clone(prefixes)
	}
	floor := hostops.NewStaticAuthority(m.hostOps)
	m.floor.Store(&floor)
}

// SkillSync copies the skills of an add-on package into the chats' skill layer
// (receipt set), or removes them (receipt nil). It runs on the owner only, which
// holds the verified package. A failure is logged and never fails the lifecycle
// operation: skills are help for a chat, the add-on works without them.
type SkillSync func(ctx context.Context, addonID string, receipt *packages.PackageReceipt) error

// SetSkillSync sets the skill copy and runs it once for every add-on the
// journal knows: the current package of an installed one, a removal for the
// others. It is how the owner reconciles the skill layer at start.
func (m *Manager) SetSkillSync(ctx context.Context, sync SkillSync) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.skillSync = sync
	if sync == nil || m.check(ctx) != nil {
		return
	}
	ids := make([]string, 0, len(m.journal.Addons))
	for id := range m.journal.Addons {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m.syncSkills(ctx, id)
	}
}

// InstalledSkillIDs returns the installed add-ons whose current package ships
// skills, sorted.
func (m *Manager) InstalledSkillIDs(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return nil, e
	}
	var out []string
	for id, st := range m.journal.Addons {
		if r, ok := currentReceipt(st); ok && st.Installed && len(r.Skills) > 0 {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// syncSkills runs the skill copy for one add-on from the journal: its current
// package while installed, a removal otherwise. The caller holds mu.
func (m *Manager) syncSkills(ctx context.Context, id string) {
	if m.skillSync == nil {
		return
	}
	var receipt *packages.PackageReceipt
	if st := m.journal.Addons[id]; st.Installed {
		if r, ok := currentReceipt(st); ok {
			receipt = &r
		}
	}
	if e := m.skillSync(ctx, id, receipt); e != nil {
		xlog.Warn("Could not copy the skills of an add-on", "addon", id, "error", e)
	}
}

func NewManager(ctx context.Context, cfg OwnerConfig, c catalog.Catalog, i *packages.Installer, admin catalog.AdminCheck) (*Manager, error) {
	if e := cfg.validate(); e != nil {
		return nil, e
	}
	m := &Manager{config: cfg, catalog: c, installer: i, admin: admin, upgradeViews: map[string]upgradeSnapshot{}}
	m.upgradeCtx, m.upgradeCancel = context.WithCancel(context.Background())
	if cfg.InstanceID != cfg.OwnerID {
		return m, nil
	}
	if i == nil || admin == nil || filepath.Clean(i.Root) != filepath.Join(cfg.Root, "packages") {
		return nil, ErrConfiguration
	}
	release, e := (OwnerGuard{Config: cfg}).Acquire(ctx)
	if e != nil {
		return nil, e
	}
	m.release = release
	if e = m.load(); e != nil {
		_ = release()
		return nil, e
	}
	m.syncUpgradeViews(true)
	return m, nil
}
func (m *Manager) Close() error {
	m.upgradeMu.Lock()
	m.upgradeClosing = true
	m.upgradeCancel()
	m.upgradeMu.Unlock()
	m.upgradeWG.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed && m.release == nil {
		return nil
	}
	m.closed = true
	if m.updates != nil {
		// In-flight update checks end and are waited for before the owner
		// lets go of its storage.
		m.updates.Close()
	}
	if m.runtime != nil {
		if e := m.runtime.Close(context.Background()); e != nil {
			return e
		}
	}
	if m.release != nil {
		e := m.release()
		if e == nil {
			m.release = nil
		}
		return e
	}
	return nil
}
func (m *Manager) check(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if m.closed || m.release == nil {
		return ErrNotOwner
	}
	return nil
}

// addonID matches a well-formed add-on or source ID that no host route uses.
var addonID idMatcher

// sourceIDPattern matches a library ID, which is never a path segment of a route.
var sourceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type idMatcher struct{}

func (idMatcher) MatchString(s string) bool { return contract.ValidAddonID(s) }

func (m *Manager) Status(ctx context.Context, id string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return Status{}, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return Status{}, ErrRecoveryRequired
	}
	s := copyStatus(m.journal.Addons[id])
	s.AddonID = id
	s.ObservedHealth = "stopped"
	s.StartEligible = s.Installed && s.DesiredEnabled
	if m.admission(id) != nil {
		s.StartEligible = false
		s.ObservedHealth = "unavailable"
	}
	for _, op := range m.journal.Operations {
		if op.Command.AddonID == id && isActive(op.Status.Phase) {
			s.StartEligible = false
		}
	}
	if m.quiesced[id] || m.dataBlocked(id) {
		s.StartEligible = false
	}
	if m.runtime != nil {
		registrations, e := m.runtime.Registry.Snapshot(ctx)
		if e != nil {
			return Status{}, e
		}
		for _, r := range registrations {
			if r.AddonID == id && r.Generation == s.Generation {
				s.ObservedHealth = "ready"
			}
		}
	}
	return s, nil
}
func (m *Manager) Submit(ctx context.Context, actor contract.Actor, cmd LifecycleCommand) (OperationStatus, error) {
	return m.submit(ctx, actor, cmd, false)
}

// submit is the one lifecycle path. The owner's automatic upgrade passes
// owner true: the owner acts on its own setting, so no administrator is asked.
// Everything after that gate is shared with the manual upgrade.
func (m *Manager) submit(ctx context.Context, actor contract.Actor, cmd LifecycleCommand, owner bool) (OperationStatus, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return OperationStatus{}, e
	}
	if !owner && (m.admin == nil || m.admin(ctx, actor) != nil) {
		return OperationStatus{}, ErrForbidden
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return OperationStatus{}, ErrRecoveryRequired
	}
	var op journalOperation
	resumed := false
	accepted := false
	if !addonID.MatchString(cmd.AddonID) || cmd.OperationID == "" || len(cmd.OperationID) > 256 {
		return OperationStatus{}, ErrConflict
	}
	// A package from a local file has no library and no signature behind it.
	// It installs only with the approval the administrator gave in the dialog
	// of their own browser session; the plain typed install, which an API key
	// or a tool can make, never takes a file.
	if cmd.SourceID == zipformat.SourceID && (cmd.Action == "install" || cmd.Action == "upgrade") && (cmd.Approval == nil || owner) {
		return OperationStatus{}, ErrForbidden
	}
	if old, ok := m.journal.Operations[cmd.OperationID]; ok {
		if !old.Command.same(cmd) || old.Actor != actor {
			return OperationStatus{}, ErrConflict
		}
		if old.Async && old.Status.Phase == string(lifecycleAccepted) {
			if ctx.Value(upgradeExecutionKey{}) != m {
				return old.Status, ErrRecoveryRequired
			}
			accepted = true
			op = old
		} else if old.Status.Phase == string(lifecycleFailed) {
			// The answer stays the failure, but the add-on must not stay
			// down because the failing attempt could not put it back.
			if e := m.syncRuntime(ctx, cmd.AddonID); e != nil {
				return old.Status, e
			}
			return old.Status, ErrOperationFailed
		}
		if accepted {
			// Accepted work continues exactly once under the owner worker.
		} else if _, again := m.canceled[cmd.OperationID]; again && old.Status.Phase == string(lifecycleIntent) {
			resumed = true
			op = old
		} else {
			if old.Status.Phase != string(lifecycleCompleted) {
				return old.Status, ErrRecoveryRequired
			}
			return old.Status, m.syncFor(ctx, cmd.AddonID)
		}
	}
	pinned := cmd.ExpectedDigest != "" || cmd.CandidateDigest != ""
	if pinned && (cmd.Action != "upgrade" || cmd.ExpectedDigest == "" || cmd.CandidateDigest == "") {
		return OperationStatus{}, ErrConflict
	}
	if pinned && !resumed && !accepted {
		// The same transition under another operation ID is a duplicate,
		// whether the first one is in flight or done. Only a failed attempt
		// leaves the transition open for a new one.
		for id, other := range m.journal.Operations {
			c := other.Command
			if id != cmd.OperationID && sameTransition(cmd, c, other.Status) {
				return OperationStatus{}, ErrTransitionConflict
			}
		}
	}
	if !resumed && !accepted {
		for _, other := range m.journal.Operations {
			if other.Command.AddonID == cmd.AddonID && isActive(other.Status.Phase) {
				return OperationStatus{}, ErrRecoveryRequired
			}
		}
	}
	next := copyStatus(m.journal.Addons[cmd.AddonID])
	// release is the candidate an explicit upgrade resolved at execution.
	var release *catalog.Release
	switch cmd.Action {
	case "install":
		if next.Installed && !resumed {
			return OperationStatus{}, ErrConflict
		}
		// Set once, here: the default provider of a shadowed tool or skill is
		// the oldest install, so an upgrade must not move it.
		if next.InstalledAt == "" || !next.Installed {
			next.InstalledAt = time.Now().UTC().Format(time.RFC3339)
		}
	case "upgrade", "enable", "disable", "uninstall":
		if !next.Installed {
			return OperationStatus{}, ErrConflict
		}
		// A caller that names the generation it saw must not change a newer
		// one. This precedes the intent record, so a stale request leaves
		// nothing behind and cannot block the addon.
		if (cmd.Action == "enable" || cmd.Action == "disable") && cmd.Generation != 0 && cmd.Generation != next.Generation {
			return OperationStatus{}, ErrStaleGeneration
		}
		if pinned && !resumed {
			r, e := m.checkPins(ctx, cmd, next)
			if e != nil {
				return OperationStatus{}, e
			}
			release = &r
		}
	default:
		return OperationStatus{}, ErrConflict
	}
	if accepted {
		phase, _, err := upgradeTransition(lifecyclePhase(op.Status.Phase), upgradeStart)
		if err != nil {
			// A phase the reducer does not know cannot be run. End the
			// operation as failed instead of leaving it half-owned.
			xlog.Warn("Addon operation has an unknown phase", "operationId", cmd.OperationID, "phase", op.Status.Phase)
			op.Status.Phase = string(lifecycleFailed)
			op.Status.FailureCode = "upgrade_failed"
			m.journal.Operations[cmd.OperationID] = op
			if e := m.persist(string(lifecycleFailed)); e != nil {
				return op.Status, e
			}
			return op.Status, ErrOperationFailed
		}
		op.Status.Phase = string(phase)
		m.journal.Operations[cmd.OperationID] = op
		if e := m.persist(string(lifecycleRunning)); e != nil {
			return op.Status, e
		}
	} else if !resumed {
		op = journalOperation{Command: cmd, Actor: actor, Status: OperationStatus{OperationID: cmd.OperationID, AddonID: cmd.AddonID, Phase: string(lifecycleIntent)}}
		m.journal.Operations[cmd.OperationID] = op
		if e := m.persist(string(lifecycleIntent)); e != nil {
			return op.Status, e
		}
	}
	// fail ends the operation. A cause that is an SDK refusal keeps its text for
	// the administrator, and so does a package that failed verification; the
	// returned error still matches ErrOperationFailed.
	fail := func(cause ...error) (OperationStatus, error) {
		// The client gets a typed cause or the generic error; the operator
		// gets the underlying error here, which would otherwise be lost.
		xlog.Warn("Addon operation failed", "action", cmd.Action, "addonId", cmd.AddonID, "operationId", cmd.OperationID, "causes", describeCauses(cause))
		op.Status.Stage = m.journal.Operations[cmd.OperationID].Status.Stage
		op.Status.Phase = string(lifecycleFailed)
		op.Status.FailureCode = "upgrade_failed"
		for _, c := range cause {
			if errors.Is(c, context.Canceled) || errors.Is(c, context.DeadlineExceeded) {
				op.Status.FailureCode = "interrupted"
			}
			if errors.Is(c, recovery.ErrNeedsRecovery) {
				op.Status.FailureCode = "recovery_required"
			}
		}
		m.journal.Operations[cmd.OperationID] = op
		if e := m.persist(string(lifecycleFailed)); e != nil {
			return op.Status, e
		}
		for _, c := range cause {
			var refusal *contract.SDKRefusal
			if errors.As(c, &refusal) {
				return op.Status, fmt.Errorf("%w: %w", ErrOperationFailed, refusal)
			}
			if errors.Is(c, packages.ErrPackage) {
				// The package is not what its library entry says. The text of
				// ErrPackage is fixed, so it is safe to show.
				return op.Status, fmt.Errorf("%w: %w", ErrOperationFailed, packages.ErrPackage)
			}
			var admission *HostRequirementsRefusal
			if errors.As(c, &admission) {
				// The operation is journaled as failed already; tell the
				// administrator apart "approve this" from "policy blocks this".
				op.Status.FailureCode = admission.Code()
				m.journal.Operations[cmd.OperationID] = op
				if e := m.persist(string(lifecycleFailed)); e != nil {
					return op.Status, e
				}
				return op.Status, fmt.Errorf("%w: %w", ErrOperationFailed, admission)
			}
			var stale *ApprovalStaleError
			if errors.As(c, &stale) {
				op.Status.FailureCode = "approval_stale"
				m.journal.Operations[cmd.OperationID] = op
				if e := m.persist(string(lifecycleFailed)); e != nil {
					return op.Status, e
				}
				return op.Status, fmt.Errorf("%w: %w", ErrOperationFailed, stale)
			}
			var typed *FailureCause
			if errors.As(c, &typed) {
				return op.Status, fmt.Errorf("%w: %w", ErrOperationFailed, typed)
			}
		}
		return op.Status, ErrOperationFailed
	}
	if next.Identity == "" {
		next.AddonID = cmd.AddonID
		next.Identity = m.config.StorageID + ":" + cmd.AddonID
		next.DataDirectory = filepath.Join(m.config.Root, "data", cmd.AddonID)
		next.GrantsPath = filepath.Join(m.config.Root, "metadata", cmd.AddonID, "grants.json")
	}
	switch cmd.Action {
	case "install", "upgrade":
		receipt, published := m.canceled[cmd.OperationID]
		delete(m.canceled, cmd.OperationID)
		if !published {
			// Resolving a release and staging a package fetch and verify over
			// the network and the disk. They touch neither the journal nor
			// the runtime, so the state lock is released: reads stay
			// available, and opMu keeps every other operation out.
			var r catalog.Release
			var resolveErr error
			if e := m.upgradeStage(cmd.OperationID, "preparing-package"); e != nil {
				return op.Status, e
			}
			m.mu.Unlock()
			fromFile := cmd.SourceID == zipformat.SourceID
			var staged packages.StagedPackage
			var stageErr error
			if fromFile {
				// A local file has no library entry: the staged file is the
				// release, and the same Stage checks apply to its contents.
				staged, stageErr = m.stageUpload(ctx, actor, cmd.Version)
			} else {
				if release != nil {
					r = *release
				} else {
					r, resolveErr = m.resolveRelease(ctx, cmd)
				}
				if resolveErr == nil {
					staged, stageErr = m.installer.Stage(ctx, r, nil)
				}
			}
			m.mu.Lock()
			if resolveErr != nil {
				return fail(resolveErr, &FailureCause{Code: resolveCauseCode(resolveErr), detail: resolveErr})
			}
			if stageErr != nil {
				if errors.Is(stageErr, uploads.ErrNotFound) {
					return fail(stageErr, &FailureCause{Code: CauseFileMissing, detail: stageErr})
				}
				return fail(stageErr, &FailureCause{Code: CausePackageFetch, detail: stageErr})
			}
			if fromFile {
				if cause := m.fileVersionRefusal(cmd, next, staged.Receipt()); cause != nil {
					if discardErr := m.installer.Discard(staged); discardErr != nil {
						xlog.Warn("Could not discard refused addon stage", "addon", cmd.AddonID, "error", discardErr)
					}
					return fail(cause)
				}
				if staged.Receipt().Manifest.ID != cmd.AddonID {
					if discardErr := m.installer.Discard(staged); discardErr != nil {
						xlog.Warn("Could not discard refused addon stage", "addon", cmd.AddonID, "error", discardErr)
					}
					return fail(packages.ErrPackage)
				}
			}
			if m.uncertain || m.runtimeRecoveryRequired {
				return op.Status, ErrRecoveryRequired
			}
			grant, blanket, e := m.approvedGrant(ctx, cmd, staged.Receipt())
			if e != nil {
				op.Status.HostOperations = summaryPointer(m.requests(staged.Receipt()))
				if discardErr := m.installer.Discard(staged); discardErr != nil {
					xlog.Warn("Could not discard refused addon stage", "addon", cmd.AddonID, "error", discardErr)
				}
				return fail(e)
			}
			op.Status.HostOperations = summaryPointer(m.requests(staged.Receipt()))
			// The grants are written before the journal commits the operation,
			// and removed again when it does not complete. They take effect
			// only once it has. Until then admission sees them as pending.
			if len(grant) > 0 {
				if e = m.stageGrants(ctx, cmd, actor, staged.Receipt(), next, grant, blanket); e != nil {
					m.setPending(cmd.AddonID, nil)
					if discardErr := m.installer.Discard(staged); discardErr != nil {
						xlog.Warn("Could not discard refused addon stage", "addon", cmd.AddonID, "error", discardErr)
					}
					return fail(e)
				}
				defer m.endGrants(cmd.OperationID, cmd.AddonID)
			} else {
				m.setPending(cmd.AddonID, nil)
			}
			if release != nil {
				// An explicit upgrade never changes the trusted identity,
				// the library or the channel, whatever the add-on holds.
				if last, ok := lastReceipt(m.journal.Addons[cmd.AddonID]); !ok || recovery.CheckCompatible(last, staged.Receipt()) != nil || staged.Receipt().Digest != cmd.CandidateDigest {
					return fail()
				}
			}
			if m.recovery != nil && m.recovery.HasData(cmd.AddonID) {
				// Retained data is only handed to the same trusted identity
				// and never to an older data format: this holds for an
				// upgrade and for a reinstall alike.
				if last, ok := lastReceipt(m.journal.Addons[cmd.AddonID]); ok && recovery.CheckCompatible(last, staged.Receipt()) != nil {
					return fail()
				}
				if cmd.Action != "upgrade" {
					// A reinstall has no running generation to back up, and
					// the candidate would migrate the retained data when it
					// starts. Only an upgrade takes a verified backup first.
					if last, ok := lastReceipt(m.journal.Addons[cmd.AddonID]); ok && recovery.FormatChanged(last, staged.Receipt()) {
						return fail()
					}
				}
				if cmd.Action == "upgrade" {
					return m.upgradeWithRecovery(ctx, cmd, op, &next, staged, fail)
				}
			}
			if e = m.upgradeStage(cmd.OperationID, "committing-package"); e != nil {
				return op.Status, e
			}
			receipt, e = m.installer.Commit(ctx, staged)
			if e != nil {
				return fail(&FailureCause{Code: CauseInstallStorage, detail: fmt.Errorf("commit: %w", e)})
			}
			// Installer publication alone is not a power-loss durability promise. Flush
			// every immutable file and directory before journaling its generation.
			if e = syncPackage(receipt.Directory); e != nil {
				return fail(&FailureCause{Code: CauseInstallStorage, detail: fmt.Errorf("sync package: %w", e)})
			}
			if m.boundary != nil {
				if e = m.boundary("published"); e != nil {
					m.uncertain = true
					return op.Status, e
				}
			}
		}
		next.Generation++
		next.Generations = append(next.Generations, Generation{Number: next.Generation, Receipt: receipt})
		next.Installed = true
		// A new generation of a running add-on must prove it can start before
		// it replaces the last good one in the journal. When it cannot, the
		// last good generation runs again and the operation fails.
		if cmd.Action == "upgrade" {
			if e := m.startCandidate(ctx, cmd.AddonID, m.journal.Addons[cmd.AddonID], receipt, next.Generation); e != nil {
				if ctx.Err() != nil {
					// The caller went away, which says nothing about the
					// candidate. Keep the operation open so the same ID
					// can continue; the last good generation is running.
					if m.canceled == nil {
						m.canceled = map[string]packages.PackageReceipt{}
					}
					m.canceled[cmd.OperationID] = receipt
					return op.Status, e
				}
				return fail()
			}
		}
	case "enable":
		if e := m.admission(cmd.AddonID); e != nil {
			return fail(e)
		}
		// Data that needs recovery is not served: it is restored or purged first.
		if m.dataBlocked(cmd.AddonID) {
			return fail()
		}
		next.DesiredEnabled = true
	case "disable":
		next.DesiredEnabled = false
	case "uninstall":
		if e := m.removeGrants(cmd.AddonID); e != nil {
			return fail(&FailureCause{Code: CauseInstallStorage, detail: fmt.Errorf("delete the host grants: %w", e)})
		}
		next.Installed = false
		next.DesiredEnabled = false
	}
	if e := os.MkdirAll(filepath.Dir(next.GrantsPath), 0700); e != nil {
		return fail(&FailureCause{Code: CauseInstallStorage, detail: fmt.Errorf("create the grants folder: %w", e)})
	}
	op.Next = &next
	op.Status.Generation = next.Generation
	op.Status.Phase = string(lifecyclePrepared)
	m.journal.Operations[cmd.OperationID] = op
	if e := m.persist(string(lifecyclePrepared)); e != nil {
		return op.Status, e
	}
	result, e := m.apply(op)
	if e != nil {
		if m.runtime != nil {
			m.runtimeRecoveryRequired = true
			_ = m.runtime.Close(context.Background())
		}
		return result, e
	}
	switch cmd.Action {
	case "install", "upgrade", "uninstall":
		m.syncSkills(ctx, cmd.AddonID)
	}
	if cmd.SourceID == zipformat.SourceID && (cmd.Action == "install" || cmd.Action == "upgrade") {
		// The package is installed; the staged file has done its work.
		m.dropUpload(actor, cmd.Version)
	}
	return result, m.syncFor(ctx, cmd.AddonID)
}

// describeCauses is the text of the errors a failed operation had, for the log.
func describeCauses(causes []error) string {
	var parts []string
	for _, c := range causes {
		var typed *FailureCause
		if errors.As(c, &typed) && typed.detail != nil {
			parts = append(parts, typed.Code+": "+typed.detail.Error())
		} else if c != nil {
			parts = append(parts, c.Error())
		}
	}
	if len(parts) == 0 {
		return "no cause recorded (a check refused the operation)"
	}
	return strings.Join(parts, "; ")
}

// resolveCauseCode is the cause code of a release that could not be resolved.
func resolveCauseCode(e error) string {
	switch {
	case errors.Is(e, catalog.ErrNotFound):
		return CauseReleaseNotFound
	case errors.Is(e, catalog.ErrConflict), errors.Is(e, catalog.ErrShadowed):
		return CauseLibraryConflict
	}
	return CauseLibraryUnavailable
}

// resolveRelease finds the release a command names. The library cache is
// filled by a refresh at startup, which runs in the background and can fail
// once (a slow start, a library that is not up yet) and leave the cache empty
// until someone refreshes. An install must not depend on that: when the cache
// has nothing for the library, read the library once now and look again.
func (m *Manager) resolveRelease(ctx context.Context, cmd LifecycleCommand) (catalog.Release, error) {
	r, e := m.catalog.Resolve(ctx, cmd.SourceID, cmd.AddonID, cmd.Version)
	if e == nil || !(errors.Is(e, catalog.ErrUnavailable) || errors.Is(e, catalog.ErrRefresh)) || m.catalog.Sources == nil {
		return r, e
	}
	if _, rerr := m.catalog.Sources.Refresh(ctx, cmd.SourceID); rerr != nil {
		return catalog.Release{}, fmt.Errorf("%w (library read on demand: %w)", e, rerr)
	}
	return m.catalog.Resolve(ctx, cmd.SourceID, cmd.AddonID, cmd.Version)
}

func (m *Manager) apply(op journalOperation) (OperationStatus, error) {
	m.journal.Addons[op.Command.AddonID] = copyStatus(*op.Next)
	op.Status.Phase = string(lifecycleApplied)
	m.journal.Operations[op.Command.OperationID] = op
	if e := m.persist(string(lifecycleApplied)); e != nil {
		return op.Status, e
	}
	op.Status.Phase = string(lifecycleCompleted)
	op.Next = nil
	m.journal.Operations[op.Command.OperationID] = op
	e := m.persist(string(lifecycleCompleted))
	if e == nil {
		// The identity first: an entry whose operation completed must not meet
		// an add-on the Authority does not know yet.
		m.noteIdentity(op.Command.AddonID, m.journal.Addons[op.Command.AddonID])
		m.markCompleted(op.Command.OperationID)
	}
	return op.Status, e
}
func (m *Manager) Reconcile(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return e
	}
	// A supervisor closed after an uncertain publication cannot be reused.
	// Keep ownership fenced until Close succeeds and a new manager reconciles.
	if m.runtimeRecoveryRequired {
		return ErrRecoveryRequired
	}
	if e := m.load(); e != nil {
		return e
	}
	m.uncertain = false
	if e := m.cleanStages(); e != nil {
		return e
	}
	for id, op := range m.journal.Operations {
		if e := ctx.Err(); e != nil {
			return e
		}
		switch lifecyclePhase(op.Status.Phase) {
		case lifecycleAccepted, lifecycleRunning, lifecycleIntent:
			op.Status.Phase = string(lifecycleFailed)
			op.Status.FailureCode = "interrupted"
			m.journal.Operations[id] = op
			if e := m.persist(string(lifecycleFailed)); e != nil {
				return e
			}
		case lifecyclePrepared, lifecycleApplied:
			if op.Next == nil {
				return ErrConfiguration
			}
			if _, e := m.apply(op); e != nil {
				return e
			}
		case lifecycleCompleted, lifecycleFailed:
		default:
			return ErrConfiguration
		}
	}
	m.reconcileData(ctx)
	for id, op := range m.journal.Operations {
		if op.Async && op.Status.Phase == string(lifecycleFailed) && m.dataBlocked(op.Command.AddonID) {
			op.Status.FailureCode = "recovery_required"
			m.journal.Operations[id] = op
		}
	}
	if e := m.persist("reconciled"); e != nil {
		return e
	}
	// Grants follow the journal: a crash between the two files leaves entries
	// of operations that never completed and of add-ons that are gone.
	if e := m.reconcileGrantsLocked(ctx); e != nil {
		xlog.Warn("Could not reconcile the host grants with the journal", "error", e)
	}
	return m.syncRuntime(ctx, "")
}
func syncPackage(root string) error {
	var dirs []string
	e := filepath.Walk(root, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if info.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		f, e := os.Open(path) // #nosec G122,G304 -- the tree is owned by this process and locked; the walk only normalises its own files
		if e != nil {
			return e
		}
		e = f.Sync()
		return errors.Join(e, f.Close())
	})
	if e != nil {
		return e
	}
	dirs = append([]string{filepath.Dir(root)}, dirs...)
	for n := len(dirs) - 1; n >= 0; n-- {
		f, e := os.Open(dirs[n])
		if e != nil {
			return e
		}
		e = f.Sync()
		e = errors.Join(e, f.Close())
		if e != nil {
			return e
		}
	}
	return nil
}

// Only unpublished Installer staging is collectible. An unreferenced published
// package may be the result of a crash before prepared; retain it for inspection.
func (m *Manager) cleanStages() error {
	root, e := openPackageRoot(m.installer.Root)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	defer func() { _ = root.Close() }()
	entries, e := fs.ReadDir(root.FS(), ".")
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "stage-") {
			continue
		}
		if e = fs.WalkDir(root.FS(), entry.Name(), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			// Descriptor chmod avoids following a link swapped in after WalkDir.
			f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
			if err != nil {
				return err
			}
			return errors.Join(f.Chmod(0700), f.Close()) // #nosec G122 -- the tree is owned by this process and locked; the walk only normalises its own files
		}); e != nil {
			return e
		}
		if e = root.RemoveAll(entry.Name()); e != nil {
			return e
		}
	}
	return nil
}

// Pin each ancestor and reject links, including the package root, before any
// enumeration or mutation. Root operations keep later path races contained.
func openPackageRoot(path string) (*os.Root, error) {
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator)), string(filepath.Separator)) {
		info, e := root.Lstat(part)
		if e != nil {
			_ = root.Close()
			return nil, e
		}
		if !info.IsDir() {
			_ = root.Close()
			return nil, ErrConfiguration
		}
		next, e := root.OpenRoot(part)
		_ = root.Close()
		if e != nil {
			return nil, e
		}
		pinned, e := next.Stat(".")
		if e != nil || !os.SameFile(info, pinned) {
			_ = next.Close()
			return nil, ErrConfiguration
		}
		root = next
	}
	return root, nil
}

// AttachRuntime is owner-only. Durable reconciliation precedes process execution.
func (m *Manager) AttachRuntime(ctx context.Context, s *addonruntime.Supervisor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return e
	}
	if m.uncertain || m.runtime != nil || s == nil {
		return ErrConflict
	}
	m.hostOpsSet = true // An omitted startup policy is an immutable empty grant set.
	m.runtime = s
	m.instances = map[string]addonruntime.Instance{}
	m.startFailed = map[string]error{}
	m.reconcileData(ctx)
	return m.syncRuntime(ctx, "")
}

// syncFor synchronizes the runtime and reports the start failure of the one
// add-on a command is about. The other add-ons keep their own state.
func (m *Manager) syncFor(ctx context.Context, id string) error {
	if e := m.syncRuntime(ctx, id); e != nil {
		return e
	}
	return m.startFailed[id]
}

// startCandidate stops the running generation of an enabled add-on and starts
// the one that an upgrade published. If the candidate does not become ready,
// the previous generation is started again, so a failed upgrade never leaves
// the add-on stopped on a generation that never ran. Without a running
// instance there is nothing to replace and nothing to prove here.
func (m *Manager) startCandidate(ctx context.Context, id string, prev Status, receipt packages.PackageReceipt, number uint64) error {
	if e := m.admit(receipt); e != nil {
		return e
	}
	if m.runtime == nil || !prev.Installed || !prev.DesiredEnabled {
		return nil
	}
	instance, running := m.instances[id]
	if !running || instance.Generation != prev.Generation {
		return nil
	}
	// Stopping and restoring belong to the owner, not to the request: a
	// cancellation must not leave the last good generation stopped, and the
	// old and the new process never run at once.
	detached, release := m.detached(ctx)
	defer release()
	if e := m.runtime.Stop(detached, instance); e != nil {
		return e
	}
	delete(m.instances, id)
	started, e := m.runtime.Start(ctx, receipt, number)
	if e == nil {
		m.instances[id] = started
		delete(m.startFailed, id)
		return nil
	}
	for _, g := range prev.Generations {
		if g.Number != prev.Generation {
			continue
		}
		back, be := m.runtime.Start(detached, g.Receipt, g.Number)
		if be == nil {
			m.instances[id] = back
			delete(m.startFailed, id)
			continue
		}
		// Recorded so that the next command on this add-on, with any
		// operation ID, tries the journaled generation again.
		m.startFailed[id] = be
	}
	return e
}

// detached returns a bounded context that a canceled request cannot cancel.
func (m *Manager) detached(ctx context.Context) (context.Context, context.CancelFunc) {
	d := m.restoreTimeout
	if d <= 0 {
		d = 2 * time.Minute
	}
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}

// syncRuntime makes the running processes match the journal. A start failure
// is recorded per add-on and does not fail the call: only the add-on named by
// target is tried again when it already failed, so an unrelated command never
// waits for a broken add-on. A cancelled context and a failure to stop a
// process are returned.
func (m *Manager) syncRuntime(ctx context.Context, target string) error {
	if m.runtimeRecoveryRequired {
		return ErrRecoveryRequired
	}
	if m.runtime == nil {
		return nil
	}
	for id, instance := range m.instances {
		if m.quiesced[id] {
			continue
		}
		state := m.journal.Addons[id]
		// An add-on that lost an operation it requires stops, as one that
		// is disabled does. Its running calls finish; the next one is refused.
		if !state.Installed || !state.DesiredEnabled || state.Generation != instance.Generation || m.admission(id) != nil {
			if e := m.runtime.Stop(ctx, instance); e != nil {
				return e
			}
			delete(m.instances, id)
		}
	}
	type launch struct {
		id         string
		generation Generation
		instance   addonruntime.Instance
		err        error
	}
	var launches []*launch
	for id, state := range m.journal.Addons {
		if !state.Installed || !state.DesiredEnabled {
			delete(m.startFailed, id)
			continue
		}
		if _, ok := m.instances[id]; ok {
			continue
		}
		if _, failed := m.startFailed[id]; failed && id != target {
			continue
		}
		if e := m.admission(id); e != nil {
			m.startFailed[id] = e
			continue
		}
		if m.quiesced[id] || m.dataBlocked(id) {
			continue
		}
		for _, generation := range state.Generations {
			if generation.Number == state.Generation {
				launches = append(launches, &launch{id: id, generation: generation})
				break
			}
		}
	}
	// Each add-on is a separate process that takes seconds to be ready, so
	// they start side by side. The bound keeps a host with many add-ons from
	// forking them all at once. The results are applied below, in the order of
	// the journal, by this goroutine alone: the workers touch nothing of the
	// manager.
	slices.SortFunc(launches, func(a, b *launch) int { return strings.Compare(a.id, b.id) })
	slots := make(chan struct{}, startConcurrency)
	var wg sync.WaitGroup
	for _, l := range launches {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			l.instance, l.err = m.runtime.Start(ctx, l.generation.Receipt, l.generation.Number)
		}()
	}
	wg.Wait()
	var cancelled error
	for _, l := range launches {
		if l.err != nil {
			if ctx.Err() != nil {
				cancelled = l.err
				continue
			}
			m.startFailed[l.id] = l.err
			continue
		}
		delete(m.startFailed, l.id)
		m.instances[l.id] = l.instance
	}
	return cancelled
}

// startConcurrency bounds how many add-on processes start at the same time.
const startConcurrency = 4

// Runtime returns the current ready incarnation, not the lifecycle stop handle
// retained in instances. Authorization and operation replay decisions remain the
// caller's responsibility; this accessor never dispatches or retries an operation.
func (m *Manager) Runtime(ctx context.Context, id string) (addonruntime.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return addonruntime.Instance{}, e
	}
	state := m.journal.Addons[id]
	if m.admission(id) != nil {
		return addonruntime.Instance{}, addonruntime.ErrRuntime
	}
	if m.uncertain || m.runtimeRecoveryRequired || m.runtime == nil || !state.Installed || !state.DesiredEnabled {
		return addonruntime.Instance{}, addonruntime.ErrRuntime
	}
	return m.runtime.Current(ctx, id, state.Generation)
}

// ConnectorKind is a kind of connector binding that a running add-on serves.
type ConnectorKind struct {
	AddonID string
	contract.ConnectorKind
}

// ConnectorKinds lists the connector kinds of the add-ons that run now. An
// add-on that is installed but not ready serves none, and a process that does
// not own the runtime serves none.
func (m *Manager) ConnectorKinds(ctx context.Context) ([]ConnectorKind, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return nil, e
	}
	if m.uncertain || m.runtimeRecoveryRequired || m.runtime == nil {
		return nil, addonruntime.ErrRuntime
	}
	registrations, e := m.runtime.Registry.Snapshot(ctx)
	if e != nil {
		return nil, e
	}
	var out []ConnectorKind
	for _, r := range registrations {
		if r.Capabilities.Connector == nil {
			continue
		}
		for _, k := range r.Capabilities.Connector.Kinds {
			out = append(out, ConnectorKind{AddonID: r.AddonID, ConnectorKind: k})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddonID+"/"+out[i].ID < out[j].AddonID+"/"+out[j].ID })
	return out, nil
}

// Connector calls the connector route of a running add-on.
func (m *Manager) Connector(ctx context.Context, addonID string, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	instance, e := m.Runtime(ctx, addonID)
	if e != nil {
		return contract.ConnectorResponse{}, e
	}
	cr, ok := instance.Client.(contract.ConnectorRuntime)
	if !ok {
		return contract.ConnectorResponse{}, addonruntime.ErrRuntime
	}
	return cr.Connector(ctx, req)
}

// OperationStatus only observes lifecycle intent. It never resumes a command or
// infers runtime completion from a completed journal publication.
func (m *Manager) OperationStatus(ctx context.Context, actor contract.Actor, id string) (OperationStatus, error) {
	m.upgradeMu.Lock()
	view, found := m.upgradeViews[id]
	closing := m.upgradeClosing
	m.upgradeMu.Unlock()
	if found {
		if closing {
			return OperationStatus{}, ErrNotOwner
		}
		if m.admin == nil || m.admin(ctx, actor) != nil {
			return OperationStatus{}, ErrForbidden
		}
		if view.Actor != actor {
			return OperationStatus{}, ErrOperationNotFound
		}
		if err := ctx.Err(); err != nil {
			return OperationStatus{}, err
		}
		return view.Status, nil
	}
	// A slow lifecycle publication must not strand disconnected status readers.
	for !m.mu.TryLock() {
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return OperationStatus{}, ctx.Err()
		case <-timer.C:
		}
	}
	defer m.mu.Unlock()
	if err := m.check(ctx); err != nil {
		return OperationStatus{}, err
	}
	if m.admin == nil || m.admin(ctx, actor) != nil {
		return OperationStatus{}, ErrForbidden
	}
	op, ok := m.journal.Operations[id]
	if !ok || op.Actor.UserID == "" || op.Actor.CredentialID == "" || (op.Actor.Mode != "named-user" && op.Actor.Mode != "local-owner") || op.Actor != actor {
		return OperationStatus{}, ErrOperationNotFound
	}
	result := OperationStatus{OperationID: id, AddonID: op.Status.AddonID, Phase: op.Status.Phase, Generation: op.Status.Generation, RuntimeState: "not-observed", HostOperations: copyRequestSummary(op.Status.HostOperations)}
	if m.uncertain || m.runtimeRecoveryRequired {
		result.RuntimeState = string(lifecycleRecovery)
	}
	return result, nil
}

var ErrOperationNotFound = errors.New("addon lifecycle operation not found")

// Apps returns the apps the actor may see. The owner decides who is an
// administrator from its own check, never from the request; a check that
// cannot answer yields the reader's view, which has fewer rights.
//
// Visibility in version one: an addon that is installed and desired-enabled is
// visible to every authenticated caller who holds the addons feature, which
// the gateway checks first. No per-addon ACL exists; data access stays under
// per-resource ACLs on every call. An administrator also sees installed addons
// that are disabled, so a stopped addon can be started.
func (m *Manager) Apps(ctx context.Context, actor contract.Actor) ([]apps.App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return nil, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return nil, ErrRecoveryRequired
	}
	isAdmin := m.admin != nil && m.admin(ctx, actor) == nil
	snaps := make([]apps.Snapshot, 0, len(m.journal.Addons))
	for id, st := range m.journal.Addons {
		receipt, ok := currentReceipt(st)
		if !ok {
			continue
		}
		snap := apps.Snapshot{AddonID: id, InstalledAt: st.InstalledAt, Installed: st.Installed, Enabled: st.DesiredEnabled, Generation: st.Generation, State: addonruntime.StateStopped, Receipt: receipt, HostOps: held(m.hostAuthority(), id)}
		if st.DesiredEnabled && m.runtime != nil {
			snap.State = m.runtime.State(id, st.Generation)
		}
		summary := m.requests(receipt)
		snap.HostOperations = summaryPointer(summary)
		if len(summary.MissingRequired) != 0 {
			snap.Unavailable = true
			snap.State = "failed"
		}
		// The apps list answers from the check cache only: it never
		// evaluates and never reaches a library.
		if m.updates != nil {
			if cached, ok := m.updates.Cached(id, receipt.Digest, st.Generation); ok && cached.State == updates.UpdateAvailable {
				snap.UpdatePending = true
			}
		}
		snaps = append(snaps, snap)
	}
	return apps.Build(snaps, isAdmin), nil
}

// InstalledIDs returns the identifiers of every installed addon, enabled or
// not, sorted. It is for the host's own housekeeping (the pin sweep); it takes
// no actor and returns no detail.
func (m *Manager) InstalledIDs(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return nil, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return nil, ErrRecoveryRequired
	}
	out := make([]string, 0, len(m.journal.Addons))
	for id, st := range m.journal.Addons {
		if st.Installed {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

func currentReceipt(st Status) (packages.PackageReceipt, bool) {
	for _, g := range st.Generations {
		if g.Number == st.Generation {
			return g.Receipt, true
		}
	}
	return packages.PackageReceipt{}, false
}

// AppAsset returns the package of an enabled addon whose current installed
// package has the given digest, and its generation. The digest is the bare
// lower-case hex of the package digest. A disabled, uninstalled or replaced
// package is the same denial for every caller, administrators included: a page
// that is still open must not be able to fetch code the owner has withdrawn.
func (m *Manager) AppAsset(ctx context.Context, actor contract.Actor, id, digest string) (packages.PackageReceipt, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return packages.PackageReceipt{}, 0, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return packages.PackageReceipt{}, 0, ErrRecoveryRequired
	}
	st, ok := m.journal.Addons[id]
	if !ok || !addonID.MatchString(id) || !st.Installed || !st.DesiredEnabled || st.Generation == 0 || len(digest) != 64 {
		return packages.PackageReceipt{}, 0, ErrForbidden
	}
	receipt, ok := currentReceipt(st)
	if !ok || m.admit(receipt) != nil || receipt.Manifest.Capabilities.UI == nil || receipt.Digest != "sha256:"+digest {
		return packages.PackageReceipt{}, 0, ErrForbidden
	}
	return receipt, st.Generation, nil
}

// AppIcon returns the receipt of an app the actor may see when the generation
// is the installed one and the package has a verified icon. Every other case
// is the same denial, so the answer reveals nothing about hidden addons.
func (m *Manager) AppIcon(ctx context.Context, actor contract.Actor, id string, generation uint64) (packages.PackageReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return packages.PackageReceipt{}, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return packages.PackageReceipt{}, ErrRecoveryRequired
	}
	st, ok := m.journal.Addons[id]
	if !ok || !addonID.MatchString(id) || !st.Installed || generation == 0 || generation != st.Generation {
		return packages.PackageReceipt{}, ErrForbidden
	}
	if !st.DesiredEnabled && (m.admin == nil || m.admin(ctx, actor) != nil) {
		return packages.PackageReceipt{}, ErrForbidden
	}
	receipt, ok := currentReceipt(st)
	if !ok || receipt.Icon == nil || receipt.Manifest.Capabilities.UI == nil || receipt.Manifest.Capabilities.UI.Icon != receipt.Icon.Path {
		return packages.PackageReceipt{}, ErrForbidden
	}
	return receipt, nil
}

// toolDeclaration is the declared tool set of one installed generation.
type toolDeclaration struct {
	generation uint64
	tools      []string
}

// DeclaredTools returns the tools the manifest of the current installed
// generation declares. It answers from the in-memory journal, cached per
// generation, and fails when the add-on is not installed, the generation is
// not the installed one (ErrForbidden, a denial), or when the journal is
// uncertain or runtime recovery is required (ErrRecoveryRequired), the
// manager is closed (ErrNotOwner) or the context is done (its error), which
// are unavailable conditions.
func (m *Manager) DeclaredTools(ctx context.Context, id string, generation uint64) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return nil, e
	}
	st, ok := m.journal.Addons[id]
	if m.uncertain || m.runtimeRecoveryRequired {
		return nil, ErrRecoveryRequired
	}
	if !ok || !st.Installed || generation == 0 || st.Generation != generation {
		return nil, ErrForbidden
	}
	if m.admission(id) != nil {
		return nil, ErrForbidden
	}
	if c, ok := m.toolCache[id]; ok && c.generation == generation {
		return c.tools, nil
	}
	receipt, ok := currentReceipt(st)
	if !ok {
		return nil, ErrForbidden
	}
	if m.toolCache == nil {
		m.toolCache = map[string]toolDeclaration{}
	}
	tools := append([]string{}, receipt.Manifest.Capabilities.Tools...)
	m.toolCache[id] = toolDeclaration{generation: generation, tools: tools}
	return tools, nil
}

// DeclaredCapabilities returns the capabilities of the manifest of the current
// installed generation: the declared tools, their definitions, the admin tools
// and the resource mapping. It answers like DeclaredTools: a stale or unknown
// generation is a denial, an uncertain journal is unavailable. The UI-only
// resource management endpoints are its only reader of the admin tools.
func (m *Manager) DeclaredCapabilities(ctx context.Context, id string, generation uint64) (contract.Capabilities, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return contract.Capabilities{}, e
	}
	st, ok := m.journal.Addons[id]
	if m.uncertain || m.runtimeRecoveryRequired {
		return contract.Capabilities{}, ErrRecoveryRequired
	}
	if !ok || !st.Installed || generation == 0 || st.Generation != generation {
		return contract.Capabilities{}, ErrForbidden
	}
	receipt, ok := currentReceipt(st)
	if !ok {
		return contract.Capabilities{}, ErrForbidden
	}
	if m.admit(receipt) != nil {
		return contract.Capabilities{}, ErrForbidden
	}
	c := receipt.Manifest.Capabilities
	out := contract.Capabilities{Service: c.Service, Tools: append([]string(nil), c.Tools...), AdminTools: append([]string(nil), c.AdminTools...), Actions: c.Actions, Undo: c.Undo}
	out.ToolDefinitions = append([]contract.ToolDefinition(nil), c.ToolDefinitions...)
	out.SkillDefinitions = append([]contract.SkillDefinition(nil), c.SkillDefinitions...)
	out.ContextFiles = append([]contract.ContextFile(nil), c.ContextFiles...)
	out.Credentials = slices.Clone(c.Credentials)
	for _, us := range c.UserSettings {
		us.Values = slices.Clone(us.Values)
		out.UserSettings = append(out.UserSettings, us)
	}
	if c.ExecutionProvider != nil {
		p := *c.ExecutionProvider
		p.Tools = slices.Clone(p.Tools)
		out.ExecutionProvider = &p
	}
	if c.HostOperations != nil {
		h := *c.HostOperations
		h.Required = slices.Clone(h.Required)
		h.Optional = slices.Clone(h.Optional)
		out.HostOperations = &h
	}
	if c.Resources != nil {
		r := *c.Resources
		out.Resources = &r
	}
	return out, nil
}
