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
	mu                sync.Mutex
	config            OwnerConfig
	catalog           catalog.Catalog
	installer         *packages.Installer
	admin             catalog.AdminCheck
	release           func() error
	closed, uncertain bool
	journal           journal
	boundary          func(string) error
	runtime           *addonruntime.Supervisor
	instances         map[string]addonruntime.Instance
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
	if m.closed {
		return nil
	}
	m.closed = true
	if m.runtime != nil {
		if e := m.runtime.Close(context.Background()); e != nil {
			return e
		}
	}
	if m.release != nil {
		return m.release()
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

var addonID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func (m *Manager) Status(ctx context.Context, id string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return Status{}, e
	}
	if m.uncertain {
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
	if m.uncertain {
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
		return old.Status, nil
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
			_ = m.runtime.Close(context.Background())
		}
		return result, e
	}
	return result, m.syncRuntime(ctx)
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
	return m.syncRuntime(ctx)
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
	return m.syncRuntime(ctx)
}
func (m *Manager) syncRuntime(ctx context.Context) error {
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
			continue
		}
		if _, ok := m.instances[id]; ok {
			continue
		}
		for _, generation := range state.Generations {
			if generation.Number == state.Generation {
				instance, e := m.runtime.Start(ctx, generation.Receipt, generation.Number)
				if e != nil {
					return e
				}
				m.instances[id] = instance
				break
			}
		}
	}
	return nil
}
