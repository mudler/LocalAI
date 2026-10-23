// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// SetRecovery attaches the data recovery of the owner. An add-on with data is
// then upgraded through it: backed up and verified, migrated while nothing
// else touches the data, and published only when it is healthy. Add-ons
// without data keep the plain upgrade. It is owner-only and set once, before
// commands are served.
func (m *Manager) SetRecovery(r *recovery.Recovery) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recovery = r
}

// RecoveryHost returns the manager as the recovery package reaches it. Each
// call takes the manager's lock; it is for use outside a command.
func (m *Manager) RecoveryHost() recovery.UpgradeHost { return &managerHost{m: m} }

// managerHost adapts the manager to recovery.UpgradeHost. With locked set the
// caller already holds the manager's lock, as a lifecycle command does.
type managerHost struct {
	m       *Manager
	locked  bool
	publish func(ctx context.Context, receipt packages.PackageReceipt, number uint64) error
}

func (h *managerHost) lock() func() {
	if h.locked {
		return func() {}
	}
	h.m.mu.Lock()
	return h.m.mu.Unlock
}

func lastReceipt(st Status) (packages.PackageReceipt, bool) {
	if r, ok := currentReceipt(st); ok {
		return r, true
	}
	if n := len(st.Generations); n > 0 {
		return st.Generations[n-1].Receipt, true
	}
	return packages.PackageReceipt{}, false
}

func (h *managerHost) Info(ctx context.Context, id string) (recovery.Info, error) {
	defer h.lock()()
	m := h.m
	if e := m.check(ctx); e != nil {
		return recovery.Info{}, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return recovery.Info{}, ErrRecoveryRequired
	}
	st := m.journal.Addons[id]
	info := recovery.Info{Installed: st.Installed, Enabled: st.DesiredEnabled, Generation: st.Generation}
	if r, ok := lastReceipt(st); ok {
		info.Receipt = r
	}
	if _, ok := m.instances[id]; ok {
		info.Running = true
	}
	for _, g := range st.Generations {
		info.Published = append(info.Published, g.Number)
	}
	return info, nil
}

type quiesced struct {
	h  *managerHost
	id string
}

func (h *managerHost) Quiesce(ctx context.Context, id string) (recovery.Quiesced, error) {
	defer h.lock()()
	m := h.m
	if e := m.check(ctx); e != nil {
		return nil, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return nil, ErrRecoveryRequired
	}
	if m.quiesced == nil {
		m.quiesced = map[string]bool{}
	}
	if m.quiesced[id] {
		return nil, recovery.ErrBusy
	}
	if instance, ok := m.instances[id]; ok && m.runtime != nil {
		// Stopping belongs to the owner, not to the request: a canceled
		// request must not leave a half stopped process.
		dctx, release := m.detached(ctx)
		defer release()
		if e := m.runtime.Stop(dctx, instance); e != nil {
			return nil, e
		}
		delete(m.instances, id)
	}
	m.quiesced[id] = true
	return &quiesced{h: &managerHost{m: m, locked: h.locked}, id: id}, nil
}

func (q *quiesced) Resume(ctx context.Context) error {
	defer q.h.lock()()
	m := q.h.m
	delete(m.quiesced, q.id)
	if m.closed || m.uncertain || m.runtimeRecoveryRequired {
		return nil
	}
	// The start failure of this add-on is an observation like any other.
	return m.syncRuntime(ctx, q.id)
}

func (q *quiesced) Release() {
	defer q.h.lock()()
	delete(q.h.m.quiesced, q.id)
}

func (h *managerHost) Commit(ctx context.Context, candidate recovery.Candidate) (packages.PackageReceipt, error) {
	staged, ok := candidate.(packages.StagedPackage)
	if !ok {
		return packages.PackageReceipt{}, ErrConflict
	}
	defer h.lock()()
	m := h.m
	receipt, e := m.installer.Commit(ctx, staged)
	if e != nil {
		return packages.PackageReceipt{}, e
	}
	if e = syncPackage(receipt.Directory); e != nil {
		return packages.PackageReceipt{}, e
	}
	if m.boundary != nil {
		if e = m.boundary("published"); e != nil {
			m.uncertain = true
			return packages.PackageReceipt{}, e
		}
	}
	return receipt, nil
}

func (h *managerHost) StartCandidate(ctx context.Context, id string, receipt packages.PackageReceipt, number uint64) error {
	defer h.lock()()
	m := h.m
	if m.runtime == nil {
		return nil
	}
	started, e := m.runtime.Start(ctx, receipt, number)
	if e != nil {
		return e
	}
	m.instances[id] = started
	delete(m.startFailed, id)
	return nil
}

func (h *managerHost) StopCandidate(ctx context.Context, id string) error {
	defer h.lock()()
	m := h.m
	instance, ok := m.instances[id]
	if !ok || m.runtime == nil {
		return nil
	}
	if e := m.runtime.Stop(ctx, instance); e != nil {
		return e
	}
	delete(m.instances, id)
	return nil
}

func (h *managerHost) Publish(ctx context.Context, id string, receipt packages.PackageReceipt, number uint64) error {
	if h.publish == nil {
		return errors.New("no publication in progress")
	}
	return h.publish(ctx, receipt, number)
}

func (m *Manager) dataBlocked(id string) bool { return m.recovery != nil && m.recovery.Blocked(id) }

// reconcileData finishes or undoes what a crash left in the data recovery
// journals, before any process is started. An error leaves the add-on
// blocked, which is the safe state: nothing here starts anything.
func (m *Manager) reconcileData(ctx context.Context) {
	if m.recovery == nil {
		return
	}
	_ = m.recovery.With(&managerHost{m: m, locked: true}).Reconcile(ctx)
}

// upgradeWithRecovery runs the upgrade of an add-on with data through the
// recovery package. The caller holds the lock. The generation is published
// with the journal ordering of a plain upgrade, from inside the recovery
// sequence, once the candidate has migrated the data and is healthy.
func (m *Manager) upgradeWithRecovery(ctx context.Context, cmd LifecycleCommand, op journalOperation, next *Status, staged packages.StagedPackage, fail func(...error) (OperationStatus, error)) (OperationStatus, error) {
	var result OperationStatus
	var applyErr error
	applied := false
	host := &managerHost{m: m, locked: true}
	host.publish = func(_ context.Context, receipt packages.PackageReceipt, number uint64) error {
		if number != next.Generation+1 {
			return ErrConflict
		}
		next.Generation = number
		next.Generations = append(next.Generations, Generation{Number: number, Receipt: receipt})
		next.Installed = true
		if e := os.MkdirAll(filepath.Dir(next.GrantsPath), 0700); e != nil {
			return e
		}
		op.Next = next
		op.Status.Generation = next.Generation
		op.Status.Phase = "prepared"
		m.journal.Operations[cmd.OperationID] = op
		if e := m.persist("prepared"); e != nil {
			applyErr = e
			result = op.Status
			return e
		}
		var e error
		result, e = m.apply(op)
		if e != nil {
			applyErr = e
			return e
		}
		applied = true
		return nil
	}
	_, e := m.recovery.With(host).UpgradeReceipt(ctx, cmd.AddonID, staged)
	switch {
	case applyErr != nil:
		if m.runtime != nil {
			m.runtimeRecoveryRequired = true
			_ = m.runtime.Close(context.Background())
		}
		return result, applyErr
	case applied:
		return result, m.syncFor(ctx, cmd.AddonID)
	case e != nil:
		return fail()
	}
	return fail()
}

// Recovery returns the data recovery of the owner for an administrator. It
// is the one entry the management routes use: the administrator check is the
// manager's own, never the request's.
func (m *Manager) Recovery(ctx context.Context, actor contract.Actor) (*recovery.Recovery, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return nil, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recovery == nil {
		return nil, ErrNotOwner
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return nil, ErrRecoveryRequired
	}
	return m.recovery, nil
}

// DataRecovery returns the data recovery without an administrator check. It
// is for the owner's own startup wiring, never for a request.
func (m *Manager) DataRecovery() *recovery.Recovery {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recovery
}
