// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
)

// ErrGrantsConflict is what a GrantStore returns when the revision the
// approval saw is no longer the current one. The install is refused as stale.
var ErrGrantsConflict = errors.New("host grants changed")

// ApprovalStaleError says that the package the owner staged is not the one the
// administrator approved, or that the grants changed since. Summary holds the
// true request of the staged package, so the caller can ask again.
type ApprovalStaleError struct {
	Digest  string
	Summary hostops.RequestSummary
}

func (e *ApprovalStaleError) Error() string {
	return "the add-on changed since you reviewed it; review its host access again"
}

// GrantStage is what the manager asks a GrantStore to record for one lifecycle
// operation. The entry is pending: it takes effect only when the operation
// completes in the journal (see Manager.OperationCompleted).
type GrantStage struct {
	AddonID, Identity string
	// Generation is the generation the operation creates.
	Generation  uint64
	OperationID string
	// Action is "install" or "upgrade".
	Action, Version, Digest, SourceID string
	// Ops are exact operation names, all declared by the verified manifest and
	// registered by the host, none denied, none already held.
	Ops []string
	// ExpectedRevision is the revision of the grants the administrator saw. A
	// store that finds another revision returns ErrGrantsConflict. Zero means
	// no claim, as for a blanket grant.
	ExpectedRevision uint64
	// Blanket names the blanket switch that produced the grant, such as
	// "blanket:any@rev3", or is empty for an explicit approval.
	Blanket string
	Actor   contract.Actor
}

// BlanketQuery asks whether the blanket switch grants operations to a package.
type BlanketQuery struct {
	Receipt packages.PackageReceipt
	// Required and Optional are the declared operations the host registers and
	// no rule denies, and that the add-on does not hold yet.
	Required, Optional []string
}

// GrantStore is the owner's record of the grants made in the UI. The manager
// never reads grants from it: it reads the Authority. The store is written
// before the lifecycle journal commits an operation, so a crash between the two
// leaves a pending entry that Reconcile removes.
type GrantStore interface {
	// Stage records pending grants for an operation, idempotent by operation ID.
	Stage(ctx context.Context, g GrantStage) error
	// Abort removes the pending entry of an operation that did not complete.
	Abort(ctx context.Context, operationID string) error
	// Remove deletes the grants of an uninstalled add-on. When keepHint is set
	// the store keeps a non-authoritative hint to pre-tick a later install.
	Remove(ctx context.Context, addonID string, keepHint bool) error
	// Reconcile removes the entries of add-ons that are not installed and the
	// pending entries of operations that did not complete. installed maps an
	// add-on ID to its trusted identity.
	Reconcile(ctx context.Context, installed map[string]string, completed func(operationID string) bool) error
	// Blanket returns the operations the blanket switch grants to the package
	// and the name of the switch for the audit, or nothing when the switch is
	// off or the package is out of its scope.
	Blanket(ctx context.Context, q BlanketQuery) (ops []string, name string)
}

// SetGrantStore sets the store of UI grants. The first call wins. Without one,
// an approval can grant nothing and the deployment's policy is the only source.
func (m *Manager) SetGrantStore(s GrantStore) {
	if s == nil {
		return
	}
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	if m.grants == nil {
		m.grants = s
	}
}

func (m *Manager) grantStore() GrantStore {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	return m.grants
}

// OperationCompleted reports whether a lifecycle operation completed in the
// journal. A grant staged by an operation counts only after this is true.
//
// It never takes the manager lock, so an Authority may call it from inside a
// check that the manager itself makes while it holds that lock.
func (m *Manager) OperationCompleted(operationID string) bool {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	return m.completed[operationID]
}

func (m *Manager) markCompleted(operationID string) {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	if m.completed == nil {
		m.completed = map[string]bool{}
	}
	m.completed[operationID] = true
}

// rebuildCompleted reads the completed operations and the identities of the
// installed add-ons from the journal just loaded.
func (m *Manager) rebuildCompleted() {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	m.completed = map[string]bool{}
	for id, op := range m.journal.Operations {
		if op.Status.Phase == string(lifecycleCompleted) {
			m.completed[id] = true
		}
	}
	m.identities = map[string]string{}
	for id, st := range m.journal.Addons {
		if st.Installed {
			m.identities[id] = st.Identity
		}
	}
}

// TrustedIdentity returns the trusted identity of the installed package of an
// add-on, or empty when it is not installed. A grant of the owner is bound to
// it. Like OperationCompleted it never takes the manager lock, so an Authority
// may call it from inside a check that the manager makes under that lock.
func (m *Manager) TrustedIdentity(addonID string) string {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	return m.identities[addonID]
}

// noteIdentity records the identity an applied operation left behind.
func (m *Manager) noteIdentity(addonID string, st Status) {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	if m.identities == nil {
		m.identities = map[string]string{}
	}
	if st.Installed {
		m.identities[addonID] = st.Identity
		return
	}
	delete(m.identities, addonID)
}

// pendingAuthority adds the operations an install or upgrade in flight is
// about to be granted. It lets admission see them before the operation
// completes, and nothing else does: the broker asks the real Authority.
type pendingAuthority struct {
	hostops.Authority
	addonID string
	extra   []string
}

func (p pendingAuthority) Allows(addonID, op string) (bool, hostops.Reason) {
	ok, why := p.Authority.Allows(addonID, op)
	if !ok && !why.Denied() && addonID == p.addonID && slices.Contains(p.extra, op) {
		return true, hostops.ReasonNone
	}
	return ok, why
}

func (p pendingAuthority) Snapshot(addonID string) hostops.EffectiveSet {
	set := p.Authority.Snapshot(addonID)
	if addonID == p.addonID {
		set.Operations = append(slices.Clone(set.Operations), p.extra...)
		slices.Sort(set.Operations)
	}
	return set
}

func (p pendingAuthority) Entitled(addonID string) bool {
	return p.Authority.Entitled(addonID) || (addonID == p.addonID && len(p.extra) > 0)
}

// pendingFor returns the Authority admission uses for an add-on: the real one
// plus the grants of the operation in flight for that add-on.
func (m *Manager) pendingFor(id string) hostops.Authority {
	base := m.hostAuthority()
	m.grantsMu.Lock()
	extra := m.pending[id]
	m.grantsMu.Unlock()
	if len(extra) == 0 {
		return base
	}
	return pendingAuthority{Authority: base, addonID: id, extra: extra}
}

func (m *Manager) setPending(id string, ops []string) {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	if m.pending == nil {
		m.pending = map[string][]string{}
	}
	if len(ops) == 0 {
		delete(m.pending, id)
		return
	}
	m.pending[id] = slices.Clone(ops)
}

// approvedGrant decides what an install or upgrade grants: the approved
// operations, cut to those the verified manifest declares, the host registers,
// no rule denies and the add-on does not already hold. The blanket switch adds
// its operations by the same cut. It also returns the summary of what the add-on
// would still miss, for the refusal.
func (m *Manager) approvedGrant(ctx context.Context, cmd LifecycleCommand, r packages.PackageReceipt) (grant []string, blanket string, err error) {
	m.setPending(r.Manifest.ID, nil)
	s := m.requests(r)
	declared := append(slices.Clone(s.Required), s.Optional...)
	// A name that is held, denied or unregistered cannot be granted. Missing
	// holds exactly the declared, registered names that the add-on lacks.
	grantable := func(names []string) []string {
		var out []string
		for _, n := range names {
			if !slices.Contains(declared, n) || !slices.Contains(hostops.SupportedOperations(), n) {
				continue
			}
			missing := slices.Contains(s.MissingRequired, n) || slices.Contains(s.MissingOptional, n)
			denied := slices.Contains(s.DeniedRequired, n) || slices.Contains(s.DeniedOptional, n)
			if missing && !denied && !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
		slices.Sort(out)
		return out
	}
	store := m.grantStore()
	if store != nil && cmd.Approval == nil {
		// The grants name the add-on ID and the storage, not the package. A
		// package from another library or signer must not inherit them without
		// a person looking at it: it is held like an upgrade that asks for more.
		if prev, ok := currentReceipt(m.journal.Addons[r.Manifest.ID]); ok && (prev.SourceID != r.SourceID || prev.Verification != r.Verification) {
			return nil, "", &HostRequirementsRefusal{Summary: s, SourceChanged: true}
		}
	}
	if cmd.Approval != nil {
		if cmd.Approval.Digest != r.Digest {
			return nil, "", &ApprovalStaleError{Digest: r.Digest, Summary: s}
		}
		if store != nil {
			grant = grantable(cmd.Approval.Ops)
		}
	} else if store != nil {
		q := BlanketQuery{Receipt: r}
		q.Required = grantable(s.Required)
		q.Optional = grantable(s.Optional)
		if len(q.Required)+len(q.Optional) > 0 {
			ops, name := store.Blanket(ctx, q)
			grant = grantable(ops)
			blanket = name
		}
	}
	m.setPending(r.Manifest.ID, grant)
	if refusal := m.admit(r); refusal != nil {
		m.setPending(r.Manifest.ID, nil)
		return nil, "", refusal
	}
	return grant, blanket, nil
}

func (m *Manager) stageGrants(ctx context.Context, cmd LifecycleCommand, actor contract.Actor, r packages.PackageReceipt, next Status, grant []string, blanket string) error {
	store := m.grantStore()
	if store == nil || len(grant) == 0 {
		return nil
	}
	e := store.Stage(ctx, GrantStage{
		AddonID: cmd.AddonID, Identity: next.Identity, Generation: next.Generation + 1,
		OperationID: cmd.OperationID, Action: cmd.Action, Version: r.Manifest.Version, Digest: r.Digest, SourceID: r.SourceID,
		Ops: grant, ExpectedRevision: expectedRevision(cmd), Blanket: blanket, Actor: actor,
	})
	if errors.Is(e, ErrGrantsConflict) {
		return &ApprovalStaleError{Digest: r.Digest, Summary: m.requests(r)}
	}
	if e != nil {
		return fmt.Errorf("record host grants: %w", e)
	}
	return nil
}

func expectedRevision(cmd LifecycleCommand) uint64 {
	if cmd.Approval == nil {
		return 0
	}
	return cmd.Approval.Revision
}

// removeGrants deletes the grants of an uninstalled add-on. The data stays
// unless the administrator purges it, and then only a hint of the grants stays.
//
// It runs before the journal commits the uninstall, and an error stops the
// uninstall: grants that cannot be deleted must not stay for the next package
// installed under the same ID. A crash after the delete leaves an installed
// add-on without grants, which is the safe side.
func (m *Manager) removeGrants(id string) error {
	store := m.grantStore()
	if store == nil {
		return nil
	}
	keep := m.recovery != nil && m.recovery.HasData(id)
	if e := store.Remove(context.Background(), id, keep); e != nil {
		xlog.Warn("Could not delete the host grants of an add-on to uninstall", "addon", id, "error", e)
		return e
	}
	return nil
}

// ReconcileGrants lets the store drop what no longer matches the journal: the
// grants of add-ons that are not installed, and pending entries of operations
// that did not complete. Call it once the journal is loaded.
func (m *Manager) ReconcileGrants(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reconcileGrantsLocked(ctx)
}

// reconcileGrantsLocked is ReconcileGrants for a caller that holds mu.
func (m *Manager) reconcileGrantsLocked(ctx context.Context) error {
	store := m.grantStore()
	if store == nil {
		return nil
	}
	installed := map[string]string{}
	for id, st := range m.journal.Addons {
		if st.Installed {
			installed[id] = st.Identity
		}
	}
	return store.Reconcile(ctx, installed, m.OperationCompleted)
}

// HostGrantsChanged tells the manager that the grants or the deny rules
// changed. An add-on that runs without an operation it requires is stopped, and
// one that was refused for a missing operation gets another try. A running call
// is not interrupted.
func (m *Manager) HostGrantsChanged(ctx context.Context) error {
	_, _, e := m.HostGrantsChangedReport(ctx)
	return e
}

// HostGrantsChangedReport is HostGrantsChanged that also says which add-ons
// the change started and which it stopped, so the owner can record it.
func (m *Manager) HostGrantsChangedReport(ctx context.Context) (started, stopped []string, err error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return nil, nil, e
	}
	if m.uncertain || m.runtimeRecoveryRequired || m.runtime == nil {
		return nil, nil, nil
	}
	before := map[string]bool{}
	for id := range m.instances {
		before[id] = true
	}
	for id, failure := range m.startFailed {
		var refusal *HostRequirementsRefusal
		if errors.As(failure, &refusal) {
			delete(m.startFailed, id)
		}
	}
	err = m.syncRuntime(ctx, "")
	for id := range m.instances {
		if !before[id] {
			started = append(started, id)
		}
	}
	for id := range before {
		if _, ok := m.instances[id]; !ok {
			stopped = append(stopped, id)
		}
	}
	slices.Sort(started)
	slices.Sort(stopped)
	return started, stopped, err
}

// endGrants runs when an install or upgrade that staged grants returns. If the
// operation did not reach the point the journal commits, the pending entry is
// removed. A canceled upgrade that the same operation ID continues keeps it.
func (m *Manager) endGrants(operationID, addonID string) {
	m.setPending(addonID, nil)
	phase := m.journal.Operations[operationID].Status.Phase
	if phase == string(lifecyclePrepared) || phase == string(lifecycleApplied) || phase == string(lifecycleCompleted) {
		return
	}
	if _, again := m.canceled[operationID]; again {
		return
	}
	if store := m.grantStore(); store != nil {
		if e := store.Abort(context.Background(), operationID); e != nil {
			xlog.Warn("Could not remove the pending host grants of a failed operation", "operationId", operationID, "error", e)
		}
	}
}
