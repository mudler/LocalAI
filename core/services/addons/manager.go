// SPDX-License-Identifier: MIT
// Package addons owns durable addon intent on one explicitly designated host.
package addons

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
)

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
	mu                      sync.Mutex
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
	// scanner reads the size of data directories outside mu.
	scanner storageScanner
}

func NewManager(ctx context.Context, cfg OwnerConfig, c catalog.Catalog, i *packages.Installer, admin catalog.AdminCheck) (*Manager, error) {
	if e := cfg.validate(); e != nil {
		return nil, e
	}
	m := &Manager{config: cfg, catalog: c, installer: i, admin: admin}
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
	return m, nil
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed && m.release == nil {
		return nil
	}
	m.closed = true
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
	for _, op := range m.journal.Operations {
		if op.Command.AddonID == id && op.Status.Phase != "completed" && op.Status.Phase != "failed" {
			s.StartEligible = false
		}
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return OperationStatus{}, e
	}
	if m.admin == nil || m.admin(ctx, actor) != nil {
		return OperationStatus{}, ErrForbidden
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return OperationStatus{}, ErrRecoveryRequired
	}
	if !addonID.MatchString(cmd.AddonID) || cmd.OperationID == "" || len(cmd.OperationID) > 256 {
		return OperationStatus{}, ErrConflict
	}
	if old, ok := m.journal.Operations[cmd.OperationID]; ok {
		if old.Command != cmd || old.Actor != actor {
			return OperationStatus{}, ErrConflict
		}
		if old.Status.Phase == "failed" {
			return old.Status, ErrOperationFailed
		}
		if old.Status.Phase != "completed" {
			return old.Status, ErrRecoveryRequired
		}
		return old.Status, m.syncFor(ctx, cmd.AddonID)
	}
	for _, op := range m.journal.Operations {
		if op.Command.AddonID == cmd.AddonID && op.Status.Phase != "completed" && op.Status.Phase != "failed" {
			return OperationStatus{}, ErrRecoveryRequired
		}
	}
	next := copyStatus(m.journal.Addons[cmd.AddonID])
	switch cmd.Action {
	case "install":
		if next.Installed {
			return OperationStatus{}, ErrConflict
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
	default:
		return OperationStatus{}, ErrConflict
	}
	op := journalOperation{Command: cmd, Actor: actor, Status: OperationStatus{OperationID: cmd.OperationID, AddonID: cmd.AddonID, Phase: "intent"}}
	m.journal.Operations[cmd.OperationID] = op
	if e := m.persist("intent"); e != nil {
		return op.Status, e
	}
	fail := func() (OperationStatus, error) {
		op.Status.Phase = "failed"
		m.journal.Operations[cmd.OperationID] = op
		if e := m.persist("failed"); e != nil {
			return op.Status, e
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
		r, e := m.catalog.Resolve(ctx, cmd.SourceID, cmd.AddonID, cmd.Version)
		if e != nil {
			return fail()
		}
		staged, e := m.installer.Stage(ctx, r, nil)
		if e != nil {
			return fail()
		}
		receipt, e := m.installer.Commit(ctx, staged)
		if e != nil {
			return fail()
		}
		// Installer publication alone is not a power-loss durability promise. Flush
		// every immutable file and directory before journaling its generation.
		if e = syncPackage(receipt.Directory); e != nil {
			return fail()
		}
		if m.boundary != nil {
			if e = m.boundary("published"); e != nil {
				m.uncertain = true
				return op.Status, e
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
				return fail()
			}
		}
	case "enable":
		next.DesiredEnabled = true
	case "disable":
		next.DesiredEnabled = false
	case "uninstall":
		next.Installed = false
		next.DesiredEnabled = false
	}
	if e := os.MkdirAll(filepath.Dir(next.GrantsPath), 0700); e != nil {
		return fail()
	}
	op.Next = &next
	op.Status.Generation = next.Generation
	op.Status.Phase = "prepared"
	m.journal.Operations[cmd.OperationID] = op
	if e := m.persist("prepared"); e != nil {
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
	return result, m.syncFor(ctx, cmd.AddonID)
}
func (m *Manager) apply(op journalOperation) (OperationStatus, error) {
	m.journal.Addons[op.Command.AddonID] = copyStatus(*op.Next)
	op.Status.Phase = "applied"
	m.journal.Operations[op.Command.OperationID] = op
	if e := m.persist("applied"); e != nil {
		return op.Status, e
	}
	op.Status.Phase = "completed"
	op.Next = nil
	m.journal.Operations[op.Command.OperationID] = op
	return op.Status, m.persist("completed")
}
func (m *Manager) Reconcile(ctx context.Context) error {
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
		switch op.Status.Phase {
		case "intent":
			op.Status.Phase = "failed"
			m.journal.Operations[id] = op
			if e := m.persist("failed"); e != nil {
				return e
			}
		case "prepared", "applied":
			if op.Next == nil {
				return ErrConfiguration
			}
			if _, e := m.apply(op); e != nil {
				return e
			}
		case "completed", "failed":
		default:
			return ErrConfiguration
		}
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
		f, e := os.Open(path)
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
	defer root.Close()
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
			return errors.Join(f.Chmod(0700), f.Close())
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
			root.Close()
			return nil, e
		}
		if !info.IsDir() {
			root.Close()
			return nil, ErrConfiguration
		}
		next, e := root.OpenRoot(part)
		root.Close()
		if e != nil {
			return nil, e
		}
		pinned, e := next.Stat(".")
		if e != nil || !os.SameFile(info, pinned) {
			next.Close()
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
	m.runtime = s
	m.instances = map[string]addonruntime.Instance{}
	m.startFailed = map[string]error{}
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
	if m.runtime == nil || !prev.Installed || !prev.DesiredEnabled {
		return nil
	}
	instance, running := m.instances[id]
	if !running || instance.Generation != prev.Generation {
		return nil
	}
	if e := m.runtime.Stop(ctx, instance); e != nil {
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
		// If this does not work either (a cancelled context, say), the next
		// synchronization starts the journaled generation again.
		if back, be := m.runtime.Start(ctx, g.Receipt, g.Number); be == nil {
			m.instances[id] = back
		}
	}
	return e
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
		state := m.journal.Addons[id]
		if !state.Installed || !state.DesiredEnabled || state.Generation != instance.Generation {
			if e := m.runtime.Stop(ctx, instance); e != nil {
				return e
			}
			delete(m.instances, id)
		}
	}
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
		for _, generation := range state.Generations {
			if generation.Number == state.Generation {
				instance, e := m.runtime.Start(ctx, generation.Receipt, generation.Number)
				if e != nil {
					if ctx.Err() != nil {
						return e
					}
					m.startFailed[id] = e
					break
				}
				delete(m.startFailed, id)
				m.instances[id] = instance
				break
			}
		}
	}
	return nil
}

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
	if m.uncertain || m.runtimeRecoveryRequired || m.runtime == nil || !state.Installed || !state.DesiredEnabled {
		return addonruntime.Instance{}, addonruntime.ErrRuntime
	}
	return m.runtime.Current(ctx, id, state.Generation)
}

// OperationStatus only observes lifecycle intent. It never resumes a command or
// infers runtime completion from a completed journal publication.
func (m *Manager) OperationStatus(ctx context.Context, actor contract.Actor, id string) (OperationStatus, error) {
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
	result := OperationStatus{OperationID: id, AddonID: op.Status.AddonID, Phase: op.Status.Phase, Generation: op.Status.Generation, RuntimeState: "not-observed"}
	if m.uncertain || m.runtimeRecoveryRequired {
		result.RuntimeState = "recovery-required"
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
		snap := apps.Snapshot{AddonID: id, Installed: st.Installed, Enabled: st.DesiredEnabled, Generation: st.Generation, State: addonruntime.StateStopped, Receipt: receipt}
		if st.DesiredEnabled && m.runtime != nil {
			snap.State = m.runtime.State(id, st.Generation)
		}
		snaps = append(snaps, snap)
	}
	return apps.Build(snaps, isAdmin), nil
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
	if !ok || receipt.Manifest.Capabilities.UI == nil || receipt.Digest != "sha256:"+digest {
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
